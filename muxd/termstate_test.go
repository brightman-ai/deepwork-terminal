package muxd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TermState's promise, as tests: a model restored from another model's State is indistinguishable
// from it — on the screen, in the scrollback, and in how the NEXT bytes land. The last clause is
// the one that matters to a reconnect: a restore that looks right but places the next write
// somewhere else is a screen that drifts the moment the program says anything.

// rendered resolves a state's lines against its own style table, so two models whose tables were
// built in different orders can be compared on what they draw rather than on their ids.
func rendered(lines []Line, styles []Style) []string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		var b strings.Builder
		pos := 0
		for _, sp := range ln.Spans {
			b.WriteString(ln.Text[pos:sp.Start])
			st := DefaultStyle
			if int(sp.Style) < len(styles) {
				st = styles[sp.Style]
			}
			fmt.Fprintf(&b, "{%v}", st)
			pos = int(sp.Start)
		}
		b.WriteString(ln.Text[pos:])
		out[i] = b.String()
	}
	return out
}

func sameState(t *testing.T, what string, a, b *History) {
	t.Helper()
	sa, la, oka := a.State(1000)
	sb, lb, okb := b.State(1000)
	if !oka || !okb {
		t.Fatalf("%s: State failed (a=%v b=%v)", what, oka, okb)
	}
	if got, want := rendered(sb.Lines, sb.Styles), rendered(sa.Lines, sa.Styles); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s: screens differ\nwant:\n%s\ngot:\n%s", what, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
	if got, want := rendered(lb, sb.Styles), rendered(la, sa.Styles); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s: scrollback differs\nwant:\n%s\ngot:\n%s", what, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
	if (sa.Parked == nil) != (sb.Parked == nil) {
		t.Fatalf("%s: parked screen presence differs", what)
	}
	if sa.Parked != nil {
		if got, want := rendered(sb.Parked.Lines, sb.Styles), rendered(sa.Parked.Lines, sa.Styles); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%s: parked screens differ\nwant:\n%s\ngot:\n%s", what, strings.Join(want, "\n"), strings.Join(got, "\n"))
		}
	}
	type pos struct{ Row, Col, Top, Bot int }
	if p, q := (pos{sa.CursorRow, sa.CursorCol, sa.ScrollTop, sa.ScrollBot}), (pos{sb.CursorRow, sb.CursorCol, sb.ScrollTop, sb.ScrollBot}); p != q {
		t.Fatalf("%s: cursor/region differ: want %+v got %+v", what, p, q)
	}
	if sa.Pen != sb.Pen || sa.Alt != sb.Alt || sa.CursorShape != sb.CursorShape {
		t.Fatalf("%s: pen/alt/shape differ: want %v %v %v got %v %v %v", what,
			sa.Pen, sa.Alt, sa.CursorShape, sb.Pen, sb.Alt, sb.CursorShape)
	}
	if p, q := [3]int{sa.SavedRow, sa.SavedCol}, [3]int{sb.SavedRow, sb.SavedCol}; p != q || sa.SavedPen != sb.SavedPen {
		t.Fatalf("%s: DECSC slot differs: want %v %v got %v %v", what, p, sa.SavedPen, q, sb.SavedPen)
	}
	if fmt.Sprint(sa.Modes) != fmt.Sprint(sb.Modes) {
		t.Fatalf("%s: modes differ: want %v got %v", what, sa.Modes, sb.Modes)
	}
	if !bytes.Equal(sa.Pending, sb.Pending) {
		t.Fatalf("%s: pending differs: want %q got %q", what, sa.Pending, sb.Pending)
	}
}

func TestTermState_RestoredModelContinuesExactlyLikeTheOriginal(t *testing.T) {
	g := Grid{Cols: 20, Rows: 6}
	a := NewHistory(g, 100)
	// Scrolling coloured output, wide characters, a scroll region, modes, keypad, cursor shape, a
	// saved cursor — and a write cut in the middle of an escape sequence, which is the state a
	// keyframe is taken in whenever its offset falls between two halves of one.
	feed(a,
		"\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b=\x1b[5 q",
		"line one\r\n\x1b[31mred two\x1b[0m\r\n中文宽字符\r\nfour\r\nfive\r\nsix\r\nseven\r\neight\r\n",
		"\x1b[2;5r\x1b[4;3Hin region\x1b7",
		"\x1b[1;44mpen is bold on blue\x1b[3",
	)

	b := NewHistory(Grid{Cols: 80, Rows: 24}, 100) // a different shape on purpose: Restore must adopt the state's
	st, scroll, ok := a.State(1000)
	if !ok {
		t.Fatal("State failed on a healthy model")
	}
	b.Restore(st, scroll)
	sameState(t, "right after restore", a, b)

	// The pending "\x1b[3" must complete into SGR 32 on both, not print "2m".
	next := "2mgreen\x1b[0m\x1b8after restore\r\n\x1b[r\x1b[?1000l\x1b>more\r\nand more\r\n"
	feed(a, next)
	feed(b, next)
	sameState(t, "after the same continuation", a, b)
}

