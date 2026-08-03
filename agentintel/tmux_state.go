package agentintel

import (
	"context"
	"fmt"
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
	// AwaitingSince: transcript time of the completion behind AwaitingUser (zero when not
	// awaiting). Reload-proof (transcript-derived) → the frontend keys its per-window "seen"
	// dismissal on it so a cleared dot stays cleared across F5 yet re-appears on a new turn.
	AwaitingSince time.Time `json:"awaitingSince,omitempty"`
	// EndedOnQuestion: the completed turn ended on a free-text question. Refines the SAME
	// needs-you dot's label ("有提问" vs "已完成") — it never raises its severity, because an
	// agent at an empty prompt is not blocked. See AgentState.EndedOnQuestion.
	EndedOnQuestion bool `json:"endedOnQuestion,omitempty"`
	// StatusRule is the single rule that produced this pane's verdict ("transcript.running",
	// "screen.approval", …), present on EVERY decision — green included.
	//
	// It used to be gated on AwaitingUser, on the reasoning that "a running pane accuses nobody,
	// so it needs no defence". A pane stuck green while its agent waits accuses nobody and gets
	// nobody's attention either — that is the failure mode you cannot even notice, and it was
	// undiagnosable by construction, since five different rules return Running and none of them
	// left a mark. The rule is stable while the status is, and this frame is diff-suppressed, so
	// shipping it always costs nothing between polls.
	//
	// StatusEvidence — the matched screen line, scrubbed and truncated — stays confined to
	// attention decisions: it changes every poll (spinner frames), which would defeat the
	// suppression, and on a green pane the rule alone answers the question.
	//
	// Diagnostic fields: nothing renders them, they exist so a wrong dot can be traced instead of
	// re-argued. See status_decision.go.
	StatusRule     string `json:"statusRule,omitempty"`
	StatusEvidence string `json:"statusEvidence,omitempty"`
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

// OverviewTailLines caps how many trailing lines each Agent-Overview card's tail carries.
// The PC overview's active cards grow to fill the viewport, so the tail must carry enough real
// output to fill a tall card (not leave it padded/empty) — the card then bottom-aligns + clips
// to whatever height it actually gets. The whole screen is captured regardless (CaptureWindowTail),
// so this only widens the post-strip cap; it's still naturally bounded by the source screen's height.
//
// EXPORTED because it is the cap for BOTH overview feeds — the tmux one below and the non-tmux one
// in sessions_overview.go, which renders into the SAME card grid at the SAME height. That was
// previously two constants: this one at 40, and a `sessionTailLines = 8` whose comment claimed
// "matches the tmux overview's card so both render at the same height" while being 5x smaller. The
// claim was false the day it was written (this had been 40 for 24 days), and the symptom was
// visible: a non-tmux card showed ~8 lines in a ~60-line-tall box, the rest blank. One constant is
// the only way that sentence stays true — a comment cannot hold two numbers equal, a symbol can.
const OverviewTailLines = 40

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
	Installed     bool `json:"installed"`
	ServerRunning bool `json:"serverRunning"`
	Attached      bool `json:"attached"`
	// AttachedSession is the tmux session name this shellPID's client is attached
	// to (empty when not attached). It scopes the pane bar to THIS session's
	// windows rather than any session that merely has a client somewhere.
	AttachedSession string     `json:"attachedSession"`
	Prefix          TmuxPrefix `json:"prefix"`
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

	// tmuxProbeTTL is how long ANY tmux probe result stays authoritative — topology
	// (topologySnapshot) and per-shell client identity (TmuxProber.ClientFor) alike. ONE number
	// for one concept: "how stale may a tmux answer be". Just under the 1s poll interval, so a
	// tick never reuses the previous tick's answer.
	//
	// Why memoize at all: a tmux server handles commands ONE AT A TIME, and every WebSocket
	// connection used to run the full probe for itself, once a second. N clients therefore did not
	// cost N× in parallel — they queued behind each other and each one's latency grew with N.
	// Measured on a 6-pane server (scripts/diag/tmuxprobe): 72–172ms with nobody attached, 616ms–4.3s with
	// the UI actually in use, and 8 concurrent callers at 515ms vs 75ms once shared. Same probe,
	// made 10–60× slower purely by contention it created itself.
	//
	// Deliberately the same shape as sessions_overview's snapshot cache (see sessions_overview.go),
	// for the same reason: the payload is global while the callers are per-connection.
	tmuxProbeTTL = 900 * time.Millisecond
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

	mu               sync.Mutex
	installed        bool
	installedAt      time.Time
	prefix           TmuxPrefix
	prefixAt         time.Time
	prefixResolved   bool
	modeKeys         string
	modeKeysAt       time.Time
	modeKeysResolved bool

	// topologyMu guards the shared topology snapshot AND serialises its rebuild: a caller that
	// arrives while a rebuild is in flight waits for that result instead of starting a second
	// probe against the same single-threaded tmux server (which is exactly the pile-up
	// tmuxProbeTTL exists to end). Separate from mu so a cheap Prefix()/TmuxInstalled() lookup
	// is never stuck behind a topology probe.
	topologyMu   sync.Mutex
	topology     TmuxState
	topologyAt   time.Time
	topologyRead bool
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
// The split below is the whole point: the CALLER-SPECIFIC half (Attached / AttachedSession — who
// is asking) is computed per call, while the SHARED half (installed/prefix/mode-keys/topology —
// what the tmux server looks like) comes from one memoized probe. See tmuxProbeTTL.
func (s *TmuxStateService) State(ctx context.Context, shellPID int) TmuxState {
	st := s.topologySnapshot(ctx)
	if !st.Installed {
		return st
	}
	if shellPID > 0 {
		st.Attached = s.Attached(ctx, shellPID)
		if st.Attached {
			cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
			st.AttachedSession = s.prober.FindClientSession(cctx, shellPID)
			cancel()
		}
	}
	return st
}

// topologySnapshot returns the shellPID-independent half of the state, rebuilt at most once per
// tmuxProbeTTL and shared by every concurrent caller.
//
// Returns by value, and Sessions is only ever REPLACED (never appended to) by a rebuild, so a
// caller filling in its own Attached fields cannot mutate what the next caller reads.
func (s *TmuxStateService) topologySnapshot(ctx context.Context) TmuxState {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	now := time.Now()
	if s.topologyRead && now.Sub(s.topologyAt) < tmuxProbeTTL {
		TmuxProbeServedTotal.Inc()
		return s.topology
	}
	// The user is mid-keystroke: serve what we have rather than queue a dozen tmux commands ahead
	// of their echo. See interaction.go — bounded, so this can defer but never starve.
	if probeDeferredForInteraction(s.topologyAt, now) {
		TmuxProbeDeferredTotal.Inc()
		return s.topology
	}

	probeStart := now
	st := TmuxState{
		Installed: s.TmuxInstalled(),
		Prefix:    s.Prefix(ctx),
		ModeKeys:  s.ModeKeys(ctx),
	}
	if st.Installed {
		// ONE list-sessions answers both "is a server up" and "which sessions have a client".
		// They used to be two separate invocations of the same command — `ServerRunning` looked
		// only at the exit code, `attachedSessions` only at the output — which is one more command
		// queued on a single-threaded server for an answer already in hand.
		attached, running := s.sessionAttachment(ctx)
		st.ServerRunning = running
		if running {
			cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
			panes, err := s.prober.ListPanes(cctx)
			if err == nil && len(panes) > 0 {
				st.Sessions = s.buildSessions(cctx, panes, attached)
			}
			cancel()
		}
	}

	windows, panes := 0, 0
	for _, sess := range st.Sessions {
		windows += len(sess.Windows)
		for _, w := range sess.Windows {
			panes += len(w.Panes)
		}
	}
	LogTmuxProbe(ctx, time.Since(probeStart), panes, windows)

	// A probe cut short by a cancelled/expired ctx is NOT cached: caching it would pin an empty
	// topology for the rest of the TTL and blank the UI's tab strip for a full second on every
	// hiccup. Serve it once, let the next caller retry.
	if ctx.Err() == nil {
		s.topology = st
		s.topologyAt = time.Now()
		s.topologyRead = true
	}
	return st
}

// attachedSessions returns the set of session names that currently have a
// client attached (from list-sessions #{session_attached}).
func (s *TmuxStateService) attachedSessions(ctx context.Context) map[string]bool {
	attached, _ := s.sessionAttachment(ctx)
	return attached
}

// sessionAttachment is the single `list-sessions` behind both questions the caller has: which
// sessions have a client attached, and whether a tmux server answered at all (it exits non-zero
// with "no server running" when there is none). Splitting these into two commands cost an extra
// round trip on a server that handles them one at a time.
func (s *TmuxStateService) sessionAttachment(ctx context.Context) (map[string]bool, bool) {
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	out, err := tmuxCommandContext(cctx,
		"list-sessions", "-F", "#{session_name}"+tmuxFieldSep+"#{session_attached}",
	).Output()
	if err != nil {
		return nil, false
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
	return result, true
}

// buildSessions groups panes into sessions → windows → panes and runs per-pane
// agent detection against a single shared ps snapshot.
func (s *TmuxStateService) buildSessions(ctx context.Context, panes []TmuxPane, attached map[string]bool) []TmuxSessionState {
	// agents: PID → tool, computed once over the shared ps snapshot.
	agents := s.prober.DetectAgentProcessesInPanes(ctx, panes)
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
			winTool[winKey{p.SessionName, p.WindowIndex}] = agents[p.PanePID].Tool
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
				if tail, terr := s.prober.CaptureWindowTail(tctx, p.SessionWindow, winTool[wk], OverviewTailLines); terr == nil {
					m.Tail = tail
				}
				tcancel()
			}
			winMeta[wk] = m
		}

		ps := TmuxPaneState{
			Index: p.PaneIndex,
			// Active = "this pane is focused WITHIN its window" (tmux #{pane_active}), NOT
			// "this pane's window is the active window" (that's p.Active / TmuxWindowState.Active,
			// the same value for every pane in the window). Pre-existing mix-up: every consumer
			// (activeCwd/activeTool, useAgentOverview.windowCwd, and now the per-pane resource
			// drawer's currentPaneKey) does `panes.find(p => p.active)` expecting the ONE truly
			// focused pane in a split — wiring p.Active here made every pane in the active window
			// report active:true, so `.find()` silently landed on whichever pane sorts first
			// instead of the tmux-focused one. Harmless with one pane per window (the common case,
			// which is why this went unnoticed); wrong the moment a window has a split. p.PaneActive
			// (#{pane_active}) is the correct per-pane signal — already captured, just unused here.
			Active: p.PaneActive,
			PID:    p.PanePID,
			CWD:    p.PaneCWD,
			PaneID: p.PaneID,
		}
		if agent, ok := agents[p.PanePID]; ok {
			tool := agent.Tool
			ps.AgentTool = tool
			decision := s.paneDecision(ctx, p, agent)
			ps.AgentStatus = decision.Status
			// Needs-you: an explicit block (waiting) always counts; an idle pane counts
			// only if the driver says a turn actually completed (not fresh-idle). Snapshot()
			// reuses the driver Status() just updated, so no extra transcript read.
			snap := s.paneMonitor.Snapshot(paneKey(p))
			ps.AwaitingUser = ps.AgentStatus == StatusWaiting ||
				(ps.AgentStatus == StatusIdle && snap.AwaitingUser)
			decision.Awaiting = ps.AwaitingUser
			// Carry the reload-proof "completed at" so the frontend's seen-layer can tell
			// THIS completion from the next one, plus whether that turn ended on a question
			// (labels the same amber dot "有提问" instead of a bare "已完成").
			if ps.AwaitingUser {
				ps.AwaitingSince = snap.AwaitingSince
				ps.EndedOnQuestion = snap.EndedOnQuestion
			}
			// WHY this pane has the status it has — on EVERY decision now, not only the ones
			// that ask something of the user.
			//
			// Provenance used to be confined to attention states, reasoning that "a running pane
			// accuses nobody, so it needs no defence". Two reports in one hour falsified that. A
			// pane stuck GREEN while its agent waits is the SILENT failure — nobody tells you, so
			// you sit there — which is strictly worse than a false amber you glance at and
			// dismiss. And it was untraceable BY CONSTRUCTION: FIVE rules can return Running
			// (transcript.running / transcript.writing / transcript.unlocatable / screen.spinner /
			// a screen veto), and none of them left a mark in the payload or the log, so "why is
			// it green" could only ever be answered by guessing at the source.
			//
			// The RULE is safe to ship unconditionally precisely because it is stable while the
			// state is: this frame is diff-suppressed, and a string that changes only when the
			// REASON changes costs nothing between polls — and that change is exactly the moment
			// worth pushing. EVIDENCE stays gated, because it carries a live screen line that
			// differs on every poll (spinner frames, token counters) and would defeat the
			// suppression for no diagnostic gain: for a green pane the rule already says it all.
			ps.StatusRule = string(decision.Rule)
			if decision.IsAttention() {
				ps.StatusEvidence = decision.Evidence
			}
			// Logged for every decision too, same reason. Volume is already bounded: the
			// coalescer emits on CHANGE and at most once per 30s while a decision stands.
			LogStatusDecision(ctx, "tmux", fmt.Sprintf("%s.%d", p.SessionWindow, p.PaneIndex), tool, decision)
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

// panePromptVerdict scrapes the pane's last visible lines once and classifies them. The PTY is the
// ONE per-pane-reliable liveness signal — a spinner can only render in the pane that is actually
// working — so it is the tiebreaker whenever the transcript-derived status may be stale or (Claude,
// cwd-located) mis-attributed. Returns PromptUnknown on any capture error, so an ambiguous read
// never overrides the transcript.
// paneTailLines is the raw form of the same capture panePromptVerdict classifies. It exists for
// the transcript tiebreak, which needs the TEXT rather than a verdict about it — see
// matchPaneToTranscript. An error yields nil, and nil simply means "no evidence": the caller then
// falls back to the mtime guess it would have made anyway.
func (s *TmuxStateService) paneTailLines(ctx context.Context, p TmuxPane) []string {
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	lines, err := s.prober.CapturePane(cctx, p.SessionWindow, p.PaneIndex, paneScanLines)
	if err != nil {
		return nil
	}
	return lines
}

func (s *TmuxStateService) panePromptVerdict(ctx context.Context, p TmuxPane) OutputVerdict {
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	lines, err := s.prober.CapturePane(cctx, p.SessionWindow, p.PaneIndex, paneScanLines)
	if err != nil {
		return OutputVerdict{State: PromptUnknown, Rule: RuleNone}
	}
	return AnalyzeOutputDetail(lines)
}

// paneDecision derives a pane's agent status with a JSONL-gated terminal read, and names the
// rule that produced it:
//   - transcript being written (PaneAgentMonitor.Active) → working → Running, WITHOUT touching the pane.
//   - transcript stopped → read the visible pane: a permission/selection/input PROMPT lives there
//     (never in the transcript), so AnalyzeOutput on it is the ground truth — needs-permission →
//     Waiting (the push trigger), a spinner → still Running, otherwise the turn is done → Idle.
//
// This keeps the (slightly brittle, version-coupled) prompt scrape OFF the hot path: it runs only
// for stopped panes, not every agent pane every poll — accurate where it matters, cheap otherwise.
//
// It returns a StatusDecision rather than a bare status so a wrong verdict can be traced to ONE
// rule afterwards; the classification itself is unchanged.
func (s *TmuxStateService) paneDecision(ctx context.Context, p TmuxPane, agent DetectedAgent) StatusDecision {
	tool := agent.Tool
	// Accurate JSONL-derived status: a turn's end is recorded in the transcript
	// (Claude end_turn / Codex task_complete → waiting/idle), a Bash/Read tool is
	// executing = running. This fixes the mtime heuristic's blind spots — a just-
	// written ask card looked "running", a silently-running long tool looked "idle",
	// and (Codex) a finished turn looked perpetually "running" because its rollout
	// was unlocatable. Both Claude and Codex carry turn boundaries in JSONL, so both
	// use the driver; a Running result is still confirmed against the pane for a
	// terminal-only permission prompt.
	if tool == ToolClaude || tool == ToolCodex {
		// The tail closure is the ONLY new cost here and it is almost never paid: the monitor
		// invokes it exclusively when a claude pane's identity lookup missed AND two or more
		// transcripts in this cwd are still unclaimed — the ambiguity that used to be settled by
		// mtime order, i.e. by luck. Everything else keeps the cached binding and captures nothing.
		tail := func() []string { return s.paneTailLines(ctx, p) }
		if st, ok := s.paneMonitor.StatusWithTail(paneKey(p), p.PaneCWD, tool, tail, agent.ProcessPID); ok {
			switch st {
			case StatusRunning:
				// A pending tool may instead be blocked on a permission [Y/n] — that prompt
				// is terminal UI, absent from the transcript — so confirm against the pane.
				if v := s.panePromptVerdict(ctx, p); v.State == PromptNeedsPermission {
					return StatusDecision{Status: StatusWaiting, Rule: v.Rule, Evidence: v.Line}
				}
				return StatusDecision{Status: StatusRunning, Rule: RuleTranscriptRunning}
			case StatusWaiting:
				return StatusDecision{Status: StatusWaiting,
					Rule: TranscriptStatusRule(StatusWaiting, s.paneMonitor.Snapshot(paneKey(p)))}
			case StatusIdle:
				// The transcript says this turn ended (idle → drives the done-unseen dot / "跑完了"
				// notification). But a transcript-idle can be STALE within a poll: an agent that just
				// hit a turn boundary and immediately kept going (auto-continue, queued prompt,
				// next-turn thinking) is still working before its next line lands. The pane's OWN PTY
				// is the tiebreaker — a live spinner can only render in the pane that's actually
				// running — so a visibly-spinning idle is vetoed back to Running. Gated on Active()
				// (transcript freshly written) so a long-settled idle pane, which isn't spinning
				// anyway, never pays for a pane scrape. Only a POSITIVE spinner overrides; an
				// idle/unknown PTY trusts the transcript, so a genuinely-done pane still reads Idle.
				if s.paneMonitor.Active(paneKey(p), p.PaneCWD, tool, agent.ProcessPID) {
					if v := s.panePromptVerdict(ctx, p); v.State == PromptRunning {
						return StatusDecision{Status: StatusRunning, Rule: v.Rule, Evidence: v.Line}
					}
				}
				return StatusDecision{Status: StatusIdle,
					Rule: TranscriptStatusRule(StatusIdle, s.paneMonitor.Snapshot(paneKey(p)))}
			}
		}
	}

	// Codex, or Claude transcript not locatable yet: the mtime gate + terminal read. NOTE this
	// is the one arm where the SCREEN ALONE can declare Waiting — there is no transcript
	// opinion to confirm — so its decisions carry the screen rule and the line that matched.
	if s.paneMonitor.Active(paneKey(p), p.PaneCWD, tool, agent.ProcessPID) {
		return StatusDecision{Status: StatusRunning, Rule: RuleTranscriptWriting}
	}
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	lines, err := s.prober.CapturePane(cctx, p.SessionWindow, p.PaneIndex, paneScanLines)
	if err != nil {
		return StatusDecision{Status: StatusRunning, Rule: RuleTranscriptUnlocatable}
	}
	v := AnalyzeOutputDetail(lines)
	switch v.State {
	case PromptNeedsPermission:
		return StatusDecision{Status: StatusWaiting, Rule: v.Rule, Evidence: v.Line}
	case PromptRunning:
		return StatusDecision{Status: StatusRunning, Rule: v.Rule, Evidence: v.Line}
	default:
		return StatusDecision{Status: StatusIdle, Rule: v.Rule, Evidence: v.Line}
	}
}
