package terminal

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Web Push backend: VAPID keypair (persisted), subscription store (persisted),
// and the three contract endpoints. The background notifier lives in
// push_notifier.go. Frontend depends on this exact contract:
//
//	GET  {api}/push/vapid       → { "publicKey": "<base64url>" }
//	POST {api}/push/subscribe   → 200  (PushSubscription + optional sessionId)
//	POST {api}/push/unsubscribe → 200  ({ endpoint })
//
// All three are behind authWrap, consistent with every other route (see server.go).

// vapidKeys is the persisted VAPID keypair. Generated once on first run and
// reused across restarts — critical so existing browser subscriptions stay
// valid (a new public key would silently invalidate them all).
type vapidKeys struct {
	PublicKey  string `json:"publicKey"`  // base64url, served to the frontend
	PrivateKey string `json:"privateKey"` // base64url, signing key (never leaves the server)
}

// pushSubscription is a W3C PushSubscription plus our optional sessionId.
// It is what the browser's PushManager.subscribe() yields, JSON-serialized.
type pushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	SessionID string `json:"sessionId,omitempty"`
	// Origin is the page origin (scheme://host) the subscription was created under, kept
	// as diagnostic metadata. NOTE: subscriptions are NOT dropped on origin change — Web
	// Push delivery goes via the push service (APNs/FCM), independent of the tunnel, so a
	// subscription keeps working after the tunnel URL changes. Tap-target liveness is
	// handled instead by deep-linking notifications to the CURRENT tunnel URL (see
	// pushNotifier.notify), not by unregistering subs.
	Origin string `json:"origin,omitempty"`
}

// toWebpush converts to the library's subscription shape.
func (p pushSubscription) toWebpush() *webpush.Subscription {
	return &webpush.Subscription{
		Endpoint: p.Endpoint,
		Keys: webpush.Keys{
			P256dh: p.Keys.P256dh,
			Auth:   p.Keys.Auth,
		},
	}
}

// pushStore owns the VAPID keypair, the subscription set, and the notifier
// lifecycle. All exported behavior is safe for concurrent use.
type pushStore struct {
	dir string

	// server backref, set by NewServer after construction. Lets the notifier
	// reach the tmux provider and the subscription set. Read-only after wiring.
	server *Server

	// subscriber is the VAPID "sub" claim used to sign every push token. Resolved
	// once at construction (Config → DW_VAPID_SUBSCRIBER → valid placeholder) and
	// read-only thereafter. MUST be a valid mailto:/https: URL or Apple rejects the
	// token with 403 BadJwtToken (see resolveVapidSubscriber).
	subscriber string

	mu     sync.Mutex
	vapid  vapidKeys
	subs   map[string]pushSubscription // dedupe by endpoint
	loaded bool

	// notifier handle (see push_notifier.go). nil when not running.
	notifier *pushNotifier
}

// newPushStore loads (or initializes) VAPID keys and subscriptions from disk.
// VAPID keys are generated and persisted on first run; subscriptions are loaded
// if present. Never returns nil — a load failure degrades to an empty store
// with freshly generated keys. subscriber is the VAPID "sub" claim (see
// resolveVapidSubscriber), wired in so every send signs with a valid contact URL.
func newPushStore(dataDir, subscriber string) *pushStore {
	ps := &pushStore{
		dir:        dataDir,
		subscriber: subscriber,
		subs:       map[string]pushSubscription{},
	}
	ps.loadVapid()
	ps.loadSubs()
	ps.loaded = true
	return ps
}

func (s *pushStore) vapidPath() string { return filepath.Join(s.dir, "vapid.json") }
func (s *pushStore) subsPath() string  { return filepath.Join(s.dir, "push_subs.json") }

// loadVapid reads vapid.json, or generates+persists a fresh keypair on miss.
// Caller need not hold the lock (only invoked from the constructor).
func (s *pushStore) loadVapid() {
	if data, err := os.ReadFile(s.vapidPath()); err == nil {
		var v vapidKeys
		if json.Unmarshal(data, &v) == nil && v.PublicKey != "" && v.PrivateKey != "" {
			s.vapid = v
			return
		}
	}
	// First run (or corrupt file): generate and persist.
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		// Extremely unlikely (crypto/rand failure). Leave keys empty; the vapid
		// endpoint will report unavailable rather than crash the server.
		logger.Error("vapid key generation failed", "error", err)
		return
	}
	s.vapid = vapidKeys{PublicKey: pub, PrivateKey: priv}
	s.persistVapid()
}

