package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

// Background agent-waiting notifier — the CHANNEL-AGNOSTIC engine behind every notify
// channel (iLink/WeChat, Feishu, DingTalk, WeCom, Slack).
//
// A server-global goroutine (independent of any browser tab) polls every
// notifyPollInterval, diffs each tracked agent's AgentStatus, and on one finishing a turn
// (running → idle/waiting) fans ONE coalesced event out through the coordinator to every
// enabled provider. It knows nothing about any specific channel.
//
// ── TWO SOURCES, ONE SINK ───────────────────────────────────────────────────────────
// The shape is: two event SOURCES feed one transition rule, one coalescing batch and one
// coordinator fan-out.
//
//	tmux topology poll ─┐
//	                    ├─→ observe() → pending batch → flush() → notify.Coordinator
//	PTY session states ─┘
//
//   - the tmux source sees panes under the tmux server.
//   - the PTY source sees the terminal sessions THIS server owns, and is the only source a
//     user without tmux has. It reads the same memoized Agent-Overview snapshot the WS push
//     uses (sessions_overview.go) rather than growing a second detector.
//
// Before this, the whole notifier was gated on tmuxInstalled(): a user running `claude` or
// `codex` in a plain tab got NO completion notification at all, ever — "让 gpt 数 10 个数，
// 早该数完通知我了，没有". The two sources cannot double-fire for one agent: a session that
// merely runs `tmux attach` hosts no agent in its own process tree (the agent lives under
// the tmux server), so the PTY detector reports no tool for it and the tmux source owns it.
//
// Lifecycle (Server-owned): gated on "≥1 channel enabled" — the coordinator is the SSOT —
// and NOT on tmux. Started at boot / on a channel being enabled / on iLink login; stopped
// when the last channel is disabled. No busy-spin: the goroutine only exists while there is
// someone to notify, and the tmux half of a tick is skipped when tmux is absent.
//
// Dedupe: a target that stays "waiting" across ticks does not re-fire; it leaves the fired
// set once it transitions away, so the next completion fires again.

const (
	// notifyPollInterval is how often the notifier recomputes agent state.
	notifyPollInterval = 2 * time.Second
	// notifyStateTimeout bounds a single tmux topology computation.
	notifyStateTimeout = 3 * time.Second
	// notifyPerPaneCooldown suppresses re-notifying the same pane within this window
	// (a chatty pane that flaps running→idle→running won't flood).
	notifyPerPaneCooldown = 2 * time.Minute
	// notifyCoalesceWindow buffers transitions briefly so several panes that finish
	// close together collapse into ONE merged notification.
	notifyCoalesceWindow = 5 * time.Second
)

// ptyKeyPrefix namespaces PTY-session ids inside the notifier's per-target maps, so a
// session id can never collide with a tmux pane id ("session:window:pane") — the two
// sources share every map, and a collision would silently merge two different agents.
const ptyKeyPrefix = "pty:"

func ptyKey(sessionID string) string { return ptyKeyPrefix + sessionID }

// ptySessionID reverses ptyKey; ok is false for a tmux pane key. The two sources differ in
// exactly one respect — which clock governs their cooldown — and this is how that is asked.
func ptySessionID(key string) (string, bool) {
	if rest, ok := strings.CutPrefix(key, ptyKeyPrefix); ok {
		return rest, true
	}
	return "", false
}

// agentNotifier is the running poller. Owned by *Server; one at a time.
type agentNotifier struct {
	server *Server
	cancel context.CancelFunc
	done   chan struct{}

	// Per-target tracking. Keys are tmux pane ids ("session:window:pane") or PTY session
	// ids ("pty:<uuid>") — one namespace so both sources share one transition rule.
	prev         map[string]agentintel.AgentStatus // last status — drives running→idle/waiting transitions
	meta         map[string]targetMeta             // last-seen tool/cwd/location/transcript
	lastNotified map[string]time.Time              // per-pane cooldown (tmux targets; PTY uses signalGate)
	pending      map[string]bool                   // targets triggered in the current (coalescing) batch
	pendingSince time.Time

	baseline  map[string]agentintel.SessionSummary // transcriptPath → summary at last notification (delta source)
	archived  map[string]archivedRec               // transcriptPath → closed-pane session (today-scoped)
	lastFlush time.Time
}

