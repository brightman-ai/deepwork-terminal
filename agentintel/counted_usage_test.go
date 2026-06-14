package agentintel

import (
	"testing"
)

func TestUsageAccumulator_Dedup(t *testing.T) {
	ua := NewUsageAccumulator()
	key := CountedUsageKey{SessionID: "sess1", MessageID: "msg1"}

	// First ingest: delta = current
	d1 := ua.Ingest(key, UsageTotals{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})
	if d1.InputTokens != 10 || d1.OutputTokens != 5 {
		t.Fatalf("first ingest delta wrong: %+v", d1)
	}
	if ua.Totals.InputTokens != 10 {
		t.Fatalf("totals wrong after first: %+v", ua.Totals)
	}

	// Second ingest with same values: delta must be 0 (duplicate).
	d2 := ua.Ingest(key, UsageTotals{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})
	if d2.InputTokens != 0 || d2.OutputTokens != 0 {
		t.Fatalf("duplicate should produce zero delta: %+v", d2)
	}
	if ua.Totals.InputTokens != 10 {
		t.Fatalf("totals must not grow on duplicate: %+v", ua.Totals)
	}

	// Third ingest with higher values (streaming update): delta = diff.
	d3 := ua.Ingest(key, UsageTotals{InputTokens: 10, OutputTokens: 20, TotalTokens: 30})
	if d3.OutputTokens != 15 {
		t.Fatalf("incremental delta wrong: %+v", d3)
	}
	if ua.Totals.OutputTokens != 20 {
		t.Fatalf("totals after increment wrong: %+v", ua.Totals)
	}
}

func TestUsageAccumulator_MultiMessage(t *testing.T) {
	ua := NewUsageAccumulator()
	k1 := CountedUsageKey{SessionID: "s", MessageID: "m1"}
	k2 := CountedUsageKey{SessionID: "s", MessageID: "m2"}

	ua.Ingest(k1, UsageTotals{InputTokens: 100, OutputTokens: 50, TotalTokens: 150})
	ua.Ingest(k2, UsageTotals{InputTokens: 200, OutputTokens: 80, TotalTokens: 280})

	if ua.Totals.InputTokens != 300 {
		t.Fatalf("expected 300 input tokens, got %d", ua.Totals.InputTokens)
	}
	if ua.Totals.OutputTokens != 130 {
		t.Fatalf("expected 130 output tokens, got %d", ua.Totals.OutputTokens)
	}
}

func TestUsageAccumulator_CacheFields(t *testing.T) {
	ua := NewUsageAccumulator()
	key := CountedUsageKey{SessionID: "s", MessageID: "m"}

	ua.Ingest(key, UsageTotals{
		InputTokens:       1,
		OutputTokens:      2,
		CacheReadTokens:   500,
		CacheCreateTokens: 100,
		TotalTokens:       603,
	})
	// Duplicate — all zero delta.
	ua.Ingest(key, UsageTotals{
		InputTokens:       1,
		OutputTokens:      2,
		CacheReadTokens:   500,
		CacheCreateTokens: 100,
		TotalTokens:       603,
	})

	if ua.Totals.CacheReadTokens != 500 {
		t.Fatalf("cache read tokens wrong: %d", ua.Totals.CacheReadTokens)
	}
	if ua.Totals.CacheCreateTokens != 100 {
		t.Fatalf("cache create tokens wrong: %d", ua.Totals.CacheCreateTokens)
	}
}
