package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WebhookSettings is the per-provider config for the IM-bot webhook channels
// (Feishu / DingTalk / WeCom). URL is itself a credential (it embeds a token), so
// it is encrypted at rest and redacted in any status/log output (§12.8).
type WebhookSettings struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"` // optional signing secret (Feishu/DingTalk 加签)
}

func parseWebhook(cfg ProviderConfig) WebhookSettings {
	var ws WebhookSettings
	if len(cfg.Settings) > 0 {
		_ = json.Unmarshal(cfg.Settings, &ws)
	}
	return ws
}

// buildFn produces the request URL + body for one IM from the event + settings.
type buildFn func(ws WebhookSettings, e Event, now time.Time) (reqURL string, body []byte)

// webhookProvider is the generic webhook delivery channel; per-IM differences
// (payload shape, signing) live in the build closure.
type webhookProvider struct {
	kind, name string
	build      buildFn
	httpc      *http.Client
	now        func() time.Time
}

func newWebhook(kind, name string, build buildFn, now func() time.Time) *webhookProvider {
	if now == nil {
		now = time.Now
	}
	return &webhookProvider{kind: kind, name: name, build: build, httpc: &http.Client{Timeout: 10 * time.Second}, now: now}
}

func (p *webhookProvider) Kind() string { return p.kind }
func (p *webhookProvider) Name() string { return p.name }

