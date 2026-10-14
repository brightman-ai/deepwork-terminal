package terminal

import (
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// http_inflight.go — making "the page's HTTP stopped while its WebSocket kept working" visible.
//
// A browser opens at most six HTTP/1.1 connections to one host, shared by every tab of every
// page on it; WebSockets do not count. When six requests from one browser are held open, every
// further fetch — a paste upload, a file read, the page's own diagnostics — queues inside the
// browser, unseen by this server, while the terminals (WebSocket) look healthy. On 2026-10-13
// both of the page's log channels went silent for over ten minutes while keystrokes kept arriving,
// and nothing on this side could say why. This records the two facts that answer it: which
// requests a client is holding when it nears the limit, and which requests took long.

const (
	// browserConnLimit is the per-host HTTP/1.1 connection budget of every mainstream browser.
	browserConnLimit = 6
	// slowHTTPRequest is how long a plain request may take before it is worth a line: past it, a
	// browser's queue behind it is already a user-visible stall.
	slowHTTPRequest = 3 * time.Second
	// httpLogEvery bounds the log lines per client (near-limit) and per path (slow).
	httpLogEvery = 30 * time.Second
)

type inflightRequest struct {
	path  string
	start time.Time
}

type httpInflight struct {
	mu       sync.Mutex
	next     uint64
	byClient map[string]map[uint64]inflightRequest
	warnedAt map[string]time.Time // client → last near-limit line
	slowAt   map[string]time.Time // path → last slow line
	clockNow func() time.Time
	logNear  func(client string, held []inflightRequest, now time.Time)
	logSlow  func(client, method, path string, took time.Duration)
}

func newHTTPInflight() *httpInflight {
	return &httpInflight{
		byClient: make(map[string]map[uint64]inflightRequest),
		warnedAt: make(map[string]time.Time),
		slowAt:   make(map[string]time.Time),
		clockNow: time.Now,
		logNear: func(client string, held []inflightRequest, now time.Time) {
			paths := make([]string, 0, len(held))
			for _, r := range held {
				paths = append(paths, r.path+" "+now.Sub(r.start).Round(time.Millisecond).String())
			}
			logger.Warn("a browser is about to run out of HTTP connections to this server",
				"client", client, "in_flight", len(held), "limit", browserConnLimit, "held", paths,
				"effect", "its further HTTP requests queue in the browser (uploads, file reads, diagnostics) while the terminals keep working")
		},
		logSlow: func(client, method, path string, took time.Duration) {
			logger.Info("slow HTTP request", "client", client, "method", method, "path", path,
				"took_ms", took.Milliseconds(),
				"effect", "held one of the browser's six connections to this host for that long")
		},
	}
}

// requestPath is the path alone: the query can carry the auth code (?auth=), and it never goes
// into a log line.
func requestPath(r *http.Request) string { return r.URL.Path }

func clientOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *httpInflight) begin(client, path string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	id := h.next
	reqs := h.byClient[client]
	if reqs == nil {
		reqs = make(map[uint64]inflightRequest)
		h.byClient[client] = reqs
	}
	now := h.clockNow()
	reqs[id] = inflightRequest{path: path, start: now}
	if len(reqs) >= browserConnLimit-1 && now.Sub(h.warnedAt[client]) >= httpLogEvery {
		h.warnedAt[client] = now
		held := make([]inflightRequest, 0, len(reqs))
		for _, r := range reqs {
			held = append(held, r)
		}
		sort.Slice(held, func(i, j int) bool { return held[i].start.Before(held[j].start) })
		h.logNear(client, held, now)
	}
	return id
}

func (h *httpInflight) end(client, method string, id uint64) {
	h.mu.Lock()
	reqs := h.byClient[client]
	r, ok := reqs[id]
	delete(reqs, id)
	if len(reqs) == 0 {
		delete(h.byClient, client)
	}
	now := h.clockNow()
	var logIt bool
	if ok && now.Sub(r.start) >= slowHTTPRequest && now.Sub(h.slowAt[r.path]) >= httpLogEvery {
		h.slowAt[r.path] = now
		logIt = true
	}
	h.mu.Unlock()
	if logIt {
		h.logSlow(client, method, r.path, now.Sub(r.start))
	}
}

// inFlight is how many requests a client is holding right now.
func (h *httpInflight) inFlight(client string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byClient[client])
}

// middleware tracks every plain HTTP request. WebSocket upgrades are not tracked: they leave the
// browser's HTTP connection pool the moment they are upgraded, and live for as long as a tab does.
func (h *httpInflight) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}
		client := clientOf(r)
		id := h.begin(client, requestPath(r))
		defer h.end(client, r.Method, id)
		next.ServeHTTP(w, r)
	})
}
