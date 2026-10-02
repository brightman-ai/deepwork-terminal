package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brightman-ai/kit/agentanalytics"
)

func TestOutcomeInvalidationPreservesTranscriptAndUsageMemos(t *testing.T) {
	reporter := newAgentReporter()
	reporter.revision = 7
	reporter.datasets["30d"] = datasetMemo{revision: 7}
	reporter.reports["30d|UTC"] = agentReportCacheEntry{builtAt: time.Now()}
	reporter.usageReports["30d|UTC"] = usageReportMemo{revision: 7}

	reporter.mu.Lock()
	reporter.invalidateOutcomeReportsLocked()
	reporter.mu.Unlock()

	if reporter.revision != 7 {
		t.Fatalf("outcome change moved transcript revision to %d, want 7", reporter.revision)
	}
	if memo, ok := reporter.datasets["30d"]; !ok || memo.revision != 7 {
		t.Fatal("outcome change discarded the combined transcript dataset")
	}
	if len(reporter.reports) != 0 {
		t.Fatal("outcome-derived report cache was not invalidated")
	}
	if memo, ok := reporter.usageReports["30d|UTC"]; !ok || memo.revision != 7 {
		t.Fatal("outcome change discarded an unrelated usage report")
	}
}

func TestHumanOutcomeRoundTripRequiresCompletedWorkAndIsIdempotent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	started, ended := now.Add(-time.Minute), now.Add(-time.Second)
	reporter := newOutcomeReporter(t, now, []agentanalytics.ActivityWorkItem{
		{ID: "work-completed", Runtime: "codex", Status: agentanalytics.LifecycleCompleted, Outcome: agentanalytics.OutcomeCompletedUnverified, SourceRef: "rollout:completed", StartedAt: &started, EndedAt: &ended},
		{ID: "work-open", Runtime: "codex", Status: agentanalytics.LifecycleStarted, Outcome: agentanalytics.OutcomeOpen, SourceRef: "rollout:open", StartedAt: &started},
	})
	var err error

	first, err := reporter.RecordHumanOutcome(context.Background(), "work-completed", agentanalytics.OutcomeHumanAccepted)
	if err != nil {
		t.Fatalf("record completed work: %v", err)
	}
	if first.Source != "human_feedback.v1" || first.OracleKind != "human_acceptance" || first.Ref != "rollout:completed" || first.Confidence != 1 {
		t.Fatalf("outcome evidence lost its provenance: %+v", first)
	}
	if _, err := reporter.RecordHumanOutcome(context.Background(), "work-open", agentanalytics.OutcomeHumanAccepted); err != errOutcomeWorkItemOpen {
		t.Fatalf("open work outcome error=%v, want %v", err, errOutcomeWorkItemOpen)
	}
	before, err := os.Stat(reporter.outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := reporter.RecordHumanOutcome(context.Background(), "work-completed", agentanalytics.OutcomeHumanAccepted)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	after, err := os.Stat(reporter.outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !retry.At.Equal(first.At) {
		t.Fatalf("retry duplicated evidence: size %d→%d, evidence time %s→%s", before.Size(), after.Size(), first.At, retry.At)
	}
	if mode := after.Mode().Perm(); mode != 0o600 {
		t.Fatalf("outcome evidence mode=%#o, want 0600", mode)
	}

	// Reports consume an overlay of the raw transcript memo: the visible outcome changes,
	// while the source projection remains completed_unverified.
	detail, evidence := reporter.DetailWithOutcomeEvidence(context.Background(), "30d", "UTC", agentanalytics.DetailFilter{Limit: 30})
	if got := detail.Report.Summary.VerifiedPass; got != 1 {
		t.Fatalf("accepted outcome count=%d, want 1", got)
	}
	if len(detail.Tasks) != 2 || detail.Tasks[0].Outcome != agentanalytics.OutcomeHumanAccepted {
		t.Fatalf("detail did not resolve human outcome: %+v", detail.Tasks)
	}
	if got := evidence["work-completed"]; len(got) != 1 || got[0].At != first.At {
		t.Fatalf("detail evidence=%+v, want the one persisted event", got)
	}
	if raw := reporter.datasets["30d"].dataset.WorkItems[0].Outcome; raw != agentanalytics.OutcomeCompletedUnverified {
		t.Fatalf("outcome overlay mutated source transcript memo: %q", raw)
	}

	var stored agentanalytics.OutcomeEvidence
	contents, err := os.ReadFile(reporter.outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents[:len(contents)-1], &stored); err != nil {
		t.Fatalf("read stored evidence: %v", err)
	}
	if stored.WorkItemID != "work-completed" || stored.Status != agentanalytics.OutcomeHumanAccepted {
		t.Fatalf("stored evidence=%+v", stored)
	}
}

func newOutcomeReporter(t *testing.T, now time.Time, workItems []agentanalytics.ActivityWorkItem) *agentReporter {
	t.Helper()
	dataDir := t.TempDir()
	codexHome := filepath.Join(dataDir, "codex")
	t.Setenv("DW_CODEX_HOME", codexHome)
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(dataDir, "claude", "projects"))
	t.Setenv("DEEPWORK_HOME", "")
	path := filepath.Join(codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"), "rollout-outcome.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reporter := newAgentReporter(dataDir)
	reporter.now = func() time.Time { return now }
	reporter.files[path] = agentFileProjection{
		size: info.Size(), modUnixNano: info.ModTime().UnixNano(), runtime: "codex", sessionID: "outcome",
		pricedWith: pricingSnapshot(),
		dataset:    agentanalytics.ActivityDataset{WorkItems: append([]agentanalytics.ActivityWorkItem(nil), workItems...)},
	}
	return reporter
}

func TestAgentOutcomeHTTPRoundTripKeepsPagedDetailAndSummaryConsistent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	newestStart, newestEnd := now.Add(-20*time.Second), now.Add(-10*time.Second)
	targetStart, targetEnd := now.Add(-2*time.Minute), now.Add(-time.Minute)
	openStart := now.Add(-time.Hour)
	reporter := newOutcomeReporter(t, now, []agentanalytics.ActivityWorkItem{
		{ID: "work-newest", Runtime: "codex", Status: agentanalytics.LifecycleCompleted, Outcome: agentanalytics.OutcomeCompletedUnverified, StartedAt: &newestStart, EndedAt: &newestEnd},
		{ID: "work-target", Runtime: "claude", Status: agentanalytics.LifecycleCompleted, Outcome: agentanalytics.OutcomeCompletedUnverified, SourceRef: "transcript:target", StartedAt: &targetStart, EndedAt: &targetEnd},
		{ID: "work-open", Runtime: "codex", Status: agentanalytics.LifecycleStarted, Outcome: agentanalytics.OutcomeOpen, StartedAt: &openStart},
	})
	srv := &Server{agentUsage: reporter}
	detail := func(cursor string) struct {
		Report struct {
			Summary struct {
				VerifiedPass int `json:"verified_pass"`
			} `json:"summary"`
		} `json:"report"`
		Tasks []struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
		} `json:"tasks"`
		NextCursor      string                                      `json:"next_cursor"`
		OutcomeEvidence map[string][]agentanalytics.OutcomeEvidence `json:"outcome_evidence"`
	} {
		query := "/usage/agent-report/detail?window=30d&timezone=UTC&limit=1"
		if cursor != "" {
			query += "&cursor=" + cursor // cursors are URL-safe base64
		}
		w := httptest.NewRecorder()
		srv.handleAgentReportDetail(w, httptest.NewRequest(http.MethodGet, query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
		}
		var got struct {
			Report struct {
				Summary struct {
					VerifiedPass int `json:"verified_pass"`
				} `json:"summary"`
			} `json:"report"`
			Tasks []struct {
				ID      string `json:"id"`
				Outcome string `json:"outcome"`
			} `json:"tasks"`
			NextCursor      string                                      `json:"next_cursor"`
			OutcomeEvidence map[string][]agentanalytics.OutcomeEvidence `json:"outcome_evidence"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode detail: %v body=%s", err, w.Body.String())
		}
		return got
	}

	firstPage := detail("")
	if len(firstPage.Tasks) != 1 || firstPage.Tasks[0].ID != "work-newest" || firstPage.NextCursor == "" {
		t.Fatalf("first detail page=%+v, want newest row and next cursor", firstPage)
	}
	secondPage := detail(firstPage.NextCursor)
	if len(secondPage.Tasks) != 1 || secondPage.Tasks[0].ID != "work-target" {
		t.Fatalf("second detail page=%+v, want target row", secondPage)
	}

	postOutcome := func(id, outcome string) int {
		t.Helper()
		body, err := json.Marshal(map[string]string{"work_item_id": id, "outcome": outcome})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/usage/agent-report/outcome", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		srv.handleAgentOutcome(w, r)
		return w.Code
	}
	if status := postOutcome("work-target", "human_accepted"); status != http.StatusOK {
		t.Fatalf("completed feedback status=%d, want 200", status)
	}
	if status := postOutcome("work-target", "human_accepted"); status != http.StatusOK {
		t.Fatalf("idempotent retry status=%d, want 200", status)
	}
	if status := postOutcome("work-open", "human_accepted"); status != http.StatusConflict {
		t.Fatalf("open work feedback status=%d, want 409", status)
	}
	if status := postOutcome("missing", "human_accepted"); status != http.StatusNotFound {
		t.Fatalf("missing work feedback status=%d, want 404", status)
	}

	refreshedPage := detail(firstPage.NextCursor)
	if len(refreshedPage.Tasks) != 1 || refreshedPage.Tasks[0].Outcome != string(agentanalytics.OutcomeHumanAccepted) {
		t.Fatalf("paged detail did not reflect feedback: %+v", refreshedPage.Tasks)
	}
	if refreshedPage.Report.Summary.VerifiedPass != 1 {
		t.Fatalf("report summary accepted count=%d, want 1", refreshedPage.Report.Summary.VerifiedPass)
	}
	if events := refreshedPage.OutcomeEvidence["work-target"]; len(events) != 1 || events[0].Ref != "transcript:target" {
		t.Fatalf("paged evidence=%+v, want one attributable event", events)
	}
}
