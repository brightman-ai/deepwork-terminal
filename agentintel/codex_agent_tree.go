package agentintel

// Codex subagent tree adapter.
//
// Codex multi-agent sessions are not embedded as Claude-style tool rows. Each
// child gets its own rollout whose FIRST subagent session_meta carries the hard
// graph identity:
//
//   source.subagent.thread_spawn.{parent_thread_id,depth,agent_path,...}
//
// The child's own event stream then carries task_started/task_complete (or
// turn_aborted) and cumulative token_count. This adapter indexes those rollout
// files and projects them onto AgentNode. The graph never relies on matching
// free-text descriptions.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const codexAgentDiscoveryInterval = time.Second
const codexAgentIndexMaxFiles = 50_000

type codexSpawnMeta struct {
	ID             string
	ParentThreadID string
	Depth          int
	AgentPath      string
	Nickname       string
	Role           string
	CreatedAt      time.Time
	Path           string
}

type codexAgentFile struct {
	meta       codexSpawnMeta
	reader     *JSONLReader
	status     AgentNodeStatus
	started    bool
	endedAt    time.Time
	latestOut  int
	diagnostic string
}

type codexAgentIndex struct {
	mu            sync.Mutex
	sessionsRoot  string
	lastDiscovery time.Time
	files         map[string]*codexAgentFile // rollout path → incremental state
	seen          map[string]struct{}        // every inspected rollout, including non-subagents
	spawnReaders  map[string]*JSONLReader    // root/child path → incremental spawn parser
	descriptions  map[string]string          // parent thread + canonical agent path → spawn message
	lastScanError bool
}

type codexAgentTree struct {
	rootPath     string
	rootThreadID string
	rootActive   bool
	rootEndedAt  time.Time
	index        *codexAgentIndex
}

var codexIndexRegistry struct {
	sync.Mutex
	byRoot map[string]*codexAgentIndex
}

func sharedCodexAgentIndex(sessionsRoot string) *codexAgentIndex {
	codexIndexRegistry.Lock()
	defer codexIndexRegistry.Unlock()
	if codexIndexRegistry.byRoot == nil {
		codexIndexRegistry.byRoot = make(map[string]*codexAgentIndex)
	}
	if idx := codexIndexRegistry.byRoot[sessionsRoot]; idx != nil {
		return idx
	}
	idx := &codexAgentIndex{
		sessionsRoot: sessionsRoot,
		files:        make(map[string]*codexAgentFile), seen: make(map[string]struct{}),
		spawnReaders: make(map[string]*JSONLReader), descriptions: make(map[string]string),
	}
	codexIndexRegistry.byRoot[sessionsRoot] = idx
	return idx
}

func newCodexAgentTree(rootPath string) codexAgentTree {
	return codexAgentTree{
		rootPath: rootPath,
		index:    sharedCodexAgentIndex(codexSessionsRoot(rootPath)),
	}
}

func codexSessionsRoot(path string) string {
	d := filepath.Dir(path)
	for {
		if filepath.Base(d) == "sessions" {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			return filepath.Dir(path)
		}
		d = p
	}
}

func (t *codexAgentTree) update(rootThreadID string, rootActive bool, rootEndedAt time.Time) {
	if strings.TrimSpace(rootThreadID) != "" {
		t.rootThreadID = strings.TrimSpace(rootThreadID)
	}
	t.rootActive = rootActive
	t.rootEndedAt = rootEndedAt
	if t.rootThreadID == "" || t.index == nil || t.index.sessionsRoot == "" {
		return
	}
	idx := t.index
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Root/parent rollouts own spawn task descriptions. Incremental parsing is
	// cheap; duplicated fork history is idempotent because the map key is path.
	idx.scanSpawnDescriptions(t.rootPath, t.rootThreadID)

	now := time.Now()
	if idx.lastDiscovery.IsZero() || now.Sub(idx.lastDiscovery) >= codexAgentDiscoveryInterval {
		idx.discover()
		idx.lastDiscovery = now
	}
	for _, f := range idx.files {
		idx.scanSpawnDescriptions(f.meta.Path, f.meta.ID)
		f.update()
	}
}

