package muxd

import (
	"strings"
	"testing"
)

// The screen model is the thing that decides WHICH LINES LEFT THE SCREEN and WHAT COLOUR they
// were. Both answers are load-bearing for scrollback in a way they never were for the preview it
// grew out of, so the tests that matter are the ones where a stream and a screen genuinely
// disagree: repaints, alternate screens, scroll regions, and chunk boundaries that fall in the
// middle of an escape sequence.

// newTestVT builds an incremental model with a collecting sink, which is how every test here asks
// "what actually left the screen".
func newTestVT(t *testing.T, rows, cols int) (*vt, *[]string) {
	t.Helper()
	v := newVT(rows, cols, newStyleTable())
	v.trackAlt = true
	var got []string
	v.onScrollOff = func(row []cell) { got = append(got, strings.TrimRight(rowText(row), " ")) }
	return v, &got
}

func writeAll(v *vt, chunks ...string) {
	for _, c := range chunks {
		v.Write([]byte(c))
	}
}

func TestVT_ScrolledLinesLeaveInOrder(t *testing.T) {
	v, got := newTestVT(t, 3, 20)
	writeAll(v, "one\r\ntwo\r\nthree\r\nfour\r\nfive\r\n")
	// A 3-row screen holding "four"/"five"/"" means one, two and three have left, in that order.
	if want := []string{"one", "two", "three"}; !eqLines(*got, want) {
		t.Fatalf("scrolled off %q, want %q", *got, want)
	}
}

// A chunk boundary can fall anywhere. This is the failure mode that does not exist in the
// one-shot path and is guaranteed to happen in the incremental one: a 32 KB PTY read splitting
// `ESC [ 3 1 m` between the `3` and the `1` would print `1m` into the user's history and lose the
// colour, once per split, forever.
func TestVT_EscapeSequenceSplitAcrossWrites(t *testing.T) {
	v, _ := newTestVT(t, 3, 20)
	writeAll(v, "\x1b[3", "1mred")
	line := v.textLines()[0]
	if line != "red" {
		t.Fatalf("got %q, want %q — the split sequence leaked into the text", line, "red")
	}
	if st := v.styles.style(v.cells[0][0].style); st.FG != IndexedColor(1) {
		t.Fatalf("colour lost across the split: fg=%v", st.FG)
	}
}

func TestVT_UTF8RuneSplitAcrossWrites(t *testing.T) {
	v, _ := newTestVT(t, 3, 20)
	b := []byte("中")
	v.Write(b[:1])
	v.Write(b[1:])
	if line := v.textLines()[0]; line != "中" {
		t.Fatalf("got %q, want 中 — the rune was cut at the chunk boundary", line)
	}
}

func TestVT_PendingNeverGrowsPastTheCap(t *testing.T) {
	v, _ := newTestVT(t, 3, 20)
	// An escape that never terminates. A terminal prints some of it and moves on; what it must
	// NOT do is buffer it forever inside a daemon that owns every shell on the machine.
	v.Write([]byte("\x1b[" + strings.Repeat("1;", maxPendingBytes)))
	if len(v.pending) > maxPendingBytes {
		t.Fatalf("pending grew to %d, cap is %d", len(v.pending), maxPendingBytes)
	}
	if v.dropped == 0 {
		t.Fatal("dropped bytes were not counted — the loss would be invisible")
	}
}

// ── Colour ──────────────────────────────────────────────────────────────────────────────────────

func TestVT_SGRFormsAllParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Style
	}{
		{"basic fg", "\x1b[31m", Style{FG: IndexedColor(1)}},
		{"basic bg", "\x1b[44m", Style{BG: IndexedColor(4)}},
		{"bright fg", "\x1b[92m", Style{FG: IndexedColor(10)}},
		{"bright bg", "\x1b[103m", Style{BG: IndexedColor(11)}},
		{"256 fg", "\x1b[38;5;208m", Style{FG: IndexedColor(208)}},
		{"256 bg", "\x1b[48;5;17m", Style{BG: IndexedColor(17)}},
		{"truecolor", "\x1b[38;2;10;20;30m", Style{FG: RGBColor(10, 20, 30)}},
		{"colon subparams", "\x1b[38:5:208m", Style{FG: IndexedColor(208)}},
		{"attrs", "\x1b[1;3;4m", Style{Attrs: AttrBold | AttrItalic | AttrUnderline}},
		{"attr off", "\x1b[1;4m\x1b[24m", Style{Attrs: AttrBold}},
		{"combined", "\x1b[1;31;44m", Style{FG: IndexedColor(1), BG: IndexedColor(4), Attrs: AttrBold}},
		{"reset", "\x1b[1;31m\x1b[0m", DefaultStyle},
		{"bare m is reset", "\x1b[1;31m\x1b[m", DefaultStyle},
		{"fg default", "\x1b[31m\x1b[39m", DefaultStyle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := newTestVT(t, 3, 20)
			writeAll(v, tc.in)
			if v.cur != tc.want {
				t.Fatalf("style = %+v, want %+v", v.cur, tc.want)
			}
		})
	}
}

