package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// The drawing rules of serializeKeyframe, one at a time: each case writes a stream into the screen
// model, restores it into a fresh model, and requires the two to hold the same state, cell by cell
// (compareModelStates). The cases that are about HOW a state is reached also look at the bytes,
// because the state alone would be reached by writing a space for every skipped cell.

type rowCase struct {
	name       string
	cols, rows int
	stream     string
	history    int      // how much scrollback the keyframe takes (0 = all, -1 = none)
	has        []string // must appear in the restore
	lacks      []string // must not
	// holds says the stream made the state this case is about; without it a changed model could
	// turn the case into a test of nothing.
	holds func(st muxd.TermState, scroll []muxd.Line) bool
}

// allRows is the scrollback and the screen, top to bottom.
func allRows(st muxd.TermState, scroll []muxd.Line) []muxd.Line {
	return append(append([]muxd.Line(nil), scroll...), st.Lines...)
}

func anyRow(st muxd.TermState, scroll []muxd.Line, f func(i int, ln muxd.Line) bool) bool {
	for i, ln := range allRows(st, scroll) {
		if f(i, ln) {
			return true
		}
	}
	return false
}

// hasEmptyIn says some row holds a never-written cell in a style that satisfies f.
func hasEmptyIn(st muxd.TermState, scroll []muxd.Line, f func(muxd.Style) bool) bool {
	return anyRow(st, scroll, func(_ int, ln muxd.Line) bool {
		it := newRowReader(ln, st.Cols)
		for {
			_, r, _, id, ok := it.next()
			if !ok {
				return false
			}
			if r == 0 && int(id) < len(st.Styles) && f(st.Styles[id]) {
				return true
			}
		}
	})
}

func wrappedRows(st muxd.TermState, scroll []muxd.Line) int {
	n := 0
	anyRow(st, scroll, func(_ int, ln muxd.Line) bool {
		if ln.Wrapped {
			n++
		}
		return false
	})
	return n
}

