package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// stubDetector replaces the `ps`-backed process-tree detector.
func stubDetector(t *testing.T, fn func(ctx context.Context, pid int) agentintel.AgentTool) *int {
	t.Helper()
	calls := 0
	old := detectShellAgent
	detectShellAgent = func(ctx context.Context, pid int) agentintel.AgentTool { calls++; return fn(ctx, pid) }
	t.Cleanup(func() { detectShellAgent = old })
	return &calls
}

func always(tool agentintel.AgentTool) func(context.Context, int) agentintel.AgentTool {
	return func(context.Context, int) agentintel.AgentTool { return tool }
}

type stubTmuxState struct {
	state agentintel.TmuxState
	err   error
}

func (f stubTmuxState) TmuxState(context.Context, int) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return json.Marshal(f.state)
}

func paneWith(active bool, tool agentintel.AgentTool) agentintel.TmuxPaneState {
	return agentintel.TmuxPaneState{Active: active, SurfaceUnit: agentintel.SurfaceUnit{AgentTool: tool}}
}

// A window whose panes are given; `active` is the window being looked at.
func windowWith(active bool, panes ...agentintel.TmuxPaneState) agentintel.TmuxWindowState {
	return agentintel.TmuxWindowState{Active: active, Panes: panes}
}

func attachedTo(windows ...agentintel.TmuxWindowState) agentintel.TmuxState {
	return agentintel.TmuxState{Attached: true, AttachedSession: "work",
		Sessions: []agentintel.TmuxSessionState{{Name: "work", Windows: windows}}}
}

func sessionWithShell(pid int) *Session {
	s := &Session{ID: "s1"}
	s.shellPID = pid
	return s
}

// The case that kept the bug alive for sessions that predate the fix: nothing observed (the
// announcement scrolled out of the ring), but the program in front is Codex.
func TestUnknownPasteModeIsInferredFromAKnownAgent(t *testing.T) {
	for _, tool := range []agentintel.AgentTool{agentintel.ToolCodex, agentintel.ToolClaude} {
		stubDetector(t, always(tool))
		sess := sessionWithShell(4242)
		enabled, known := (&Server{}).effectivePasteMode(context.Background(), sess)
		if !enabled || !known {
			t.Fatalf("%s: enabled=%t known=%t, want the agent's negotiated mode (on)", tool, enabled, known)
		}
		// Inference is not observation: nothing is recorded, so a real DECSET/DECRST still wins
		// and nothing wrong gets persisted into the daemon.
		if _, observed := sess.PasteMode(); observed {
			t.Fatal("an inferred mode leaked into the session's observed state")
		}
	}
}

func TestObservedPasteModeBeatsInference(t *testing.T) {
	calls := stubDetector(t, always(agentintel.ToolCodex))
	sess := sessionWithShell(4242)
	sess.notePasteMode(false) // the program really did switch it off
	sess.metaSaver = func(string, []byte) error { return nil }
	enabled, known := (&Server{}).effectivePasteMode(context.Background(), sess)
	if enabled || !known {
		t.Fatalf("enabled=%t known=%t; an observed OFF must not be overridden by a guess", enabled, known)
	}
	if *calls != 0 {
		t.Fatal("inference ran even though the mode was known")
	}
}

func TestPasteModeStaysUnknownWithoutEvidence(t *testing.T) {
	stubDetector(t, always(agentintel.ToolNone))
	enabled, known := (&Server{}).effectivePasteMode(context.Background(), sessionWithShell(4242))
	if enabled || known {
		t.Fatalf("a plain shell must stay unknown, got enabled=%t known=%t", enabled, known)
	}

	calls := stubDetector(t, always(agentintel.ToolCodex))
	if _, known := (&Server{}).effectivePasteMode(context.Background(), sessionWithShell(0)); known {
		t.Fatal("a session with no shell pid has nothing to inspect")
	}
	if *calls != 0 {
		t.Fatal("the detector was asked about pid 0")
	}
}

func TestPasteModeInferenceForTmuxTabsUsesOnlyTheFocusedPane(t *testing.T) {
	stubDetector(t, always(agentintel.ToolNone))
	infer := func(p TmuxStateProvider) (bool, bool) {
		return (&Server{tmuxProvider: p}).effectivePasteMode(context.Background(), sessionWithShell(4242))
	}
	codex, none := agentintel.ToolCodex, agentintel.ToolNone
	if on, known := infer(stubTmuxState{state: attachedTo(
		windowWith(false, paneWith(true, none)),
		windowWith(true, paneWith(true, codex)))}); !on || !known {
		t.Fatalf("Codex in the focused pane of the active window: on=%t known=%t", on, known)
	}
	// A Codex in a window nobody is looking at says nothing about the pane that gets this paste.
	if on, known := infer(stubTmuxState{state: attachedTo(
		windowWith(false, paneWith(true, codex)),
		windowWith(true, paneWith(true, none)))}); on || known {
		t.Fatalf("Codex in a background window must not count: on=%t known=%t", on, known)
	}
	// Nor does the other half of a split.
	if on, known := infer(stubTmuxState{state: attachedTo(
		windowWith(true, paneWith(false, codex), paneWith(true, none)))}); on || known {
		t.Fatalf("Codex in an unfocused pane must not count: on=%t known=%t", on, known)
	}
	if _, known := infer(stubTmuxState{err: errors.New("tmux gone")}); known {
		t.Fatal("a tmux error must read as unknown")
	}
	notAttached := attachedTo(windowWith(true, paneWith(true, codex)))
	notAttached.Attached = false
	if _, known := infer(stubTmuxState{state: notAttached}); known {
		t.Fatal("a shell that is not attached to tmux must not borrow tmux's answer")
	}
}

