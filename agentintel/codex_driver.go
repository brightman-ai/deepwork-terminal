package agentintel

import (
	"os"
	"time"
)

// CodexSessionState tracks the state derived from Codex JSONL parsing.
type CodexSessionState struct {
	SessionID  string
	Model      string
	CWD        string
	Status     AgentStatus
	Awaiting   bool      // a turn completed (task_complete) and no new turn started since = needs-you
	LastTurnAt time.Time // transcript time of the last task_complete — the reload-proof "completed at" behind Awaiting
	// LastEventAt is the transcript time of the newest row of ANY kind. Running is asserted by
	// events (task_started / response_item) and only ever retracted by another event, so without
	// a "when was that asserted" there is no way to tell a turn in progress from a turn that
	// stopped existing — a killed CLI, an Esc, a machine that slept. See staleRun.
	LastEventAt  time.Time
	InputTokens  int
	OutputTokens int
	CachedTokens int
	TotalTokens  int
	UpdatedAt    time.Time
}

// CodexDriver parses a Codex CLI JSONL rollout file and derives session state.
type CodexDriver struct {
	jsonlPath string // kept for staleRun: "has anything been written since" needs the file, not just its parsed rows
	reader    *JSONLReader
	state     CodexSessionState
	agentTree codexAgentTree
}

func NewCodexDriver(jsonlPath string) *CodexDriver {
	return &CodexDriver{
		jsonlPath: jsonlPath,
		reader:    NewJSONLReader(jsonlPath),
		state:     CodexSessionState{Status: StatusIdle},
		agentTree: newCodexAgentTree(jsonlPath),
	}
}

// staleRun reports whether a Running verdict has outlived its evidence: the newest event is
// older than pendingReplyWindow AND nothing has been appended to the rollout since.
//
// Codex asserts Running on task_started / response_item and retracts it only on task_complete.
// That completion is a row that must be WRITTEN — so anything that stops the CLI before it
// (Esc, a crash, a killed pane, a laptop lid) leaves the last word as "running" and no one ever
// comes back to correct it. This is the same defect the claude driver had through a different
// rule, and it is bounded the same way: both halves must hold, because a genuinely working
// agent writes continuously and the clock alone would cut off a long tool call.
func (cd *CodexDriver) staleRun(now time.Time) bool {
	if cd.state.LastEventAt.IsZero() || now.Sub(cd.state.LastEventAt) <= pendingReplyWindow {
		return false
	}
	info, err := os.Stat(cd.jsonlPath)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > pendingReplyWindow
}

// Update reads new JSONL lines and updates state.
func (cd *CodexDriver) Update() error {
	err := cd.reader.ReadNewFunc(func(row map[string]any) bool {
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
			cd.state.SessionID = firstString(payload["session_id"], payload["id"], cd.state.SessionID)

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
				cd.state.InputTokens = intFromAny(firstNonNil(usage["input_tokens"], usage["input"]))
				cd.state.OutputTokens = intFromAny(firstNonNil(usage["output_tokens"], usage["output"]))
				cd.state.CachedTokens = intFromAny(firstNonNil(usage["cached_input_tokens"], usage["cached"]))
				cd.state.TotalTokens = intFromAny(firstNonNil(usage["total_tokens"], usage["total"]))
			}

		case "response_item":
			cd.state.Status = StatusRunning
			cd.state.Awaiting = false
		}

		if at := parseTime(row); at.After(cd.state.LastEventAt) {
			cd.state.LastEventAt = at
		}
		cd.state.UpdatedAt = time.Now()
		return true
	})
	if err != nil {
		return err
	}
	cd.agentTree.update(cd.state.SessionID, cd.state.Status == StatusRunning, cd.state.LastTurnAt)
	return nil
}

// State returns the current derived state.
func (cd *CodexDriver) State() CodexSessionState { return cd.state }

// AgentTree returns Codex CLI subagents projected onto the same runtime-neutral
// contract ClaudeDriver exposes. Parentage comes from child rollout metadata,
// never from description matching.
func (cd *CodexDriver) AgentTree() []AgentNode { return cd.agentTree.nodes() }

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
	// A turn that stopped without ever writing its completion is not a turn in progress. Idle
	// rather than Awaiting: nothing announced itself finished, so there is no result waiting to
	// be read, and raising a needs-you here would page the user about a session that simply died.
	if as.Status == StatusRunning && cd.staleRun(time.Now()) {
		as.Status = StatusIdle
		as.AwaitingUser = false
	}
	// Same shelf life as the Claude driver, from the same helper — see ExpireStaleAwaiting.
	return ExpireStaleAwaiting(as, time.Now())
}
