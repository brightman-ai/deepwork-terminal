package agentintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMetricsTranscript writes a synthetic Claude transcript under a fake HOME so
// SessionMetricsForCWD can resolve it from a cwd, mirroring the encoding ClaudeProjectDir
// uses. fname lets a test stage multiple transcripts to exercise newest-mtime selection.
func writeMetricsTranscript(t *testing.T, home, cwd, fname, jsonl string) string {
	t.Helper()
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	dir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	p := filepath.Join(dir, fname)
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return p
}

// TestSessionMetrics_TwoTurns parses a synthetic 2-turn transcript and asserts the
// turn delimitation, token summing, ttft computation, tool-call counting, and Group-B
// nullity.
//
// Turn 1: user "hello" → assistant (usage + 1 tool_use) → user(tool_result) →
//
//	assistant (usage, end_turn). Tokens of BOTH assistant msgs must sum.
//
// Turn 2: user "again" → assistant (usage + 2 tool_use).
func TestSessionMetrics_TwoTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	jsonl := strings.Join([]string{
		// ---- Turn 1 ----
		`{"type":"user","timestamp":"2026-06-10T10:00:00.000Z","message":{"content":"hello"}}`,
		// first assistant: ttft anchor (10:00:02), usage in1=100/out1=10/cr=5/cc=3, 1 tool_use
		`{"type":"assistant","timestamp":"2026-06-10T10:00:02.000Z","message":{"model":"claude-opus-4","id":"m1","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":3},"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
		// tool_result echo (user row, NOT a new turn), no is_error
		`{"type":"user","timestamp":"2026-06-10T10:00:03.000Z","message":{"content":[{"type":"tool_result","is_error":false,"content":"ok"}]}}`,
		// second assistant in same turn: usage in2=200/out2=20/cr=7/cc=1, end_turn, last ts 10:00:05
		`{"type":"assistant","timestamp":"2026-06-10T10:00:05.000Z","message":{"model":"claude-opus-4","id":"m2","stop_reason":"end_turn","usage":{"input_tokens":200,"output_tokens":20,"cache_read_input_tokens":7,"cache_creation_input_tokens":1},"content":[{"type":"text","text":"done"}]}}`,
		// ---- Turn 2 ----
		`{"type":"user","timestamp":"2026-06-10T10:01:00.000Z","message":{"content":"again"}}`,
		// assistant with 2 tool_use blocks, usage in=50/out=5
		`{"type":"assistant","timestamp":"2026-06-10T10:01:01.500Z","message":{"model":"claude-sonnet-4","id":"m3","usage":{"input_tokens":50,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"tool_use","name":"Read","input":{}},{"type":"tool_use","name":"Edit","input":{}}]}}`,
		"",
	}, "\n")
	writeMetricsTranscript(t, home, cwd, "session.jsonl", jsonl)

	pl := NewProjectLocator()
	m := SessionMetricsForCWD(pl, cwd, "sess-123", "My Session", true)

	// --- detail ---
	if m.Detail.ID != "sess-123" || m.Detail.Title != "My Session" || !m.Detail.Active {
		t.Errorf("detail id/title/active wrong: %+v", m.Detail)
	}
	if m.Detail.TurnCount != 2 {
		t.Errorf("detail.turn_count = %d, want 2", m.Detail.TurnCount)
	}
	if m.Detail.ModelID != "claude-sonnet-4" {
		t.Errorf("detail.model_id = %q, want latest 'claude-sonnet-4'", m.Detail.ModelID)
	}
	if m.Detail.EndedAt != nil {
		t.Errorf("detail.ended_at must be nil, got %v", *m.Detail.EndedAt)
	}
	if m.Detail.CreatedAt == "" || m.Detail.UpdatedAt == "" {
		t.Errorf("detail timestamps empty: created=%q updated=%q", m.Detail.CreatedAt, m.Detail.UpdatedAt)
	}

	// --- turns ---
	if len(m.Turns) != 2 {
		t.Fatalf("turn_count = %d, want 2", len(m.Turns))
	}
	t1 := m.Turns[0]
	if t1.TurnNumber != 1 || t1.UserInput != "hello" {
		t.Errorf("turn1 number/input wrong: %+v", t1)
	}
	// tokens summed across both assistant messages in turn 1
	if t1.InputTokens == nil || *t1.InputTokens != 300 {
		t.Errorf("turn1 input_tokens = %v, want 300", t1.InputTokens)
	}
	if t1.OutputTokens == nil || *t1.OutputTokens != 30 {
		t.Errorf("turn1 output_tokens = %v, want 30", t1.OutputTokens)
	}
	if t1.CacheReadTokens == nil || *t1.CacheReadTokens != 12 {
		t.Errorf("turn1 cache_read_tokens = %v, want 12", t1.CacheReadTokens)
	}
	if t1.CacheCreateTokens == nil || *t1.CacheCreateTokens != 4 {
		t.Errorf("turn1 cache_create_tokens = %v, want 4", t1.CacheCreateTokens)
	}
	// ttft = first assistant (10:00:02) − user (10:00:00) = 2000ms
	if t1.TtftMs == nil || *t1.TtftMs != 2000 {
		t.Errorf("turn1 ttft_ms = %v, want 2000", t1.TtftMs)
	}
	// duration = last assistant (10:00:05) − user (10:00:00) = 5000ms
	if t1.DurationMs != 5000 {
		t.Errorf("turn1 duration_ms = %d, want 5000", t1.DurationMs)
	}
	// output window = last(10:00:05) − first(10:00:02) = 3000ms
	if t1.OutputWindowMs == nil || *t1.OutputWindowMs != 3000 {
		t.Errorf("turn1 output_window_ms = %v, want 3000", t1.OutputWindowMs)
	}
	if t1.ToolCalls == nil || *t1.ToolCalls != 1 {
		t.Errorf("turn1 tool_calls = %v, want 1", t1.ToolCalls)
	}

	t2 := m.Turns[1]
	if t2.TurnNumber != 2 || t2.UserInput != "again" {
		t.Errorf("turn2 number/input wrong: %+v", t2)
	}
	if t2.ToolCalls == nil || *t2.ToolCalls != 2 {
		t.Errorf("turn2 tool_calls = %v, want 2", t2.ToolCalls)
	}
	// ttft = 10:01:01.5 − 10:01:00 = 1500ms
	if t2.TtftMs == nil || *t2.TtftMs != 1500 {
		t.Errorf("turn2 ttft_ms = %v, want 1500", t2.TtftMs)
	}

	// --- summary ---
	sum := m.Summary
	if sum.TurnCount != 2 || sum.UserPrompts != 2 {
		t.Errorf("summary turn_count/user_prompts = %d/%d, want 2/2", sum.TurnCount, sum.UserPrompts)
	}
	if sum.InputTokens != 350 { // 300 + 50
		t.Errorf("summary input_tokens = %d, want 350", sum.InputTokens)
	}
	if sum.OutputTokens != 35 { // 30 + 5
		t.Errorf("summary output_tokens = %d, want 35", sum.OutputTokens)
	}
	if sum.CacheReadTokens != 12 || sum.CacheCreateTokens != 4 {
		t.Errorf("summary cache tokens = %d/%d, want 12/4", sum.CacheReadTokens, sum.CacheCreateTokens)
	}
	if sum.ToolCalls != 3 { // 1 + 2
		t.Errorf("summary tool_calls = %d, want 3", sum.ToolCalls)
	}
	if sum.TotalDurationMs != 5000 { // turn1 5000 + turn2 (single assistant, last==first relative to user it's 1500) ... computed below
		// turn2 duration = last assistant − user = 1500ms; total = 5000 + 1500 = 6500
	}
	if sum.TotalDurationMs != 6500 {
		t.Errorf("summary total_duration_ms = %d, want 6500", sum.TotalDurationMs)
	}
	// tool_errors derivable (a tool_result with is_error field was seen) → non-nil, 0 errors
	if sum.ToolErrors == nil || *sum.ToolErrors != 0 {
		t.Errorf("summary tool_errors = %v, want non-nil 0", sum.ToolErrors)
	}
	if sum.StartedAt == "" {
		t.Errorf("summary started_at empty")
	}

	// --- Group B (non-derivable) must all be nil ---
	if sum.ModelCalls != nil || sum.AgentCalls != nil || sum.PermissionRequests != nil ||
		sum.ToolCallsByCategory != nil {
		t.Errorf("non-derivable Group-B fields must be nil: %+v", sum)
	}

	// --- cost IS derived (claude-sonnet-4 is a known model) ---
	if sum.TotalCost == nil || *sum.TotalCost <= 0 {
		t.Errorf("summary total_cost = %v, want a positive derived cost for known model", sum.TotalCost)
	}
	if sum.Currency != "USD" {
		t.Errorf("summary currency = %q, want USD", sum.Currency)
	}
	// --- top-level price = the model's reference unit price (per-MTok) ---
	if m.Price == nil {
		t.Fatalf("m.Price must be non-nil for known model claude-sonnet-4")
	}
	if m.Price.Input <= 0 || m.Price.Output <= 0 || m.Price.Currency != "USD" {
		t.Errorf("price unit prices invalid: %+v", m.Price)
	}
}

// TestSessionMetrics_ToolError asserts is_error=true in a tool_result is counted.
func TestSessionMetrics_ToolError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-06-10T10:00:00.000Z","message":{"content":"do it"}}`,
		`{"type":"assistant","timestamp":"2026-06-10T10:00:01.000Z","message":{"model":"claude-opus-4","id":"m1","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","name":"Bash","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-06-10T10:00:02.000Z","message":{"content":[{"type":"tool_result","is_error":true,"content":"boom"}]}}`,
		"",
	}, "\n")
	writeMetricsTranscript(t, home, cwd, "session.jsonl", jsonl)

	m := SessionMetricsForCWD(NewProjectLocator(), cwd, "s", "", false)
	if m.Summary.ToolErrors == nil || *m.Summary.ToolErrors != 1 {
		t.Errorf("tool_errors = %v, want 1", m.Summary.ToolErrors)
	}
}

// TestSessionMetrics_EmptyWhenNoTranscript asserts a codex/empty cwd yields a valid
// zero-shape, never an error.
func TestSessionMetrics_EmptyWhenNoTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := SessionMetricsForCWD(NewProjectLocator(), t.TempDir(), "sX", "T", true)
	if m.Detail.TurnCount != 0 || len(m.Turns) != 0 {
		t.Errorf("expected empty metrics, got turn_count=%d turns=%d", m.Detail.TurnCount, len(m.Turns))
	}
	if m.Detail.ID != "sX" || m.Detail.Title != "T" || !m.Detail.Active {
		t.Errorf("detail not echoed on empty: %+v", m.Detail)
	}
	// Group A counters zero, Group B + nullable Group A nil.
	if m.Summary.ToolErrors != nil || m.Summary.OutputWindowMs != nil || m.Summary.ModelCalls != nil {
		t.Errorf("empty summary nullable fields must be nil: %+v", m.Summary)
	}
	if m.Turns == nil {
		t.Errorf("turns must be non-nil empty slice for clean JSON []")
	}
}
