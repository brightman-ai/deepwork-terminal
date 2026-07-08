package terminal

// Notification HTTP surface — status + config (provider on/off + redacted settings)
// + per-provider test send. Delivery metrics are owned by the notify.Coordinator
// (the single fan-out point), so there is no second tally here to drift against.

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/brightman-ai/deepwork-terminal/notify"
)

// notifyConfigPayload is the single source the UI renders from: per-provider config
// (enabled) joined with runtime status (configured/healthy/quota) + redacted settings
// + delivery metrics. Both the full settings section and the quick sheet read this.
func (s *Server) notifyConfigPayload(r *http.Request) map[string]any {
	ctx := r.Context()
	cfg, _ := s.coordinator.Config(ctx)
	statuses := s.coordinator.Statuses(ctx)
	providers := make([]map[string]any, 0, len(statuses))
	for _, st := range statuses {
		pc, _ := cfg.Get(st.Kind)
		providers = append(providers, map[string]any{
			"kind":           st.Kind,
			"name":           st.Name,
			"enabled":        st.Enabled,
			"configured":     st.Configured,
			"healthy":        st.Healthy,
			"quota":          st.Quota,
			"todaySent":      st.TodaySent,
			"activationHint": st.ActivationHint,
			"settings":       json.RawMessage(notify.RedactWebhook(pc.Settings)), // creds masked (§12.8)
		})
	}
	return map[string]any{
		"providers": providers,
		"metrics":   s.coordinator.Metrics(),
	}
}

// handleNotifyConfig → GET /api/notify/config. The provider config + status SSOT.
func (s *Server) handleNotifyConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.notifyConfigPayload(r))
}

// handleNotifyConfigSave → PUT /api/notify/config. Updates provider on/off toggles
// (webhook credentials go through handleNotifyProviderSettings so the GET-redacted
// values are never round-tripped back into storage).
func (s *Server) handleNotifyConfigSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Providers []struct {
			Kind    string `json:"kind"`
			Enabled bool   `json:"enabled"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	for _, p := range body.Providers {
		if err := s.coordinator.SetEnabled(r.Context(), p.Kind, p.Enabled); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	// Enabling a channel (e.g. a WeChat/Feishu/DingTalk webhook) must bring the notifier up
	// now — otherwise nothing polls for turn-ends until the next restart. Idempotent + a no-op
	// when it's already running; harmless if the user just toggled everything off.
	s.push.ensureNotifier()
	writeJSON(w, http.StatusOK, s.notifyConfigPayload(r))
}

// handleNotifyProviderSettings → PUT /api/notify/providers/{kind}/settings. Sets a
// webhook channel's url/secret (encrypted at rest by the config store).
func (s *Server) handleNotifyProviderSettings(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// secret is write-only and a pointer so the UI can distinguish three intents:
	// omitted/null = keep the existing secret (editing only the URL must not wipe it),
	// "" = clear it (turn signing off), "<value>" = set/replace it.
	var in struct {
		URL    string  `json:"url"`
		Secret *string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	merged := notify.WebhookSettings{URL: in.URL}
	if in.Secret != nil {
		merged.Secret = *in.Secret // set or clear
	} else if cfg, cerr := s.coordinator.Config(r.Context()); cerr == nil {
		if pc, ok := cfg.Get(kind); ok { // keep existing secret
			var old notify.WebhookSettings
			_ = json.Unmarshal(pc.Settings, &old)
			merged.Secret = old.Secret
		}
	}
	out, _ := json.Marshal(merged)
	if err := s.coordinator.SetSettings(r.Context(), kind, out); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Configuring a webhook is intent to use it → make sure the notifier is running.
	s.push.ensureNotifier()
	writeJSON(w, http.StatusOK, s.notifyConfigPayload(r))
}

// handleNotifyProviderTest → POST /api/notify/providers/{kind}/test. Sends a real
// test notification to ONE provider (per-provider cooldown; honest outcome, never a
// fake "sent"; iLink consumes real WeChat quota and the UI warns of that).
func (s *Server) handleNotifyProviderTest(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	res, ok := s.coordinator.Test(r.Context(), kind, s.notifyTestEvent())
	if !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "测试有冷却，请稍候再试"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "result": res.Outcome.String()})
}

// handleNotifyTest → POST /notify/test. Fires a test to every ENABLED provider and
// returns each channel's honest outcome (or "cooldown" when gated).
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	e := s.notifyTestEvent()
	cfg, _ := s.coordinator.Config(r.Context())
	results := map[string]string{}
	for _, kind := range s.coordinator.Kinds() {
		if !cfg.Enabled(kind) {
			continue
		}
		res, ok := s.coordinator.Test(r.Context(), kind, e)
		if !ok {
			results[kind] = "cooldown"
			continue
		}
		results[kind] = res.Outcome.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) notifyTestEvent() notify.Event {
	return notify.Event{
		Title:   "✅ 测试通知",
		Kind:    notify.KindTest,
		Summary: "Deepwork 通知链路测试 — 收到即正常",
		DeepURL: s.notifyDeepURL(""),
	}
}

// handleNotifyStatus → GET /notify/status. Back-compat overview consumed by the
// current IGS UI: iLink + web-push health sub-objects + tunnel URL + the new
// provider config/metrics (so the UI can migrate incrementally to /api/notify/config).
func (s *Server) handleNotifyStatus(w http.ResponseWriter, r *http.Request) {
	var ilinkView ilinkStatus
	if s.ilink != nil {
		ilinkView = s.ilink.status()
	} else {
		ilinkView = ilinkStatus{MaxSends: ilinkMaxSends}
	}
	payload := s.notifyConfigPayload(r)
	payload["tunnelUrl"] = s.tunnel.PublicURL()
	payload["ilink"] = ilinkView
	payload["webPush"] = map[string]any{
		"subscriptions":   s.push.count(),
		"notifierRunning": s.push.notifierRunning(),
		"subs":            s.push.subsDetails(),
	}
	writeJSON(w, http.StatusOK, payload)
}
