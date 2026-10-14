package muxd

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"unsafe"
)

// How the model reads what xterm.js 6.0.0 reads: character sets, bytes that are not UTF-8, marks
// that stack on the character before them, and control characters in the middle of a sequence.
//
// Every expected value below is what xterm.js headless 6.0.0 ended with for the same bytes (a
// Uint8Array write, as the browser does it), not what the model was seen to do; the random-stream
// comparison against the same oracle is xterm_parity_test.go, and this file pins the cases by name so
// that one of them failing says which rule broke.

// bs is bytes that are not necessarily text.
func bs(b ...byte) []byte { return b }

type inputCase struct {
	name   string
	chunks []any // string or []byte, written in order
	cols   int   // 0 means 20
	rows   int   // 0 means 4
	want   []string
	cursor [2]int // x, y
}

func runInputCases(t *testing.T, cases []inputCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := tc.cols, tc.rows
			if cols == 0 {
				cols = 20
			}
			if rows == 0 {
				rows = 4
			}
			v, _ := newTestVT(t, rows, cols)
			for _, c := range tc.chunks {
				switch c := c.(type) {
				case string:
					v.Write([]byte(c))
				case []byte:
					v.Write(c)
				}
			}
			got := v.textLines()
			if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", tc.want) || [2]int{v.col, v.row} != tc.cursor {
				t.Fatalf("rows %q cursor (x,y)=(%d,%d)\nwant   %q cursor %v", got, v.col, v.row, tc.want, tc.cursor)
			}
		})
	}
}

// ── Character sets ──────────────────────────────────────────────────────────────────────────────

func TestXtermInput_CharacterSets(t *testing.T) {
	runInputCases(t, []inputCase{
		{name: "a box drawn with DEC Special Graphics", chunks: []any{"\x1b(0lqqk\r\nx  x\r\nmqqj\x1b(B"}, want: []string{"┌──┐", "│  │", "└──┘"}, cursor: [2]int{4, 2}},
		{name: "SO shifts to G1 and SI back", chunks: []any{"\x1b)0a\x0eq\x0fq"}, want: []string{"a─q"}, cursor: [2]int{3, 0}},
		{name: "DECSC keeps the set in use, DECRC puts it back", chunks: []any{"\x1b(0\x1b7\x1b(Bl\r\n\x1b8l"}, want: []string{"┌"}, cursor: [2]int{1, 0}},
		{name: "DECRC restores the set, not the designations or the level", chunks: []any{"\x1b(0\x1b7\x1b(B\x1b8q\x0e\x0fq"}, want: []string{"─q"}, cursor: [2]int{2, 0}},
		{name: "a shift between DECSC and DECRC is undone by DECRC", chunks: []any{"\x1b)0\x0e\x1b7\x0f\x1b8q"}, want: []string{"─"}, cursor: [2]int{1, 0}},
		{name: "DECSTR returns to US ASCII", chunks: []any{"\x1b(0\x1b[!pq"}, want: []string{"q"}, cursor: [2]int{1, 0}},
		{name: "RIS returns to US ASCII", chunks: []any{"\x1b(0\x1bcq"}, want: []string{"q"}, cursor: [2]int{1, 0}},
		{name: "a flag xterm has no handler for changes nothing", chunks: []any{"\x1b(0\x1b(1q"}, want: []string{"─"}, cursor: [2]int{1, 0}},
		{name: "and on its own draws ASCII", chunks: []any{"\x1b(1q"}, want: []string{"q"}, cursor: [2]int{1, 0}},
		{name: "REP repeats the character that was drawn", chunks: []any{"\x1b(0q\x1b[3b"}, want: []string{"────"}, cursor: [2]int{4, 0}},
		{name: "CSI ? 2 h puts all four sets back to US", chunks: []any{"\x1b)0\x0e\x1b[?2hq"}, want: []string{"q"}, cursor: [2]int{1, 0}},
		{name: "LS2 selects G2", chunks: []any{"\x1b*0\x1bnq\x1b(Bq"}, want: []string{"──"}, cursor: [2]int{2, 0}},
		{name: "LS3 selects G3", chunks: []any{"\x1b+0\x1boq"}, want: []string{"─"}, cursor: [2]int{1, 0}},
		{name: "LS1R selects G1", chunks: []any{"\x1b)0\x1b~q"}, want: []string{"─"}, cursor: [2]int{1, 0}},
		{name: "ESC - designates G1 and ESC . designates G2", chunks: []any{"\x1b-0\x0eq\x0f\x1b.0\x1bnq"}, want: []string{"──"}, cursor: [2]int{2, 0}},
		{name: "ESC / designates nothing", chunks: []any{"\x1b/0\x1boq"}, want: []string{"q"}, cursor: [2]int{1, 0}},
		{name: "ESC % G selects the default set", chunks: []any{"\x1b(0\x1b%Gq"}, want: []string{"q"}, cursor: [2]int{1, 0}},
		{name: "the British set has a pound", chunks: []any{"\x1b(A#"}, want: []string{"£"}, cursor: [2]int{1, 0}},
		{name: "the Dutch set keeps the first letter of ij and maps a bar to f", chunks: []any{"\x1b(4[|"}, want: []string{"if"}, cursor: [2]int{2, 0}},
	})
}

