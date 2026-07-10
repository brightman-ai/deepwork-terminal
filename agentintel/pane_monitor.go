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
	driver    paneDriver // cached incremental status driver (rebuilt when path changes)
}

// paneDriver is the minimal incremental-status surface (ClaudeDriver / CodexDriver):
// Update() parses only NEW transcript lines, so re-reading status each poll is cheap.
type paneDriver interface {
	Update() error
	AgentState() AgentState
}

func newPaneDriver(path string, tool AgentTool) paneDriver {
	switch tool {
	case ToolClaude:
		return NewClaudeDriver(path, "")
	case ToolCodex:
		return NewCodexDriver(path)
	}
	return nil
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
	m.mu.Lock()
	path := m.entryLocked(key, cwd, tool).path
	m.mu.Unlock()
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

// Status returns the JSONL-derived agent status for a pane via a cached INCREMENTAL
// driver (each poll parses only new transcript lines). This is the accurate signal —
// it knows the pending tool's NAME, so an AskUserQuestion reads as waiting-for-the-
// user and a Bash/Read reads as executing=running, where a mtime/terminal heuristic
// cannot tell them apart. ok is false when the transcript isn't locatable yet (the
// caller then falls back to the terminal read).
func (m *PaneAgentMonitor) Status(key, cwd string, tool AgentTool) (AgentStatus, bool) {
	if m == nil || tool == ToolNone || key == "" {
		return StatusNone, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pt := m.entryLocked(key, cwd, tool)
	if pt.path == "" {
		return StatusNone, false
	}
	if pt.driver == nil {
		pt.driver = newPaneDriver(pt.path, tool)
	}
	if pt.driver == nil {
		return StatusNone, false
	}
	if err := pt.driver.Update(); err != nil {
		return StatusNone, false
	}
	st := pt.driver.AgentState().Status
	return st, st != ""
}

// Awaiting reports whether the pane's agent finished a turn and is waiting on the
// user (needs-you). Cheap: reuses the driver already updated by Status() this poll,
// so call it right after Status() with no extra transcript read.
func (m *PaneAgentMonitor) Awaiting(key, cwd string, tool AgentTool) bool {
	if m == nil || tool == ToolNone || key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pt := m.cache[key]
	if pt == nil || pt.driver == nil {
		return false
	}
	return pt.driver.AgentState().AwaitingUser
}

// AwaitingSince returns the transcript timestamp of the turn-completion behind the pane's
// current needs-you state (zero when not awaiting or the driver isn't cached). Reuses the
// driver already updated by Status() this poll — no extra transcript read. The frontend keys
// its reload-proof "seen" layer on this. Call right after Status()/Awaiting().
func (m *PaneAgentMonitor) AwaitingSince(key string) time.Time {
	if m == nil || key == "" {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pt := m.cache[key]
	if pt == nil || pt.driver == nil {
		return time.Time{}
	}
	return pt.driver.AgentState().AwaitingSince
}

// entryLocked returns the pane's cache entry, (re)locating the transcript path
// periodically (a path change drops the now-stale driver). m.mu MUST be held.
func (m *PaneAgentMonitor) entryLocked(key, cwd string, tool AgentTool) *paneTranscript {
	pt, ok := m.cache[key]
	if ok && time.Since(pt.locatedAt) < pathRelocateAfter && pt.path != "" {
		return pt
	}
	resolved := m.locate(cwd, tool)
	if pt == nil {
		pt = &paneTranscript{}
		m.cache[key] = pt
	}
	if resolved != pt.path {
		pt.path = resolved
		pt.driver = nil // path changed → drop stale driver
	}
	pt.locatedAt = time.Now()
	return pt
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
