package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

// Background Web Push notifier.
//
// A server-global goroutine (independent of any browser tab) polls the tmux
// topology every notifyPollInterval, diffs each pane's AgentStatus, and on a
// transition INTO "waiting" (from any non-waiting state) sends one web push to
// every stored subscription.
//
// Lifecycle: gated on (tmux installed AND ≥1 subscription). Started by the first
// subscribe; stopped when the last subscription is removed. No busy-spin — the
// goroutine only exists while there is someone to notify.
//
// Dedupe: a pane that stays "waiting" across ticks does not re-fire. The pane
// leaves the fired set once it transitions away from "waiting", so the next
// waiting transition fires again.
//
// Prune: a push that fails with 404/410 (subscription gone) removes that
// subscription from the store.

const (
	// notifyPollInterval is how often the notifier recomputes tmux state.
	notifyPollInterval = 2 * time.Second
	// notifyStateTimeout bounds a single tmux topology computation.
	notifyStateTimeout = 3 * time.Second
	// pushSendTimeout bounds a single web push HTTP request.
	pushSendTimeout = 10 * time.Second
	// pushBodyLogLimit caps how much of a push service's error body we log/return.
	pushBodyLogLimit = 256
	// notifyPerPaneCooldown suppresses re-notifying the same pane within this window
	// (a chatty pane that flaps running→idle→running won't flood).
	notifyPerPaneCooldown = 2 * time.Minute
	// notifyCoalesceWindow buffers transitions briefly so several panes that finish
	// close together collapse into ONE merged notification.
	notifyCoalesceWindow = 5 * time.Second
)

// pushNotifier is the running poller. Owned by pushStore; one at a time.
type pushNotifier struct {
	server *Server
	cancel context.CancelFunc
	done   chan struct{}

	// Per-pane tracking (keyed by stable pane id "session:window:pane").
	prev         map[string]agentintel.AgentStatus // last status — drives running→idle/waiting transitions
	meta         map[string]paneMeta               // last-seen tool/cwd/location/transcript
	lastNotified map[string]time.Time              // per-pane cooldown
	pending      map[string]bool                   // panes triggered in the current (coalescing) batch
	pendingSince time.Time

	baseline  map[string]agentintel.SessionSummary // transcriptPath → summary at last notification (delta source)
	archived  map[string]archivedRec               // transcriptPath → closed-pane session (today-scoped)
	lastFlush time.Time
}

// ensureNotifier starts the background notifier if tmux is installed and not
// already running. Idempotent and safe to call from the subscribe handler.
func (s *pushStore) ensureNotifier() {
	if s.server == nil || !s.server.tmuxInstalled() {
		return
	}
	s.mu.Lock()
	if s.notifier != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &pushNotifier{
		server:       s.server,
		cancel:       cancel,
		done:         make(chan struct{}),
		prev:         map[string]agentintel.AgentStatus{},
		meta:         map[string]paneMeta{},
		lastNotified: map[string]time.Time{},
		pending:      map[string]bool{},
		baseline:     map[string]agentintel.SessionSummary{},
		archived:     map[string]archivedRec{},
	}
	s.notifier = n
	s.mu.Unlock()

	n.loadState() // restore per-pane status + delivery metrics before the loop starts
	go n.run(ctx)
	logger.Info("push notifier started")
}

// stopNotifier stops the background notifier if running. Idempotent.
func (s *pushStore) stopNotifier() {
	s.mu.Lock()
	n := s.notifier
	s.notifier = nil
	s.mu.Unlock()
	if n == nil {
		return
	}
	n.cancel()
	<-n.done
	logger.Info("push notifier stopped")
}

