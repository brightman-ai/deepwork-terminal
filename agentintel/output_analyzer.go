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

// choiceListPattern detects numbered choice prompts like Claude's plan mode:
//   "1. Yes, auto-accept edits"
//   "❯ 1. Yes, auto-accept edits"
//   "  2. Yes, manually approve edits"
//   "  3. Tell Claude what to change"
var choiceListPattern = regexp.MustCompile(`^\s*[❯›>]?\s*[1-9]\.\s+\S`)

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

	// 1. Check last 5 lines for approval/permission/choice prompts.
	checkLines := lines
	if len(checkLines) > 5 {
		checkLines = lines[len(lines)-5:]
	}
	for _, line := range checkLines {
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

	// 1b. Check for numbered choice list (e.g., "1. Yes, auto-accept" / "2. Yes, manually").
	// Need ≥2 consecutive numbered items in last 5 lines to confirm it's a choice menu.
	choiceCount := 0
	choiceLine := ""
	for _, line := range checkLines {
		if choiceListPattern.MatchString(line) {
			choiceCount++
			if choiceLine == "" {
				choiceLine = line // the first item identifies the menu; the rest are its siblings
			}
		}
	}
	if choiceCount >= 2 {
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
