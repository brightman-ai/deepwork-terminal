package agentintel

import (
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TmuxPrefix is the resolved tmux prefix key.
// Display is the human label (e.g. "C-b"); Bytes is the control byte(s) the
// client must send to emulate the prefix (e.g. C-b → 0x02, C-a → 0x01).
type TmuxPrefix struct {
	Display string `json:"display"`
	Bytes   []byte `json:"bytes"`
}

// TmuxPaneState is one pane within the topology, enriched with agent detection.
type TmuxPaneState struct {
	Index       int         `json:"index"`
	Active      bool        `json:"active"`
	Title       string      `json:"title"`
	PID         int         `json:"pid"`
	CWD         string      `json:"cwd"`
	PaneID      string      `json:"paneId,omitempty"` // stable tmux pane id ("%N")
	AgentTool   AgentTool   `json:"agentTool,omitempty"`
	AgentStatus AgentStatus `json:"agentStatus,omitempty"`
	// AwaitingUser: the agent completed a turn / is blocked and hasn't been responded
	// to — drives the "needs-you" dot. Distinct from AgentStatus==idle, which also
	// covers a fresh pane that never ran a turn (not awaiting).
	AwaitingUser bool `json:"awaitingUser,omitempty"`
}

// TmuxWindowState is one window with its panes.
type TmuxWindowState struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	// WindowID is tmux's stable "@N" id — survives index reuse/reorder, unlike Index. The Agent
	// Overview keys its per-window seen-state on it so a reused index can't inherit stale state.
	WindowID string          `json:"windowId,omitempty"`
	Active   bool            `json:"active"`
	Panes    []TmuxPaneState `json:"panes"`
	// Tail is the last few lines of this window's active pane, for the Agent Overview's
	// per-window live preview. Optional: absent when capture failed or is disabled.
	Tail []string `json:"tail,omitempty"`
}

// overviewTailLines caps how many trailing lines each window's Agent-Overview tail carries.
// The PC overview's active cards grow to fill the viewport, so the tail must carry enough real
// output to fill a tall card (not leave it padded/empty) — the card then bottom-aligns + clips
// to whatever height it actually gets. The whole screen is captured regardless (CaptureWindowTail),
// so this only widens the post-strip cap; it's still naturally bounded by the source pane's height.
const overviewTailLines = 40

// overviewTailTimeout bounds each per-window tail capture. It is well under tmuxCmdTimeout so N
// windows' tails can't monopolise the poll's budget or starve the status captures.
const overviewTailTimeout = 400 * time.Millisecond

// TmuxSessionState is one tmux session with its windows.
type TmuxSessionState struct {
	Name     string            `json:"name"`
	Attached bool              `json:"attached"`
	Windows  []TmuxWindowState `json:"windows"`
}

// TmuxState is the full tmux topology snapshot for a host process.
// It is designed to be cheap to recompute (~1s poll): prefix + installed are
// cached, and the topology comes from a single batched tmux format query plus
// one shared ps snapshot for per-pane agent detection.
type TmuxState struct {
	Installed     bool               `json:"installed"`
	ServerRunning bool               `json:"serverRunning"`
	Attached      bool               `json:"attached"`
	// AttachedSession is the tmux session name this shellPID's client is attached
	// to (empty when not attached). It scopes the pane bar to THIS session's
	// windows rather than any session that merely has a client somewhere.
	AttachedSession string             `json:"attachedSession"`
	Prefix          TmuxPrefix         `json:"prefix"`
	// ModeKeys is the resolved global `mode-keys` option ("vi" | "emacs"). It tells the
	// client which copy-mode key table is active, so a semantic copy-mode motion (e.g.
	// halfpage-up) can be mapped to the correct keystroke for THIS server — the SSOT for
	// "how to express copy-mode motions" shared by every connected client.
	ModeKeys string             `json:"modeKeys"`
	Sessions []TmuxSessionState `json:"sessions"`
}

