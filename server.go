package terminal

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/brightman-ai/deepwork-terminal/authgate"
	"github.com/brightman-ai/deepwork-terminal/internal/spa"
	"github.com/brightman-ai/deepwork-terminal/notify"
	tunnelkit "github.com/brightman-ai/kit/tunnel"
	"github.com/brightman-ai/kit/webserve"
)

// Server is a complete terminal session HTTP service.
// Standalone: ListenAndServe() runs API + SPA.
// Embedded: Handler() returns API routes for a host to mount.
type Server struct {
	mux          *http.ServeMux
	mgr          *SessionManager
	hooks        Hooks
	config       Config
	listener     net.Listener
	tunnel       *tunnelkit.Tunnel
	tmuxProvider TmuxStateProvider
	push         *pushStore
	ilink        *ilinkStore
	coordinator  *notify.Coordinator
	uploads      *uploadIndex
	authThrottle *authgate.Throttle
	mu           sync.Mutex
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
	s.tunnel = tunnelkit.New(s.config.DataDir)
	s.authThrottle = authgate.NewThrottle()
	s.uploads = newUploadIndex(s.config.DataDir)
	s.mgr = NewSessionManager(s.config.BufferSize, s.config.DefaultShell)
	// Default in-process tmux provider so standalone gets tmux state without a host.
	// An injected provider (WithTmuxProvider) wins over the default.
	if s.tmuxProvider == nil {
		s.tmuxProvider = newDefaultTmuxProvider()
	}
	// Native agent-state push so standalone streams per-session agent state over
	// the existing WS transport without a host. A host-injected
	// Hooks.AgentStatePush wins over this default (back-compat).
	if s.hooks.AgentStatePush == nil {
		s.hooks.AgentStatePush = nativeAgentStatePush(s.newAgentIntelMonitor())
	}
	// Web Push: load (or generate) persisted VAPID keys + subscriptions. The
	// resolved subscriber ("sub" claim) must be a valid mailto:/https: URL or Apple
	// APNs rejects the token — see resolveVapidSubscriber.
	s.push = newPushStore(s.config.DataDir, resolveVapidSubscriber(s.config))
	s.push.server = s
	// WeChat iLink notification channel (channel B). Resumes a prior login if one
	// is persisted. Wired after push so newIlinkStore can reach s.push.ensureNotifier.
	s.ilink = newIlinkStore(s.config.DataDir, s)
	// Notification coordinator: fans an Event out to every enabled provider (iLink /
	// web push / Feishu / DingTalk / WeCom) and is the single delivery-metrics owner.
	// Built after ilink/push so its adapters can wrap them.
	s.coordinator = newNotifyCoordinator(s)
	// If subscriptions survived a restart, resume the background notifier so
	// push keeps working with no browser tab and no fresh subscribe call.
	if s.push.count() > 0 {
		s.push.ensureNotifier()
	}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	return s, nil
}

// tmuxInstalled reports whether tmux is available, via the default provider's
// service when present. Used to gate the push notifier (don't poll without tmux).
func (s *Server) tmuxInstalled() bool {
	if p, ok := s.tmuxProvider.(*defaultTmuxProvider); ok {
		return p.svc.TmuxInstalled()
	}
	// A host-injected provider implies a tmux-aware environment.
	return s.tmuxProvider != nil
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
	// CORS on the API so another deepwork-terminal instance's page (mesh remote terminal) can
	// reach this one's REST cross-origin. Standalone only — when EMBEDDED the host owns headers.
	root.Handle("/api/", corsMiddleware(http.StripPrefix("/api", s.mux)))
	// /fresh — a bookmarkable cache-buster: redirect to "/" with a unique query so the
	// browser can't reuse a stale cached index.html and loads the current build. A manual
	// escape hatch alongside the no-cache index.html + the in-app auto-reloader; the user's
	// existing query (e.g. ?auth=) is preserved.
	root.HandleFunc("/fresh", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		q.Set("t", strconv.FormatInt(time.Now().UnixNano(), 10))
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusFound)
	})
	root.Handle("/", spa.Handler())

	// Production security headers on every standalone response (API + SPA). The terminal
	// frames nothing, so frame-src 'none'. HSTS stays OFF: this listener is reached over
	// plain HTTP on the LAN (e.g. http://stwork:8087), and HSTS would pin that host to
	// HTTPS and brick HTTP access; the cloudflare tunnel owns HSTS for the public HTTPS
	// domain. The CSP carries no upgrade-insecure-requests, so HTTP origins load fine and
	// clipboard keeps its execCommand fallback. When EMBEDDED (Handler()) the host (pro)
	// owns the headers — this wraps only the standalone listener. SSOT: kit/webserve.
	// CSP mirrors webserve.SPACSP("'none'") EXCEPT connect-src: the remote-terminal mesh has THIS
	// page open WebSocket/fetch directly to OTHER deepwork-terminal instances (user-added peers at
	// arbitrary http LAN / https cloudflare hosts), which 'self' would block at the CSP layer. We
	// can't enumerate peer hosts ahead of time, so connect-src allows the needed schemes. Tradeoff:
	// looser XSS-exfiltration defense — acceptable for a tool that already grants full shell access;
	// every other directive stays strict. (Built inline to keep the change in this repo, not kit.)
	const meshCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self' http: https: ws: wss:; " +
		"worker-src 'self'; manifest-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'self'; " +
		"frame-ancestors 'self'; form-action 'self'"
	secured := webserve.Config{
		CSP:          meshCSP,
		FrameOptions: "SAMEORIGIN",
		HSTS:         false,
	}.Middleware(root)

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

	srv := &http.Server{Handler: secured}
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

