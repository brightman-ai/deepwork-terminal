package agentintel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// claudeHomeFixture redirects the whole claude config root into a temp dir, so a test can
// write a PID→session record without ever touching the real ~/.claude — where a stray
// sessions/<pid>.json would be read by the live server and mis-bind a real pane. Both roots
// have to move together: ClaudeProjectsRoot consults DW_CLAUDE_PROJECTS FIRST, so leaving it
// set would put the transcripts somewhere else than the record pointing at them.
func claudeHomeFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("DW_CLAUDE_PROJECTS", "")
	return home
}

// writeSessionRecord writes the file claude itself maintains at <ClaudeHome>/sessions/<pid>.json.
func writeSessionRecord(t *testing.T, home string, pid int, sessionID, cwd string) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"pid": pid, "sessionId": sessionID, "cwd": cwd,
		// Fields the locator deliberately ignores, present so the fixture matches reality:
		// reading "status" from here would be a SECOND opinion about whether an agent is busy.
		"status": "busy", "version": "2.1.220",
	})
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeTranscriptFor creates <projects>/<encoded cwd>/<sessionID>.jsonl with a given mtime.
func writeTranscriptFor(t *testing.T, cwd, sessionID string, modAgo time.Duration) string {
	t.Helper()
	dir := NewProjectLocator().ClaudeProjectDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"assistant"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := time.Now().Add(-modAgo)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestClaudeSessionForProcess covers the lookup that replaced the guess. The failure modes all
// return a MISS rather than a wrong path, because the caller's fallback (the cwd scan) is only
// safe to reach if this never answers confidently and incorrectly.
func TestClaudeSessionForProcess(t *testing.T) {
	cwd := "/tmp/dw-identity-proj"

	t.Run("record names the transcript", func(t *testing.T) {
		home := claudeHomeFixture(t)
		want := writeTranscriptFor(t, cwd, "sess-a", 0)
		writeSessionRecord(t, home, 4242, "sess-a", cwd)

		got, err := NewProjectLocator().ClaudeSessionForProcess(4242, cwd)
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	})

	t.Run("a record for another cwd is not this pane's", func(t *testing.T) {
		home := claudeHomeFixture(t)
		writeTranscriptFor(t, "/tmp/dw-identity-other", "sess-b", 0)
		writeSessionRecord(t, home, 4243, "sess-b", "/tmp/dw-identity-other")

		if _, err := NewProjectLocator().ClaudeSessionForProcess(4243, cwd); err == nil {
			t.Fatal("a record whose cwd is elsewhere must MISS — PID reuse is the case this guards")
		}
	})

	t.Run("named transcript does not exist yet", func(t *testing.T) {
		home := claudeHomeFixture(t)
		writeSessionRecord(t, home, 4244, "sess-not-written", cwd)

		if _, err := NewProjectLocator().ClaudeSessionForProcess(4244, cwd); err == nil {
			t.Fatal("a session whose transcript has not appeared yet must MISS, not return a ghost path")
		}
	})

	t.Run("no record at all", func(t *testing.T) {
		claudeHomeFixture(t)
		if _, err := NewProjectLocator().ClaudeSessionForProcess(4245, cwd); err == nil {
			t.Fatal("a claude too old to publish the record must MISS so the caller falls back")
		}
	})

	t.Run("no pid", func(t *testing.T) {
		claudeHomeFixture(t)
		if _, err := NewProjectLocator().ClaudeSessionForProcess(0, cwd); err == nil {
			t.Fatal("without a pid there is no identity to look up")
		}
	})
}

// TestPaneLocate_IdentityBeatsSameCWDNewest is the regression gate for the bug this was written
// for. Two claude panes in ONE repo: the helper's transcript is older, the main session's is
// being written right now. The old rule — newest first, skip what a sibling already claimed —
// gave the helper the main session's file, because "distinct" is not "correct". It then
// inherited that session's interrupted turn and sat green for ten hours with an idle prompt on
// its own screen.
//
// Deliberately resolved in the order that USED to produce the wrong answer: helper first, so it
// would grab the newest file if anything still decided by mtime.
func TestPaneLocate_IdentityBeatsSameCWDNewest(t *testing.T) {
	home := claudeHomeFixture(t)
	cwd := "/tmp/dw-identity-shared"

	helper := writeTranscriptFor(t, cwd, "sess-helper", 30*time.Minute) // older
	main := writeTranscriptFor(t, cwd, "sess-main", 0)                  // newest — the tempting one
	writeSessionRecord(t, home, 5001, "sess-helper", cwd)
	writeSessionRecord(t, home, 5002, "sess-main", cwd)

	m := NewPaneAgentMonitor(NewProjectLocator())

	if got := m.locate(cwd, ToolClaude, 5001, nil, nil); got != helper {
		t.Fatalf("helper pane bound %q, want its OWN transcript %q", got, helper)
	}
	if got := m.locate(cwd, ToolClaude, 5002, map[string]bool{helper: true}, nil); got != main {
		t.Fatalf("main pane bound %q, want %q", got, main)
	}
}

// TestPaneLocate_FallsBackToCWDScan: identity is an improvement, not a precondition. A claude
// that publishes no record must still be located exactly as before — otherwise this "fix" would
// blind every pane running an older CLI.
func TestPaneLocate_FallsBackToCWDScan(t *testing.T) {
	claudeHomeFixture(t)
	cwd := "/tmp/dw-identity-norecord"
	older := writeTranscriptFor(t, cwd, "sess-old", 30*time.Minute)
	newest := writeTranscriptFor(t, cwd, "sess-new", 0)

	m := NewPaneAgentMonitor(NewProjectLocator())

	if got := m.locate(cwd, ToolClaude, 6001, nil, nil); got != newest {
		t.Fatalf("with no record the newest file is still the answer: got %q want %q", got, newest)
	}
	if got := m.locate(cwd, ToolClaude, 6002, map[string]bool{newest: true}, nil); got != older {
		t.Fatalf("sibling exclusion must still apply: got %q want %q", got, older)
	}
}
