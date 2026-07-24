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

	"github.com/brightman-ai/deepwork-terminal/internal/spa"
	"github.com/brightman-ai/deepwork-terminal/notify"
	"github.com/brightman-ai/kit/authgate"
	tunnelkit "github.com/brightman-ai/kit/tunnel"
	"github.com/brightman-ai/kit/webserve"
)

// standaloneCSP is the security policy for the STANDALONE listener (API + SPA). Package-level
// so a test can pin the two directives that are deliberately non-default and whose silent
// failure modes cost real debugging time: connect-src (remote-peer mesh) and frame-src (the
// html preview's sandboxed iframe). Rationale for each is at its use site in Run().
const standaloneCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self' http: https: ws: wss:; " +
	"worker-src 'self'; manifest-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'self'; " +
	"frame-ancestors 'self'; form-action 'self'"

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
	usage        *usageReporter
	agentUsage   *agentReporter
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
		s.config.AuthCode = authgate.Generate()
	}
	s.tunnel = tunnelkit.New(s.config.DataDir)
	s.authThrottle = authgate.NewThrottle()
	s.uploads = newUploadIndex(s.config.DataDir)
	// Runtime-configurable upload cap (upload_limit.go): load any persisted override
	// (<DataDir>/upload-limit.json) left by a previous SetUploadLimitMB, and remember
	// DataDir so a future SetUploadLimitMB call knows where to persist. Best-effort — a
	// missing file is not an error (LoadUploadLimit handles that); an unexpected I/O
	// error is logged but never blocks startup, since the compile-time
	// ClipboardMaxUploadSize default already stands as the fallback.
	if err := LoadUploadLimit(s.config.DataDir); err != nil {
		fmt.Fprintf(os.Stderr, "upload-limit: failed to load persisted limit: %v\n", err)
	}
	// Usage/cost/quota reporter (kit/usage SSOT). Cheap to build — sources only
	// resolve their roots here; no disk walk until a report is requested.
	s.usage = newUsageReporter()
	s.agentUsage = newAgentReporter(s.config.DataDir)
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
	// Resume the background notifier at startup whenever ANY delivery channel already
	// exists — a surviving web-push subscription, a persisted iLink (WeChat) login, or an
	// enabled webhook provider (Feishu/DingTalk/WeCom). Missing any arm left that channel's
	// users with no notifier after a restart (it only (re)started on a fresh web-push
	// subscribe / iLink login), so turn-end pushes silently stopped every restart.
	if s.push.count() > 0 || s.ilink.loggedIn() || s.coordinator.AnyEnabled(context.Background()) {
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
	// /fresh (or /refresh — both accepted, whichever the user types) — a bookmarkable
	// cache-buster: redirect to "/" with a unique query so the browser can't reuse a
	// stale cached index.html and loads the current build. A manual escape hatch
	// alongside the no-cache index.html + the in-app auto-reloader; the user's existing
	// query (e.g. ?auth=) is preserved.
	freshRedirect := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		q.Set("t", strconv.FormatInt(time.Now().UnixNano(), 10))
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusFound)
	}
	root.HandleFunc("/fresh", freshRedirect)
	root.HandleFunc("/refresh", freshRedirect)
	root.Handle("/", spa.Handler())

	// Production security headers on every standalone response (API + SPA).
	// HSTS stays OFF: this listener is reached over
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
	// frame-src is 'self' (not 'none'): the 文件 preview renders an .html 产物 inside a
	// SAME-ORIGIN sandboxed iframe (/files/raw?render=1). Under frame-src 'none' Chrome blocks
	// that frame and paints a broken-document glyph — the failure is silent and looks like a
	// server bug. pro passes 'self' for the same reason (webui.go SPACSP("'self'")); keeping
	// 'none' here was the standalone-vs-pro drift. Framing stays same-origin-only, and the
	// framed response carries its own locked-down CSP (files.go htmlRenderCSP).
	// script-src adds 'wasm-unsafe-eval' so the markdown reader's graphviz/dot renderer
	// (@hpcc-js/wasm) can instantiate its WebAssembly module; Chrome blocks WASM compile under a
	// bare 'self'. It grants WASM instantiation only, NOT JS eval()/new Function() — a much
	// narrower relaxation. (mermaid is pure JS and needs none of this.) NOTE: the EMBEDDED host
	// (pro, :8087) owns its own CSP header — it must add the same token for graphviz to render there.
	secured := webserve.Config{
		CSP:          standaloneCSP,
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

	srv := &http.Server{Handler: secured}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	// One canonical startup summary (see emitStartupBanner). Local is reachable the moment
	// Serve accepts, so we print before bringing up the optional tunnel.
	s.emitStartupBanner()

	// Public access on demand: open a Cloudflare quick tunnel and append its live URL to the
	// banner. Synchronous — Serve is already accepting above, so cloudflared can reach
	// localhost, and a printed URL means the tunnel is actually up (not merely requested).
	// ctx cancellation (Ctrl-C mid-connect) aborts the wait cleanly.
	if s.config.Tunnel {
		localAddr := fmt.Sprintf("http://localhost:%d", s.Port())
		if url, terr := s.tunnel.Start(ctx, localAddr); terr != nil {
			fmt.Fprintf(os.Stderr, "  Internet:  tunnel failed: %v\n\n", terr)
		} else {
			fmt.Printf("  Internet:  %s\n\n", url)
		}
	}

	select {
	case <-ctx.Done():
		srv.Close()
		return nil
	case err := <-serveErr:
		return err
	}
}