// defaultPrefix is C-b (tmux default) used when prefix cannot be read.
var defaultPrefix = TmuxPrefix{Display: "C-b", Bytes: []byte{0x02}}

// defaultModeKeys is tmux's compiled default; tmux auto-switches to "vi" when
// $EDITOR/$VISUAL contains "vi" at server start. show-options reports the effective value.
const defaultModeKeys = "emacs"

const (
	tmuxInstalledTTL = 60 * time.Second
	tmuxPrefixTTL    = 10 * time.Second
	tmuxCmdTimeout   = 1500 * time.Millisecond
)

// TmuxStateService aggregates tmux topology + agent detection with light caching.
// It is safe for concurrent use. A nil receiver is never valid — use NewTmuxStateService.
type TmuxStateService struct {
	prober      *TmuxProber
	inspector   *ProcessInspector
	paneMonitor *PaneAgentMonitor

	// overviewActive gates the per-window tail capture: true only while some client has the Agent
	// Overview open (POST /tmux/overview). Off → the poll does zero extra capture-pane work.
	overviewActive atomic.Bool

	mu             sync.Mutex
	installed      bool
	installedAt    time.Time
	prefix         TmuxPrefix
	prefixAt       time.Time
	prefixResolved bool
	modeKeys         string
	modeKeysAt       time.Time
	modeKeysResolved bool
}

// NewTmuxStateService builds a service over the shared process inspector so it
// reuses the same ps snapshot as the rest of the package.
func NewTmuxStateService() *TmuxStateService {
	insp := SharedProcessInspector
	return &TmuxStateService{
		prober:      NewTmuxProber(insp),
		inspector:   insp,
		paneMonitor: NewPaneAgentMonitor(nil),
	}
}

// TmuxInstalled reports whether the tmux binary is available, cached for 60s.
func (s *TmuxStateService) TmuxInstalled() bool {
	s.mu.Lock()
	if !s.installedAt.IsZero() && time.Since(s.installedAt) < tmuxInstalledTTL {
		v := s.installed
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	_, err := exec.LookPath("tmux")
	installed := err == nil

	s.mu.Lock()
	s.installed = installed
	s.installedAt = time.Now()
	s.mu.Unlock()
	return installed
}

// Prefix returns the resolved tmux prefix, cached with a short TTL.
// Falls back to C-b when tmux is absent or the option is unreadable.
func (s *TmuxStateService) Prefix(ctx context.Context) TmuxPrefix {
	s.mu.Lock()
	if s.prefixResolved && time.Since(s.prefixAt) < tmuxPrefixTTL {
		p := s.prefix
		s.mu.Unlock()
		return p
	}
	s.mu.Unlock()

	p := s.resolvePrefix(ctx)

	s.mu.Lock()
	s.prefix = p
	s.prefixAt = time.Now()
	s.prefixResolved = true
	s.mu.Unlock()
	return p
}

func (s *TmuxStateService) resolvePrefix(ctx context.Context) TmuxPrefix {
	if !s.TmuxInstalled() {
		return defaultPrefix
	}
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	out, err := tmuxCommandContext(cctx, "show-options", "-g", "prefix").Output()
	if err != nil {
		return defaultPrefix
	}
	// Output form: "prefix C-b" (or "prefix C-a", "prefix M-x", ...).
	line := strings.TrimSpace(string(out))
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return defaultPrefix
	}
	return parsePrefix(fields[1])
}

