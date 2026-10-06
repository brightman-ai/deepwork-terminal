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
	"context"
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

// handleUsageQuota → GET /api/usage/quota. Per-runtime account presence, billing mode,
// last-known 5h/7d windows and CLI health. Read-only: it reports what the runtimes have
// already written to disk, and never reaches out to a provider.
func queryUsageQuotasLocked() []usage.QuotaInfo {
	return reconcileCodexAttribution(usage.QueryAllQuotas(), usageCredentialSource)
}

func (s *Server) queryUsageQuotas() []usage.QuotaInfo {
	usageCredentialSourceMu.RLock()
	defer usageCredentialSourceMu.RUnlock()
	return queryUsageQuotasLocked()
}

func (s *Server) handleUsageQuota(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"quotas": s.queryUsageQuotas()})
}

// handleUsageQuotaRefresh → POST /api/usage/quota/refresh. The USER-INITIATED refresh: it
// asks each runtime that CAN be asked for its current quota, instead of re-reading a file
// that may have nothing new in it.
//
// This exists because re-reading the disk cannot always help. Codex records only the
// rate-limit family of the model it is currently running, so while a session works on a
// per-model plan the ACCOUNT limit stops being written entirely — its newest reading on
// disk can be hours stale and no amount of polling will improve it. Pressing 刷新 must
// actually go and ask.
//
// WHICH runtimes can be asked is the domain's business, not this handler's: kit/usage owns
// the provider registry, and this layer only relays what each one did. A probe failure is not
// an error for the caller — the response still carries the (offline) quotas, so the UI
// degrades to the last-known reading rather than showing nothing.
func (s *Server) handleUsageQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	usageCredentialSourceMu.RLock()
	defer usageCredentialSourceMu.RUnlock()
	probe := usage.ProbeAll(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"quotas": queryUsageQuotasLocked(),
		"probe":  probe,
	})
}

// handleUsageReport → GET /api/usage/report?window=24h|7d|14d|30d. Per-provider token + cost
// breakdown.
//
// The UI prefetches all four windows at once, so this is answered four times per open. It is
// served from a revision-keyed cache rather than a timed one: a report is reused exactly
// while the facts behind it have not moved, so nothing is recomputed and nothing goes stale
// waiting for a TTL. (This comment used to promise a short-TTL cache that a July refactor had
// already removed, which is how four uncached full refreshes ended up queueing on one mutex.)
func (s *Server) handleUsageReport(w http.ResponseWriter, r *http.Request) {
	usageCredentialSourceMu.RLock()
	defer usageCredentialSourceMu.RUnlock()
	window := parseUsageWindow(r.URL.Query().Get("window"))
	// New servers share the exact ModelRequestUsage materialized facts with the
	// Agent report. This is the authoritative path for tier/effective-date/cache
	// TTL/local-calendar pricing. The fallback keeps older isolated handler tests
	// and embedders with only a legacy usageReporter compatible.
	if s.agentUsage != nil {
		timezone := r.URL.Query().Get("timezone")
		if timezone == "" {
			timezone = "Asia/Shanghai"
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_timezone"})
			return
		}
		writeJSON(w, http.StatusOK, s.agentUsage.UsageReport(r.Context(), string(window), timezone))
		return
	}

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

// reportWarmWindows is exactly the set the UI prefetches on every popover open; warming
// these keeps the answer at click distance instead of one cold scan behind whichever
// window the user happened to click first.
var reportWarmWindows = []string{"24h", "7d", "14d", "30d"}

// reportWarmTimezone is the operator's own calendar. Reports are memoized per timezone, so
// a click from another tz still pays its first scan — warming the primary one is the 95%
// case, not a promise about all of them.
const reportWarmTimezone = "Asia/Shanghai"

const reportWarmInterval = time.Minute

// startReportWarmer recomputes the report windows on a ticker. The report cache is
// revision-keyed — invalidated by exactly the sessions an active machine keeps writing —
// so on a working machine every popover open used to pay the full scans again (observed
// 2026-10-06: 7d alone 4-8s cold, four windows prefetching into one reporter mutex; the
// fee section read as permanently empty because the user closes it before the wait ends).
// Warm on the clock instead: a click then waits at most one scan, usually a memo hit, and
// the 拉取于 timestamp on screen keeps an honestly-stale answer honest.
func (s *Server) startReportWarmer(ctx context.Context) {
	if s.agentUsage == nil || testRunWithoutIsolatedDeepworkHome() {
		return // no authoritative path, or a go test that must not touch the developer's home
	}
	if _, err := time.LoadLocation(reportWarmTimezone); err != nil {
		return // same validation as the handler; without a usable tz there is nothing to warm
	}
	warm := func() {
		usageCredentialSourceMu.RLock()
		defer usageCredentialSourceMu.RUnlock()
		for _, w := range reportWarmWindows {
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.agentUsage.UsageReport(ctx, w, reportWarmTimezone)
		}
	}
	warm()
	go func() {
		ticker := time.NewTicker(reportWarmInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				warm()
			}
		}
	}()
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