func (s *pushStore) persistVapid() {
	data, err := json.MarshalIndent(s.vapid, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(s.dir, 0755)
	// 0600: the private key is a signing secret.
	_ = os.WriteFile(s.vapidPath(), data, 0600)
}

// loadSubs reads push_subs.json into the in-memory set, deduped by endpoint.
func (s *pushStore) loadSubs() {
	data, err := os.ReadFile(s.subsPath())
	if err != nil {
		return
	}
	var list []pushSubscription
	if json.Unmarshal(data, &list) != nil {
		return
	}
	for _, sub := range list {
		if sub.Endpoint != "" {
			s.subs[sub.Endpoint] = sub
		}
	}
}

// persistSubs writes the current set to disk. Caller MUST hold s.mu.
func (s *pushStore) persistSubsLocked() {
	list := make([]pushSubscription, 0, len(s.subs))
	for _, sub := range s.subs {
		list = append(list, sub)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(s.dir, 0755)
	_ = os.WriteFile(s.subsPath(), data, 0644)
}

// PublicKey returns the VAPID public key (base64url), empty if unavailable.
func (s *pushStore) PublicKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vapid.PublicKey
}

// privateKey returns the VAPID private key for signing. Caller need not lock.
func (s *pushStore) privateKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vapid.PrivateKey
}

// snapshot returns a copy of the current subscriptions for sending without
// holding the lock across network I/O.
func (s *pushStore) snapshot() []pushSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pushSubscription, 0, len(s.subs))
	for _, sub := range s.subs {
		out = append(out, sub)
	}
	return out
}

// count returns the number of stored subscriptions.
func (s *pushStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// notifierRunning reports whether the background event poller is active.
func (s *pushStore) notifierRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifier != nil
}

// pushSubDetail is the non-sensitive view of one subscription for the UI detail
// popover: the page origin it was created under + a masked endpoint tail (never
// the full endpoint, which is a capability URL).
type pushSubDetail struct {
	Origin       string `json:"origin"`
	EndpointTail string `json:"endpointTail"`
}

// subsDetails returns a privacy-safe summary of each subscription.
func (s *pushStore) subsDetails() []pushSubDetail {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pushSubDetail, 0, len(s.subs))
	for _, sub := range s.subs {
		out = append(out, pushSubDetail{Origin: sub.Origin, EndpointTail: endpointTail(sub.Endpoint)})
	}
	return out
}

// add stores (or replaces) a subscription, deduped by endpoint, and persists.
// Returns the new subscription count.
func (s *pushStore) add(sub pushSubscription) int {
	s.mu.Lock()
	s.subs[sub.Endpoint] = sub
	n := len(s.subs)
	s.persistSubsLocked()
	s.mu.Unlock()
	return n
}

// remove deletes a subscription by endpoint and persists. Returns the new count.
func (s *pushStore) remove(endpoint string) int {
	s.mu.Lock()
	if _, ok := s.subs[endpoint]; ok {
		delete(s.subs, endpoint)
		s.persistSubsLocked()
	}
	n := len(s.subs)
	s.mu.Unlock()
	return n
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP handlers (FIXED CONTRACT — frontend depends on these shapes)
// ─────────────────────────────────────────────────────────────────────────────

// handlePushVAPID handles GET /push/vapid → { "publicKey": "<base64url>" }.
// Behind authWrap (consistent with all other GETs); the public key is not secret
// but keeping one auth model keeps the surface simple.
func (s *Server) handlePushVAPID(w http.ResponseWriter, r *http.Request) {
	pub := s.push.PublicKey()
	if pub == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "vapid unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": pub})
}

// handlePushSubscribe handles POST /push/subscribe.
// Body = PushSubscription JSON { endpoint, keys:{p256dh,auth} } + optional { sessionId }.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var sub pushSubscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "incomplete subscription"})
		return
	}
	// Bind the subscription to its page origin. Prefer the body field; fall back to the
	// request's Origin header (browsers send it on cross-origin and on same-origin POST).
	if sub.Origin == "" {
		sub.Origin = r.Header.Get("Origin")
	}
	n := s.push.add(sub)
	// A subscription now exists → ensure the background notifier is running.
	s.push.ensureNotifier()
	logger.Info("push subscribe", "endpoint_tail", endpointTail(sub.Endpoint), "count", n)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": n})
}

