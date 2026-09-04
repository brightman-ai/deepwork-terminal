package muxd

import (
	"math"
	"strconv"
)

// Grid is a terminal's size: the two numbers that are only ever meaningful together.
//
// They were carried as loose `cols, rows` pairs and as `*[2]uint16` / `*[2]int` almost
// everywhere geometry travels — two pairs of parallel fields on the server's Session alone,
// and callers reading `ev.Resize[0]`, which says nothing about which axis it is. A pair that
// always travels together, is always validated together, and is meaningless apart is a type;
// writing it as two variables means every function that touches it re-derives that fact, and
// one of them eventually gets the order wrong.
//
// It is deliberately NOT the wire format. The protocol structs keep their own `Cols`/`Rows`
// fields with their own JSON names and widths (see ResizeReq, ResizedPayload): the wire is a
// contract with other processes and other versions, and it does not get reshaped because the
// in-memory representation improved. Conversion happens at the boundary, in one place per
// direction, which is also where the uint16 clamping belongs.
type Grid struct {
	Cols int
	Rows int
}

// Zero reports whether this grid constrains nothing — an observer, not a window.
//
// A single method rather than `g.Cols <= 0 || g.Rows <= 0` at a dozen call sites, because the
// interesting part is that HALF a size is not a thing: one zero axis means the whole grid is
// absent, and spelling that out per site is how the two axes drift into being checked
// differently. See Session.SetAttachSize for the same rule stated for the wire.
func (g Grid) Zero() bool { return g.Cols <= 0 || g.Rows <= 0 }

// String is for logs and test failures: "80x24" reads at a glance, "{80 24}" does not.
func (g Grid) String() string { return strconv.Itoa(g.Cols) + "x" + strconv.Itoa(g.Rows) }

// gridFromWire converts a protocol pair into the in-memory representation.
func gridFromWire(cols, rows uint16) Grid { return Grid{Cols: int(cols), Rows: int(rows)} }

// Wire returns the pair as the protocol carries it. Negative or oversized values cannot
// survive the trip, so they are clamped here rather than silently truncated by the cast —
// a 70000-column window would otherwise arrive as 4464.
func (g Grid) Wire() (cols, rows uint16) {
	c, r := g.Cols, g.Rows
	if c < 0 {
		c = 0
	}
	if r < 0 {
		r = 0
	}
	if c > math.MaxUint16 {
		c = math.MaxUint16
	}
	if r > math.MaxUint16 {
		r = math.MaxUint16
	}
	return uint16(c), uint16(r)
}
