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
