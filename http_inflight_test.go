package terminal

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A client nearing the browser's connection budget is reported once, with what it holds.
func TestHTTPInflightReportsAClientNearingTheBrowserLimit(t *testing.T) {
	h := newHTTPInflight()
	var reported [][]inflightRequest
	h.logNear = func(_ string, held []inflightRequest, _ time.Time) { reported = append(reported, held) }
	h.logSlow = func(string, string, string, time.Duration) {}

	release := make(chan struct{})
	var started, done sync.WaitGroup
	handler := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		<-release
	}))
	for i := 0; i < browserConnLimit-1; i++ {
		started.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/sessions/x/overview?auth=SECRET", nil))
		}()
	}
	started.Wait()
	if got := h.inFlight("192.0.2.1"); got != browserConnLimit-1 {
		t.Fatalf("in flight = %d, want %d", got, browserConnLimit-1)
	}
	close(release)
	done.Wait()

	if len(reported) != 1 {
		t.Fatalf("near-limit reports = %d, want exactly 1 (rate-limited per client)", len(reported))
	}
	for _, r := range reported[0] {
		if r.path != "/sessions/x/overview" {
			t.Fatalf("logged path %q: the query (which can hold the auth code) must never be logged", r.path)
		}
	}
	if got := h.inFlight("192.0.2.1"); got != 0 {
		t.Fatalf("in flight after completion = %d, want 0", got)
	}
}

// WebSocket upgrades leave the browser's HTTP pool; counting them would cry wolf for every tab.
func TestHTTPInflightIgnoresWebSocketUpgrades(t *testing.T) {
	h := newHTTPInflight()
	var seen int
	handler := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = h.inFlight("192.0.2.1") }))
	req := httptest.NewRequest("GET", "/sessions/x/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if seen != 0 {
		t.Fatalf("a WebSocket upgrade was counted as an HTTP request in flight")
	}
}

func TestHTTPInflightReportsSlowRequestsOncePerPathPerInterval(t *testing.T) {
	h := newHTTPInflight()
	now := time.Unix(1000, 0)
	h.clockNow = func() time.Time { return now }
	var slow []string
	h.logSlow = func(_, _, path string, _ time.Duration) { slow = append(slow, path) }
	h.logNear = func(string, []inflightRequest, time.Time) {}

	for i := 0; i < 3; i++ {
		id := h.begin("c", "/files/read")
		now = now.Add(slowHTTPRequest)
		h.end("c", "GET", id)
	}
	id := h.begin("c", "/fast")
	h.end("c", "GET", id)
	if len(slow) != 1 || slow[0] != "/files/read" {
		t.Fatalf("slow reports = %v, want one for /files/read (rate-limited) and none for /fast", slow)
	}
}
