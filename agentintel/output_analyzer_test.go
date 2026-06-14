package agentintel

import "testing"

func TestAnalyzeOutput_Empty(t *testing.T) {
	if got := AnalyzeOutput(nil); got != PromptUnknown {
		t.Errorf("nil → %v, want PromptUnknown", got)
	}
	if got := AnalyzeOutput([]string{}); got != PromptUnknown {
		t.Errorf("empty → %v, want PromptUnknown", got)
	}
}

func TestAnalyzeOutput_NeedsPermission(t *testing.T) {
	cases := []struct {
		lines []string
		desc  string
	}{
		{[]string{"Do you want to proceed? [Y/n]"}, "[Y/n]"},
		{[]string{"overwrite file? [y/N]"}, "[y/N]"},
		{[]string{"some output", "Allow this action?"}, "Allow"},
		{[]string{"lots", "of", "output", "Approve the change?"}, "Approve"},
		{[]string{"(y/n)"}, "(y/n)"},
	}
	for _, tc := range cases {
		got := AnalyzeOutput(tc.lines)
		if got != PromptNeedsPermission {
			t.Errorf("%s: got %v, want PromptNeedsPermission", tc.desc, got)
		}
	}
}

func TestAnalyzeOutput_Running(t *testing.T) {
	// Spinner chars in last line → running.
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for _, sp := range spinners {
		lines := []string{"previous output", sp + " Thinking..."}
		got := AnalyzeOutput(lines)
		if got != PromptRunning {
			t.Errorf("spinner %q: got %v, want PromptRunning", sp, got)
		}
	}
}

func TestAnalyzeOutput_LikelyIdle(t *testing.T) {
	// Prompt char at end of output, no model name in previous line.
	cases := []struct {
		lines []string
		desc  string
	}{
		{[]string{"some output", "❯"}, "❯ alone"},
		{[]string{"some output", "❯ "}, "❯ with space"},
		{[]string{"output", "> "}, "> prompt"},
		{[]string{"output", "$ "}, "$ prompt"},
		{[]string{"output", "% "}, "% prompt"},
	}
	for _, tc := range cases {
		got := AnalyzeOutput(tc.lines)
		if got != PromptLikelyIdle {
			t.Errorf("%s: got %v, want PromptLikelyIdle", tc.desc, got)
		}
	}
}

func TestAnalyzeOutput_Idle(t *testing.T) {
	// Prompt char + model name in previous line → PromptIdle.
	cases := []struct {
		lines []string
		desc  string
	}{
		{[]string{"claude-4.6 (opus)", "❯ "}, "claude-4.6 model line"},
		{[]string{"gpt-5.5", "$ "}, "gpt-5.5 model line"},
		{[]string{"gemini-2.0-flash", "> "}, "gemini model"},
		{[]string{"opencode gemini-2.0", "❯ "}, "opencode model"},
	}
	for _, tc := range cases {
		got := AnalyzeOutput(tc.lines)
		if got != PromptIdle {
			t.Errorf("%s: got %v, want PromptIdle", tc.desc, got)
		}
	}
}

func TestAnalyzeOutput_Unknown(t *testing.T) {
	cases := []struct {
		lines []string
		desc  string
	}{
		{[]string{"Reading 100 files..."}, "long active line"},
		{[]string{"Processing large batch of work items..."}, "long line no prompt"},
		{[]string{"intermediate line", "another long output line here more text"}, "no prompt char"},
	}
	for _, tc := range cases {
		got := AnalyzeOutput(tc.lines)
		if got != PromptUnknown {
			t.Errorf("%s: got %v, want PromptUnknown", tc.desc, got)
		}
	}
}

func TestAnalyzeOutput_PermissionBeatsSpinner(t *testing.T) {
	// Permission patterns should win even if spinner appears elsewhere.
	lines := []string{
		"⠋ working...",
		"Allow this operation? [Y/n]",
	}
	got := AnalyzeOutput(lines)
	if got != PromptNeedsPermission {
		t.Errorf("permission should beat spinner: got %v", got)
	}
}

func TestAnalyzeOutput_LongLastLine(t *testing.T) {
	// Last line > 15 chars starting with '>' → not a prompt.
	lines := []string{">> This is a long line that exceeds the prompt limit"}
	got := AnalyzeOutput(lines)
	if got != PromptUnknown {
		t.Errorf("long line should be PromptUnknown, got %v", got)
	}
}
