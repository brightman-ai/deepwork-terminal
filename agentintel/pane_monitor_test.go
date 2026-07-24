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

// writeClaudeSessions writes N named session files in one cwd's project dir, names[0] newest.
func writeClaudeSessions(t *testing.T, cwd string, names ...string) func() {
	t.Helper()
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Now()
	for i, name := range names {
		path := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"assistant"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mod := base.Add(-time.Duration(i) * time.Second) // names[0] newest
		_ = os.Chtimes(path, mod, mod)
	}
	return func() { os.RemoveAll(dir) }
}

// TestPaneMonitorMutualExclusion is the regression guard for the false "跑完了" done-unseen: two
// Claude panes sharing ONE cwd must resolve to DIFFERENT session files. Claude exposes no per-process
// session identity (no held fd / env / lock), so cwd is the only locator — the binding cache is what
// keeps a still-running pane from snapping to a sibling's just-completed transcript.
func TestPaneMonitorMutualExclusion(t *testing.T) {
	cwd := "/tmp/dw-panemon-mutex"
	defer writeClaudeSessions(t, cwd, "sess-newest", "sess-older")()
	m := NewPaneAgentMonitor(nil)

	m.mu.Lock()
	p1 := m.entryLocked("p1", cwd, ToolClaude, 1).path
	p2 := m.entryLocked("p2", cwd, ToolClaude, 2).path
	m.mu.Unlock()

	if p1 == "" || p2 == "" {
		t.Fatalf("both panes should locate a file: p1=%q p2=%q", p1, p2)
	}
	if p1 == p2 {
		t.Fatalf("two same-cwd panes must NOT share a session file; both got %q", p1)
	}
	if !strings.HasSuffix(p1, "sess-newest.jsonl") {
		t.Errorf("first pane should take the newest, got %q", p1)
	}
	if !strings.HasSuffix(p2, "sess-older.jsonl") {
		t.Errorf("second pane should take the next-newest (sibling excluded), got %q", p2)
	}
}

// TestPaneMonitorSinglePaneNewest: a lone pane still takes the newest (exclusion is a no-op when
// nothing else is bound) — the fix must not regress the single-agent common case.
func TestPaneMonitorSinglePaneNewest(t *testing.T) {
	cwd := "/tmp/dw-panemon-single"
	defer writeClaudeSessions(t, cwd, "sess-newest", "sess-older")()
	m := NewPaneAgentMonitor(nil)
	m.mu.Lock()
	p := m.entryLocked("solo", cwd, ToolClaude, 1).path
	m.mu.Unlock()
	if !strings.HasSuffix(p, "sess-newest.jsonl") {
		t.Errorf("lone pane should take the newest, got %q", p)
	}
}

// TestPaneMonitorSticky: once bound to an existing file, a pane must NOT relocate to a newer
// same-cwd session that appears later — that theft is what re-fired a long-settled pane's stale
// done-unseen. Guards the sticky rule (relocate only on PID change / file gone).
func TestPaneMonitorSticky(t *testing.T) {
	cwd := "/tmp/dw-panemon-sticky"
	cleanup := writeClaudeSessions(t, cwd, "sess-mine")
	defer cleanup()
	m := NewPaneAgentMonitor(nil)

	m.mu.Lock()
	first := m.entryLocked("p1", cwd, ToolClaude, 1).path
	// Simulate the relocate window having elapsed.
	m.cache["p1"].locatedAt = time.Now().Add(-pathRelocateAfter - time.Second)
	m.mu.Unlock()

	// A NEWER sibling session appears in the same cwd (does not remove sess-mine).
	defer writeClaudeSessions(t, cwd, "sess-newer")()

	m.mu.Lock()
	after := m.entryLocked("p1", cwd, ToolClaude, 1).path
	m.mu.Unlock()

	if after != first {
		t.Errorf("sticky: pane must keep its bound file when a newer sibling appears; was %q, now %q", first, after)
	}
	if !strings.HasSuffix(after, "sess-mine.jsonl") {
		t.Errorf("expected to stay on sess-mine, got %q", after)
	}
}
