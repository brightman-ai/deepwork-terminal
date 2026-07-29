package terminal

import (
	"os"
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
	// ReleaseRepo is the GitHub "owner/name" whose published releases this build's Version
	// should be compared against ("有新版本吗"). EMPTY DISABLES THE CHECK, and empty is the
	// default on purpose: only the binary knows which product it is. deepwork-pro embeds this
	// package as a subsystem and stamps Version with ITS OWN tag — comparing that against
	// deepwork-terminal's releases would either claim "已是最新发布版" about a repo it does not
	// come from, or point the user at another product's download page. A library must not
	// assume its embedder's identity; the binary opts in (see cmd/dw-terminal/main.go).
	ReleaseRepo string

	// Tunnel, when true, opens a Cloudflare quick tunnel at startup so the server is
	// reachable over the public internet (a temporary *.trycloudflare.com URL, no
	// Cloudflare account needed) — the same thing the UI's tunnel toggle does, wired to
	// a launch flag so an agent can bring up public access in one command. The URL is
	// printed in the startup banner. Named/persistent tunnels stay UI-driven (they need
	// an interactive `cloudflared login`).
	Tunnel bool
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

// WithTunnel opens a Cloudflare quick tunnel at startup when true (see Config.Tunnel).
func WithTunnel(enabled bool) Option { return func(s *Server) { s.config.Tunnel = enabled } }
