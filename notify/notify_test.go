package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	kind string
	out  Outcome
	sent int
}

func (f *fakeProvider) Kind() string { return f.kind }
func (f *fakeProvider) Name() string { return f.kind }
func (f *fakeProvider) Send(ctx context.Context, e Event, cfg ProviderConfig) (Outcome, string) {
	f.sent++
	return f.out, ""
}
func (f *fakeProvider) Status(ctx context.Context, cfg ProviderConfig) Status {
	return Status{Name: f.kind, Configured: true, Healthy: true}
}

type memStore struct{ c Config }

func (m *memStore) Load(ctx context.Context) (Config, error) { return m.c, nil }
func (m *memStore) Save(ctx context.Context, c Config) error { m.c = c; return nil }

func fixedNow() time.Time { return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC) }

// Fan-out delivers to every ENABLED provider and skips disabled ones (the hard
// "关了不推" guarantee lives in code, not docs).
func TestCoordinatorFanoutSkipsDisabled(t *testing.T) {
	a := &fakeProvider{kind: "a", out: OutcomeSent}
	b := &fakeProvider{kind: "b", out: OutcomeSent}
	store := &memStore{c: Config{Providers: []ProviderConfig{{Kind: "a", Enabled: true}, {Kind: "b", Enabled: false}}}}
	c := NewCoordinator(store, fixedNow, a, b)

	rec := c.Send(context.Background(), Event{Kind: KindWaiting})
	if a.sent != 1 {
		t.Fatalf("enabled provider a should send once, got %d", a.sent)
	}
	if b.sent != 0 {
		t.Fatalf("disabled provider b must NOT send, got %d", b.sent)
	}
	if len(rec.Results) != 1 || rec.Results[0].Provider != "a" {
		t.Fatalf("record should contain only a: %+v", rec.Results)
	}
}

// Test-send cooldown is PER provider (testing WeChat must not block testing Feishu).
func TestCoordinatorTestCooldownPerProvider(t *testing.T) {
	a := &fakeProvider{kind: "a", out: OutcomeSent}
	b := &fakeProvider{kind: "b", out: OutcomeSent}
	store := &memStore{c: Config{Providers: []ProviderConfig{{Kind: "a", Enabled: true}, {Kind: "b", Enabled: true}}}}
	c := NewCoordinator(store, fixedNow, a, b)

	if _, ok := c.Test(context.Background(), "a", Event{}); !ok {
		t.Fatal("first test of a should be allowed")
	}
	if _, ok := c.Test(context.Background(), "a", Event{}); ok {
		t.Fatal("second test of a within cooldown should be blocked")
	}
	if _, ok := c.Test(context.Background(), "b", Event{}); !ok {
		t.Fatal("test of b must NOT be blocked by a's cooldown (per-provider)")
	}
}

// Statuses is the single join point: Enabled comes from Config, runtime from provider.
func TestCoordinatorStatusJoinsEnabled(t *testing.T) {
	a := &fakeProvider{kind: "a"}
	store := &memStore{c: Config{Providers: []ProviderConfig{{Kind: "a", Enabled: true}}}}
	c := NewCoordinator(store, fixedNow, a)
	st := c.Statuses(context.Background())
	if len(st) != 1 || !st[0].Enabled || !st[0].Configured {
		t.Fatalf("status should join enabled(config)+configured(runtime): %+v", st)
	}
}

// PlainText shows ALL sessions (capped + tail), with exactly the just-changed one
// marked 🆕 — the ③ list-bug fix at the renderer level.
func TestPlainTextShowsAllSessionsCapped(t *testing.T) {
	var sessions []SessionRef
	for i := 0; i < 7; i++ {
		sessions = append(sessions, SessionRef{Tool: "claude", Location: "loc", Status: "idle", Turns: 1, JustChanged: i == 3})
	}
	e := Event{Title: "t", Counts: Counts{Idle: 7}, Sessions: sessions}
	txt := PlainText(e)
	if !strings.Contains(txt, "✅7 完成") {
		t.Fatalf("header should count all 7 idle:\n%s", txt)
	}
	if !strings.Contains(txt, "…还有 2 个") {
		t.Fatalf("list should cap at 5 with a tail:\n%s", txt)
	}
	if n := strings.Count(txt, "🆕"); n != 1 {
		t.Fatalf("exactly one 🆕, got %d:\n%s", n, txt)
	}
}

// Webhook url + secret are credentials → redacted for the config API (§12.8).
func TestRedactWebhookHidesCredential(t *testing.T) {
	raw, _ := json.Marshal(WebhookSettings{URL: "https://open.feishu.cn/open-apis/bot/v2/hook/abcdef123456", Secret: "topsecret"})
	var ws WebhookSettings
	_ = json.Unmarshal(RedactWebhook(raw), &ws)
	if strings.Contains(ws.URL, "abcdef") {
		t.Fatalf("url credential leaked: %s", ws.URL)
	}
	if ws.Secret == "topsecret" {
		t.Fatalf("secret leaked: %s", ws.Secret)
	}
}