func (p *webhookProvider) Send(ctx context.Context, e Event, cfg ProviderConfig) (Outcome, string) {
	ws := parseWebhook(cfg)
	if strings.TrimSpace(ws.URL) == "" {
		return OutcomeNotConfigured, ""
	}
	reqURL, body := p.build(ws, e, p.now())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return OutcomeFailed, "请求构造失败"
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpc.Do(req)
	if err != nil {
		return OutcomeFailed, "网络错误" // transport error → failed (no fan-out fallback; other providers独立)
	}
	defer resp.Body.Close() //nolint:errcheck
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if out := classifyWebhook(resp.StatusCode, snippet); out != OutcomeSent {
		return out, fmt.Sprintf("HTTP %d %s", resp.StatusCode, clip(string(snippet), 60))
	}
	return OutcomeSent, ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (p *webhookProvider) Status(ctx context.Context, cfg ProviderConfig) Status {
	ws := parseWebhook(cfg)
	configured := strings.TrimSpace(ws.URL) != ""
	hint := ""
	if !configured {
		hint = "未配置 webhook"
	}
	return Status{Name: p.name, Configured: configured, Healthy: configured, ActivationHint: hint}
}

// classifyWebhook maps an HTTP status + (short) body to an Outcome. IM bots return
// 2xx with a JSON {errcode|code|StatusCode: 0} on success; a non-zero code is a
// logical failure even on HTTP 200.
func classifyWebhook(status int, body []byte) Outcome {
	if status < 200 || status >= 300 {
		return OutcomeFailed
	}
	var r struct {
		Errcode    *int `json:"errcode"`    // DingTalk / WeCom
		Code       *int `json:"code"`       // Feishu
		StatusCode *int `json:"StatusCode"` // Feishu (older)
	}
	_ = json.Unmarshal(body, &r)
	for _, c := range []*int{r.Errcode, r.Code, r.StatusCode} {
		if c != nil && *c != 0 {
			return OutcomeFailed
		}
	}
	return OutcomeSent
}

// markdownBody renders an Event as markdown text (DingTalk/WeCom/Feishu cards).
func markdownBody(e Event) string {
	text := PlainText(e)
	if e.DeepURL != "" {
		text += "\n\n[打开 Deepwork](" + e.DeepURL + ")"
	}
	return text
}

// ── Feishu (Lark) custom bot ──────────────────────────────────────────────────
// Interactive card (markdown + optional button). Optional sign: HMAC-SHA256 with
// key = "<timestampSec>\n<secret>", empty message, base64.

func NewFeishuProvider(now func() time.Time) Provider {
	return newWebhook("feishu", "飞书", func(ws WebhookSettings, e Event, t time.Time) (string, []byte) {
		card := map[string]any{
			"config": map[string]any{"wide_screen_mode": true},
			"header": map[string]any{
				"title":    map[string]any{"tag": "plain_text", "content": e.Title},
				"template": feishuTemplate(e.Kind),
			},
			"elements": feishuElements(e),
		}
		payload := map[string]any{"msg_type": "interactive", "card": card}
		if ws.Secret != "" {
			ts := strconv.FormatInt(t.Unix(), 10)
			payload["timestamp"] = ts
			payload["sign"] = feishuSign(ts, ws.Secret)
		}
		body, _ := json.Marshal(payload)
		return ws.URL, body
	}, now)
}

func feishuTemplate(k Kind) string {
	switch k {
	case KindWaiting:
		return "orange"
	case KindDone:
		return "green"
	case KindTest:
		return "blue"
	default:
		return "grey"
	}
}

func feishuElements(e Event) []any {
	md := markdownBody(e) // title is in the card header; PlainText carries only the body
	els := []any{map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": md}}}
	if e.DeepURL != "" {
		els = append(els, map[string]any{
			"tag": "action",
			"actions": []any{map[string]any{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": "打开 Deepwork"},
				"url":  e.DeepURL,
				"type": "primary",
			}},
		})
	}
	return els
}

func feishuSign(ts, secret string) string {
	mac := hmac.New(sha256.New, []byte(ts+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ── DingTalk custom robot ─────────────────────────────────────────────────────
// markdown message. Optional sign appended to URL: sign = urlencode(base64(
// HMAC-SHA256(key=secret, data="<timestampMs>\n<secret>"))).

func NewDingTalkProvider(now func() time.Time) Provider {
	return newWebhook("dingtalk", "钉钉", func(ws WebhookSettings, e Event, t time.Time) (string, []byte) {
		reqURL := ws.URL
		if ws.Secret != "" {
			tsMs := strconv.FormatInt(t.UnixMilli(), 10)
			reqURL = appendQuery(reqURL, "timestamp", tsMs)
			reqURL = appendQuery(reqURL, "sign", dingtalkSign(tsMs, ws.Secret))
		}
		payload := map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": e.Title, "text": markdownBody(e)},
		}
		body, _ := json.Marshal(payload)
		return reqURL, body
	}, now)
}

func dingtalkSign(tsMs, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsMs + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ── WeCom (企业微信) group robot ───────────────────────────────────────────────
// markdown message; the bot key is embedded in the URL (no extra signing).

func NewWeComProvider(now func() time.Time) Provider {
	return newWebhook("wecom", "企业微信", func(ws WebhookSettings, e Event, t time.Time) (string, []byte) {
		payload := map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"content": markdownBody(e)},
		}
		body, _ := json.Marshal(payload)
		return ws.URL, body
	}, now)
}

// ── Slack incoming webhook ─────────────────────────────────────────────────────
// Plain {"text":...} message. The webhook URL itself is the credential — no signing,
// the Secret field is unused (same model as WeCom). Slack renders **mrkdwn**, not
// standard markdown: links are <url|text> and bold is *text*, so we build the link
// natively instead of reusing markdownBody (which emits [text](url) Slack shows raw).
// Success is HTTP 200 + the plain-text body "ok" (not JSON); classifyWebhook handles
// that — an unparseable body leaves all code pointers nil → OutcomeSent.

func NewSlackProvider(now func() time.Time) Provider {
	return newWebhook("slack", "Slack", func(ws WebhookSettings, e Event, t time.Time) (string, []byte) {
		text := "*" + e.Title + "*\n" + PlainText(e) // title isn't in PlainText; bold it (mrkdwn)
		if e.DeepURL != "" {
			text += "\n<" + e.DeepURL + "|打开 Deepwork>" // mrkdwn link, NOT [text](url)
		}
		payload := map[string]any{"text": text}
		body, _ := json.Marshal(payload)
		return ws.URL, body // URL is the secret; no signing
	}, now)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func appendQuery(u, k, v string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + k + "=" + url.QueryEscape(v)
}

// RedactWebhook returns a display-safe copy of settings: URL/secret reduced to a
// masked tail so the config API and logs never expose the credential (§12.8).
func RedactWebhook(raw json.RawMessage) json.RawMessage {
	var ws WebhookSettings
	if len(raw) == 0 || json.Unmarshal(raw, &ws) != nil {
		return raw
	}
	red := WebhookSettings{URL: maskTail(ws.URL)}
	if ws.Secret != "" {
		red.Secret = "••••"
	}
	out, _ := json.Marshal(red)
	return out
}

func maskTail(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return "••••" + s[len(s)-6:]
}
