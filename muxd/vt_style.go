package muxd

// Cell styling for the scrollback: how a character LOOKS, stored so that keeping colour costs
// almost nothing.
//
// ── Why interning ───────────────────────────────────────────────────────────────────────────────
// Storing style per cell is what makes coloured scrollback look unaffordable. A cell carrying
// rune(4B) + fg(4B) + bg(4B) + attrs(1B) is 13 bytes; a 200-column line is 2.6 KB; 50 000 lines is
// 130 MB per session, and this daemon holds a dozen. That arithmetic is why the first cut of this
// feature proposed dropping colour entirely.
//
// But a terminal session uses a TINY number of DISTINCT styles — a shell, git and a TUI together
// land around 20–80 for a whole day of work. So the style does not belong in the cell; an INDEX
// into a per-session table does. That takes the per-cell cost from 12 bytes to 2, and the table
// itself to about a kilobyte. Combined with run-length spans (see lineStore) the coloured line
// ends up within ~15% of the plain-text one, and colour stops being a trade-off.
//
// ── Why a hard cap, and a ladder under it ───────────────────────────────────────────────────────
// "The set of styles is tiny" is true of real programs and false of adversarial ones: anything
// emitting truecolour gradients (`lolcat`, a progress bar cycling hues, a syntax highlighter on a
// 24-bit theme) can mint a fresh (fg,bg,attrs) triple per CELL. Unbounded interning turns that
// into an unbounded map inside a daemon that is supposed to be the stable thing on the machine.
//
// So the table is capped, and when it fills we DEGRADE rather than refuse: quantise 24-bit colour
// to the 256-colour cube, then drop background, then keep attributes only, then fall back to the
// default style. Every rung still returns a usable id, so the emulator never has to handle "no
// style available" — and the degradation is deterministic, which keeps the tests honest.

// Attrs is the set of non-colour cell attributes, as a bitfield.
//
// One uint16 rather than eight bools: it is half the size of the smallest struct Go would give
// eight bools, it compares and hashes in one instruction (the table's map key is a Style), and
// SGR itself is defined as a set of independent switches — the bitfield is the domain's own shape.
type Attrs uint16

const (
	AttrBold Attrs = 1 << iota
	AttrDim
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrInverse
	AttrHidden
	AttrStrike
)

// Color is a cell colour: default, one of the 256 palette indices, or 24-bit RGB.
//
// The three cases are packed into one uint32 with a tag in the high byte instead of a struct with
// a kind field, because Style is a map key: a packed integer keeps Style comparable and 8 bytes
// wide, and "default" stays the zero value — which is what makes the zero Style the default style
// and therefore StyleID 0 (see newStyleTable).
//
// The palette is deliberately NOT resolved to RGB here. Index 2 means "the terminal's green", and
// which green that is belongs to the viewer's theme, not to the daemon's storage. Resolving it at
// write time would bake today's theme into history that outlives it.
type Color uint32

const (
	// ColorDefault is "whatever the terminal's default is" — not black, not white.
	ColorDefault Color = 0

	colorTagIndexed Color = 1 << 24
	colorTagRGB     Color = 2 << 24
	colorTagMask    Color = 0xff << 24
	colorValueMask  Color = 0x00ffffff
)

// IndexedColor is one of the 256 palette entries (0–15 the ANSI set, 16–231 the cube, 232–255 the
// greyscale ramp).
func IndexedColor(i uint8) Color { return colorTagIndexed | Color(i) }

// RGBColor is a 24-bit colour, as sent by `ESC [ 38;2;r;g;b m`.
func RGBColor(r, g, b uint8) Color {
	return colorTagRGB | Color(r)<<16 | Color(g)<<8 | Color(b)
}

// IsDefault reports whether this colour is the terminal's default (the zero value).
func (c Color) IsDefault() bool { return c == ColorDefault }

// IsIndexed reports whether this colour is a palette index; Index returns which one.
func (c Color) IsIndexed() bool { return c&colorTagMask == colorTagIndexed }

// Index is the palette entry, valid only when IsIndexed.
func (c Color) Index() uint8 { return uint8(c & 0xff) }

// IsRGB reports whether this colour is 24-bit; RGB returns the components.
func (c Color) IsRGB() bool { return c&colorTagMask == colorTagRGB }

// RGB is the colour's components, valid only when IsRGB.
func (c Color) RGB() (r, g, b uint8) {
	v := c & colorValueMask
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}

// quantize maps a 24-bit colour onto the nearest 256-colour palette entry, so a truecolour stream
// can keep being interned after the table fills instead of collapsing straight to default.
//
// The mapping is xterm's own layout, in reverse: the 6×6×6 cube for colour, the 24-step ramp for
// greys. Greys are detected before the cube because the cube's diagonal is a coarse 6 steps while
// the ramp gives 24 — sending `#808080` to the cube would visibly shift it.
//
// Non-RGB colours pass through unchanged: quantising an index or the default would be a loss with
// nothing to gain.
func (c Color) quantize() Color {
	if !c.IsRGB() {
		return c
	}
	r, g, b := c.RGB()
	// Near-grey: use the 24-step ramp (indices 232–255), which is finer than the cube's diagonal.
	if maxU8(r, g, b)-minU8(r, g, b) <= 8 {
		lum := (int(r) + int(g) + int(b)) / 3
		switch {
		case lum < 8:
			return IndexedColor(16) // cube black — the ramp does not reach true black
		case lum > 238:
			return IndexedColor(231) // cube white
		default:
			return IndexedColor(uint8(232 + (lum-8)*24/231))
		}
	}
	q := func(v uint8) int {
		// The cube's steps are 0,95,135,175,215,255 — not evenly spaced, so the boundaries
		// are the midpoints between them rather than v*6/256.
		switch {
		case v < 48:
			return 0
		case v < 115:
			return 1
		default:
			return int(v-35) / 40
		}
	}
	return IndexedColor(uint8(16 + 36*q(r) + 6*q(g) + q(b)))
}

