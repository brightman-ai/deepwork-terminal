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
// the two: an agent still working on an old prompt IS running, and cutting it to idle would fire
// a false "done". So the clock alone never decides — the conversation must ALSO have gone quiet,
// and a working agent keeps producing activity (here: the request being retried).
//
// This used to be modelled as "the file's mtime is fresh". That proxy is exactly what Claude
// Code's bookkeeping rows defeated in production (see the next test); the intent it stood for —
// something is still moving the conversation — is what is pinned here.
func TestClaudeDriver_StaleClockAloneDoesNotDowngrade(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-2*time.Hour), nil),
		rowAt("user", now.Add(-time.Hour), nil),
		rowAt("system", now.Add(-time.Minute), map[string]any{"subtype": "api_error"}),
	}, 0)

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — the request is still being worked a minute ago", got)
	}
}

// TestClaudeDriver_BookkeepingRowsDoNotKeepStalePendingRunning is the production regression
// behind the mtime proxy's removal: Claude Code appends untimestamped bookkeeping rows
// (last-prompt, mode, permission-mode, atis-latch, titles) at moments unrelated to any turn, and
// a slash command the user runs echoes system/local_command rows. Both refreshed the file mtime,
// so an abandoned pending reply read "just written" and flipped back to running every time one
// landed. Neither is the agent working.
func TestClaudeDriver_BookkeepingRowsDoNotKeepStalePendingRunning(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-2*time.Hour), nil),
		rowAt("user", now.Add(-time.Hour), nil),
		{"type": "last-prompt"},
		{"type": "mode"},
		{"type": "permission-mode"},
		{"type": "atis-latch"},
		{"type": "custom-title"},
		rowAt("system", now.Add(-time.Minute), map[string]any{"subtype": "local_command"}),
	}, 0) // file just written — by bookkeeping

	if got := cd.AgentState().Status; got != StatusIdle {
		t.Fatalf("status = %s, want idle — bookkeeping and a local slash-command echo are not an agent at work", got)
	}
}

// TestClaudeDriver_RenameNoticeDoesNotPinRunning: /rename injects an isMeta user row ("The user
// named this session …") after a finished turn. Observed on two live panes that each showed
// Running for a day while sitting at an empty prompt. It is context, not a prompt — nothing ever
// answers it — and the needs-you fact from the end_turn before it must survive.
func TestClaudeDriver_RenameNoticeDoesNotPinRunning(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("user", now.Add(-2*time.Hour), nil),
		rowAt("assistant", now.Add(-2*time.Hour+time.Minute), map[string]any{"message": map[string]any{
			"role": "assistant", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "done"}},
		}}),
		rowAt("system", now.Add(-2*time.Second), map[string]any{"subtype": "local_command"}),
		rowAt("user", now.Add(-time.Second), map[string]any{
			"isMeta": true,
			"message": map[string]any{"role": "user",
				"content": "<system-reminder>\nThe user named this session \"ueba基线-1012\".\n</system-reminder>"},
		}),
	}, time.Second)

	as := cd.AgentState()
	if as.Status != StatusIdle {
		t.Fatalf("status = %s, want idle — a /rename notice is not a prompt awaiting a reply", as.Status)
	}
	if !as.AwaitingUser {
		t.Fatal("the last real turn ended on end_turn — that needs-you fact must survive a /rename")
	}
}

// TestClaudeDriver_RealPromptAfterRenameRunsAgain: the isMeta skip must not swallow the next
// real turn.
func TestClaudeDriver_RealPromptAfterRenameRunsAgain(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-time.Hour), map[string]any{"message": map[string]any{
			"role": "assistant", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "done"}},
		}}),
		rowAt("user", now.Add(-30*time.Minute), map[string]any{"isMeta": true}),
		rowAt("user", now.Add(-5*time.Second), nil),
	}, 5*time.Second)

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — a real prompt after a /rename is a real turn", got)
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

// TestClaudeDriver_CompactSummaryDoesNotPinRunning: compaction while the agent is genuinely idle
// writes a synthetic user row carrying the continuation summary (isCompactSummary=true). By row
// order alone that row is newer than the last assistant turn, which used to read as "your prompt
// landed, the reply is coming" — pinning the pane Running even though the on-screen text right
// after it literally says the session was compacted WHILE IDLE. No assistant row ever answers
// this row, so unlike an ordinary prompt it cannot self-correct by someone eventually replying.
func TestClaudeDriver_CompactSummaryDoesNotPinRunning(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("user", now.Add(-2*time.Hour), nil),
		rowAt("assistant", now.Add(-2*time.Hour+time.Minute), map[string]any{"message": map[string]any{
			"role": "assistant", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "done"}},
		}}),
		rowAt("user", now.Add(-time.Second), map[string]any{"isCompactSummary": true}),
	}, time.Second)

	as := cd.AgentState()
	if as.Status != StatusIdle {
		t.Fatalf("status = %s, want idle — a compact summary is not a prompt awaiting a reply", as.Status)
	}
	if !as.AwaitingUser {
		t.Fatal("the last real turn already ended on end_turn — that needs-you fact must survive a compaction boundary")
	}
}

// TestClaudeDriver_RealPromptAfterCompactSummaryRunsAgain: the skip must not be sticky — a real
// prompt typed after the compaction boundary is an ordinary running turn.
func TestClaudeDriver_RealPromptAfterCompactSummaryRunsAgain(t *testing.T) {
	now := time.Now()
	cd := driverFor(t, []map[string]any{
		rowAt("assistant", now.Add(-time.Hour), map[string]any{"message": map[string]any{
			"role": "assistant", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "done"}},
		}}),
		rowAt("user", now.Add(-30*time.Minute), map[string]any{"isCompactSummary": true}),
		rowAt("user", now.Add(-5*time.Second), nil),
	}, 5*time.Second)

	if got := cd.AgentState().Status; got != StatusRunning {
		t.Fatalf("status = %s, want running — a real prompt after the compaction boundary is a real turn", got)
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

// TestCodexDriver_NoCompletionKeepsTranscriptRunning: a missing task_complete is
// ambiguous in JSONL. It can mean a long tool call or an interrupted UI turn;
// screen evidence resolves the latter, so the driver must not guess from age.
func TestCodexDriver_NoCompletionKeepsTranscriptRunning(t *testing.T) {
	now := time.Now()
	cd := codexDriverFor(t, []map[string]any{
		codexRow("task_started", now.Add(-2*time.Hour)),
	}, time.Hour) // nothing appended for an hour either

	as := cd.AgentState()
	if as.Status != StatusRunning {
		t.Fatalf("status = %s, want running until explicit screen/process evidence", as.Status)
	}
	if as.AwaitingUser {
		t.Fatal("a turn without completion has no result waiting to be read")
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
