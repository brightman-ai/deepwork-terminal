package terminal

import (
	"context"
	"encoding/json"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// pasteInferBudget bounds the evidence-gathering below. It runs on the WS connect path, where the
// browser is waiting for its replay; an answer that arrives late is worth less than no answer
// (unknown is a valid, safe one).
const pasteInferBudget = 250 * time.Millisecond

// pasteLateBudget bounds the ONE re-ask after a connect went out without an answer. Off the
// critical path now, so it can outlast a cold `ps` (430-640ms measured on a busy machine —
// longer than pasteInferBudget, which is exactly why the connect-time ask can miss).
const pasteLateBudget = 3 * time.Second

// detectShellAgent finds the AI CLI running under a shell. A variable so tests stand in for `ps`.
var detectShellAgent = func(ctx context.Context, shellPID int) agentintel.AgentTool {
	return agentintel.SharedProcessInspector.DetectToolCtx(ctx, shellPID)
}

// agentNegotiatesPaste: Codex and Claude Code switch bracketed paste on when they start. That is
// a fact about the programs, not a guess about the session.
func agentNegotiatesPaste(t agentintel.AgentTool) bool {
	return t == agentintel.ToolCodex || t == agentintel.ToolClaude
}

// effectivePasteMode is the paste mode a browser should be told about: the OBSERVED one when there
// is one, otherwise (and only then) what the foreground program is known to negotiate.
//
// Why a session can be "unknown" at all, and why that is not rare: the mode is announced once, at
// program start, and the server learns it by watching the output stream (paste_mode.go). A session
// that was already running when the server first saw it, whose announcement had scrolled out of the
// 1 MiB ring, has nothing to observe — and a browser that has just loaded starts with bracketed
// paste off. Telling that browser nothing leaves a long-running Codex receiving every pasted
// character as a keystroke. Restarting the program would cure it (it announces itself again), but
// "restart your session" is not a fix.
//
// This is inference, so it is kept out of the session's own state: nothing is recorded or
// persisted, and the first real DECSET/DECRST seen on the stream replaces it.
func (s *Server) effectivePasteMode(ctx context.Context, sess *Session) (enabled, known bool) {
	return s.effectivePasteModeWithin(ctx, sess, pasteInferBudget)
}

// effectivePasteModeWithin is effectivePasteMode with the budget as a parameter: the WS connect
// path passes pasteInferBudget (the browser is waiting), the late re-ask passes pasteLateBudget
// (nobody is). Everything else — observed wins, inference is never recorded — is identical.
func (s *Server) effectivePasteModeWithin(ctx context.Context, sess *Session, budget time.Duration) (enabled, known bool) {
	if enabled, known = sess.PasteMode(); known {
		return enabled, true
	}
	pid := sess.ShellPID()
	if pid <= 0 {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// An answer that arrives after the budget ran out is a stale-cache answer at best (the
	// inspector falls back to its last snapshot on cancellation); unknown is safer than that.
	if tool := detectShellAgent(ctx, pid); ctx.Err() == nil && agentNegotiatesPaste(tool) {
		return true, true
	}
	// A tab attached to tmux: the program is not under the shell's process tree but under tmux's
	// server. Only the pane that has the keyboard counts — a Codex in another window, or in the
	// other half of a split, says nothing about the pane that would receive this paste.
	if tool := activeTmuxPaneAgent(ctx, s.tmuxProvider, pid); ctx.Err() == nil && agentNegotiatesPaste(tool) {
		return true, true
	}
	return false, false
}

// activeTmuxPaneAgent is the agent in the focused pane of the active window of the tmux session
// this shell is attached to; ToolNone when there is none or any step cannot be answered.
func activeTmuxPaneAgent(ctx context.Context, provider TmuxStateProvider, shellPID int) agentintel.AgentTool {
	if provider == nil || shellPID <= 0 {
		return agentintel.ToolNone
	}
	raw, err := provider.TmuxState(ctx, shellPID)
	if err != nil {
		return agentintel.ToolNone
	}
	var st agentintel.TmuxState
	if json.Unmarshal(raw, &st) != nil || !st.Attached || st.AttachedSession == "" {
		return agentintel.ToolNone
	}
	for i := range st.Sessions {
		if st.Sessions[i].Name != st.AttachedSession {
			continue
		}
		for _, w := range st.Sessions[i].Windows {
			if !w.Active {
				continue
			}
			for _, p := range w.Panes {
				if p.Active {
					return p.SurfaceUnit.AgentTool
				}
			}
		}
	}
	return agentintel.ToolNone
}

// latePasteModeFrame answers the question a connect had to leave open. Called OFF the critical
// path, after a replay went out with the mode unknown: re-ask with a budget that survives a cold
// ps, and return the frame to push to the waiting connection — nil when there is nothing to say.
//
// Two honest silences:
//   - The mode became KNOWN while we waited: that answer reached the browser through the live
//     output stream on its own (a real DECSET/DECRST rides the same bytes the browser renders).
//     Pushing a second copy would be redundant, not wrong — but the interesting case is the one
//     where nothing was ever observed, which is the only case left.
//   - The re-ask still comes back unknown: the browser stays exactly where the replay left it.
//
// The frame goes ONLY to the connection that asked. An inferred mode is not an observation, so
// it must not enter the session's output stream — the next replay would then present inference
// as observed fact, and notePasteMode would record and persist a guess.
func (s *Server) latePasteModeFrame(ctx context.Context, sess *Session) []byte {
	if _, known := sess.PasteMode(); known {
		return nil
	}
	enabled, known := s.effectivePasteModeWithin(ctx, sess, pasteLateBudget)
	if !known {
		return nil
	}
	if enabled {
		return []byte("\x1b[?2004h")
	}
	return []byte("\x1b[?2004l")
}

// startProcessWarmer primes the shared process snapshot at startup. The connect-time inference
// budget is shorter than a cold ps on a busy machine, so without this the first connections after
// a restart — precisely when no session's paste mode has been observed yet — all miss and fall
// back on the late push. One shot, best-effort; the 3s snapshot TTL and the background attention
// loop keep it warm from there.
func (s *Server) startProcessWarmer(ctx context.Context) {
	go func() {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		agentintel.SharedProcessInspector.Warm(wctx)
	}()
}
