package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
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
)

// pushNotifier is the running poller. Owned by pushStore; one at a time.
type pushNotifier struct {
	server *Server
	cancel context.CancelFunc
	done   chan struct{}

	// fired tracks panes currently in the "waiting" state we've already pushed
	// for, keyed by stable pane id ("session:window:pane"). Dedupes re-fires.
	fired map[string]bool
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
		server: s.server,
		cancel: cancel,
		done:   make(chan struct{}),
		fired:  map[string]bool{},
	}
	s.notifier = n
	s.mu.Unlock()

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

// tick computes the current tmux state, diffs waiting panes, and fires pushes
// for new waiting transitions.
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

	// Collect the set of panes currently waiting, then diff against fired.
	waitingNow := map[string]bool{}
	for _, sess := range st.Sessions {
		for _, win := range sess.Windows {
			for _, pane := range win.Panes {
				if pane.AgentStatus != agentintel.StatusWaiting {
					continue
				}
				id := fmt.Sprintf("%s:%d:%d", sess.Name, win.Index, pane.Index)
				waitingNow[id] = true
				if n.fired[id] {
					continue // already notified while it stays waiting → dedupe
				}
				n.fired[id] = true
				n.notify(notifyEvent{
					paneID:     id,
					tool:       string(pane.AgentTool),
					session:    sess.Name,
					window:     win.Index,
					windowName: win.Name,
					pane:       pane.Index,
					cwd:        pane.CWD,
				})
			}
		}
	}
	// Drop panes that left the waiting state so a future transition re-fires.
	for id := range n.fired {
		if !waitingNow[id] {
			delete(n.fired, id)
		}
	}
}

// notifyEvent carries the per-pane facts the message needs — the human-readable
// location (session/window/pane + names) and the running agent, NOT a URL.
type notifyEvent struct {
	paneID     string
	tool       string // claude / codex / …
	session    string // tmux session name (or terminal tab name for a plain session)
	window     int
	windowName string // the pane's human "name" (tmux pane titles are usually unset/noisy)
	pane       int
	cwd        string // for resolving the agent's transcript name
}

// notify is the channel-agnostic coordinator for ONE agent-waiting event (this is
// the single fan-out point; event detection + dedupe live in tick's fired set, so
// channels A/B never double-fire). It tries WeChat iLink (B) first; on delivery it
// does NOT also web-push (no double notify). On B dormant/ambiguous/not-configured
// it falls through to Web Push (A), the reliable APNs/FCM baseline.
func (n *pushNotifier) notify(ev notifyEvent) {
	// Human-readable content — WHICH agent + WHERE it waits + WHICH transcript. NO
	// URL: a raw link in the body is unreadable (the user's explicit ask), esp. in
	// WeChat. The cli type goes in the BODY (not only the title) so channels that
	// surface only the body still show claude/codex.
	title := "⏳ 需要你的输入"
	desc := describeEvent(ev, n.transcriptName(ev.cwd, ev.tool))
	// Deep-link is used ONLY as the web-push tap target (data.url), never shown as text.
	deepURL := n.server.notifyDeepURL(ev.session)

	// Channel B — WeChat iLink (primary when active). Plain readable text, NO URL.
	ilinkAttempted := false
	if il := n.server.ilink; il != nil {
		switch il.trySend(context.Background(), ev.paneID, title+"\n"+desc) {
		case ilinkSent:
			n.server.metrics.record("ilink", false)
			logger.Info("notify via ilink", "pane", ev.paneID)
			return // delivered to WeChat → skip web push
		case ilinkDormant, ilinkAmbiguous:
			ilinkAttempted = true // tried but parked/failed → real fallback to A
		default:
			// ilinkNotConfigured → B simply isn't set up; not a fallback.
		}
	}

	// Channel A — Web Push (baseline / fallback). body = readable; data.url = tap target.
	store := n.server.push
	subs := store.snapshot()
	if len(subs) == 0 {
		n.server.metrics.record("none", ilinkAttempted)
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"title": title,
		"body":  desc,
		"tag":   ev.paneID,
		"data": map[string]any{
			"url":       deepURL,
			"sessionId": ev.session,
		},
	})
	// Single source of truth for fan-out delivery + prune (shared with /push/test).
	res := store.broadcast(payload, subs)
	if res.delivered > 0 {
		n.server.metrics.record("webpush", ilinkAttempted)
	} else {
		n.server.metrics.record("none", ilinkAttempted)
	}
	logger.Info("push sent", "pane", ev.paneID, "subs", len(subs),
		"delivered", res.delivered, "rejected", len(res.rejected), "pruned", res.pruned)
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

// describeEvent builds the human-readable notification body: WHICH agent + WHERE
// it is (session / window + name / pane index) + WHICH transcript — no URL. e.g.
// "claude · main · 窗口1 editor · 面板0 · abc123.jsonl". For a plain terminal tab
// the session field is the tab name (e.g. "终端 2"), so the same shape reads
// naturally. All user-controllable fields are sanitized to a safe single line.
func describeEvent(ev notifyEvent, transcript string) string {
	tool := ev.tool
	if tool == "" {
		tool = "agent"
	}
	var b strings.Builder
	b.WriteString(tool)
	b.WriteString(" · ")
	b.WriteString(sanitizeField(ev.session))
	if wn := sanitizeField(ev.windowName); wn != "" {
		fmt.Fprintf(&b, " · 窗口%d %s", ev.window, wn)
	} else {
		fmt.Fprintf(&b, " · 窗口%d", ev.window)
	}
	fmt.Fprintf(&b, " · 面板%d", ev.pane)
	if t := sanitizeField(transcript); t != "" {
		b.WriteString(" · " + t)
	}
	return b.String()
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

// transcriptName resolves the running agent's transcript file name from its CWD
// (best effort; empty when not resolvable). NOTE: it picks the newest transcript
// under the project dir — accurate for the common one-session-per-dir case, but
// may misattribute when multiple same-tool sessions share a CWD. Called only on a
// waiting transition, so the one ReadDir stays off the 2s poll hot path.
func (n *pushNotifier) transcriptName(cwd, tool string) string {
	if cwd == "" {
		return ""
	}
	pl := agentintel.NewProjectLocator()
	switch tool {
	case "claude":
		if files, err := pl.ClaudeSessionFiles(cwd); err == nil && len(files) > 0 {
			return filepath.Base(files[0])
		}
	case "codex":
		if p := agentintel.CodexNewestRolloutForCWD(pl, cwd); p != "" {
			return filepath.Base(p)
		}
	}
	return ""
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
