package agentintel

// CountedUsageKey deduplicates streaming token usage by (sessionID, messageID).
// Claude JSONL has 2-10 duplicate rows per message with cumulative usage values.
// We track max per key and only accumulate the delta.
type CountedUsageKey struct {
	SessionID string
	MessageID string
}

// UsageTotals holds token counts for a session or message.
type UsageTotals struct {
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheCreateTokens int
	TotalTokens       int
}

// UsageAccumulator deduplicates and aggregates token usage across messages.
type UsageAccumulator struct {
	byKey  map[CountedUsageKey]UsageTotals
	Totals UsageTotals
}

func NewUsageAccumulator() *UsageAccumulator {
	return &UsageAccumulator{
		byKey: make(map[CountedUsageKey]UsageTotals),
	}
}

// Ingest processes a usage entry and returns the delta added to totals.
// new_stored[field] = max(prev[field], current[field]); delta = new_stored - prev; totals += delta.
func (ua *UsageAccumulator) Ingest(key CountedUsageKey, current UsageTotals) UsageTotals {
	prev := ua.byKey[key]
	stored := UsageTotals{
		InputTokens:       maxInt(prev.InputTokens, current.InputTokens),
		OutputTokens:      maxInt(prev.OutputTokens, current.OutputTokens),
		CacheReadTokens:   maxInt(prev.CacheReadTokens, current.CacheReadTokens),
		CacheCreateTokens: maxInt(prev.CacheCreateTokens, current.CacheCreateTokens),
		TotalTokens:       maxInt(prev.TotalTokens, current.TotalTokens),
	}
	delta := UsageTotals{
		InputTokens:       stored.InputTokens - prev.InputTokens,
		OutputTokens:      stored.OutputTokens - prev.OutputTokens,
		CacheReadTokens:   stored.CacheReadTokens - prev.CacheReadTokens,
		CacheCreateTokens: stored.CacheCreateTokens - prev.CacheCreateTokens,
		TotalTokens:       stored.TotalTokens - prev.TotalTokens,
	}
	ua.byKey[key] = stored
	ua.Totals.InputTokens += delta.InputTokens
	ua.Totals.OutputTokens += delta.OutputTokens
	ua.Totals.CacheReadTokens += delta.CacheReadTokens
	ua.Totals.CacheCreateTokens += delta.CacheCreateTokens
	ua.Totals.TotalTokens += delta.TotalTokens
	return delta
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
