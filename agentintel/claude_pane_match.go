package agentintel

import (
	"regexp"
	"strings"
	"unicode"
)

// ── binding by what the pane actually shows ───────────────────────────────────────────────────
//
// This is the LAST resort in a three-step ladder, and it only ever runs where the previous two
// have nothing to say:
//
//	① identity   — claude's own PID→session record (ClaudeSessionForProcess). Exact, one file read.
//	② this file  — the pane's visible text vs each candidate transcript. Runs ONLY when ① missed
//	               AND two or more transcripts are still in play for one cwd, i.e. exactly the
//	               ambiguity that put a helper pane on the main session's transcript for ten hours.
//	③ mtime      — newest unclaimed file. A guess, and the thing ② exists to replace.
//
// Why it is a tiebreak and not the primary: what a pane SHOWS is rendered, what a transcript
// HOLDS is source. Between them sit line wrapping, markdown turned into ANSI, tool output folded
// into "⎿ +66 lines", and a status line that repaints every second. Matching is therefore a
// normalization problem, and normalization can only ever be approximate — while ① is a key
// lookup that cannot be approximately right. Two idle claude panes in one repo also LOOK nearly
// identical (empty prompt, model/cost line, permission hint); the distinguishing text is a line
// or two further up. So this is real evidence, but it is evidence that has to be handled
// carefully, not a stronger identity.
//
// The rule it enforces is therefore: answer only when the answer is unambiguous, and say nothing
// otherwise. A silent wrong binding is precisely the failure this whole ladder is repaying.

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// normalizeForMatch reduces text to the only thing both sides agree on: letters, digits and CJK,
// with everything else dropped.
//
// Every discarded class is one the two sides genuinely disagree about. Whitespace: the pane wraps
// and indents, the transcript does not. Punctuation and symbols: `**bold**` reaches the screen as
// ANSI with the asterisks gone, box-drawing and "…" exist only on screen. Case is kept — it costs
// nothing and adds signal for latin text.
func normalizeForMatch(s string) string {
	s = ansiSeq.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

const (
	// paneMatchChunk is how much normalized text is looked for. Long enough that a collision
	// between two sessions is not a real possibility, short enough to survive a message whose
	// beginning has scrolled off the top of the captured window.
	paneMatchChunk = 48
	// paneMatchMin is the shortest fingerprint that may decide anything. Below it a match is
	// plausibly coincidence ("好的" appears in every session), and a coincidence here is a wrong
	// binding — the exact failure being repaid.
	paneMatchMin = 16
	// paneMatchMessages is how many recent assistant turns are tried. The newest is the most
	// likely to still be on screen; going a little further back covers a pane whose last turn was
	// short or whose tail has scrolled.
	paneMatchMessages = 3
	// paneMatchTailBytes bounds the transcript read. A session file can be tens of megabytes and
	// only its end can possibly be on screen, so the whole file is never touched.
	paneMatchTailBytes = 96 << 10
)

// transcriptFingerprints returns up to paneMatchMessages normalized chunks taken from the END of
// the transcript's most recent assistant messages, newest first.
//
// The end of a message rather than its start: it is the part nearest the prompt, so it is the part
// still visible in a bounded pane capture.
func transcriptFingerprints(path string) []string {
	var texts []string
	r := NewJSONLReader(path)
	_ = r.ReadTailFunc(paneMatchTailBytes, func(row map[string]any) bool {
		if t, _ := row["type"].(string); t != "assistant" {
			return true
		}
		msg, ok := row["message"].(map[string]any)
		if !ok {
			return true
		}
		content, ok := msg["content"].([]any)
		if !ok {
			return true
		}
		var sb strings.Builder
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if bt, _ := block["type"].(string); bt != "text" {
				continue
			}
			if txt, _ := block["text"].(string); txt != "" {
				sb.WriteString(txt)
			}
		}
		if norm := normalizeForMatch(sb.String()); len(norm) >= paneMatchMin {
			texts = append(texts, norm)
		}
		return true
	})

	out := make([]string, 0, paneMatchMessages)
	for i := len(texts) - 1; i >= 0 && len(out) < paneMatchMessages; i-- {
		out = append(out, lastRunes(texts[i], paneMatchChunk))
	}
	return out
}

// lastRunes returns the final n runes of s (all of it when shorter).
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// matchPaneToTranscript picks the candidate whose recent output is visibly ON this pane.
//
// Returns "" for every uncertain outcome — nothing matched, or MORE THAN ONE matched. The second
// case is the important one: two candidates that both appear on one screen means the evidence
// cannot separate them, and picking either would be a coin flip dressed up as a decision. The
// caller then falls back to the mtime guess, which is no worse than before this existed.
func matchPaneToTranscript(paneLines []string, candidates []string) string {
	paneNorm := normalizeForMatch(strings.Join(paneLines, "\n"))
	if len(paneNorm) < paneMatchMin {
		return ""
	}
	found := ""
	for _, path := range candidates {
		matched := false
		for _, fp := range transcriptFingerprints(path) {
			if len(fp) >= paneMatchMin && strings.Contains(paneNorm, fp) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if found != "" {
			return "" // two sessions claim the same screen — refuse rather than guess
		}
		found = path
	}
	return found
}
