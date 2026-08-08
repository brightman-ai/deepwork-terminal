package terminal

import (
	"context"
	"time"

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
	AwaitingSince   time.Time // zero when not awaiting, or when the completion is undated
	EndedOnQuestion bool
	// ActivityAt is when this session's agent last WROTE to its transcript — the age of the
	// evidence behind Status. Cache-only (one stat, never a directory scan), and read AFTER the
	// status resolution below so the session is bound to a transcript by the time it is asked.
	//
	// The tmux pane has shipped this since a pane sat "running" for ten hours off a transcript
	// nothing had touched overnight. A non-tmux card could tell exactly the same lie, and nothing
	// on it would have caught the difference — the field simply had not been carried across.
	ActivityAt time.Time
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

	// ONE decision, shared with the tmux pane. This function used to hold its own copy of the
	// whole verdict — transcript status, the permission-prompt confirmation, needs-you,
	// awaiting-since, the rule — under a comment asking it not to drift from the pane's copy.
	// It had drifted on three of them (no spinner veto, a rule naming the wrong subsystem for an
	// unlocatable transcript, an ungated EndedOnQuestion). See agentintel/surface_decision.go.
	//
	// The only thing this source contributes is where the screen comes from. It is already in
	// memory — replayed for the card's tail — so unlike a pane there is nothing to capture and
	// nothing that can fail. `ok=false` here means the session has no replay YET (no ring
	// content), which is genuinely "could not read", not "read it, it was blank".
	unit, decision := t.monitor.DecideSurface(agentintel.SurfaceProbe{
		Key: key, CWD: cwd, Tool: agent.Tool, ProcessPID: agent.ProcessPID,
		Screen: func() ([]string, bool) { return screen, screen != nil },
	})

	out := sessionAgentState{
		Tool:            unit.AgentTool,
		Status:          unit.AgentStatus,
		AwaitingUser:    unit.AwaitingUser,
		AwaitingSince:   unit.AwaitingSince,
		EndedOnQuestion: unit.EndedOnQuestion,
		ActivityAt:      unit.ActivityAt,
		Decision:        decision,
	}
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
