package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

func TestParserBoundary_KnowsWhenTheStreamIsMidSequence(t *testing.T) {
	for _, tc := range []struct {
		chunks []string
		ground bool
	}{
		{[]string{"plain text"}, true},
		{[]string{"\x1b[3"}, false},
		{[]string{"\x1b[3", "1m"}, true},
		{[]string{"\x1b]0;title"}, false},
		{[]string{"\x1b]0;title\a"}, true},
		{[]string{"\x1b]0;title\x1b"}, false},
		{[]string{"\x1b]0;title\x1b\\"}, true},
		{[]string{"\xe4\xb8"}, false}, // half of 中
		{[]string{"\xe4\xb8", "\xad"}, true},
		{[]string{"\x1b(", "B"}, true},
		{[]string{"\x1b["}, false},
		{[]string{"\x1b[\x18"}, true}, // CAN aborts
		{[]string{"\x1bP1$r"}, false},
	} {
		var p parserBoundary
		for _, c := range tc.chunks {
			p.feed([]byte(c))
		}
		if p.atGround() != tc.ground {
			t.Errorf("%q: atGround=%v, want %v", tc.chunks, p.atGround(), tc.ground)
		}
	}
}

// The reviewer's case: the window ends inside `ESC [ 3`, the live frame that follows brings `1m`.
// The paste mode must not be inserted between them.
func TestRestoreBytes_PasteModeNeverLandsInsideASequence(t *testing.T) {
	vs := viewerSync{ring: []byte("text\x1b[3")}
	body, _ := restoreBytes(vs, true, true)
	want := restorePrelude + "\x1b[?2004h" + "text\x1b[3"
	if string(body) != want {
		t.Fatalf("restore = %q\nwant      %q", body, want)
	}
}

// The browser's parser works on code points, so a UTF-8 encoded C1 control is a control: C2 9B is
// CSI. A frame cut inside the SGR it starts is not a place to insert anything (codex review
// 2026-10-14: a late paste-mode assertion there printed the remainder as literal "1mRED").
func TestParserBoundary_UTF8EncodedC1ActsAsItsSevenBitSpelling(t *testing.T) {
	cases := []struct {
		name   string
		frames [][]byte
		ground []bool // atGround after each frame
	}{
		{"CSI (U+009B) split before its final byte", [][]byte{{0xc2, 0x9b, '3'}, []byte("1mRED")}, []bool{false, true}},
		{"CSI split between the two bytes of U+009B", [][]byte{{'a', 0xc2}, {0x9b, '3', '1'}, []byte("m")}, []bool{false, false, true}},
		{"OSC (U+009D) ended by ST (U+009C)", [][]byte{{0xc2, 0x9d}, []byte("0;title"), {0xc2, 0x9c}}, []bool{false, false, true}},
		{"a UTF-8 title inside an OSC stays inside it", [][]byte{{0x1b, ']', '0', ';'}, []byte("中文"), {0x07}}, []bool{false, false, true}},
		{"NEL (U+0085) is a single-character control", [][]byte{{0xc2, 0x85}}, []bool{true}},
		{"an ordinary two-byte character", [][]byte{[]byte("é")}, []bool{true}},
	}
	for _, tc := range cases {
		var p parserBoundary
		for i, f := range tc.frames {
			p.feed(f)
			if p.atGround() != tc.ground[i] {
				t.Errorf("%s: after frame %d atGround=%v, want %v", tc.name, i, p.atGround(), tc.ground[i])
			}
		}
	}
}

// Codex final review, 2026-10-14: two more places where the tracker said "ground" and xterm did not.
// A C0 control inside an escape sequence is executed and the sequence goes on; BEL ends an OSC but
// not a DCS (or SOS, PM, APC), which end only at ST.
func TestParserBoundary_C0InEscapeAndBELInDCS(t *testing.T) {
	cases := []struct {
		name   string
		frames [][]byte
		ground []bool
	}{
		{"ESC, then LF, then the rest of the CSI", [][]byte{{0x1b, '\n'}, []byte("[31mRED")}, []bool{false, true}},
		{"BEL inside a DCS is data", [][]byte{[]byte("\x1bPqabc\x07"), []byte("more\x1b\\")}, []bool{false, true}},
		{"BEL still ends an OSC", [][]byte{[]byte("\x1b]0;title\x07")}, []bool{true}},
		{"BEL inside an APC is data", [][]byte{[]byte("\x1b_x\x07"), []byte("\x1b\\")}, []bool{false, true}},
	}
	for _, tc := range cases {
		var p parserBoundary
		for i, f := range tc.frames {
			p.feed(f)
			if p.atGround() != tc.ground[i] {
				t.Errorf("%s: after frame %d atGround=%v, want %v", tc.name, i, p.atGround(), tc.ground[i])
			}
		}
	}
}

// A reused browser terminal can come back from 1049l with a DEC set designated and shifted in (the
// cursor restore brings both back). The prelude must leave every designation US and G0 in GL, or
// the restore's plain text is drawn as line-drawing glyphs.
func TestRestorePrelude_ResetsCharsetsAReusedTerminalLeftShiftedIn(t *testing.T) {
	g := muxd.Grid{Cols: 20, Rows: 3}
	term := muxd.NewTerminalModel(g, 100)
	term.Write([]byte("\x1b)0\x0e\x1b7\x1b[?1049h")) // G1 = DEC, SO, saved, then a TUI's alternate screen
	term.Write([]byte(restorePrelude + "hello"))
	st, _, ok := term.State(0)
	require.True(t, ok)
	require.Equal(t, "hello", strings.TrimRight(st.Lines[0].Text, " "))
}

// A keyframe whose only news is a designation is not pristine: the window's `SO qq` needs it.
func TestPristine_SeesADesignatedCharset(t *testing.T) {
	g := muxd.Grid{Cols: 20, Rows: 3}
	m := muxd.NewTerminalModel(g, 100)
	m.Write([]byte("\x1b)0"))
	st, sb, ok := m.State(0)
	require.True(t, ok)
	require.False(t, pristine(st, sb), "a designated G1 must be restated by the keyframe")
	fresh := muxd.NewTerminalModel(g, 100)
	st, sb, _ = fresh.State(0)
	require.True(t, pristine(st, sb), "a power-on terminal is still pristine")
}
