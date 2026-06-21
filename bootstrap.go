package terminal

// One-time bootstrap tokens for tap-to-auth from a push notification.
//
// Problem (codex high-6): the legacy `?auth=<code>` deep link puts the LONG-LIVED
// auth code straight into a URL, which leaks to browser history, reverse-proxy
// logs, and Referer headers — and stays valid forever.
//
// Fix: a notification's deep link instead carries a fresh, SINGLE-USE,
// short-TTL bootstrap token. The page exchanges it (GET /auth/bootstrap) for the
// auth code once; the token is consumed on first use and expires quickly, so a
// leaked URL is worthless. Only the device that received the push ever sees the
// token, so possession is a reasonable proof of ownership for a single-user tool.

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const bootstrapTTL = 5 * time.Minute

// bootstrapMaxOutstanding caps the in-memory token map so a pathological burst of
// notifications can't grow it without bound (codex low-finding). Far above any real
// single-user rate: ~one token per agent-waiting event within a 5-minute TTL.
const bootstrapMaxOutstanding = 4096

type bootstrapStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token → expiry
}

func newBootstrapStore() *bootstrapStore {
	return &bootstrapStore{tokens: map[string]time.Time{}}
}

// issue mints a fresh one-time bootstrap token valid for bootstrapTTL. Returns ""
// only on the (cryptographically unreachable) rand failure.
func (b *bootstrapStore) issue() string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	tok := hex.EncodeToString(raw)
	b.mu.Lock()
	b.pruneLocked()
	if len(b.tokens) >= bootstrapMaxOutstanding {
		// Anomalous burst: refuse to grow unbounded. The notification still fires;
		// it just won't carry a tap-to-auth token this time (manual auth still works).
		b.mu.Unlock()
		logger.Warn("bootstrap token store at cap; skipping issue", "cap", bootstrapMaxOutstanding)
		return ""
	}
	b.tokens[tok] = nowFunc().Add(bootstrapTTL)
	b.mu.Unlock()
	return tok
}

// consume validates and SINGLE-USES a token: it returns true at most once per
// token, and only before the token has expired. The entry is removed on any
// lookup hit (used or expired) so a token can never be replayed.
func (b *bootstrapStore) consume(tok string) bool {
	if tok == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	exp, ok := b.tokens[tok]
	if !ok {
		return false
	}
	delete(b.tokens, tok)
	return nowFunc().Before(exp)
}

func (b *bootstrapStore) pruneLocked() {
	now := nowFunc()
	for t, exp := range b.tokens {
		if now.After(exp) {
			delete(b.tokens, t)
		}
	}
}

// handleAuthBootstrap → GET /auth/bootstrap?token=<tok>. NOT behind authWrap: it
// authenticates by consuming a one-time token embedded in a push notification's
// deep link. On success it returns the auth code so the frontend stores it exactly
// as a manual entry would — but the URL only ever carried a single-use token, not
// the long-lived secret.
func (s *Server) handleAuthBootstrap(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if s.bootstrap == nil || !s.bootstrap.consume(tok) {
		// Log rejected redemptions so brute-force / replay attempts are visible.
		logger.Info("bootstrap exchange rejected", "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		return
	}
	// NOTE: this is a BEARER endpoint — possession of a valid one-time token is
	// treated as full auth for this single-user tool, returning the auth code the
	// frontend stores exactly as a manual entry would. Acceptable because the token
	// only ever reaches the device that received the push.
	writeJSON(w, http.StatusOK, map[string]string{"authCode": s.config.AuthCode})
}