// emitStartupBanner prints the ONE canonical startup summary to stdout — the addresses to
// reach the server and the auth code to get in. It is the single owner of startup output
// (main.go prints nothing), so there is one format on one stream: an agent driving the CLI
// parses every access detail from stdout, and stderr stays reserved for errors. The Internet
// line is appended by the caller once the optional tunnel is live, so the trailing blank line
// is emitted here only when no tunnel will follow.
func (s *Server) emitStartupBanner() {
	port := s.Port()
	fmt.Printf("\n  dw-terminal %s\n\n", s.config.Version)
	fmt.Printf("  Local:     http://localhost:%d\n", port)
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		fmt.Printf("  Network:   http://%s:%d\n", hostname, port)
	}
	fmt.Printf("  Auth Code: %s\n", s.config.AuthCode)
	if !s.config.Tunnel {
		fmt.Println()
	}
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
	s.mux.HandleFunc("POST /tunnel/login", wrap(s.handleTunnelLogin))
	s.mux.HandleFunc("POST /tunnel/named", wrap(s.handleTunnelNamed))
	s.mux.HandleFunc("POST /tunnel/stop", wrap(s.handleTunnelStop))
	s.mux.HandleFunc("GET /workbench", wrap(s.handleGetWorkbench))
	s.mux.HandleFunc("PUT /workbench", wrap(s.handleSaveWorkbench))
	s.mux.HandleFunc("GET /store", wrap(s.handleGetStore))
	s.mux.HandleFunc("PUT /store", wrap(s.handleSaveStore))
	s.mux.HandleFunc("GET /tmux/state", wrap(s.handleTmuxState))
	s.mux.HandleFunc("GET /tmux/prefix", wrap(s.handleTmuxPrefix))
	s.mux.HandleFunc("POST /tmux/copy-motion", wrap(s.handleTmuxCopyMotion))
	s.mux.HandleFunc("POST /tmux/select-window", wrap(s.handleTmuxSelectWindow))
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
	// Write side of the workbench file service: mkdir/create/rename/delete, same
	// session-cwd anchoring + safeResolve traversal guard as the read handlers above.
	s.mux.HandleFunc("POST /files/mkdir", wrap(s.handleFilesMkdir))
	s.mux.HandleFunc("POST /files/create", wrap(s.handleFilesCreate))
	s.mux.HandleFunc("POST /files/rename", wrap(s.handleFilesRename))
	s.mux.HandleFunc("POST /files/delete", wrap(s.handleFilesDelete))
	// Chunked, resumable upload (files_chunk.go): slices a large file into ≤8 MiB parts so it
	// clears Cloudflare's ~100 MB body cap and survives a flaky mobile link (a dropped upload
	// resumes from the last landed chunk instead of restarting). Staging on disk under
	// <DataDir>/uploads-partial; same session-cwd anchoring + safeResolve guard as above.
	s.mux.HandleFunc("POST /files/upload/init", wrap(s.handleChunkUploadInit))
	s.mux.HandleFunc("POST /files/upload/chunk", wrap(s.handleChunkUploadChunk))
	s.mux.HandleFunc("POST /files/upload/complete", wrap(s.handleChunkUploadComplete))
	s.mux.HandleFunc("GET /files/upload/status", wrap(s.handleChunkUploadStatus))
	s.mux.HandleFunc("POST /files/upload/abort", wrap(s.handleChunkUploadAbort))
	// Runtime-configurable session upload cap (upload_limit.go SSOT): read the current
	// effective/default/floor/ceiling, or set a new one (persisted to DataDir).
	s.mux.HandleFunc("GET /files/upload-limit", wrap(s.handleUploadLimitGet))
	s.mux.HandleFunc("PUT /files/upload-limit", wrap(s.handleUploadLimitSet))
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
	// Usage/cost/quota (kit/usage SSOT, shared shape with deepwork-pro). Served here
	// so BOTH standalone (:18074, StripPrefix "/api") AND pro-embed (:8087, StripPrefix
	// "/api/cli") render the same UsageChip from one backend — no per-shell fork.
	s.mux.HandleFunc("GET /usage/quota", wrap(s.handleUsageQuota))
	// POST, not GET: this one has a side effect (a real provider request), and only the user
	// pressing 刷新 may trigger it — never a poll.
	s.mux.HandleFunc("POST /usage/quota/refresh", wrap(s.handleUsageQuotaRefresh))
	s.mux.HandleFunc("GET /usage/report", wrap(s.handleUsageReport))
	s.mux.HandleFunc("GET /usage/agent-report", wrap(s.handleAgentReport))
	s.mux.HandleFunc("GET /usage/agent-report/detail", wrap(s.handleAgentReportDetail))
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
			// Only charge the throttle for non-empty wrong codes — those are actual brute-force
			// guesses. An empty token means "not logged in yet" (browser startup, first visit);
			// penalising it exhausts the free burst before the user ever sees the auth dialog,
			// causing spurious 429s on regular API calls right after login.
			if token != "" {
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
			// Private Network Access (Chrome 104+): a page fetching a peer on a LAN/tailscale
			// (private/local) address space sends Access-Control-Request-Private-Network on the
			// preflight; without this ack the browser blocks the mesh fetch with an opaque
			// "Failed to fetch" (the reported remote-terminal "地址不可达") EVEN THOUGH the normal
			// CORS headers are all present. Echo the ack so the cross-origin peer probe/WS survive.
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
