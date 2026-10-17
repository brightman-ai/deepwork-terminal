package agentintel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brightman-ai/kit/transcript"
)

// ProjectLocator maps CWD to Claude/Codex project directories.
//
// WHERE those directories live, how a project path is encoded into a claude shard, and how
// transcripts are enumerated newest-first are all answered by kit/transcript — the single
// resolver every layer shares. Resolving them independently here is how a CLAUDE_CONFIG_DIR
// user ends up with agent detection reading one ~/.claude while usage reads another.
type ProjectLocator struct {
	processEnv  func(int) ([]string, error)
	openRollout func(string) (*os.File, error)

	// Codex rollouts are immutable JSONL files as far as their head metadata is
	// concerned, but the body is appended to while a session is alive. Keep the
	// cheap directory/file stat work separate from the expensive head read, and
	// invalidate a head only when its path, size or mtime changes.
	codexMu   sync.Mutex
	codexRoot string
	codexDirs map[string]*codexDirCache
	codexMeta map[string]codexMetaCache
}

func NewProjectLocator() *ProjectLocator {
	return &ProjectLocator{processEnv: processEnvironment, openRollout: os.Open}
}

// ClaudeProjectDir returns the Claude JSONL directory for a project path.
// Claude Code encodes the project path by replacing '/' and '.' with '-'.
// Example: /Users/foo/proj → ~/.claude/projects/-Users-foo-proj/
// Important: resolves symlinks first (macOS /tmp → /private/tmp).
func (pl *ProjectLocator) ClaudeProjectDir(projectPath string) string {
	// Resolve symlinks: Claude Code uses the real path.
	if resolved, err := filepath.EvalSymlinks(projectPath); err == nil {
		projectPath = resolved
	}
	return filepath.Join(transcript.ClaudeProjectsRoot(), transcript.EncodeProjectDir(projectPath))
}

// ClaudeSessionFiles returns all .jsonl files in the project directory, newest first.
// A missing project dir stays an error: "this project was never opened in claude" is a
// different fact from "it has no sessions", and callers act on the difference.
func (pl *ProjectLocator) ClaudeSessionFiles(projectPath string) ([]string, error) {
	dir := pl.ClaudeProjectDir(projectPath)
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	return transcript.NewestFiles(dir, "", transcript.JSONLSuffix, 0), nil
}

// ClaudeProjectsRoot returns the root holding every claude project shard.
func (pl *ProjectLocator) ClaudeProjectsRoot() string {
	return transcript.ClaudeProjectsRoot()
}

// claudeSessionIndex is claude's own PID → session record, written at
// <ClaudeHome>/sessions/<pid>.json when the CLI starts. Only the fields we rely on are
// named; the file carries more (name, status, version) that is deliberately NOT read —
// status in particular, because a second opinion about whether an agent is busy is how
// two sources of truth start disagreeing. The transcript stays the authority on state;
// this file answers WHICH transcript, and nothing else.
type claudeSessionIndex struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

// ClaudeSessionForProcess maps a live claude PID to the transcript that process is writing.
//
// This exists because cwd is NOT an identity. A repo with two claude panes open (a main
// session and a helper, say) has two transcripts in one project shard, and picking among
// them by mtime — even while skipping the files siblings already claimed — decides ownership
// by whoever resolved first. Observed on a live machine: the helper pane bound the MAIN
// pane's transcript, inherited its interrupted turn, and sat green for ten hours while its
// own screen showed an idle prompt. Distinctness was never the missing property; correctness
// was.
//
// claude publishes the mapping itself, keyed by PID, which is exactly the identity a pane
// already has (tmux → pane_pid → the agent process). So this is a lookup, not a heuristic —
// the same shape CodexSessionForProcess has had all along, and the reason Codex panes never
// showed this bug.
//
// Every failure is a miss, never a wrong answer: a claude too old to write the file, a
// CLAUDE_CONFIG_DIR the caller cannot read, a session whose transcript has not appeared yet.
// The caller falls back to the cwd scan, which is where this started — no worse than before,
// and no pretending.
func (pl *ProjectLocator) ClaudeSessionForProcess(processPID int, projectPath string) (string, error) {
	if processPID <= 0 {
		return "", os.ErrNotExist
	}
	home, projects, processEnvKnown := pl.claudeRootsForProcess(processPID)
	if !processEnvKnown {
		// A PID-index record in the host profile is not proof that this process uses
		// that profile. Fall back to cwd-based transcript discovery instead of
		// binding an unrelated session when the OS hides the child's environment.
		return "", os.ErrNotExist
	}
	raw, err := os.ReadFile(filepath.Join(home, "sessions", strconv.Itoa(processPID)+".json"))
	if err != nil {
		return "", err
	}
	var idx claudeSessionIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return "", err
	}
	if idx.SessionID == "" {
		return "", os.ErrNotExist
	}
	// PID reuse is the one way this record can lie, and it is largely self-healing: every
	// claude writes this file at startup, so a live claude PID's record is that claude's own.
	// The residual case — the PID inherited by something that never rewrites it — is caught by
	// the two checks below: the recorded cwd must still be the pane's, and the transcript it
	// names must exist. A record that fails either is treated as absent.
	dir := idx.CWD
	if dir == "" {
		dir = projectPath
	} else if projectPath != "" && canonicalPath(dir) != canonicalPath(projectPath) {
		return "", os.ErrNotExist
	}
	path := filepath.Join(projects, transcript.EncodeProjectDir(canonicalPath(dir)), idx.SessionID+transcript.JSONLSuffix)
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

