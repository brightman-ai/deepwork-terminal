package terminal

import (
	"testing"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// This layer's whole job is changing the SHAPE of a line for a consumer that cannot use the
// internal one, so the tests are about the shape — especially the multi-byte cases, which is the
// entire reason the conversion happens on this side of the wire.

func TestSegmentsOf_PlainLineIsOneDefaultSegment(t *testing.T) {
	got := segmentsOf(muxd.Line{Text: "hello world"})
	if len(got) != 1 || got[0].T != "hello world" || got[0].S != 0 {
		t.Fatalf("got %+v, want one default-styled segment", got)
	}
}

func TestSegmentsOf_EmptyLineHasNoSegments(t *testing.T) {
	if got := segmentsOf(muxd.Line{Text: ""}); len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestSegmentsOf_SplitsAtSpanBoundaries(t *testing.T) {
	// "red" styled 7, then the rest default.
	l := muxd.Line{Text: "red and black", Spans: []muxd.Span{{Start: 0, Style: 7}, {Start: 3, Style: 0}}}
	got := segmentsOf(l)
	if len(got) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(got), got)
	}
	if got[0].T != "red" || got[0].S != 7 {
		t.Fatalf("first segment = %+v", got[0])
	}
	if got[1].T != " and black" || got[1].S != 0 {
		t.Fatalf("second segment = %+v", got[1])
	}
}

func TestSegmentsOf_DefaultTextBeforeTheFirstSpanIsKept(t *testing.T) {
	// A span at offset 5 means the first five bytes were default-styled. Dropping them would
	// silently delete text from the user's history.
	l := muxd.Line{Text: "plain COLOURED", Spans: []muxd.Span{{Start: 6, Style: 3}}}
	got := segmentsOf(l)
	if len(got) != 2 || got[0].T != "plain " || got[0].S != 0 || got[1].T != "COLOURED" {
		t.Fatalf("got %+v", got)
	}
}

// The reason this conversion exists at all: JavaScript strings are UTF-16, so a byte offset
// crossing the boundary would cut Chinese and emoji in half. Splitting here means no offset ever
// leaves the process.
func TestSegmentsOf_MultibyteBoundariesSurvive(t *testing.T) {
	l := muxd.Line{Text: "中文x", Spans: []muxd.Span{{Start: 0, Style: 4}, {Start: 6, Style: 0}}}
	got := segmentsOf(l)
	if len(got) != 2 {
		t.Fatalf("got %d segments: %+v", len(got), got)
	}
	if got[0].T != "中文" {
		t.Fatalf("first segment = %q, want 中文 — the split landed mid-rune", got[0].T)
	}
	if got[1].T != "x" {
		t.Fatalf("second segment = %q", got[1].T)
	}
}

// The offsets arrive from another process. A malformed one must produce a short line, never a
// panic inside the server that owns every terminal on the machine.
func TestSegmentsOf_MalformedOffsetsCannotPanic(t *testing.T) {
	cases := [][]muxd.Span{
		{{Start: 99, Style: 1}},                      // past the end
		{{Start: 3, Style: 1}, {Start: 1, Style: 2}}, // out of order
		{{Start: 0, Style: 1}, {Start: 0, Style: 2}}, // zero-length run
	}
	for _, spans := range cases {
		got := segmentsOf(muxd.Line{Text: "abc", Spans: spans})
		for _, seg := range got {
			if seg.T == "" {
				t.Fatalf("emitted an empty segment for %+v", spans)
			}
		}
	}
}

func TestColorJSON_ThreeCasesAreDistinguishable(t *testing.T) {
	if got := colorJSON(muxd.ColorDefault); got != "" {
		t.Fatalf("default colour = %q, want empty (the client renders it as inherit)", got)
	}
	if got := colorJSON(muxd.IndexedColor(208)); got != "i208" {
		t.Fatalf("indexed = %q", got)
	}
	if got := colorJSON(muxd.RGBColor(0x0a, 0xb0, 0xff)); got != "#0ab0ff" {
		t.Fatalf("rgb = %q", got)
	}
	// Palette index 0 is black, NOT "no colour". Collapsing them would turn every black cell
	// into an unstyled one.
	if colorJSON(muxd.IndexedColor(0)) == colorJSON(muxd.ColorDefault) {
		t.Fatal("indexed black and the default colour must not serialise the same")
	}
}

func TestRuneIndexOf(t *testing.T) {
	cases := []struct {
		s    string
		byte int
		want int
	}{
		{"abc", 0, 0},
		{"abc", 2, 2},
		{"中文x", 6, 2}, // two runes precede byte 6
		{"中文x", 3, 1},
		{"abc", 99, 3}, // clamped
		{"abc", -5, 0}, // clamped
	}
	for _, tc := range cases {
		if got := runeIndexOf(tc.s, tc.byte); got != tc.want {
			t.Fatalf("runeIndexOf(%q, %d) = %d, want %d", tc.s, tc.byte, got, tc.want)
		}
	}
}

func TestAtoi64FallsBackInsteadOfFailing(t *testing.T) {
	// These are all "where am I scrolled to" hints. Refusing the whole request over a malformed
	// one would break scrolling in order to complain about a URL.
	if got := atoi64("", 500); got != 500 {
		t.Fatalf("empty = %d", got)
	}
	if got := atoi64("nonsense", 500); got != 500 {
		t.Fatalf("garbage = %d", got)
	}
	if got := atoi64("42", 500); got != 42 {
		t.Fatalf("valid = %d", got)
	}
	if got := atoi64("-3", 0); got != -3 {
		t.Fatalf("negative should pass through (the daemon clamps): %d", got)
	}
}
