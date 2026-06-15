package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
					paneID:  id,
					tool:    string(pane.AgentTool),
					session: sess.Name,
					window:  win.Index,
					pane:    pane.Index,
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

// notifyEvent carries the per-pane facts the payload needs.
type notifyEvent struct {
	paneID  string
	tool    string
	session string
	window  int
	pane    int
}

// notify builds the SW payload and sends it to every stored subscription.
// Failed-gone (404/410) subscriptions are pruned.
func (n *pushNotifier) notify(ev notifyEvent) {
	store := n.server.push
	subs := store.snapshot()
	if len(subs) == 0 {
		return
	}

	tool := ev.tool
	if tool == "" {
		tool = "agent"
	}
	// data.url deep-links the SW back to the session via query param.
	deepURL := "/?session=" + url.QueryEscape(ev.session)

	payload, _ := json.Marshal(map[string]any{
		"title": "⏳ 需要你的输入",
		"body":  fmt.Sprintf("%s · %s:%d 正在等待输入", tool, ev.session, ev.window),
		"tag":   ev.paneID,
		"data": map[string]any{
			"url":       deepURL,
			"sessionId": ev.session,
		},
	})

	// Single source of truth for fan-out delivery + prune (shared with /push/test).
	res := store.broadcast(payload, subs)
	logger.Info("push sent", "pane", ev.paneID, "subs", len(subs),
		"delivered", res.delivered, "rejected", len(res.rejected), "pruned", res.pruned)
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