// The model's tables are checked against what xterm draws for every printable ASCII character
// under every designating flag worth trying (testdata/xterm_charsets.json, made by the script next
// to the vector generator), through the real path: the designation, then the characters.
func TestXtermInput_CharacterSetTablesMatchXterm(t *testing.T) {
	data, err := os.ReadFile("testdata/xterm_charsets.json")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var golden map[string]string
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var ascii strings.Builder
	for c := 0x20; c < 0x7f; c++ {
		ascii.WriteByte(byte(c))
	}
	for flag, want := range golden {
		v := newVT(2, 120, newStyleTable())
		v.Write([]byte("\x1b(" + flag + ascii.String()))
		if got := v.textLines()[0]; got != strings.TrimRight(want, " ") {
			t.Errorf("ESC ( %s: row = %q, want %q", flag, got, want)
		}
	}
	if len(golden) < 20 {
		t.Fatalf("only %d sets in the golden file", len(golden))
	}
}

// ── Bytes that are not UTF-8 ────────────────────────────────────────────────────────────────────

func TestXtermInput_InvalidUTF8IsDroppedNotReplaced(t *testing.T) {
	runInputCases(t, []inputCase{
		{name: "a byte that starts nothing", chunks: []any{bs('a', 0xff, 'b')}, want: []string{"ab"}, cursor: [2]int{2, 0}},
		{name: "a lead byte followed by ASCII takes only itself", chunks: []any{bs('a', 0xe2, 'A', 'b')}, want: []string{"aAb"}, cursor: [2]int{3, 0}},
		{name: "an overlong two-byte form", chunks: []any{bs('a', 0xc0, 0x80, 'b')}, want: []string{"ab"}, cursor: [2]int{2, 0}},
		{name: "an overlong three-byte form", chunks: []any{bs('a', 0xe0, 0x80, 0x80, 'b')}, want: []string{"ab"}, cursor: [2]int{2, 0}},
		{name: "a surrogate", chunks: []any{bs('a', 0xed, 0xa0, 0x80, 'b')}, want: []string{"ab"}, cursor: [2]int{2, 0}},
		{name: "a value past U+10FFFF", chunks: []any{bs('a', 0xf4, 0x90, 0x80, 0x80, 'b')}, want: []string{"ab"}, cursor: [2]int{2, 0}},
		{name: "the byte order mark", chunks: []any{bs('a', 0xef, 0xbb, 0xbf, 'b')}, want: []string{"ab"}, cursor: [2]int{2, 0}},
		{name: "a U+FFFD that really is one is a character", chunks: []any{bs('a', 0xef, 0xbf, 0xbd, 'b')}, want: []string{"a�b"}, cursor: [2]int{3, 0}},
		{name: "a character split across two writes", chunks: []any{bs('a', 0xe2, 0x82), bs(0xac, 'b')}, want: []string{"a€b"}, cursor: [2]int{3, 0}},
		{name: "a cut character that the next write does not finish", chunks: []any{bs('a', 0xe2, 0x82), bs('x')}, want: []string{"ax"}, cursor: [2]int{2, 0}},
		{name: "a stray byte inside a CSI does not end it", chunks: []any{"\x1b[3", bs(0x80), "1mx"}, want: []string{"x"}, cursor: [2]int{1, 0}},
		{name: "a character that is not a parameter ends it, and is dropped", chunks: []any{"\x1b[3\u00e91mx"}, want: []string{"1mx"}, cursor: [2]int{3, 0}},
	})
}

func TestXtermInput_ACutCharacterIsHeldAsPending(t *testing.T) {
	h := NewHistory(Grid{Cols: 20, Rows: 3}, 10)
	h.Write(bs('a', 0xe2, 0x82))
	st, _, _ := h.State(0)
	if string(st.Pending) != "\xe2\x82" {
		t.Fatalf("pending = %q, want the two bytes of the cut character", st.Pending)
	}
}

// ── Marks that stack on the character before them ───────────────────────────────────────────────

