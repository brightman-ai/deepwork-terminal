package agentintel

import (
	"regexp"
	"strings"
	"unicode"
)

// PromptState indicates what the terminal output suggests about agent state.
type PromptState int

const (
	PromptUnknown         PromptState = iota
	PromptRunning                     // Active output / spinner detected
	PromptLikelyIdle                  // Prompt char detected but no confirming status line
	PromptIdle                        // Prompt char + confirming context (e.g. model name visible)
	PromptNeedsPermission             // [Y/n] or similar approval prompt
	PromptDone                        // Process exited
)

// spinnerChars is the set of Unicode spinner/progress rune values commonly
// used by AI CLI tools (braille spinner set).
const spinnerChars = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

// approvalPatterns matches common permission/approval prompts.
var approvalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\[Y/n\]`),
	regexp.MustCompile(`\[y/N\]`),
	regexp.MustCompile(`\(y/n\)`),
	regexp.MustCompile(`\(Y/N\)`),
	regexp.MustCompile(`\bAllow\b`),
	regexp.MustCompile(`\bApprove\b`),
}

// How far up the screen each family of prompt evidence is worth looking. They are DIFFERENT
// numbers on purpose — this is where "don't miss it" and "don't cry wolf" pull apart.
const (
	// promptTailLines bounds the one-liner patterns below (approval + interactive). They are
	// bottom-anchored by nature — "[Y/n]" renders ON the input line — and the vocabulary is
	// dangerously ordinary: `\bAllow\b` and `\bApprove\b` match "Allow me to explain" and
	// "Approve the PR" in any agent's prose. Scanning those wide would light up the amber dot
	// (and fire a notification) on text that is merely being written. So: stays narrow.
	promptTailLines = 5

	// menuScanLines bounds the numbered-menu scan. A RENDERED menu is tall, and Claude Code's
	// AskUserQuestion got taller still when it gained a preview panel: the option list ends up
	// above the preview box, "Notes: press n to add notes", the "Chat about this" line, the input
	// box and the bottom chrome. Measured on the reported pane: the options sat **18 lines above
	// the bottom**, so a 5-line window could not see them and the "confirm against the pane" veto
	// in paneDecision — which exists precisely to catch a blocked agent — never fired. The pane
	// stayed green while Claude waited for an answer.
	//
	// Widening is only safe because the menu rule carries its own discriminator (see
	// selectionCursorPattern): a numbered list Claude PRINTS has no selection cursor, a menu it is
	// DISPLAYING always does.
	menuScanLines = 24

	// paneScanLines is how many lines the tmux pane capture must fetch for the above to be
	// answerable. One constant, because the capture used to be 14 while the menu needs 24 — a cap
	// upstream of the analysis silently truncates the evidence the analysis is looking for.
	paneScanLines = menuScanLines
)

// choiceListPattern detects numbered choice prompts like Claude's plan mode:
//
//	"1. Yes, auto-accept edits"
//	"❯ 1. Yes, auto-accept edits"
//	"  2. Yes, manually approve edits"
//	"  3. Tell Claude what to change"
var choiceListPattern = regexp.MustCompile(`^\s*[❯›>]?\s*[1-9]\.\s+\S`)

// selectionCursorPattern matches a numbered item carrying a TUI selection cursor: "❯ 1. …".
//
// This is what separates a menu from a list. `choiceListPattern` alone answers "are there
// numbered items on screen", which over a 24-line window is also true of any plan, checklist or
// enumerated answer the agent just wrote — exactly the "crying wolf" failure the wider window
// would otherwise buy. A cursor is drawn by the widget rendering the menu; prose never has one.
//
// Deliberately NOT including '>': markdown blockquotes ("> 1. first step") are everywhere in
// agent output, and treating one as a live menu is the false positive this guard exists to stop.
var selectionCursorPattern = regexp.MustCompile(`^\s*[❯›]\s*[1-9]\.\s+\S`)

// interactivePromptPatterns detects text prompts that ask the user to make a decision.
var interactivePromptPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)would you like to proceed`),
	regexp.MustCompile(`(?i)ready to code\?`),
	regexp.MustCompile(`(?i)do you want to`),
	regexp.MustCompile(`(?i)press enter to`),
	regexp.MustCompile(`(?i)shift\+tab to approve`),
}

// modelPattern matches strings that look like a model name (word + digits + dots,
// e.g. "claude-4.6", "gpt-5.5", "gemini-2.0").
var modelPattern = regexp.MustCompile(`\b[a-zA-Z][\w-]*\d+[\w.-]*\d[\w.]*\b`)

// promptChars is the set of Unicode code points considered "prompt characters"
// that indicate an idle shell waiting for input.
var promptChars = map[rune]bool{
	'❯': true,
	'›': true,
	'>': true,
	'$': true,
	'%': true,
}

// OutputVerdict is AnalyzeOutput's answer WITH its provenance: which rule fired and the
// exact line that convinced it.
//
// The state alone is not diagnosable after the fact — six independent rules produce
// PromptNeedsPermission and the screen they read is overwritten seconds later, so a wrong
// verdict leaves no trace of its cause (see status_decision.go for the incident that made
// this necessary). Line is already scrubbed and truncated, so it is safe to log and to ship.
type OutputVerdict struct {
	State PromptState
	Rule  StatusRule
	Line  string
}

