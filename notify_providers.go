package terminal

// Terminal-side wiring of the host-agnostic notify package: the encrypted local
// ConfigStore, the iLink + web-push Provider adapters (the two channels that are
// terminal-specific, wrapping existing stores), and the Coordinator that fans an
// Event out to every enabled channel (iLink / web push / Feishu / DingTalk / WeCom / Slack).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/brightman-ai/deepwork-terminal/notify"
)

// ── encrypted local ConfigStore (implements notify.Store) ───────────────────────

type fileConfigStore struct {
	mu   sync.Mutex
	path string
	key  []byte
}

func newFileConfigStore(dataDir string) *fileConfigStore {
	return &fileConfigStore{
		path: filepath.Join(dataDir, "notify-config.json.enc"),
		key:  loadOrCreateIlinkKey(dataDir), // reuse the same 0600 machine key as iLink
	}
}

// defaultNotifyConfig is the out-of-box config: the two channels the user already
// has (WeChat iLink + web push) ON — matching "默认只开 1-2 个 provider" — and the
// webhook IM channels OFF until configured. Fan-out of two channels the user owns is
// the behavior they chose (decision ①), not an accidental double-buzz.
func defaultNotifyConfig() notify.Config {
	return notify.Config{
		Version: notify.ConfigVersion,
		Providers: []notify.ProviderConfig{
			{Kind: "ilink", Enabled: true},
			{Kind: "webpush", Enabled: true},
			{Kind: "feishu", Enabled: false},
			{Kind: "dingtalk", Enabled: false},
			{Kind: "wecom", Enabled: false},
			{Kind: "slack", Enabled: false},
		},
	}
}

func (s *fileConfigStore) Load(ctx context.Context) (notify.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := os.ReadFile(s.path)
	if err != nil {
		return defaultNotifyConfig(), nil // first run → defaults
	}
	plain, err := aesgcmOpen(s.key, enc)
	if err != nil {
		logger.Warn("notify config decrypt failed, using defaults", "error", err)
		return defaultNotifyConfig(), nil // corrupt → defaults, never crash
	}
	var c notify.Config
	if json.Unmarshal(plain, &c) != nil || len(c.Providers) == 0 {
		return defaultNotifyConfig(), nil
	}
	return c, nil
}

func (s *fileConfigStore) Save(ctx context.Context, c notify.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plain, err := json.Marshal(c)
	if err != nil {
		return err
	}
	enc, err := aesgcmSeal(s.key, plain)
	if err != nil {
		return err
	}
	return ilinkAtomicWrite(s.path, enc, 0600)
}

// ── iLink provider (wraps the existing ilinkStore) ──────────────────────────────

type ilinkProvider struct{ s *ilinkStore }

func (p ilinkProvider) Kind() string { return "ilink" }
func (p ilinkProvider) Name() string { return "微信" }

func (p ilinkProvider) Send(ctx context.Context, e notify.Event, cfg notify.ProviderConfig) (notify.Outcome, string) {
	if p.s == nil {
		return notify.OutcomeNotConfigured, ""
	}
	tag := "dw-notify"
	if e.Kind == notify.KindTest {
		tag = "dw-notify-test"
	}
	text := e.Title + "\n\n" + notify.PlainText(e)
	switch p.s.trySend(ctx, tag, text) {
	case ilinkSent:
		return notify.OutcomeSent, ""
	case ilinkDormant:
		reason := "休眠 — 回微信任意字符续种"
		if st := p.s.status(); st.Dormant != "" {
			reason = "休眠: " + st.Dormant
		}
		return notify.OutcomeDormant, reason
	case ilinkAmbiguous:
		return notify.OutcomeFailed, "投递结果未知(网络)" // honest: unknown delivery → not claimed as sent
	default:
		return notify.OutcomeNotConfigured, "未连接"
	}
}

func (p ilinkProvider) Status(ctx context.Context, cfg notify.ProviderConfig) notify.Status {
	st := ilinkStatus{MaxSends: ilinkMaxSends}
	if p.s != nil {
		st = p.s.status()
	}
	hint := ""
	switch {
	case !st.LoggedIn:
		hint = "未连接 — 用微信扫码"
	case st.Dormant != "":
		hint = "已休眠 — 回微信任意字符恢复"
	case st.SentCount >= st.MaxSends-2 && st.MaxSends > 0:
		hint = "配额将尽，回微信任意字符续订"
	}
	return notify.Status{
		Name:           "微信",
		Configured:     st.LoggedIn,
		Healthy:        st.Active,
		Quota:          &notify.Quota{Used: st.SentCount, Max: st.MaxSends},
		ActivationHint: hint,
	}
}

// ── web-push provider (wraps the existing pushStore) ────────────────────────────

type webpushProvider struct{ s *Server }

func (p webpushProvider) Kind() string { return "webpush" }
func (p webpushProvider) Name() string { return "浏览器" }

func (p webpushProvider) Send(ctx context.Context, e notify.Event, cfg notify.ProviderConfig) (notify.Outcome, string) {
	subs := p.s.push.snapshot()
	if len(subs) == 0 {
		return notify.OutcomeNotConfigured, ""
	}
	payload, _ := json.Marshal(map[string]any{
		"title": e.Title,
		"body":  notify.PlainText(e),
		"tag":   "dw-notify",
		"data":  map[string]any{"url": e.DeepURL},
	})
	res := p.s.push.broadcast(payload, subs)
	if res.delivered > 0 {
		return notify.OutcomeSent, ""
	}
	// Failed — surface WHY so the user can self-fix (the Apple-push troubleshooting case).
	switch {
	case res.pruned > 0:
		return notify.OutcomeFailed, fmt.Sprintf("订阅失效(410 Gone)，已移除%d个 — 请重新「开启浏览器通知」", res.pruned)
	case len(res.rejected) > 0:
		return notify.OutcomeFailed, fmt.Sprintf("HTTP %d %s", res.rejected[0].Status, res.rejected[0].Reason)
	default:
		return notify.OutcomeFailed, "无订阅送达"
	}
}

func (p webpushProvider) Status(ctx context.Context, cfg notify.ProviderConfig) notify.Status {
	n := p.s.push.count()
	return notify.Status{
		Name:       "浏览器",
		Configured: n > 0,
		Healthy:    n > 0,
		ActivationHint: func() string {
			if n == 0 {
				return "未订阅 — 开启浏览器通知"
			}
			return ""
		}(),
	}
}

// ── coordinator builder ─────────────────────────────────────────────────────────

// newNotifyCoordinator wires the fan-out coordinator with all channels in display
// order. Webhook channels (Feishu/DingTalk/WeCom/Slack) are generic and live in the
// notify package; iLink + web push are terminal-specific adapters.
func newNotifyCoordinator(s *Server) *notify.Coordinator {
	store := newFileConfigStore(s.config.DataDir)
	return notify.NewCoordinator(store, nowFunc,
		ilinkProvider{s: s.ilink},
		webpushProvider{s: s},
		notify.NewFeishuProvider(nowFunc),
		notify.NewDingTalkProvider(nowFunc),
		notify.NewWeComProvider(nowFunc),
		notify.NewSlackProvider(nowFunc),
	)
}

// ── AES-GCM helpers (shared by the config store and iLink's encrypted state) ─────

func aesgcmSeal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func aesgcmOpen(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
