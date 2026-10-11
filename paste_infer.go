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
	if enabled, known = sess.PasteMode(); known {
		return enabled, true
	}
	pid := sess.ShellPID()
	if pid <= 0 {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, pasteInferBudget)
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
