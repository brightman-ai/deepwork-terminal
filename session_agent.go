package terminal

import (
	"context"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// Per-SESSION agent detection — the non-tmux twin of tmux's per-PANE detection.
//
// ── Why this is built in rather than injected ────────────────────────────────────────────────
// It used to be a host hook (Hooks.AgentDetect), justified as "avoids an import cycle". That
// justification expired: this package already imports agentintel directly (sessions_overview.go
// renders session tails through it), so there is no cycle to avoid — only two consequences of the
// indirection, both bad:
//
//   - The standalone binary set no hook at all, so on :18074 every non-tmux terminal reported NO
//     agent, no status, no overview dot. Same feature, one shell, invisible on the other — the
//     drift family this codebase keeps paying for.
//   - The one host that DID implement it resolved a session's transcript as "newest Claude file in
//     this cwd". With two terminals open on the same repo — the normal case — both bind to the same
//     file and each reports the OTHER's state. That is the documented false-attribution bug that
//     was fixed for tmux panes months ago (PaneAgentMonitor's binding cache) and never reached
//     this path, because this path had its own private copy of the logic.
//
// Both disappear by construction once detection lives here, once, for every shell.
//
// ── Why PaneAgentMonitor ─────────────────────────────────────────────────────────────────────
// "Pane" is a misnomer for what it does: it binds an opaque KEY to a transcript and keeps that
// binding sticky (PID-anchored, excluding files already claimed by other keys), then answers from
// an incremental driver that parses only newly-appended lines. Nothing in it is tmux-specific — a
// terminal session id is as good a key as a pane id — so reusing it gives this path the collision
// avoidance, the reload-proof AwaitingSince and the sub-millisecond steady-state cost that took
// several iterations to get right on the tmux side, with no second implementation to keep in sync.
type sessionAgentTracker struct {
	inspector *agentintel.ProcessInspector
	monitor   *agentintel.PaneAgentMonitor
}

func newSessionAgentTracker() *sessionAgentTracker {
	return &sessionAgentTracker{
		inspector: agentintel.SharedProcessInspector,
		monitor:   agentintel.NewPaneAgentMonitor(nil),
	}
}

// Tool answers only "which agent runs under this shell, if any" — the cheap half of State,
// with no transcript read, no screen and no binding side effects.
//
// It exists so the BEL qualification (session_signal.go) asks THIS component instead of
// reaching for the process inspector itself. The question "what counts as an agent here" must
// have exactly one answer: the day the detector learns a new runtime, a gate holding its own
// copy would keep silently rejecting that runtime's bells.
func (t *sessionAgentTracker) Tool(ctx context.Context, shellPID int) agentintel.AgentTool {
	if t == nil || shellPID <= 0 {
		return agentintel.ToolNone
	}
	return t.inspector.DetectAgentCtx(ctx, shellPID).Tool
}

// sessionAgentState is what one terminal session's card and tab dot both render.
type sessionAgentState struct {
	Tool            agentintel.AgentTool
	Status          agentintel.AgentStatus
	AwaitingUser    bool
	AwaitingSince   string // RFC3339; "" when not awaiting or the completion is undated
	EndedOnQuestion bool
	// Decision is the provenance of the status above: which single rule produced it and,
	// for a screen-derived verdict, the line that matched. It exists so a wrong "needs you"
	// can be traced afterwards instead of re-argued from an approximation — see
	// agentintel/status_decision.go for the incident that motivated it.
	Decision agentintel.StatusDecision
}

// State resolves one session's agent state.
//
// key is the terminal session id: stable for the session's whole life and never reused, which is
// exactly what the binding cache needs (a recycled key would inherit a dead session's transcript).
//
// screen is the session's rendered PTY screen, already computed for the overview tail. It is the
// ONLY place a permission prompt exists — the CLI draws it in the terminal and never writes it to
// the transcript — so it is consulted for a running agent exactly as the tmux path consults
// capture-pane. Pass the UNSTRIPPED screen: the prompt lives in the bottom chrome that the tail
// strips away. nil is fine (the transcript-derived status is then used as-is).
func (t *sessionAgentTracker) State(ctx context.Context, key string, shellPID int, cwd string, screen []string) sessionAgentState {
	if t == nil || key == "" || shellPID <= 0 {
		return sessionAgentState{}
	}
	agent := t.inspector.DetectAgentCtx(ctx, shellPID)
	if agent.Tool == agentintel.ToolNone {
		return sessionAgentState{}
	}
	out := sessionAgentState{Tool: agent.Tool, Status: agentintel.StatusIdle}

	status, ok := t.monitor.Status(key, cwd, agent.Tool, agent.ProcessPID)
	if !ok {
		// Transcript not locatable yet (a just-started agent, or Codex before its rollout
		// exists). Fall back to the same mtime gate the tmux path uses: writing = working.
		// NOTE this is the one arm where the SCREEN ALONE can declare Waiting — there is no
		// transcript opinion to confirm it against — which is exactly why its decision carries
		// the rule and the line that produced it.
		if t.monitor.Active(key, cwd, agent.Tool, agent.ProcessPID) {
			out.Status = agentintel.StatusRunning
			out.Decision = agentintel.StatusDecision{Status: out.Status, Rule: agentintel.RuleTranscriptWriting}
		} else {
			v := agentintel.AnalyzeOutputDetail(screen)
			if v.State == agentintel.PromptNeedsPermission {
				out.Status = agentintel.StatusWaiting
			}
			out.Decision = agentintel.StatusDecision{Status: out.Status, Rule: v.Rule, Evidence: v.Line}
		}
		out.AwaitingUser = out.Status == agentintel.StatusWaiting
		out.Decision.Awaiting = out.AwaitingUser
		t.logDecision(ctx, key, out)
		return out
	}
	out.Status = status
	out.Decision = agentintel.StatusDecision{Status: status,
		Rule: agentintel.TranscriptStatusRule(status, t.monitor.Snapshot(key))}

	// A transcript-running agent may actually be blocked on a permission prompt: the tool_use is
	// pending in the transcript while the CLI waits for your y/n on screen. Only this direction is
	// checked — the screen can prove "blocked", it cannot prove "not blocked".
	if status == agentintel.StatusRunning {
		if v := agentintel.AnalyzeOutputDetail(screen); v.State == agentintel.PromptNeedsPermission {
			out.Status = agentintel.StatusWaiting
			out.Decision = agentintel.StatusDecision{Status: out.Status, Rule: v.Rule, Evidence: v.Line}
		}
	}

	snap := t.monitor.Snapshot(key)
	out.AwaitingUser = out.Status == agentintel.StatusWaiting ||
		(out.Status == agentintel.StatusIdle && snap.AwaitingUser)
	if out.AwaitingUser && !snap.AwaitingSince.IsZero() {
		out.AwaitingSince = snap.AwaitingSince.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		out.EndedOnQuestion = snap.EndedOnQuestion
	}
	out.Decision.Awaiting = out.AwaitingUser
	t.logDecision(ctx, key, out)
	return out
}

// logDecision emits the attention decisions (and only those: a running agent accuses nobody).
// The log is coalesced per session on the decision itself, so a state CHANGE is emitted at
// once while a session that simply stays "needs you" across the 1s ticks stays quiet.
func (t *sessionAgentTracker) logDecision(ctx context.Context, key string, out sessionAgentState) {
	if !out.Decision.IsAttention() {
		return
	}
	agentintel.LogStatusDecision(ctx, "session", key, out.Tool, out.Decision)
}

// Prune drops bindings for sessions that no longer exist. Called once per overview rebuild so a
// closed session's transcript binding is released — and, more importantly, so it stops blocking a
// live session from claiming that file (bindings are mutually exclusive by design).
func (t *sessionAgentTracker) Prune(live map[string]bool) {
	if t == nil {
		return
	}
	t.monitor.Prune(live)
}