// An agent can use a different Claude profile from the terminal server. Read only
// directory settings from its process environment; credentials never leave this function.
func (pl *ProjectLocator) claudeRootsForProcess(pid int) (home, projects string, processEnvKnown bool) {
	home, projects = transcript.ClaudeHome(), transcript.ClaudeProjectsRoot()
	readEnvironment := pl.processEnv
	if readEnvironment == nil {
		readEnvironment = processEnvironment
	}
	environment, err := readEnvironment(pid)
	if err != nil {
		return home, projects, false
	}
	var config, userHome, override string
	for _, field := range environment {
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "CLAUDE_CONFIG_DIR":
			config = value
		case "HOME":
			userHome = value
		case "DW_CLAUDE_PROJECTS":
			override = value
		}
	}
	selected := config
	if selected == "" && filepath.IsAbs(userHome) {
		selected = filepath.Join(userHome, ".claude")
	}
	if filepath.IsAbs(selected) && filepath.Clean(selected) != filepath.Clean(home) {
		home, projects = selected, filepath.Join(selected, "projects")
	}
	if filepath.IsAbs(override) {
		projects = override
	}
	return home, projects, true
}

func (pl *ProjectLocator) ClaudeSessionFilesForProcess(pid int, cwd string) ([]string, error) {
	_, projects, _ := pl.claudeRootsForProcess(pid)
	dir := filepath.Join(projects, transcript.EncodeProjectDir(canonicalPath(cwd)))
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	return transcript.NewestFiles(dir, "", transcript.JSONLSuffix, 0), nil
}

// ClaudeAllSessionFiles returns every .jsonl across ALL Claude projects, newest first.
// Used by the cross-project input drawer. Missing root → empty slice.
func (pl *ProjectLocator) ClaudeAllSessionFiles() []string {
	return transcript.NewestFiles(pl.ClaudeProjectsRoot(), "", transcript.JSONLSuffix, 0)
}

// CodexSessionDir returns the Codex sessions base directory.
func (pl *ProjectLocator) CodexSessionDir() string {
	return transcript.CodexSessionsRoot()
}

// CodexSessionFiles returns every Codex rollout JSONL under the sessions root, newest first.
// Codex nests rollouts by date (sessions/YYYY/MM/DD/rollout-*.jsonl), so this MUST be a
// recursive walk — a flat read of the base dir finds only the year directories.
func (pl *ProjectLocator) CodexSessionFiles() []string {
	// Preserve the path spelling returned by the filesystem (callers and tests use
	// it as the transcript identity); canonicalization belongs to cwd comparisons.
	root := filepath.Clean(pl.CodexSessionDir())
	pl.codexMu.Lock()
	defer pl.codexMu.Unlock()
	if root == "" {
		return nil
	}
	if pl.codexRoot != root {
		pl.codexRoot = root
		pl.codexDirs = make(map[string]*codexDirCache)
		pl.codexMeta = make(map[string]codexMetaCache)
	}
	if pl.codexDirs == nil {
		pl.codexDirs = make(map[string]*codexDirCache)
	}
	pl.refreshCodexDirLocked(root)
	fileCapacity := 0
	for _, dir := range pl.codexDirs {
		fileCapacity += len(dir.files)
	}
	files := make([]codexFile, 0, fileCapacity)
	seenDirs := make(map[string]struct{})
	seenFiles := make(map[string]struct{})
	var collect func(string)
	collect = func(dir string) {
		d := pl.codexDirs[dir]
		if d == nil {
			return
		}
		seenDirs[dir] = struct{}{}
		files = append(files, d.files...)
		for _, file := range d.files {
			seenFiles[file.path] = struct{}{}
		}
		for _, child := range d.children {
			collect(child)
		}
	}
	collect(root)
	// Rollouts are normally retained, but cleanup/rotation can remove dated
	// directories. Drop cache entries that are no longer reachable so this
	// long-lived locator cannot grow with every historical file ever seen.
	for dir := range pl.codexDirs {
		if _, ok := seenDirs[dir]; !ok {
			delete(pl.codexDirs, dir)
		}
	}
	for path := range pl.codexMeta {
		if _, ok := seenFiles[path]; !ok {
			delete(pl.codexMeta, path)
		}
	}
	// Only today's (and the newest observed) dated directory can contain an
	// actively appended rollout. Older days are immutable; statting every
	// historical file on every notifier poll recreates the original syscall
	// storm. Directory mtime still discovers new dated files below.
	hotDirs := map[string]struct{}{
		filepath.Join(root, time.Now().Format("2006/01/02")):                    {},
		filepath.Join(root, time.Now().Add(-24*time.Hour).Format("2006/01/02")): {},
	}
	if len(files) > 0 {
		latest := files[0]
		for _, file := range files[1:] {
			if file.mod.After(latest.mod) {
				latest = file
			}
		}
		hotDirs[filepath.Dir(latest.path)] = struct{}{}
	}
	for i := range files {
		if _, hot := hotDirs[filepath.Dir(files[i].path)]; !hot {
			continue
		}
		if info, err := os.Stat(files[i].path); err == nil {
			files[i].mod = info.ModTime()
			files[i].size = info.Size()
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].mod.Equal(files[j].mod) {
			return files[i].path < files[j].path
		}
		return files[i].mod.After(files[j].mod)
	})
	out := make([]string, len(files))
	for i := range files {
		out[i] = files[i].path
	}
	return out
}

