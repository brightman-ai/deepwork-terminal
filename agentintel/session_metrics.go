package agentintel

import (
	"strings"
	"time"
)

// SessionMetrics is the full overview payload for ONE agent session: a per-turn
// breakdown, an aggregate summary, and session-level detail. It feeds the shared
// @ce OverviewPanel. Group-B metrics (cost, agent/model calls, permission requests,
// tool-call categories) are NOT derivable from a Claude transcript and are left nil
// → serialized as JSON null → the UI renders "—".
type SessionMetrics struct {
	Detail  SessionDetail  `json:"detail"`
	Summary SessionSummary `json:"summary"`
	Turns   []TurnMetrics  `json:"turns"`
}

// SessionDetail is the session-level header. ended_at is always null (a live session
// has no end); active is passed in by the caller (is the agent currently running?).
type SessionDetail struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Active    bool    `json:"active"`
	TurnCount int     `json:"turn_count"`
	ModelID   string  `json:"model_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	EndedAt   *string `json:"ended_at"`
}

// SessionSummary aggregates every turn. Group-A fields (counts/tokens/durations) are
// real sums. tool_errors is nil when no tool_result carried is_error info. Group-B
// fields are always nil (transcript can't reveal them honestly).
type SessionSummary struct {
	UserPrompts       int    `json:"user_prompts"`
	TurnCount         int    `json:"turn_count"`
	StartedAt         string `json:"started_at"`
	TotalDurationMs   int64  `json:"total_duration_ms"`
	InputTokens       int    `json:"input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
	CacheReadTokens   int    `json:"cache_read_tokens"`
	CacheCreateTokens int    `json:"cache_create_tokens"`
	ToolCalls         int    `json:"tool_calls"`
	ToolErrors        *int   `json:"tool_errors"`
	OutputWindowMs    *int64 `json:"output_window_ms"`

	// Group B — not derivable from the transcript. Always null.
	ModelCalls          *int    `json:"model_calls"`
	AgentCalls          *int    `json:"agent_calls"`
	PermissionRequests  *int    `json:"permission_requests"`
	ToolCallsByCategory *string `json:"tool_calls_by_category"`
	TotalCost           *string `json:"total_cost"`
	Currency            *string `json:"currency"`
}

// TurnMetrics is one user→assistant cycle. A turn opens on a user text message and
// runs until the next user text message. Nullable pointer fields are nil when the
// turn produced no assistant activity (e.g. a trailing user prompt still in flight).
type TurnMetrics struct {
	TurnNumber        int     `json:"turn_number"`
	UserInput         string  `json:"user_input"`
	DurationMs        int64   `json:"duration_ms"`
	TtftMs            *int64  `json:"ttft_ms"`
	InputTokens       *int    `json:"input_tokens"`
	OutputTokens      *int    `json:"output_tokens"`
	CacheReadTokens   *int    `json:"cache_read_tokens"`
	CacheCreateTokens *int    `json:"cache_create_tokens"`
	ToolCalls         *int    `json:"tool_calls"`
	OutputWindowMs    *int64  `json:"output_window_ms"`
}

// userInputMax caps the user_input snippet we surface per turn. The OverviewPanel
// shows a glanceable label, not the full prompt.
const userInputMax = 200

// SessionMetricsForCWD locates the CURRENT Claude transcript for cwd (the most
// recently MODIFIED .jsonl in the project's transcript dir) and parses it into turns
// + summary + detail. sessionID and title are echoed into detail; active flags
// whether the agent is presently running.
//
// Robustness contract (mirrors recent_files.go): a missing transcript dir, a codex
// session with no Claude transcript, malformed rows — none are errors. They yield a
// valid-but-empty SessionMetrics (turn_count 0, Group-B nil). The caller never has to
// branch on tool type.
func SessionMetricsForCWD(pl *ProjectLocator, cwd, sessionID, title string, active bool) SessionMetrics {
	detail := SessionDetail{
		ID:        sessionID,
		Title:     title,
		Active:    active,
		TurnCount: 0,
		EndedAt:   nil,
	}
	sm := SessionMetrics{Detail: detail, Summary: emptySummary(), Turns: []TurnMetrics{}}

	path := newestClaudeTranscript(pl, cwd)
	if path == "" {
		return sm
	}
	return parseTranscript(path, sessionID, title, active)
}

// newestClaudeTranscript returns the most-recently-modified .jsonl for cwd, or "" if
// the project has no transcript dir / no transcripts. ClaudeSessionFiles already sorts
// newest-mtime-first, so the current conversation is simply files[0].
func newestClaudeTranscript(pl *ProjectLocator, cwd string) string {
	files, err := pl.ClaudeSessionFiles(cwd)
	if err != nil || len(files) == 0 {
		return ""
	}
	return files[0]
}

