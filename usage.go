package terminal

// Usage/cost/quota HTTP surface — the host-agnostic SSOT that lets BOTH the
// standalone terminal (:18074) AND the pro-embedded terminal (:8087, via the
// /api/cli StripPrefix forward) render the same UsageChip from ONE backend.
//
// The domain compute lives in kit/usage (ccusage-style: local transcript logs +
// kit/pricing, no DB, no network). This file is only the thin HTTP adapter +
// a short-TTL report cache, mirroring the notify surface (metrics.go): handlers
// are *Server methods, registered WITHOUT the /api prefix (the host mounts the
// mux under http.StripPrefix("/api", …) — or /api/cli when embedded).

import (
	"net/http"
	"sync"
	"time"

	"github.com/brightman-ai/kit/usage"
)

// usageReporter owns the (claude JSONL + codex rollout) scan source and a
// short-TTL cache for the expensive full-tree report. Held on the Server rather
// than as package globals so it is per-instance and GC'd with the server.
type usageReporter struct {
	source usage.TokenSource
	ttl    time.Duration

	mu    sync.Mutex
	cache map[usage.WindowKind]usageReportEntry
}

type usageReportEntry struct {
	report  usage.UsageReport
	builtAt time.Time
}

// newUsageReporter builds the combined source once. NewJSONLTokenSource /
// NewCodexModelScanSource only resolve their roots ($HOME + env overrides); no
// disk is walked until a request actually builds a report.
func newUsageReporter() *usageReporter {
	return &usageReporter{
		source: usage.TokenSource(&usage.CombinedModelScanSource{
			Sources: []usage.ModelScanSource{
				usage.NewJSONLTokenSource(),
				usage.NewCodexModelScanSource(),
			},
		}),
		ttl:   60 * time.Second,
		cache: map[usage.WindowKind]usageReportEntry{},
	}
}

// cached returns a report for window when it is younger than ttl.
func (u *usageReporter) cached(window usage.WindowKind) (usage.UsageReport, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	entry, ok := u.cache[window]
	if !ok || time.Since(entry.builtAt) > u.ttl {
		return usage.UsageReport{}, false
	}
	return entry.report, true
}

// store caches report for window with the current build time.
func (u *usageReporter) store(window usage.WindowKind, report usage.UsageReport) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cache[window] = usageReportEntry{report: report, builtAt: time.Now()}
}

// handleUsageQuota → GET /api/usage/quota. Subscription 5h/7d remaining% per
// detected runtime (claude/codex). Honest degradation when a runtime is absent
// or its rate-limit drop file has not been written yet (available=false).
func (s *Server) handleUsageQuota(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"quotas": usage.QueryAllQuotas()})
}

// handleUsageReport → GET /api/usage/report?window=24h|7d|14d|30d. Per-provider
// token + cost breakdown, served from a short-TTL cache so repeated hits don't
// re-walk ~/.claude/projects/** and ~/.codex/sessions/** every time.
func (s *Server) handleUsageReport(w http.ResponseWriter, r *http.Request) {
	window := parseUsageWindow(r.URL.Query().Get("window"))

	report, ok := s.usage.cached(window)
	if !ok {
		report = usage.BuildReport(window, s.usage.source)
		if report.Available {
			report.DataSource = "claude_jsonl+codex_rollout"
		}
		s.usage.store(window, report)
	}
	writeJSON(w, http.StatusOK, report)
}

// parseUsageWindow maps the query value to a WindowKind, defaulting to 7d.
func parseUsageWindow(v string) usage.WindowKind {
	switch v {
	case "24h":
		return usage.Window24h
	case "14d":
		return usage.Window14d
	case "30d":
		return usage.Window30d
	default:
		return usage.Window7d
	}
}