// Metrics track per-provider today count + last-success time + a recent-3 trail
// (with failure detail) — the observability/troubleshooting data — and survive a
// snapshot→restore round-trip (the cross-restart persistence).
func TestMetricsObservabilityAndRestore(t *testing.T) {
	m := newMetrics(fixedNow)
	m.record(EventRecord{Results: []Result{{Provider: "webpush", Outcome: OutcomeFailed, Detail: "订阅失效(410 Gone)"}}})
	m.record(EventRecord{Results: []Result{{Provider: "webpush", Outcome: OutcomeFailed, Detail: "410"}}})
	m.record(EventRecord{Results: []Result{{Provider: "webpush", Outcome: OutcomeFailed, Detail: "410"}}})
	m.record(EventRecord{Results: []Result{{Provider: "webpush", Outcome: OutcomeSent}}}) // 4th → recent caps at 3
	m.record(EventRecord{Results: []Result{{Provider: "ilink", Outcome: OutcomeSent}}})

	check := func(label string, v MetricsView) {
		var wp *ProviderMetric
		for i := range v.PerProvider {
			if v.PerProvider[i].Provider == "webpush" {
				wp = &v.PerProvider[i]
			}
		}
		if wp == nil {
			t.Fatalf("%s: webpush missing", label)
		}
		if wp.Sent != 1 || wp.Failed != 3 {
			t.Fatalf("%s: counts %+v", label, wp)
		}
		if wp.LastSuccessAtMs == 0 {
			t.Fatalf("%s: lastSuccess should be set", label)
		}
		if len(wp.Recent) != recentCap {
			t.Fatalf("%s: recent should cap at %d, got %d", label, recentCap, len(wp.Recent))
		}
		if wp.Recent[len(wp.Recent)-1].Outcome != OutcomeSent {
			t.Fatalf("%s: newest recent should be the sent one", label)
		}
		// the failure detail must be preserved for troubleshooting
		if wp.Recent[0].Detail == "" {
			t.Fatalf("%s: failure detail dropped", label)
		}
	}
	check("live", m.view())

	// snapshot → restore into a fresh metrics → same observability data.
	m2 := newMetrics(fixedNow)
	m2.restore(m.snapshot())
	check("restored", m2.view())
}

func TestClassifyWebhook(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   Outcome
	}{
		{200, `{"errcode":0}`, OutcomeSent},
		{200, `{"errcode":9499,"errmsg":"bad"}`, OutcomeFailed},
		{200, `{"code":0}`, OutcomeSent},
		{200, `{"code":19021}`, OutcomeFailed},
		{500, ``, OutcomeFailed},
		{200, ``, OutcomeSent},
	}
	for _, c := range cases {
		if got := classifyWebhook(c.status, []byte(c.body)); got != c.want {
			t.Errorf("classifyWebhook(%d,%q) = %v, want %v", c.status, c.body, got, c.want)
		}
	}
}

// Offline 加签 oracle: feishuSign/dingtalkSign must match an HMAC-SHA256 computed by
// an INDEPENDENT implementation (Python, per each vendor's官方算法) for fixed vectors.
// This catches any drift in the signing math (key vs data, seconds vs millis, the
// "\n" join) WITHOUT a live bot — the cheapest signature-correctness check. WeCom and
// Slack are intentionally absent: neither signs (the webhook URL is the credential).
func TestWebhookSigningVectors(t *testing.T) {
	// Feishu: base64(HMAC-SHA256(key = "<ts_sec>\n<secret>", data = "")), ts in SECONDS.
	if got := feishuSign("1700000000", "feishu-secret"); got != "OrBzY1Y01Gq+HgJsl+7OfWcMVwc7YocohQm5iiZwjhU=" {
		t.Fatalf("feishuSign vector mismatch (algo drift?): got %s", got)
	}
	// DingTalk: base64(HMAC-SHA256(key = secret, data = "<ts_ms>\n<secret>")), ts in MILLIS.
	if got := dingtalkSign("1700000000000", "ding-secret"); got != "nqq88ibHb0KGsDlurqi82ts6x3l4frnYUqJn0JfHX9o=" {
		t.Fatalf("dingtalkSign vector mismatch (algo drift?): got %s", got)
	}
}

// Slack speaks mrkdwn, not standard markdown: the deep link must render as
// <url|text> (NOT [text](url)), the payload is {"text":...}, the webhook URL is the
// only credential (no signing query), and a plain-text "ok" body (not JSON) is a
// success. Captures a real POST to assert all four differences from the other IMs.
func TestSlackProviderPayload(t *testing.T) {
	var gotBody []byte
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok")) // Slack returns plain text, not JSON
	}))
	defer srv.Close()

	p := NewSlackProvider(fixedNow)
	cfg := ProviderConfig{Kind: "slack", Enabled: true}
	cfg.Settings, _ = json.Marshal(WebhookSettings{URL: srv.URL, Secret: "ignored"})
	e := Event{Title: "agent 等待", Kind: KindWaiting, Counts: Counts{Waiting: 1}, DeepURL: "https://x.example/?session=foo"}

	out, _ := p.Send(context.Background(), e, cfg)
	if out != OutcomeSent {
		t.Fatalf(`plain "ok" body should be OutcomeSent, got %v`, out)
	}
	if gotQuery != "" {
		t.Fatalf("slack must not append a signing query, got %q", gotQuery)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf(`body is not {"text":...}: %s`, gotBody)
	}
	if !strings.Contains(payload.Text, "<https://x.example/?session=foo|打开 Deepwork>") {
		t.Fatalf("deep link must be mrkdwn <url|text>:\n%s", payload.Text)
	}
	if strings.Contains(payload.Text, "](") {
		t.Fatalf("must NOT use standard markdown [text](url):\n%s", payload.Text)
	}
	if !strings.Contains(payload.Text, "agent 等待") {
		t.Fatalf("title missing from text:\n%s", payload.Text)
	}
}
