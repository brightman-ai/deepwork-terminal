package terminal

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brightman-ai/deepwork-terminal/authgate"
)

// TestAuthWrap_Throttle exercises the real authWrap chokepoint wired to authgate.Throttle: the
// throttle is GLOBAL (failures from different source IPs share one budget), trips into 429 +
// Retry-After past the burst, and a CORRECT code is served instantly even mid-attack (never delayed,
// never locked out). The unit-level decay/cap/burst math is covered in package authgate.
func TestAuthWrap_Throttle(t *testing.T) {
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     "E3X1-M6T2",
		DataDir:      t.TempDir(),
	}))
	require.NoError(t, err)
	require.IsType(t, &authgate.Throttle{}, srv.authThrottle)

	handler := srv.authWrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrong := func(remoteAddr string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest("GET", "/sessions", nil)
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-CLI-Auth", "WRONG-CODE")
		w := httptest.NewRecorder()
		handler(w, req)
		return w
	}

	// Within the free burst: plain 401, no throttle — and failures from TWO different IPs count
	// against the SAME global budget (per-IP would never trip behind a tunnel). Rapid calls make
	// score decay negligible, so N failures ≈ score N.
	addrs := []string{"10.0.0.5:1111", "10.0.0.9:2222"}
	for i := 0; i < 5; i++ {
		w := wrong(addrs[i%2])
		assert.Equal(t, http.StatusUnauthorized, w.Code, "failure %d should be a plain 401", i+1)
	}

	// One more failure (different IP again) is past the SHARED burst → 429 + Retry-After.
	w := wrong("10.0.0.5:3333")
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "past the global burst → 429")
	secs, convErr := strconv.Atoi(w.Header().Get("Retry-After"))
	require.NoError(t, convErr, "Retry-After must be an integer seconds value")
	assert.GreaterOrEqual(t, secs, 1, "Retry-After should be at least 1s")

	// The CORRECT code is served instantly even with the throttle hot — never delayed, never locked.
	req, _ := http.NewRequest("GET", "/sessions", nil)
	req.RemoteAddr = "10.0.0.9:4444"
	req.Header.Set("X-CLI-Auth", "E3X1-M6T2")
	wOK := httptest.NewRecorder()
	start := time.Now()
	handler(wOK, req)
	elapsed := time.Since(start)
	assert.Equal(t, http.StatusOK, wOK.Code, "correct code must pass even mid-attack")
	assert.Less(t, elapsed, 100*time.Millisecond, "correct code must NOT be delayed by the throttle")
}
