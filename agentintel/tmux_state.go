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
//
// The agent facts are NOT declared here: they are the embedded SurfaceUnit, the single declaration
// this payload shares with the non-tmux session card (sessions_overview.go). What each of those
// fields means is documented once, on that type — see surface_unit.go.
type TmuxPaneState struct {
	Index  int    `json:"index"`
	Active bool   `json:"active"`
	Title  string `json:"title"`
	PID    int    `json:"pid"`
	CWD    string `json:"cwd"`
	PaneID string `json:"paneId,omitempty"` // stable tmux pane id ("%N")
	// Embedded, not listed: everything below this line is what both surfaces carry, promoted into
	// this payload's JSON at exactly this position. A new surface fact belongs in SurfaceUnit,
	// where both feeds get it or neither does.
	SurfaceUnit
}

// TmuxWindowState is one window with its panes — and, on the Agent Overview, one CARD.
//
// The identity fields below are tmux's own and stay here (see SurfaceCard on why identity is not
// shared). What the card SAYS is not tmux-specific and is no longer computed by whoever renders
// it: the embedded SurfaceCard carries the roll-up of Panes, produced by the one rule in
// agentintel.RollUp, so a tmux card and a non-tmux card answer "what is the agent doing here"
// through the same fields and the same logic.
type TmuxWindowState struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	// WindowID is tmux's stable "@N" id — survives index reuse/reorder, unlike Index. The Agent
	// Overview keys its per-window seen-state on it so a reused index can't inherit stale state.
	WindowID string `json:"windowId,omitempty"`
	Active   bool   `json:"active"`
	// CWD is the card's working directory: the active pane's, falling back to the first pane's.
	// It used to be derived client-side (`windowCwd`) from the same two-line rule the card title
	// and the workbench both depend on — a rule that has no reason to live in a renderer.
	CWD   string          `json:"cwd,omitempty"`
	Panes []TmuxPaneState `json:"panes"`
	// Embedded: the card's own agent facts (RollUp of Panes) and its tail. Tail lived here as a
	// bare field before; it is the same key in the same place, now declared once for both feeds.
	SurfaceCard
}

// RollUpPanes recomputes the window's CARD facts from the panes it currently holds.
//
// Called once per built window, after its panes are final and sorted — a card whose roll-up was
// taken before its last pane arrived would describe a window that never existed. It is the only
// writer of the embedded SurfaceUnit, so "the card disagrees with its panes" has one place to be.
func (w *TmuxWindowState) RollUpPanes() {
	units := make([]SurfaceUnit, len(w.Panes))
	active := -1
	for i, p := range w.Panes {
		units[i] = p.SurfaceUnit
		// #{pane_active} — the ONE focused pane in a split, not "its window is current". The
		// difference matters here for the same reason it matters in the payload: with the wrong
		// flag every pane looks focused and the tiebreaker silently picks whichever sorts first.
		if p.Active && active < 0 {
			active = i
		}
	}
	w.SurfaceUnit = RollUp(units, active)
	w.CWD = ""
	if len(w.Panes) > 0 {
		w.CWD = w.Panes[0].CWD
		if active >= 0 {
			w.CWD = w.Panes[active].CWD
		}
	}
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
	// ServerVanished: we HAD a tmux server with sessions on it, and now there is none.
	//
	// Distinct from `!ServerRunning`, which is also the honest answer for someone who simply
	// never started tmux. The difference is the whole point: a machine that never had tmux
	// should say nothing, while a server that DIED under a user who was using it must not be
	// reported by silence.
	//
	// It is on the wire because only the server can know it — the flag survives a page reload,
	// a reconnect, and a client that was not watching when it happened, none of which a
	// frontend-side "it used to be there" could.
	//
	// The incident that put it here (2026-08-08 19:34:22): a tmux server holding eleven days of
	// work took SIGSEGV. The pane bar is gated on `attached && windows.length`, so it simply
	// disappeared — indistinguishable from "this shell isn't in tmux". The only trace anywhere
	// was one INFO line saying `window-size unreadable`, and the user found out by typing
	// `tmux attach` himself and reading "no sessions". That is「观察不到 ≠ 不存在」inverted: we
	// DID observe an absence, and published it as though nothing had happened.
	ServerVanished bool `json:"serverVanished,omitempty"`
	Attached       bool `json:"attached"`
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
	// windowSize is the resolved global `window-size`. Read but never published: see WindowSize.
	windowSize         string
	windowSizeAt       time.Time
	windowSizeResolved bool
	windowSizeLogged   string

	// topologyMu guards the shared topology snapshot AND serialises its rebuild: a caller that
	// arrives while a rebuild is in flight waits for that result instead of starting a second
	// probe against the same single-threaded tmux server (which is exactly the pile-up
	// tmuxProbeTTL exists to end). Separate from mu so a cheap Prefix()/TmuxInstalled() lookup
	// is never stuck behind a topology probe.
	topologyMu   sync.Mutex
	topology     TmuxState
	topologyAt   time.Time
	topologyRead bool
	// sawSessions records that a tmux server with at least one session was once observed by
	// THIS process. It is what makes ServerVanished mean "it died" rather than "you don't use
	// tmux" — and it is deliberately sticky for the life of the process: a server that comes
	// back clears it by being observed again, but nothing else should.
	sawSessions bool
	// vanishReported keeps the WARN to one per disappearance instead of one per second.
	vanishReported bool

	// cmdTimeout is this service's budget for one topology probe's tmux commands. A field
	// rather than a bare const because the budget is the thing that decides whether a probe
	// ANSWERS or merely runs out of time, and a rule that important has to be reachable — by a
	// test reproducing the exact shape that broke (child context expiring, parent healthy), and
	// by any future caller that needs a different budget. Zero means the package default.
	cmdTimeout time.Duration
}

