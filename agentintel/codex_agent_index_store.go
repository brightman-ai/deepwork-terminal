package agentintel

// Persistence for the Codex subagent index.
//
// Everything the index holds is derived from rollouts already read, and nearly all of those
// are finished threads that will never be appended to again. Keeping it only in memory meant
// every process start re-derived it from scratch: 605 rollouts inspected, 284 subagent files
// read end to end — 1.9 GB, ~8 s — and any report request landing in that window queued
// behind it. Recording what has been read makes a restart cost the appended bytes only.
//
// The cache is a CACHE. Losing, corrupting or rejecting it costs one slow start and nothing
// else, so every failure path here is a silent fall-through to the from-scratch behaviour.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func hashPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:8])
}

// codexAgentIndexSchema versions the persisted shape AND the meaning of what was read. Bump
// it when the rollout parsing changes what a cursor or a status means, not only when this
// file's fields move — a cursor restored under different semantics is worse than no cursor.
const codexAgentIndexSchema = "codex-agent-index.v1"

// codexAgentIndexSaveInterval bounds how often the index is written. update() runs about
// once a second per live pane; the cache is ~200 KB and near-always unchanged in that span.
const codexAgentIndexSaveInterval = 15 * time.Second

// agentIndexCacheDir is where the Codex subagent index persists itself, or empty for "do
// not persist". It is package-level because the index registry it belongs to already is:
// one index per sessions root, shared by every driver watching that root.
//
// Optional by construction. Unset, the index behaves exactly as it did before it could be
// saved — correct, just slower on the first pass after a start.
var agentIndexCacheDir struct {
	sync.Mutex
	dir string
}

// SetAgentIndexCacheDir tells the Codex subagent index where it may persist what it has
// already read. Call it once, before drivers are constructed.
func SetAgentIndexCacheDir(dir string) {
	agentIndexCacheDir.Lock()
	defer agentIndexCacheDir.Unlock()
	agentIndexCacheDir.dir = dir
}

func codexAgentIndexPath(sessionsRoot string) string {
	agentIndexCacheDir.Lock()
	dir := agentIndexCacheDir.dir
	agentIndexCacheDir.Unlock()
	if dir == "" {
		return ""
	}
	// One cache per sessions root, named by the root so two roots never overwrite each other.
	return filepath.Join(dir, "codex-agent-index."+hashPath(sessionsRoot)+".json")
}

type persistedCodexAgentIndex struct {
	Schema       string                    `json:"schema"`
	SessionsRoot string                    `json:"sessions_root"`
	Seen         []string                  `json:"seen"`
	Files        []persistedCodexAgentFile `json:"files"`
	SpawnCursors map[string]int64          `json:"spawn_cursors"`
	Descriptions map[string]string         `json:"descriptions"`
}

type persistedCodexAgentFile struct {
	Path           string          `json:"path"`
	Offset         int64           `json:"offset"`
	ID             string          `json:"id"`
	ParentThreadID string          `json:"parent_thread_id"`
	Depth          int             `json:"depth"`
	AgentPath      string          `json:"agent_path"`
	Nickname       string          `json:"nickname"`
	Role           string          `json:"role"`
	CreatedAt      time.Time       `json:"created_at"`
	Status         AgentNodeStatus `json:"status"`
	Started        bool            `json:"started"`
	EndedAt        time.Time       `json:"ended_at"`
	LatestOut      int             `json:"latest_out"`
	Diagnostic     string          `json:"diagnostic"`
}

// loadLocked restores what a previous run had already read. Callers hold idx.mu.
//
// A restored cursor is only ever a claim about bytes ALREADY consumed, never about bytes to
// skip: JSONLReader resets itself when the file is shorter than the cursor, so a replaced or
// truncated rollout is re-read in full rather than silently half-parsed.
func (idx *codexAgentIndex) loadLocked() {
	path := codexAgentIndexPath(idx.sessionsRoot)
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var persisted persistedCodexAgentIndex
	if json.Unmarshal(body, &persisted) != nil ||
		persisted.Schema != codexAgentIndexSchema ||
		persisted.SessionsRoot != idx.sessionsRoot {
		return
	}
	for _, seen := range persisted.Seen {
		if _, err := os.Stat(seen); err == nil {
			idx.seen[seen] = struct{}{}
		}
	}
	for _, file := range persisted.Files {
		if _, err := os.Stat(file.Path); err != nil {
			continue // the rollout is gone; so is anything we knew about it
		}
		reader := NewJSONLReader(file.Path)
		reader.offset = file.Offset
		idx.files[file.Path] = &codexAgentFile{
			meta: codexSpawnMeta{
				ID: file.ID, ParentThreadID: file.ParentThreadID, Depth: file.Depth,
				AgentPath: file.AgentPath, Nickname: file.Nickname, Role: file.Role,
				CreatedAt: file.CreatedAt, Path: file.Path,
			},
			reader: reader, status: file.Status, started: file.Started,
			endedAt: file.EndedAt, latestOut: file.LatestOut, diagnostic: file.Diagnostic,
		}
		idx.seen[file.Path] = struct{}{}
	}
	for spawnPath, offset := range persisted.SpawnCursors {
		if _, err := os.Stat(spawnPath); err != nil {
			continue
		}
		reader := NewJSONLReader(spawnPath)
		reader.offset = offset
		idx.spawnReaders[spawnPath] = reader
	}
	for key, description := range persisted.Descriptions {
		idx.descriptions[key] = description
	}
}

// saveLocked writes the index at most once per codexAgentIndexSaveInterval. Callers hold idx.mu.
func (idx *codexAgentIndex) saveLocked(now time.Time) {
	path := codexAgentIndexPath(idx.sessionsRoot)
	if path == "" || now.Sub(idx.lastSave) < codexAgentIndexSaveInterval {
		return
	}
	idx.lastSave = now

	persisted := persistedCodexAgentIndex{
		Schema: codexAgentIndexSchema, SessionsRoot: idx.sessionsRoot,
		Seen:         make([]string, 0, len(idx.seen)),
		Files:        make([]persistedCodexAgentFile, 0, len(idx.files)),
		SpawnCursors: make(map[string]int64, len(idx.spawnReaders)),
		Descriptions: idx.descriptions,
	}
	for seen := range idx.seen {
		persisted.Seen = append(persisted.Seen, seen)
	}
	for _, file := range idx.files {
		persisted.Files = append(persisted.Files, persistedCodexAgentFile{
			Path: file.meta.Path, Offset: file.reader.Offset(),
			ID: file.meta.ID, ParentThreadID: file.meta.ParentThreadID, Depth: file.meta.Depth,
			AgentPath: file.meta.AgentPath, Nickname: file.meta.Nickname, Role: file.meta.Role,
			CreatedAt: file.meta.CreatedAt, Status: file.status, Started: file.started,
			EndedAt: file.endedAt, LatestOut: file.latestOut, Diagnostic: file.diagnostic,
		})
	}
	for spawnPath, reader := range idx.spawnReaders {
		persisted.SpawnCursors[spawnPath] = reader.Offset()
	}
	body, err := json.Marshal(persisted)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, body, 0o600) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}
