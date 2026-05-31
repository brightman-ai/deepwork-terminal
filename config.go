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
