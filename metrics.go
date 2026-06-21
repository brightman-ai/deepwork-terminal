package terminal

// Notification delivery metrics — the SINGLE source of truth for "what was
// notified and how". Every agent-waiting event flows through exactly one place,
// the coordinator (pushNotifier.notify), which records exactly one outcome here.
// Because there is one writer and one fan-out point, these counters are
// authoritative (no second tally to drift against).

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// notifyTestCooldown rate-limits the manual dual-channel test so rapid taps can't
// spam real WeChat messages / drain the ~10-per-window iLink quota.
const notifyTestCooldown = 8 * time.Second

type notifyMetrics struct {
	mu          sync.Mutex
	events      int // total agent-waiting events fanned out
	viaIlink    int // delivered to WeChat
	viaWebPush  int // delivered via web push (includes ilink→webpush fallback)
	fellBack    int // ilink was attempted but we fell back to web push
	undelivered int // no channel delivered
	lastAt      time.Time
	lastChannel string    // "ilink" | "webpush" | "none"
	lastTestAt  time.Time // last manual /notify/test (cooldown gate)
}

// allowTest returns true at most once per notifyTestCooldown, recording the time.
func (m *notifyMetrics) allowTest() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := nowFunc()
	if !m.lastTestAt.IsZero() && now.Sub(m.lastTestAt) < notifyTestCooldown {
		return false
	}
	m.lastTestAt = now
	return true
}

func newNotifyMetrics() *notifyMetrics { return &notifyMetrics{} }

// record logs exactly one delivery outcome for one event. channel is the channel
// that delivered ("ilink"/"webpush") or "none"; fellBack is true when iLink was
// tried first but did not deliver.
func (m *notifyMetrics) record(channel string, fellBack bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events++
	m.lastAt = nowFunc()
	m.lastChannel = channel
	if fellBack {
		m.fellBack++
	}
	switch channel {
	case "ilink":
		m.viaIlink++
	case "webpush":
		m.viaWebPush++
	default:
		m.undelivered++
	}
}

type notifyMetricsView struct {
	Events      int    `json:"events"`
	ViaIlink    int    `json:"viaIlink"`
	ViaWebPush  int    `json:"viaWebPush"`
	FellBack    int    `json:"fellBack"`
	Undelivered int    `json:"undelivered"`
	LastAtMs    int64  `json:"lastAtMs"` // 0 = never
	LastChannel string `json:"lastChannel"`
}

func (m *notifyMetrics) view() notifyMetricsView {
	m.mu.Lock()
	defer m.mu.Unlock()
	var lastMs int64
	if !m.lastAt.IsZero() {
		lastMs = m.lastAt.UnixMilli()
	}
	return notifyMetricsView{
		Events:      m.events,
		ViaIlink:    m.viaIlink,
		ViaWebPush:  m.viaWebPush,
		FellBack:    m.fellBack,
		Undelivered: m.undelivered,
		LastAtMs:    lastMs,
		LastChannel: m.lastChannel,
	}
}

// handleNotifyStatus → GET /notify/status. The unified, single-source view of the
// whole notification mechanism: both channels' live health + delivery metrics +
// the current deep-link target. The frontend renders its "通知机制总览" from this
// one response (no per-channel polling races).
func (s *Server) handleNotifyStatus(w http.ResponseWriter, r *http.Request) {
	var ilinkView ilinkStatus
	if s.ilink != nil {
		ilinkView = s.ilink.status()
	} else {
		ilinkView = ilinkStatus{MaxSends: ilinkMaxSends}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"webPush": map[string]any{
			"subscriptions":   s.push.count(),
			"notifierRunning": s.push.notifierRunning(),
			"subs":            s.push.subsDetails(),
		},
		"ilink":     ilinkView,
		"metrics":   s.metrics.view(),
		"tunnelUrl": s.tunnel.PublicURL(),
	})
}

// handleNotifyTest → POST /notify/test. Fires a test notification down BOTH
// channels (A web push + B WeChat iLink) so the user can confirm the full chain
// of each in one tap, and returns each channel's real outcome (never a false
// "sent"). Behind authWrap.
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	// Cooldown: the test sends REAL notifications (and consumes iLink quota), so
	// rate-limit rapid taps.
	if !s.metrics.allowTest() {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "测试有冷却，请稍候再试"})
		return
	}
	// One deep-link target shared by both channels (so the WeChat test also exercises
	// the tap-to-auth deep link, not just plain message delivery).
	deepURL := s.notifyDeepURL("")

	// Channel A — web push broadcast (same path the live notifier uses).
	subs := s.push.snapshot()
	wpSent, wpRejected := 0, 0
	if len(subs) > 0 {
		payload, _ := json.Marshal(map[string]any{
			"title": "✅ 测试通知",
			"body":  "Deepwork 浏览器推送链路正常",
			"tag":   "dw-notify-test",
			"data":  map[string]any{"url": deepURL},
		})
		res := s.push.broadcast(payload, subs)
		wpSent, wpRejected = res.delivered, len(res.rejected)
	}

	// Channel B — WeChat iLink (only delivers when active; honest outcome otherwise).
	ilinkResult := "not-configured"
	if s.ilink != nil {
		testMsg := "✅ 测试通知（微信通道）— Deepwork 通知链路正常\n" + deepURL
		switch s.ilink.trySend(r.Context(), "notify-test", testMsg) {
		case ilinkSent:
			ilinkResult = "sent"
		case ilinkDormant:
			ilinkResult = "dormant"
		case ilinkAmbiguous:
			ilinkResult = "ambiguous"
		default:
			ilinkResult = "not-configured"
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"webPush": map[string]any{"sent": wpSent, "rejected": wpRejected, "subs": len(subs)},
		"ilink":   map[string]any{"result": ilinkResult},
	})
}