// The connect path must not hang on slow evidence: unknown is a safe answer, a late one is not.
func TestPasteModeInferenceIsBoundedInTime(t *testing.T) {
	stubDetector(t, func(ctx context.Context, _ int) agentintel.AgentTool {
		<-ctx.Done()
		return agentintel.ToolCodex // even a late "yes" must not be used past the budget's end
	})
	start := time.Now()
	enabled, known := (&Server{}).effectivePasteMode(context.Background(), sessionWithShell(4242))
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("effectivePasteMode took %s; it must give up within its %s budget", d, pasteInferBudget)
	}
	if enabled || known {
		t.Fatalf("enabled=%t known=%t: an answer that missed the budget must not be used", enabled, known)
	}
}

// Wiring: the real WS handler must deliver the inferred mode, or none of the above reaches a
// browser — as its own frame right after the restore, never by holding the first screen back while
// the process table and tmux are asked (their locks outlive any deadline).
func TestTerminalWSDeliversInferredPasteModeAfterTheRestore(t *testing.T) {
	stubDetector(t, always(agentintel.ToolCodex))
	server, sm, _ := newDrawerTestServer(t)
	sess, err := sm.Create("old-codex-session")
	require.NoError(t, err)
	sess.mu.Lock()
	sess.shellPID = 31337
	sess.mu.Unlock()
	payload := []byte("a repaint with no mode toggle in the retained window\r\n")
	sess.Buffer.Write(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/sessions/"+sess.ID+"/ws?auth="+testAuthCode, nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	want := append([]byte(restorePrelude), payload...)
	want = append(want, "\x1b[?2004h"...)
	var got []byte
	for len(got) < len(want) {
		kind, data, err := conn.Read(ctx)
		require.NoError(t, err)
		if kind == websocket.MessageBinary {
			got = append(got, data...)
		}
	}
	require.Equal(t, want, got)
}

// The connect budget is deliberately shorter than a cold ps, so the answer that matters most —
// a Codex that announced bracketed paste long ago, right after a server restart — routinely
// misses it. The late re-ask is what the browser is owed then.
func TestLatePasteModeFramePushesInference(t *testing.T) {
	stubDetector(t, always(agentintel.ToolCodex))
	frame := (&Server{}).latePasteModeFrame(context.Background(), sessionWithShell(4242))
	require.Equal(t, "\x1b[?2004h", string(frame))
}

// A mode that became known on its own rides the live stream to the browser; the late answer
// must not double-assert it.
func TestLatePasteModeFrameSilentWhenModeBecameKnown(t *testing.T) {
	stubDetector(t, always(agentintel.ToolCodex))
	sess := sessionWithShell(4242)
	sess.notePasteMode(false)
	require.Nil(t, (&Server{}).latePasteModeFrame(context.Background(), sess))
}

func TestLatePasteModeFrameSilentWhenStillUnknown(t *testing.T) {
	stubDetector(t, always(agentintel.ToolNone))
	require.Nil(t, (&Server{}).latePasteModeFrame(context.Background(), sessionWithShell(4242)))
}

// End to end: connect while the detector is slow (the cold-ps case), then let it answer. The
// restore must go out without waiting for it and with NO assertion (unknown is never asserted),
// and the resolution must arrive as its own frame — without the inferred byte ever entering the
// session's stream, where it would be replayed later as if observed.
func TestTerminalWSPushesLatePasteModeAfterUnknownConnect(t *testing.T) {
	gate := make(chan struct{})
	stubDetector(t, func(ctx context.Context, _ int) agentintel.AgentTool {
		select {
		case <-gate: // hold until the replay is verifiably out
			return agentintel.ToolCodex
		case <-ctx.Done():
			return agentintel.ToolNone
		}
	})

	server, sm, _ := newDrawerTestServer(t)
	sess, err := sm.Create("restart-cold-codex")
	require.NoError(t, err)
	sess.mu.Lock()
	sess.shellPID = 31337
	sess.mu.Unlock()
	payload := []byte("a repaint with no mode toggle in the retained window\r\n")
	sess.Buffer.Write(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/sessions/"+sess.ID+"/ws?auth="+testAuthCode, nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	// The replay is exactly the prelude and the payload: no assertion, and no silent "off" either.
	want := append([]byte(restorePrelude), payload...)
	var replay []byte
	for len(replay) < len(want) {
		kind, data, err := conn.Read(ctx)
		require.NoError(t, err)
		if kind == websocket.MessageBinary {
			replay = append(replay, data...)
		}
	}
	require.Equal(t, want, replay)

	close(gate)
	var late []byte
	for late == nil {
		kind, data, err := conn.Read(ctx)
		require.NoError(t, err)
		if kind == websocket.MessageBinary && bytes.Equal(data, []byte("\x1b[?2004h")) {
			late = data
		}
	}

	// The pushed byte never entered the session stream: the next replay must not present this
	// inference as an observed fact, and notePasteMode must not have persisted a guess.
	full, _ := sess.ReplayTail(1 << 20)
	require.NotContains(t, string(full), "?2004")
	if _, observed := sess.PasteMode(); observed {
		t.Fatal("an inferred mode leaked into the session's observed state")
	}
}
