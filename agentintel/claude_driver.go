package agentintel

import (
	"os"
	"strings"
	"time"
)

// ClaudeSessionState tracks the state derived from Claude JSONL parsing.
type ClaudeSessionState struct {
	Model           string
	Status          AgentStatus
	WaitReason      WaitReason
	Usage           UsageTotals
	LastUserAt      time.Time
	LastAssistAt    time.Time
	StopReason      string
	PendingTool     string // name of the unresolved tool_use (for elicitation detection); "" when none
	LastMsgQuestion bool   // the last assistant turn ended on a free-text question (heuristic)
	// LastUserInterrupt marks the newest user row as an INTERRUPT (Esc), not a prompt. It is
	// the difference between "you asked something and the agent is thinking" and "you stopped
	// the agent" — two states that are indistinguishable by row order alone, since both leave a
	// user row after the last assistant row. Cleared by the next real user or assistant row.
	LastUserInterrupt bool
	UpdatedAt         time.Time
}

// pendingReplyWindow bounds how long "your prompt is newer than the agent's last word" may be
// read as RUNNING.
//
// That rule has no corroborating signal — unlike a pending tool_use (a named tool), a fresh
// mtime (the writing gate) or a spinner (the pane). It is pure row order, so a turn that never
// got a reply stays running forever: a killed CLI, a crashed session, a machine that slept
// mid-turn. Ten hours of green was the observed case.
//
// The window is generous on purpose — extended thinking before the first assistant token is
// real, and cutting a working agent to idle would fire a false "done". It is also only half the
// test: staleness ALONE never downgrades anything. The transcript must additionally have gone
// untouched for this long (see State), and a working agent writes continuously — thinking
// blocks, tool calls, results. So the only thing that trips this is a transcript nothing is
// writing to, which is not a running agent by any definition.
const pendingReplyWindow = 15 * time.Minute

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
	jsonlPath string // kept for pendingReplyStale: "has anything been written since" needs the file, not just its parsed rows
	reader    *JSONLReader
	usage     *UsageAccumulator
	state     ClaudeSessionState
	agentTree claudeAgentTree // subagent ("Agent" tool) spawn tree — see claude_agent_tree.go
}

// NewClaudeDriver creates a driver for the given JSONL path. sessionID is used as
// part of the dedup key for CountedUsageKey.
func NewClaudeDriver(jsonlPath, sessionID string) *ClaudeDriver {
	return &ClaudeDriver{
		sessionID: sessionID,
		jsonlPath: jsonlPath,
		reader:    NewJSONLReader(jsonlPath),
		usage:     NewUsageAccumulator(),
		state:     ClaudeSessionState{Status: StatusIdle},
		agentTree: newClaudeAgentTree(jsonlPath),
	}
}

// pendingReplyStale reports whether a prompt that never got an answer has gone cold: older
// than pendingReplyWindow AND with nothing appended to the transcript since. Both halves are
// required — the clock alone would cut off a long think, while the file alone says nothing
// about whose turn it is. A transcript that cannot be stat'd is treated as NOT stale: an
// unreadable file is a reason to keep the last known reading, not to invent a new one.
func (cd *ClaudeDriver) pendingReplyStale(now time.Time) bool {
	if cd.state.LastUserAt.IsZero() || now.Sub(cd.state.LastUserAt) <= pendingReplyWindow {
		return false
	}
	info, err := os.Stat(cd.jsonlPath)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > pendingReplyWindow
}

// Update reads new JSONL lines and updates state.
func (cd *ClaudeDriver) Update() error {
	err := cd.reader.ReadNewFunc(func(row map[string]any) bool {
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
			cd.state.PendingTool = ""        // tool result arrived → no tool pending
			cd.state.LastMsgQuestion = false // you replied → the prior question is answered
			// An Esc at the prompt writes a user row like any other, and by row order alone it
			// reads as "the user just spoke, the agent must be thinking" — the exact opposite of
			// what happened. claude marks the row structurally (interruptedMessageId names the
			// assistant message that was cut off), so the fact is read rather than inferred from
			// the "[Request interrupted by user]" text it also carries; the text is a UI string
			// and would drift with wording or locale, the field is part of the record's shape.
			_, interruptedRow := row["interruptedMessageId"]
			cd.state.LastUserInterrupt = interruptedRow
			if interruptedRow {
				// Nothing is running and nothing is blocked: the CLI is back at an empty prompt
				// because you put it there. Idle, and NOT awaiting — a needs-you badge for an
				// interrupt you performed yourself is noise, and would page you about your own
				// keystroke.
				cd.state.Status = StatusIdle
			}
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
			cd.state.LastUserInterrupt = false // the agent spoke again; whatever was cut off is over
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
		cd.agentTree.scanRow(row, "", 0)
		return true
	})
	if err != nil {
		return err
	}
	cd.advanceAgentReaders()
	return nil
}