// handlePushTest handles POST /push/test → sends one canned web push to every
// stored subscription so the user can verify the full chain (permission →
// subscribe → SW push → OS notification) on demand, without waiting for an
// agent-waiting event. Reuses the notifier's send + 404/410 prune path.
//
// Returns 200 even with zero subscriptions (nothing to send is not an error;
// the frontend offers a local self-test in that case).
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	subs := s.push.snapshot()
	// Tap target: current tunnel URL (live) if up, else same-origin root.
	testURL := "/"
	if base := s.tunnel.PublicURL(); base != "" {
		testURL = base
	}
	payload, _ := json.Marshal(map[string]any{
		"title": "✅ 测试通知",
		"body":  "Deepwork 推送已就绪",
		"tag":   "dw-test",
		"data":  map[string]any{"url": testURL},
	})
	res := s.push.broadcast(payload, subs)
	logger.Info("push test", "subs", len(subs),
		"delivered", res.delivered, "rejected", len(res.rejected), "pruned", res.pruned)
	// Diagnostic shape: `sent` = delivered (2xx) count; `rejected` lists each
	// non-delivered attempt with its status+reason so a 403 BadJwtToken is visible
	// in the response (not a silent "sent"). `ok` stays true (the request itself
	// succeeded); the frontend inspects sent/rejected to give honest feedback.
	if res.rejected == nil {
		res.rejected = []rejectedSend{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"sent":     res.delivered,
		"rejected": res.rejected,
		"pruned":   res.pruned,
	})
}

// rejectedSend is one non-delivered, non-gone attempt, surfaced to the caller so
// the failure is visible (status + reason from the push service, e.g. 403 BadJwtToken).
type rejectedSend struct {
	Status int    `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// broadcastResult is the SSOT outcome of a single fan-out: how many were delivered
// (2xx), which were rejected (with status+reason), and how many gone subs were pruned.
type broadcastResult struct {
	delivered int
	rejected  []rejectedSend
	pruned    int
}

// broadcast sends payload to every subscription concurrently, prunes any that
// report gone (404/410), and returns a typed result counting delivered vs rejected.
// The single source of truth for fan-out delivery — both the test endpoint and the
// background notifier route here via sendPush. Caller passes a snapshot so we never
// hold the lock across I/O.
func (s *pushStore) broadcast(payload []byte, subs []pushSubscription) broadcastResult {
	if len(subs) == 0 {
		return broadcastResult{}
	}
	pub := s.PublicKey()
	priv := s.privateKey()
	if pub == "" || priv == "" {
		return broadcastResult{}
	}
	var (
		prune     []string
		rejected  []rejectedSend
		delivered int
		wg        sync.WaitGroup
		mu        sync.Mutex
	)
	for _, sub := range subs {
		wg.Add(1)
		go func(sub pushSubscription) {
			defer wg.Done()
			out := sendPush(payload, sub, pub, priv, s.subscriber)
			mu.Lock()
			switch out.kind {
			case pushDelivered:
				delivered++
			case pushGone:
				prune = append(prune, sub.Endpoint)
			case pushRejected, pushError:
				rejected = append(rejected, rejectedSend{Status: out.status, Reason: out.reason})
			}
			mu.Unlock()
		}(sub)
	}
	wg.Wait()
	for _, ep := range prune {
		s.remove(ep)
		logger.Info("push pruned gone subscription", "endpoint_tail", endpointTail(ep))
	}
	// Stop the poller only when NO channel needs it: no web-push subs AND iLink not
	// logged in. Otherwise an iLink-only user would lose their event source.
	if s.count() == 0 && !(s.server != nil && s.server.ilink.loggedIn()) {
		go s.stopNotifier()
	}
	return broadcastResult{delivered: delivered, rejected: rejected, pruned: len(prune)}
}

// handlePushUnsubscribe handles POST /push/unsubscribe. Body = { endpoint }.
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint required"})
		return
	}
	n := s.push.remove(req.Endpoint)
	// Last subscription gone → stop the notifier — UNLESS iLink is logged in and
	// still needs the shared event poller.
	if n == 0 && !(s.ilink != nil && s.ilink.loggedIn()) {
		s.push.stopNotifier()
	}
	logger.Info("push unsubscribe", "endpoint_tail", endpointTail(req.Endpoint), "count", n)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": n})
}

// endpointTail returns a short, non-sensitive suffix of an endpoint for logging.
func endpointTail(endpoint string) string {
	if len(endpoint) <= 12 {
		return endpoint
	}
	return "…" + endpoint[len(endpoint)-12:]
}
