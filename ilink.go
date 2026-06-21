package terminal

// WeChat iLink notification channel (channel B).
//
// A second, independent notification transport alongside Web Push (channel A,
// see push.go / push_notifier.go). It bridges agent-waiting events to the user's
// personal WeChat via the OFFICIAL ClawBot / iLink protocol (HTTP/JSON against
// ilinkai.weixin.qq.com) using github.com/the-yex/wechat-ilink-sdk — NOT the
// banned itchat/wechaty web-wechat protocols, so there is zero ban risk.
//
// Design (see docs/product/notify/spec.md §4A — upgraded after a codex design
// review that flagged "B was specced as an event list, not a state machine"):
//
//   - ilinkStore is the SINGLE owner of channel-B state. One record, one mutex,
//     atomic encrypted writes. The SDK login token is persisted via an encrypted
//     TokenStore; our own window/quota/dormant state lives in ilink.json.
//   - Proactive push requires a context_token, which the protocol only hands out
//     WITH an inbound user message. We capture it on every inbound (inboundRefresh),
//     persist it, and reuse it to push later (trySend) — across restarts too
//     (SetContextToken on resume). After 24h of no inbound, or 10 sends, the window
//     closes; we go dormant and fall back to channel A.
//   - Quota self-counting: the protocol caps ~10 server pushes per inbound window
//     and exposes no remaining-quota API, so we count ourselves and append a
//     "reply any character to renew" hint to sends 7..10. A user reply re-seeds the
//     context_token, resets the counter, and refreshes the 24h window.
//
// Invariants enforced in code (not doc-only): context_token, sentCount and
// windowStart live in ONE record mutated under ONE mutex, so the counter can never
// desync from the token; a send that definitively fails marks the channel dormant.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	ilinksdk "github.com/the-yex/wechat-ilink-sdk"
	"github.com/the-yex/wechat-ilink-sdk/ilink"
	"github.com/the-yex/wechat-ilink-sdk/login"
)

const (
	ilinkSchemaVer   = 1
	ilinkWindowTTL   = 24 * time.Hour // inbound context_token validity (△ observe to confirm)
	ilinkMaxSends    = 10             // server pushes per window (△ user-found cap; observe to confirm)
	ilinkRenewalAt   = 7              // append "reply to renew" hint starting at this send (7..10)
	ilinkSendTimeout = 15 * time.Second
)

// ilinkState is the single persisted record for channel B. Every field is mutated
// only under ilinkStore.mu and serialized together (encrypted) so no partial /
// inconsistent state (e.g. counter ahead of token) can be constructed.
type ilinkState struct {
	SchemaVer    int       `json:"schema_ver"`
	LoggedIn     bool      `json:"logged_in"`
	UserID       string    `json:"user_id"`       // who scanned (SendText target + context-token key)
	AccountID    string    `json:"account_id"`    // bot account id
	ContextToken string    `json:"context_token"` // last inbound token, reused for proactive push (+ restart restore)
	WindowStart  time.Time `json:"window_start"`  // when the current 24h window began (last inbound)
	SentCount    int       `json:"sent_count"`    // pushes since last inbound (quota vs ilinkMaxSends)
	Dormant      string    `json:"dormant_reason"` // non-empty → channel parked, events fall back to A
}

// ilinkOutcome is the result the notify coordinator uses to decide fallback.
type ilinkOutcome int

const (
	ilinkNotConfigured ilinkOutcome = iota // never logged in / disabled → channel A handles the event
	ilinkSent                              // delivered to WeChat → do NOT also web-push (no double notify)
	ilinkDormant                           // window/quota/session gone → fall back to channel A
	ilinkAmbiguous                         // network/unknown error → fall back to A (at-least-once)
)

// ilinkQR is a pending login QR exposed to the frontend.
type ilinkQR struct {
	PNGDataURL string    `json:"dataUrl"`
	StartedAt  time.Time `json:"startedAt"`
}