type codexFile struct {
	path string
	mod  time.Time
	size int64
}

type codexDirCache struct {
	mod      time.Time
	files    []codexFile
	children []string
}

// refreshCodexDirLocked refreshes only directories whose mtime changed. We still
// stat known descendants: appending a rollout changes the dated leaf directory,
// while adding a new date changes its parent. This catches both without walking
// unchanged subtrees on every polling tick.
func (pl *ProjectLocator) refreshCodexDirLocked(dir string) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		delete(pl.codexDirs, dir)
		return
	}
	entry := pl.codexDirs[dir]
	if entry == nil {
		entry = &codexDirCache{}
		pl.codexDirs[dir] = entry
	}
	if !entry.mod.Equal(info.ModTime()) {
		entry.mod = info.ModTime()
		entry.files = nil
		entry.children = nil
		if entries, readErr := os.ReadDir(dir); readErr == nil {
			for _, e := range entries {
				path := filepath.Join(dir, e.Name())
				if e.IsDir() {
					entry.children = append(entry.children, path)
					continue
				}
				if filepath.Ext(e.Name()) != transcript.JSONLSuffix {
					continue
				}
				if fi, statErr := e.Info(); statErr == nil {
					entry.files = append(entry.files, codexFile{path: path, mod: fi.ModTime(), size: fi.Size()})
				}
			}
		}
	}
	for _, child := range entry.children {
		pl.refreshCodexDirLocked(child)
	}
}

// CodexLatestSession finds the most recent ROOT Codex rollout for projectPath.
// A missing or non-matching rollout is returned as ("", nil), since unknown
// transcript identity is a normal state while a pane is starting or has exited.
//
// It uses CodexSessionFiles' recursive dated-directory index; a flat os.ReadDir of
// the base dir finds only year directories and zero .jsonl files.
func (pl *ProjectLocator) CodexLatestSession(projectPath string) (string, error) {
	wantCWD := canonicalPath(projectPath)
	for _, path := range pl.CodexSessionFiles() {
		meta, ok := pl.readCodexRootSessionMeta(path)
		if !ok {
			continue
		}
		if wantCWD == "" || meta.CanonicalCWD == wantCWD {
			return path, nil
		}
	}
	// An empty path is a normal unknown state. Callers use it to clear a stale
	// transcript; turning it into ErrNotExist made routine misses look like I/O
	// failures and incremented watcher error counters.
	return "", nil
}

// CodexSessionForProcess resolves the pane's hard runtime identity. Codex keeps
// its root rollout open for the life of the interactive process; reading that
// file descriptor set avoids cwd/latest-file ambiguity across concurrent panes.
func (pl *ProjectLocator) CodexSessionForProcess(processPID int, projectPath string) (string, error) {
	if processPID <= 0 {
		return "", os.ErrNotExist
	}
	return selectCodexRootRollout(openProcessFiles(processPID), projectPath)
}

type codexRootMeta struct {
	ID           string
	CWD          string
	CanonicalCWD string
}

