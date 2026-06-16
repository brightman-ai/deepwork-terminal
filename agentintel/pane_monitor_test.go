package agentintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudeTranscript(t *testing.T, cwd string, modAgo time.Duration) func() {
	t.Helper()
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "test-session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"assistant"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mod := time.Now().Add(-modAgo)
	_ = os.Chtimes(path, mod, mod)
	return func() { os.RemoveAll(dir) }
}

// TestPaneMonitorActive verifies the freshness gate that keeps the terminal scrape off the hot path:
// a transcript written just now reads as active (skip the terminal); a stale one does not (fall back
// to reading the pane); an unlocatable one defaults to active (never a false prompt).
func TestPaneMonitorActive(t *testing.T) {
	m := NewPaneAgentMonitor(nil)

	t.Run("fresh transcript → active", func(t *testing.T) {
		cwd := "/tmp/dw-panemon-fresh"
		defer writeClaudeTranscript(t, cwd, 0)()
		if !m.Active("p-fresh", cwd, ToolClaude) {
			t.Fatal("just-written transcript should read as active")
		}
	})

	t.Run("stale transcript → not active", func(t *testing.T) {
		cwd := "/tmp/dw-panemon-stale"
		defer writeClaudeTranscript(t, cwd, 30*time.Second)()
		if m.Active("p-stale", cwd, ToolClaude) {
			t.Fatal("transcript untouched for 30s should read as NOT active (→ read the terminal)")
		}
	})

	t.Run("unlocatable transcript → active (no false prompt)", func(t *testing.T) {
		if !m.Active("p-none", "/tmp/dw-panemon-nonexistent-xyz", ToolClaude) {
			t.Fatal("missing transcript should default to active, not trigger a terminal read")
		}
	})

	t.Run("non-agent pane → active (untracked)", func(t *testing.T) {
		if !m.Active("p-x", "/tmp", ToolNone) {
			t.Fatal("ToolNone should be a no-op (active)")
		}
	})
}
