package terminal

import "testing"

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