// lastKnownWindows is how much was on the server the last time we could see it. It is the
// difference between "tmux is gone" and "tmux is gone, and it had nine windows on it" — the
// second is the one a person needs in order to know whether to care.
func (s *TmuxStateService) lastKnownWindows() int {
	n := 0
	for _, sess := range s.topology.Sessions {
		n += len(sess.Windows)
	}
	return n
}

func (s *TmuxStateService) commandTimeout() time.Duration {
	if s.cmdTimeout > 0 {
		return s.cmdTimeout
	}
	return tmuxCmdTimeout
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

// CapturePaneForShell reads the tmux pane history of the session this shell's client is attached
// to, split at the visible-screen boundary. READ-ONLY via the tmux server socket — nothing is
// typed into the user's pane, and tmux's own copy mode is untouched. This is the long-range source
// for the web copy mode on tmux tabs: the daemon's line scrollback barely fills for a tmux session
// (tmux repaints in place and keeps its own history internally), so the only place the deep
// history lives is tmux's buffer — and capture-pane is the supported way to read it.
//
// Returns (historyLines, screenLines): history = everything from the buffer cap down to just above
// the visible screen; screen = the visible grid. The caller numbers them contiguously.
func (s *TmuxStateService) CapturePaneForShell(ctx context.Context, shellPID, historyCap int) ([]string, []string, error) {
	st := s.State(ctx, shellPID)
	if st.AttachedSession == "" {
		return nil, nil, fmt.Errorf("shell %d is not attached to tmux", shellPID)
	}
	target := ""
	for _, sess := range st.Sessions {
		if sess.Name != st.AttachedSession {
			continue
		}
		for _, win := range sess.Windows {
			if !win.Active {
				continue
			}
			paneIdx := 0
			for _, pane := range win.Panes {
				if pane.Active {
					paneIdx = pane.Index
					break
				}
			}
			target = fmt.Sprintf("%s:%d.%d", st.AttachedSession, win.Index, paneIdx)
		}
	}
	if target == "" {
		return nil, nil, fmt.Errorf("no active window found for session %q", st.AttachedSession)
	}
	full, err := s.prober.run(ctx, "capture-pane", "-t", target, "-p", "-S", fmt.Sprintf("-%d", historyCap))
	if err != nil {
		return nil, nil, fmt.Errorf("capture-pane %s: %w", target, err)
	}
	vis, err := s.prober.run(ctx, "capture-pane", "-t", target, "-p")
	if err != nil {
		return nil, nil, fmt.Errorf("capture-pane visible %s: %w", target, err)
	}
	for len(full) > 0 && full[len(full)-1] == "" {
		full = full[:len(full)-1]
	}
	for len(vis) > 0 && vis[len(vis)-1] == "" {
		vis = vis[:len(vis)-1]
	}
	// 拆分点：visible 是 full 的尾部（同一块屏）。full 比 visible 还短（很小的 pane）→ 全给 history。
	split := len(full) - len(vis)
	if split < 0 {
		split = len(full)
	}
	return full[:split], full[split:], nil
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
	cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
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
	cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
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

// WindowSize returns the resolved global `window-size` ("latest" | "largest" | "smallest" |
// "manual"), cached with the same short TTL as the prefix.
//
// ── Why a value nothing renders is read at all ───────────────────────────────────────────────
// A tmux window has exactly ONE size, so when two clients watch the same window somebody has to
// lose. Human decided who: `latest` — the client with the most recent activity, i.e. "whoever is
// using it gets the layout". The whole client-side discipline built on that decision (a page
// declares its viewport when it becomes the viewer, and never when it is not — see
// frontend/…/viewportDeclaration.ts) is correct only if tmux is actually arbitrating that way.
//
// And nothing in this program knew the option existed. `latest` is tmux's default, so the design
// worked by luck: one line in a user's tmux.conf (`set -g window-size largest`) and the phone would
// silently never get its own layout, with no error, no log, and nothing to point at. The next
// person to debug it would have to re-derive every step from scratch — which is the actual cost
// being paid here, and it is much larger than this function.
//
// Deliberately NOT put on the wire. No client acts on it; a payload field nobody reads is a second
// thing to keep true. Logging it names the assumption at the moment it stops holding, which is the
// only moment it matters.
func (s *TmuxStateService) WindowSize(ctx context.Context) string {
	s.mu.Lock()
	if s.windowSizeResolved && time.Since(s.windowSizeAt) < tmuxPrefixTTL {
		v := s.windowSize
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	v := s.resolveWindowSize(ctx)

	s.mu.Lock()
	s.windowSize = v
	s.windowSizeAt = time.Now()
	s.windowSizeResolved = true
	// Logged on CHANGE only (first observation included), so a server that has been up for a week
	// carries one line per actual state rather than one per probe.
	changed := s.windowSizeLogged != v
	s.windowSizeLogged = v
	s.mu.Unlock()

	if changed {
		LogTmuxWindowSize(ctx, v)
	}
	return v
}

func (s *TmuxStateService) resolveWindowSize(ctx context.Context) string {
	if !s.TmuxInstalled() {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
	defer cancel()
	out, err := tmuxCommandContext(cctx, "show-options", "-g", "window-size").Output()
	if err != nil {
		// Unreadable is not the same as any particular value, and guessing "latest" here would
		// re-create exactly the silent assumption this exists to end.
		return ""
	}
	// Output form: "window-size latest".
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
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
	cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
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
	cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
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
			cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
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
	// answered records that the probe actually REACHED a conclusion about the topology, as
	// opposed to running out of time on the way there. The two produce the same empty
	// Sessions and mean opposite things.
	answered := true
	if st.Installed {
		// Resolved for its side effect (see WindowSize): it names the arbitration rule the whole
		// multi-client sizing design rests on, and says so out loud the moment it stops being
		// `latest`. TTL-cached like the prefix, so this is one tmux command per 10s, not per probe.
		s.WindowSize(ctx)
		// ONE list-sessions answers both "is a server up" and "which sessions have a client".
		// They used to be two separate invocations of the same command — `ServerRunning` looked
		// only at the exit code, `attachedSessions` only at the output — which is one more command
		// queued on a single-threaded server for an answer already in hand.
		attached, running, reachedServer := s.sessionAttachment(ctx)
		answered = reachedServer
		st.ServerRunning = running
		if running {
			cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
			panes, err := s.prober.ListPanes(cctx)
			if err == nil && len(panes) > 0 {
				st.Sessions = s.buildSessions(cctx, panes, attached)
			}
			// tmuxCmdTimeout is ONE budget for list-panes plus a capture per window, so a busy
			// machine exhausts it partway through and leaves Sessions empty or short. Measured on
			// a loaded laptop: a bare tmux call went from ~5ms to 0.5s, nine windows blew the
			// 1.5s budget, and the pane bar vanished.
			answered = answered && err == nil && cctx.Err() == nil
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

	// ── Did a server we were watching go away? ────────────────────────────────────────────────
	// Sticky by design — the flag stays until a server is actually seen again, so a client that
	// reloads or reconnects after the fact still learns what happened.
	//
	// `sawSessions` is the load-bearing half: without it this fires for everyone who simply never
	// starts tmux, and a warning that cries wolf once is never believed again. Pinned by
	// TestServerVanished_StaysSilentForSomeoneWhoNeverRanTmux (verified red when the guard is
	// dropped).
	//
	// The `answered` gate is DEFENCE, not the live guard, and saying so is the honest version:
	// a probe that could not answer already returns the cached topology below rather than `st`,
	// so today this branch is unreachable for it — an attempt to verify it red failed for exactly
	// that reason. It stays because the two rules are independent: if that lower return ever
	// stops shielding this, "could not find out" must still not become "it died".
	if answered {
		switch {
		case len(st.Sessions) > 0:
			s.sawSessions = true
			s.vanishReported = false
		case !st.ServerRunning && s.sawSessions:
			st.ServerVanished = true
			if !s.vanishReported {
				s.vanishReported = true
				TmuxServerVanishedTotal.Inc()
				LogTmuxServerVanished(ctx, s.lastKnownWindows())
			}
		}
	}

	// A probe that did not ANSWER must not be published as one. "there are no panes" and "I
	// could not find out" produce the identical empty Sessions and mean opposite things, and
	// only the first may replace what we know.
	//
	// This used to be guarded — the intent was already written down right here — but on the
	// PARENT ctx, while the timeout that actually fires lives on the per-command child. The
	// parent stays healthy, so every timed-out probe was published AND cached for a full TTL:
	// the pane bar disappeared, came back on the next good probe, disappeared again. That is
	// the「一会有一会没有」, and it is a correctness bug, not a slow machine — a slow machine is
	// merely what makes it fire.
	//
	// Degraded and nothing known yet is the one case where the empty answer is still the best
	// one available: serve it, but never cache it, so the next caller retries immediately.
	if ctx.Err() != nil {
		return st
	}
	if !answered && s.topologyRead {
		return s.topology
	}
	if answered {
		s.topology = st
		s.topologyAt = time.Now()
		s.topologyRead = true
	}
	return st
}

// attachedSessions returns the set of session names that currently have a
// client attached (from list-sessions #{session_attached}).
func (s *TmuxStateService) attachedSessions(ctx context.Context) map[string]bool {
	attached, _, _ := s.sessionAttachment(ctx)
	return attached
}

// sessionAttachment is the single `list-sessions` behind both questions the caller has: which
// sessions have a client attached, and whether a tmux server answered at all (it exits non-zero
// with "no server running" when there is none). Splitting these into two commands cost an extra
// round trip on a server that handles them one at a time.
func (s *TmuxStateService) sessionAttachment(ctx context.Context) (map[string]bool, bool, bool) {
	cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
	defer cancel()
	// list-clients, not list-sessions' #{session_attached}, because THIS PROCESS is a client.
	// The persistent control connection (tmux_control.go) attaches to a session to run commands
	// on it, which flips session_attached from 0 to 1 — measured — and would report a session
	// you detached from as still attached. An observer that changes the quantity it observes is
	// not an observer. #{client_control_mode} is how tmux lets us subtract ourselves.
	//
	// It answers the second question too: tmux exits non-zero with "no server running", while a
	// live server with nobody attached exits 0 with no output. Same one command as before.
	out, err := tmuxCommandContext(cctx,
		"list-clients", "-F", "#{client_session}"+tmuxFieldSep+"#{client_control_mode}",
	).Output()
	if err != nil {
		// tmux exits non-zero with "no server running", and that IS an answer — the third
		// return says so. A context that expired is not an answer about anything: collapsing
		// the two into `running=false` publishes「tmux 没在跑」to a user whose tmux is fine,
		// which is the same mistake as an empty pane bar, one layer up and louder.
		return nil, false, cctx.Err() == nil
	}
	return parseClientAttachment(string(out)), true, true
}

// parseClientAttachment turns `list-clients` rows into "which sessions a USER is looking at".
//
// Control-mode clients are subtracted because one of them is ours. Pre-3.2 tmux expands
// client_control_mode to empty and such a client counts as real — the safe direction, since it
// can only fail to subtract, never invent an attachment that is not there.
func parseClientAttachment(out string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, tmuxFieldSep, 2)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimSpace(fields[1]) == "1" {
			continue
		}
		result[fields[0]] = true
	}
	return result
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
			// ONE decision, shared with the non-tmux session card. The only thing this source
			// contributes is where the screen comes from: a capture-pane, which can fail — and
			// saying so (ok=false) is what keeps "could not read it" from being published as
			// "there is nothing there". See agentintel/surface_decision.go.
			unit, decision := s.paneMonitor.DecideSurface(SurfaceProbe{
				Key: paneKey(p), CWD: p.PaneCWD, Tool: tool, ProcessPID: agent.ProcessPID,
				Screen: func() ([]string, bool) {
					cctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
					defer cancel()
					lines, err := s.prober.CapturePane(cctx, p.SessionWindow, p.PaneIndex, paneScanLines)
					return lines, err == nil
				},
			})
			// Every agent fact this pane carries — status, needs-you, the reload-proof
			// completion time, the rule behind the verdict, the age of the evidence — arrives
			// as ONE value from ONE place. This block used to re-derive each of them here, and
			// the session card re-derived them again in its own file; the two had drifted on
			// three of them before anyone noticed.
			ps.SurfaceUnit = unit
			// Logged for every decision, not just the ones asking something of the user: a pane
			// stuck GREEN while its agent waits is the failure nobody is told about, and five
			// different rules return Running. Volume is bounded by the coalescer — it emits on
			// CHANGE and at most once per 30s while a decision stands.
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
			// The card, from the panes that are actually in it. After the sort, so the roll-up's
			// "first unit that …" tiebreakers read in the same order the wire and the UI do.
			w.RollUpPanes()
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

