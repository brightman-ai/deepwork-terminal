package agentintel

import (
	"strings"
	"testing"
)

// TailFromLines is the non-tmux twin of CaptureWindowTail. These lock in the two properties the
// overview card depends on: the agent's pinned bottom chrome is removed BEFORE the line cap
// applies (otherwise a card shows nothing but the input box), and an unknown/bare shell degrades
// to raw content instead of being mangled.

func TestScreenTail_StripsClaudeChromeBeforeCapping(t *testing.T) {
	// A realistic Claude frame: real output on top, then the pinned input box + status chrome.
	screen := "building the parser\n" +
		"ran 42 tests, all green\n" +
		"────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────\n" +
		"  🤖 claude-opus-5 | 💰 $1.20 | 🧠 42%\n" +
		"  ⏵⏵ accept edits\n" +
		"                        18234 tokens\n"

	got := TailFromLines(strings.Split(screen, "\n"), ToolClaude, 8)

	// Had we capped to the last 8 lines first, the result would be pure chrome and the card would
	// show an input box instead of what the agent actually did.
	if len(got) != 2 {
		t.Fatalf("want 2 content lines after chrome strip, got %d: %q", len(got), got)
	}
	if got[0] != "building the parser" || got[1] != "ran 42 tests, all green" {
		t.Fatalf("wrong content retained: %q", got)
	}
}

func TestScreenTail_CapsToRequestedLines(t *testing.T) {
	screen := "l1\nl2\nl3\nl4\nl5\n"
	got := TailFromLines(strings.Split(screen, "\n"), ToolNone, 3)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(got), got)
	}
	// Keeps the LAST n (most recent output), not the first.
	if got[0] != "l3" || got[2] != "l5" {
		t.Fatalf("want the most recent lines, got %q", got)
	}
}

func TestScreenTail_BareShellKeepsContent(t *testing.T) {
	// No recognizable agent frame → must degrade to the raw content, never mis-strip.
	screen := "$ ls\nREADME.md  main.go\n$ \n"
	got := TailFromLines(strings.Split(screen, "\n"), ToolNone, 8)
	if len(got) == 0 {
		t.Fatal("bare shell output must not be stripped away entirely")
	}
	if got[0] != "$ ls" {
		t.Fatalf("bare shell content altered: %q", got)
	}
}

func TestScreenTail_ZeroLinesReturnsNil(t *testing.T) {
	if got := TailFromLines([]string{"anything"}, ToolNone, 0); got != nil {
		t.Fatalf("want nil for a zero-line request, got %q", got)
	}
}

func TestScreenTail_EmptyScreenIsEmptyNotPadding(t *testing.T) {
	// A session that has printed nothing must yield an empty tail so the card can say
	// "(no recent output)" rather than rendering blank padding rows.
	if got := TailFromLines([]string{"", "", ""}, ToolNone, 8); len(got) != 0 {
		t.Fatalf("want empty tail for a blank screen, got %q", got)
	}
}
