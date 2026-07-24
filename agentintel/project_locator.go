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
	"time"

	"github.com/brightman-ai/kit/transcript"
)

// ProjectLocator maps CWD to Claude/Codex project directories.
//
// WHERE those directories live, how a project path is encoded into a claude shard, and how
// transcripts are enumerated newest-first are all answered by kit/transcript — the single
// resolver every layer shares. Resolving them independently here is how a CLAUDE_CONFIG_DIR
// user ends up with agent detection reading one ~/.claude while usage reads another.
type ProjectLocator struct{}

func NewProjectLocator() *ProjectLocator {
	return &ProjectLocator{}
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
	return transcript.NewestFiles(pl.CodexSessionDir(), "", transcript.JSONLSuffix, 0)
}

// CodexLatestSession finds the most recent ROOT Codex rollout for projectPath.
//
// It MUST use the recursive walk (CodexSessionFiles): a flat os.ReadDir of the base dir
// finds only the year DIRECTORIES and zero .jsonl files — which made this always return
// ErrNotExist. That in turn left every Codex pane's transcript unlocatable, so
// PaneAgentMonitor.Active fell back to "assume busy" and the pane read as perpetually
// Running → the running→waiting transition that fires the turn-end push never happened →
// Codex sessions never notified.
func (pl *ProjectLocator) CodexLatestSession(projectPath string) (string, error) {
	wantCWD := canonicalPath(projectPath)
	for _, path := range pl.CodexSessionFiles() {
		meta, ok := readCodexRootSessionMeta(path)
		if !ok {
			continue
		}
		if wantCWD == "" || canonicalPath(meta.CWD) == wantCWD {
			return path, nil
		}
	}
	return "", os.ErrNotExist
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
	ID  string
	CWD string
}

func readCodexRootSessionMeta(path string) (codexRootMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexRootMeta{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
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
	return codexRootMeta{ID: row.Payload.ID, CWD: row.Payload.CWD}, true
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
		if !ok || (wantCWD != "" && canonicalPath(meta.CWD) != wantCWD) {
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