func TestXtermInput_CombiningCharacters(t *testing.T) {
	const (
		acute          = "\u0301"
		circ           = "\u0302"
		zwj            = "\u200d"
		vs16           = "\ufe0f"
		smallHighAlJuz = "\u0898" // a mark to Unicode (14), absent from xterm's Unicode 11 table: a cell
		jamoMid        = "\u1160" // a letter to Unicode, zero-width to xterm
	)
	runInputCases(t, []inputCase{
		{name: "a mark stacks on the character before it", chunks: []any{"e" + acute + "x"}, want: []string{"e" + acute + "x"}, cursor: [2]int{2, 0}},
		{name: "after a sequence it has nothing to stack on and takes a column", chunks: []any{"e\x1b[m" + acute + "x"}, want: []string{"e" + acute + "x"}, cursor: [2]int{3, 0}},
		{name: "at the start of a row likewise", chunks: []any{acute + "x"}, want: []string{acute + "x"}, cursor: [2]int{2, 0}},
		{name: "after a carriage return likewise", chunks: []any{"e\r" + acute + "x"}, want: []string{acute + "x"}, cursor: [2]int{2, 0}},
		{name: "on a wide character", chunks: []any{"\u4e2d" + acute + "x"}, want: []string{"\u4e2d" + acute + "x"}, cursor: [2]int{3, 0}},
		{name: "two marks", chunks: []any{"e" + acute + circ + "x"}, want: []string{"e" + acute + circ + "x"}, cursor: [2]int{2, 0}},
		{name: "a joiner and a variation selector", chunks: []any{"a" + zwj + "b" + vs16 + "x"}, want: []string{"a" + zwj + "b" + vs16 + "x"}, cursor: [2]int{3, 0}},
		{name: "at the margin, on the last cell, without wrapping", chunks: []any{"abcdefghij" + acute + "x"}, cols: 10, want: []string{"abcdefghij" + acute, "x"}, cursor: [2]int{1, 1}},
		{name: "at the margin after a sequence it has no cell to go in, and the cursor still moves on", chunks: []any{"abcdefghij\x1b[m" + acute + "x"}, cols: 10, want: []string{"abcdefghij", "x"}, cursor: [2]int{1, 1}},
		{name: "DEL does not end the stacking", chunks: []any{"e\x7f" + acute + "x"}, want: []string{"e" + acute + "x"}, cursor: [2]int{2, 0}},
		{name: "REP repeats the character with its marks", chunks: []any{"e" + acute + "\x1b[2b"}, want: []string{"e" + acute + "e" + acute + "e" + acute}, cursor: [2]int{3, 0}},
		{name: "a mark on a space", chunks: []any{" " + acute + "x"}, want: []string{" " + acute + "x"}, cursor: [2]int{2, 0}},
		{name: "a character Unicode calls a mark that xterm's table does not is a cell", chunks: []any{"a" + smallHighAlJuz + "b"}, want: []string{"a" + smallHighAlJuz + "b"}, cursor: [2]int{3, 0}},
		{name: "a letter Unicode calls a letter that xterm's table lists stacks", chunks: []any{"a" + jamoMid + "b"}, want: []string{"a" + jamoMid + "b"}, cursor: [2]int{2, 0}},
	})
}

func TestXtermInput_WidthsByXtermsUnicode11Table(t *testing.T) {
	// A few that matter, named; every code point is checked in TestRuneWidthMatchesXtermUnicode11.
	for _, tc := range []struct {
		r    rune
		want int
	}{
		{0x0301, 0}, {0x200d, 0}, {0xfe0f, 0}, {0x20e3, 0}, {0x1160, 0}, {0x11ff, 0}, {0xe0100, 0}, {0x1d167, 0}, {0x3099, 0},
		{0x0a51, 0}, {0x061c, 0}, {0x180e, 0}, // marks in the Unicode 11 table (not in the Unicode 6 one)
		{0x0898, 1}, {0x00ad, 1}, {0x2329, 2}, {0x3040, 1}, {0x303f, 1}, {0x4dc0, 1}, {0xa960, 2},
		{0x1f916, 2}, {0x1f4b0, 2}, {0x1f9e0, 2}, {0x2705, 2}, {0x2728, 2}, {0x1f3fb, 2}, // emoji with a presentation of their own
		{0x23fa, 1}, {0x273b, 1}, {0x26a0, 1}, {0x1f1e6, 1}, // ⏺ ✻ ⚠ and a regional indicator stay narrow
	} {
		if got := runeWidth(tc.r); got != tc.want {
			t.Errorf("runeWidth(U+%04X) = %d, want %d", tc.r, got, tc.want)
		}
	}
}

// A cell is still eight bytes with room for marks: the grid of a long session is a lot of cells.
func TestXtermInput_ACellIsStillEightBytes(t *testing.T) {
	if n := unsafe.Sizeof(cell{}); n != 8 {
		t.Fatalf("sizeof(cell) = %d, want 8", n)
	}
}

// The text of a scrolled-off row has the marks in it, a lone mark is remembered as such, and the row
// reads back into the same cells.
func TestXtermInput_MarksSurviveTheStore(t *testing.T) {
	const acute, circ = "\u0301", "\u0302"
	h := NewHistory(Grid{Cols: 12, Rows: 2}, 100)
	feed(h, "e"+acute+"x\x1b[m"+acute+"y\r\n", "\u4e2d"+acute+circ+"z\r\n", "tail\r\n")
	lines, _ := h.Read(0, 10)
	if len(lines) < 2 {
		t.Fatalf("read %d lines", len(lines))
	}
	if want := "e" + acute + "x" + acute + "y"; lines[0].Text != want {
		t.Fatalf("line 0 = %q, want %q", lines[0].Text, want)
	}
	if fmt.Sprint(lines[0].Lone) != "[2]" {
		t.Fatalf("line 0 lone = %v, want the lone mark's column [2]", lines[0].Lone)
	}
	if want := "\u4e2d" + acute + circ + "z"; lines[1].Text != want {
		t.Fatalf("line 1 = %q, want %q", lines[1].Text, want)
	}
	// Back into cells: the stacked marks come back on their cells and the lone one in its own.
	back := decodeRow(lines[0], h.vt.combos)
	var cells []string
	for _, c := range back[:4] {
		cells = append(cells, string(c.r)+h.vt.combos.get(c.ext))
	}
	if got, want := fmt.Sprintf("%q", cells), fmt.Sprintf("%q", []string{"e" + acute, "x", acute, "y"}); got != want {
		t.Fatalf("decoded cells = %s, want %s", got, want)
	}
}

