package agentintel

import "strings"

// stripAgentChrome removes an agent TUI's persistent bottom chrome (input box, status/mode
// lines, token counter, "Debug" hint, custom status line, trailing padding) from a raw
// capture-pane screen so the Agent Overview's live tail shows the agent's REAL recent output
// instead of a static frame. It is per-agent by design (request B): each tool's frame is
// stripped by its own known layout, and anything unrecognized (bare shell, gemini, opencode,
// or a tool whose frame isn't present) degrades to the raw content with only trailing blanks
// trimmed — never mis-stripped, never errored (B-3).
//
// The returned slice is the content ABOVE the chrome (trailing blanks trimmed). When the whole
// screen was chrome it returns an empty slice, so the card shows "(no recent output)" rather
// than token/border residue (B-4). Callers still cap to the last N lines.
func stripAgentChrome(lines []string, tool AgentTool) []string {
	switch tool {
	case ToolClaude:
		return stripClaudeChrome(lines)
	case ToolCodex:
		return stripCodexChrome(lines)
	default:
		// Unknown agent / bare shell: no known frame to strip — return content untouched
		// (only trailing padding removed). This is the B-3 degradation path.
		return trimTrailingBlank(lines)
	}
}

// stripClaudeChrome strips Claude Code's bottom frame. Claude pins a fixed frame to the bottom
// of the screen in every state (thinking / streaming output / waiting for input):
//
//	<real conversation output>            ← what we want to keep
//	────────────────────────────────────  ← input box TOP border  (pure U+2500 rule)
//	❯ <queued input, may be multi-line>   ← input line(s)
//	────────────────────────────────────  ← input box BOTTOM border
//	  🤖 <model> | 💰 <cost> | 🧠 <ctx>    ← status line
//	  ⏵⏵ <permission mode> …               ← mode line
//	              NNNNNN tokens · …         ← token counter (right-aligned)
//	      globalVersion … latestVersion …   ← optional update notice
//	                            Debug        ← debug hint
//	  ● <branch>  /  ◯ <subagent> …          ← optional custom status line
//
// The input box is always the bottom-most pair of full-width horizontal rules, and everything
// from its TOP border down is chrome. Real conversation output never contains a full-width pure
// rule, so anchoring on that border pair is robust across terminal heights and agent states.
// If no such border exists the screen isn't a recognizable Claude frame (e.g. the active pane
// is really a shell) → return the content untouched.
func stripClaudeChrome(raw []string) []string {
	lines := trimTrailingBlank(raw)
	if len(lines) == 0 {
		return nil
	}
	borders := make([]int, 0, 2)
	for i, l := range lines {
		if isBoxBorderLine(l) {
			borders = append(borders, i)
		}
	}
	if len(borders) == 0 {
		// No Claude input box on screen → not a Claude frame; leave the tail raw (B-3).
		return lines
	}
	// The input box is the bottom-most border pair; chrome begins at its TOP border. With a
	// single visible border (bottom scrolled off) fall back to cutting from that one border.
	chromeStart := borders[len(borders)-1]
	if len(borders) >= 2 {
		chromeStart = borders[len(borders)-2]
	}
	return trimTrailingBlank(lines[:chromeStart])
}

// stripCodexChrome strips Codex's bottom TUI chrome. PENDING A REAL SAMPLE: no live Codex
// session existed when this landed and the handoff forbids guessing Codex's frame layout, so it
// currently degrades to a safe no-op (trailing blanks trimmed) — Codex cards show the raw tail
// rather than a mis-stripped one. Plug the Codex-specific border/status rules in here once a
// capture fixture exists (mirror stripClaudeChrome; add a testdata/codex_*.txt fixture + case).
func stripCodexChrome(raw []string) []string {
	return trimTrailingBlank(raw)
}

// isBoxBorderLine reports whether a line is one of Claude's input-box border rules: a run of
// only U+2500 ("─") box-drawing characters (surrounding whitespace ignored). The width gate
// keeps a stray short "─" decoration from being mistaken for a full-width box border.
func isBoxBorderLine(s string) bool {
	const minBorderRunes = 10
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	n := 0
	for _, r := range trimmed {
		if r != '─' {
			return false
		}
		n++
	}
	return n >= minBorderRunes
}

// trimTrailingBlank drops trailing whitespace-only lines so a card never shows empty padding
// under short output. It returns a slice header over the input (no copy).
func trimTrailingBlank(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}
