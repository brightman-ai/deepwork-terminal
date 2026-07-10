package agentintel

import (
	"strings"
	"time"
)

// ClaudeSessionState tracks the state derived from Claude JSONL parsing.
type ClaudeSessionState struct {
	Model        string
	Status       AgentStatus
	WaitReason   WaitReason
	Usage        UsageTotals
	LastUserAt   time.Time
	LastAssistAt time.Time
	StopReason   string
	PendingTool  string // name of the unresolved tool_use (for elicitation detection); "" when none
	LastMsgQuestion bool // the last assistant turn ended on a free-text question (heuristic)
	UpdatedAt    time.Time
}

// textEndsQuestion is a best-effort "did the agent ASK the user something" heuristic for a
// free-text turn end — the transcript can't otherwise tell a plain-language question from a
// finished task (both are stop_reason=end_turn). It checks the LAST non-empty line, ignoring
// trailing markdown/quote punctuation, for a '?' / '？'. False positives (a message that merely
// ends on a rhetorical '?') are accepted: such a turn is still legitimately "awaiting you".
func textEndsQuestion(s string) bool {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(strings.TrimSpace(lines[i]), " \t*_`\"'）)》」")
		if line == "" {
			continue
		}
		r := []rune(line)
		last := r[len(r)-1]
		return last == '?' || last == '？'
	}
	return false
}

// elicitationTools are tools whose pending (unresolved) tool_use means the agent is
// ASKING the user and is genuinely waiting for an answer — not executing work. A
// non-elicitation tool_use (Bash/Read/Edit…) that is pending means the tool is
// EXECUTING, i.e. the agent is RUNNING, not waiting.
var elicitationTools = map[string]bool{
	"AskUserQuestion": true,
	"ExitPlanMode":    true,
}

func isElicitationTool(name string) bool { return elicitationTools[name] }

// ClaudeDriver parses a Claude Code JSONL transcript and derives session state.
type ClaudeDriver struct {
	sessionID string
	reader    *JSONLReader
	usage     *UsageAccumulator
	state     ClaudeSessionState
}

// NewClaudeDriver creates a driver for the given JSONL path. sessionID is used as
// part of the dedup key for CountedUsageKey.
func NewClaudeDriver(jsonlPath, sessionID string) *ClaudeDriver {
	return &ClaudeDriver{
		sessionID: sessionID,
		reader:    NewJSONLReader(jsonlPath),
		usage:     NewUsageAccumulator(),
		state:     ClaudeSessionState{Status: StatusIdle},
	}
}

// Update reads new JSONL lines and updates state.
func (cd *ClaudeDriver) Update() error {
	return cd.reader.ReadNewFunc(func(row map[string]any) bool {
		rowType, _ := row["type"].(string)
		now := time.Now()

		switch rowType {
		case "user":
			ts := parseTime(row)
			if ts.After(cd.state.LastUserAt) {
				cd.state.LastUserAt = ts
			}
			cd.state.StopReason = ""
			cd.state.Status = StatusRunning
			cd.state.WaitReason = WaitNone
			cd.state.PendingTool = ""    // tool result arrived → no tool pending
			cd.state.LastMsgQuestion = false // you replied → the prior question is answered
			// Check for interrupted tool use result.
			if msg, ok := row["message"].(map[string]any); ok {
				if content, ok := msg["content"].([]any); ok {
					for _, item := range content {
						if block, ok := item.(map[string]any); ok {
							if res, ok := block["toolUseResult"].(map[string]any); ok {
								if interrupted, _ := res["interrupted"].(bool); interrupted {
									cd.state.Status = StatusWaiting
									cd.state.WaitReason = WaitPermission
								}
							}
						}
					}
				}
			}

		case "assistant":
			ts := parseTime(row)
			if ts.After(cd.state.LastAssistAt) {
				cd.state.LastAssistAt = ts
			}
			cd.state.Status = StatusRunning
			cd.state.WaitReason = WaitNone
			cd.state.PendingTool = "" // recomputed below from this turn's tool_use blocks
			msg, ok := row["message"].(map[string]any)
			if !ok {
				break
			}
			// Model
			if model, ok := msg["model"].(string); ok && model != "" {
				cd.state.Model = model
			}
			// Stop reason
			if sr, ok := msg["stop_reason"].(string); ok && sr != "" {
				cd.state.StopReason = sr
				switch sr {
				case "end_turn":
					cd.state.Status = StatusIdle
					cd.state.WaitReason = WaitNone
				case "tool_use":
					cd.state.Status = StatusRunning
					cd.state.WaitReason = WaitNone
				}
			} else {
				cd.state.StopReason = ""
			}
			// Free-text-question heuristic: gather this turn's text blocks and record whether it
			// ended on a question, so State() can escalate an end_turn to waiting (see textEndsQuestion).
			var text strings.Builder
			if content, ok := msg["content"].([]any); ok {
				for _, item := range content {
					if block, ok := item.(map[string]any); ok {
						if bt, _ := block["type"].(string); bt == "text" {
							if t, _ := block["text"].(string); t != "" {
								text.WriteString(t)
								text.WriteByte('\n')
							}
						}
					}
				}
			}
			cd.state.LastMsgQuestion = textEndsQuestion(text.String())
			// Capture the pending tool name when this turn ends in a tool call, so
			// State() can tell an interactive tool (AskUserQuestion — the agent is
			// asking the user = waiting) from a working tool (Bash/Read — executing =
			// running). Cleared on the next user line (the tool result arrived).
			if cd.state.StopReason == "tool_use" {
				if content, ok := msg["content"].([]any); ok {
					for _, item := range content {
						block, ok := item.(map[string]any)
						if !ok {
							continue
						}
						if bt, _ := block["type"].(string); bt != "tool_use" {
							continue
						}
						if name, _ := block["name"].(string); name != "" {
							cd.state.PendingTool = name
							if isElicitationTool(name) {
								break // an elicitation tool dominates the turn
							}
						}
					}
				}
			}
			// Usage dedup
			if msgID, ok := msg["id"].(string); ok && msgID != "" {
				if usageRaw, ok := msg["usage"].(map[string]any); ok {
					current := UsageTotals{
						InputTokens:       intFromAny(usageRaw["input_tokens"]),
						OutputTokens:      intFromAny(usageRaw["output_tokens"]),
						CacheReadTokens:   intFromAny(usageRaw["cache_read_input_tokens"]),
						CacheCreateTokens: intFromAny(usageRaw["cache_creation_input_tokens"]),
					}
					current.TotalTokens = current.InputTokens + current.OutputTokens +
						current.CacheReadTokens + current.CacheCreateTokens
					key := CountedUsageKey{
						SessionID: cd.sessionID,
						MessageID: msgID,
					}
					cd.usage.Ingest(key, current)
					cd.state.Usage = cd.usage.Totals
				}
			}
		}

		cd.state.UpdatedAt = now
		return true
	})
}