func TestXtermInput_ATerminalModelRestoredFromAStateContinuesLikeTheOriginal(t *testing.T) {
	a := NewHistory(Grid{Cols: 14, Rows: 4}, 100)
	feed(a,
		"\x1b(0\x1b)A\x0e", // DEC graphics in G0, British in G1, shifted out
		"e\u0301x\x1b[m\u0301y\r\n",
		"\x1b7lq\r\n",
	)
	st, scroll, ok := a.State(100)
	if !ok {
		t.Fatal("State failed")
	}
	if st.Charset == nil || st.Charset.G != [4]byte{'0', 'A', 0, 0} || st.Charset.Level != 1 || st.Charset.InUse != 'A' || st.Charset.Saved != 'A' {
		t.Fatalf("State.Charset = %+v, want G0=0 G1=A, shifted out, with A in use and saved", st.Charset)
	}
	b := NewHistory(Grid{Cols: 80, Rows: 24}, 100)
	b.Restore(st, scroll)
	sameState(t, "right after restore", a, b)
	rest := []string{"\x1b8lqk#\x0f#\x0e\u00e9z", "\x1b[mq\u0301\r\n"}
	feed(a, rest...)
	feed(b, rest...)
	sameState(t, "after the same continuation", a, b)
	sa, _, _ := a.State(100)
	sb, _, _ := b.State(100)
	if fmt.Sprintf("%+v", sa.Charset) != fmt.Sprintf("%+v", sb.Charset) || sa.JoinWidth != sb.JoinWidth {
		t.Fatalf("charset/join state differ: %+v %d vs %+v %d", sa.Charset, sa.JoinWidth, sb.Charset, sb.JoinWidth)
	}
}

func TestXtermInput_TheMarkTableIsBounded(t *testing.T) {
	v := newVT(3, 40, newStyleTable())
	// More distinct sequences than the table holds: nothing may fail, and the characters stay.
	var b strings.Builder
	for i := 0; i < maxCombos+50; i++ {
		b.WriteString("\x1b[H")
		b.WriteRune('a')
		b.WriteRune(rune(0x0300 + i%0x70))
		b.WriteRune(rune(0x0300 + (i/0x70)%0x70))
		b.WriteRune(rune(0x1dc0 + i/0x2000))
	}
	v.Write([]byte(b.String()))
	if n := len(v.combos.seqs); n > maxCombos {
		t.Fatalf("%d sequences in the table, the bound is %d", n, maxCombos)
	}
	if got := v.textLines()[0]; !strings.HasPrefix(got, "a") {
		t.Fatalf("row = %q", got)
	}
	// And one cell does not collect marks without end.
	v = newVT(3, 40, newStyleTable())
	v.Write([]byte("a" + strings.Repeat("\u0301", 500)))
	if n := len(v.combos.get(v.cells[0][0].ext)); n > maxMarkBytes+2 {
		t.Fatalf("one cell holds %d bytes of marks, the bound is %d", n, maxMarkBytes)
	}
}

// ── A control inside a sequence ─────────────────────────────────────────────────────────────────

func TestXtermInput_ControlsInsideSequences(t *testing.T) {
	runInputCases(t, []inputCase{
		{name: "a line feed inside a CSI is carried out and the sequence goes on", chunks: []any{"ab\x1b[1\nDcd"}, want: []string{"ab", " cd"}, cursor: [2]int{3, 1}},
		{name: "and a cursor-up after it lands a row higher", chunks: []any{"\x1b[2;1Hab\x1b[1\nAcd"}, want: []string{"", "abcd"}, cursor: [2]int{4, 1}},
		{name: "a carriage return", chunks: []any{"abc\x1b[\rD!"}, want: []string{"!bc"}, cursor: [2]int{1, 0}},
		{name: "a backspace", chunks: []any{"abc\x1b[2\bD!"}, want: []string{"!bc"}, cursor: [2]int{1, 0}},
		{name: "CAN abandons the sequence", chunks: []any{"x\x1b[3\x18my"}, want: []string{"xmy"}, cursor: [2]int{3, 0}},
		{name: "SUB abandons it too", chunks: []any{"x\x1b[3\x1amy"}, want: []string{"xmy"}, cursor: [2]int{3, 0}},
		{name: "ESC abandons it and begins another", chunks: []any{"\x1b[3\x1b[31mred"}, want: []string{"red"}, cursor: [2]int{3, 0}},
		{name: "DEL is ignored inside it", chunks: []any{"\x1b[3\x7f1mred"}, want: []string{"red"}, cursor: [2]int{3, 0}},
		{name: "a control between the intermediates of a charset designation", chunks: []any{"\x1b(\n0q"}, want: []string{"", "─"}, cursor: [2]int{1, 1}},
		{name: "a control between ESC and its final byte", chunks: []any{"ab\x1b\nMcd"}, want: []string{"abcd"}, cursor: [2]int{4, 0}},
		{name: "a control inside a sequence a write ends in the middle of, once", chunks: []any{"ab\x1b[1\n", "Dcd"}, want: []string{"ab", " cd"}, cursor: [2]int{3, 1}},
		{name: "a parameter after an intermediate ignores the whole sequence", chunks: []any{"\x1b[1 2qx"}, want: []string{"x"}, cursor: [2]int{1, 0}},
		{name: "a private marker after a parameter likewise", chunks: []any{"ab\x1b[1?hx"}, want: []string{"abx"}, cursor: [2]int{3, 0}},
		{name: "an ignored sequence is read to its end across writes", chunks: []any{"\x1b[1?h", "x"}, want: []string{"x"}, cursor: [2]int{1, 0}},
		{name: "ESC SP F is a sequence xterm has no function for", chunks: []any{"\x1b Fx"}, want: []string{"x"}, cursor: [2]int{1, 0}},
		{name: "so is ESC Z", chunks: []any{"\x1bZx"}, want: []string{"x"}, cursor: [2]int{1, 0}},
		{name: "a character that is not ASCII ends an escape and is dropped", chunks: []any{"\x1b\u00e9x"}, want: []string{"x"}, cursor: [2]int{1, 0}},
	})
}