// run is the poll loop. It exits on ctx cancel.
func (n *pushNotifier) run(ctx context.Context) {
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

// tick polls the tmux topology, detects per-pane running→{idle,waiting} transitions
// (a turn just completed / a prompt appeared), buffers them through a per-pane
// cooldown + a short coalescing window, and tracks panes that vanish as "archived".
func (n *pushNotifier) tick(ctx context.Context) {
	provider := n.server.tmuxProvider
	if provider == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, notifyStateTimeout)
	raw, err := provider.TmuxState(cctx, 0)
	cancel()
	if err != nil || raw == nil {
		return
	}
	var st agentintel.TmuxState
	if json.Unmarshal(raw, &st) != nil {
		return
	}

	now := time.Now()
	pl := agentintel.NewProjectLocator()
	current := map[string]bool{}

	for _, sess := range st.Sessions {
		for _, win := range sess.Windows {
			for _, pane := range win.Panes {
				tool := string(pane.AgentTool)
				stt := pane.AgentStatus
				// Live = a pane with an agent in an interactive state. Others (no agent /
				// exited) fall through and become "archived" if previously tracked.
				if tool == "" || (stt != agentintel.StatusRunning && stt != agentintel.StatusIdle && stt != agentintel.StatusWaiting) {
					continue
				}
				id := fmt.Sprintf("%s:%d:%d", sess.Name, win.Index, pane.Index)
				current[id] = true
				n.meta[id] = paneMeta{
					tool: tool, cwd: pane.CWD, session: sess.Name,
					windowName: win.Name, window: win.Index, pane: pane.Index,
					transcriptPath: transcriptPath(pl, pane.CWD, tool),
				}
				prev := n.prev[id]
				n.prev[id] = stt
				// Trigger: the pane FINISHED working (running → idle/waiting). Excludes
				// fresh-start idle and startup-existing state (prev must be running).
				if prev == agentintel.StatusRunning && (stt == agentintel.StatusIdle || stt == agentintel.StatusWaiting) {
					if last, ok := n.lastNotified[id]; ok && now.Sub(last) < notifyPerPaneCooldown {
						continue // per-pane cooldown: don't re-notify within the window
					}
					if len(n.pending) == 0 {
						n.pendingSince = now
					}
					n.pending[id] = true
				}
			}
		}
	}

	// Panes that vanished (closed / agent exited) → archived, keyed by transcript.
	for id := range n.prev {
		if current[id] {
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

// flush computes the live + archived metrics, builds one (merged) notification with
// the stats body, sends it through the A/B coordinator, and advances the baseline.
func (n *pushNotifier) flush(now time.Time, pl *agentintel.ProjectLocator) {
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

	// Advance baseline + per-pane cooldowns, clear the batch.
	n.baseline = liveSumm
	for id := range n.pending {
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

// ── per-pane metadata + archived record + IO helpers ────────────────────────

type paneMeta struct {
	tool, cwd, session, windowName string
	window, pane                   int
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
	LastFlush time.Time                          `json:"lastFlush"`
	Metrics   notify.MetricsState               `json:"metrics"`
}

func (n *pushNotifier) statePath() string {
	return filepath.Join(n.server.config.DataDir, "notify-state.json")
}

func (n *pushNotifier) loadState() {
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
	}
	n.lastFlush = st.LastFlush
	if n.server.coordinator != nil {
		n.server.coordinator.RestoreMetrics(st.Metrics)
	}
}

func (n *pushNotifier) saveState() {
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
// path when no tunnel runs), plus a one-time bootstrap token in the URL FRAGMENT
// (#) for tap-to-auth. A fragment is never sent to the server, proxies, or in a
// Referer header, so the bearer token can't leak to tunnel/access logs.
func (s *Server) notifyDeepURL(session string) string {
	deepURL := "/?session=" + url.QueryEscape(session)
	if base := s.tunnel.PublicURL(); base != "" {
		deepURL = strings.TrimRight(base, "/") + deepURL
	}
	if s.bootstrap != nil {
		if tok := s.bootstrap.issue(); tok != "" {
			deepURL += "#bootstrap=" + tok
		}
	}
	return deepURL
}

// sanitizeField makes a user-controllable terminal field safe for a one-line
// notification: control chars / newlines / tabs → space, whitespace collapsed,
// then truncated. Prevents a crafted (or accidentally multi-line) session /
// window name / transcript from breaking the message layout or spamming the body.
func sanitizeField(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	const max = 48
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

// pushOutcomeKind classifies the result of a single send attempt.
type pushOutcomeKind int

const (
	// pushDelivered: the push service accepted the message (2xx).
	pushDelivered pushOutcomeKind = iota
	// pushGone: the subscription is dead (404/410) → prune it.
	pushGone
	// pushRejected: the service returned a non-2xx, non-gone status (e.g. Apple
	// 403 BadJwtToken). The message was NOT delivered; surfaced, never pruned.
	pushRejected
	// pushError: transport/library error before any HTTP status (no response).
	pushError
)

// pushOutcome is the typed result of one sendPush. status is the HTTP status
// (0 when kind==pushError); reason carries the service's error body / error text
// so callers can show WHY a push failed (Apple returns e.g. "BadJwtToken").
type pushOutcome struct {
	kind   pushOutcomeKind
	status int
	reason string
}

// sendPush delivers one notification and returns a typed outcome. It LOGS every
// non-2xx result (status + service error body + endpoint tail) so silent failures
// — the bug where Apple rejected a token but the UI said "sent" — are now visible.
func sendPush(payload []byte, sub pushSubscription, pub, priv, subscriber string) pushOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), pushSendTimeout)
	defer cancel()
	resp, err := webpush.SendNotificationWithContext(ctx, payload, sub.toWebpush(), &webpush.Options{
		Subscriber:      subscriber,
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		TTL:             60,
	})
	if err != nil {
		logger.Warn("push send error", "endpoint_tail", endpointTail(sub.Endpoint), "error", err)
		return pushOutcome{kind: pushError, reason: err.Error()}
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return pushOutcome{kind: pushDelivered, status: resp.StatusCode}
	}

	// Non-2xx: read the (short) descriptive body the push service returns, e.g.
	// Apple's "BadJwtToken". This is what makes a rejected push diagnosable.
	body := readBodySnippet(resp.Body)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		logger.Info("push gone", "endpoint_tail", endpointTail(sub.Endpoint),
			"status", resp.StatusCode, "body", body)
		return pushOutcome{kind: pushGone, status: resp.StatusCode, reason: body}
	}
	logger.Warn("push rejected", "endpoint_tail", endpointTail(sub.Endpoint),
		"status", resp.StatusCode, "body", body)
	return pushOutcome{kind: pushRejected, status: resp.StatusCode, reason: body}
}

// readBodySnippet reads at most pushBodyLogLimit bytes of a response body and
// trims it to a single tidy line for logs / API responses.
func readBodySnippet(r io.Reader) string {
	buf, _ := io.ReadAll(io.LimitReader(r, pushBodyLogLimit))
	return strings.TrimSpace(string(buf))
}
