package agentintel

import (
	"os"
	"sync"
	"time"
)

// transcriptActiveWindow: a pane whose JSONL transcript was written within this window is actively
// producing output → it's working, so we skip the terminal read entirely. Past it the pane has
// stopped and we fall back to reading the terminal to tell "blocked on a prompt" from "done". Tuned
// below the notification latency the user accepts (~5s) so a real wait surfaces within a poll or two.
const transcriptActiveWindow = 3 * time.Second

// PaneAgentMonitor answers one cheap question per agent pane: is its JSONL TRANSCRIPT being written
// right now? That alone separates "actively working" (skip the terminal) from "stopped" (read the
// terminal to see if it's blocked on a prompt). The transcript path is located once per pane and
// cached — locating it scans a project dir (some hold hundreds of files), so we don't repeat it
// every poll; only the cheap os.Stat for the mtime runs each time.
//
// Status itself (running / waiting / idle) is NOT decided here: the permission/selection/input
// PROMPT is terminal UI, absent from the transcript, so it can only be read from the pane. This
// type is purely the gate that keeps that read off the hot path for busy panes.
type PaneAgentMonitor struct {
	locator *ProjectLocator

	mu    sync.Mutex
	cache map[string]*paneTranscript
}

type paneTranscript struct {
	path      string
	locatedAt time.Time
}

// pathRelocateAfter: re-resolve the latest transcript for a pane occasionally so a brand-new
// session file (newer than the cached one) is picked up, without a readdir on every poll.
const pathRelocateAfter = 20 * time.Second

func NewPaneAgentMonitor(locator *ProjectLocator) *PaneAgentMonitor {
	if locator == nil {
		locator = NewProjectLocator()
	}
	return &PaneAgentMonitor{locator: locator, cache: make(map[string]*paneTranscript)}
}

// Active reports whether the pane's transcript was written within transcriptActiveWindow — i.e. the
// agent is currently producing output. Unknown transcript (not locatable yet) counts as active so a
// freshly-seen agent pane is never wrongly read as a prompt before we know better.
func (m *PaneAgentMonitor) Active(key, cwd string, tool AgentTool) bool {
	if m == nil || tool == ToolNone || key == "" {
		return true
	}
	path := m.path(key, cwd, tool)
	if path == "" {
		return true // can't locate yet → assume busy, don't read the terminal as a prompt
	}
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) < transcriptActiveWindow
}

// Prune drops cache entries for panes no longer present. Call once per topology recompute.
func (m *PaneAgentMonitor) Prune(keep map[string]bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.cache {
		if !keep[key] {
			delete(m.cache, key)
		}
	}
}

// path returns the pane's latest transcript path, cached with periodic re-resolution.
func (m *PaneAgentMonitor) path(key, cwd string, tool AgentTool) string {
	m.mu.Lock()
	pt, ok := m.cache[key]
	m.mu.Unlock()
	if ok && time.Since(pt.locatedAt) < pathRelocateAfter && pt.path != "" {
		return pt.path
	}
	resolved := m.locate(cwd, tool)
	m.mu.Lock()
	m.cache[key] = &paneTranscript{path: resolved, locatedAt: time.Now()}
	m.mu.Unlock()
	return resolved
}

func (m *PaneAgentMonitor) locate(cwd string, tool AgentTool) string {
	switch tool {
	case ToolClaude:
		if files, err := m.locator.ClaudeSessionFiles(cwd); err == nil && len(files) > 0 {
			return files[0]
		}
	case ToolCodex:
		if p, err := m.locator.CodexLatestSession(cwd); err == nil {
			return p
		}
	}
	return ""
}
