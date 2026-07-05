package agentintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCapture loads a real `tmux capture-pane -p` fixture the same way CaptureWindowTail splits
// live output: on "\n", keeping the trailing empty element that a final newline produces.
func readCapture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return strings.Split(string(b), "\n")
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// chromeMarkers are strings that must NEVER survive a Claude strip: the input-box border, the
// "Debug" hint, the model/status glyphs, the permission-mode line, and the custom status line
// below it. (A bare "tokens" is NOT here — Claude's in-conversation activity readout legitimately
// says "↓ 1.3k tokens"; the chrome's token *counter* is asserted per-fixture instead.)
var chromeMarkers = []string{"─────", "Debug", "🤖", "💰", "bypass permissions", "● main", "◯ general-purpose"}

func assertNoChrome(t *testing.T, got []string) {
	t.Helper()
	j := joined(got)
	for _, m := range chromeMarkers {
		if strings.Contains(j, m) {
			t.Fatalf("stripped tail still contains chrome marker %q:\n%s", m, j)
		}
	}
}

// B-1: Claude "thinking" state (Read tool calls + a "Reticulating…" spinner), whose screen also
// carries a custom status line BELOW the Debug hint. All chrome must go; the spinner (the real
// "what is it doing") must remain as the last line.
func TestStripClaudeChrome_ThinkingState(t *testing.T) {
	got := stripAgentChrome(readCapture(t, "claude_thinking.txt"), ToolClaude)
	assertNoChrome(t, got)
	if !strings.Contains(lastNonEmpty(got), "Reticulating") {
		t.Fatalf("expected last content line to be the thinking spinner, got %q", lastNonEmpty(got))
	}
	if len(got) == 0 {
		t.Fatal("expected real content, got empty")
	}
}

// B-1: Claude "streaming output" state (tool output + a todo list + a token status line). Chrome
// gone; the todo content preserved.
func TestStripClaudeChrome_OutputState(t *testing.T) {
	got := stripAgentChrome(readCapture(t, "claude_output.txt"), ToolClaude)
	assertNoChrome(t, got)
	// The chrome token *counter* line must be gone (distinct from the in-conversation "↓ 1.3k
	// tokens" activity readout, which is real content and may remain).
	if j := joined(got); strings.Contains(j, "359255 tokens") || strings.Contains(j, "/goal active") {
		t.Fatalf("chrome token-counter line survived:\n%s", j)
	}
	if !strings.Contains(joined(got), "P3 实施") {
		t.Fatalf("expected todo content to survive, got:\n%s", joined(got))
	}
}

// B-3: a bare zsh screen (mostly blank + prompt) with ToolNone must pass through untouched —
// only trailing blank padding trimmed, never mis-stripped or emptied.
func TestStripBareShell_NoStrip(t *testing.T) {
	raw := readCapture(t, "zsh_bare.txt")
	got := stripAgentChrome(raw, ToolNone)
	if len(got) == 0 {
		t.Fatal("bare shell tail was emptied — must degrade to raw content (B-3)")
	}
	if !strings.Contains(lastNonEmpty(got), "ubuntu@anthony-work") {
		t.Fatalf("bare shell prompt line lost: %q", lastNonEmpty(got))
	}
	// Exactly trimTrailingBlank of the input — nothing else touched.
	if want := trimTrailingBlank(raw); joined(got) != joined(want) {
		t.Fatalf("bare shell content was altered beyond trailing-blank trim:\n%s", joined(got))
	}
}

// B-3: a shell screen with a fancy git prompt (window is even NAMED "claude_remote" but its
// active pane is a shell) must not be stripped.
func TestStripGitPromptShell_NoStrip(t *testing.T) {
	got := stripAgentChrome(readCapture(t, "zsh_git_prompt.txt"), ToolNone)
	if !strings.Contains(joined(got), "git commit") {
		t.Fatalf("git-prompt shell content lost:\n%s", joined(got))
	}
}

// B-3 defensive: even if the tool is MIS-attributed as Claude, a screen with no Claude input box
// (a bare shell) must survive — stripClaudeChrome anchors on the border pair and finds none.
func TestStripClaudeChrome_OnBareShell_LeavesRaw(t *testing.T) {
	raw := readCapture(t, "zsh_git_prompt.txt")
	got := stripClaudeChrome(raw)
	if joined(got) != joined(trimTrailingBlank(raw)) {
		t.Fatalf("Claude strip mangled a non-Claude screen:\n%s", joined(got))
	}
}

// B-4: a screen that is ALL chrome (no conversation output above the box) must strip to empty,
// so the card shows "(no recent output)" rather than border/token residue. The box structure
// here is the real one verified from live captures; only the content above it is omitted.
func TestStripClaudeChrome_AllChrome_Empty(t *testing.T) {
	border := strings.Repeat("─", 57)
	raw := []string{
		border,
		"❯ ",
		border,
		"  🤖 Opus 4.8 | 💰 $417.47 | 🧠 123.1K(12%) | fast✗",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
		"                                          126032 tokens",
		"                                                  Debug",
		"",
		"",
	}
	got := stripAgentChrome(raw, ToolClaude)
	if len(got) != 0 {
		t.Fatalf("all-chrome screen should strip to empty, got %d lines:\n%s", len(got), joined(got))
	}
}

// B-1 waiting-for-input: a permission choice menu sits ABOVE the input box, so stripping the box
// preserves the question the user must answer. Box structure is the verified real one.
func TestStripClaudeChrome_WaitingKeepsChoices(t *testing.T) {
	border := strings.Repeat("─", 57)
	raw := []string{
		"● Do you want to make this edit to tail_strip.go?",
		"  ❯ 1. Yes",
		"    2. Yes, and don't ask again",
		"    3. No, tell Claude what to do differently",
		border,
		"❯ ",
		border,
		"  🤖 Opus 4.8 | 💰 $417.47 | 🧠 123.1K(12%)",
		"                                          126032 tokens",
		"                                                  Debug",
	}
	got := stripAgentChrome(raw, ToolClaude)
	assertNoChrome(t, got)
	if !strings.Contains(joined(got), "1. Yes") || !strings.Contains(joined(got), "Do you want to make this edit") {
		t.Fatalf("waiting-state choices/question were lost:\n%s", joined(got))
	}
}

// Codex has no live sample yet (handoff: don't guess its frame) → safe no-op degradation.
func TestStripCodexChrome_PendingSample_NoOp(t *testing.T) {
	raw := []string{"codex line 1", "codex line 2", "", ""}
	got := stripAgentChrome(raw, ToolCodex)
	if joined(got) != "codex line 1\ncodex line 2" {
		t.Fatalf("codex strip should be a trailing-blank-trim no-op until a fixture exists, got:\n%s", joined(got))
	}
}

func TestIsBoxBorderLine(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{strings.Repeat("─", 57), true},
		{"  " + strings.Repeat("─", 20) + "  ", true}, // surrounding whitespace ignored
		{strings.Repeat("─", 3), false},               // too short — a decoration, not a box border
		{"─── heading ───", false},                    // contains non-border runes
		{"", false},
		{"   ", false},
		{"regular text line", false},
		{"----------", false}, // ASCII hyphens are not U+2500
	}
	for _, c := range cases {
		if got := isBoxBorderLine(c.in); got != c.want {
			t.Errorf("isBoxBorderLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
