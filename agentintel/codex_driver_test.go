package agentintel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeCodexRow(rowType string, payload map[string]any) map[string]any {
	return map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"type":      rowType,
		"payload":   payload,
	}
}

func TestCodexDriver_SessionMeta(t *testing.T) {
	rows := []map[string]any{
		makeCodexRow("session_meta", map[string]any{
			"cwd":        "/home/user/project",
			"model":      "o4-mini",
			"session_id": "codex-sess-1",
		}),
	}
	path := writeJSONL(t, rows)

	d := NewCodexDriver(path)
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	s := d.State()
	if s.CWD != "/home/user/project" {
		t.Errorf("CWD: got %q", s.CWD)
	}
	if s.Model != "o4-mini" {
		t.Errorf("Model: got %q", s.Model)
	}
	if s.SessionID != "codex-sess-1" {
		t.Errorf("SessionID: got %q", s.SessionID)
	}
}

func TestCodexDriver_TokenCount(t *testing.T) {
	rows := []map[string]any{
		makeCodexRow("session_meta", map[string]any{
			"model": "o4-mini",
		}),
		makeCodexRow("event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input":  float64(1500),
					"output": float64(300),
					"cached": float64(1000),
					"total":  float64(2800),
				},
			},
		}),
	}
	path := writeJSONL(t, rows)

	d := NewCodexDriver(path)
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	s := d.State()
	if s.InputTokens != 1500 {
		t.Errorf("InputTokens: got %d, want 1500", s.InputTokens)
	}
	if s.OutputTokens != 300 {
		t.Errorf("OutputTokens: got %d, want 300", s.OutputTokens)
	}
	if s.CachedTokens != 1000 {
		t.Errorf("CachedTokens: got %d, want 1000", s.CachedTokens)
	}
	if s.TotalTokens != 2800 {
		t.Errorf("TotalTokens: got %d, want 2800", s.TotalTokens)
	}
}

func TestCodexDriver_TokenCountLatestWins(t *testing.T) {
	// Codex is already cumulative — later row should replace earlier.
	rows := []map[string]any{
		makeCodexRow("event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input": float64(100), "output": float64(50), "cached": float64(0), "total": float64(150),
				},
			},
		}),
		makeCodexRow("event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input": float64(500), "output": float64(200), "cached": float64(300), "total": float64(1000),
				},
			},
		}),
	}
	path := writeJSONL(t, rows)

	d := NewCodexDriver(path)
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	s := d.State()
	if s.TotalTokens != 1000 {
		t.Errorf("TotalTokens: got %d, want 1000 (latest wins)", s.TotalTokens)
	}
}

func TestCodexDriver_AgentState(t *testing.T) {
	rows := []map[string]any{
		makeCodexRow("session_meta", map[string]any{"model": "o3"}),
		makeCodexRow("event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input": float64(200), "output": float64(80), "cached": float64(0), "total": float64(280),
				},
			},
		}),
	}
	path := writeJSONL(t, rows)

	d := NewCodexDriver(path)
	_ = d.Update()

	as := d.AgentState()
	if as.Tool != ToolCodex {
		t.Errorf("tool: got %q", as.Tool)
	}
	if as.Model != "o3" {
		t.Errorf("model: got %q", as.Model)
	}
	if as.InputTokens != 200 {
		t.Errorf("input tokens: got %d", as.InputTokens)
	}
}

// TestCodexDriver_TurnLifecycle asserts the driver derives Running during a turn
// and Waiting once the turn completes. The Waiting result is the running→waiting
// transition push_notifier fires the turn-end notification on — before this the
// driver only ever emitted Running, so Codex sessions never notified.
func TestCodexDriver_TurnLifecycle(t *testing.T) {
	// Mid-turn: task_started + a response_item → Running.
	midTurn := writeJSONL(t, []map[string]any{
		makeCodexRow("session_meta", map[string]any{"model": "gpt-5.5"}),
		makeCodexRow("event_msg", map[string]any{"type": "task_started"}),
		makeCodexRow("response_item", map[string]any{"type": "message"}),
	})
	d := NewCodexDriver(midTurn)
	if err := d.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if got := d.State().Status; got != StatusRunning {
		t.Errorf("mid-turn status: got %q, want %q", got, StatusRunning)
	}

	// Turn complete: the full sequence ending in task_complete → Waiting.
	done := writeJSONL(t, []map[string]any{
		makeCodexRow("session_meta", map[string]any{"model": "gpt-5.5"}),
		makeCodexRow("event_msg", map[string]any{"type": "task_started"}),
		makeCodexRow("response_item", map[string]any{"type": "message"}),
		makeCodexRow("event_msg", map[string]any{"type": "task_complete"}),
	})
	d2 := NewCodexDriver(done)
	if err := d2.Update(); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if got := d2.State().Status; got != StatusWaiting {
		t.Errorf("post-turn status: got %q, want %q (turn-end → notify trigger)", got, StatusWaiting)
	}
}

// TestCodexLatestSession_RecursiveWalk guards the fix for the notification bug:
// Codex nests rollouts under sessions/YYYY/MM/DD/, so a flat ReadDir of the base
// dir found zero .jsonl files → the pane's transcript was unlocatable → the pane
// read as perpetually Running → never notified. The locator must walk recursively.
func TestCodexLatestSession_RecursiveWalk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	nested := filepath.Join(home, ".codex", "sessions", "2026", "07", "02")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rollout := filepath.Join(nested, "rollout-2026-07-02T08-23-50-abc.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	pl := NewProjectLocator()
	got, err := pl.CodexLatestSession("/any/cwd")
	if err != nil {
		t.Fatalf("CodexLatestSession error: %v (flat ReadDir regressed?)", err)
	}
	if got != rollout {
		t.Errorf("got %q, want nested rollout %q", got, rollout)
	}
}