// ModeKeys returns the resolved global mode-keys ("vi" | "emacs"), cached with the
// same short TTL as the prefix. Falls back to "emacs" when tmux is absent or unreadable.
func (s *TmuxStateService) ModeKeys(ctx context.Context) string {
	s.mu.Lock()
	if s.modeKeysResolved && time.Since(s.modeKeysAt) < tmuxPrefixTTL {
		v := s.modeKeys
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	v := s.resolveModeKeys(ctx)

	s.mu.Lock()
	s.modeKeys = v
	s.modeKeysAt = time.Now()
	s.modeKeysResolved = true
	s.mu.Unlock()
	return v
}

func (s *TmuxStateService) resolveModeKeys(ctx context.Context) string {
	if !s.TmuxInstalled() {
		return defaultModeKeys
	}
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	out, err := tmuxCommandContext(cctx, "show-options", "-g", "mode-keys").Output()
	if err != nil {
		return defaultModeKeys
	}
	// Output form: "mode-keys vi" (or "mode-keys emacs").
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return defaultModeKeys
	}
	if fields[1] == "vi" {
		return "vi"
	}
	return "emacs"
}

// parsePrefix converts a tmux key spec ("C-b", "C-a", "M-x", "F1") into a
// display label + the control byte(s) to emulate it. Only C-<letter> maps to a
// single control byte; anything else keeps its display but carries no bytes
// (the client then falls back to native key handling).
func parsePrefix(spec string) TmuxPrefix {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return defaultPrefix
	}
	display := spec
	var b []byte
	if len(spec) == 3 && (spec[0] == 'C' || spec[0] == 'c') && spec[1] == '-' {
		c := spec[2]
		// Ctrl-letter → control byte: C-a=0x01 ... C-z=0x1a.
		switch {
		case c >= 'a' && c <= 'z':
			b = []byte{c - 'a' + 1}
		case c >= 'A' && c <= 'Z':
			b = []byte{c - 'A' + 1}
		}
		// Normalize display to upper Ctrl form (C-b).
		display = "C-" + strings.ToLower(string(c))
	}
	if b == nil {
		// Unknown / non-control prefix: still report display, no emulation bytes.
		return TmuxPrefix{Display: display, Bytes: nil}
	}
	return TmuxPrefix{Display: display, Bytes: b}
}

// ServerRunning reports whether any tmux server is reachable for this process.
func (s *TmuxStateService) ServerRunning(ctx context.Context) bool {
	if !s.TmuxInstalled() {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	// list-sessions exits non-zero ("no server running") when no server exists.
	err := tmuxCommandContext(cctx, "list-sessions", "-F", "#{session_name}").Run()
	return err == nil
}

// Attached reports whether the shell identified by shellPID is running inside a
// tmux client (i.e. a tmux client process exists in its descendant tree).
func (s *TmuxStateService) Attached(ctx context.Context, shellPID int) bool {
	if shellPID <= 0 || !s.TmuxInstalled() {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	return s.prober.DetectTmux(cctx, shellPID)
}

// SetOverviewActive toggles per-window tail capture on/off — called when a client opens/closes
// the Agent Overview (via POST /tmux/overview). Off by default so tail costs nothing until asked.
func (s *TmuxStateService) SetOverviewActive(v bool) { s.overviewActive.Store(v) }

// State builds the full TmuxState snapshot. shellPID (optional, 0 to skip) is
// used to compute the Attached flag for the calling session's shell.
//
// It is non-blocking-friendly: every tmux/ps subprocess runs under a short
// context timeout, and a missing server degrades gracefully to an empty
// session list rather than an error.
func (s *TmuxStateService) State(ctx context.Context, shellPID int) TmuxState {
	st := TmuxState{
		Installed: s.TmuxInstalled(),
		Prefix:    s.Prefix(ctx),
		ModeKeys:  s.ModeKeys(ctx),
	}
	if !st.Installed {
		return st
	}

	st.ServerRunning = s.ServerRunning(ctx)
	if shellPID > 0 {
		st.Attached = s.Attached(ctx, shellPID)
		if st.Attached {
			cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
			st.AttachedSession = s.prober.FindClientSession(cctx, shellPID)
			cancel()
		}
	}
	if !st.ServerRunning {
		return st
	}

	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	panes, err := s.prober.ListPanes(cctx)
	if err != nil || len(panes) == 0 {
		return st
	}

	attachedSessions := s.attachedSessions(ctx)
	st.Sessions = s.buildSessions(cctx, panes, attachedSessions)
	return st
}

// attachedSessions returns the set of session names that currently have a
// client attached (from list-sessions #{session_attached}).
func (s *TmuxStateService) attachedSessions(ctx context.Context) map[string]bool {
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	out, err := tmuxCommandContext(cctx,
		"list-sessions", "-F", "#{session_name}"+tmuxFieldSep+"#{session_attached}",
	).Output()
	if err != nil {
		return nil
	}
	result := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, tmuxFieldSep, 2)
		if len(fields) != 2 {
			continue
		}
		n, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
		result[fields[0]] = n > 0
	}
	return result
}