func TestKeyframeRows_RoundTripAndBytes(t *testing.T) {
	cases := []rowCase{
		{
			name: "a wrapped row continues its line instead of starting a new one",
			cols: 10, rows: 4,
			stream: "abcdefghijklmnopqrstuvwxy",
			has:    []string{"abcdefghijklmnopqrstuvwxy"},
			holds:  func(st muxd.TermState, sc []muxd.Line) bool { return wrappedRows(st, sc) == 2 },
		},
		{
			name: "cells never written are moved over, not typed",
			cols: 20, rows: 3,
			stream: "a\x1b[10Cb",
			has:    []string{"a\x1b[10Cb"},
			lacks:  []string{"          "},
			holds:  func(st muxd.TermState, _ []muxd.Line) bool { return len(st.Lines[0].Nulls) == 2 },
		},
		{
			name: "cells erased in a colour are erased again, not typed as coloured spaces",
			cols: 12, rows: 3,
			stream: "\x1b[3Cabc\x1b[1G\x1b[44m\x1b[3X\x1b[0m",
			has:    []string{"\x1b[3X"},
			lacks:  []string{"   "},
			holds: func(st muxd.TermState, sc []muxd.Line) bool {
				return hasEmptyIn(st, sc, func(s muxd.Style) bool { return !s.BG.IsDefault() })
			},
		},
		{
			name: "a wide character that did not fit leaves its row short and continues on the next",
			cols: 5, rows: 4,
			stream: "abcd中xy",
			lacks:  []string{"abcd\r\n"},
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return st.Lines[1].Wrapped && st.Lines[0].Content == 4
			},
		},
		{
			name: "a coloured wide character that did not fit leaves the cell it skipped in its own colour",
			cols: 5, rows: 4,
			stream: "abcd\x1b[31m中xy\x1b[0m",
			holds: func(st muxd.TermState, sc []muxd.Line) bool {
				return st.Lines[1].Wrapped && hasEmptyIn(st, sc, func(s muxd.Style) bool { return s.FG != muxd.ColorDefault })
			},
		},
		{
			name: "a continued row whose predecessor ends in an erased cell",
			cols: 6, rows: 4,
			stream: "abcdefg\x1b[1;6H\x1b[X",
			has:    []string{"\x1b[A"},
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return st.Lines[1].Wrapped && !writtenAt(st.Lines[0], 5)
			},
		},
		{
			name: "scrollback that starts in the middle of a line",
			cols: 10, rows: 3,
			stream: strings.Repeat("0123456789", 5),
			// fifty characters make five rows of one line; with no history taken, the screen's first
			// row is a continuation with nothing above it
			history: -1,
			holds:   func(st muxd.TermState, sc []muxd.Line) bool { return len(sc) == 0 && st.Lines[0].Wrapped },
		},
		{
			name: "a character printed over the second half of a wide one empties the first half in its pen",
			cols: 8, rows: 3,
			stream: "中\x1b[1;2H\x1b[31mX\x1b[0m",
			holds: func(st muxd.TermState, sc []muxd.Line) bool {
				return hasEmptyIn(st, sc, func(s muxd.Style) bool { return s.FG != muxd.ColorDefault })
			},
		},
		{
			name: "a character printed over the first half of a wide one empties the second half in its pen",
			cols: 8, rows: 3,
			stream: "ab中\x1b[1;3H\x1b[32mY\x1b[0m",
			holds: func(st muxd.TermState, sc []muxd.Line) bool {
				return hasEmptyIn(st, sc, func(s muxd.Style) bool { return s.FG != muxd.ColorDefault })
			},
		},
		{
			name: "a wide character printed over the first half of another",
			cols: 8, rows: 3,
			stream: "a中中\x1b[1;3H\x1b[33m宽\x1b[0m",
			holds: func(st muxd.TermState, sc []muxd.Line) bool {
				return hasEmptyIn(st, sc, func(s muxd.Style) bool { return s.FG != muxd.ColorDefault })
			},
		},
		{
			name: "a character typed over the second half of a wide one in the last column (autowrap off)",
			cols: 6, rows: 3,
			stream: "abcd中\x1b[?7lZ\x1b[?7h",
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return len(st.Lines[0].Hidden) == 1 && st.Lines[0].Hidden[0].R == 'Z'
			},
		},
		{
			name: "a wide character skipped at the margin (autowrap off) leaves an empty cell",
			cols: 6, rows: 3,
			stream: "\x1b[?7labcd中宽\x1b[?7h",
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return len(st.Lines[0].Hidden) == 1 && st.Lines[0].Hidden[0].R <= 0
			},
		},
		{
			name: "the same, with a row continuing below it",
			cols: 6, rows: 4,
			stream: "abcd中宽\x1b[1;1H\x1b[?7labcd中宽\x1b[?7h",
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return len(st.Lines[0].Hidden) == 1 && st.Lines[1].Wrapped
			},
		},
		{
			name: "insert and newline modes are stated again",
			cols: 10, rows: 3,
			stream: "\x1b[4h\x1b[20hab\r",
			has:    []string{"\x1b[20h", "\x1b[4h"},
			holds:  func(st muxd.TermState, _ []muxd.Line) bool { return st.InsertMode && st.NewlineMode },
		},
		{
			name: "a zero-width character in a cell of its own, at the start of a row that continues the one above",
			cols: 5, rows: 4,
			stream: "abcdef\x1b[2;1H\u0301",
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return st.Lines[1].Wrapped && len(st.Lines[1].Lone) == 1 && st.Lines[1].Lone[0] == 0
			},
		},
		{
			name: "designated character sets and the shift are stated again",
			cols: 10, rows: 3,
			stream: "\x1b(0\x1b)A\x0eq",
			has:    []string{"\x1b(0", "\x1b)A", "\x0e"},
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return st.Charset != nil && st.Charset.G[0] == '0' && st.Charset.G[1] == 'A' && st.Charset.Level == 1
			},
		},
		{
			name: "the set DECRC put back, which no designation holds",
			cols: 10, rows: 3,
			stream: "\x1b(0\x1b7\x1b(B\x1b8",
			has:    []string{"\x1b8"},
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return st.Charset != nil && st.Charset.InUse == '0' && st.Charset.G[0] == 0 && st.Charset.Saved == '0'
			},
		},
		{
			name: "a narrow character that marks widened to two columns at the margin (autowrap off)",
			cols: 6, rows: 3,
			stream: "abcde\x1b[?7l中\u200d\x1b[?7h",
			holds: func(st muxd.TermState, _ []muxd.Line) bool {
				return len(st.Lines[0].Hidden) == 1 && st.Lines[0].Hidden[0].Col == 5 && strings.Contains(st.Lines[0].Text, "e\u200d")
			},
		},
		{
			name: "marks stay on their character",
			cols: 10, rows: 3,
			stream: "e\u0301x\u200d\u0308y",
			has:    []string{"e\u0301", "x\u200d\u0308"},
		},
		{
			name: "marks stay on their character in a row with cells never written",
			cols: 12, rows: 3,
			stream: "e\u0301\x1b[3Cx\u200d\u0308y",
			has:    []string{"e\u0301", "x\u200d\u0308"},
			holds:  func(st muxd.TermState, _ []muxd.Line) bool { return len(st.Lines[0].Nulls) == 2 },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := muxd.NewTerminalModel(muxd.Grid{Cols: c.cols, Rows: c.rows}, 100_000)
			h.Write([]byte(c.stream))
			limit := max(c.history, 0)
			if c.history == 0 {
				limit = 100_000
			}
			a, scrollA, ok := h.State(limit)
			require.True(t, ok)
			if c.holds != nil {
				require.True(t, c.holds(a, scrollA), "the stream no longer makes the state this case is about")
			}
			restore := restorePrelude + string(serializeKeyframe(a, scrollA))
			for _, s := range c.has {
				require.Contains(t, restore, s)
			}
			for _, s := range c.lacks {
				require.NotContains(t, restore, s)
			}
			g := muxd.NewTerminalModel(muxd.Grid{Cols: c.cols, Rows: c.rows}, 100_000)
			g.Write([]byte(restore))
			b, scrollB, ok := g.State(limit)
			require.True(t, ok)
			require.Empty(t, compareModelStates(a, scrollA, b, scrollB, map[string]int{}), "restore: %q", restore)
		})
	}
}
