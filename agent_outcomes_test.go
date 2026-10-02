package terminal

import (
	"context"
	"encoding/json"
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
	dataDir := t.TempDir()
	codexHome := filepath.Join(dataDir, "codex")
	t.Setenv("DW_CODEX_HOME", codexHome)
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(dataDir, "claude", "projects"))
	t.Setenv("DEEPWORK_HOME", "")
	now := time.Now().UTC().Truncate(time.Second)
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
	started, ended := now.Add(-time.Minute), now.Add(-time.Second)
	reporter := newAgentReporter(dataDir)
	reporter.now = func() time.Time { return now }
	reporter.files[path] = agentFileProjection{
		size: info.Size(), modUnixNano: info.ModTime().UnixNano(), runtime: "codex", sessionID: "outcome",
		pricedWith: pricingSnapshot(),
		dataset: agentanalytics.ActivityDataset{WorkItems: []agentanalytics.ActivityWorkItem{
			{ID: "work-completed", Runtime: "codex", Status: agentanalytics.LifecycleCompleted, Outcome: agentanalytics.OutcomeCompletedUnverified, SourceRef: "rollout:completed", StartedAt: &started, EndedAt: &ended},
			{ID: "work-open", Runtime: "codex", Status: agentanalytics.LifecycleStarted, Outcome: agentanalytics.OutcomeOpen, SourceRef: "rollout:open", StartedAt: &started},
		}},
	}

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