func TestVT_MalformedExtendedColorIsSkippedNotGuessed(t *testing.T) {
	v, _ := newTestVT(t, 3, 20)
	// `38` with no mode following it. Guessing would leave every subsequent character the wrong
	// colour until the next reset; skipping leaves it uncoloured, which is the honest answer.
	writeAll(v, "\x1b[38m", "x")
	if v.cur != DefaultStyle {
		t.Fatalf("style = %+v, want default", v.cur)
	}
}

// ── Width ───────────────────────────────────────────────────────────────────────────────────────

func TestVT_WideCharactersTakeTwoColumns(t *testing.T) {
	v, _ := newTestVT(t, 3, 10)
	// Six double-width characters is twelve columns, so the sixth wraps onto row two. Advancing
	// one column per rune — which the model did before this file existed — would fit all six on
	// row one and put every subsequent absolute-addressed row in the wrong place.
	writeAll(v, "中中中中中中")
	lines := v.textLines()
	if lines[0] != "中中中中中" || lines[1] != "中" {
		t.Fatalf("wrapped as %q — want five on the first row and one on the second", lines)
	}
}

func TestVT_CombiningMarksDoNotAdvanceTheCursor(t *testing.T) {
	v, _ := newTestVT(t, 3, 10)
	// KNOWN LIMITATION, asserted so it is a decision rather than a surprise: a combining mark
	// belongs to the previous cell, and a cell holds one rune, so the mark is dropped. Terminal
	// output is overwhelmingly precomposed; storing marks would need a per-cell rune slice, which
	// doubles the cell for a case that affects rendering only — never layout, never search.
	writeAll(v, "éx")
	if got := v.textLines()[0]; got != "ex" {
		t.Fatalf("got %q, want %q", got, "ex")
	}
}

// ── The alternate screen: the reason a full-screen TUI costs no history ─────────────────────────

func TestVT_AlternateScreenContributesNothingToHistory(t *testing.T) {
	v, got := newTestVT(t, 3, 20)
	writeAll(v, "shell one\r\nshell two\r\n")
	writeAll(v, "\x1b[?1049h")
	// A TUI repainting for a while. In a real terminal none of this is scrollback.
	for i := 0; i < 100; i++ {
		writeAll(v, "frame\r\n")
	}
	writeAll(v, "\x1b[?1049l")
	for _, line := range *got {
		if strings.Contains(line, "frame") {
			t.Fatalf("a repainted TUI frame reached history: %q", *got)
		}
	}
}

func TestVT_AlternateScreenExitRestoresTheNormalScreen(t *testing.T) {
	v, _ := newTestVT(t, 4, 20)
	writeAll(v, "keep me\r\n")
	writeAll(v, "\x1b[?1049h", "\x1b[1;1Hfullscreen app")
	if got := v.textLines()[0]; got != "fullscreen app" {
		t.Fatalf("alt screen shows %q", got)
	}
	writeAll(v, "\x1b[?1049l")
	if got := v.textLines(); got[0] != "keep me" {
		t.Fatalf("after exit the screen is %q, want the shell back", got)
	}
}

// The one-shot mode must NOT do any of that — see vt.trackAlt. The overview renders a slice of the
// ring that routinely starts after a TUI already switched and can contain the switch back;
// restoring a screen the replay never saw would blank the card exactly when there is something to
// look at.
func TestRenderScreen_FlattensTheAlternateScreen(t *testing.T) {
	got := RenderScreen("shell\r\n\x1b[?1049hTUI frame\x1b[?1049l", 5, 40)
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "TUI frame") {
		t.Fatalf("one-shot render lost the TUI content: %q", got)
	}
}

