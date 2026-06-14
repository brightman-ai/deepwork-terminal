package agentintel

import (
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
