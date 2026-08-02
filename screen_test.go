package terminal

import (
	"strings"
	"testing"
)

// renderScreen replaces "strip the escapes and hope" with an actual grid replay. The tests that
// matter are the ones where a stream and a screen genuinely disagree — repaints, in-place
// overwrites, erases — because that is where the old approach produced garbage.

// testRows/testCols are the PTY spawn size — what a session has before any client resizes it.
// These used to be package constants baked into renderScreen; they are parameters now because a
// replay onto the wrong-size grid isn't a smaller screen, it's a corrupted one — see
// TestRenderScreen_UndersizedGridMashesRowsTogether and the screen type's doc.
const (
	testRows = spawnRows
	testCols = spawnCols
)

func TestRenderScreen_PlainOutput(t *testing.T) {
	got := renderScreen("hello\r\nworld\r\n", testRows, testCols)
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("want [hello world], got %q", got)
	}
}

func TestRenderScreen_DropsColorsButKeepsText(t *testing.T) {
	got := renderScreen("\x1b[32mbuild ok\x1b[0m\r\n", testRows, testCols)
	if len(got) != 1 || got[0] != "build ok" {
		t.Fatalf("want [build ok], got %q", got)
	}
}

func TestRenderScreen_CursorPositioningPlacesTextWhereItBelongs(t *testing.T) {
	// The defining case: two fragments written to row 3 and row 1 IN THAT ORDER. A stream-based
	// approach emits them in arrival order ("second" then "first" concatenated); a screen puts
	// each on its own row, in screen order.
	got := renderScreen("\x1b[3;1Hsecond\x1b[1;1Hfirst", testRows, testCols)
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
	got := renderScreen("\x1b[1;1Hthinking...\x1b[1;1H\x1b[Kdone!     ", testRows, testCols)
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
	got := renderScreen("progress 50%\rprogress 100%", testRows, testCols)
	if len(got) != 1 || got[0] != "progress 100%" {
		t.Fatalf("want [progress 100%%], got %q", got)
	}
}

func TestRenderScreen_EraseInDisplayClearsEarlierContent(t *testing.T) {
	got := renderScreen("old junk\r\nmore junk\r\n\x1b[2J\x1b[1;1Hfresh", testRows, testCols)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "junk") {
		t.Fatalf("ESC[2J must clear the screen, got %q", got)
	}
	if got[0] != "fresh" {
		t.Fatalf("want 'fresh' at the top, got %q", got)
	}
}

func TestRenderScreen_EraseInLineToEnd(t *testing.T) {
	got := renderScreen("abcdefgh\r\x1b[3C\x1b[K", testRows, testCols)
	if len(got) != 1 || got[0] != "abc" {
		t.Fatalf("ESC[K should clear from the cursor to EOL, got %q", got)
	}
}

func TestRenderScreen_ScrollsPastTheLastRow(t *testing.T) {
	// More lines than the grid: the earliest must fall off, the newest must survive.
	var b strings.Builder
	for i := 0; i < testRows+10; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + i%10))
		b.WriteString("\r\n")
	}
	got := renderScreen(b.String(), testRows, testCols)
	if len(got) > testRows {
		t.Fatalf("screen grew past its bound: %d rows", len(got))
	}
	if len(got) == 0 {
		t.Fatal("everything scrolled away")
	}
}

func TestRenderScreen_OSCTitleAndTwoCharEscapesLeaveNoResidue(t *testing.T) {
	// The zsh prompt case: an OSC title plus ESC = / ESC ( B. None may reach the text.
	got := renderScreen("\x1b]0;my title\x07ready\x1b=\x1b(B", testRows, testCols)
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
	got := renderScreen("\x1b[?1049h\x1b[?2004h\x1b[?25lcontent\x1b[?25h", testRows, testCols)
	if len(got) != 1 || got[0] != "content" {
		t.Fatalf("want [content], got %q", got)
	}
}

func TestRenderScreen_UTF8Preserved(t *testing.T) {
	got := renderScreen("部署完成 ✓\r\n", testRows, testCols)
	if len(got) != 1 || got[0] != "部署完成 ✓" {
		t.Fatalf("multibyte text mangled: %q", got)
	}
}

func TestRenderScreen_EmptyInput(t *testing.T) {
	if got := renderScreen("", testRows, testCols); len(got) != 0 {
		t.Fatalf("want no lines for empty input, got %q", got)
	}
}

func TestRenderScreen_MalformedCSIDoesNotHang(t *testing.T) {
	// A truncated sequence at the end of a ring slice is normal (we cut mid-stream).
	for _, in := range []string{"text\x1b", "text\x1b[", "text\x1b[38;5", "\x1b]0;unterminated"} {
		if got := renderScreen(in, testRows, testCols); len(got) > testRows {
			t.Fatalf("input %q produced a runaway screen", in)
		}
	}
}

// The grid must be the size of the PTY that produced the bytes, or the replay is not a smaller
// screen — it is a WRONG one. A TUI addresses rows absolutely, so every row past the end of an
// undersized grid is clamped onto the last row and OVERWRITES what was already there.
//
// This was live: the grid was hardcoded 48x200 while a PTY is born 50x220 (and then resized to the
// browser's viewport), so a Claude-in-tmux card showed its status line, its mode line and tmux's
// status bar mashed into a single row — "Debug" survived as "ebug" after another clamped row ate
// its D. The user reported it as "只有一小段内输出".
func TestRenderScreen_UndersizedGridMashesRowsTogether(t *testing.T) {
	at := func(row int) string { return "\x1b[" + itoa(row) + ";1H" }
	stream := at(48) + "row-48" + at(49) + "row-49" + at(50) + "row-50"

	// Correct size: every row keeps its own line.
	full := renderScreen(stream, 50, 220)
	if len(full) != 50 {
		t.Fatalf("PTY-sized grid: want 50 lines, got %d", len(full))
	}
	for i, want := range map[int]string{47: "row-48", 48: "row-49", 49: "row-50"} {
		if full[i] != want {
			t.Fatalf("PTY-sized grid: line[%d] = %q, want %q", i, full[i], want)
		}
	}

	// Undersized: rows 49 and 50 collapse onto row 48's line and the earlier ones are destroyed.
	// Asserted so the failure mode stays documented rather than becoming folklore.
	short := renderScreen(stream, 48, 220)
	if len(short) != 48 {
		t.Fatalf("undersized grid: want 48 lines, got %d", len(short))
	}
	if short[47] != "row-50" {
		t.Fatalf("undersized grid: last line = %q, want the LAST clamped write to win (row-50)", short[47])
	}
	for _, gone := range []string{"row-48", "row-49"} {
		for _, l := range short {
			if l == gone {
				t.Fatalf("undersized grid unexpectedly preserved %q — the collapse this test documents is gone", gone)
			}
		}
	}
}

// A session that has never been resized still has to replay onto a real grid, not a zero one.
func TestRenderScreen_NonPositiveSizeFallsBackToSpawnSize(t *testing.T) {
	got := renderScreen("\x1b[50;1Hbottom", 0, 0)
	if len(got) != spawnRows || got[spawnRows-1] != "bottom" {
		t.Fatalf("want a %d-row fallback grid with row 50 intact, got %d lines: %q", spawnRows, len(got), got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
