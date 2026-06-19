package terminal

import "testing"

func TestStripDeviceQueries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Queries that elicit a terminal response → stripped.
		{"cursor position DSR", "abc\x1b[6ndef", "abcdef"},
		{"status DSR", "\x1b[5nX", "X"},
		{"dec DSR", "\x1b[?6nX", "X"},
		{"primary DA bare", "\x1b[cX", "X"},
		{"primary DA 0", "\x1b[0cX", "X"},
		{"secondary DA", "\x1b[>0cX", "X"},
		{"tertiary DA", "\x1b[=cX", "X"},
		{"xtversion", "\x1b[>qZ", "Z"},
		{"kitty query", "\x1b[?uK", "K"},
		{"decrqm", "\x1b[?2026$pW", "W"},
		{"bg color query BEL", "\x1b]11;?\x07Y", "Y"},
		{"fg color query ST", "\x1b]10;?\x1b\\Y", "Y"},
		{"palette color query", "\x1b]4;1;?\x07Y", "Y"},

		// Visual / non-query sequences that must be PRESERVED.
		{"DECSCUSR cursor style", "\x1b[2 q", "\x1b[2 q"},
		{"SGR color", "\x1b[31mhi\x1b[0m", "\x1b[31mhi\x1b[0m"},
		{"cursor move", "\x1b[10;20H", "\x1b[10;20H"},
		{"OSC color set", "\x1b]11;rgb:1e/1e/1e\x07", "\x1b]11;rgb:1e/1e/1e\x07"},
		{"OSC title set", "\x1b]0;my title\x07", "\x1b]0;my title\x07"},
		{"DA response left intact", "\x1b[?1;2c", "\x1b[?1;2c"},
		{"plain text", "hello world", "hello world"},

		// Mixed: real screen draw with an embedded query.
		{"draw with query", "\x1b[Hline1\x1b[6n\x1b[2;1Hline2", "\x1b[Hline1\x1b[2;1Hline2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(stripDeviceQueries([]byte(c.in)))
			if got != c.want {
				t.Errorf("stripDeviceQueries(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