// State returns the current derived state.
func (cd *ClaudeDriver) State() ClaudeSessionState {
	s := cd.state

	// Status derivation from JSONL timeline:
	// - No data yet (agent just started) → idle (waiting for first prompt)
	// - LastUserAt > LastAssistAt → running (user sent prompt, agent processing)
	// - LastAssistAt > LastUserAt with end_turn → idle (turn completed)
	// - tool_use pending: an elicitation tool (AskUserQuestion/ExitPlanMode) → waiting
	//   for the user's answer; any other tool is EXECUTING → running. A blunt time
	//   threshold is NOT used — a long-running tool (build/test) is running, not
	//   waiting. Permission waits are caught by PTY AnalyzeOutput + the interrupted
	//   flag (watcher.currentResponse), not by elapsed time here.
	if cd.state.LastUserAt.IsZero() && cd.state.LastAssistAt.IsZero() {
		// No JSONL data — agent just started, waiting for first prompt.
		s.Status = StatusIdle
		s.WaitReason = WaitNone
	} else if cd.state.StopReason == "tool_use" && isElicitationTool(cd.state.PendingTool) {
		s.Status = StatusWaiting
		s.WaitReason = WaitQuestion
	} else if cd.state.StopReason == "end_turn" && cd.state.LastMsgQuestion {
		// Turn ended on a free-text question → escalate the amber "done" to red "waiting for
		// your answer". Heuristic (see textEndsQuestion); cleared the moment you reply (the
		// user row resets StopReason + LastMsgQuestion), so it can't stick past your response.
		s.Status = StatusWaiting
		s.WaitReason = WaitQuestion
	} else if s.Status != StatusWaiting && !cd.state.LastUserAt.IsZero() && cd.state.LastUserAt.After(cd.state.LastAssistAt) {
		s.Status = StatusRunning
		s.WaitReason = WaitNone
	}
	// Otherwise keep status set by stop_reason parsing (end_turn→idle, tool_use→running).
	return s
}

// AgentState converts to the unified AgentState model.
func (cd *ClaudeDriver) AgentState() AgentState {
	s := cd.State()
	// Needs-you: blocked (waiting) OR the turn ended and the agent spoke last
	// (assistant after user = your move). A fresh idle (no turn yet, user≥assist)
	// is NOT awaiting. Clears automatically when your next prompt makes user>assist.
	awaiting := s.Status == StatusWaiting ||
		(s.Status == StatusIdle && s.LastAssistAt.After(s.LastUserAt))
	as := AgentState{
		Tool:              ToolClaude,
		Model:             s.Model,
		Status:            s.Status,
		WaitReason:        s.WaitReason,
		AwaitingUser:      awaiting,
		InputTokens:       s.Usage.InputTokens,
		OutputTokens:      s.Usage.OutputTokens,
		CacheReadTokens:   s.Usage.CacheReadTokens,
		CacheCreateTokens: s.Usage.CacheCreateTokens,
		TotalTokens:       s.Usage.TotalTokens,
		UpdatedAt:         s.UpdatedAt,
	}
	// The completion behind this awaiting = the last assistant turn's transcript time
	// (a free-text question or a finished turn both end on an assistant message). Same
	// source as `awaiting` itself → equally reload-proof; a new turn moves it forward.
	if awaiting {
		as.AwaitingSince = s.LastAssistAt
	}
	return as
}

// parseTime extracts a timestamp from a JSONL row.
func parseTime(row map[string]any) time.Time {
	if ts, ok := row["timestamp"].(string); ok {
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}

// intFromAny safely converts any numeric JSON value to int.
func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
