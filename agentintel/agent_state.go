package agentintel

import "time"

// AgentTool identifies which AI CLI tool is running.
type AgentTool string

const (
	ToolNone     AgentTool = ""
	ToolClaude   AgentTool = "claude"
	ToolCodex    AgentTool = "codex"
	ToolGemini   AgentTool = "gemini"
	ToolOpenCode AgentTool = "opencode"
)

// AgentStatus represents the 5-state lifecycle (aligned with Daintree/Canopy).
type AgentStatus string

const (
	StatusNone    AgentStatus = "none"    // No agent detected
	StatusRunning AgentStatus = "running" // Agent actively working
	StatusIdle    AgentStatus = "idle"    // Turn completed, waiting for next prompt
	StatusWaiting AgentStatus = "waiting" // Needs human input (permission/question)
	StatusDone    AgentStatus = "done"    // Agent process exited
)

// WaitReason differentiates why the agent is waiting.
type WaitReason string

const (
	WaitNone       WaitReason = ""
	WaitPrompt     WaitReason = "prompt"     // Waiting for next user prompt
	WaitPermission WaitReason = "permission" // Waiting for tool approval [Y/n]
	WaitQuestion   WaitReason = "question"   // Asking user a question
)

// AgentState is the full state of an AI agent in a CLI session.
type AgentState struct {
	Tool       AgentTool   `json:"tool"`
	Status     AgentStatus `json:"status"`
	WaitReason WaitReason  `json:"waitReason,omitempty"`
	Model      string      `json:"model,omitempty"`

	// AwaitingUser: the agent completed a turn and has NOT been responded to yet —
	// idle-after-a-turn or blocked (waiting), but not fresh-idle (never ran) and not
	// running. Drives the "needs-you" pane-bar dot + notification; clears when the
	// user's next input lands (the agent goes running again). Derived from transcript
	// timestamps so it survives a page reload — no need to witness the live transition.
	AwaitingUser bool `json:"awaitingUser,omitempty"`

	// AwaitingSince is the transcript timestamp of the turn-completion that put the agent
	// into the current AwaitingUser state (last assistant turn for Claude, last task_complete
	// for Codex). It is DERIVED FROM THE TRANSCRIPT, so — exactly like AwaitingUser — it
	// survives a page reload: the same completion always yields the same value, and a NEW turn
	// yields a new one. The frontend keys its per-window "seen" layer on this so a dismissed
	// dot stays dismissed across F5 yet re-appears when the pane completes another turn.
	// Zero when not awaiting. [needs-you dot persistence]
	AwaitingSince time.Time `json:"awaitingSince,omitempty"`

	// EndedOnQuestion: the completed turn ended on a free-text question ("要我继续吗？") rather
	// than on a statement. It REFINES AwaitingUser — it never creates one — so a consumer can
	// label the same needs-you signal "有提问" instead of "已完成" without changing its severity.
	//
	// This used to escalate the turn all the way to StatusWaiting (red, undismissable). That was
	// wrong on both counts. Wrong severity: after end_turn the agent is sitting at an EMPTY
	// prompt — nothing is blocked, you can type anything — whereas Waiting means the CLI is
	// modal (an AskUserQuestion card, a permission [Y/n]) and literally cannot proceed. Wrong
	// confidence: it is a '?'-on-the-last-line heuristic, and a survey of the local transcript
	// corpus found EVERY waiting came from it, two thirds of them on conversational closers
	// ("要做点什么？", "需要我做什么？") — i.e. an idle agent flagged as blocking the user, with
	// no way to dismiss it. Demoting it to a flag keeps the information and drops the false alarm.
	EndedOnQuestion bool `json:"endedOnQuestion,omitempty"`

	// Token usage (from JSONL parsing)
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	CacheReadTokens   int `json:"cacheReadTokens"`
	CacheCreateTokens int `json:"cacheCreateTokens"`
	TotalTokens       int `json:"totalTokens"`

	// tmux pane info (nil if not in tmux)
	TmuxWindow *int `json:"tmuxWindow,omitempty"` // ctrl+b+N
	TmuxPane   *int `json:"tmuxPane,omitempty"`

	// Timing
	StartedAt time.Time `json:"startedAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Internal: which signals contributed to this state
	SignalSource string `json:"-"` // "jsonl", "process", "pty_idle", "output"
}

// AgentIntelResponse is the full API response for a CLI session's agent intelligence.
type AgentIntelResponse struct {
	// Current is the agent state for the active tmux window (or direct session).
	// Includes token data from JSONL. Null if no agent in the active pane.
	Current *AgentState `json:"current"`

	// Notifications lists all panes across the tmux session that need user input.
	// Includes panes from non-active windows. Empty if no panes need input.
	Notifications []AgentState `json:"notifications"`
}

// ── "Your move" has a shelf life ─────────────────────────────────────────────────────────────

// awaitingShelfLife is how long a COMPLETED turn stays worth a dot.
//
// AwaitingUser means "an agent finished and you have not looked". It used to be true forever:
// a pane whose turn ended three days ago still reported needs-you, so opening the workbench
// showed a row of amber dots for sessions that had been sitting untouched for days (observed
// live: two panes at 3.2 and 3.3 DAYS, alongside genuine ones at 0.9h and 2.2h).
//
// The dot is not wrong in a literal sense — nobody did look. It is wrong in the sense that
// matters: an alert whose job is "come see, something finished" stops doing that job once the
// thing finished long enough ago that you have obviously moved on. And it does damage while it
// sits there, because a permanent amber dot devalues the fresh ones next to it. The failure of a
// notifier is not that it is silent, it is that you learn to ignore it.
//
// 24 hours, because that is where the two cases separate. "It finished while I slept" is real and
// worth keeping. "It finished last Tuesday" is not something anyone is about to act on — and if
// they do go looking, the overview card still shows the session and its tail. Only the CLAIM ON
// YOUR ATTENTION is withdrawn, never the information.
const awaitingShelfLife = 24 * time.Hour

// ExpireStaleAwaiting withdraws a needs-you flag whose completion is older than the shelf life.
//
// Deliberately confined to a COMPLETED turn. A blocked agent (StatusWaiting — the red dot) is
// still blocked no matter how long it has been blocked: nothing has resolved, and the moment you
// look at it there is something to do. Age says a completion is no longer news; it says nothing
// at all about a block.
//
// A zero AwaitingSince means the driver could not date the completion, so there is nothing to
// judge and the flag is left alone — an undatable completion is not evidence of an old one.
//
// `now` is a parameter so the rule is testable without sleeping, and so both drivers share ONE
// definition of "too old" rather than each growing their own (they already compute AwaitingUser
// separately, which is exactly how the two paths drift).
func ExpireStaleAwaiting(as AgentState, now time.Time) AgentState {
	if !as.AwaitingUser || as.Status == StatusWaiting {
		return as
	}
	if as.AwaitingSince.IsZero() || now.Sub(as.AwaitingSince) <= awaitingShelfLife {
		return as
	}
	as.AwaitingUser = false
	// EndedOnQuestion only ever labelled the dot we just withdrew ("有提问" instead of "已完成");
	// leaving it set would describe a dot that is no longer there.
	as.EndedOnQuestion = false
	return as
}