// Close shuts down all sessions and stops the background push notifier.
func (s *Server) Close() error {
	if s.push != nil {
		s.push.stopNotifier()
	}
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
	// Cross-session resource drawer (WS5): global, top-level, not session-scoped.
	// Uploads come from the persisted index; inputs from claude/codex transcripts.
	s.mux.HandleFunc("GET /uploads", wrap(s.handleUploadsList))
	s.mux.HandleFunc("GET /uploads/raw", wrap(s.handleUploadsRaw))
	s.mux.HandleFunc("GET /inputs", wrap(s.handleInputs))
	s.mux.HandleFunc("POST /debug/logs", wrap(s.handleHudLog))
	s.mux.HandleFunc("GET /version", wrap(s.handleVersion))
	s.mux.HandleFunc("GET /settings", wrap(s.handleGetSettings))
	s.mux.HandleFunc("POST /auth/rotate", wrap(s.handleRotateAuthCode))
	s.mux.HandleFunc("GET /system", wrap(s.handleSystem))
	s.mux.HandleFunc("GET /tunnel/status", wrap(s.handleTunnelStatus))
	s.mux.HandleFunc("POST /tunnel/start", wrap(s.handleTunnelStart))
	s.mux.HandleFunc("POST /tunnel/stop", wrap(s.handleTunnelStop))
	s.mux.HandleFunc("GET /workbench", wrap(s.handleGetWorkbench))
	s.mux.HandleFunc("PUT /workbench", wrap(s.handleSaveWorkbench))
	s.mux.HandleFunc("GET /store", wrap(s.handleGetStore))
	s.mux.HandleFunc("PUT /store", wrap(s.handleSaveStore))
	s.mux.HandleFunc("GET /tmux/state", wrap(s.handleTmuxState))
	s.mux.HandleFunc("GET /tmux/prefix", wrap(s.handleTmuxPrefix))
	s.mux.HandleFunc("POST /tmux/copy-motion", wrap(s.handleTmuxCopyMotion))
	s.mux.HandleFunc("POST /tmux/refresh", wrap(s.handleTmuxRefresh))
	s.mux.HandleFunc("POST /tmux/new-session", wrap(s.handleTmuxNewSession))
	s.mux.HandleFunc("POST /tmux/overview", wrap(s.handleTmuxOverview))
	// Persist Claude Code's tui=classic (opt-in "remember" half of the fullscreen→classic advisory).
	s.mux.HandleFunc("POST /claude/tui-classic", wrap(s.handleClaudeTuiClassic))
	// Session workbench file service (CHG-016): anchored to session.CWD, traversal-safe.
	// recent = transcript tool_use signal; tree = single dir level; raw = bounded preview.
	s.mux.HandleFunc("GET /files/recent", wrap(s.handleFilesRecent))
	s.mux.HandleFunc("GET /files/tree", wrap(s.handleFilesTree))
	s.mux.HandleFunc("GET /files/search", wrap(s.handleFilesSearch))
	s.mux.HandleFunc("GET /files/raw", wrap(s.handleFilesRaw))
	// Session review (git diff): changed files + per-file unified diff for the workbench
	// cwd's repository. Read-only; every git arg is passed as a slice (no shell injection).
	s.mux.HandleFunc("GET /git/diff", wrap(s.handleGitDiff))
	// Session overview metrics (CHG-017): turn/summary breakdown of the CURRENT claude
	// transcript for the session cwd. Feeds the shared @ce OverviewPanel.
	s.mux.HandleFunc("GET /sessions/{id}/overview", wrap(s.handleSessionOverview))
	s.mux.HandleFunc("GET /push/vapid", wrap(s.handlePushVAPID))
	s.mux.HandleFunc("POST /push/subscribe", wrap(s.handlePushSubscribe))
	s.mux.HandleFunc("POST /push/unsubscribe", wrap(s.handlePushUnsubscribe))
	s.mux.HandleFunc("POST /push/test", wrap(s.handlePushTest))
	// WeChat iLink channel (channel B): scan-code login + status + logout.
	s.mux.HandleFunc("GET /ilink/status", wrap(s.handleIlinkStatus))
	s.mux.HandleFunc("POST /ilink/login", wrap(s.handleIlinkLogin))
	s.mux.HandleFunc("GET /ilink/qr", wrap(s.handleIlinkQR))
	s.mux.HandleFunc("POST /ilink/logout", wrap(s.handleIlinkLogout))
	// Unified notification status + delivery metrics (single source for the UI).
	s.mux.HandleFunc("GET /notify/status", wrap(s.handleNotifyStatus))
	// Fire one test notification down every enabled channel.
	s.mux.HandleFunc("POST /notify/test", wrap(s.handleNotifyTest))
	// Provider config SSOT (shared shape with deepwork-pro): on/off + redacted status,
	// per-provider webhook settings, per-provider test send. Registered WITHOUT the
	// /api prefix — the host mounts this mux under http.StripPrefix("/api", …) (see
	// below), so the client's /api/notify/config reaches /notify/config here.
	s.mux.HandleFunc("GET /notify/config", wrap(s.handleNotifyConfig))
	s.mux.HandleFunc("PUT /notify/config", wrap(s.handleNotifyConfigSave))
	s.mux.HandleFunc("PUT /notify/providers/{kind}/settings", wrap(s.handleNotifyProviderSettings))
	s.mux.HandleFunc("POST /notify/providers/{kind}/test", wrap(s.handleNotifyProviderTest))
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
		// authgate.CodeMatches is the SSOT auth-code comparison: constant-time (no
		// byte-by-byte timing probe — the mesh/tunnel widens who can guess) over the
		// normalized form (case-fold + strip hyphens/space, no entropy lost), shared
		// verbatim with deepwork-pro's WebUI middleware so the two boundaries agree.
		if !authgate.CodeMatches(token, s.authCode()) {
			// Global failure throttle: the default code is short (~39-bit) and a public tunnel
			// collapses all source IPs to localhost, so a single shared failure budget is what
			// actually bounds a brute-force. ONLY failures are charged — the success path below is
			// never delayed, so an honest login stays instant even mid-attack. See authThrottle.
			delay := s.authThrottle.Penalty()
			if delay > 0 {
				// Park this failed attempt to slow a guessing flood, but bail if the client hangs
				// up so we don't accumulate stuck goroutines. Then signal Retry-After + 429.
				select {
				case <-time.After(delay):
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(delay.Seconds()))))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many attempts",
				})
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized",
			})
			return
		}
		// Correct code → clear any built-up penalty so a later honest typo gets the full free burst.
		s.authThrottle.Reset()
		next(w, r)
	}
}

// corsMiddleware lets another deepwork-terminal instance's browser page call THIS instance's
// REST cross-origin (the mesh remote-terminal feature). It reflects the request Origin but
// deliberately does NOT set Access-Control-Allow-Credentials: the auth code carried in the
// explicit X-CLI-Auth header is the ONLY gate, so cookies must never ride along (that would turn
// an open CORS policy into a CSRF vector). Without credentials, browsers won't attach cookies
// cross-origin and won't expose responses to a caller lacking the code. Preflight (OPTIONS) is
// answered here, before auth, since it carries no credentials.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CLI-Auth, X-Auth-Code")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
