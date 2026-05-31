package terminal

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/brightman-ai/deepwork-terminal/internal/spa"
)

// Server is a complete terminal session HTTP service.
// Standalone: ListenAndServe() runs API + SPA.
// Embedded: Handler() returns API routes for a host to mount.
type Server struct {
	mux      *http.ServeMux
	mgr      *SessionManager
	hooks    Hooks
	config   Config
	listener net.Listener
	tunnel   *Tunnel
	mu       sync.Mutex
}

// NewServer creates a terminal session server.
func NewServer(opts ...Option) (*Server, error) {
	s := &Server{
		config: DefaultConfig(),
	}
	for _, o := range opts {
		o(s)
	}
	if s.config.DataDir == "" {
		home, _ := os.UserHomeDir()
		s.config.DataDir = filepath.Join(home, ".dw-terminal")
	}
	// Always generate auth code if not provided.
	if s.config.AuthCode == "" {
		s.config.AuthCode = generateAuthCode()
	}
	s.tunnel = NewTunnel(s.config.DataDir)
	s.mgr = NewSessionManager(s.config.BufferSize, s.config.DefaultShell)
	s.mux = http.NewServeMux()
	s.registerRoutes()
	return s, nil
}

// Handler returns the API routes (no SPA) for embedding into a host server.
// Routes are relative: GET /sessions, POST /sessions, GET /sessions/{id}/ws, etc.
// The host uses http.StripPrefix to mount at any path prefix.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Service returns the in-process terminal session service owned by this server.
// Hosts should depend on this interface for product integrations such as agent
// state enrichment instead of reaching into SessionManager internals.
func (s *Server) Service() TerminalSessionService {
	return NewInProcessService(s.mgr)
}

// ListenAndServe starts the standalone server (API + SPA).
// The SPA is the embedded Vue frontend (built by build.sh).
func (s *Server) ListenAndServe(ctx context.Context) error {
	root := http.NewServeMux()
	root.Handle("/api/", http.StripPrefix("/api", s.mux))
	root.Handle("/", spa.Handler())

	ln, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.config.Addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	// Print startup info: auth code + LAN address.
	port := s.Port()
	fmt.Fprintf(os.Stderr, "\n  Auth Code: %s\n", s.config.AuthCode)
	hostname, _ := os.Hostname()
	fmt.Fprintf(os.Stderr, "  LAN:       http://%s:%d\n\n", hostname, port)

	srv := &http.Server{Handler: root}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	return srv.Serve(ln)
}

// Port returns the actual listening port (useful when Addr is ":0").
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Close shuts down all sessions.
func (s *Server) Close() error {
	return s.mgr.CloseAll()
}

// registerRoutes sets up the API mux with net/http handlers.
func (s *Server) registerRoutes() {
	wrap := s.authWrap

	s.mux.HandleFunc("GET /sessions", wrap(s.handleListSessions))
	s.mux.HandleFunc("POST /sessions", wrap(s.handleCreateSession))
	s.mux.HandleFunc("GET /sessions/{id}", wrap(s.handleGetSession))
	s.mux.HandleFunc("DELETE /sessions/{id}", wrap(s.handleDeleteSession))
	s.mux.HandleFunc("POST /sessions/{id}/resize", wrap(s.handleResize))
	s.mux.HandleFunc("POST /sessions/{id}/input", wrap(s.handleInput))
	s.mux.HandleFunc("GET /sessions/{id}/ws", wrap(s.handleWebSocket))
	s.mux.HandleFunc("POST /sessions/{id}/paste-upload", wrap(s.handleClipboardPasteUpload))
	s.mux.HandleFunc("POST /debug/logs", wrap(s.handleHudLog))
	s.mux.HandleFunc("GET /settings", wrap(s.handleGetSettings))
	s.mux.HandleFunc("GET /system", wrap(s.handleSystem))
	s.mux.HandleFunc("GET /tunnel/status", wrap(s.handleTunnelStatus))
	s.mux.HandleFunc("POST /tunnel/start", wrap(s.handleTunnelStart))
	s.mux.HandleFunc("POST /tunnel/stop", wrap(s.handleTunnelStop))
	s.mux.HandleFunc("GET /workbench", wrap(s.handleGetWorkbench))
	s.mux.HandleFunc("PUT /workbench", wrap(s.handleSaveWorkbench))
	s.mux.HandleFunc("GET /store", wrap(s.handleGetStore))
	s.mux.HandleFunc("PUT /store", wrap(s.handleSaveStore))
}

// authWrap wraps a handler with auth checking.
// ALL requests require auth code — no exceptions, no heuristics.
// This is the only secure design: headers and IPs can be spoofed,
// so we never trust them for access control decisions.
// Local users see the auth code in the console and authenticate once.
func (s *Server) authWrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-CLI-Auth")
		if token == "" {
			token = r.Header.Get("X-Auth-Code")
		}
		if token == "" {
			token = r.URL.Query().Get("auth")
		}
		if token == "" {
			if cookie, err := r.Cookie("cli_auth"); err == nil {
				token = cookie.Value
			}
		}
		if token != s.config.AuthCode {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized",
			})
			return
		}
		next(w, r)
	}
}