// ensureNotifier starts the background notifier if it is not already running. Idempotent —
// safe to call from server boot, an iLink login, or a channel being enabled (metrics.go
// SetEnabled). The engine is channel-agnostic; whether any message actually goes out is the
// coordinator's call per enabled provider.
//
// Deliberately NOT gated on tmux: the PTY source works without it, and that gate was the
// single reason non-tmux users received nothing. A machine without tmux simply skips the
// tmux half of each tick.
func (s *Server) ensureNotifier() {
	if s == nil {
		return
	}
	s.notifierMu.Lock()
	if s.notifier != nil {
		s.notifierMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &agentNotifier{
		server:       s,
		cancel:       cancel,
		done:         make(chan struct{}),
		prev:         map[string]agentintel.AgentStatus{},
		meta:         map[string]targetMeta{},
		lastNotified: map[string]time.Time{},
		pending:      map[string]bool{},
		baseline:     map[string]agentintel.SessionSummary{},
		archived:     map[string]archivedRec{},
	}
	s.notifier = n
	s.notifierMu.Unlock()

	n.loadState() // restore per-pane status + delivery metrics before the loop starts
	go n.run(ctx)
	logger.Info("agent notifier started")
}

// stopNotifier stops the background notifier if running. Idempotent.
func (s *Server) stopNotifier() {
	s.notifierMu.Lock()
	n := s.notifier
	s.notifier = nil
	s.notifierMu.Unlock()
	if n == nil {
		return
	}
	n.cancel()
	<-n.done
	logger.Info("agent notifier stopped")
}

// notifierRunning reports whether the background agent-waiting poller is active.
func (s *Server) notifierRunning() bool {
	s.notifierMu.Lock()
	defer s.notifierMu.Unlock()
	return s.notifier != nil
}

// reconcileNotifier is the SINGLE lifecycle decision point: run the poller iff any notify
// channel is enabled (the coordinator is the SSOT for that). Call after anything that
// changes the enabled set — server boot, a channel toggle (metrics.go SetEnabled). Both
// arms are idempotent, so repeated calls are safe.
func (s *Server) reconcileNotifier(ctx context.Context) {
	if s.coordinator != nil && s.coordinator.AnyEnabled(ctx) {
		s.ensureNotifier()
	} else {
		s.stopNotifier()
	}
}

// run is the poll loop. It exits on ctx cancel.
func (n *agentNotifier) run(ctx context.Context) {
	defer close(n.done)
	defer n.saveState() // persist last-known state on shutdown
	ticker := time.NewTicker(notifyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.tick(ctx)
		}
	}
}