// The control that was inside a held sequence has acted, and is not in what is held: reading the
// bytes again must not carry it out a second time.
func TestXtermInput_AHeldSequenceKeepsNoControlThatAlreadyActed(t *testing.T) {
	h := NewHistory(Grid{Cols: 20, Rows: 4}, 10)
	h.Write([]byte("ab\x1b[1\n"))
	st, _, _ := h.State(0)
	if st.CursorRow != 1 || st.CursorCol != 2 {
		t.Fatalf("cursor (%d,%d) after the line feed inside the held sequence, want (2,1): xterm has already carried it out", st.CursorCol, st.CursorRow)
	}
	if string(st.Pending) != "\x1b[1" {
		t.Fatalf("pending = %q, want the sequence without its control", st.Pending)
	}
	// A restore that is given that state and the continuation ends where the original does.
	b := NewHistory(Grid{Cols: 20, Rows: 4}, 10)
	b.Restore(st, nil)
	h.Write([]byte("Dcd"))
	b.Write([]byte("Dcd"))
	sameState(t, "after the sequence is finished", h, b)
	if got := rendered(func() []Line { s, _, _ := h.State(0); return s.Lines }(), nil); strings.Join(got, "|") != "ab| cd||" && strings.Join(got, "|") != "ab| cd" {
		t.Fatalf("rows %q, want ab / ' cd'", got)
	}
}

func TestXtermInput_C1Controls(t *testing.T) {
	runInputCases(t, []inputCase{
		{name: "NEL", chunks: []any{"a\u0085b"}, want: []string{"a", "b"}, cursor: [2]int{1, 1}},
		{name: "IND", chunks: []any{"a\u0084b"}, want: []string{"a", " b"}, cursor: [2]int{2, 1}},
		{name: "CSI begins a sequence", chunks: []any{"\u009b31mred"}, want: []string{"red"}, cursor: [2]int{3, 0}},
		{name: "the rest are not printed", chunks: []any{"a\u0080b\u0099c"}, want: []string{"abc"}, cursor: [2]int{3, 0}},
		{name: "one inside a CSI ends it, and is carried out", chunks: []any{"ab\x1b[1\u0085A"}, want: []string{"ab", "A"}, cursor: [2]int{1, 1}},
	})
}

