package agentintel

import (
	"os"
	"testing"
	"time"
)

// rowAt builds a transcript row of the given type at a fixed time.
func rowAt(rowType string, at time.Time, extra map[string]any) map[string]any {
	row := map[string]any{
		"type":      rowType,
		"timestamp": at.UTC().Format(time.RFC3339Nano),
		"message":   map[string]any{"role": rowType, "content": []any{map[string]any{"type": "text", "text": "x"}}},
	}
	for k, v := range extra {
		row[k] = v
	}
	return row
}

func driverFor(t *testing.T, rows []map[string]any, fileAge time.Duration) *ClaudeDriver {
	t.Helper()
	path := writeJSONL(t, rows)
	mod := time.Now().Add(-fileAge)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	cd := NewClaudeDriver(path, "test")
	if err := cd.Update(); err != nil {
		t.Fatal(err)
	}
	return cd
}

// TestClaudeDriver_InterruptIsNotRunning: pressing Esc writes a user row like any other, so by
// row order alone ("your message is newer than the agent's") it read as RUNNING — the exact
// opposite of what happened. On a live machine that pinned a pane green for ten hours.
//
// It is also not AWAITING: you stopped the agent yourself, and a needs-you badge for your own
// keystroke is noise that would page you about something you just did.
func TestClaudeDriver_InterruptIsNotRunning(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("user", now.Add(-2*time.Minute), nil),
		rowAt("assistant", now.Add(-90*time.Second), nil),
		rowAt("user", now.Add(-60*time.Second), map[string]any{"interruptedMessageId": "msg_01abc"}),
	}, 60*time.Second)

	as := cd.AgentState()
	if as.Status != StatusIdle {
		t.Fatalf("status = %s, want idle — an interrupt is the user STOPPING the agent", as.Status)
	}
	if as.AwaitingUser {
		t.Fatal("an interrupt you performed must not raise a needs-you flag")
	}
}

// TestClaudeDriver_PromptAfterInterruptRunsAgain: the interrupt must not be sticky. Typing a new
// prompt right after pressing Esc is the ordinary way to redirect an agent, and that turn is
// genuinely running.
func TestClaudeDriver_PromptAfterInterruptRunsAgain(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-90*time.Second), nil),
		rowAt("user", now.Add(-60*time.Second), map[string]any{"interruptedMessageId": "msg_01abc"}),
		rowAt("user", now.Add(-30*time.Second), nil),
	}, 30*time.Second)

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — a fresh prompt after an interrupt is a real turn", got)
	}
}

// TestClaudeDriver_StalePendingReplyGoesIdle: a prompt that never got an answer — a killed CLI, a
// crashed session, a machine that slept mid-turn — used to hold the pane green forever, because
// "your row is newer than the agent's" has no corroborating signal and no clock.
func TestClaudeDriver_StalePendingReplyGoesIdle(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-2*time.Hour), nil),
		rowAt("user", now.Add(-time.Hour), nil),
	}, time.Hour) // nothing appended for an hour either

	if got := cd.AgentState().Status; got != StatusIdle {
		t.Fatalf("status = %s, want idle — an hour with nothing written is not an agent at work", got)
	}
}

// TestClaudeDriver_StaleClockAloneDoesNotDowngrade is the safety half, and the more important of
// the two: an agent that thinks for a long time before its first token IS running, and cutting it
// to idle would fire a false "done". So the clock alone never decides — the transcript must ALSO
// have gone quiet, and a working agent writes continuously.
func TestClaudeDriver_StaleClockAloneDoesNotDowngrade(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-2*time.Hour), nil),
		rowAt("user", now.Add(-time.Hour), nil),
	}, 0) // …but the file was just written

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — the file is still being written, so something IS working", got)
	}
}

// TestClaudeDriver_FreshPendingReplyStaysRunning pins the ordinary case the window must not
// disturb: you just pressed enter, the first token has not arrived.
func TestClaudeDriver_FreshPendingReplyStaysRunning(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-2*time.Minute), nil),
		rowAt("user", now.Add(-3*time.Second), nil),
	}, 3*time.Second)

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — a prompt seconds old is exactly what running looks like", got)
	}
}

// codexRow builds a rollout row at a fixed time.
func codexRow(evType string, at time.Time) map[string]any {
	return map[string]any{
		"type":      "event_msg",
		"timestamp": at.UTC().Format(time.RFC3339Nano),
		"payload":   map[string]any{"type": evType},
	}
}

func codexDriverFor(t *testing.T, rows []map[string]any, fileAge time.Duration) *CodexDriver {
	t.Helper()
	path := writeJSONL(t, rows)
	mod := time.Now().Add(-fileAge)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	cd := NewCodexDriver(path)
	if err := cd.Update(); err != nil {
		t.Fatal(err)
	}
	return cd
}

// TestCodexDriver_StaleRunGoesIdle: Codex asserts Running on task_started and retracts it only
// when task_complete is WRITTEN. Anything that stops the CLI first — Esc, a crash, a killed
// pane, a closed laptop — leaves "running" as the last word, with nobody left to correct it.
// Same defect as the claude driver's, reached through a different rule, so it gets the same
// bound rather than a second theory of what "stale" means.
func TestCodexDriver_StaleRunGoesIdle(t *testing.T) {
	now := time.Now()
	cd := codexDriverFor(t, []map[string]any{
		codexRow("task_started", now.Add(-2*time.Hour)),
	}, time.Hour) // nothing appended for an hour either

	as := cd.AgentState()
	if as.Status != StatusIdle {
		t.Fatalf("status = %s, want idle — a turn with no completion and no writes is not in progress", as.Status)
	}
	if as.AwaitingUser {
		t.Fatal("a turn that died without finishing has no result waiting to be read")
	}
}

// TestCodexDriver_LiveRunStaysRunning is the safety half: a long tool call is still a turn in
// progress, and the file keeps growing while it runs. The clock alone must never decide.
func TestCodexDriver_LiveRunStaysRunning(t *testing.T) {
	now := time.Now()
	cd := codexDriverFor(t, []map[string]any{
		codexRow("task_started", now.Add(-2*time.Hour)),
	}, 0) // …but the rollout was just written

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — the rollout is still being written", got)
	}
}

// TestCodexDriver_CompletedTurnUnaffected pins the ordinary path: a turn that DID write its
// completion stays idle+awaiting no matter how old it is. The bound must not eat the needs-you
// signal of a session you simply have not come back to yet.
func TestCodexDriver_CompletedTurnUnaffected(t *testing.T) {
	now := time.Now()
	cd := codexDriverFor(t, []map[string]any{
		codexRow("task_started", now.Add(-3*time.Hour)),
		codexRow("task_complete", now.Add(-2*time.Hour)),
	}, time.Hour)

	as := cd.AgentState()
	if as.Status != StatusIdle {
		t.Fatalf("status = %s, want idle", as.Status)
	}
	if !as.AwaitingUser {
		t.Fatal("a completed turn is still your move — the staleness bound must not clear it")
	}
}