// tick runs both sources, then the shared bookkeeping: targets that vanished become
// "archived", and a coalesced batch is flushed once its window has elapsed.
//
// Both sources feed observe(), so the definition of "a turn just completed" exists ONCE.
func (n *agentNotifier) tick(ctx context.Context) {
	now := time.Now()
	pl := agentintel.NewProjectLocator()
	current := map[string]bool{}

	// Source 1 — tmux panes. Skipped entirely without tmux (the probe is cached 60s, and a
	// machine without tmux must not pay a subprocess every 2s for a topology that cannot exist).
	// tmuxSpoke is false when the source could not be READ (absent, timed out, unparseable),
	// which is not the same as "it reported nothing".
	tmuxSpoke := false
	if n.server.tmuxInstalled() {
		tmuxSpoke = n.scanTmux(ctx, now, pl, current)
	}
	// Source 2 — this server's own PTY sessions. ALWAYS: it is the only source for a user
	// without tmux, and it costs one memoized snapshot the WS push would compute anyway. It
	// enumerates an in-process map, so unlike tmux it cannot half-fail.
	n.scanSessions(ctx, now, pl, current)

	// Targets that vanished (closed / agent exited) → archived, keyed by transcript.
	for id := range n.prev {
		if current[id] {
			continue
		}
		// A tmux read that FAILED is not evidence that its panes closed. Before the sources
		// were split, such a tick aborted before this sweep; keeping that guarantee means a
		// single timed-out `tmux list-panes` cannot file every live pane as "closed today"
		// and inflate the next notification's archive count.
		if _, isPTY := ptySessionID(id); !isPTY && !tmuxSpoke {
			continue
		}
		m := n.meta[id]
		if m.transcriptPath != "" {
			n.archived[m.transcriptPath] = archivedRec{tool: m.tool, cwd: m.cwd, closedAt: now}
		}
		delete(n.prev, id)
		delete(n.meta, id)
		delete(n.lastNotified, id)
		delete(n.pending, id)
	}
	// Archived is a "today" set — prune anything not closed today.
	for path, rec := range n.archived {
		if !sameDay(rec.closedAt, now) {
			delete(n.archived, path)
		}
	}

	// Flush the coalesced batch once the window has elapsed.
	if len(n.pending) > 0 && now.Sub(n.pendingSince) >= notifyCoalesceWindow {
		n.flush(now, pl)
	}
}

// scanTmux is the tmux source: every pane the tmux server knows about, agent-detected.
// It returns false when the topology could not be read at all — the caller must not then
// mistake an unanswered question for an empty answer.
func (n *agentNotifier) scanTmux(ctx context.Context, now time.Time, pl *agentintel.ProjectLocator, current map[string]bool) bool {
	provider := n.server.tmuxProvider
	if provider == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, notifyStateTimeout)
	raw, err := provider.TmuxState(cctx, 0)
	cancel()
	if err != nil || raw == nil {
		return false
	}
	var st agentintel.TmuxState
	if json.Unmarshal(raw, &st) != nil {
		return false
	}

	for _, sess := range st.Sessions {
		for _, win := range sess.Windows {
			for _, pane := range win.Panes {
				tool := string(pane.AgentTool)
				// Live = a pane with an agent in an interactive state. Others (no agent /
				// exited) fall through and become "archived" if previously tracked.
				if !trackableStatus(tool, pane.AgentStatus) {
					continue
				}
				n.observe(fmt.Sprintf("%s:%d:%d", sess.Name, win.Index, pane.Index), targetMeta{
					tool: tool, cwd: pane.CWD, session: sess.Name,
					windowName: win.Name, window: win.Index, pane: pane.Index,
					transcriptPath: transcriptPath(pl, pane.CWD, tool),
				}, pane.AgentStatus, now, current)
			}
		}
	}
	return true
}

// scanSessions is the PTY source: every terminal session this server owns.
//
// It reads the SAME memoized snapshot the Agent Overview pushes over the WebSocket (900ms
// TTL, sessions_overview.go) instead of computing a second view of the world — one
// detector, two consumers, so a notification can never disagree with the card that
// triggered it. When a browser is attached this costs nothing: the tick reuses the
// snapshot the push already built.
func (n *agentNotifier) scanSessions(ctx context.Context, now time.Time, pl *agentintel.ProjectLocator, current map[string]bool) {
	if n.server == nil || n.server.mgr == nil {
		return
	}
	n.observeSessions(n.server.overviewSnapshot(ctx).entries, now, pl, current)
}