// ── Scroll regions: which lines actually leave the screen ───────────────────────────────────────

func TestVT_InteriorScrollRegionDoesNotFeedHistory(t *testing.T) {
	v, got := newTestVT(t, 6, 20)
	// A pager reserving the top two rows for a header scrolls inside rows 3–6. Nothing leaves the
	// screen, so nothing is scrollback — a real terminal discards those lines too. Getting this
	// wrong fills history with re-drawn status lines.
	writeAll(v, "\x1b[3;6r")
	for i := 0; i < 20; i++ {
		writeAll(v, "body\r\n")
	}
	if len(*got) != 0 {
		t.Fatalf("interior-region scrolling produced %d history lines, want 0: %q", len(*got), *got)
	}
}

func TestVT_RegionStartingAtTheTopDoesFeedHistory(t *testing.T) {
	v, got := newTestVT(t, 6, 20)
	// The common shape: a program reserves the LAST row for a status bar. Lines still leave the
	// top of the screen, so they are still scrollback — this is xterm's rule (top margin zero).
	writeAll(v, "\x1b[1;5r")
	for i := 0; i < 10; i++ {
		writeAll(v, "line\r\n")
	}
	if len(*got) == 0 {
		t.Fatal("a region anchored at row 1 must still push lines into history")
	}
}

func TestVT_ScrollUpAndDownSequences(t *testing.T) {
	v, got := newTestVT(t, 4, 20)
	writeAll(v, "a\r\nb\r\nc\r\nd")
	writeAll(v, "\x1b[2S") // SU 2 — two lines leave the top
	if want := []string{"a", "b"}; !eqLines(*got, want) {
		t.Fatalf("SU pushed %q, want %q", *got, want)
	}
	before := len(*got)
	writeAll(v, "\x1b[2T") // SD 2 — lines leave the BOTTOM, which is not history
	if len(*got) != before {
		t.Fatalf("SD fed history %q — content pushed off the bottom is gone, not scrollback", *got)
	}
}

func TestVT_InsertAndDeleteLinesNeverFeedHistory(t *testing.T) {
	v, got := newTestVT(t, 5, 20)
	writeAll(v, "a\r\nb\r\nc\r\nd\r\ne")
	writeAll(v, "\x1b[1;1H", "\x1b[2M") // delete two lines at the top
	writeAll(v, "\x1b[1;1H", "\x1b[2L") // insert two back
	if len(*got) != 0 {
		t.Fatalf("IL/DL fed history %q — a deleted line is destroyed in place, not scrolled off", *got)
	}
}

// ── Line editing ────────────────────────────────────────────────────────────────────────────────

func TestVT_LineEditingSequences(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ECH erases in place", "abcdef\x1b[1;3H\x1b[2X", "ab  ef"},
		{"DCH shifts left", "abcdef\x1b[1;3H\x1b[2P", "abef"},
		{"ICH shifts right", "abcdef\x1b[1;3H\x1b[2@", "ab  cdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := newTestVT(t, 3, 20)
			writeAll(v, tc.in)
			if got := v.textLines()[0]; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVT_IndexAndNextLineEscapes(t *testing.T) {
	v, got := newTestVT(t, 2, 20)
	writeAll(v, "one\x1bD")   // IND — down a line, scrolling at the bottom
	writeAll(v, "two\x1bE")   // NEL — carriage return plus IND
	writeAll(v, "three\x1bM") // RI — back up
	if len(*got) == 0 || (*got)[0] != "one" {
		t.Fatalf("IND/NEL did not scroll properly: %q", *got)
	}
}

// ── Background colour erase ─────────────────────────────────────────────────────────────────────

func TestVT_EraseUsesTheCurrentBackground(t *testing.T) {
	v, _ := newTestVT(t, 3, 10)
	// Programs paint coloured panels by setting a background and clearing. Erasing to the DEFAULT
	// background instead would turn a highlighted region into an unhighlighted one.
	writeAll(v, "\x1b[44m\x1b[2J")
	st := v.styles.style(v.cells[1][5].style)
	if st.BG != IndexedColor(4) {
		t.Fatalf("erased cell has bg %v, want the current background", st.BG)
	}
}

func eqLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
