package terminal

import (
	"testing"
	"time"
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