// observeSessions is scanSessions with the snapshot handed in — the transition-visible half,
// separated so it can be driven from a test without conjuring a real agent process.
func (n *agentNotifier) observeSessions(entries []SessionOverviewEntry, now time.Time, pl *agentintel.ProjectLocator, current map[string]bool) {
	for _, e := range entries {
		status := agentintel.AgentStatus(e.AgentStatus)
		if !trackableStatus(e.AgentTool, status) {
			continue
		}
		n.observe(ptyKey(e.ID), targetMeta{
			tool: e.AgentTool, cwd: e.CWD,
			// session is the DEEP-LINK target (?session=…), location is what the message
			// shows. A tab has no window/pane coordinates, so its title IS its whole address.
			session:        n.sessionDeepName(e.ID, e.Title),
			location:       e.Title,
			transcriptPath: transcriptPath(pl, e.CWD, e.AgentTool),
		}, status, now, current)
	}
}

// trackableStatus reports whether a target is an agent in an interactive state — the only
// kind the notifier follows. Shared by both sources so "what counts as live" is one rule.
func trackableStatus(tool string, st agentintel.AgentStatus) bool {
	if tool == "" {
		return false
	}
	return st == agentintel.StatusRunning || st == agentintel.StatusIdle || st == agentintel.StatusWaiting
}

// observe folds ONE target's freshly-read status into the notifier state: it is the single
// definition of "this agent just finished a turn", shared by both sources so a second source
// cannot grow a second (drifting) notion of the transition.
func (n *agentNotifier) observe(id string, m targetMeta, status agentintel.AgentStatus, now time.Time, current map[string]bool) {
	current[id] = true
	n.meta[id] = m
	prev := n.prev[id]
	n.prev[id] = status

	// The user answered (idle/waiting → running): drop this target's cooldown so the NEXT
	// turn-end re-notifies even inside the 2-min window. A back-and-forth exchange should
	// ping on every completion, not just the first.
	if status == agentintel.StatusRunning && (prev == agentintel.StatusIdle || prev == agentintel.StatusWaiting) {
		n.clearCooldown(id)
		return
	}
	// Trigger: the target FINISHED working (running → idle/waiting). Excludes fresh-start
	// idle and startup-existing state (prev must be running).
	if prev != agentintel.StatusRunning || (status != agentintel.StatusIdle && status != agentintel.StatusWaiting) {
		return
	}
	if !n.allowFire(id, now) {
		return
	}
	if len(n.pending) == 0 {
		n.pendingSince = now
	}
	n.pending[id] = true
}

// allowFire reports whether this target may contribute to a notification now.
//
// tmux panes use the notifier's own per-pane clock, stamped at flush so a coalesced batch
// advances together. PTY sessions instead consult the SHARED per-session clock in signalGate
// — the very one the EXPLICIT signal path uses (session_signal.go) — because those two can
// describe the SAME moment for the same session: an agent that ends its turn with a BEL is
// one event, and without a shared clock the user's phone would buzz twice a second apart.
// One clock per session collapses them to whichever arrives first. Taken here rather than at
// flush because allowNotify is check-and-consume, which is what makes it atomic against the
// signal path running on another goroutine.
func (n *agentNotifier) allowFire(id string, now time.Time) bool {
	if sid, ok := ptySessionID(id); ok {
		return n.server.signals.allowNotify(sid, now)
	}
	if last, ok := n.lastNotified[id]; ok && now.Sub(last) < notifyPerPaneCooldown {
		return false
	}
	return true
}

// clearCooldown forgets a target's cooldown when the user ANSWERS it. Same reasoning for
// both sources — and for a PTY session it also re-arms the explicit-signal path, which is
// correct: once you have replied, the next bell IS news again.
func (n *agentNotifier) clearCooldown(id string) {
	if sid, ok := ptySessionID(id); ok {
		n.server.signals.clearNotify(sid)
		return
	}
	delete(n.lastNotified, id)
}

// sessionDeepName resolves a session's deep-link name (?session=…), falling back to the
// card title when the session has already gone — a notification that points at a name is
// still better than one that points nowhere.
func (n *agentNotifier) sessionDeepName(id, fallback string) string {
	if n.server != nil && n.server.mgr != nil {
		if sess, err := n.server.mgr.Get(id); err == nil {
			return sess.Name
		}
	}
	return fallback
}