// buildSessions groups panes into sessions → windows → panes and runs per-pane
// agent detection against a single shared ps snapshot.
func (s *TmuxStateService) buildSessions(ctx context.Context, panes []TmuxPane, attached map[string]bool) []TmuxSessionState {
	// agents: PID → tool, computed once over the shared ps snapshot.
	agents := s.prober.DetectAgentsInPanes(ctx, panes)
	// agentKeys: the JSONL-monitor keys for panes still hosting an agent this pass — used to prune
	// watchers for panes that went away.
	agentKeys := make(map[string]bool)

	type winKey struct {
		session string
		window  int
	}
	// winTool: the agent tool of each window's ACTIVE pane — the pane a bare "session:window"
	// tail capture targets. It drives per-agent chrome stripping of the overview tail; a window
	// whose active pane is a bare shell / non-agent maps to ToolNone → the tail is left raw.
	winTool := make(map[winKey]AgentTool)
	for _, p := range panes {
		if p.PaneActive {
			winTool[winKey{p.SessionName, p.WindowIndex}] = agents[p.PanePID]
		}
	}
	sessionOrder := []string{}
	sessionSeen := map[string]bool{}
	winOrder := map[string][]int{}
	winSeen := map[winKey]bool{}
	winMeta := map[winKey]TmuxWindowState{}
	winPanes := map[winKey][]TmuxPaneState{}

	for _, p := range panes {
		if !sessionSeen[p.SessionName] {
			sessionSeen[p.SessionName] = true
			sessionOrder = append(sessionOrder, p.SessionName)
		}
		wk := winKey{p.SessionName, p.WindowIndex}
		if !winSeen[wk] {
			winSeen[wk] = true
			winOrder[p.SessionName] = append(winOrder[p.SessionName], p.WindowIndex)
			m := TmuxWindowState{
				Index:    p.WindowIndex,
				Name:     p.WindowName,
				WindowID: p.WindowID,
				Active:   p.Active,
			}
			// Per-window live tail for the Agent Overview — captured ONLY while a client has the
			// overview open, so heads-down-in-one-terminal costs nothing. Bounded lines + a short
			// timeout so a slow window can't stall the poll. A bare session:window target captures
			// the window's active pane (background windows included, no switch needed).
			if s.overviewActive.Load() {
				tctx, tcancel := context.WithTimeout(ctx, overviewTailTimeout)
				if tail, terr := s.prober.CaptureWindowTail(tctx, p.SessionWindow, winTool[wk], overviewTailLines); terr == nil {
					m.Tail = tail
				}
				tcancel()
			}
			winMeta[wk] = m
		}

		ps := TmuxPaneState{
			Index:  p.PaneIndex,
			Active: p.Active,
			PID:    p.PanePID,
			CWD:    p.PaneCWD,
			PaneID: p.PaneID,
		}
		if tool, ok := agents[p.PanePID]; ok {
			ps.AgentTool = tool
			ps.AgentStatus = s.paneStatus(ctx, p, tool)
			// Needs-you: an explicit block (waiting) always counts; an idle pane counts
			// only if the driver says a turn actually completed (not fresh-idle). Awaiting()
			// reuses the driver Status() just updated, so no extra transcript read.
			ps.AwaitingUser = ps.AgentStatus == StatusWaiting ||
				(ps.AgentStatus == StatusIdle && s.paneMonitor.Awaiting(paneKey(p), p.PaneCWD, tool))
			agentKeys[paneKey(p)] = true
		}
		winPanes[wk] = append(winPanes[wk], ps)
	}

	sessions := make([]TmuxSessionState, 0, len(sessionOrder))
	for _, name := range sessionOrder {
		windows := make([]TmuxWindowState, 0, len(winOrder[name]))
		for _, wi := range winOrder[name] {
			wk := winKey{name, wi}
			w := winMeta[wk]
			ps := winPanes[wk]
			sort.Slice(ps, func(i, j int) bool { return ps[i].Index < ps[j].Index })
			w.Panes = ps
			windows = append(windows, w)
		}
		sort.Slice(windows, func(i, j int) bool { return windows[i].Index < windows[j].Index })
		sessions = append(sessions, TmuxSessionState{
			Name:     name,
			Attached: attached[name],
			Windows:  windows,
		})
	}
	// Drop JSONL watchers for panes that no longer host an agent (closed / agent exited).
	s.paneMonitor.Prune(agentKeys)
	return sessions
}

