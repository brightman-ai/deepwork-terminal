package terminal

import "os"

// Config for the terminal session server.
type Config struct {
	Addr         string // listen address, e.g. ":8022" (always 0.0.0.0)
	DefaultShell string // e.g. "/bin/bash --login"
	BufferSize   int    // ring buffer size in bytes, default 1MB
	MaxSessions  int    // max concurrent sessions, default 100
	AuthCode     string // auto-generated auth code, printed to console on start
	DataDir      string // data directory for persistence

	// VapidSubscriber is the VAPID JWT "sub" claim — a contact URL identifying the
	// app server to the push service. Apple APNs (iOS Web Push) REJECTS a token whose
	// sub is not a valid mailto: (real-format domain) or https: URL, so this MUST NOT
	// be a bare/localhost address. Empty → resolved from DW_VAPID_SUBSCRIBER, then a
	// valid placeholder (see defaultVapidSubscriber).
	VapidSubscriber string
}

// defaultVapidSubscriber is the fallback VAPID "sub" when neither Config nor the
// DW_VAPID_SUBSCRIBER env var supplies one. It is a VALID mailto: with a real-format
// domain (NOT @localhost) so Apple/web-push services accept the signed token. Deployers
// should override it with their own contact via Config.VapidSubscriber or the env var.
const defaultVapidSubscriber = "mailto:deepwork-terminal@users.noreply.github.com"

// resolveVapidSubscriber returns the effective VAPID "sub", in precedence order:
// explicit Config value → DW_VAPID_SUBSCRIBER env → valid placeholder.
func resolveVapidSubscriber(cfg Config) string {
	if cfg.VapidSubscriber != "" {
		return cfg.VapidSubscriber
	}
	if env := os.Getenv("DW_VAPID_SUBSCRIBER"); env != "" {
		return env
	}
	return defaultVapidSubscriber
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