// flush computes the live + archived metrics, builds one (merged) notification with
// the stats body, sends it through the A/B coordinator, and advances the baseline.
func (n *agentNotifier) flush(now time.Time, pl *agentintel.ProjectLocator) {
	var live, triggered []liveSession
	liveSumm := map[string]agentintel.SessionSummary{}
	for id, stt := range n.prev {
		m := n.meta[id]
		if m.tool == "" || m.cwd == "" {
			continue
		}
		summ := computeSummary(pl, m.cwd, m.tool, stt == agentintel.StatusRunning)
		if m.transcriptPath != "" {
			liveSumm[m.transcriptPath] = summ
		}
		ls := liveSession{
			key: id, location: m.location,
			tool: m.tool, session: m.session, window: m.window, windowName: m.windowName, pane: m.pane,
			status: stt, summary: summ, activeToday: isTodayFile(m.transcriptPath, now),
		}
		live = append(live, ls)
		if n.pending[id] {
			triggered = append(triggered, ls)
		}
	}

	var archivedToday []agentintel.SessionSummary
	archivedTodayCount, archivedSinceNotif := 0, 0
	for _, rec := range n.archived {
		archivedToday = append(archivedToday, computeSummary(pl, rec.cwd, rec.tool, false))
		archivedTodayCount++
		if rec.closedAt.After(n.lastFlush) {
			archivedSinceNotif++
		}
	}

	delta := computeDelta(liveSumm, n.baseline)
	today := computeToday(live, archivedToday)

	deepSession := ""
	if len(triggered) > 0 {
		deepSession = triggered[0].session
	} else if len(live) > 0 {
		deepSession = live[0].session
	}
	summary := buildSummaryBlocks(delta, today, archivedSinceNotif, archivedTodayCount, now.Sub(n.lastFlush), !n.lastFlush.IsZero())
	event := buildNotifyEvent(triggered, live, summary, n.server.notifyDeepURL(deepSession))

	// Advance baseline + per-pane cooldowns, clear the batch. PTY sessions are skipped: their
	// clock lives in signalGate and was already consumed at detection (see allowFire), so
	// stamping a second one here would be a clock nobody reads.
	n.baseline = liveSumm
	for id := range n.pending {
		if _, isPTY := ptySessionID(id); isPTY {
			continue
		}
		n.lastNotified[id] = now
	}
	n.pending = map[string]bool{}
	n.lastFlush = now

	// Fan out asynchronously so a slow channel (a 10-15s network send) never blocks
	// the 2s poll loop and make it miss state transitions. Baseline/lastFlush were
	// already advanced above, so the next tick is consistent regardless of send timing.
	// The coordinator records delivery metrics internally (its own lock).
	go func() {
		rec := n.server.coordinator.Send(context.Background(), event)
		logger.Info("notify fanned out", "providers", len(rec.Results))
	}()
	n.saveState() // persist prev + lastFlush (this batch's send metrics land next save)
}

// ── per-target metadata + archived record + IO helpers ──────────────────────

// targetMeta is what the notifier remembers about one tracked agent, whatever source found
// it. window/windowName/pane are tmux coordinates and stay zero for a PTY session, which
// instead carries `location` — a tab has no coordinates, its title IS its whole address.
type targetMeta struct {
	tool, cwd, session, windowName string
	window, pane                   int
	location                       string // pre-rendered "where" (PTY sessions); tmux derives it from the coordinates
	transcriptPath                 string
}

type archivedRec struct {
	tool, cwd string
	closedAt  time.Time
}

// transcriptPath resolves a pane's transcript file path from its CWD (best effort).
func transcriptPath(pl *agentintel.ProjectLocator, cwd, tool string) string {
	if cwd == "" {
		return ""
	}
	switch tool {
	case "claude":
		if files, err := pl.ClaudeSessionFiles(cwd); err == nil && len(files) > 0 {
			return files[0]
		}
	case "codex":
		return agentintel.CodexNewestRolloutForCWD(pl, cwd)
	}
	return ""
}