// turnAccum accumulates one turn while we stream rows. We hold timestamps as time.Time
// to compute durations precisely, then convert to ms at flush.
type turnAccum struct {
	userInput       string
	userTs          time.Time
	firstAssistTs   time.Time
	lastAssistTs    time.Time
	hasAssist       bool
	inputTokens     int
	outputTokens    int
	cacheReadTokens int
	cacheCreate     int
	toolCalls       int
	seenMsgIDs      map[string]bool // dedup usage per assistant message id
}

// parseTranscript streams one transcript into SessionMetrics. The row walk is the SSOT
// for turn delimitation:
//   - a "user" row WITH non-empty text content opens a new turn (flush the prior one);
//   - a "user" row that is ONLY tool_result blocks (no text) is the harness feeding a
//     tool result back — it stays inside the current turn, it does NOT open one;
//   - "assistant" rows accumulate into the open turn (usage summed, tool_use counted,
//     first/last assistant ts tracked).
func parseTranscript(path, sessionID, title string, active bool) SessionMetrics {
	var turns []TurnMetrics
	var cur *turnAccum
	turnNo := 0

	var earliestTs, latestTs time.Time
	var modelID string
	toolErrors := 0
	sawToolResult := false

	flush := func() {
		if cur == nil {
			return
		}
		turnNo++
		turns = append(turns, finalizeTurn(turnNo, cur))
		cur = nil
	}

	reader := NewJSONLReader(path)
	_ = reader.ReadNewFunc(func(row map[string]any) bool {
		rowType, _ := row["type"].(string)
		ts := parseTime(row)
		if !ts.IsZero() {
			if earliestTs.IsZero() || ts.Before(earliestTs) {
				earliestTs = ts
			}
			if ts.After(latestTs) {
				latestTs = ts
			}
		}

		msg, _ := row["message"].(map[string]any)

		switch rowType {
		case "user":
			text, isToolResultOnly, errs := inspectUserContent(msg)
			if errs > 0 {
				toolErrors += errs
				sawToolResult = true
			}
			// A pure tool_result echo belongs to the current turn.
			if isToolResultOnly {
				if cur != nil {
					// any tool_result presence means is_error info is derivable
					sawToolResult = true
				}
				return true
			}
			// Real user prompt → start a fresh turn.
			flush()
			cur = &turnAccum{
				userInput:  truncate(text, userInputMax),
				userTs:     ts,
				seenMsgIDs: map[string]bool{},
			}

		case "assistant":
			if cur == nil {
				// Assistant activity before any user prompt (rare: system seed). Open an
				// anonymous turn so its tokens/tools aren't lost.
				cur = &turnAccum{userTs: ts, seenMsgIDs: map[string]bool{}}
			}
			if !ts.IsZero() {
				if cur.firstAssistTs.IsZero() || ts.Before(cur.firstAssistTs) {
					cur.firstAssistTs = ts
				}
				if ts.After(cur.lastAssistTs) {
					cur.lastAssistTs = ts
				}
				cur.hasAssist = true
			}
			if msg == nil {
				return true
			}
			if m, ok := msg["model"].(string); ok && m != "" {
				modelID = m
			}
			accumulateAssistant(cur, msg)
		}
		return true
	})
	flush()

	if turns == nil {
		turns = []TurnMetrics{}
	}

	detail := SessionDetail{
		ID:        sessionID,
		Title:     title,
		Active:    active,
		TurnCount: len(turns),
		ModelID:   modelID,
		CreatedAt: rfc3339OrEmpty(earliestTs),
		UpdatedAt: rfc3339OrEmpty(latestTs),
		EndedAt:   nil,
	}
	summary := aggregate(turns, earliestTs, sawToolResult, toolErrors)
	return SessionMetrics{Detail: detail, Summary: summary, Turns: turns}
}

// accumulateAssistant folds one assistant message into the open turn: usage tokens
// (deduped per message id, mirroring claude_driver) and tool_use block counts.
func accumulateAssistant(cur *turnAccum, msg map[string]any) {
	// Usage — dedup by message id so a streamed message counted twice isn't doubled.
	msgID, _ := msg["id"].(string)
	if usageRaw, ok := msg["usage"].(map[string]any); ok {
		if msgID == "" || !cur.seenMsgIDs[msgID] {
			if msgID != "" {
				cur.seenMsgIDs[msgID] = true
			}
			cur.inputTokens += intFromAny(usageRaw["input_tokens"])
			cur.outputTokens += intFromAny(usageRaw["output_tokens"])
			cur.cacheReadTokens += intFromAny(usageRaw["cache_read_input_tokens"])
			cur.cacheCreate += intFromAny(usageRaw["cache_creation_input_tokens"])
		}
	}
	// Tool-use blocks.
	if content, ok := msg["content"].([]any); ok {
		for _, blk := range content {
			m, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "tool_use" {
				cur.toolCalls++
			}
		}
	}
}