// ilinkStore owns all channel-B state and the SDK client lifecycle.
type ilinkStore struct {
	dir    string
	server *Server
	key    []byte // AES-256 key (ilink.key, 0600) for at-rest encryption of state + login token

	mu      sync.Mutex
	state   ilinkState
	client  *ilinksdk.Client
	sendFn  func(ctx context.Context, toUserID, text string) error // = client.SendText; injectable for tests
	cancel  context.CancelFunc
	running bool
	qr      *ilinkQR // current login QR (nil once logged in / no login in progress)
}

// newIlinkStore loads persisted state and, if a prior login exists, resumes the
// long-poll client so a stored session keeps receiving and can push immediately.
func newIlinkStore(dataDir string, srv *Server) *ilinkStore {
	s := &ilinkStore{dir: dataDir, server: srv}
	s.key = loadOrCreateIlinkKey(dataDir)
	s.loadState()
	if s.state.LoggedIn {
		s.ensureStarted()
	}
	return s
}

func (s *ilinkStore) statePath() string             { return filepath.Join(s.dir, "ilink.json") }
func (s *ilinkStore) tokenPath(acct string) string  { return filepath.Join(s.dir, "ilink-tok-"+sanitizeAcct(acct)+".enc") }
func (s *ilinkStore) keyPath() string               { return filepath.Join(s.dir, "ilink.key") }