// computeSummary parses a session's transcript into its metrics summary (turns /
// tokens / cost). Called only at flush, so the parse is off the 2s poll hot path.
func computeSummary(pl *agentintel.ProjectLocator, cwd, tool string, active bool) agentintel.SessionSummary {
	return overviewMetrics(pl, cwd, "", "", active, tool).Summary
}

func isTodayFile(path string, now time.Time) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return sameDay(fi.ModTime(), now)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// ── state persistence (per-pane status + delivery metrics survive a restart) ────
//
// Persisting the per-pane status (prev) means a turn that finished while the server
// was down is still detected on the first tick after restart (not silently missed);
// persisting the coordinator metrics means the per-provider send history / "why it
// failed last" survives a restart (the troubleshooting intent). Saved ONLY from the
// notifier goroutine (flush + shutdown), so prev needs no extra lock; the coordinator
// metrics snapshot is itself mutex-guarded.

type notifierState struct {
	Prev      map[string]agentintel.AgentStatus `json:"prev"`
	LastFlush time.Time                         `json:"lastFlush"`
	Metrics   notify.MetricsState               `json:"metrics"`
}

func (n *agentNotifier) statePath() string {
	return filepath.Join(n.server.config.DataDir, "notify-state.json")
}

func (n *agentNotifier) loadState() {
	b, err := os.ReadFile(n.statePath())
	if err != nil {
		return
	}
	var st notifierState
	if json.Unmarshal(b, &st) != nil {
		return
	}
	if st.Prev != nil {
		n.prev = st.Prev
		// PTY sessions do NOT survive a restart: their processes are this server's children
		// and died with it, and the ids are per-run UUIDs that can never be seen again. Only
		// tmux panes (hosted by an independent server) can legitimately be resumed, which is
		// the entire reason prev is persisted. Dropping them here keeps the first tick from
		// "archiving" a session that has not existed since the previous process.
		for id := range n.prev {
			if _, isPTY := ptySessionID(id); isPTY {
				delete(n.prev, id)
			}
		}
	}
	n.lastFlush = st.LastFlush
	if n.server.coordinator != nil {
		n.server.coordinator.RestoreMetrics(st.Metrics)
	}
}

func (n *agentNotifier) saveState() {
	st := notifierState{Prev: n.prev, LastFlush: n.lastFlush}
	if n.server.coordinator != nil {
		st.Metrics = n.server.coordinator.MetricsSnapshot()
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(n.statePath(), b, 0600)
}

// notifyDeepURL builds the notification tap target — the SINGLE source for it,
// shared by the live notifier and the test endpoint. It is the CURRENT cloudflare
// tunnel URL (absolute) so a tap opens the live HTTPS origin (same-origin relative
// path when no tunnel runs). It carries NO auth token: a tap lands on the normal
// auth gate. (Earlier builds embedded a one-time bootstrap token in the URL fragment
// for tap-to-auth; that was dropped so a notification — especially one fanned out to
// a shared IM group webhook — never doubles as a login link.)
func (s *Server) notifyDeepURL(session string) string {
	deepURL := "/?session=" + url.QueryEscape(session)
	if base := s.tunnel.PublicURL(); base != "" {
		deepURL = strings.TrimRight(base, "/") + deepURL
	}
	return deepURL
}

// sanitizeField makes a user-controllable terminal field safe for a one-line
// notification: control chars / newlines / tabs → space, whitespace collapsed,
// then truncated. Prevents a crafted (or accidentally multi-line) session /
// window name / transcript from breaking the message layout or spamming the body.
func sanitizeField(s string) string { return sanitizeFieldMax(s, 48) }

// sanitizeFieldMax is sanitizeField with a caller-chosen length. Terminal-sourced text that
// is meant to be READ (an OSC notification body) needs more room than a name, but the
// control-character scrubbing must be identical — a notification body is just as
// user-controllable as a window name.
func sanitizeFieldMax(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}
