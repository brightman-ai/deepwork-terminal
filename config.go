package terminal

import (
	"os"
	"strings"
)

// Config for the terminal session server.
type Config struct {
	Addr         string // listen address, e.g. ":8022" (always 0.0.0.0)
	DefaultShell string // e.g. "/bin/bash --login"
	BufferSize   int    // ring buffer size in bytes, default 1MB
	MaxSessions  int    // max concurrent sessions, default 100
	AuthCode     string // auto-generated auth code, printed to console on start
	DataDir      string // data directory for persistence
	Version      string // build version (e.g. "v0.4.0"), surfaced to the UI via GET /version; "dev" for source builds

	// VapidSubscriber is the VAPID JWT "sub" claim — a contact identifying the app
	// server to the push service. Apple APNs (iOS Web Push) REJECTS a token whose sub
	// is not a valid mailto: (real-format domain) or https: URL → 403 BadJwtToken.
	// Pass a BARE email (e.g. "you@your.dev") or an https: URL — webpush-go prepends
	// "mailto:" automatically (a "mailto:" prefix here would double to "mailto:mailto:…").
	// Empty → resolved from DW_VAPID_SUBSCRIBER, then defaultVapidSubscriber.
	VapidSubscriber string
}

// defaultVapidSubscriber is the fallback VAPID "sub" when neither Config nor the
// DW_VAPID_SUBSCRIBER env var supplies one. NOTE: webpush-go (vapid.go) auto-prepends
// "mailto:" to any value that isn't an https: URL, so this is a BARE email (NOT prefixed
// with "mailto:"). A "mailto:" prefix here would produce an invalid "mailto:mailto:…" sub
// that Apple rejects with 403 BadJwtToken — the exact bug this const's value fixes.
const defaultVapidSubscriber = "deepwork-terminal@users.noreply.github.com"

// resolveVapidSubscriber returns the effective VAPID "sub", in precedence order:
// explicit Config value → DW_VAPID_SUBSCRIBER env → bare-email placeholder. It strips a
// leading "mailto:" (webpush-go re-adds exactly one) so any input form works and we never
// emit a double "mailto:mailto:…" sub; https: URLs pass through untouched.
func resolveVapidSubscriber(cfg Config) string {
	sub := defaultVapidSubscriber
	if cfg.VapidSubscriber != "" {
		sub = cfg.VapidSubscriber
	} else if env := os.Getenv("DW_VAPID_SUBSCRIBER"); env != "" {
		sub = env
	}
	if !strings.HasPrefix(sub, "https:") {
		sub = strings.TrimPrefix(sub, "mailto:")
	}
	return sub
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return Config{
		Addr:         ":8022",
		DefaultShell: shell,
		BufferSize:   1 << 20, // 1MB
		MaxSessions:  100,
		DataDir:      "",
	}
}

// Option configures a Server.
type Option func(*Server)

// WithConfig replaces the entire config.
func WithConfig(c Config) Option { return func(s *Server) { s.config = c } }

// WithHooks sets integration hooks.
func WithHooks(h Hooks) Option { return func(s *Server) { s.hooks = h } }

// WithAddr sets the listen address.
func WithAddr(addr string) Option { return func(s *Server) { s.config.Addr = addr } }

// WithAuthCode sets the auth code. When left empty, NewServer generates one.
func WithAuthCode(code string) Option { return func(s *Server) { s.config.AuthCode = code } }
