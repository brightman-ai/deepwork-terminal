package agentintel

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func writeJSONL(t *testing.T, rows []map[string]any) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	return f.Name()
}

func makeAssistantRow(msgID, model, stopReason string, inputTokens, outputTokens, cacheRead int) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{
			"id":          msgID,
			"model":       model,
			"stop_reason": stopReason,
			"usage": map[string]any{
				"input_tokens":                float64(inputTokens),
				"output_tokens":               float64(outputTokens),
				"cache_read_input_tokens":     float64(cacheRead),
				"cache_creation_input_tokens": float64(0),
			},
		},
	}
}

func makeUserRow() map[string]any {
	return map[string]any{
		"type":      "user",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message":   map[string]any{"content": "hello"},
	}
}

func TestClaudeDriver_BasicParsing(t *testing.T) {
	rows := []map[string]any{
		makeUserRow(),
		makeAssistantRow("msg-1", "claude-3-5-sonnet", "end_turn", 1, 200, 5000),
	}
	path := writeJSONL(t, rows)

	d := NewClaudeDriver(path, "sess-abc")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	s := d.State()
	if s.Model != "claude-3-5-sonnet" {
		t.Errorf("model: got %q", s.Model)
	}
	if s.Status != StatusIdle {
		t.Errorf("status: got %q, want idle", s.Status)
	}
	if s.Usage.OutputTokens != 200 {
		t.Errorf("output tokens: got %d, want 200", s.Usage.OutputTokens)
	}
	if s.Usage.CacheReadTokens != 5000 {
		t.Errorf("cache read tokens: got %d, want 5000", s.Usage.CacheReadTokens)
	}
}

func TestClaudeDriver_DuplicateRowDedup(t *testing.T) {
	// Claude sends 2-10 identical usage rows per message — only one should count.
	rows := []map[string]any{
		makeAssistantRow("msg-2", "claude-opus-4", "tool_use", 1, 300, 8000),
		makeAssistantRow("msg-2", "claude-opus-4", "tool_use", 1, 300, 8000),
		makeAssistantRow("msg-2", "claude-opus-4", "tool_use", 1, 300, 8000),
	}
	path := writeJSONL(t, rows)

	d := NewClaudeDriver(path, "sess-dup")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	s := d.State()
	if s.Usage.OutputTokens != 300 {
		t.Errorf("dedup failed: output tokens = %d, want 300", s.Usage.OutputTokens)
	}
	if s.Status != StatusRunning {
		t.Errorf("status: want running (tool_use), got %q", s.Status)
	}
}

func TestClaudeDriver_AssistantWithoutStopReasonRunning(t *testing.T) {
	rows := []map[string]any{
		{
			"type":      "assistant",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"message": map[string]any{
				"id":    "msg-running",
				"model": "claude-3-5-sonnet",
			},
		},
	}
	path := writeJSONL(t, rows)

	d := NewClaudeDriver(path, "sess-running")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	if got := d.State().Status; got != StatusRunning {
		t.Fatalf("status: got %q, want running", got)
	}
}

// makeAssistantToolUseRow builds an assistant turn that ends in a tool call to
// toolName (with a tool_use content block), so PendingTool detection has input.
func makeAssistantToolUseRow(msgID, model, toolName string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{
			"id":          msgID,
			"model":       model,
			"stop_reason": "tool_use",
			"content": []any{
				map[string]any{"type": "text", "text": "ok"},
				map[string]any{"type": "tool_use", "name": toolName, "input": map[string]any{}},
			},
			"usage": map[string]any{
				"input_tokens": float64(1), "output_tokens": float64(10),
				"cache_read_input_tokens": float64(0), "cache_creation_input_tokens": float64(0),
			},
		},
	}
}

// A long-running tool (Bash/build/test) pending for a while is EXECUTING → running,
// NOT "waiting for permission" — the old blunt 3s heuristic mislabeled it (the bug
// where window 3's running agent showed as completed).
func TestClaudeDriver_PendingToolUseStaysRunning(t *testing.T) {
	path := writeJSONL(t, []map[string]any{
		makeAssistantToolUseRow("msg-bash", "claude-3-5-sonnet", "Bash"),
	})
	d := NewClaudeDriver(path, "sess-bash")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	d.state.UpdatedAt = time.Now().Add(-30 * time.Second) // long-running tool

	s := d.State()
	if s.Status != StatusRunning {
		t.Fatalf("status: got %q, want running (tool executing)", s.Status)
	}
	if s.WaitReason != WaitNone {
		t.Fatalf("wait reason: got %q, want none", s.WaitReason)
	}
}