// AgentTree returns the current snapshot of the subagent ("Agent" tool) spawn
// tree parsed from this session's transcript — and, recursively, from each
// spawned agent's own transcript file (see claude_agent_tree.go for the
// schema this is based on). The result is a flat slice; reconstruct the tree
// via ParentID. Order is spawn-discovery order (stable across calls). Empty
// when no Agent tool has been used in this session.
func (cd *ClaudeDriver) AgentTree() []AgentNode {
	out := make([]AgentNode, 0, len(cd.agentTree.order))
	for _, id := range cd.agentTree.order {
		node := *cd.agentTree.nodes[id]
		for _, attempt := range cd.agentTree.attempts[id] {
			node.Attempts = append(node.Attempts, AgentAttempt{Sequence: attempt.sequence, StartedAt: attempt.startedAt, EndedAt: attempt.endedAt, Status: attempt.status})
		}
		node.Runtime = "claude"
		node.SourceRef = cd.agentTree.sessionDir
		if node.Diagnostic == "" {
			node.Diagnostic = "complete"
		}
		if node.ActiveSince.IsZero() {
			node.ActiveSince = node.StartedAt
		}
		out = append(out, node)
	}
	return out
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
	//
	// Waiting means BLOCKED: the CLI is modal and cannot proceed without you (an
	// elicitation tool's card, a permission prompt, an interrupted tool). A turn that
	// merely ENDED on a question mark is not blocked — the agent is sitting at an empty
	// prompt like after any other end_turn — so it stays Idle and rides AwaitingUser +
	// EndedOnQuestion instead. See AgentState.EndedOnQuestion for the evidence behind
	// that split; the short version is that the '?' heuristic produced every waiting in
	// the local corpus and most of them were closers like "需要我做什么？".
	if cd.state.LastUserAt.IsZero() && cd.state.LastAssistAt.IsZero() {
		// No JSONL data — agent just started, waiting for first prompt.
		s.Status = StatusIdle
		s.WaitReason = WaitNone
	} else if cd.state.StopReason == "tool_use" && isElicitationTool(cd.state.PendingTool) {
		s.Status = StatusWaiting
		s.WaitReason = WaitQuestion
	} else if s.Status != StatusWaiting && !cd.state.LastUserAt.IsZero() && cd.state.LastUserAt.After(cd.state.LastAssistAt) {
		// A user row newer than the agent's last word normally means "your prompt landed, the
		// reply is coming". Two cases where it does not, and both used to read as running:
		// you INTERRUPTED the turn (Esc — you stopped it, nothing is coming), or the reply
		// never arrived at all and never will (a killed CLI, a crashed session, a machine that
		// slept). Neither is an agent at work, so neither should hold a pane green.
		if cd.state.LastUserInterrupt || cd.pendingReplyStale(time.Now()) {
			s.Status = StatusIdle
		} else {
			s.Status = StatusRunning
		}
		s.WaitReason = WaitNone
	}
	// A finished main turn is NOT idle if it left background subagents still running: the agent
	// AS A WHOLE is still working (run_in_background Agents outlive the turn that spawned them),
	// so the pane must read as running — not a needs-you idle. Scoped to Idle only: a genuine
	// question / elicitation Waiting still blocks on the user regardless of subagents. anyRunning()
	// reflects the live subagent tree (spawns minus their <task-notification> completions), which
	// Update() refreshed just before State() was called. This also clears AwaitingUser (a Running
	// status is neither Waiting nor Idle), so no false needs-you dot while subagents churn.
	if s.Status == StatusIdle && cd.agentTree.anyRunning(time.Now()) {
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
		Tool:         ToolClaude,
		Model:        s.Model,
		Status:       s.Status,
		WaitReason:   s.WaitReason,
		AwaitingUser: awaiting,
		// Refines the needs-you signal without changing its severity: a turn that ended on a
		// question is still "your move", just phrased as a question rather than a report.
		EndedOnQuestion:   awaiting && s.LastMsgQuestion,
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
	// A completion has a shelf life: past it, the needs-you dot is withdrawn (the session and its
	// tail stay on the card — only the claim on your attention expires). Shared with the Codex
	// driver so "too old to be news" is ONE definition. See ExpireStaleAwaiting.
	return ExpireStaleAwaiting(as, time.Now())
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