func (idx *codexAgentIndex) discover() {
	idx.lastScanError = false
	err := filepath.WalkDir(idx.sessionsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			idx.lastScanError = true
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if _, ok := idx.seen[path]; ok {
			return nil
		}
		meta, ok, recognizable := readCodexSpawnMeta(path)
		if recognizable {
			idx.seen[path] = struct{}{}
		}
		if !ok {
			return nil
		}
		idx.files[path] = &codexAgentFile{
			meta: meta, reader: NewJSONLReader(path), status: AgentUnknown,
			diagnostic: "complete",
		}
		return nil
	})
	if err != nil {
		idx.lastScanError = true
	}
	if len(idx.files) > codexAgentIndexMaxFiles {
		all := make([]*codexAgentFile, 0, len(idx.files))
		for _, f := range idx.files {
			all = append(all, f)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].meta.CreatedAt.After(all[j].meta.CreatedAt) })
		for _, f := range all[codexAgentIndexMaxFiles:] {
			delete(idx.files, f.meta.Path)
			delete(idx.spawnReaders, f.meta.Path)
		}
		idx.lastScanError = true // retained nodes advertise a partial source snapshot
	}
}

func readCodexSpawnMeta(path string) (codexSpawnMeta, bool, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexSpawnMeta{}, false, false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	recognizable := false
	for lines := 0; lines < 32 && s.Scan(); lines++ {
		var row map[string]any
		if json.Unmarshal(s.Bytes(), &row) != nil || stringValue(row["type"]) != "session_meta" {
			continue
		}
		payload, _ := row["payload"].(map[string]any)
		recognizable = true
		source, _ := payload["source"].(map[string]any)
		subagent, _ := source["subagent"].(map[string]any)
		spawn, _ := subagent["thread_spawn"].(map[string]any)
		parent := firstString(spawn["parent_thread_id"], payload["parent_thread_id"])
		id := firstString(payload["id"], payload["agent_thread_id"])
		if id == "" || parent == "" {
			continue
		}
		return codexSpawnMeta{
			ID: id, ParentThreadID: parent,
			Depth:     intFromAny(firstNonNil(spawn["depth"], payload["depth"])),
			AgentPath: firstString(spawn["agent_path"], payload["agent_path"]),
			Nickname:  firstString(spawn["agent_nickname"], payload["agent_nickname"]),
			Role:      firstString(spawn["agent_role"], payload["agent_role"]),
			CreatedAt: parseTime(row), Path: path,
		}, true, true
	}
	return codexSpawnMeta{}, false, recognizable
}

func (idx *codexAgentIndex) scanSpawnDescriptions(path, ownerThreadID string) {
	// Description extraction is presentation enrichment, not graph identity. A
	// Per-file cursors make the steady-state cost O(new rows), including long-lived
	// roots and nested parents. Description is presentation enrichment only; graph
	// identity never depends on this optional text.
	r := idx.spawnReaders[path]
	if r == nil {
		r = NewJSONLReader(path)
		idx.spawnReaders[path] = r
	}
	_ = r.ReadNewFunc(func(row map[string]any) bool {
		if stringValue(row["type"]) != "response_item" {
			return true
		}
		payload, _ := row["payload"].(map[string]any)
		if stringValue(payload["type"]) != "function_call" || stringValue(payload["name"]) != "spawn_agent" {
			return true
		}
		var args map[string]any
		if json.Unmarshal([]byte(stringValue(payload["arguments"])), &args) != nil {
			return true
		}
		name := normalizeAgentPath(stringValue(args["task_name"]))
		if name == "" {
			return true
		}
		desc := strings.TrimSpace(stringValue(args["message"]))
		if desc != "" {
			idx.descriptions[codexSpawnKey(ownerThreadID, name)] = desc
		}
		return true
	})
}

