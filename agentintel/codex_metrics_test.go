package agentintel

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRollout writes lines (already JSON strings) to a rollout file under dir.
func writeRollout(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return path
}

// TestParseCodexRollout_TwoEvents parses a synthetic 2-token_count-event rollout and
// asserts: model resolves from turn_context, tokens sum with cached split out of input,
// cost > 0 in USD.
func TestParseCodexRollout_TwoEvents(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"timestamp":"2026-06-14T23:09:50.000Z","type":"session_meta","payload":{"cwd":"/home/ubuntu/proj","model_provider":"openai"}}`,
		`{"timestamp":"2026-06-14T23:09:51.000Z","type":"turn_context","payload":{"model":"gpt-5.5","cwd":"/home/ubuntu/proj"}}`,
		`{"timestamp":"2026-06-14T23:09:52.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"first question"}]}}`,
		// event 1: input 1000 (cached 200), output 100 → non-cached 800
		`{"timestamp":"2026-06-14T23:09:55.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":200,"output_tokens":100,"reasoning_output_tokens":30,"total_tokens":1100},"total_token_usage":{"input_tokens":1000,"cached_input_tokens":200,"output_tokens":100,"reasoning_output_tokens":30,"total_tokens":1100},"model_context_window":258400}}}`,
		`{"timestamp":"2026-06-14T23:10:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"second question"}]}}`,
		// event 2: input 2000 (cached 500), output 300 → non-cached 1500
		`{"timestamp":"2026-06-14T23:10:05.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":2000,"cached_input_tokens":500,"output_tokens":300,"reasoning_output_tokens":80,"total_tokens":2300},"total_token_usage":{"input_tokens":3000,"cached_input_tokens":700,"output_tokens":400,"reasoning_output_tokens":110,"total_tokens":3400},"model_context_window":258400}}}`,
	}
	path := writeRollout(t, dir, "rollout-test.jsonl", lines)

	m := parseCodexRollout(path, "sess-1", "Codex Pane", true)

	// Model.
	if m.Detail.ModelID != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", m.Detail.ModelID)
	}
	// Turns: two user messages.
	if m.Detail.TurnCount != 2 {
		t.Fatalf("turn_count = %d, want 2", m.Detail.TurnCount)
	}
	if m.Summary.UserPrompts != 2 {
		t.Fatalf("user_prompts = %d, want 2", m.Summary.UserPrompts)
	}

	// Token sums: cached split OUT of input.
	// non-cached input = (1000-200) + (2000-500) = 800 + 1500 = 2300
	if m.Summary.InputTokens != 2300 {
		t.Fatalf("input_tokens (non-cached) = %d, want 2300", m.Summary.InputTokens)
	}
	// cache_read = 200 + 500 = 700
	if m.Summary.CacheReadTokens != 700 {
		t.Fatalf("cache_read_tokens = %d, want 700", m.Summary.CacheReadTokens)
	}
	// output = 100 + 300 = 400
	if m.Summary.OutputTokens != 400 {
		t.Fatalf("output_tokens = %d, want 400", m.Summary.OutputTokens)
	}
	// no cache-write tier for OpenAI.
	if m.Summary.CacheCreateTokens != 0 {
		t.Fatalf("cache_create_tokens = %d, want 0", m.Summary.CacheCreateTokens)
	}

	// Cost > 0 in USD.
	if m.Summary.TotalCost == nil {
		t.Fatal("total_cost is nil, want > 0")
	}
	if *m.Summary.TotalCost <= 0 {
		t.Fatalf("total_cost = %v, want > 0", *m.Summary.TotalCost)
	}
	if m.Summary.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", m.Summary.Currency)
	}

	// Expected cost (base tier 5/30/0.5 per MTok, context < 272k):
	//   event1: 800*5/1e6 + 100*30/1e6 + 200*0.5/1e6 = 0.004 + 0.003 + 0.0001 = 0.0071
	//   event2: 1500*5/1e6 + 300*30/1e6 + 500*0.5/1e6 = 0.0075 + 0.009 + 0.00025 = 0.01675
	//   total = 0.02385
	want := 0.02385
	got := *m.Summary.TotalCost
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("total_cost = %v, want %v", got, want)
	}

	// Price row is the gpt-5.5 reference.
	if m.Price == nil {
		t.Fatal("price is nil, want gpt-5.5 reference")
	}
	if m.Price.Input != 5 || m.Price.Output != 30 || m.Price.CacheRead != 0.5 {
		t.Fatalf("price = %+v, want 5/30/0.5", m.Price)
	}
	if m.Price.ContextThreshold != 272000 {
		t.Fatalf("context_threshold = %d, want 272000", m.Price.ContextThreshold)
	}
}

// TestParseCodexRollout_Empty asserts a missing/empty path yields a valid-but-empty shape.
func TestCodexSessionMetricsForCWD_NoRollout(t *testing.T) {
	pl := NewProjectLocator()
	// A cwd with no matching rollout and (in CI) likely no ~/.codex at all → empty shape,
	// never a panic. (If the host HAS codex rollouts, it falls back to the newest; either
	// way the shape is valid.)
	m := CodexSessionMetricsForCWD(pl, "/nonexistent/path/xyz", "s", "t", false)
	if m.Turns == nil {
		t.Fatal("turns is nil, want empty slice")
	}
	if m.Detail.ID != "s" || m.Detail.Title != "t" {
		t.Fatalf("detail echo wrong: %+v", m.Detail)
	}
}