// A string (OSC, DCS, SOS, PM, APC) ends where xterm says it does: at ESC, CAN, SUB and the C1
// terminator, and for OSC at BEL — and nowhere else. (Until a string ends nothing in it is text, so a
// terminator the model misses swallows everything after it.)
func TestXtermInput_StringsEndWhereXtermEndsThem(t *testing.T) {
	runInputCases(t, []inputCase{
		{name: "SOS ended by ESC, which begins a CSI", chunks: []any{"a\x1bXabc\x1b[31mred"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "SOS ended by ESC \\", chunks: []any{"a\x1bXabc\x1b\\red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "BEL does not end an SOS", chunks: []any{"a\x1bXabc\x07def\x1b\\red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "APC ended by CAN", chunks: []any{"a\x1b_abc\x18red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "BEL does not end a DCS", chunks: []any{"a\x1bPxx\x07yy\x1b\\red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "OSC ended by BEL", chunks: []any{"a\x1b]0;t\x07red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "OSC ended by CAN", chunks: []any{"a\x1b]0;t\x18red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "OSC ended by the ESC of a CSI", chunks: []any{"a\x1b]0;t\x1b[31mred"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "OSC ended by the C1 terminator", chunks: []any{"a\u009d0;t\u009cred"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "OSC ended by ESC \\", chunks: []any{"a\x1b]0;t\x1b\\red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "a control inside an OSC is not carried out", chunks: []any{"a\x1b]0;t\nx\x07red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "a C1 control ends an OSC and is carried out", chunks: []any{"ab\x1b]0;t\u0085red"}, want: []string{"ab", "red"}, cursor: [2]int{3, 1}},
		{name: "an OSC held across writes", chunks: []any{"a\x1b]0;ti", "tle\x07red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "an SOS held across writes", chunks: []any{"a\x1bXti", "tle\x1b\\red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "ESC ESC [ begins the CSI", chunks: []any{"a\x1b\x1b[31mred"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "a character that is not ASCII ends an SOS and is dropped", chunks: []any{"a\x1bX\u00e9red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "and a DCS before its final byte", chunks: []any{"a\x1bP\u00e9red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "and one inside its parameters", chunks: []any{"a\x1bP1;2\u00e9red"}, want: []string{"ared"}, cursor: [2]int{4, 0}},
		{name: "a DCS past its final byte takes it as content", chunks: []any{"a\x1bPq\u00e9red\x1b\\x"}, want: []string{"ax"}, cursor: [2]int{2, 0}},
		{name: "so does one that is being ignored", chunks: []any{"a\x1bP1<\u00e9red\x1b\\x"}, want: []string{"ax"}, cursor: [2]int{2, 0}},
		{name: "and an OSC", chunks: []any{"a\x1b]0;\u00e9\u4e2d\x07x"}, want: []string{"ax"}, cursor: [2]int{2, 0}},
	})
}

// Whatever the stream, the model must stay up: a panic in it switches the session's history off for
// good (History.guard). This throws sequences, bytes and marks at it in every combination a PTY read
// could cut them into, with resizes in between, on the reflowing model.
func TestXtermInput_NoStreamBreaksTheModel(t *testing.T) {
	frags := []string{
		"\x1b", "\x1b[", "\x1b]", "\x1bP", "\x1bX", "\x1b_", "\x1b(", "\x1b)0", "\x1b(0", "\x1b(B", "\x1b%G", "\x1bc", "\x1b7", "\x1b8", "\x1bM", "\x1bD", "\x1bE",
		"\x1b[1;2H", "\x1b[3A", "\x1b[2K", "\x1b[J", "\x1b[3J", "\x1b[4h", "\x1b[4l", "\x1b[?7l", "\x1b[?7h", "\x1b[?1049h", "\x1b[?1049l", "\x1b[?2h", "\x1b[!p",
		"\x1b[3@", "\x1b[2P", "\x1b[2X", "\x1b[5b", "\x1b[r", "\x1b[2;4r", "\x1b[31m", "\x1b[0m", "\x1b[1;2;3;4;5;6;7;8;9;10;11;12m", "\x1b[ q", "\x1b[$p", "\x1b[?",
		"\x0e", "\x0f", "\x18", "\x1a", "\x07", "\x08", "\t", "\n", "\r", "\x0b", "\x0c", "\x7f", "\x00",
		"a", "bc", "def ", "0123456789", " ", "~", "|", "[", "\\", "{", "lqk", "mqj", "x",
		"\u0301", "\u0302", "\u200d", "\ufe0f", "\u1160", "\U000e0100", "\U0001d167", "\u3099", "\u0a51", "\u00ad",
		"\u4e2d", "\u6587", "\u5b57", "\u00e9", "\u20ac", "\U00020000", "\U0001f600",
		"\u0080", "\u0084", "\u0085", "\u0088", "\u0090", "\u0098", "\u009b", "\u009c", "\u009d", "\u009e", "\u009f",
		"\xff", "\xfe", "\x80", "\xbf", "\xc0\x80", "\xc2", "\xe2\x82", "\xe2", "\xe0\x80\x80", "\xed\xa0\x80", "\xf0\x9f\x98", "\xf4\x90\x80\x80", "\xf8\x88\x80\x80\x80", "\xef\xbb\xbf",
	}
	rng := rand.New(rand.NewSource(20261014))
	for iter := 0; iter < 400; iter++ {
		g := Grid{Cols: 2 + rng.Intn(20), Rows: 2 + rng.Intn(8)}
		h := NewTerminalModel(g, 1000)
		var stream []byte
		for n := 20 + rng.Intn(60); n > 0; n-- {
			stream = append(stream, frags[rng.Intn(len(frags))]...)
			if rng.Intn(10) == 0 {
				stream = append(stream, byte(rng.Intn(256)))
			}
		}
		for len(stream) > 0 {
			n := min(len(stream), 1+rng.Intn(24))
			h.Write(stream[:n])
			stream = stream[n:]
			if rng.Intn(12) == 0 {
				h.Resize(Grid{Cols: 2 + rng.Intn(20), Rows: 2 + rng.Intn(8)})
			}
		}
		st, _, ok := h.State(1000)
		if !ok || h.Stats().Broken {
			t.Fatalf("iteration %d: the model broke: %+v", iter, h.Stats())
		}
		if st.CursorCol < 0 || st.CursorCol > st.Cols+1 || st.CursorRow < 0 || st.CursorRow >= st.Rows {
			t.Fatalf("iteration %d: cursor (%d,%d) outside a %dx%d screen", iter, st.CursorCol, st.CursorRow, st.Cols, st.Rows)
		}
		if len(st.Pending) > maxPendingBytes {
			t.Fatalf("iteration %d: %d bytes pending", iter, len(st.Pending))
		}
	}
}

// runeWidth against xterm itself: every code point a terminal can print, as the width xterm.js 6.0.0
// with the Unicode 11 addon active gives it (testdata/xterm_unicode11_widths.json, made by
// scripts/diag/xtermvectors/dump_widths.mjs). The table is copied from the addon, so this says the
// copy is whole — and it is what a Unicode 11 character that the tests below do not name is held to.
func TestRuneWidthMatchesXtermUnicode11(t *testing.T) {
	data, err := os.ReadFile("testdata/xterm_unicode11_widths.json")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var golden struct {
		Version string
		Runs    [][3]int
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if golden.Version != "11" {
		t.Fatalf("the widths file is for Unicode %q, want 11", golden.Version)
	}
	checked, bad := 0, 0
	for _, run := range golden.Runs {
		for cp := run[0]; cp <= run[1]; cp++ {
			checked++
			if got := runeWidth(rune(cp)); got != run[2] {
				if bad++; bad <= 10 {
					t.Errorf("runeWidth(U+%04X) = %d, xterm says %d", cp, got, run[2])
				}
			}
		}
	}
	if checked < 1_000_000 {
		t.Fatalf("only %d code points checked", checked)
	}
	if bad > 0 {
		t.Fatalf("%d of %d code points differ from xterm", bad, checked)
	}
}

// The browser's xterm is built with convertEol, which in xterm.js is LNM: a line feed returns the
// carriage. The terminal model starts that way; the archive model (NewHistory) does not.
func TestNewTerminalModel_StartsWithLineFeedReturningTheCarriage(t *testing.T) {
	text := func(h *History) []string {
		st, _, _ := h.State(0)
		var out []string
		for _, l := range modelLines(st.Lines) {
			out = append(out, l.T)
		}
		return out
	}
	term := NewTerminalModel(Grid{Cols: 20, Rows: 4}, 100)
	term.Write([]byte("ab\ncd\n"))
	if got := fmt.Sprintf("%q", text(term)[:3]); got != `["ab" "cd" ""]` {
		t.Fatalf("terminal model: %s, want each line at column 0", got)
	}
	if st, _, _ := term.State(0); !st.NewlineMode {
		t.Fatal("terminal model: NewlineMode is off")
	}
	archive := NewHistory(Grid{Cols: 20, Rows: 4}, 100)
	archive.Write([]byte("ab\ncd\n"))
	if got := fmt.Sprintf("%q", text(archive)[:3]); got != `["ab" "  cd" ""]` {
		t.Fatalf("archive model: %s, want the staircase a bare LF makes", got)
	}
	// It is an option: CSI 20 l turns it off, and RIS does not turn it back on (xterm leaves options alone).
	term.Write([]byte("\x1b[20l\x1b[2J\x1b[Hef\ngh\x1bc"))
	term.Write([]byte("ij\nkl"))
	if got := fmt.Sprintf("%q", text(term)[:2]); got != `["ij" "  kl"]` {
		t.Fatalf("after CSI 20 l and RIS: %s, want LNM to stay off", got)
	}
}

// A terminal model seeded from the daemon's snapshot keeps its convertEol: the snapshot's own LNM is
// the archive model's default (off), not the browser's.
func TestRestore_ATerminalModelKeepsConvertEol(t *testing.T) {
	archive := NewHistory(Grid{Cols: 20, Rows: 4}, 100)
	archive.Write([]byte("seed\r\n"))
	st, scroll, _ := archive.State(100)
	if st.NewlineMode {
		t.Fatal("the archive model starts with LNM on")
	}
	term := NewTerminalModel(Grid{Cols: 20, Rows: 4}, 100)
	term.Restore(st, scroll)
	term.Write([]byte("ab\ncd"))
	got, _, _ := term.State(0)
	if !got.NewlineMode || modelLines(got.Lines)[2].T != "cd" {
		t.Fatalf("after the restore: NewlineMode=%v rows %+v, want LNM kept (cd at column 0)", got.NewlineMode, modelLines(got.Lines))
	}
	// An archive model restored from the same state is restored as it was.
	plain := NewHistory(Grid{Cols: 20, Rows: 4}, 100)
	plain.Restore(st, scroll)
	if p, _, _ := plain.State(0); p.NewlineMode {
		t.Fatal("a restored archive model turned LNM on")
	}
}

// Colon sub-parameters belong to the parameter they follow: `1:31` is a 1 carrying a 31, not a 1 and a
// 31. Only 38, 48 and 58 (and 4's style) read them. Every expectation is what xterm.js headless 6.0.0
// ended with for `ESC [ <seq> m a` (cell 0's flags and colours).
func TestXtermInput_ColonSubParametersBelongToTheirParameter(t *testing.T) {
	idx := func(n uint8) Color { return IndexedColor(n) }
	for _, tc := range []struct {
		seq      string
		attrs    Attrs
		fg, bg   Color
		wantNote string
	}{
		{"1:31", AttrBold, ColorDefault, ColorDefault, "the 31 is the 1's, not a colour"},
		{"1;31", AttrBold, idx(1), ColorDefault, ""},
		{"2:9", AttrDim, ColorDefault, ColorDefault, "and not strike"},
		{"3:1;1", AttrItalic | AttrBold, ColorDefault, ColorDefault, ""},
		{"38:5:196", 0, idx(196), ColorDefault, ""},
		{"38;5;196", 0, idx(196), ColorDefault, ""},
		{"38:2::10:20:30", 0, RGBColor(10, 20, 30), ColorDefault, "with the colour space slot"},
		{"38:2:10:20:30", 0, RGBColor(20, 30, 0), ColorDefault, "without it xterm reads one place early"},
		{"38;2;10;20;30", 0, RGBColor(10, 20, 30), ColorDefault, ""},
		{"48:5:21", 0, ColorDefault, idx(21), ""},
		{"48:2::1:2:3;38:5:2", 0, idx(2), RGBColor(1, 2, 3), ""},
		{"4:3", AttrUnderline, ColorDefault, ColorDefault, "a style of underline"},
		{"4:3;31", AttrUnderline, idx(1), ColorDefault, ""},
		{"4m\x1b[4:0", 0, ColorDefault, ColorDefault, "4:0 turns it off"},
		{"4:0", 0, ColorDefault, ColorDefault, ""},
		{"1;38:5:9;4", AttrBold | AttrUnderline, idx(9), ColorDefault, "the next parameter is a parameter again"},
		{"58:5:9;1", AttrBold, ColorDefault, ColorDefault, "58 takes its colour and keeps nothing"},
		{"58;2;1;2;3;1", AttrBold, ColorDefault, ColorDefault, ""},
		{"38:5", 0, idx(0), ColorDefault, "a colour with its value missing is 0"},
		{"38;5", 0, idx(0), ColorDefault, ""},
		// Measured on @xterm/headless 6.0.0: the unknown mode takes the next five parameters with it
		// (its slot list is full), 6 means nothing to xterm, and 7 is inverse.
		{"38;1;2;3;4;5;6;7", AttrInverse, ColorDefault, ColorDefault, "an unknown colour mode takes what follows with it"},
		{"6", 0, ColorDefault, ColorDefault, "rapid blink is not one of xterm's"},
		// SGR 21 is a double underline in xterm.js 6.0.0, which the model draws as its one underline; 22 is the bold/dim reset.
		{"21", AttrUnderline, ColorDefault, ColorDefault, "not bold off"},
		{"1;21", AttrBold | AttrUnderline, ColorDefault, ColorDefault, ""},
		{"1;4;21;24", AttrBold, ColorDefault, ColorDefault, "24 ends either underline"},
		{"1;22", 0, ColorDefault, ColorDefault, ""},
		{"2;22", 0, ColorDefault, ColorDefault, ""},
		{"5", AttrBlink, ColorDefault, ColorDefault, ""},
	} {
		v := newVT(2, 10, newStyleTable())
		v.Write([]byte("\x1b[" + tc.seq + "ma"))
		st := v.styles.style(v.cells[0][0].style)
		if st.Attrs != tc.attrs || st.FG != tc.fg || st.BG != tc.bg {
			t.Errorf("ESC [ %s m: attrs %v fg %v bg %v; want attrs %v fg %v bg %v (%s)", tc.seq, st.Attrs, st.FG, st.BG, tc.attrs, tc.fg, tc.bg, tc.wantNote)
		}
	}
}

// An explicit `CSI 20 l` survives a snapshot: the daemon's archive model starts with LNM off, and a
// terminal model seeded from its state must tell the stream's word from a default. (xterm's answer for
// convertEol: true after `CSI 20 l`: "ab" / "  cd".)
func TestRestore_AnExplicitLNMSurvivesTheSnapshot(t *testing.T) {
	rows := func(h *History) []string {
		st, _, _ := h.State(0)
		var out []string
		for _, l := range modelLines(st.Lines) {
			out = append(out, l.T)
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		stream string
		want   []string
		set    bool
	}{
		{"reset by the stream", "\x1b[20l", []string{"ab", "  cd"}, true},
		{"set by the stream", "\x1b[20h", []string{"ab", "cd"}, true},
		{"set, then reset", "\x1b[20h\x1b[20l", []string{"ab", "  cd"}, true},
		{"never touched: the receiver's default", "", []string{"ab", "cd"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := NewHistory(Grid{Cols: 20, Rows: 4}, 100)
			archive.Write([]byte(tc.stream + "x\r\n"))
			st, scroll, _ := archive.State(100)
			if st.NewlineModeSet != tc.set {
				t.Fatalf("NewlineModeSet = %v, want %v", st.NewlineModeSet, tc.set)
			}
			term := NewTerminalModel(Grid{Cols: 20, Rows: 4}, 100)
			term.Restore(st, scroll)
			term.Write([]byte("\x1b[2J\x1b[Hab\ncd"))
			if got := rows(term)[:2]; fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("after the restore and a line feed between ab and cd: %q, want %q", got, tc.want)
			}
		})
	}
	// RIS leaves the option alone, as xterm does.
	h := NewHistory(Grid{Cols: 20, Rows: 4}, 100)
	h.Write([]byte("\x1b[20l\x1bc"))
	if st, _, _ := h.State(0); !st.NewlineModeSet || st.NewlineMode {
		t.Fatalf("after CSI 20 l and RIS: set=%v mode=%v, want the stream's choice kept", st.NewlineModeSet, st.NewlineMode)
	}
}
