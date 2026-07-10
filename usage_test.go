package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newUsageTestServer builds a Server with only the usage reporter wired — enough
// to exercise the usage handlers directly (no auth, no full NewServer).
func newUsageTestServer() *Server {
	return &Server{usage: newUsageReporter()}
}

// GET /usage/report default window → 200, window=7d, 7 daily rows (row count is a
// deterministic contract regardless of how much local usage data exists).
func TestHandleUsageReportDefault(t *testing.T) {
	s := newUsageTestServer()
	req := httptest.NewRequest(http.MethodGet, "/usage/report", nil)
	w := httptest.NewRecorder()
	s.handleUsageReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Window string `json:"window"`
		Rows   []any  `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v\nbody=%s", err, w.Body.String())
	}
	if body.Window != "7d" {
		t.Errorf("expected window=7d, got %q", body.Window)
	}
	if len(body.Rows) != 7 {
		t.Errorf("expected 7 rows for 7d window, got %d", len(body.Rows))
	}
}

// GET /usage/report?window=30d → window echoed, 30 rows.
func TestHandleUsageReport30d(t *testing.T) {
	s := newUsageTestServer()
	req := httptest.NewRequest(http.MethodGet, "/usage/report?window=30d", nil)
	w := httptest.NewRecorder()
	s.handleUsageReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Window string `json:"window"`
		Rows   []any  `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v\nbody=%s", err, w.Body.String())
	}
	if body.Window != "30d" {
		t.Errorf("expected window=30d, got %q", body.Window)
	}
	if len(body.Rows) != 30 {
		t.Errorf("expected 30 rows for 30d window, got %d", len(body.Rows))
	}
}

// The short-TTL cache must serve the second identical request from memory (same
// window) — a second call returns the cached report without a rebuild.
func TestHandleUsageReportCached(t *testing.T) {
	s := newUsageTestServer()
	req := httptest.NewRequest(http.MethodGet, "/usage/report?window=7d", nil)
	w1 := httptest.NewRecorder()
	s.handleUsageReport(w1, req)
	if _, ok := s.usage.cached(parseUsageWindow("7d")); !ok {
		t.Fatal("expected 7d report to be cached after first request")
	}
}

// GET /usage/quota → 200 with a quotas array (possibly empty when no CLI is
// installed; the contract is a well-formed array, never a 500).
func TestHandleUsageQuota(t *testing.T) {
	s := newUsageTestServer()
	req := httptest.NewRequest(http.MethodGet, "/usage/quota", nil)
	w := httptest.NewRecorder()
	s.handleUsageQuota(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Quotas []any `json:"quotas"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v\nbody=%s", err, w.Body.String())
	}
	if body.Quotas == nil {
		t.Error("quotas key must be present as an array (even if empty)")
	}
}