func TestTermState_AlternateScreenRestoresBothScreens(t *testing.T) {
	a := NewHistory(Grid{Cols: 30, Rows: 5}, 100)
	feed(a, "shell prompt $ ls\r\nfile1 file2\r\n$ vim\x1b[?1049h\x1b[H\x1b[2Jediting a file\x1b[3;1H~\r\n~")

	b := NewHistory(Grid{Cols: 30, Rows: 5}, 100)
	st, scroll, _ := a.State(100)
	if !st.Alt || st.Parked == nil {
		t.Fatalf("expected the alternate screen with the shell parked behind it, got alt=%v parked=%v", st.Alt, st.Parked != nil)
	}
	b.Restore(st, scroll)
	sameState(t, "inside the TUI", a, b)

	// Leaving the TUI must bring back the shell that was parked — on both.
	feed(a, "\x1b[?1049l$ ")
	feed(b, "\x1b[?1049l$ ")
	sameState(t, "after leaving the TUI", a, b)
}

func TestTermState_ModesAreReportedInTheOrderTheyLastChanged(t *testing.T) {
	h := NewHistory(Grid{Cols: 20, Rows: 4}, 10)
	feed(h, "\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[?1000l\x1b[?2026h\x1b[?1049h\x1b[?1049l\x1b[?2026l")
	st, _, _ := h.State(0)
	want := "[{1006 true} {2004 true} {1000 false}]"
	if got := fmt.Sprint(st.Modes); got != want {
		t.Fatalf("modes = %s, want %s (2026 and 1049 are structure, not restorable flags)", got, want)
	}

	// RIS returns every observed mode to its power-on value, as an explicit change.
	feed(h, "\x1b[?25l\x1bc")
	st, _, _ = h.State(0)
	for _, m := range st.Modes {
		if want := m.Mode == 25; m.On != want {
			t.Fatalf("after RIS mode %d = %v, want %v", m.Mode, m.On, want)
		}
	}
}

func TestRingBuffer_ReadFromIsByteExactAcrossWrapAndReset(t *testing.T) {
	rb := NewRingBuffer(16)
	_, _ = rb.Write([]byte("0123456789"))
	if data, end, ok := rb.ReadFrom(4); !ok || string(data) != "456789" || end != 10 {
		t.Fatalf("ReadFrom(4) = %q,%d,%v", data, end, ok)
	}
	// An escape sequence split at the requested position comes back split — no boundary cleanup.
	_, _ = rb.Write([]byte("\x1b[31mX"))
	if data, _, ok := rb.ReadFrom(12); !ok || string(data) != "31mX" {
		t.Fatalf("ReadFrom mid-sequence = %q,%v; want the raw tail", data, ok)
	}
	// Wrap: seq is now 32 with 16 bytes held, so 16 is the oldest position still readable.
	_, _ = rb.Write([]byte("abcdefghijklmnop"))
	if _, _, ok := rb.ReadFrom(15); ok {
		t.Fatal("ReadFrom an evicted position must refuse, not return a shorter tail")
	}
	if data, end, ok := rb.ReadFrom(16); !ok || string(data) != "abcdefghijklmnop" || end != 32 {
		t.Fatalf("ReadFrom(oldest) = %q,%d,%v", data, end, ok)
	}
	if data, _, ok := rb.ReadFrom(32); !ok || len(data) != 0 {
		t.Fatalf("ReadFrom(end) = %q,%v; want empty and ok", data, ok)
	}
	if _, _, ok := rb.ReadFrom(33); ok {
		t.Fatal("ReadFrom the future must refuse")
	}
	// Reset empties the buffer and moves seq; the old positions are gone, the new end is valid.
	rb.Reset()
	end := rb.Seq()
	if _, _, ok := rb.ReadFrom(end - 1); ok {
		t.Fatal("ReadFrom before a Reset must refuse")
	}
	_, _ = rb.Write([]byte("new"))
	if data, _, ok := rb.ReadFrom(end); !ok || string(data) != "new" {
		t.Fatalf("ReadFrom after Reset = %q,%v", data, ok)
	}
}

