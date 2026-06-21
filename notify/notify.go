// Package notify is the host-agnostic notification provider abstraction shared by
// deepwork-terminal and deepwork-pro (imported like agentintel). It defines:
//
//   - Event: the structured "what happened", rendered per-provider (no rendering
//     logic leaks into the data contract).
//   - Provider: a delivery channel (WeChat iLink / web push / Feishu / DingTalk /
//     WeCom). Stateless w.r.t. config — its ProviderConfig is passed in per call by
//     the one config owner (Coordinator), so there is a single join point.
//   - Config / Store: the provider on/off + credentials SSOT. The host supplies a
//     concrete Store (terminal = encrypted local file; pro = per-user DB; a per-user
//     store instance is bound at construction so Load/Save need no userID param).
//   - Coordinator: fans an Event out to every ENABLED provider (no priority/fallback
//     orchestration — fan-out is the model), records one EventRecord per event.
//
// The package is pure: no HTTP server, no tmux, no global state. Webhook providers
// (Feishu/DingTalk/WeCom) live here too (generic HTTP, reusable by pro); iLink and
// web push are terminal-specific and implement Provider in package terminal.
package notify

import (
	"context"
	"encoding/json"
)

// Kind classifies what happened so a provider can pick an icon / urgency and a
// non-session host (pro) can map its own events onto a shared vocabulary.
type Kind int

const (
	KindInfo    Kind = iota // generic
	KindWaiting             // an agent needs user input (most urgent)
	KindDone                // an agent finished a turn (idle, can continue)
	KindRunning             // an agent is working
	KindTest                // a manual "send test" from the settings UI
)

// Outcome is one provider's result for one Send. Generalized from ilinkOutcome.
type Outcome int

const (
	OutcomeNotConfigured Outcome = iota // channel not set up (no creds / not logged in)
	OutcomeSent                         // accepted by the channel
	OutcomeDormant                      // configured but temporarily unable (quota/session window)
	OutcomeFailed                       // attempted and rejected/errored
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSent:
		return "sent"
	case OutcomeDormant:
		return "dormant"
	case OutcomeFailed:
		return "failed"
	default:
		return "not-configured"
	}
}

// SessionRef is one agent session referenced by a notification. Pure semantic data
// — providers render it to their native format (plain text vs interactive card).
type SessionRef struct {
	Tool        string `json:"tool"`        // "claude" | "codex"
	Location    string `json:"location"`    // readable "where": "main · 窗口3 TERMINAL · 面板1"
	Status      string `json:"status"`      // "waiting" | "idle" | "running"
	JustChanged bool   `json:"justChanged"` // transitioned in this batch → 🆕 marker
	Turns       int    `json:"turns"`
	Stats       string `json:"stats"` // optional compact stats ("in 29k out 532k cc 2.3M cr 152.9M ~$112")
}

// Counts is the live header tally across ALL tracked sessions (never capped — the
// list may be truncated for display but the counts stay true; §12.2 single source).
type Counts struct {
	Waiting int `json:"waiting"`
	Idle    int `json:"idle"`
	Running int `json:"running"`
}

// Event is the structured notification. No rendering methods, no provider knowledge
// — PlainText(Event) (package fn) and per-provider renderers consume it.
type Event struct {
	Title    string            `json:"title"`
	Kind     Kind              `json:"kind"`
	Counts   Counts            `json:"counts"`
	Sessions []SessionRef      `json:"sessions"` // recency-sorted; ALL live (renderer caps for display)
	Summary  string            `json:"summary"`  // optional pre-rolled stat blocks (delta/today), host-built
	DeepURL  string            `json:"deepUrl"`  // tap target; may be empty
	Extra    map[string]string `json:"extra"`    // free fields for non-session hosts (pro: platform/count)
}

// Quota is a channel's send budget within its current window (iLink ~10/window).
type Quota struct {
	Used int `json:"used"`
	Max  int `json:"max"`
}

// Status is a provider's runtime health, joined with config Enabled by the
// Coordinator (the SINGLE join point — providers MUST NOT set Enabled themselves).
// It carries NO credential plaintext (§12.8).
type Status struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`    // filled by Coordinator from Config, not by the provider
	Configured     bool   `json:"configured"` // has creds / logged in / webhook set
	Healthy        bool   `json:"healthy"`
	Quota          *Quota `json:"quota,omitempty"` // nil for channels without a quota
	TodaySent      int    `json:"todaySent"`       // filled by Coordinator from metrics, not the provider
	ActivationHint string `json:"activationHint"`  // e.g. "回任意字符续订" / "未配置 webhook"（no creds）
}

// Provider is one delivery channel. STATELESS w.r.t. config: the matching
// ProviderConfig is passed in per call by the Coordinator (the one config owner).
type Provider interface {
	Kind() string // stable id: "ilink" | "webpush" | "feishu" | "dingtalk" | "wecom"
	Name() string // display: "微信" | "浏览器" | "飞书" | "钉钉" | "企业微信"
	// Send renders e to the channel's native format and delivers it. cfg is this
	// provider's current config (Settings carries webhook url/secret etc.). It returns
	// the Outcome plus a short human-readable detail for troubleshooting — the failure
	// reason on failure (e.g. "订阅失效(410 Gone)", "BadJwtToken", "休眠:配额满"), "" on
	// clean success. The detail flows into the recent-3 history shown in the UI.
	Send(ctx context.Context, e Event, cfg ProviderConfig) (Outcome, string)
	// Status reports runtime health. The provider does NOT set Status.Enabled
	// (Coordinator joins it from Config) nor TodaySent (Coordinator joins metrics).
	Status(ctx context.Context, cfg ProviderConfig) Status
}

// ProviderConfig is one channel's on/off + per-kind typed Settings.
type ProviderConfig struct {
	Kind     string          `json:"kind"`
	Enabled  bool            `json:"enabled"`
	Settings json.RawMessage `json:"settings,omitempty"` // per-kind (webhook: {url,secret})
}

// Config is the provider config SSOT (shared data schema; persisted by the host's Store).
type Config struct {
	Version   int              `json:"version"`
	Providers []ProviderConfig `json:"providers"`
}

// ConfigVersion is the current schema version (Load upgrades older in place, §12.11).
const ConfigVersion = 1

// Get returns the config for a kind (zero value + false when absent).
func (c Config) Get(kind string) (ProviderConfig, bool) {
	for _, p := range c.Providers {
		if p.Kind == kind {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// Enabled reports whether a kind is present AND enabled.
func (c Config) Enabled(kind string) bool {
	p, ok := c.Get(kind)
	return ok && p.Enabled
}

// withDefaults upgrades an older/empty Config in place: stamps Version, drops
// unknown duplicate kinds (last wins). Unknown provider kinds are kept (a newer
// host may know them) but a Coordinator simply skips kinds it has no Provider for.
func (c Config) withDefaults() Config {
	c.Version = ConfigVersion
	seen := map[string]int{}
	out := make([]ProviderConfig, 0, len(c.Providers)) // fresh slice (don't alias caller's store)
	for _, p := range c.Providers {
		if i, dup := seen[p.Kind]; dup {
			out[i] = p // last wins
			continue
		}
		seen[p.Kind] = len(out)
		out = append(out, p)
	}
	c.Providers = out
	return c
}

// Store persists the Config. A per-user store instance is bound at construction
// (pro), so Load/Save carry no userID — the user dimension is the host's concern.
type Store interface {
	Load(ctx context.Context) (Config, error)
	Save(ctx context.Context, c Config) error
}
