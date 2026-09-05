package muxd

import "testing"

// The style table is what makes coloured scrollback affordable, and it is also the one place a
// hostile stream can try to spend the daemon's memory. Both halves get tested: the compression
// has to work, and the cap has to hold without turning "the table is full" into an error any
// caller has to handle.

func TestStyleTable_DefaultIsAlwaysZero(t *testing.T) {
	tb := newStyleTable()
	// A line with no spans means "entirely default". That reading only holds if the default style
	// is id 0 and nothing else ever takes that slot.
	if id := tb.intern(DefaultStyle); id != DefaultStyleID {
		t.Fatalf("default style interned as %d", id)
	}
	if tb.style(DefaultStyleID) != DefaultStyle {
		t.Fatal("id 0 does not resolve to the default style")
	}
}

func TestStyleTable_InterningIsStableAndDeduplicating(t *testing.T) {
	tb := newStyleTable()
	red := Style{FG: IndexedColor(1)}
	a, b := tb.intern(red), tb.intern(red)
	if a != b {
		t.Fatalf("the same style interned as %d and %d", a, b)
	}
	if len(tb.list) != 2 {
		t.Fatalf("table holds %d styles after interning one", len(tb.list)-1)
	}
	if tb.style(a) != red {
		t.Fatalf("id %d resolves to %+v, want %+v", a, tb.style(a), red)
	}
}

func TestStyleTable_OutOfRangeIDResolvesToDefaultRatherThanPanicking(t *testing.T) {
	// Only reachable from a corrupt arena, and this runs inside the daemon that owns every live
	// shell on the machine. A bad byte in a history buffer must not be able to take that down.
	if got := newStyleTable().style(StyleID(60000)); got != DefaultStyle {
		t.Fatalf("got %+v, want the default style", got)
	}
}

// A truecolour gradient can mint a distinct style per CELL. Unbounded interning would turn that
// into an unbounded map inside a resident daemon.
func TestStyleTable_HoldsTheCapUnderATruecolorFlood(t *testing.T) {
	tb := newStyleTable()
	for r := 0; r < 256; r++ {
		for g := 0; g < 256; g++ {
			tb.intern(Style{FG: RGBColor(uint8(r), uint8(g), 0)})
		}
	}
	if len(tb.list) > maxStyles {
		t.Fatalf("table grew to %d, cap is %d", len(tb.list), maxStyles)
	}
	if tb.overflow == 0 {
		t.Fatal("degradation was not counted — a slightly off-colour history would be unexplainable")
	}
	// Every rung of the ladder still returns a usable id, so no caller has to handle "full".
	if id := tb.intern(Style{FG: RGBColor(3, 4, 5), Attrs: AttrBold}); int(id) >= len(tb.list) {
		t.Fatalf("intern returned an unusable id %d for a table of %d", id, len(tb.list))
	}
}

func TestColorQuantize(t *testing.T) {
	cases := []struct {
		name string
		in   Color
		want Color
	}{
		// Greys go to the 24-step ramp, which is finer than the cube's 6-step diagonal; sending
		// #808080 to the cube would visibly shift it.
		{"mid grey uses the ramp", RGBColor(0x80, 0x80, 0x80), IndexedColor(232 + (128-8)*24/231)},
		{"black clamps to cube black", RGBColor(0, 0, 0), IndexedColor(16)},
		{"white clamps to cube white", RGBColor(255, 255, 255), IndexedColor(231)},
		{"pure red hits the cube corner", RGBColor(255, 0, 0), IndexedColor(16 + 36*5)},
		{"pure green hits the cube corner", RGBColor(0, 255, 0), IndexedColor(16 + 6*5)},
		{"pure blue hits the cube corner", RGBColor(0, 0, 255), IndexedColor(16 + 5)},
		// Quantising a palette index or the default would be a loss with nothing to gain.
		{"indexed passes through", IndexedColor(42), IndexedColor(42)},
		{"default passes through", ColorDefault, ColorDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.quantize(); got != tc.want {
				t.Fatalf("quantize = %v (index %d), want %v (index %d)",
					got, got.Index(), tc.want, tc.want.Index())
			}
		})
	}
}

func TestColorEncodingRoundTrips(t *testing.T) {
	if c := IndexedColor(208); !c.IsIndexed() || c.Index() != 208 || c.IsRGB() || c.IsDefault() {
		t.Fatalf("indexed colour misencoded: %v", c)
	}
	c := RGBColor(10, 20, 30)
	r, g, b := c.RGB()
	if !c.IsRGB() || r != 10 || g != 20 || b != 30 {
		t.Fatalf("rgb colour misencoded: %v → %d,%d,%d", c, r, g, b)
	}
	if !ColorDefault.IsDefault() || ColorDefault.IsRGB() || ColorDefault.IsIndexed() {
		t.Fatal("the default colour must be exactly one of the three cases")
	}
	// Index 0 is black, not "no colour". Packing it as a bare 0 would make the two
	// indistinguishable and turn every black cell into an unstyled one.
	if IndexedColor(0).IsDefault() {
		t.Fatal("indexed black must not read as the default colour")
	}
}

func TestRuneWidth(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii", 'a', 1},
		{"latin accented", 'é', 1},
		{"cyrillic", 'ж', 1},
		{"cjk", '中', 2},
		{"hangul", '한', 2},
		{"fullwidth latin", 'Ａ', 2},
		{"katakana", 'ア', 2},
		{"emoji", '🚀', 2},
		{"box drawing stays narrow", '─', 1},
		{"check mark stays narrow", '✓', 1},
		{"combining acute", 0x0301, 0},
		{"zero width joiner", 0x200D, 0},
		{"soft hyphen is drawn", 0x00AD, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runeWidth(tc.r); got != tc.want {
				t.Fatalf("runeWidth(%q) = %d, want %d", tc.r, got, tc.want)
			}
		})
	}
}

func TestWideRangesAreSortedAndDisjoint(t *testing.T) {
	// runeWidth binary-searches this table. An unsorted or overlapping entry would not fail
	// loudly — it would silently mis-measure some scripts and mis-wrap their lines.
	for i, r := range wideRanges {
		if r[0] > r[1] {
			t.Fatalf("range %d is inverted: %04X-%04X", i, r[0], r[1])
		}
		if i > 0 && r[0] <= wideRanges[i-1][1] {
			t.Fatalf("range %d (%04X-%04X) overlaps or precedes range %d (%04X-%04X)",
				i, r[0], r[1], i-1, wideRanges[i-1][0], wideRanges[i-1][1])
		}
	}
}