func TestScreenSnapRPC_StateAndOffsetDescribeTheSameInstant(t *testing.T) {
	c, id, w := historyPair(t)
	if _, err := w.Write([]byte("\x1b[?2004hone\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\n\x1b[32mseven")); err != nil {
		t.Fatalf("write: %v", err)
	}
	produce(t, c, id, 2)
	var snap ScreenSnapAck
	waitFor(t, 3*time.Second, "the snapshot to include the last write", func() bool {
		var err error
		snap, err = c.ScreenSnap(id)
		return err == nil && len(snap.Lines) > 0 && strings.Contains(snap.Lines[len(snap.Lines)-1].Text, "seven")
	})
	if !snap.Enabled || snap.Broken {
		t.Fatalf("snapshot enabled=%v broken=%v", snap.Enabled, snap.Broken)
	}
	if snap.Rows != 5 || snap.Cols != 80 {
		t.Fatalf("snapshot grid = %dx%d, want 80x5", snap.Cols, snap.Rows)
	}
	if fmt.Sprint(snap.Modes) != "[{2004 true}]" {
		t.Fatalf("snapshot modes = %v", snap.Modes)
	}
	if snap.HistoryTotal != 2 {
		t.Fatalf("HistoryTotal = %d, want 2 (one, two scrolled off a 5-row screen)", snap.HistoryTotal)
	}
	// Offset is the ring position: attaching from it replays nothing that the state already shows.
	st, err := c.Attach(id, AttachOptions{Since: &snap.Offset})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer st.Close()
	if st.ReplayFrom != snap.Offset || st.Offset != snap.Offset {
		t.Fatalf("attach from the snapshot offset replayed [%d,%d), want nothing after %d", st.ReplayFrom, st.Offset, snap.Offset)
	}
}

// Review findings (codex, 2026-10-14), each pinned with the reviewer's own reproduction.

func TestVT_AutowrapOffOverwritesTheMargin(t *testing.T) {
	h := NewHistory(Grid{Cols: 10, Rows: 3}, 10)
	feed(h, "\x1b[?7lABCDEFGHIJK")
	st, _, _ := h.State(0)
	if got := st.Lines[0].Text; got != "ABCDEFGHIK" {
		t.Fatalf("row 0 = %q, want %q: with DECAWM off the margin is overwritten, not wrapped", got, "ABCDEFGHIK")
	}
	if len(st.Lines) > 1 && st.Lines[1].Text != "" {
		t.Fatalf("row 1 = %q, want empty", st.Lines[1].Text)
	}
}

func TestVT_EachScreenHasItsOwnSavedCursor(t *testing.T) {
	h := NewHistory(Grid{Cols: 10, Rows: 5}, 10)
	// ESC 7 on the normal screen at (3,4); the TUI's ESC 8 on the alternate screen must not
	// reach it — the alternate screen has its own slot.
	feed(h, "\x1b[4;5H\x1b7\x1b[?1049h\x1b8Z")
	st, _, _ := h.State(0)
	if st.Lines[0].Text != "Z" {
		t.Fatalf("alt row 0 = %q: ESC 8 restored the NORMAL screen's slot on the alternate screen", st.Lines[0].Text)
	}
	// And leaving restores the normal screen's cursor (1049's DECRC).
	feed(h, "\x1b[?1049lX")
	st, _, _ = h.State(0)
	if got := st.Lines[3].Text; got != "    X" {
		t.Fatalf("normal row 3 = %q, want the cursor back at (3,4)", got)
	}
}

func TestVT_KeypadSpellingsAreOneSetting(t *testing.T) {
	h := NewHistory(Grid{Cols: 10, Rows: 3}, 10)
	feed(h, "\x1b=\x1b[?66l")
	st, _, _ := h.State(0)
	if got := fmt.Sprint(st.Modes); got != "[{66 false}]" {
		t.Fatalf("modes = %s: ESC = then ?66l must end NUMERIC, as one setting in order", got)
	}
}
