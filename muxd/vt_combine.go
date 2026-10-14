package muxd

import "unicode/utf8"

// Combining characters: a mark that has no column of its own is stored with the character before it.
//
// xterm keeps such a cell as a string ("e" + U+0301) and a text read of the row — translateToString,
// which is what copy, search and a restore all use — returns it with the marks in place. A cell here
// is eight bytes (a rune and a style id), so the marks live in a table next to the grid and the cell
// carries an id into it: `ext`, in what would otherwise be padding. Cells stay plain values that can
// be copied, shifted and compared like any other, and a screen without a single mark costs nothing.
//
// The table is per model (a stream of Thai or Arabic text interns a few hundred distinct sequences),
// append-only, and bounded: past maxCombos distinct sequences, or when a single cell has collected
// maxMarkBytes of them, further marks are dropped. That loses the mark's glyph and never its
// effect on layout — a mark has no width — and it keeps hostile "zalgo" input from growing a
// daemon that owns every shell on the machine.
const (
	maxCombos    = 8192
	maxMarkBytes = 96
	// extWide, the top bit of ext, says the cell spans two columns whatever its character's width is
	// — what xterm does to the cell a mark lands on when the character before the mark was a wide one
	// that autowrap-off dropped at the margin (the mark takes that character's width, 2, onto the
	// narrow cell it stacks on). The ids below it never reach that bit.
	extWide uint16 = 1 << 15
)

// comboTable interns the mark sequences of a model's cells; id 0 is "none".
type comboTable struct {
	seqs []string // seqs[id-1]
	ids  map[string]uint16
}

func newComboTable() *comboTable { return &comboTable{ids: make(map[string]uint16)} }

// get returns the marks of a cell, given its ext: "" for none and for an id this table does not hold.
func (t *comboTable) get(ext uint16) string {
	id := ext &^ extWide
	if t == nil || id == 0 || int(id) > len(t.seqs) {
		return ""
	}
	return t.seqs[id-1]
}

// intern returns the id of marks, adding it if there is room; 0 means there was none.
func (t *comboTable) intern(marks string) uint16 {
	if t == nil || marks == "" {
		return 0
	}
	if id, ok := t.ids[marks]; ok {
		return id
	}
	if len(t.seqs) >= maxCombos {
		return 0
	}
	t.seqs = append(t.seqs, marks)
	id := uint16(len(t.seqs))
	t.ids[marks] = id
	return id
}

// with returns c with mark r appended to the cell's marks — or c unchanged when the mark cannot be
// kept (see maxCombos).
func (t *comboTable) with(c cell, r rune) cell {
	have := t.get(c.ext)
	if len(have) >= maxMarkBytes {
		return c
	}
	var buf [maxMarkBytes + utf8.UTFMax]byte
	n := copy(buf[:], have)
	n += utf8.EncodeRune(buf[n:], r)
	if id := t.intern(string(buf[:n])); id != 0 {
		c.ext = c.ext&extWide | id
	}
	return c
}
