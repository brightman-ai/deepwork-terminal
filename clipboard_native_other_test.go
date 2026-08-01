//go:build !darwin

package terminal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseFileURIList covers the Linux clipboard format. This is the only automated
// coverage the non-darwin reader gets — CI runs on ubuntu, but a runner has no clipboard
// helper installed, so readClipboardFilePaths itself always takes the "no tool" exit
// there. The parsing is where a real desktop's output would go wrong, so it is the part
// that gets pinned.
func TestParseFileURIList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "crlf terminated, as gnome and kde emit it",
			raw:  "file:///home/a/one.m4a\r\nfile:///home/a/two.m4a\r\n",
			want: []string{"/home/a/one.m4a", "/home/a/two.m4a"},
		},
		{
			name: "percent escapes are decoded — the path is handed to the agent verbatim",
			raw:  "file:///home/a/my%20recording%20%231.m4a",
			want: []string{"/home/a/my recording #1.m4a"},
		},
		{
			name: "comment lines are part of the format, not data",
			raw:  "# this is a comment\nfile:///home/a/one.m4a",
			want: []string{"/home/a/one.m4a"},
		},
		{
			name: "an explicit localhost host is still this machine",
			raw:  "file://localhost/home/a/one.m4a",
			want: []string{"/home/a/one.m4a"},
		},
		{
			name: "a file on another host is not on this disk",
			raw:  "file://fileserver/share/one.m4a",
			want: nil,
		},
		{
			name: "a copied hyperlink is not a file",
			raw:  "https://example.com/one.m4a",
			want: nil,
		},
		{name: "empty clipboard", raw: "", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseFileURIList([]byte(tc.raw)))
		})
	}
}
