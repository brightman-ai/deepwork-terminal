package agentintel

import "time"

// CodexSessionState tracks the state derived from Codex JSONL parsing.
type CodexSessionState struct {
	SessionID    string
	Model        string
	CWD          string
	Status       AgentStatus
	Awaiting     bool      // a turn completed (task_complete) and no new turn started since = needs-you
	LastTurnAt   time.Time // transcript time of the last task_complete — the reload-proof "completed at" behind Awaiting
	InputTokens  int
	OutputTokens int
	CachedTokens int
	TotalTokens  int
	UpdatedAt    time.Time
}

// CodexDriver parses a Codex CLI JSONL rollout file and derives session state.
type CodexDriver struct {
	reader *JSONLReader
	state  CodexSessionState
}

func NewCodexDriver(jsonlPath string) *CodexDriver {
	return &CodexDriver{
		reader: NewJSONLReader(jsonlPath),
		state:  CodexSessionState{Status: StatusIdle},
	}
}

// Update reads new JSONL lines and updates state.
func (cd *CodexDriver) Update() error {
	return cd.reader.ReadNewFunc(func(row map[string]any) bool {
		rowType, _ := row["type"].(string)
		payload, _ := row["payload"].(map[string]any)

		switch rowType {
		case "session_meta":
			if payload == nil {
				break
			}
			if cwd, ok := payload["cwd"].(string); ok && cwd != "" {
				cd.state.CWD = cwd
			}
			if model, ok := payload["model"].(string); ok && model != "" {
				cd.state.Model = model
			}
			if sid, ok := payload["session_id"].(string); ok && sid != "" {
				cd.state.SessionID = sid
			}

		case "event_msg":
			if payload == nil {
				break
			}
			switch evType, _ := payload["type"].(string); evType {
			case "task_started":
				// A turn began — you responded (or kicked it off), so no longer awaiting.
				cd.state.Status = StatusRunning
				cd.state.Awaiting = false
			case "task_complete":
				// Turn finished → Idle ("Turn completed, waiting for next prompt",
				// per AgentStatus). One turn-complete semantic shared with
				// claude_driver's end_turn→Idle; a genuine "needs human input"
				// (approval/question) is detected separately by the tool-agnostic
				// PTY analyzer, not inferred from a turn boundary. push_notifier
				// still fires the turn-end web push on this running→Idle transition
				// (it triggers on Idle OR Waiting), so notifications are preserved
				// without pinning a permanent red "waiting" dot on a resting pane.
				cd.state.Status = StatusIdle
				cd.state.Awaiting = true // turn done, your move → needs-you (soft)
				// Record the transcript time of THIS completion (not time.Now) so the
				// needs-you "seen" key is reload-proof and advances on the next turn.
				cd.state.LastTurnAt = parseTime(row)
			case "token_count":
				info, ok := payload["info"].(map[string]any)
				if !ok {
					break
				}
				usage, ok := info["total_token_usage"].(map[string]any)
				if !ok {
					break
				}
				// Codex values are already cumulative totals — take latest directly.
				cd.state.InputTokens = intFromAny(usage["input"])
				cd.state.OutputTokens = intFromAny(usage["output"])
				cd.state.CachedTokens = intFromAny(usage["cached"])
				cd.state.TotalTokens = intFromAny(usage["total"])
			}

		case "response_item":
			cd.state.Status = StatusRunning
			cd.state.Awaiting = false
		}

		cd.state.UpdatedAt = time.Now()
		return true
	})
}

// State returns the current derived state.
func (cd *CodexDriver) State() CodexSessionState { return cd.state }

// AgentState converts to the unified AgentState model.
func (cd *CodexDriver) AgentState() AgentState {
	s := cd.state
	as := AgentState{
		Tool:         ToolCodex,
		Model:        s.Model,
		Status:       s.Status,
		AwaitingUser: s.Awaiting, // task_complete seen, no new turn → your move

		InputTokens:  s.InputTokens,
		OutputTokens: s.OutputTokens,
		TotalTokens:  s.TotalTokens,
		UpdatedAt:    s.UpdatedAt,
	}
	if s.Awaiting {
		as.AwaitingSince = s.LastTurnAt
	}
	return as
}