func maxU8(a, b, c uint8) uint8 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func minU8(a, b, c uint8) uint8 {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// Style is how one cell looks. Comparable and 8 bytes wide so it can be a map key.
type Style struct {
	FG    Color
	BG    Color
	Attrs Attrs
}

// DefaultStyle is the zero value, and always interns to StyleID 0.
var DefaultStyle = Style{}

// IsDefault reports whether nothing about this style differs from the terminal's default. Callers
// use it to skip emitting a span at all, which is why most stored lines carry no spans.
func (s Style) IsDefault() bool { return s == DefaultStyle }

// StyleID indexes a session's style table. Zero is ALWAYS the default style — that invariant is
// what lets a line with no spans mean "entirely default" rather than "spans not recorded".
type StyleID uint16

// DefaultStyleID is the table entry every table starts with.
const DefaultStyleID StyleID = 0

// maxStyles bounds one session's style table.
//
// 4096 is far above what real programs use (tens) and far below what a truecolour stream would
// mint (millions). It also fits the degradation ladder: after quantisation the reachable space is
// 257 foregrounds × 257 backgrounds × 256 attribute combinations, so the cap has to be a cap
// rather than a generous allowance — see styleTable.intern.
const maxStyles = 4096

// styleTable interns styles for ONE session.
//
// Per session, not global: the ids are stored in that session's lines, and a global table would
// make one session's history depend on another session's colours — including for eviction, which
// must never be able to invalidate ids already written into an arena. Ids are therefore
// append-only for the life of the session and never reused.
type styleTable struct {
	ids  map[Style]StyleID
	list []Style
	// overflow counts styles that could not be interned as requested and had to be degraded.
	// Kept as a number rather than a log line because it is per-session and unbounded in
	// principle; a session that reports overflow is a session whose history is slightly
	// off-colour, which is worth being able to see without being worth a log flood.
	overflow int64
}

func newStyleTable() *styleTable {
	return &styleTable{
		ids:  map[Style]StyleID{DefaultStyle: DefaultStyleID},
		list: []Style{DefaultStyle},
	}
}

// intern returns the id for a style, adding it if the table has room.
//
// When the table is full it walks a ladder of progressively cheaper approximations rather than
// failing: quantise RGB → drop the background → attributes only → default. Every rung returns a
// valid id, so no caller ever has to deal with "the table is full"; the cost of an adversarial
// stream is a gradual loss of fidelity in that one session's history, not unbounded memory and
// not a broken terminal.
func (t *styleTable) intern(s Style) StyleID {
	if id, ok := t.ids[s]; ok {
		return id
	}
	if len(t.list) < maxStyles {
		id := StyleID(len(t.list))
		t.list = append(t.list, s)
		t.ids[s] = id
		return id
	}
	t.overflow++
	// Rung 1: 24-bit → palette. Collapses a gradient onto at most 256 entries, which is the
	// difference between "this session mints styles forever" and "this session stops minting".
	if q := (Style{FG: s.FG.quantize(), BG: s.BG.quantize(), Attrs: s.Attrs}); q != s {
		if id, ok := t.ids[q]; ok {
			return id
		}
	}
	// Rung 2: background is the less informative half of a colour pair for reading text back.
	if id, ok := t.ids[Style{FG: s.FG.quantize(), Attrs: s.Attrs}]; ok {
		return id
	}
	// Rung 3: attributes survive colour loss — bold/underline still carry meaning in plain text.
	if id, ok := t.ids[Style{Attrs: s.Attrs}]; ok {
		return id
	}
	return DefaultStyleID
}

// style resolves an id back to its style. An out-of-range id (only reachable from a corrupt
// arena) resolves to the default rather than panicking: this runs inside the daemon that owns
// every live shell on the machine, and a bad byte in a history buffer must not be able to take
// that down.
func (t *styleTable) style(id StyleID) Style {
	if int(id) < len(t.list) {
		return t.list[int(id)]
	}
	return DefaultStyle
}

// snapshot copies the table for a reader. Callers serialise it to clients, which must not hold a
// reference into a table the write path keeps appending to.
func (t *styleTable) snapshot() []Style {
	out := make([]Style, len(t.list))
	copy(out, t.list)
	return out
}

// approxBytes is the table's own memory footprint, for the accounting in History.Stats. The map
// dominates and Go gives no exact figure, so this is a documented estimate: one Style (8 B) in
// the slice plus roughly a map entry (key 8 B + value 2 B + bucket overhead ≈ 32 B) each.
func (t *styleTable) approxBytes() int { return len(t.list) * (8 + 32) }
