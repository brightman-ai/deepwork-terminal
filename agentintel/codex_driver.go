package agentintel

import "time"

// CodexSessionState tracks the state derived from Codex JSONL parsing.
type CodexSessionState struct {
	SessionID    string
	Model        string
	CWD          string
	Status       AgentStatus
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
			evType, _ := payload["type"].(string)
			if evType != "token_count" {
				break
			}
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

		case "response_item":
			cd.state.Status = StatusRunning
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
	return AgentState{
		Tool:         ToolCodex,
		Model:        s.Model,
		Status:       s.Status,
		InputTokens:  s.InputTokens,
		OutputTokens: s.OutputTokens,
		TotalTokens:  s.TotalTokens,
		UpdatedAt:    s.UpdatedAt,
	}
}
