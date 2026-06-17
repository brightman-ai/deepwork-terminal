package agentintel

import (
	"time"

	"github.com/brightman-ai/kit/pricing"
)

// CodexSessionMetricsForCWD locates the CURRENT Codex rollout for cwd and parses it
// into the SAME SessionMetrics shape as the Claude extractor — so the shared @ce
// OverviewPanel renders a Codex pane identically to a Claude one.
//
// Rollout selection: Codex nests rollouts by date and does NOT encode the project path
// into the directory name (unlike Claude). So we walk every rollout newest-first and pick
// the newest whose session_meta.cwd (or any turn_context.cwd) matches cwd. If none match
// (cwd unknown / older format), we fall back to the newest rollout overall — the live
// pane is almost always the most recently written file.
//
// Robustness contract (mirrors SessionMetricsForCWD): a missing ~/.codex/sessions, no
// rollout, malformed rows — none are errors. They yield a valid-but-empty SessionMetrics
// (turn_count 0, Group-B nil). The caller never branches on tool type.
func CodexSessionMetricsForCWD(pl *ProjectLocator, cwd, sessionID, title string, active bool) SessionMetrics {
	empty := SessionMetrics{
		Detail:  SessionDetail{ID: sessionID, Title: title, Active: active, EndedAt: nil},
		Summary: emptySummary(),
		Turns:   []TurnMetrics{},
	}

	path := newestCodexRolloutForCWD(pl, cwd)
	if path == "" {
		return empty
	}
	return parseCodexRollout(path, sessionID, title, active)
}

// CodexRolloutExistsForCWD reports whether a Codex rollout can be located for cwd. The
// overview handler uses it as the fallback signal: when the Claude extractor yields zero
// turns AND a codex rollout exists, it routes to codex even without the explicit tool param.
func CodexRolloutExistsForCWD(pl *ProjectLocator, cwd string) bool {
	return newestCodexRolloutForCWD(pl, cwd) != ""
}

// newestCodexRolloutForCWD returns the path of the rollout to read for cwd, or "".
// Preference: the newest rollout whose recorded cwd matches; else the newest rollout.
func newestCodexRolloutForCWD(pl *ProjectLocator, cwd string) string {
	files := pl.CodexSessionFiles() // newest-mtime first
	if len(files) == 0 {
		return ""
	}
	if cwd != "" {
		for _, f := range files {
			if rolloutCWD(f) == cwd {
				return f
			}
		}
	}
	return files[0]
}

// rolloutCWD cheaply extracts the cwd a rollout was recorded under. It scans only the
// leading rows (session_meta is the first line; turn_context follows) and stops early.
func rolloutCWD(path string) string {
	found := ""
	scanned := 0
	reader := NewJSONLReader(path)
	_ = reader.ReadNewFunc(func(row map[string]any) bool {
		scanned++
		if c := codexRowCWD(row); c != "" {
			found = c
			return false
		}
		return scanned < 50 // cwd appears in the first rows; bound the scan
	})
	return found
}

// codexRowCWD returns the cwd carried by a session_meta or turn_context row, else "".
func codexRowCWD(row map[string]any) string {
	rowType, _ := row["type"].(string)
	if rowType != "session_meta" && rowType != "turn_context" {
		return ""
	}
	payload, _ := row["payload"].(map[string]any)
	if payload == nil {
		return ""
	}
	if c, ok := payload["cwd"].(string); ok {
		return c
	}
	return ""
}

// codexCostState accumulates the session cost PER token_count event. Cost MUST be summed
// per-event (not from cumulative totals) because the long-context tier decision is
// per-request: gpt-5.x switches to the above-272k price once a single request's context
// exceeds the threshold. any is set once at least one priced (model-known) event landed —
// only then is total_cost emitted (honest, never a fabricated 0).
type codexCostState struct {
	total    float64
	currency string
	any      bool
}

