package terminal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// The defect these cover, in the user's words: "为什么这个终端在跑 /status，卡片却显示红色 waiting".
//
// The old host-injected detector resolved a session's transcript as "newest Claude file in this
// cwd". Two terminals open on the same repo — the normal case — therefore both read the SAME file
// and each reported the other's state. tmux panes were fixed for this months ago via the binding
// cache; this path had its own copy of the logic and never got the fix. Reusing PaneAgentMonitor
// here is what makes the two paths one path.

// writeClaudeTranscript writes a minimal Claude JSONL whose last assistant turn is `text`,
// timestamped `at`, and returns its path.
func writeClaudeTranscript(t *testing.T, dir, name, text string, at time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	row := map[string]any{
		"type":      "assistant",
		"timestamp": at.UTC().Format(time.RFC3339Nano),
		"message": map[string]any{
			"id": name, "model": "claude-test", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Give the two files distinct mtimes so "newest" is well-defined for the locator.
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

// Two sessions sharing a cwd must never share a transcript: whichever one binds a file first keeps
// it, and the second gets the other candidate — not a copy of its sibling's state.
func TestSessionAgentTracker_SameCwdSessionsGetDistinctTranscripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/tmp/dw-test-repo"
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-dw-test-repo")
	base := time.Now().Add(-time.Hour)
	newest := writeClaudeTranscript(t, projDir, "sess-newest", "要我接着做吗？", base.Add(time.Minute))
	older := writeClaudeTranscript(t, projDir, "sess-older", "已完成。", base)

	m := agentintel.NewPaneAgentMonitor(nil)
	// Two distinct session keys, same cwd, same tool, no PID anchor (Claude exposes none).
	if _, ok := m.Status("session-A", cwd, agentintel.ToolClaude); !ok {
		t.Skip("locator found no transcript in this environment")
	}
	if _, ok := m.Status("session-B", cwd, agentintel.ToolClaude); !ok {
		t.Fatal("second session could not bind a transcript at all")
	}
	a, b := m.Snapshot("session-A"), m.Snapshot("session-B")
	if a.AwaitingSince.Equal(b.AwaitingSince) {
		t.Fatalf("both sessions bound the SAME transcript (awaitingSince %v) — this is the 张冠李戴 bug: "+
			"one terminal would render the other's status", a.AwaitingSince)
	}
	_ = newest
	_ = older
}

// A session with no agent process reports nothing at all — no tool, no status, hence no dot.
// The old path could still produce a status here, because it went straight to the newest file in
// the cwd without first proving an agent was actually running in THIS session.
func TestSessionAgentTracker_NoAgentNoStatus(t *testing.T) {
	tr := newSessionAgentTracker()
	got := tr.State(context.Background(), "s1", os.Getpid(), t.TempDir(), nil)
	if got.Tool != agentintel.ToolNone || got.Status != "" {
		t.Fatalf("a shell with no agent reported tool=%q status=%q, want empty", got.Tool, got.Status)
	}
	if got.AwaitingUser {
		t.Fatal("a shell with no agent must not be marked needs-you")
	}
}

// A dead/absent shell PID must be inert rather than falling through to some cwd-wide guess.
func TestSessionAgentTracker_NoShellPIDIsInert(t *testing.T) {
	tr := newSessionAgentTracker()
	if got := tr.State(context.Background(), "s1", 0, "/tmp", nil); got.Tool != agentintel.ToolNone {
		t.Fatalf("shellPID 0 → tool %q, want none", got.Tool)
	}
}

// The overview card and the REST session list must be ONE computation. They used to be two
// (the list called the hook per request, the cards called it per tick), which is how a tab dot and
// its card could disagree.
func TestSessionAgentStatuses_ComesFromTheSameSnapshotAsTheCards(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()
	snap := srv.overviewSnapshot(ctx)
	statuses := srv.sessionAgentStatuses(ctx)

	for _, e := range snap.entries {
		got, ok := statuses[e.ID]
		if e.AgentTool == "" {
			if ok {
				t.Fatalf("session %s has no agent in the card but %v in the list", e.ID, got)
			}
			continue
		}
		if !ok || got[0] != e.AgentTool || got[1] != e.AgentStatus {
			t.Fatalf("list says %v, card says (%q,%q) — they must be the same snapshot",
				got, e.AgentTool, e.AgentStatus)
		}
	}
}

// A closed session must release its transcript binding, or it keeps excluding a live session from
// claiming that file and the live one silently degrades to "no status".
func TestSessionsOverview_PrunesBindingsForClosedSessions(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()
	srv.sessionsOverview(ctx)
	if err := sm.Destroy(sess.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	srv.sessionsOverview(ctx)
	if got := srv.sessionAgent.monitor.Snapshot(sess.ID); got.Tool != agentintel.ToolNone {
		t.Fatalf("binding for a destroyed session survived: %+v", got)
	}
}