// inspectUserContent classifies a user message's content. It returns the joined text
// (empty for a tool-result-only echo), whether the message is ONLY tool_result blocks,
// and how many of those tool_results carried is_error=true.
//
// Content may be a plain string (text prompt) or an array of blocks (text / tool_result).
func inspectUserContent(msg map[string]any) (text string, toolResultOnly bool, errCount int) {
	if msg == nil {
		return "", false, 0
	}
	switch c := msg["content"].(type) {
	case string:
		return c, false, 0
	case []any:
		var sb strings.Builder
		hasText := false
		hasToolResult := false
		for _, blk := range c {
			m, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			switch bt, _ := m["type"].(string); bt {
			case "text":
				if t, _ := m["text"].(string); t != "" {
					if hasText {
						sb.WriteByte('\n')
					}
					sb.WriteString(t)
					hasText = true
				}
			case "tool_result":
				hasToolResult = true
				if isErr, _ := m["is_error"].(bool); isErr {
					errCount++
				}
			}
		}
		txt := strings.TrimSpace(sb.String())
		// Tool-result-only when there are tool_results and no user text.
		return txt, hasToolResult && txt == "", errCount
	}
	return "", false, 0
}

// finalizeTurn converts an accumulator into the public TurnMetrics. Durations:
//   - duration_ms   = last assistant ts − user ts (0 if no assistant activity yet)
//   - ttft_ms       = first assistant ts − user ts (nil if no assistant yet)
//   - output_window = last assistant ts − first assistant ts (nil if no assistant)
//
// Token / tool fields are nil when the turn had no assistant message — honest "—"
// rather than a misleading 0.
func finalizeTurn(n int, a *turnAccum) TurnMetrics {
	t := TurnMetrics{
		TurnNumber: n,
		UserInput:  a.userInput,
	}
	if a.hasAssist {
		if !a.userTs.IsZero() {
			t.DurationMs = a.lastAssistTs.Sub(a.userTs).Milliseconds()
			ttft := a.firstAssistTs.Sub(a.userTs).Milliseconds()
			t.TtftMs = &ttft
		}
		ow := a.lastAssistTs.Sub(a.firstAssistTs).Milliseconds()
		t.OutputWindowMs = &ow
		it, ot := a.inputTokens, a.outputTokens
		cr, cc := a.cacheReadTokens, a.cacheCreate
		tc := a.toolCalls
		t.InputTokens, t.OutputTokens = &it, &ot
		t.CacheReadTokens, t.CacheCreateTokens = &cr, &cc
		t.ToolCalls = &tc
	}
	return t
}

// aggregate sums finalized turns into the summary. user_prompts counts turns that
// carried real user text. tool_errors is nil unless at least one tool_result with
// is_error info was seen. output_window_ms is the Σ of per-turn windows (nil if no
// turn had assistant activity).
func aggregate(turns []TurnMetrics, earliest time.Time, sawToolResult bool, toolErrors int) SessionSummary {
	s := emptySummary()
	s.TurnCount = len(turns)
	s.StartedAt = rfc3339OrEmpty(earliest)

	var owSum int64
	anyWindow := false
	for _, t := range turns {
		if strings.TrimSpace(t.UserInput) != "" {
			s.UserPrompts++
		}
		s.TotalDurationMs += t.DurationMs
		if t.InputTokens != nil {
			s.InputTokens += *t.InputTokens
		}
		if t.OutputTokens != nil {
			s.OutputTokens += *t.OutputTokens
		}
		if t.CacheReadTokens != nil {
			s.CacheReadTokens += *t.CacheReadTokens
		}
		if t.CacheCreateTokens != nil {
			s.CacheCreateTokens += *t.CacheCreateTokens
		}
		if t.ToolCalls != nil {
			s.ToolCalls += *t.ToolCalls
		}
		if t.OutputWindowMs != nil {
			owSum += *t.OutputWindowMs
			anyWindow = true
		}
	}
	if anyWindow {
		s.OutputWindowMs = &owSum
	}
	if sawToolResult {
		te := toolErrors
		s.ToolErrors = &te
	}
	return s
}

// emptySummary returns a summary with all Group-A counters zeroed and all Group-B
// (and the nullable Group-A) pointers nil — the honest "no data" shape.
func emptySummary() SessionSummary {
	return SessionSummary{
		ToolErrors:          nil,
		OutputWindowMs:      nil,
		ModelCalls:          nil,
		AgentCalls:          nil,
		PermissionRequests:  nil,
		ToolCallsByCategory: nil,
		TotalCost:           nil,
		Currency:            nil,
	}
}

// truncate clips s to max runes, appending an ellipsis when cut. Operates on runes so
// a multibyte boundary is never split.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// rfc3339OrEmpty formats t as RFC3339, or "" when t is the zero value.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