// AnalyzeOutput examines the last few lines of terminal output to detect prompt state.
// This is the LOWEST priority signal — only used when JSONL and process signals are
// ambiguous. Uses structural patterns only; no hardcoded tool-specific strings.
//
// Thin wrapper over AnalyzeOutputDetail: callers that only branch on the state keep reading
// as before, callers that must be able to explain the verdict take the detail.
func AnalyzeOutput(lines []string) PromptState { return AnalyzeOutputDetail(lines).State }

// AnalyzeOutputDetail is AnalyzeOutput plus the rule + matched line behind the answer.
// Identical classification — every return carries the rule that produced it and nothing more.
func AnalyzeOutputDetail(lines []string) OutputVerdict {
	if len(lines) == 0 {
		return OutputVerdict{State: PromptUnknown, Rule: RuleScreenQuiet}
	}

	// 1. One-liner approval / interactive prompts. Bottom-anchored — see promptTailLines for why
	//    this window stays narrow while the menu scan below does not.
	for _, line := range tailLines(lines, promptTailLines) {
		for _, pat := range approvalPatterns {
			if pat.MatchString(line) {
				return OutputVerdict{PromptNeedsPermission, RuleScreenApproval, truncEvidence(line)}
			}
		}
		for _, pat := range interactivePromptPatterns {
			if pat.MatchString(line) {
				return OutputVerdict{PromptNeedsPermission, RuleScreenInteractive, truncEvidence(line)}
			}
		}
	}

	// 1b. Numbered choice menu ("❯ 1. Yes, auto-accept" / "  2. Yes, manually").
	//
	// TWO conditions, and both carry weight:
	//   · ≥2 numbered items — one "1." is a sentence, two is a list. NOT required to be adjacent:
	//     a long option wraps, and Claude's preview layout puts the preview box's border between
	//     them, so "consecutive" would fail on exactly the screens this is for.
	//   · ≥1 of them under a selection cursor — the difference between a menu being DISPLAYED and
	//     a list being PRINTED. Without it, widening the window to 24 lines would classify every
	//     enumerated answer the agent writes as "waiting for you".
	choiceCount, cursorSeen := 0, false
	choiceLine := ""
	for _, line := range tailLines(lines, menuScanLines) {
		if !choiceListPattern.MatchString(line) {
			continue
		}
		choiceCount++
		if selectionCursorPattern.MatchString(line) {
			cursorSeen = true
			choiceLine = line // the cursor line is the best evidence: it names the menu AND proves it live
		} else if choiceLine == "" {
			choiceLine = line
		}
	}
	if choiceCount >= 2 && cursorSeen {
		return OutputVerdict{PromptNeedsPermission, RuleScreenChoiceList, truncEvidence(choiceLine)}
	}

	// 2. Check last line for spinner characters → running.
	lastLine := lines[len(lines)-1]
	for _, r := range lastLine {
		if strings.ContainsRune(spinnerChars, r) {
			return OutputVerdict{PromptRunning, RuleScreenSpinner, truncEvidence(lastLine)}
		}
	}

	// 3. Find last non-empty line.
	lastNonEmpty := ""
	lastNonEmptyIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			lastNonEmpty = trimmed
			lastNonEmptyIdx = i
			break
		}
	}
	if lastNonEmpty == "" {
		return OutputVerdict{State: PromptUnknown, Rule: RuleScreenQuiet}
	}

	// 4. Check if last non-empty line looks like a prompt (short, starts with prompt char).
	isPromptLine := isShellPrompt(lastNonEmpty)
	if !isPromptLine {
		return OutputVerdict{State: PromptUnknown, Rule: RuleScreenQuiet}
	}

	// 5. Try to upgrade to PromptIdle if second-to-last line contains a model-like pattern.
	if lastNonEmptyIdx > 0 {
		prevLine := strings.TrimSpace(lines[lastNonEmptyIdx-1])
		if prevLine != "" && modelPattern.MatchString(prevLine) {
			return OutputVerdict{PromptIdle, RuleScreenPromptIdle, truncEvidence(lastNonEmpty)}
		}
	}

	return OutputVerdict{PromptLikelyIdle, RuleScreenPromptLikely, truncEvidence(lastNonEmpty)}
}

// tailLines returns the last n lines (all of them when there are fewer). One helper so the two
// windows above are visibly the SAME operation with different bounds, not two open-coded slices
// that can drift apart.
func tailLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// isShellPrompt returns true if the line looks like a shell/CLI prompt:
// length ≤ 15, starts with a known prompt character followed by space or end-of-string.
func isShellPrompt(line string) bool {
	if len([]rune(line)) > 15 {
		return false
	}
	runes := []rune(line)
	if len(runes) == 0 {
		return false
	}
	first := runes[0]
	if !promptChars[first] {
		// Also accept lines composed entirely of ASCII punctuation + space, e.g. "$ " or "> ".
		if !unicode.IsPunct(rune(first)) && !unicode.IsSymbol(rune(first)) {
			return false
		}
	}
	// Must be followed by space or nothing.
	if len(runes) >= 2 && runes[1] != ' ' {
		return false
	}
	return true
}