func sanitizeAcct(acct string) string {
	if acct == "" {
		return "default"
	}
	out := make([]rune, 0, len(acct))
	for _, r := range acct {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// ── persistence (encrypted, atomic) ─────────────────────────────────────────

func (s *ilinkStore) loadState() {
	raw, err := os.ReadFile(s.statePath())
	if err != nil {
		return // first run → zero state (logged out)
	}
	plain, err := s.open(raw)
	if err != nil {
		logger.Warn("ilink state decrypt failed → treating as logged out", "error", err)
		return
	}
	var st ilinkState
	if json.Unmarshal(plain, &st) != nil {
		logger.Warn("ilink state corrupt → treating as logged out")
		return
	}
	s.state = st
}

// persistLocked writes the current state encrypted + atomically. Caller holds s.mu.
func (s *ilinkStore) persistLocked() {
	s.state.SchemaVer = ilinkSchemaVer
	plain, err := json.Marshal(s.state)
	if err != nil {
		return
	}
	enc, err := s.seal(plain)
	if err != nil {
		logger.Error("ilink state seal failed", "error", err)
		return
	}
	if err := ilinkAtomicWrite(s.statePath(), enc, 0600); err != nil {
		logger.Error("ilink state write failed", "error", err)
	}
}

// ── crypto (AES-256-GCM with a local 0600 keyfile) ──────────────────────────
//
// Threat model (honest, per spec §4A): the key lives 0600 next to the ciphertext,
// so against a local-root attacker this is ~equivalent to 0600. It protects against
// casual file reads, backups, and accidental disclosure — not against root on the
// host. This matches the accepted "0600 + 加密 够用" decision for a single-user
// self-hosted tool.

func loadOrCreateIlinkKey(dir string) []byte {
	p := filepath.Join(dir, "ilink.key")
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		logger.Error("ilink key generation failed", "error", err)
		return key
	}
	_ = os.MkdirAll(dir, 0700)
	if err := ilinkAtomicWrite(p, key, 0600); err != nil {
		logger.Error("ilink key write failed", "error", err)
	}
	return key
}

// seal/open delegate to the shared AES-GCM helpers (notify_providers.go) so the
// iLink token store and the notify config store use one encryption path (one key).
func (s *ilinkStore) seal(plain []byte) ([]byte, error) { return aesgcmSeal(s.key, plain) }
func (s *ilinkStore) open(data []byte) ([]byte, error) { return aesgcmOpen(s.key, data) }

// ilinkAtomicWrite writes data to a temp file then renames it into place so a
// crash mid-write never leaves a half-written (and now undecryptable) file.
func ilinkAtomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ilink-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck — no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ── encrypted login-token store (implements login.TokenStore) ───────────────

type ilinkTokenStore struct{ s *ilinkStore }

func (t ilinkTokenStore) Save(accountID string, tok *login.TokenInfo) error {
	plain, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	enc, err := t.s.seal(plain)
	if err != nil {
		return err
	}
	return ilinkAtomicWrite(t.s.tokenPath(accountID), enc, 0600)
}

func (t ilinkTokenStore) Load(accountID string) (*login.TokenInfo, error) {
	raw, err := os.ReadFile(t.s.tokenPath(accountID))
	if err != nil {
		return nil, err // missing → SDK treats as "no stored token"
	}
	plain, err := t.s.open(raw)
	if err != nil {
		return nil, err
	}
	var tok login.TokenInfo
	if err := json.Unmarshal(plain, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func (t ilinkTokenStore) Delete(accountID string) error {
	err := os.Remove(t.s.tokenPath(accountID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (t ilinkTokenStore) List() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(t.s.dir, "ilink-tok-*.enc"))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		base := filepath.Base(m)
		acct := base[len("ilink-tok-") : len(base)-len(".enc")]
		out = append(out, acct)
	}
	return out, nil
}

// ── lifecycle ───────────────────────────────────────────────────────────────

// ensureStarted builds the SDK client (encrypted token store, QR + login hooks,
// inbound handler) and runs the long-poll loop in a goroutine. Idempotent.
func (s *ilinkStore) ensureStarted() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	resumeUser, resumeTok := s.state.UserID, s.state.ContextToken
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	store := ilinkTokenStore{s: s}
	sdkLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var client *ilinksdk.Client
	client, err := ilinksdk.NewClient(
		ilinksdk.WithTokenStore(store),
		ilinksdk.WithLogger(sdkLogger),
		ilinksdk.WithOnLogin(func(_ context.Context, qr *login.QRCode) error {
			s.stashQR(qr)
			return nil
		}),
		ilinksdk.WithOnLoginSuccess(func(_ context.Context, res *ilink.LoginResult) error {
			s.onLoginSuccess(res)
			return nil
		}),
		ilinksdk.WithOnSessionExpired(func(c context.Context) (*ilink.LoginResult, error) {
			return client.Login(c, func(_ context.Context, qr *login.QRCode) error {
				s.stashQR(qr)
				return nil
			})
		}),
	)
	if err != nil {
		logger.Error("ilink client init failed", "error", err)
		cancel()
		s.setRunning(false)
		return
	}

	s.mu.Lock()
	s.client = client
	s.sendFn = client.SendText
	s.cancel = cancel
	s.mu.Unlock()

	// Restore the last inbound context token so proactive pushes work right after
	// a restart (until the 24h window lapses) without waiting for a new inbound.
	if resumeUser != "" && resumeTok != "" {
		client.SetContextToken(resumeUser, resumeTok)
	}

	// Inbound = the only moment we (re)gain a context_token. Capture it, reset the
	// window, then send a counted ack so the user sees the channel is live/renewed.
	client.OnText(func(c context.Context, msg *ilink.Message, _ string) error {
		s.inboundRefresh(msg.FromUserID, msg.ContextToken)
		s.trySend(c, "ilink-ack", "✅ 微信通知已激活。本轮约可推送 "+fmt.Sprint(ilinkMaxSends)+" 条；配额将尽时我会提示你回复任意字符续订。")
		return nil
	})

	go func() {
		defer s.setRunning(false)
		if err := client.Run(ctx, nil); err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("ilink run exited", "error", err)
		}
	}()

	// A logged-in/resumable channel means there is someone to notify → make sure
	// the shared event poller is running even if Web Push has no subscriptions.
	if s.server != nil && s.server.push != nil {
		s.server.push.ensureNotifier()
	}
	logger.Info("ilink client started")
}

func (s *ilinkStore) setRunning(v bool) {
	s.mu.Lock()
	s.running = v
	s.mu.Unlock()
}

func (s *ilinkStore) stashQR(qr *login.QRCode) {
	// Encode the WeChat login URL (qr.ImageURL, e.g. https://liteapp.weixin.qq.com/q/…),
	// NOT qr.Content (a raw token). WeChat only acts on the URL — scanning the bare
	// token just shows the string. Mirrors the SDK's own terminal QR (login.go:88).
	scan := qr.ImageURL
	if scan == "" {
		scan = qr.Content
	}
	png, err := qrcode.Encode(scan, qrcode.Medium, 256)
	if err != nil {
		logger.Warn("ilink qr encode failed", "error", err)
		return
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	s.mu.Lock()
	s.qr = &ilinkQR{PNGDataURL: dataURL, StartedAt: qr.StartedAt}
	s.mu.Unlock()
	logger.Info("ilink login qr ready")
}

func (s *ilinkStore) onLoginSuccess(res *ilink.LoginResult) {
	s.mu.Lock()
	s.state.LoggedIn = true
	s.state.UserID = res.UserID
	s.state.AccountID = res.AccountID
	s.qr = nil
	s.persistLocked()
	s.mu.Unlock()
	logger.Info("ilink login success", "user", res.UserID)
}

// inboundRefresh captures the context_token from an inbound message, resets the
// send window/quota, and clears any dormant flag. The SINGLE writer of these
// fields together → counter can never desync from the token.
func (s *ilinkStore) inboundRefresh(userID, ctxToken string) {
	s.mu.Lock()
	s.state.LoggedIn = true
	if userID != "" {
		s.state.UserID = userID
	}
	if ctxToken != "" {
		s.state.ContextToken = ctxToken
	}
	s.state.WindowStart = nowFunc()
	s.state.SentCount = 0
	s.state.Dormant = ""
	client := s.client
	s.persistLocked()
	s.mu.Unlock()

	if client != nil && userID != "" && ctxToken != "" {
		client.SetContextToken(userID, ctxToken)
	}
	// Someone is now reachable → ensure the poller runs.
	if s.server != nil && s.server.push != nil {
		s.server.push.ensureNotifier()
	}
}

// nowFunc is overridable in tests for deterministic window/TTL assertions.
var nowFunc = time.Now

// ── send path (the coordinator calls trySend) ───────────────────────────────

// trySend attempts one proactive push to WeChat. All gating + the counter
// increment happen atomically under the lock; only the network SendText runs
// unlocked. Returns the outcome the coordinator uses to decide channel-A fallback.
func (s *ilinkStore) trySend(ctx context.Context, eventID, text string) ilinkOutcome {
	s.mu.Lock()
	switch {
	case !s.state.LoggedIn || s.sendFn == nil:
		s.mu.Unlock()
		return ilinkNotConfigured
	case s.state.UserID == "" || s.state.ContextToken == "":
		s.mu.Unlock()
		return ilinkDormant // logged in but no inbound seed yet → user must message the bot
	case s.state.Dormant != "":
		s.mu.Unlock()
		return ilinkDormant
	case nowFunc().Sub(s.state.WindowStart) > ilinkWindowTTL:
		s.markDormantLocked("window expired (>24h, no inbound)")
		s.mu.Unlock()
		return ilinkDormant
	case s.state.SentCount >= ilinkMaxSends:
		s.markDormantLocked("quota exhausted (>=10 per window)")
		s.mu.Unlock()
		return ilinkDormant
	}
	seq := s.state.SentCount + 1
	s.state.SentCount = seq
	toUser := s.state.UserID
	send := s.sendFn
	s.persistLocked()
	s.mu.Unlock()

	full := text
	if seq >= ilinkRenewalAt {
		full = fmt.Sprintf("%s\n\n（本轮已发 %d/%d，回复任意字符可续订通知）", text, seq, ilinkMaxSends)
	}

	cctx, cancel := context.WithTimeout(ctx, ilinkSendTimeout)
	defer cancel()
	err := send(cctx, toUser, full)
	if err == nil {
		return ilinkSent
	}
	if errors.Is(err, ilinksdk.ErrSessionExpired) || errors.Is(err, ilinksdk.ErrSessionPaused) {
		s.markDormant("session expired/paused: " + err.Error())
		return ilinkDormant
	}
	logger.Warn("ilink send ambiguous → falling back to web push", "event", eventID, "error", err)
	return ilinkAmbiguous
}

func (s *ilinkStore) markDormant(reason string) {
	s.mu.Lock()
	s.markDormantLocked(reason)
	s.mu.Unlock()
}

func (s *ilinkStore) markDormantLocked(reason string) {
	s.state.Dormant = reason
	s.persistLocked()
	logger.Info("ilink channel dormant", "reason", reason)
}

// loggedIn reports whether a session exists (used to gate the shared notifier).
func (s *ilinkStore) loggedIn() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LoggedIn
}

// logout tears down the client and clears all persisted channel-B state.
func (s *ilinkStore) logout() {
	s.mu.Lock()
	client := s.client
	cancel := s.cancel
	acct := s.state.AccountID
	s.state = ilinkState{}
	s.qr = nil
	s.client = nil
	s.cancel = nil
	s.persistLocked()
	s.mu.Unlock()

	if client != nil {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Logout(ctx)
		c()
	}
	if cancel != nil {
		cancel()
	}
	_ = ilinkTokenStore{s: s}.Delete(acct)
	_ = ilinkTokenStore{s: s}.Delete("")
	logger.Info("ilink logged out")
}

// statusSnapshot is the frontend-facing view of channel-B state.
type ilinkStatus struct {
	LoggedIn    bool   `json:"loggedIn"`
	Active      bool   `json:"active"` // can push right now (logged in, seeded, not dormant, within window)
	Dormant     string `json:"dormant,omitempty"`
	SentCount   int    `json:"sentCount"`
	MaxSends    int    `json:"maxSends"`
	UserID      string `json:"userId,omitempty"`
	QRPending   bool   `json:"qrPending"`
}

func (s *ilinkStore) status() ilinkStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.state.LoggedIn && s.state.UserID != "" && s.state.ContextToken != "" &&
		s.state.Dormant == "" && nowFunc().Sub(s.state.WindowStart) <= ilinkWindowTTL &&
		s.state.SentCount < ilinkMaxSends
	return ilinkStatus{
		LoggedIn:  s.state.LoggedIn,
		Active:    active,
		Dormant:   s.state.Dormant,
		SentCount: s.state.SentCount,
		MaxSends:  ilinkMaxSends,
		UserID:    s.state.UserID,
		QRPending: s.qr != nil,
	}
}