// parseCodexRollout streams one Codex rollout into SessionMetrics.
//
// Turn model: Codex has no clean per-turn token attribution in the rollout (token_count
// events arrive asynchronously), so turns are delimited by the USER's response_item
// messages — each user message opens a turn that runs until the next user message. A turn
// carries its user_input snippet + duration (user→next user, or →session end). Per-turn
// tokens are left nil (honest "—") because the rollout doesn't attribute token_count
// events back to a specific user turn. The SUMMARY tokens/cost ARE real: summed from
// last_token_usage across every token_count event.
//
// Token math per token_count event (last_token_usage = THIS request):
//   - input_tokens INCLUDES cached_input_tokens (cached is a subset)
//   - output_tokens already includes reasoning_output_tokens
//   - non-cached input = input_tokens - cached_input_tokens
// Summary mapping:
//   - InputTokens       += non-cached input   (Σ)
//   - CacheReadTokens   += cached_input_tokens (Σ)
//   - OutputTokens      += output_tokens       (Σ)
//   - CacheCreateTokens  = 0 (OpenAI has no cache-write tier)
// Cost per event: pricing.Usage{Input: non-cached, Output: output, CacheRead: cached},
// pricing.Cost(model, u) → context = Input+CacheRead = input_tokens, so the >272k tier
// applies per request. Σ over events.
func parseCodexRollout(path, sessionID, title string, active bool) SessionMetrics {
	var turns []TurnMetrics
	var cur *turnAccum
	turnNo := 0

	var earliestTs, latestTs time.Time
	var modelID string
	cost := &codexCostState{}

	// Aggregate token sums (summary is the SSOT for tokens; turns carry only timing).
	sumInput, sumOutput, sumCacheRead := 0, 0, 0

	flush := func() {
		if cur == nil {
			return
		}
		turnNo++
		turns = append(turns, finalizeCodexTurn(turnNo, cur))
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
		payload, _ := row["payload"].(map[string]any)

		switch rowType {
		case "session_meta", "turn_context":
			// model lives on turn_context (session_meta carries only model_provider).
			if payload != nil {
				if m, ok := payload["model"].(string); ok && m != "" {
					modelID = m
				}
			}

		case "response_item":
			if payload == nil {
				break
			}
			// A user message opens a turn. developer/system seeds and assistant/tool
			// items are NOT turn-openers.
			itemType, _ := payload["type"].(string)
			role, _ := payload["role"].(string)
			if itemType == "message" && role == "user" {
				text := codexMessageText(payload)
				flush()
				cur = &turnAccum{
					userInput: truncate(text, userInputMax),
					userTs:    ts,
				}
			} else if cur != nil && !ts.IsZero() {
				// Assistant / tool activity extends the current turn's window.
				if cur.firstAssistTs.IsZero() || ts.Before(cur.firstAssistTs) {
					cur.firstAssistTs = ts
				}
				if ts.After(cur.lastAssistTs) {
					cur.lastAssistTs = ts
				}
				cur.hasAssist = true
			}

		case "event_msg":
			if payload == nil {
				break
			}
			if evType, _ := payload["type"].(string); evType != "token_count" {
				break
			}
			info, ok := payload["info"].(map[string]any)
			if !ok {
				break
			}
			last, ok := info["last_token_usage"].(map[string]any)
			if !ok {
				break
			}
			in := intFromAny(last["input_tokens"])
			cached := intFromAny(last["cached_input_tokens"])
			out := intFromAny(last["output_tokens"])
			nonCached := in - cached
			if nonCached < 0 {
				nonCached = 0
			}
			sumInput += nonCached
			sumCacheRead += cached
			sumOutput += out
			accumulateCodexCost(cost, modelID, nonCached, out, cached)
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

	summary := aggregateCodex(turns, earliestTs, sumInput, sumOutput, sumCacheRead)
	price := enrichCodexPricing(modelID, cost, &summary)
	return SessionMetrics{Detail: detail, Summary: summary, Turns: turns, Price: price}
}

// accumulateCodexCost prices ONE token_count event and folds it into cost. context =
// Input+CacheRead = input_tokens, so pricing.Cost picks the >272k tier per request.
// An unknown model → pricing.Cost ok=false → the event contributes nothing and cost.any
// stays as-is (honest: total_cost only emitted once a priced event lands).
func accumulateCodexCost(cost *codexCostState, model string, nonCachedInput, output, cached int) {
	u := pricing.Usage{
		Input:     nonCachedInput,
		Output:    output,
		CacheRead: cached,
	}
	if c, currency, ok := pricing.Cost(model, u); ok {
		cost.total += c
		cost.currency = currency
		cost.any = true
	}
}

// finalizeCodexTurn converts an accumulator into TurnMetrics. Token fields are nil
// (honest "—": the rollout doesn't attribute token_count events to a user turn).
// Durations mirror finalizeTurn: user→last-activity, with ttft / output window when
// the turn produced post-user activity.
func finalizeCodexTurn(n int, a *turnAccum) TurnMetrics {
	t := TurnMetrics{TurnNumber: n, UserInput: a.userInput}
	if a.hasAssist && !a.userTs.IsZero() {
		t.DurationMs = a.lastAssistTs.Sub(a.userTs).Milliseconds()
		ttft := a.firstAssistTs.Sub(a.userTs).Milliseconds()
		t.TtftMs = &ttft
		ow := a.lastAssistTs.Sub(a.firstAssistTs).Milliseconds()
		t.OutputWindowMs = &ow
	}
	return t
}

// aggregateCodex builds the summary. Token sums come from the token_count events (the
// SSOT — they're the only honest per-request attribution Codex gives us), NOT from the
// per-turn fields (which are nil). user_prompts = turns with real user text. Group-B and
// the tool_errors / output_window pointers stay nil (not derivable from the rollout).
func aggregateCodex(turns []TurnMetrics, earliest time.Time, sumInput, sumOutput, sumCacheRead int) SessionSummary {
	s := emptySummary()
	s.TurnCount = len(turns)
	s.StartedAt = rfc3339OrEmpty(earliest)

	for _, t := range turns {
		if trimmedNonEmpty(t.UserInput) {
			s.UserPrompts++
		}
		s.TotalDurationMs += t.DurationMs
	}
	s.InputTokens = sumInput
	s.OutputTokens = sumOutput
	s.CacheReadTokens = sumCacheRead
	s.CacheCreateTokens = 0
	return s
}

// enrichCodexPricing sets total_cost (Σ per-event tiered) + the model's reference unit
// price. total_cost is emitted only when a priced event landed. Unknown model → nil price
// (honest, never a fabricated 0). Mirrors enrichPricing.
func enrichCodexPricing(model string, cost *codexCostState, s *SessionSummary) *PriceJSON {
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

// codexMessageText joins the text from a Codex response_item message payload. Content is
// an array of blocks; each may carry "text". Non-text blocks (images, tool refs) are
// skipped. Returns "" when no text is present.
func codexMessageText(payload map[string]any) string {
	content, ok := payload["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, blk := range content {
		m, ok := blk.(map[string]any)
		if !ok {
			continue
		}
		if txt, _ := m["text"].(string); txt != "" {
			parts = append(parts, txt)
		}
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

// trimmedNonEmpty reports whether s has non-whitespace content.
func trimmedNonEmpty(s string) bool {
	return truncate(s, 1) != ""
}
