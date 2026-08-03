// Command pricecov reports WHY usage facts go unpriced.
//
// The report already tells you THAT a row is incomplete: `priced_requests` is below
// `requests`, so the money carries a「≈」and the reader knows it is a floor. What it
// cannot tell you is which of the several quite different causes is responsible, and
// they call for opposite responses:
//
//	price_rule_missing        — no rule for this model. Add one (catalog/table).
//	service_tier_missing      — the request never recorded HOW it was billed, so any
//	                            price would be a guess between standard and priority.
//	missing_model_or_timestamp— the transcript never named a model at all.
//	cache_write_ttl_unknown   — cache-write tokens with no TTL split; the 5m and 1h
//	                            rates differ enough that picking one invents money.
//
// Only the first is fixed by editing a price table. Diagnosing it by eye — "the
// number looks low" — is how a transcript-format gap gets mistaken for a missing
// price and answered by adding a rule that changes nothing.
//
// Usage:
//
//	go run ./scripts/diag/pricecov            # last 7 days, top models per cause
//	go run ./scripts/diag/pricecov -days 30
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/brightman-ai/kit/transcript"
	"github.com/brightman-ai/kit/usage"
)

// collect reads request facts straight from both transcript roots, the same scanners
// the server uses. It deliberately does NOT go through the server's cached dataset:
// a probe that shares the cache cannot tell a stale cache from a real gap.
func collect(since time.Time) []transcript.ModelRequestUsage {
	var facts []transcript.ModelRequestUsage
	keep := func(batch []transcript.ModelRequestUsage, err error) {
		if err != nil {
			return
		}
		for _, f := range batch {
			if f.At.After(since) {
				facts = append(facts, f)
			}
		}
	}
	for _, path := range transcript.NewestFiles(transcript.CodexSessionsRoot(), "", transcript.JSONLSuffix, 0) {
		keep(transcript.ScanCodexRequestUsage(path))
	}
	for _, path := range transcript.NewestFiles(transcript.ClaudeProjectsRoot(), "", transcript.JSONLSuffix, 0) {
		keep(transcript.ScanClaudeRequestUsage(path))
	}
	return facts
}

func main() {
	days := flag.Int("days", 7, "look back this many days")
	top := flag.Int("top", 5, "models to name per cause")
	flag.Parse()

	since := time.Now().AddDate(0, 0, -*days)
	facts := collect(since)
	if len(facts) == 0 {
		fmt.Fprintln(os.Stderr, "no request facts found")
		os.Exit(1)
	}

	type bucket struct {
		requests int
		tokens   int64
		byModel  map[string]int
	}
	causes := map[string]*bucket{}
	var priced, total int
	var pricedTokens, totalTokens int64

	for _, f := range facts {
		tokens := f.InputTokens + f.CachedInputTokens + f.OutputTokens +
			f.CacheWrite5mTokens + f.CacheWrite1hTokens + f.CacheWriteUnknownTokens
		total++
		totalTokens += tokens
		projection := usage.ProjectRequestCost(f)
		if projection.Complete {
			priced++
			pricedTokens += tokens
			continue
		}
		cause := "unknown"
		if len(projection.Diagnostics) > 0 {
			cause = projection.Diagnostics[0]
		}
		b := causes[cause]
		if b == nil {
			b = &bucket{byModel: map[string]int{}}
			causes[cause] = b
		}
		b.requests++
		b.tokens += tokens
		model := f.Model
		if model == "" {
			model = "(no model recorded)"
		}
		b.byModel[model+"  ["+f.Runtime+"]"]++
	}

	fmt.Printf("days=%d  facts=%d  priced=%d (%.1f%%)  tokens priced=%.1f%%\n\n",
		*days, total, priced, pct(priced, total), pctI64(pricedTokens, totalTokens))

	names := make([]string, 0, len(causes))
	for cause := range causes {
		names = append(names, cause)
	}
	sort.Slice(names, func(i, j int) bool { return causes[names[i]].requests > causes[names[j]].requests })

	for _, cause := range names {
		b := causes[cause]
		fmt.Printf("%-28s %6d requests (%.1f%%)  %s tokens\n", cause, b.requests, pct(b.requests, total), commas(b.tokens))
		for _, m := range topKeys(b.byModel, *top) {
			fmt.Printf("    %-46s %d\n", m.key, m.n)
		}
		fmt.Println()
	}
}

type kv struct {
	key string
	n   int
}

func topKeys(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n > out[j].n })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

func pctI64(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

func commas(v int64) string {
	s := fmt.Sprint(v)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, parts...), ",")
}