// AskUserQuestion is the agent ASKING the user → waiting (question), immediately and
// regardless of elapsed time (the bug where window 1's ask card showed as something
// other than waiting).
func TestClaudeDriver_AskUserQuestionWaitsForAnswer(t *testing.T) {
	path := writeJSONL(t, []map[string]any{
		makeAssistantToolUseRow("msg-ask", "claude-3-5-sonnet", "AskUserQuestion"),
	})
	d := NewClaudeDriver(path, "sess-ask")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	s := d.State()
	if s.Status != StatusWaiting {
		t.Fatalf("status: got %q, want waiting", s.Status)
	}
	if s.WaitReason != WaitQuestion {
		t.Fatalf("wait reason: got %q, want question", s.WaitReason)
	}
}

// After the question, the user's answer arrives (a user line) → pending tool cleared,
// agent processing → running.
func TestClaudeDriver_AnswerClearsPendingWait(t *testing.T) {
	path := writeJSONL(t, []map[string]any{
		makeAssistantToolUseRow("msg-ask", "claude-3-5-sonnet", "AskUserQuestion"),
		makeUserRow(),
	})
	d := NewClaudeDriver(path, "sess-ans")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	s := d.State()
	if s.Status != StatusRunning {
		t.Fatalf("status: got %q, want running (answer received)", s.Status)
	}
	if s.PendingTool != "" {
		t.Fatalf("pending tool: got %q, want cleared", s.PendingTool)
	}
}

func TestClaudeDriver_InterruptedToolUseResultWaitsForPermission(t *testing.T) {
	path := writeJSONL(t, []map[string]any{
		makeAssistantRow("msg-tool", "claude-3-5-sonnet", "tool_use", 1, 20, 0),
		{
			"type":      "user",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"message": map[string]any{
				"content": []any{
					map[string]any{
						"toolUseResult": map[string]any{
							"interrupted": true,
						},
					},
				},
			},
		},
	})

	d := NewClaudeDriver(path, "sess-interrupted")
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	s := d.State()
	if s.Status != StatusWaiting {
		t.Fatalf("status: got %q, want waiting", s.Status)
	}
	if s.WaitReason != WaitPermission {
		t.Fatalf("wait reason: got %q, want permission", s.WaitReason)
	}
}

func TestClaudeDriver_IncrementalRead(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "*.jsonl")
	path := f.Name()

	enc := json.NewEncoder(f)
	_ = enc.Encode(makeAssistantRow("msg-3", "claude-haiku", "end_turn", 0, 50, 1000))
	f.Sync()

	d := NewClaudeDriver(path, "sess-inc")
	if err := d.Update(); err != nil {
		t.Fatalf("first Update error: %v", err)
	}
	if d.State().Usage.OutputTokens != 50 {
		t.Fatalf("first read wrong: %+v", d.State().Usage)
	}

	// Append second turn.
	_ = enc.Encode(makeAssistantRow("msg-4", "claude-haiku", "end_turn", 0, 120, 2000))
	f.Sync()
	f.Close()

	if err := d.Update(); err != nil {
		t.Fatalf("second Update error: %v", err)
	}
	if d.State().Usage.OutputTokens != 170 {
		t.Fatalf("incremental read wrong: output=%d, want 170", d.State().Usage.OutputTokens)
	}
}

// PaneAgentMonitor.Status is the accurate signal the tmux/notifier path now uses.
// It must distinguish the reported cases: an AskUserQuestion = waiting (window 1),
// a plain pending tool = running (window 3), end_turn = idle.
func TestPaneAgentMonitorStatus(t *testing.T) {
	ask := writeJSONL(t, []map[string]any{makeAssistantToolUseRow("m1", "claude-3-5-sonnet", "AskUserQuestion")})
	bash := writeJSONL(t, []map[string]any{makeAssistantToolUseRow("m2", "claude-3-5-sonnet", "Bash")})
	done := writeJSONL(t, []map[string]any{makeAssistantRow("m3", "claude-3-5-sonnet", "end_turn", 1, 50, 0)})

	m := NewPaneAgentMonitor(nil)
	check := func(name, path string, want AgentStatus) {
		t.Helper()
		m.cache[name] = &paneTranscript{path: path, locatedAt: time.Now()} // inject path, skip dir resolution
		st, ok := m.Status(name, "", ToolClaude)
		if !ok || st != want {
			t.Fatalf("%s: got (%v, ok=%v), want %v", name, st, ok, want)
		}
	}
	check("ask", ask, StatusWaiting)
	check("bash", bash, StatusRunning)
	check("done", done, StatusIdle)
}

func TestClaudeDriver_AgentState(t *testing.T) {
	rows := []map[string]any{
		makeAssistantRow("msg-5", "claude-3-5-sonnet", "end_turn", 1, 100, 3000),
	}
	path := writeJSONL(t, rows)

	d := NewClaudeDriver(path, "sess-as")
	_ = d.Update()

	as := d.AgentState()
	if as.Tool != ToolClaude {
		t.Errorf("tool: got %q", as.Tool)
	}
	if as.OutputTokens != 100 {
		t.Errorf("output tokens: got %d", as.OutputTokens)
	}
	if as.CacheReadTokens != 3000 {
		t.Errorf("cache read tokens: got %d", as.CacheReadTokens)
	}
}