func (s *ilinkStore) currentQR() *ilinkQR {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qr
}

// ── HTTP handlers (behind authWrap, consistent with /push/*) ────────────────

// handleIlinkStatus → GET /ilink/status.
func (s *Server) handleIlinkStatus(w http.ResponseWriter, r *http.Request) {
	if s.ilink == nil {
		writeJSON(w, http.StatusOK, ilinkStatus{MaxSends: ilinkMaxSends})
		return
	}
	writeJSON(w, http.StatusOK, s.ilink.status())
}

// handleIlinkLogin → POST /ilink/login. Starts the client; the QR appears via
// /ilink/qr (and status.qrPending) once the SDK requests a scan.
func (s *Server) handleIlinkLogin(w http.ResponseWriter, r *http.Request) {
	if s.ilink == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ilink unavailable"})
		return
	}
	s.ilink.ensureStarted()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleIlinkQR → GET /ilink/qr. 204 when no login is in progress.
func (s *Server) handleIlinkQR(w http.ResponseWriter, r *http.Request) {
	if s.ilink == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	qr := s.ilink.currentQR()
	if qr == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, qr)
}

// handleIlinkLogout → POST /ilink/logout.
func (s *Server) handleIlinkLogout(w http.ResponseWriter, r *http.Request) {
	if s.ilink != nil {
		s.ilink.logout()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
