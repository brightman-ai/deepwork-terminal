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
// with freshly generated keys.
func newPushStore(dataDir string) *pushStore {
	ps := &pushStore{
		dir:  dataDir,
		subs: map[string]pushSubscription{},
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
	n := s.push.add(sub)
	// A subscription now exists → ensure the background notifier is running.
	s.push.ensureNotifier()
	logger.Info("push subscribe", "endpoint_tail", endpointTail(sub.Endpoint), "count", n)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": n})
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
	// Last subscription gone → stop the notifier (no busy-spin when nobody listens).
	if n == 0 {
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