// paneKey is the stable per-pane id the transcript-freshness cache is keyed on (the pane's shell PID).
func paneKey(p TmuxPane) string {
	return strconv.Itoa(p.PanePID)
}

// paneStatus derives a pane's agent status with a JSONL-gated terminal read:
//   - transcript being written (PaneAgentMonitor.Active) → working → Running, WITHOUT touching the pane.
//   - transcript stopped → read the visible pane: a permission/selection/input PROMPT lives there
//     (never in the transcript), so AnalyzeOutput on it is the ground truth — needs-permission →
//     Waiting (the push trigger), a spinner → still Running, otherwise the turn is done → Idle.
//
// This keeps the (slightly brittle, version-coupled) prompt scrape OFF the hot path: it runs only
// for stopped panes, not every agent pane every poll — accurate where it matters, cheap otherwise.
func (s *TmuxStateService) paneStatus(ctx context.Context, p TmuxPane, tool AgentTool) AgentStatus {
	// Accurate JSONL-derived status: a turn's end is recorded in the transcript
	// (Claude end_turn / Codex task_complete → waiting/idle), a Bash/Read tool is
	// executing = running. This fixes the mtime heuristic's blind spots — a just-
	// written ask card looked "running", a silently-running long tool looked "idle",
	// and (Codex) a finished turn looked perpetually "running" because its rollout
	// was unlocatable. Both Claude and Codex carry turn boundaries in JSONL, so both
	// use the driver; a Running result is still confirmed against the pane for a
	// terminal-only permission prompt.
	if tool == ToolClaude || tool == ToolCodex {
		if st, ok := s.paneMonitor.Status(paneKey(p), p.PaneCWD, tool); ok {
			switch st {
			case StatusWaiting, StatusIdle:
				return st
			case StatusRunning:
				// A pending tool may instead be blocked on a permission [Y/n] — that prompt
				// is terminal UI, absent from the transcript — so confirm against the pane.
				cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
				lines, err := s.prober.CapturePane(cctx, p.SessionWindow, p.PaneIndex, 14)
				cancel()
				if err == nil && AnalyzeOutput(lines) == PromptNeedsPermission {
					return StatusWaiting
				}
				return StatusRunning
			}
		}
	}

	// Codex, or Claude transcript not locatable yet: the mtime gate + terminal read.
	if s.paneMonitor.Active(paneKey(p), p.PaneCWD, tool) {
		return StatusRunning
	}
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	lines, err := s.prober.CapturePane(cctx, p.SessionWindow, p.PaneIndex, 14)
	if err != nil {
		return StatusRunning
	}
	switch AnalyzeOutput(lines) {
	case PromptNeedsPermission:
		return StatusWaiting
	case PromptRunning:
		return StatusRunning
	default:
		return StatusIdle
	}
}
