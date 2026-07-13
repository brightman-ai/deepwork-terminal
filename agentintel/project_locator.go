package agentintel

import (
	"os"
	"path/filepath"

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

// CodexLatestSession finds the most recent Codex rollout JSONL.
//
// It MUST use the recursive walk (CodexSessionFiles): a flat os.ReadDir of the base dir
// finds only the year DIRECTORIES and zero .jsonl files — which made this always return
// ErrNotExist. That in turn left every Codex pane's transcript unlocatable, so
// PaneAgentMonitor.Active fell back to "assume busy" and the pane read as perpetually
// Running → the running→waiting transition that fires the turn-end push never happened →
// Codex sessions never notified.
// (projectPath is not used yet — Codex rollouts aren't keyed by cwd; newest wins. Per-cwd
// matching would parse each rollout's session_meta.cwd, a future refinement.)
func (pl *ProjectLocator) CodexLatestSession(projectPath string) (string, error) {
	files := pl.CodexSessionFiles() // recursive, newest-first
	if len(files) == 0 {
		return "", os.ErrNotExist
	}
	return files[0], nil
}
