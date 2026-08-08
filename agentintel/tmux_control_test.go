package agentintel

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func requireTmuxServer(t *testing.T) *TmuxProber {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tp := NewTmuxProber(SharedProcessInspector)
	if _, err := tp.ListPanes(context.Background()); err != nil {
		t.Skip("no tmux server")
	}
	return tp
}

// The transport is an optimisation, so the ONE thing that must never differ is the answer.
// A persistent connection that returns subtly different bytes — a stripped trailing line, an
// unescaped frame terminator inside a captured screen — would corrupt the topology in a way
// no latency graph would show.
func TestControlTransportAnswersIdenticallyToSpawning(t *testing.T) {
	tp := requireTmuxServer(t)
	ctx := context.Background()

	panes, err := tp.ListPanes(ctx) // over the control connection
	if err != nil {
		t.Fatal(err)
	}
	window := ""
	for _, p := range panes {
		if window == "" {
			window = p.SessionWindow
		}
	}
	tail, err := tp.CaptureWindowTail(ctx, window, ToolClaude, 40)
	if err != nil {
		t.Fatal(err)
	}

	tp.control.Close()
	tp.control = nil // force the process path
	spawnedPanes, err := tp.ListPanes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spawnedTail, err := tp.CaptureWindowTail(ctx, window, ToolClaude, 40)
	if err != nil {
		t.Fatal(err)
	}

	if len(panes) != len(spawnedPanes) {
		t.Fatalf("pane count differs by transport: control=%d spawned=%d", len(panes), len(spawnedPanes))
	}
	for i := range panes {
		// PID/cwd can legitimately move between the two reads; identity and geometry cannot.
		if panes[i].PaneID != spawnedPanes[i].PaneID || panes[i].SessionWindow != spawnedPanes[i].SessionWindow {
			t.Fatalf("pane %d differs: control=%+v spawned=%+v", i, panes[i], spawnedPanes[i])
		}
	}
	// A live pane may print between the two captures; compare shape, not exact content.
	if (len(tail) == 0) != (len(spawnedTail) == 0) {
		t.Fatalf("capture emptiness differs by transport: control=%d spawned=%d", len(tail), len(spawnedTail))
	}
}

// A frame terminator is `%end <ts> <cmd> <flags>`. tmux does NOT escape pane content, so a
// screen that happens to show the text "%end" must not truncate its own capture — the
// difference between a terminator and a coincidence is the three fields after the keyword.
func TestControlFrameEndIsNotConfusedWithPaneContent(t *testing.T) {
	for _, line := range []string{
		"%end 1786171529 12 1",
		"%error 1786171529 12 1",
	} {
		keyword := strings.Fields(line)[0]
		if !isControlFrameEnd(line, keyword) {
			t.Fatalf("real terminator rejected: %q", line)
		}
	}
	for _, line := range []string{
		"%end",
		"%end of transmission",
		"  %end 1 2 3",
		"%endpoint 1 2 3 4",
		"%error: could not open file",
	} {
		if isControlFrameEnd(line, "%end") || isControlFrameEnd(line, "%error") {
			t.Fatalf("pane content mistaken for a terminator: %q", line)
		}
	}
}

// The connection is best-effort: when it cannot be made, every call must still be answered by
// spawning a process — the behaviour that shipped before it existed. Otherwise a tmux server
// that restarts would take the pane bar with it.
func TestProberFallsBackWhenTheControlConnectionCannotDial(t *testing.T) {
	tp := requireTmuxServer(t)
	tp.control.Close()
	// A connection that is dead AND inside its redial throttle can never answer.
	tp.control = newTmuxControl()
	tp.control.lastDial = time.Now()

	panes, err := tp.ListPanes(context.Background())
	if err != nil || len(panes) == 0 {
		t.Fatalf("a dead control connection stopped the prober answering: panes=%d err=%v", len(panes), err)
	}
}

// The reply must belong to the command that asked. A caller that times out drops the
// connection rather than leaving an orphan reply to be handed to the next question.
func TestControlDropsTheConnectionWhenTheCallerTimesOut(t *testing.T) {
	tp := requireTmuxServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := tp.control.run(ctx, "list-sessions"); err == nil {
		t.Fatal("an expired context still produced an answer")
	}
	tp.control.mu.Lock()
	live := tp.control.proc != nil
	tp.control.mu.Unlock()
	if live {
		t.Fatal("the connection survived a timeout, so the next command could read this one's reply")
	}
}

// The observer must not change what it observes. The control connection attaches to a session
// in order to run commands on it, and attaching flips #{session_attached} — measured live: 0 → 1.
// Reading attachment from list-clients and subtracting control-mode clients is what keeps a
// session you detached from reading as detached.
func TestAttachmentSubtractsOurOwnControlClient(t *testing.T) {
	sep := tmuxFieldSep
	got := parseClientAttachment(strings.Join([]string{
		"work" + sep + "0",  // a real terminal
		"work" + sep + "1",  // our control connection, attached to the same session
		"solo" + sep + "1",  // a session whose ONLY client is ours
		"legacy" + sep + "", // pre-3.2 tmux: unknown → counted, the safe direction
		"malformed-no-sep",  // ignored rather than guessed at
	}, "\n"))
	if !got["work"] {
		t.Fatal("a session with a real client must read as attached")
	}
	if got["solo"] {
		t.Fatal("our own control client was counted as a user attachment")
	}
	if !got["legacy"] {
		t.Fatal("a client we cannot classify must count as real, not be silently subtracted")
	}
	if len(got) != 2 {
		t.Fatalf("unexpected sessions: %+v", got)
	}
}
