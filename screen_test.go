package terminal

import (
	"strings"
	"testing"
)

// renderScreen replaces "strip the escapes and hope" with an actual grid replay. The tests that
// matter are the ones where a stream and a screen genuinely disagree — repaints, in-place
// overwrites, erases — because that is where the old approach produced garbage.

func TestRenderScreen_PlainOutput(t *testing.T) {
	got := renderScreen("hello\r\nworld\r\n")
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("want [hello world], got %q", got)
	}
}

func TestRenderScreen_DropsColorsButKeepsText(t *testing.T) {
	got := renderScreen("\x1b[32mbuild ok\x1b[0m\r\n")
	if len(got) != 1 || got[0] != "build ok" {
		t.Fatalf("want [build ok], got %q", got)
	}
}

func TestRenderScreen_CursorPositioningPlacesTextWhereItBelongs(t *testing.T) {
	// The defining case: two fragments written to row 3 and row 1 IN THAT ORDER. A stream-based
	// approach emits them in arrival order ("second" then "first" concatenated); a screen puts
	// each on its own row, in screen order.
	got := renderScreen("\x1b[3;1Hsecond\x1b[1;1Hfirst")
	if len(got) < 3 {
		t.Fatalf("want 3 rows, got %d: %q", len(got), got)
	}
	if got[0] != "first" {
		t.Fatalf("row 1 should hold the LAST write to it, got %q", got[0])
	}
	if got[2] != "second" {
		t.Fatalf("row 3 should hold its own text, got %q", got[2])
	}
}

func TestRenderScreen_RepaintOverwritesInPlace(t *testing.T) {
	// A TUI redrawing the same line must yield ONE line showing the final state — this is the
	// exact behavior whose absence made the overview card unreadable.
	got := renderScreen("\x1b[1;1Hthinking...\x1b[1;1H\x1b[Kdone!     ")
	if len(got) != 1 {
		t.Fatalf("a repaint of one line must stay one line, got %d: %q", len(got), got)
	}
	if got[0] != "done!" {
		t.Fatalf("want the final state 'done!', got %q", got[0])
	}
	if strings.Contains(got[0], "thinking") {
		t.Fatalf("stale frame survived the repaint: %q", got[0])
	}
}

func TestRenderScreen_CarriageReturnOverwrite(t *testing.T) {
	// Progress bars use bare CR. "50%" then "100%" on the same line → only the latest shows.
	got := renderScreen("progress 50%\rprogress 100%")
	if len(got) != 1 || got[0] != "progress 100%" {
		t.Fatalf("want [progress 100%%], got %q", got)
	}
}

func TestRenderScreen_EraseInDisplayClearsEarlierContent(t *testing.T) {
	got := renderScreen("old junk\r\nmore junk\r\n\x1b[2J\x1b[1;1Hfresh")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "junk") {
		t.Fatalf("ESC[2J must clear the screen, got %q", got)
	}
	if got[0] != "fresh" {
		t.Fatalf("want 'fresh' at the top, got %q", got)
	}
}

func TestRenderScreen_EraseInLineToEnd(t *testing.T) {
	got := renderScreen("abcdefgh\r\x1b[3C\x1b[K")
	if len(got) != 1 || got[0] != "abc" {
		t.Fatalf("ESC[K should clear from the cursor to EOL, got %q", got)
	}
}

func TestRenderScreen_ScrollsPastTheLastRow(t *testing.T) {
	// More lines than the grid: the earliest must fall off, the newest must survive.
	var b strings.Builder
	for i := 0; i < screenRows+10; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + i%10))
		b.WriteString("\r\n")
	}
	got := renderScreen(b.String())
	if len(got) > screenRows {
		t.Fatalf("screen grew past its bound: %d rows", len(got))
	}
	if len(got) == 0 {
		t.Fatal("everything scrolled away")
	}
}

func TestRenderScreen_OSCTitleAndTwoCharEscapesLeaveNoResidue(t *testing.T) {
	// The zsh prompt case: an OSC title plus ESC = / ESC ( B. None may reach the text.
	got := renderScreen("\x1b]0;my title\x07ready\x1b=\x1b(B")
	joined := strings.Join(got, "\n")
	if strings.ContainsRune(joined, 0x1b) || strings.Contains(joined, "my title") {
		t.Fatalf("escape residue or title leaked: %q", got)
	}
	if !strings.Contains(joined, "ready") {
		t.Fatalf("real text lost: %q", got)
	}
}

func TestRenderScreen_PrivateModesAreIgnored(t *testing.T) {
	// Alt-screen / bracketed-paste / cursor-hide toggles must not corrupt the grid.
	got := renderScreen("\x1b[?1049h\x1b[?2004h\x1b[?25lcontent\x1b[?25h")
	if len(got) != 1 || got[0] != "content" {
		t.Fatalf("want [content], got %q", got)
	}
}

func TestRenderScreen_UTF8Preserved(t *testing.T) {
	got := renderScreen("部署完成 ✓\r\n")
	if len(got) != 1 || got[0] != "部署完成 ✓" {
		t.Fatalf("multibyte text mangled: %q", got)
	}
}

func TestRenderScreen_EmptyInput(t *testing.T) {
	if got := renderScreen(""); len(got) != 0 {
		t.Fatalf("want no lines for empty input, got %q", got)
	}
}

func TestRenderScreen_MalformedCSIDoesNotHang(t *testing.T) {
	// A truncated sequence at the end of a ring slice is normal (we cut mid-stream).
	for _, in := range []string{"text\x1b", "text\x1b[", "text\x1b[38;5", "\x1b]0;unterminated"} {
		if got := renderScreen(in); len(got) > screenRows {
			t.Fatalf("input %q produced a runaway screen", in)
		}
	}
}