type codexMetaCache struct {
	size int64
	mod  time.Time
	meta codexRootMeta
	ok   bool
}

// session_meta lines can carry a large base_instructions payload. Reusing the
// scanner's 64 KiB head buffer avoids allocating that buffer once per rollout
// while the locator builds its first metadata index.
var codexHeadBufferPool = sync.Pool{New: func() any { return make([]byte, 64*1024) }}

func (pl *ProjectLocator) readCodexRootSessionMeta(path string) (codexRootMeta, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return codexRootMeta{}, false
	}
	pl.codexMu.Lock()
	defer pl.codexMu.Unlock()
	if pl.codexMeta == nil {
		pl.codexMeta = make(map[string]codexMetaCache)
	}
	if cached, ok := pl.codexMeta[path]; ok && cached.size == info.Size() && cached.mod.Equal(info.ModTime()) {
		return cached.meta, cached.ok
	}
	open := pl.openRollout
	if open == nil {
		open = os.Open
	}
	meta, ok := readCodexRootSessionMetaWithOpen(path, open)
	pl.codexMeta[path] = codexMetaCache{size: info.Size(), mod: info.ModTime(), meta: meta, ok: ok}
	return meta, ok
}

func readCodexRootSessionMeta(path string) (codexRootMeta, bool) {
	return readCodexRootSessionMetaWithOpen(path, os.Open)
}

func readCodexRootSessionMetaWithOpen(path string, open func(string) (*os.File, error)) (codexRootMeta, bool) {
	f, err := open(path)
	if err != nil {
		return codexRootMeta{}, false
	}
	defer f.Close()
	buffer := codexHeadBufferPool.Get().([]byte)
	defer codexHeadBufferPool.Put(buffer)
	sc := bufio.NewScanner(f)
	sc.Buffer(buffer[:0], 4*1024*1024)
	if !sc.Scan() {
		return codexRootMeta{}, false
	}
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string          `json:"id"`
			CWD    string          `json:"cwd"`
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(sc.Bytes(), &row) != nil || row.Type != "session_meta" || row.Payload.ID == "" {
		return codexRootMeta{}, false
	}
	// Subagents have an object-valued source.subagent.thread_spawn. Root sources
	// are scalar values such as "cli". Unknown objects are not assumed to be roots.
	source := strings.TrimSpace(string(row.Payload.Source))
	if strings.HasPrefix(source, "{") {
		return codexRootMeta{}, false
	}
	return codexRootMeta{ID: row.Payload.ID, CWD: row.Payload.CWD, CanonicalCWD: canonicalPath(row.Payload.CWD)}, true
}

func selectCodexRootRollout(paths []string, projectPath string) (string, error) {
	wantRoot := canonicalPath(transcript.CodexSessionsRoot())
	wantCWD := canonicalPath(projectPath)
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, path := range paths {
		clean := canonicalPath(path)
		if filepath.Ext(clean) != transcript.JSONLSuffix || !pathWithin(clean, wantRoot) {
			continue
		}
		meta, ok := readCodexRootSessionMeta(clean)
		if !ok || (wantCWD != "" && meta.CanonicalCWD != wantCWD) {
			continue
		}
		info, _ := os.Stat(clean)
		var mod time.Time
		if info != nil {
			mod = info.ModTime()
		}
		candidates = append(candidates, candidate{path: clean, mod: mod})
	}
	if len(candidates) == 0 {
		return "", os.ErrNotExist
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	return candidates[0].path, nil
}

func openProcessFiles(pid int) []string {
	seen := make(map[string]struct{})
	var paths []string
	add := func(path string) {
		path = strings.TrimSuffix(strings.TrimSpace(path), " (deleted)")
		if path == "" || filepath.Ext(path) != transcript.JSONLSuffix {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd")); err == nil {
		for _, entry := range entries {
			if target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", entry.Name())); err == nil {
				add(target)
			}
		}
		// /proc 可读即为 fd 真相——零 jsonl 命中=该进程确实没开 rollout，也是答案。
		// 曾经空命中还掉进下面的 lsof 兜底：每轮 tmux 扫描 × 每个 codex pane 对忙进程
		// 反复 lsof（单次 100% CPU），生产实测并发风暴把 load 拉到 19。lsof 只属于
		// 无 /proc 的平台（macOS/BSD）。
		return paths
	}
	// macOS/BSD fallback. -Fn emits one filename per n-prefixed line and no
	// user-controlled formatting. A short timeout keeps topology polling bounded.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-Fn", "-p", fmt.Sprint(pid)).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "n/") {
				add(strings.TrimPrefix(line, "n"))
			}
		}
	}
	return paths
}

func canonicalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func pathWithin(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