func (f *codexAgentFile) update() {
	err := f.reader.ReadNewFunc(func(row map[string]any) bool {
		if stringValue(row["type"]) != "event_msg" {
			return true
		}
		payload, _ := row["payload"].(map[string]any)
		switch stringValue(payload["type"]) {
		case "task_started":
			f.started = true
			f.status = AgentRunning
			f.endedAt = time.Time{}
		case "task_complete":
			f.started = true
			f.status = AgentDone
			f.endedAt = parseTime(row)
		case "turn_aborted", "task_failed":
			f.started = true
			f.status = AgentError
			f.endedAt = parseTime(row)
		case "token_count":
			info, _ := payload["info"].(map[string]any)
			usage, _ := info["total_token_usage"].(map[string]any)
			f.latestOut = intFromAny(firstNonNil(usage["output_tokens"], usage["output"]))
		}
		return true
	})
	if err != nil {
		f.diagnostic = "parse-error"
	}
}

func (t *codexAgentTree) nodes() []AgentNode {
	if t.rootThreadID == "" || t.index == nil {
		return nil
	}
	idx := t.index
	idx.mu.Lock()
	defer idx.mu.Unlock()
	// Only descendants of this root belong in its tree. BFS prevents an unrelated
	// Codex thread in the same sessions directory from leaking into the dock.
	children := make(map[string][]*codexAgentFile)
	for _, f := range idx.files {
		children[f.meta.ParentThreadID] = append(children[f.meta.ParentThreadID], f)
	}
	for _, list := range children {
		sort.SliceStable(list, func(i, j int) bool { return list[i].meta.CreatedAt.Before(list[j].meta.CreatedAt) })
	}

	var out []AgentNode
	var walk func(parentThreadID, parentNodeID string)
	walk = func(parentThreadID, parentNodeID string) {
		for _, f := range children[parentThreadID] {
			desc := strings.TrimSpace(idx.descriptions[codexSpawnKey(f.meta.ParentThreadID, normalizeAgentPath(f.meta.AgentPath))])
			if desc == "" {
				desc = firstString(f.meta.Nickname, strings.TrimPrefix(f.meta.AgentPath, "/root/"), f.meta.AgentPath)
			}
			kind := firstString(f.meta.Role, "codex")
			diagnostic := f.diagnostic
			if diagnostic == "" {
				diagnostic = "complete"
			}
			if idx.lastScanError && diagnostic == "complete" {
				diagnostic = "partial"
			}
			status := f.status
			endedAt := f.endedAt
			if status == AgentRunning && !t.rootActive && !t.rootEndedAt.IsZero() {
				// The authoritative root rollout has ended but the child source has no
				// terminal event. Preserve uncertainty; never advertise immortal
				// "running" and never invent success from a timeout.
				status = AgentUnknown
				endedAt = t.rootEndedAt
				diagnostic = "source-ended"
			}
			node := AgentNode{
				ID: f.meta.ID, ParentID: parentNodeID, Depth: maxInt(1, f.meta.Depth),
				SubagentType: kind, Description: desc, Status: status,
				StartedAt: f.meta.CreatedAt, ActiveSince: f.meta.CreatedAt, EndedAt: endedAt, TokensDown: f.latestOut,
				Runtime: "codex", SourceRef: f.meta.Path, Diagnostic: diagnostic,
			}
			if f.started && !f.meta.CreatedAt.IsZero() {
				node.Attempts = []AgentAttempt{{Sequence: 1, StartedAt: f.meta.CreatedAt, EndedAt: endedAt, Status: status}}
			}
			out = append(out, node)
			walk(f.meta.ID, f.meta.ID)
		}
	}
	walk(t.rootThreadID, "")
	return out
}

func normalizeAgentPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "/") {
		return s
	}
	return "/root/" + strings.TrimPrefix(s, "root/")
}

func codexSpawnKey(parentThreadID, agentPath string) string {
	// task_name is local to its parent ("nested"), while child metadata carries
	// the canonical path ("/root/reviewer/nested"). parent thread + final path
	// segment is therefore the stable join; concurrent siblings must already have
	// distinct task_name values in the collaboration protocol.
	return strings.TrimSpace(parentThreadID) + "\x00" + filepath.Base(normalizeAgentPath(agentPath))
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func firstString(vs ...any) string {
	for _, v := range vs {
		if s := strings.TrimSpace(stringValue(v)); s != "" {
			return s
		}
	}
	return ""
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}
