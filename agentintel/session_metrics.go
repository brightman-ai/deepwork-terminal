package agentintel

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/brightman-ai/kit/pricing"
)

// SessionMetrics is the full overview payload for ONE agent session: a per-turn
// breakdown, an aggregate summary, and session-level detail. It feeds the shared
// @ce OverviewPanel. Group-B metrics (agent/model calls, permission requests,
// tool-call categories) are NOT derivable from a Claude transcript and are left nil
// → serialized as JSON null → the UI renders "—". Cost is the exception: it IS
// derivable (summed tokens × kit/pricing) so summary.total_cost is computed here.
//
// Price is the current model's reference UNIT price (per-MTok), looked up once from
// the kit/pricing SSOT. It is nil (omitted) when the model is unknown to the table —
// never a fabricated 0.
type SessionMetrics struct {
	Detail  SessionDetail  `json:"detail"`
	Summary SessionSummary `json:"summary"`
	Turns   []TurnMetrics  `json:"turns"`
	Price   *PriceJSON     `json:"price,omitempty"`
}

// PriceJSON is the current model's reference unit price, in currency per MILLION
// tokens, mirrored from pricing.ModelPrice's BASE Tier. cache_write_5m / cache_write_1h
// are 0 for providers without a cache-write tier (OpenAI, Gemini). context_threshold is
// the long-context premium boundary in tokens (0 = no tier). It is only emitted when the
// model resolves in the kit/pricing table.
type PriceJSON struct {
	Input            float64 `json:"input"`
	Output           float64 `json:"output"`
	CacheRead        float64 `json:"cache_read"`
	CacheWrite5m     float64 `json:"cache_write_5m"`
	CacheWrite1h     float64 `json:"cache_write_1h"`
	Currency         string  `json:"currency"`
	ContextThreshold int     `json:"context_threshold,omitempty"`
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

	// Cost IS derivable: summed tokens × kit/pricing for the session model. nil when
	// the model is unknown to the price table (never a fabricated 0). currency is
	// omitted when there is no cost.
	TotalCost *float64 `json:"total_cost"`
	Currency  string   `json:"currency,omitempty"`
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
	// Incremental cache: first open parses the file, later opens ingest only newly-appended
	// rows, so re-opening the overview as the session grows is O(new bytes), not O(file).
	return overviewMetricsCache.metricsFor(path, sessionID, title, active, time.Now())
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

// costState accumulates the session cost PER ASSISTANT MESSAGE. Cost MUST be summed
// per-request (not from session-aggregate tokens) because the context-tier decision is
// per-request and cache-write splits into 5m/1h TTLs at different prices. seenMsgIDs is
// session-scoped so a streamed message counted twice (or echoed across turns) is never
// double-charged. any is set once at least one priced (model-known) message was seen —
// only then does enrichPricing emit total_cost (honest, never a fabricated 0).
type costState struct {
	total      float64
	currency   string
	any        bool
	seenMsgIDs map[string]bool
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

// SessionSummaryForPath parses a KNOWN transcript path into its summary, dispatching
// by tool. The single path-based resolver: a caller that already resolved a pane's
// transcript path must use this rather than re-resolving "newest by cwd" (which
// drifts when several sessions share a directory). Zero summary on empty input.
func SessionSummaryForPath(path, tool string) SessionSummary {
	if path == "" {
		return SessionSummary{}
	}
	if tool == "codex" {
		return parseCodexRollout(path, "", "", false).Summary
	}
	return parseTranscript(path, "", "", false).Summary
}

// parseTranscript streams one transcript into SessionMetrics. The row walk is the SSOT
// for turn delimitation:
//   - a "user" row WITH non-empty text content opens a new turn (flush the prior one);
//   - a "user" row that is ONLY tool_result blocks (no text) is the harness feeding a
//     tool result back — it stays inside the current turn, it does NOT open one;
//   - "assistant" rows accumulate into the open turn (usage summed, tool_use counted,
//     first/last assistant ts tracked).
// transcriptAccum is the incremental parse state for ONE transcript: the per-turn walk
// (finalized turns + the in-progress cur), aggregate counters, and a persistent reader whose
// offset advances across calls. update() ingests only NEW rows; snapshot() renders metrics
// non-destructively (the in-progress turn shows but is NOT finalized, so the next update keeps
// appending to it). One-shot parseTranscript and the cached overview path share this code.
type transcriptAccum struct {
	mu     sync.Mutex
	reader *JSONLReader

	turns      []TurnMetrics
	cur        *turnAccum
	turnNo     int
	earliestTs time.Time
	latestTs   time.Time
	modelID    string
	toolErrors int
	sawToolRes bool
	cost       *costState

	lastAccess time.Time
}

func newTranscriptAccum(path string) *transcriptAccum {
	return &transcriptAccum{reader: NewJSONLReader(path), cost: &costState{seenMsgIDs: map[string]bool{}}}
}

func (a *transcriptAccum) flush() {
	if a.cur == nil {
		return
	}
	a.turnNo++
	a.turns = append(a.turns, finalizeTurn(a.turnNo, a.cur))
	a.cur = nil
}

// ingest folds ONE row into the running state (the SSOT turn-delimitation walk).
func (a *transcriptAccum) ingest(row map[string]any) bool {
	rowType, _ := row["type"].(string)
	ts := parseTime(row)
	if !ts.IsZero() {
		if a.earliestTs.IsZero() || ts.Before(a.earliestTs) {
			a.earliestTs = ts
		}
		if ts.After(a.latestTs) {
			a.latestTs = ts
		}
	}

	msg, _ := row["message"].(map[string]any)

	switch rowType {
	case "user":
		text, isToolResultOnly, errs := inspectUserContent(msg)
		if errs > 0 {
			a.toolErrors += errs
			a.sawToolRes = true
		}
		// A pure tool_result echo belongs to the current turn.
		if isToolResultOnly {
			if a.cur != nil {
				a.sawToolRes = true
			}
			return true
		}
		// Real user prompt → start a fresh turn.
		a.flush()
		a.cur = &turnAccum{
			userInput:  truncate(text, userInputMax),
			userTs:     ts,
			seenMsgIDs: map[string]bool{},
		}

	case "assistant":
		if a.cur == nil {
			// Assistant activity before any user prompt (rare: system seed). Open an
			// anonymous turn so its tokens/tools aren't lost.
			a.cur = &turnAccum{userTs: ts, seenMsgIDs: map[string]bool{}}
		}
		if !ts.IsZero() {
			if a.cur.firstAssistTs.IsZero() || ts.Before(a.cur.firstAssistTs) {
				a.cur.firstAssistTs = ts
			}
			if ts.After(a.cur.lastAssistTs) {
				a.cur.lastAssistTs = ts
			}
			a.cur.hasAssist = true
		}
		if msg == nil {
			return true
		}
		if m, ok := msg["model"].(string); ok && m != "" {
			a.modelID = m
		}
		accumulateAssistant(a.cur, msg, a.cost)
	}
	return true
}

// update ingests rows appended since the last call (O(new bytes)). A truncated/replaced file
// (size < the reader offset) resets the accumulator so we never double-count or serve stale.
func (a *transcriptAccum) update() {
	if info, err := os.Stat(a.reader.path); err == nil && info.Size() < a.reader.offset {
		a.reset()
	}
	_ = a.reader.ReadNewFunc(a.ingest)
}

func (a *transcriptAccum) reset() {
	a.reader.offset = 0
	a.turns = nil
	a.cur = nil
	a.turnNo = 0
	a.earliestTs = time.Time{}
	a.latestTs = time.Time{}
	a.modelID = ""
	a.toolErrors = 0
	a.sawToolRes = false
	a.cost = &costState{seenMsgIDs: map[string]bool{}}
}

// snapshot renders metrics for the rows ingested so far WITHOUT consuming the in-progress
// turn. The active turn shows provisionally (same number flush would assign); cost is walk-
// level (accumulated per assistant message) so it already includes the in-progress turn.
func (a *transcriptAccum) snapshot(sessionID, title string, active bool) SessionMetrics {
	turns := a.turns
	if a.cur != nil {
		// Full slice expression forces copy-on-append so a.turns' backing array is untouched.
		turns = append(a.turns[:len(a.turns):len(a.turns)], finalizeTurn(a.turnNo+1, a.cur))
	}
	if turns == nil {
		turns = []TurnMetrics{}
	}
	detail := SessionDetail{
		ID:        sessionID,
		Title:     title,
		Active:    active,
		TurnCount: len(turns),
		ModelID:   a.modelID,
		CreatedAt: rfc3339OrEmpty(a.earliestTs),
		UpdatedAt: rfc3339OrEmpty(a.latestTs),
		EndedAt:   nil,
	}
	summary := aggregate(turns, a.earliestTs, a.sawToolRes, a.toolErrors)
	// Cost (Σ per-message tiered, accumulated during the walk) + the model's reference unit
	// price. Unknown model → no priced message → total_cost nil (honest, never a fabricated 0).
	price := enrichPricing(a.modelID, a.cost, &summary)
	return SessionMetrics{Detail: detail, Summary: summary, Turns: turns, Price: price}
}

func parseTranscript(path, sessionID, title string, active bool) SessionMetrics {
	a := newTranscriptAccum(path)
	a.update()
	return a.snapshot(sessionID, title, active)
}

// maxMetricsAccums bounds the overview metrics cache (one accum per active transcript). LRU
// by last access — a few concurrently-watched sessions, not the whole history.
const maxMetricsAccums = 8

type metricsCache struct {
	mu sync.Mutex
	m  map[string]*transcriptAccum
}

var overviewMetricsCache = &metricsCache{m: make(map[string]*transcriptAccum)}

// metricsFor returns incrementally-updated metrics for a transcript path: the first call
// parses the file, later calls ingest only newly-appended rows, so re-opening the overview
// while a session grows is O(new bytes), not O(file). Truncation/rotation is handled in update.
func (c *metricsCache) metricsFor(path, sessionID, title string, active bool, now time.Time) SessionMetrics {
	c.mu.Lock()
	a := c.m[path]
	if a == nil {
		if len(c.m) >= maxMetricsAccums {
			c.evictOldestLocked()
		}
		a = newTranscriptAccum(path)
		c.m[path] = a
	}
	a.lastAccess = now
	c.mu.Unlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.update()
	return a.snapshot(sessionID, title, active)
}

func (c *metricsCache) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for k, a := range c.m {
		if oldestKey == "" || a.lastAccess.Before(oldest) {
			oldestKey, oldest = k, a.lastAccess
		}
	}
	if oldestKey != "" {
		delete(c.m, oldestKey)
	}
}

// accumulateAssistant folds one assistant message into the open turn: usage tokens
// (deduped per message id, mirroring claude_driver), per-message tiered cost (cost),
// and tool_use block counts.
func accumulateAssistant(cur *turnAccum, msg map[string]any, cost *costState) {
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
		// Cost is computed PER MESSAGE (the context-tier decision is per-request) and
		// summed. Deduped by the same message id (session-scoped) so a streamed message
		// isn't double-charged. model is THIS message's model (cost mixes models honestly).
		if msgID == "" || !cost.seenMsgIDs[msgID] {
			if msgID != "" {
				cost.seenMsgIDs[msgID] = true
			}
			model, _ := msg["model"].(string)
			accumulateCost(cost, model, usageRaw)
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

// accumulateCost prices ONE assistant message and folds it into cost. It builds a
// per-message pricing.Usage and calls pricing.Cost (which picks the context tier per
// request). The cache-write split:
//   - usage.cache_creation (object) present → CacheWrite5m = ephemeral_5m_input_tokens,
//     CacheWrite1h = ephemeral_1h_input_tokens (the precise per-TTL breakdown);
//   - else → fall back to usage.cache_creation_input_tokens as all-5m (legacy shape).
//
// An unknown model → pricing.Cost ok=false → the message contributes nothing and
// cost.any stays as-is (honest: total_cost is only emitted once a priced message lands).
func accumulateCost(cost *costState, model string, usageRaw map[string]any) {
	u := pricing.Usage{
		Input:     intFromAny(usageRaw["input_tokens"]),
		Output:    intFromAny(usageRaw["output_tokens"]),
		CacheRead: intFromAny(usageRaw["cache_read_input_tokens"]),
	}
	if cc, ok := usageRaw["cache_creation"].(map[string]any); ok {
		u.CacheWrite5m = intFromAny(cc["ephemeral_5m_input_tokens"])
		u.CacheWrite1h = intFromAny(cc["ephemeral_1h_input_tokens"])
	} else {
		u.CacheWrite5m = intFromAny(usageRaw["cache_creation_input_tokens"])
	}
	if c, currency, ok := pricing.Cost(model, u); ok {
		cost.total += c
		cost.currency = currency
		cost.any = true
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
		Currency:            "",
	}
}

// enrichPricing sets the session cost (Σ per-message tiered, accumulated during the
// walk into cost) and looks up the model's reference unit price. model is the latest
// assistant model id from the transcript (used only for the reference price row).
//
// total_cost is emitted only when at least one priced (model-known) message was seen —
// the honest "no price data" shape, never a fabricated 0. An empty model (or one unknown
// to the price table) returns a nil price.
func enrichPricing(model string, cost *costState, s *SessionSummary) *PriceJSON {
	if cost != nil && cost.any {
		total := cost.total
		s.TotalCost = &total
		s.Currency = cost.currency
	}
	if model == "" {
		return nil
	}
	if p, ok := pricing.Lookup(model); ok {
		return &PriceJSON{
			Input:            p.InputPerM,
			Output:           p.OutputPerM,
			CacheRead:        p.CacheReadPerM,
			CacheWrite5m:     p.CacheWrite5mPerM,
			CacheWrite1h:     p.CacheWrite1hPerM,
			Currency:         p.Currency,
			ContextThreshold: p.ContextThreshold,
		}
	}
	return nil
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
