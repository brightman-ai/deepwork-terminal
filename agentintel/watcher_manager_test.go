package agentintel

import (
	"context"
	"testing"
	"time"
)

func TestToolFromEngine(t *testing.T) {
	cases := map[string]AgentTool{
		"claude":      ToolClaude,
		"Claude Code": ToolClaude,
		"  CODEX  ":   ToolCodex,
		"codex-cli":   ToolCodex,
		"bash":        ToolNone,
		"":            ToolNone,
		"gemini":      ToolNone,
	}
	for engine, want := range cases {
		if got := toolFromEngine(engine); got != want {
			t.Errorf("toolFromEngine(%q) = %q, want %q", engine, got, want)
		}
	}
}

func TestMonitorManagerNotConfigured(t *testing.T) {
	m := NewAgentIntelMonitorManager(nil, nil)
	if _, _, err := m.Subscribe(context.Background(), "s1"); err == nil {
		t.Fatal("expected error when neither getter nor resolver configured")
	}
	if _, _, err := m.Subscribe(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty session id")
	}
}

func TestMonitorManagerSessionNotFound(t *testing.T) {
	getter := func(context.Context, string) (SessionActivity, bool) { return nil, false }
	m := NewAgentIntelMonitorManager(getter, nil)
	if _, _, err := m.Subscribe(context.Background(), "missing"); err == nil {
		t.Fatal("expected not-found error from getter returning false")
	}
}

// resolver mode shares one watcher across subscribers and tears it down only
// when the last subscriber releases.
func TestMonitorManagerResolverRefCount(t *testing.T) {
	resolver := func(context.Context, string) (AgentIntelResponse, error) {
		return AgentIntelResponse{}, nil
	}
	m := NewAgentIntelMonitorManagerWithResolver(nil, nil, resolver)

	_, rel1, err := m.Subscribe(context.Background(), "s1")
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	_, rel2, err := m.Subscribe(context.Background(), "s1")
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}

	m.mu.Lock()
	entry, ok := m.watchers["s1"]
	refs := 0
	if ok {
		refs = entry.refs
	}
	m.mu.Unlock()
	if !ok || refs != 2 {
		t.Fatalf("expected one shared watcher with refs=2, ok=%v refs=%d", ok, refs)
	}

	rel1()
	m.mu.Lock()
	_, stillThere := m.watchers["s1"]
	m.mu.Unlock()
	if !stillThere {
		t.Fatal("watcher removed while a subscriber remained")
	}

	rel2()
	m.mu.Lock()
	_, gone := m.watchers["s1"]
	m.mu.Unlock()
	if gone {
		t.Fatal("watcher not torn down after last subscriber left")
	}
}

type fakeActivity struct {
	cwd, engine string
	last        time.Time
	tail        []string
}

func (f fakeActivity) WorkingDir() string       { return f.cwd }
func (f fakeActivity) Engine() string           { return f.engine }
func (f fakeActivity) LastActivity() time.Time  { return f.last }
func (f fakeActivity) TailLines(n int) []string { return f.tail }

// session mode locates the activity via the getter and starts a JSONL watcher.
func TestMonitorManagerSessionMode(t *testing.T) {
	getter := func(context.Context, string) (SessionActivity, bool) {
		return fakeActivity{cwd: t.TempDir(), engine: "claude"}, true
	}
	m := NewAgentIntelMonitorManager(getter, nil)

	ch, release, err := m.Subscribe(context.Background(), "s1")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer release()
	if ch == nil {
		t.Fatal("expected non-nil channel in session mode")
	}

	m.mu.Lock()
	_, ok := m.watchers["s1"]
	m.mu.Unlock()
	if !ok {
		t.Fatal("expected a watcher registered for session mode")
	}
}
