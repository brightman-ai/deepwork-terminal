package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// reconnect_pack.go — turning a restore point back into bytes, and sending it (REQ-TS-12/13).
//
// The keyframe (session_screen.go) is a model's view of the terminal; a browser understands only a
// byte stream. serializeKeyframe writes the stream that, executed on a cleared xterm, puts it into
// the keyframe's state: scrollback pushed through the top of the screen, both screens drawn
// absolutely, then the state the following bytes assume — modes, scrolling region, saved cursor,
// cursor, pen. The window after the keyframe is then appended verbatim. Everything here is ordinary
// output; the browser needs no new code.

// writeSGR switches the pen to st, from whatever it was, in one sequence.
func writeSGR(w *strings.Builder, st muxd.Style) {
	w.WriteString("\x1b[0")
	a := st.Attrs
	for _, f := range []struct {
		bit  muxd.Attrs
		code string
	}{
		{muxd.AttrBold, "1"}, {muxd.AttrDim, "2"}, {muxd.AttrItalic, "3"}, {muxd.AttrUnderline, "4"},
		{muxd.AttrBlink, "5"}, {muxd.AttrInverse, "7"}, {muxd.AttrHidden, "8"}, {muxd.AttrStrike, "9"},
	} {
		if a&f.bit != 0 {
			w.WriteString(";" + f.code)
		}
	}
	color := func(c muxd.Color, base string) {
		switch {
		case c.IsDefault():
		case c.IsIndexed():
			fmt.Fprintf(w, ";%s;5;%d", base, c.Index())
		case c.IsRGB():
			r, g, b := c.RGB()
			fmt.Fprintf(w, ";%s;2;%d;%d;%d", base, r, g, b)
		}
	}
	color(st.FG, "38")
	color(st.BG, "48")
	w.WriteByte('m')
}

func cup(w *strings.Builder, row, col int) {
	w.WriteString("\x1b[" + strconv.Itoa(row+1) + ";" + strconv.Itoa(col+1) + "H")
}

// ── Drawing rows ────────────────────────────────────────────────────────────────────────────────
//
// A stored row (muxd.Line) is text plus three things that text alone loses, and the restore needs
// all of them, because a browser that restored is later RESIZED and re-wraps what it holds:
//
//   - which cells were never written (Nulls, and the styled blanks past Content): a typed space and
//     an empty cell look the same, but xterm's row ends where the last WRITTEN cell does;
//   - which rows continue the one above (Wrapped): xterm marks those itself when a character wraps,
//     so a continued row is written by letting the text run on instead of starting a new line;
//   - the width the row had (Cols).
//
// Never-written cells are skipped with CUF, or erased with ECH when they carry a background (a
// coloured erase is not a row of coloured spaces); neither costs a byte per cell.
//
// Some cells can only be made the way xterm makes them. A zero-width character stacks on the cell
// before it unless a control sequence came between (then it takes a column of its own); an empty
// cell that holds more than a background, or a cell left where a wide character's second half should
// be, comes from printing over half of a wide character. Those are rare, so they cost a few bytes in
// the rows that have them; the rest of the stream is the text as stored.

// rowReader walks the cells of a stored row left to right, as Line describes them. It is the one
// place that knows how a Line maps to cells; muxd's decodeRow is the same reading for the model.
type rowReader struct {
	ln      muxd.Line
	cols    int
	content int // columns that hold written cells
	off     int // byte offset into ln.Text
	col     int // next column
	span    int // next unread span
	nulls   int // next unread entry of ln.Nulls (pairs)
	lones   int // next unread entry of ln.Lone
	owe     int // empty cells still to be returned after a character that a mark widened
	oweWith muxd.StyleID
	style   muxd.StyleID
	// marks are the zero-width characters that follow the character just returned in the text — the
	// accents and joiners that stack on it, which a terminal keeps in the same cell.
	marks string
	// widened says the character just returned was made two columns wide by the marks after it, in
	// the last two columns: the second column is behind it (the reader has moved past both), and
	// marks holds what made it so (see drawRow).
	widened bool
	// lone says the character just returned is a zero-width one in a cell of its own (see
	// muxd.Line.Lone): it takes a column, and has to be written where nothing joins it to the cell
	// before.
	lone bool
	// hidden is what sits where the second half of the double-width character just returned should
	// be, when it is anything but that second half (see muxd.Line.Hidden).
	hidden      bool
	hiddenRune  rune // 0 or less for an empty cell
	hiddenStyle muxd.StyleID
}

func newRowReader(ln muxd.Line, cols int) rowReader {
	content := ln.Content
	if content == 0 && ln.Cols == 0 {
		content = cellWidth(ln.Text) // a line from before the row recorded its extent: all of it counts
	}
	return rowReader{ln: ln, cols: cols, content: content}
}

func (it *rowReader) isLone(col int) bool {
	for it.lones < len(it.ln.Lone) && int(it.ln.Lone[it.lones]) < col {
		it.lones++
	}
	return it.lones < len(it.ln.Lone) && int(it.ln.Lone[it.lones]) == col
}

func (it *rowReader) isNull(col int) bool {
	for it.nulls+1 < len(it.ln.Nulls) && int(it.ln.Nulls[it.nulls+1]) <= col {
		it.nulls += 2
	}
	return it.nulls+1 < len(it.ln.Nulls) && int(it.ln.Nulls[it.nulls]) <= col
}

// next returns the next cell: its column, the character (0 for a cell nothing was written to), its
// width in cells (the second cell of a wide character is part of it) and style. ok is false at the
// end of what the row stores — everything after is a default-styled empty cell — or at the edge of
// the grid.
func (it *rowReader) next() (col int, r rune, w int, style muxd.StyleID, ok bool) {
	it.hidden, it.marks, it.lone, it.widened = false, "", false, false
	if it.owe > 0 { // the cell a mark's widening kept behind the character before: an empty one here
		it.owe--
		col = it.col
		it.col++
		return col, 0, 1, it.oweWith, true
	}
	for it.off < len(it.ln.Text) {
		for it.span < len(it.ln.Spans) && int(it.ln.Spans[it.span].Start) <= it.off {
			it.style = it.ln.Spans[it.span].Style
			it.span++
		}
		r, size := utf8.DecodeRuneInString(it.ln.Text[it.off:])
		it.off += size
		w = runeCells(r)
		lone := false
		if w == 0 {
			if !it.isLone(it.col) {
				continue // a mark with nothing to stack on
			}
			w, lone = 1, true
		}
		col = it.col
		if col+w > it.cols {
			it.off = len(it.ln.Text)
			it.col = it.cols
			return 0, 0, 0, 0, false // past the edge: a wider row than this grid keeps what fits
		}
		if !lone && (col >= it.content || it.isNull(col)) {
			it.col++
			return col, 0, 1, it.style, true
		}
		it.col += w
		it.lone = lone
		behind := -1 // the hidden cell directly behind a character that is not a wide one: a mark widened it
		if w == 1 {
			for i, h := range it.ln.Hidden {
				if int(h.Col) == col+1 {
					behind = i
				}
			}
		}
		if end := it.off; !(lone && behind >= 0 && col+2 != it.cols) && end < len(it.ln.Text) { // the marks that stack on it
			for end < len(it.ln.Text) {
				m, n := utf8.DecodeRuneInString(it.ln.Text[end:])
				if runeCells(m) != 0 || it.isLone(it.col) {
					break
				}
				end += n
			}
			it.marks = it.ln.Text[it.off:end]
			it.off = end
		}
		for _, h := range it.ln.Hidden {
			if int(h.Col) != col+1 {
				continue
			}
			switch {
			case w == 2:
				it.hidden, it.hiddenRune, it.hiddenStyle = true, h.R, h.Style
			case col+2 == it.cols && it.marks != "":
				// xterm does this at the margin (see drawRow); the cell behind stays as it was.
				it.widened = true
				it.hiddenRune, it.hiddenStyle = h.R, h.Style
				it.col++
			default:
				// Elsewhere the widening is not something output can be told to do; the marks stack on
				// the character, and the cell behind keeps its column.
				it.owe, it.oweWith = 1, h.Style
			}
		}
		return col, r, w, it.style, true
	}
	// Written cells whose blanks the text dropped (trailing default-styled spaces are not stored).
	if it.col < it.content && it.col < it.cols {
		col = it.col
		it.col++
		if it.isNull(col) {
			return col, 0, 1, muxd.DefaultStyleID, true
		}
		return col, ' ', 1, muxd.DefaultStyleID, true
	}
	return 0, 0, 0, 0, false
}

// packer writes the bytes of one keyframe, tracking the pen the terminal has been left with so a
// style is only switched when it changes.
type packer struct {
	w      strings.Builder
	styles []muxd.Style
	cols   int
	rows   int
	pen    muxd.Style
	// wrapOff says the stream has autowrap off (?7l), which what is written after the modes are
	// stated must leave as it found it.
	wrapOff bool
}

func (p *packer) setPen(st muxd.Style) {
	if st != p.pen {
		writeSGR(&p.w, st)
		p.pen = st
	}
}

func (p *packer) styleOf(id muxd.StyleID) muxd.Style {
	if int(id) < len(p.styles) {
		return p.styles[id]
	}
	return muxd.DefaultStyle
}

func (p *packer) cuf(n int) {
	if n == 1 {
		p.w.WriteString("\x1b[C")
	} else if n > 1 {
		p.w.WriteString("\x1b[" + strconv.Itoa(n) + "C")
	}
}

func (p *packer) cha(col int) { p.w.WriteString("\x1b[" + strconv.Itoa(col+1) + "G") }

func (p *packer) cup(row, col int) { cup(&p.w, row, col) }

// drawPlainRow is drawRow for the row that is nothing but written cells — no cell never written,
// none beside a wide character that is not its second half, its text no wider than the grid — which
// is nearly every row of a log or a prompt: the text goes out as it is stored, one piece per style.
// It reports false for any other row, having written nothing.
func (p *packer) drawPlainRow(ln muxd.Line) (rowEnd, bool) {
	if ln.Text == "" || len(ln.Nulls) > 0 || len(ln.Hidden) > 0 || len(ln.Lone) > 0 {
		return rowEnd{}, false
	}
	width := cellWidth(ln.Text)
	content := ln.Content
	if content == 0 && ln.Cols == 0 {
		content = width
	}
	if width > content || content > p.cols {
		return rowEnd{}, false // coloured blanks past what was written, or a row from a wider grid
	}
	if first, _ := utf8.DecodeRuneInString(ln.Text); runeCells(first) == 0 {
		return rowEnd{}, false
	}
	text, pos, id := ln.Text, 0, muxd.DefaultStyleID
	for _, sp := range ln.Spans {
		if at := int(sp.Start); at > pos {
			p.setPen(p.styleOf(id))
			p.w.WriteString(text[pos:at])
			pos = at
		}
		id = sp.Style
	}
	if pos < len(text) {
		p.setPen(p.styleOf(id))
		p.w.WriteString(text[pos:])
	}
	if content > width { // written blanks the text dropped (default-styled, at the end)
		p.setPen(muxd.DefaultStyle)
		for ; width < content; width++ {
			p.w.WriteByte(' ')
		}
	}
	p.setPen(muxd.DefaultStyle)
	return rowEnd{x: content, empty: content < p.cols}, true
}

// What the first cell of a row is: nothing, a character, a double-width one, or a cell that cannot
// be what makes the wrap onto the row happen — a zero-width character in a cell of its own, or a
// character that has to be preceded by moving the cursor (see drawRow).
const (
	cellEmpty = iota
	cellNarrow
	cellWide
	cellNoWrap
)

// firstCell says what the first cell of a row is, and its style.
func (p *packer) firstCell(ln muxd.Line) (kind int, style muxd.Style) {
	it := newRowReader(ln, p.cols)
	col, r, w, id, ok := it.next()
	if !ok || r == 0 {
		return cellEmpty, muxd.DefaultStyle
	}
	st := p.styleOf(id)
	if it.lone {
		return cellNoWrap, st
	}
	if marked(st) || len(ln.Spans) > 0 {
		// [character][empty in the same style]: drawn behind a move of the cursor that would cancel
		// the wrap.
		if _, nr, _, nid, nok := it.next(); nok && nr == 0 && marked(p.styleOf(nid)) && p.styleOf(nid) == st && col+w < p.cols {
			return cellNoWrap, st
		}
	}
	return w, st
}

// wideFiller is a double-width character written only to be overwritten or skipped, where the way
// to leave a cell in some state is to have a wide character there first (see drawRow).
const wideFiller = "中"

// marked says an empty cell with this style carries more than a background, which erasing cannot
// give it.
func marked(st muxd.Style) bool { return st.FG != muxd.ColorDefault || st.Attrs != 0 }

// rowEnd is how a drawn row ended.
type rowEnd struct {
	x     int        // the cursor's column; cols means the wrap is pending
	empty bool       // the row's last cell is an empty one...
	tail  muxd.Style // ...with this style

	// fix is an empty cell still to be made in the last column, which the row below has to wrap
	// first (see drawRow).
	fix *lastCellFix
}

// lastCellFix is the wide character in a row's last two columns (wide, in wideStyle) whose second
// half is to be an empty cell in style.
type lastCellFix struct {
	wide      string
	wideStyle muxd.Style
	style     muxd.Style
}

// drawRow writes one row's cells, starting from column 0 of a row whose cells are all empty. It
// reports where the cursor is (cols means the wrap is pending, which is what lets the next character
// continue this row onto the following one) and what the row's last cell is.
//
// scrub says the cell at column 0 holds a placeholder that has to end up empty.
//
// An empty cell that carries a foreground or an attribute cannot be erased into being (erasing keeps
// only the background); xterm leaves one only where a character is printed over half of a
// double-width one, and it is put back the same way: the wide character is written first, then the
// neighbour is printed over its other half. That is also how a cell is left where the second half of
// a wide character in the last column should be (a character typed with autowrap off, or a skipped
// wide one).
func (p *packer) drawRow(ln muxd.Line, scrub, continued bool) rowEnd {
	if re, ok := p.drawPlainRow(ln); ok {
		return re
	}
	rowMarked := false // whether any empty cell of this row can carry more than a background
	for _, sp := range ln.Spans {
		rowMarked = rowMarked || marked(p.styleOf(sp.Style))
	}
	var fix *lastCellFix
	it := newRowReader(ln, p.cols)
	x := 0
	skip := 0 // columns the cursor still has to move over
	flushSkip := func() {
		if skip > 0 {
			p.cuf(skip)
			x += skip
			skip = 0
		}
	}
	runStart, run := 0, 0 // the empty cells being gathered
	var runBG muxd.Color
	flushRun := func() {
		if run == 0 {
			return
		}
		switch {
		case !runBG.IsDefault():
			flushSkip()
			p.setPen(muxd.Style{BG: runBG})
			p.w.WriteString("\x1b[" + strconv.Itoa(run) + "X") // ECH: erased cells keep the pen's background
			scrub = scrub && runStart != 0
		case scrub && runStart == 0:
			flushSkip()
			p.setPen(muxd.DefaultStyle)
			p.w.WriteString("\x1b[X")
			scrub = false
		}
		skip += run
		run = 0
	}
	peek := func() (r rune, st muxd.Style, ok bool) {
		next := it
		_, r, _, id, ok := next.next()
		return r, p.styleOf(id), ok
	}
	end := 0 // the column after the last stored cell
	lastEmpty, lastStyle := false, muxd.DefaultStyle
	for {
		col, r, w, id, ok := it.next()
		if !ok {
			break
		}
		end = col + w
		st := p.styleOf(id)
		if r == 0 {
			lastEmpty, lastStyle = true, st
			if rowMarked && marked(st) && col+1 < p.cols {
				// The character printed over the second half of a wide one empties its first half, in
				// the pen of the character. If the next cell is not such a character (in this style),
				// a stand-in is printed in this style and the real cell put there after.
				flushRun()
				flushSkip()
				p.w.WriteString(wideFiller)
				p.cha(col + 1) // absolute: relative moves start from the margin when the wrap is pending
				x++
				scrub = scrub && col != 0
				nr, nst, nok := peek()
				if !nok || nr == 0 || nst != st {
					p.setPen(st)
					p.w.WriteByte(' ')
					p.cha(col + 1)
					if !nok || nr == 0 { // the cell is an empty one: the stand-in is erased
						p.setPen(muxd.Style{BG: nst.BG})
						p.w.WriteString("\x1b[X")
						if nok {
							it.next()
							skip++
							end = col + 2
							lastStyle = nst
						}
					}
				}
				continue
			}
			bg := st.BG
			if run > 0 && bg != runBG {
				flushRun()
			}
			if run == 0 {
				runStart, runBG = col, bg
			}
			run++
			continue
		}
		lastEmpty = false
		flushRun()
		flushSkip()
		var nr rune
		var nst muxd.Style
		nok := false
		if rowMarked {
			nr, nst, nok = peek()
		}
		if it.widened {
			// A narrow character that marks have made two columns wide, in the last two. xterm makes
			// one when a wide character is dropped at the margin (autowrap off) and a mark then joins
			// the cell before it, taking the dropped character's width; the cell behind it is not
			// touched, so it is written first, and the cursor put back on it.
			if it.lone {
				p.w.WriteString("\x1b[m")
				p.pen = muxd.DefaultStyle
			}
			p.setPen(st)
			p.w.WriteRune(r)
			if it.hiddenRune > 0 {
				p.setPen(p.styleOf(it.hiddenStyle))
				p.w.WriteRune(it.hiddenRune)
				p.cha(p.cols - 1)
			}
			p.w.WriteString("\x1b[?7l" + wideFiller + it.marks + "\x1b[?7h")
			x = p.cols - 1
			end = p.cols
			scrub = false
			continue
		}
		if col+w < p.cols && nok && nr == 0 && marked(nst) && nst == st {
			// [character][empty in the same style]: the wide character this one is printed over leaves
			// its second half empty, in this character's pen.
			p.cha(col + w - 1)
			p.w.WriteString(wideFiller)
			p.cha(col)
			p.setPen(st)
			p.w.WriteRune(r)
			p.w.WriteString(it.marks)
			x += w
			it.next()
			skip++
			end = col + w + 1
			lastEmpty, lastStyle = true, st
			scrub = false
			continue
		}
		if it.lone { // nothing may join it to the cell before: a control sequence comes between
			p.w.WriteString("\x1b[m")
			p.pen = muxd.DefaultStyle
		}
		p.setPen(st)
		p.w.WriteRune(r)
		p.w.WriteString(it.marks)
		x += w
		scrub = false // the placeholder at column 0 is overwritten by the first cell
		if it.hidden && col+w == p.cols {
			// The second half of this wide character is not one: a character typed with autowrap off
			// landed on it, or a wide one was skipped there. Autowrap off sticks the cursor at the margin,
			// and what lands there is left as it is.
			if hs := p.styleOf(it.hiddenStyle); it.hiddenRune <= 0 && continued {
				// Nothing can wrap from an empty last cell, so the cell is made once the wrap is done.
				fix = &lastCellFix{wide: string(r) + it.marks, wideStyle: st, style: hs}
			} else {
				p.w.WriteString("\x1b[?7l")
				p.setPen(hs)
				if it.hiddenRune > 0 {
					p.w.WriteRune(it.hiddenRune)
				} else {
					p.w.WriteString(wideFiller)
					x = p.cols - 1
				}
				p.w.WriteString("\x1b[?7h")
			}
		}
	}
	flushRun() // a trailing run of plain empty cells needs nothing; a coloured one was erased above
	if scrub { // nothing was stored at column 0: the row is empty, and so must its placeholder be
		p.setPen(muxd.DefaultStyle)
		p.w.WriteString("\x1b[X")
	}
	p.setPen(muxd.DefaultStyle)
	re := rowEnd{x: x, empty: lastEmpty, tail: lastStyle}
	if end < p.cols { // the cells past what is stored are empty and plain
		re = rowEnd{x: x, empty: true}
	}
	re.fix = fix
	return re
}

// continueRow makes the next character drawn continue the row just drawn onto the one below, and
// reports whether the first cell of that next row holds a placeholder to be erased (scrub).
//
// xterm marks a row as a continuation only when a character wraps onto it, so the wrap has to be
// real: the cursor is put in the pending-wrap state at the last column and a character is printed.
// Usually that is simply how the previous row ended. When it ended early — its tail was erased, or
// a double-width character did not fit — the pending state is reached with a placeholder in the
// last cell, which is erased once the wrap has happened.
func (p *packer) continueRow(prev rowEnd, next muxd.Line) (scrub bool) {
	kind, first := p.firstCell(next)
	switch {
	case prev.x >= p.cols: // the previous row ended in its last cell: the wrap is already pending
		if f := prev.fix; f != nil {
			// Back up to the wide character, print it again (the wrap is pending after it, which is
			// what the skipped character needs to find; from any other column it would clear the
			// wide character's first half), and come back down.
			p.w.WriteByte(' ') // the character that wraps
			p.w.WriteString("\x1b[A")
			p.cha(p.cols - 2)
			p.setPen(f.wideStyle)
			p.w.WriteString(f.wide + "\x1b[?7l")
			p.setPen(f.style)
			p.w.WriteString(wideFiller + "\x1b[?7h")
			p.setPen(muxd.DefaultStyle)
			p.w.WriteString("\x1b[B")
			p.cha(0)
			return true
		}
		if kind == cellEmpty || kind == cellNoWrap {
			p.w.WriteByte(' ') // the character that wraps; erased below, or printed over
			p.cha(0)
			return true
		}
		return false
	case kind == cellWide && prev.empty && prev.tail == first:
		// A double-width character that does not fit leaves the last cell empty, in its own pen, and
		// moves on: exactly the row that ended early here.
		p.cuf(p.cols - 1 - prev.x)
		return false
	}
	p.cuf(p.cols - 1 - prev.x)
	p.w.WriteString("  ") // the placeholder that makes the wrap pending, then the character that wraps
	p.w.WriteString("\x1b[A")
	p.cha(p.cols - 1)
	p.setPen(muxd.Style{BG: prev.tail.BG})
	p.w.WriteString("\x1b[X")
	p.setPen(muxd.DefaultStyle)
	p.w.WriteString("\x1b[B")
	p.cha(0)
	return kind == cellEmpty || kind == cellNoWrap
}

// leadingContinuation gives the first row drawn the "continues the row above" mark when there is
// no row above to continue: a dummy row is wrapped from and then deleted, which moves the marked row
// up into its place.
func (p *packer) leadingContinuation(next muxd.Line) (scrub bool) {
	p.cup(0, p.cols-1)
	p.w.WriteString("  ")
	p.cup(0, 0)
	p.w.WriteString("\x1b[M")
	kind, _ := p.firstCell(next)
	return kind == cellEmpty || kind == cellNoWrap
}

// drawRows draws rows from the top of an empty screen. absolute positions every row with CUP — the
// alternate screen, which must not scroll — otherwise rows are separated by newlines, so that rows
// beyond the screen's height scroll off the top into the history, which is how a terminal gets a
// scrollback at all.
func (p *packer) drawRows(rows []muxd.Line, absolute bool) {
	var prev rowEnd
	for i, ln := range rows {
		scrub := false
		switch {
		case ln.Wrapped && i > 0:
			scrub = p.continueRow(prev, ln)
		case ln.Wrapped:
			scrub = p.leadingContinuation(ln)
		case absolute:
			if blankRow(ln) && !(i+1 < len(rows) && rows[i+1].Wrapped) {
				continue
			}
			p.cup(i, 0)
		case i > 0:
			p.w.WriteString("\r\n")
		}
		prev = p.drawRow(ln, scrub, i+1 < len(rows) && rows[i+1].Wrapped)
	}
}

// blankRow says a row holds nothing at all — not even written spaces, which the text drops when
// they trail but Content still counts.
func blankRow(ln muxd.Line) bool { return ln.Text == "" && ln.Content == 0 && len(ln.Spans) == 0 }

// placeCursor puts the cursor at (row, col), where col == cols means "after the last column with
// the wrap still pending": reproduced by writing the last cell again, which is the only way to
// put a terminal into that state.
func (p *packer) placeCursor(lines []muxd.Line, row, col int) {
	if col < p.cols {
		p.cup(row, col)
		return
	}
	if row < len(lines) {
		it := newRowReader(lines[row], p.cols)
		for {
			c, r, w, id, ok := it.next()
			if !ok {
				break
			}
			if c+w == p.cols && r != 0 {
				if it.hidden && it.hiddenRune <= 0 {
					break // an empty cell follows the wide character: nothing here to leave the wrap pending after
				}
				p.cup(row, c)
				p.setPen(p.styleOf(id))
				p.w.WriteRune(r)
				p.w.WriteString(it.marks)
				if it.hidden { // and what was typed over its second half goes back the way it got there
					p.w.WriteString("\x1b[?7l")
					p.setPen(p.styleOf(it.hiddenStyle))
					p.w.WriteRune(it.hiddenRune)
					if !p.wrapOff {
						p.w.WriteString("\x1b[?7h")
					}
				}
				return
			}
		}
	}
	// The last cell is empty, and a terminal cannot be left pending after an empty cell (any move
	// that would erase one cancels the wrap): the cursor is one column short.
	p.cup(row, p.cols-1)
}

// charsetFinal is the final byte that designates a set; 0, the US set, is 'B'.
func charsetFinal(b byte) byte {
	if b == 0 {
		return 'B'
	}
	return b
}

// useCharset makes the set in use (GL) the one given, through G0. It changes G0's designation, so
// the designations are stated again afterwards.
func (p *packer) useCharset(b byte) {
	p.w.WriteString("\x1b(" + string(charsetFinal(b)) + "\x0f")
}

// lastCellIsUntouchedBySets says the row's last cell holds a character that no character set
// changes, so that it can be written again under any of them. The sets map the letters a-z (DEC
// graphics) and # @ [ \ ] ^ _ ` { | } ~ (the national sets), and nothing else below DEL.
func (p *packer) lastCellIsUntouchedBySets(lines []muxd.Line, row int) bool {
	if row >= len(lines) {
		return false
	}
	it := newRowReader(lines[row], p.cols)
	for {
		c, r, w, _, ok := it.next()
		if !ok {
			return false
		}
		if c+w == p.cols && r != 0 {
			return r >= 127 || !(r >= 'a' && r <= 'z' || strings.ContainsRune("#@[\\]^_`{|}~", r))
		}
	}
}

func runeCells(r rune) int { return muxd.RuneWidth(r) }

func cellWidth(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf { // from the first non-ASCII byte on, count by character
			n := i
			for _, r := range s[i:] {
				n += runeCells(r)
			}
			return n
		}
	}
	return len(s)
}

// serializeKeyframe renders a keyframe as bytes for a freshly cleared terminal (the connection
// sends replay_reset first). The whole of it is one synchronised update, so the browser paints the
// result once instead of watching five thousand lines scroll past.
func serializeKeyframe(st muxd.TermState, scrollback []muxd.Line) []byte {
	rows := st.Rows
	p := &packer{styles: st.Styles, cols: st.Cols, rows: rows}
	p.w.Grow(len(scrollback)*(st.Cols+16) + rows*(st.Cols+16) + 512)
	p.w.WriteString("\x1b[?2026h\x1b[0m\x1b[H")

	// The normal screen is drawn together with the scrollback, as one run of rows: scrollback enters
	// xterm's history only by leaving the top of the screen, so drawing history and screen in order
	// and letting the first scroll off is the one way to get every line (and every continued row)
	// into history exactly as it was. When the alternate screen is up this is the PARKED screen.
	normal := st.Lines
	if st.Alt && st.Parked != nil {
		normal = st.Parked.Lines
	}
	all := make([]muxd.Line, 0, len(scrollback)+rows)
	all = append(all, scrollback...)
	all = append(all, normal...)
	for len(all) < len(scrollback)+rows {
		all = append(all, muxd.Line{})
	}
	p.drawRows(all, false)

	if st.Alt && st.Parked != nil {
		// The normal screen is drawn: its region, cursor and pen, then the switch, which saves
		// exactly that cursor and pen for the switch back.
		pk := st.Parked
		if pk.ScrollTop != 0 || pk.ScrollBot != rows-1 {
			fmt.Fprintf(&p.w, "\x1b[%d;%dr", pk.ScrollTop+1, pk.ScrollBot+1)
		}
		p.placeCursor(pk.Lines, pk.CursorRow, pk.CursorCol)
		p.setPen(pk.Pen)
		if pk.Charset != 0 { // the switch saves the set in use along with the cursor
			p.useCharset(pk.Charset)
		}
		p.w.WriteString("\x1b[?1049h\x1b[0m")
		p.pen = muxd.DefaultStyle
		if pk.Charset != 0 { // and the alternate screen is drawn in the US set
			p.useCharset(0)
		}
		p.drawRows(st.Lines, true)
	}

	// The state the next bytes assume. Modes after the drawing, so none of them (autowrap, a
	// hidden cursor) can change how the drawing itself lands.
	for _, m := range st.Modes {
		if m.On {
			fmt.Fprintf(&p.w, "\x1b[?%dh", m.Mode)
		} else {
			fmt.Fprintf(&p.w, "\x1b[?%dl", m.Mode)
		}
		if m.Mode == 7 {
			p.wrapOff = !m.On
		}
	}
	if st.CursorShape >= 0 {
		fmt.Fprintf(&p.w, "\x1b[%d q", st.CursorShape)
	}
	if st.ScrollTop != 0 || st.ScrollBot != rows-1 {
		// DECSTBM homes the cursor, so it precedes every cursor placement below.
		fmt.Fprintf(&p.w, "\x1b[%d;%dr", st.ScrollTop+1, st.ScrollBot+1)
	}
	cs := st.Charset // nil: the power-on state, which the prelude has put it in
	// A set can be in use that no designation holds: DECRC puts back the one in use at DECSC, and the
	// designations may have changed since. Output reaches that state the same way — a DECRC after the
	// designations — which also moves the cursor and the pen, so they are placed after it.
	heldBySlot := cs != nil && cs.InUse != cs.G[min(max(cs.Level, 0), 3)] && cs.InUse == cs.Saved
	slot := st.SavedRow != 0 || st.SavedCol != 0 || st.SavedPen != (muxd.Style{}) || (cs != nil && cs.Saved != 0) || heldBySlot
	if slot {
		p.placeCursor(st.Lines, min(st.SavedRow, rows-1), st.SavedCol)
		p.setPen(st.SavedPen)
		if cs != nil && cs.Saved != 0 { // DECSC keeps the set in use with the cursor
			p.useCharset(cs.Saved)
		}
		p.w.WriteString("\x1b7")
		if cs != nil && cs.Saved != 0 {
			p.useCharset(0)
		}
	}
	restored := heldBySlot && (st.CursorCol < st.Cols || p.lastCellIsUntouchedBySets(st.Lines, st.CursorRow))
	if !restored {
		// The cursor is placed while the US set is in use: a pending wrap is reached by writing the
		// last cell again, and a set that draws 'q' as a line would turn the character into another.
		p.placeCursor(st.Lines, st.CursorRow, st.CursorCol)
		p.setPen(st.Pen)
	}
	if cs != nil {
		// Designations of G0..G3 and the shift that selects one.
		for g, f := range cs.G {
			p.w.WriteString("\x1b" + string("()*+"[g]) + string(charsetFinal(f)))
		}
		p.w.WriteString([]string{"\x0f", "\x0e", "\x1bn", "\x1bo"}[min(max(cs.Level, 0), 3)])
	}
	if restored {
		p.w.WriteString("\x1b8")
		p.pen = st.SavedPen
		p.placeCursor(st.Lines, st.CursorRow, st.CursorCol)
		p.setPen(st.Pen)
	}
	// The two ANSI modes that move characters. LNM is not reset by the prelude (xterm keeps it as an
	// option: the browser's convertEol), so it is stated when it is on, and when the stream turned it
	// off — a model whose default is off (the daemon archive) says nothing about it. Insert mode goes
	// last because it would shift the cell the pending-wrap placement above writes.
	switch {
	case st.NewlineMode:
		p.w.WriteString("\x1b[20h")
	case st.NewlineModeSet:
		p.w.WriteString("\x1b[20l")
	}
	if st.InsertMode {
		p.w.WriteString("\x1b[4h")
	}
	p.w.WriteString("\x1b[?2026l")
	return []byte(p.w.String())
}

// pristine reports a keyframe that is still a terminal's power-on state — nothing in history,
// nothing drawn, nothing set. It has nothing to say, so it is not said: the restore is then the
// window alone, byte-for-byte what a plain replay of the same bytes always was.
// (A terminal starts with LNM on: the frontend builds xterm with convertEol, which is that mode.)
func pristine(st muxd.TermState, scrollback []muxd.Line) bool {
	if len(scrollback) > 0 || st.Alt || len(st.Modes) > 0 || st.CursorShape >= 0 || st.InsertMode || !st.NewlineMode {
		return false
	}
	if st.CursorRow != 0 || st.CursorCol != 0 || st.SavedRow != 0 || st.SavedCol != 0 {
		return false
	}
	if st.Pen != muxd.DefaultStyle || st.SavedPen != muxd.DefaultStyle {
		return false
	}
	if st.ScrollTop != 0 || st.ScrollBot != st.Rows-1 {
		return false
	}
	if st.Charset != nil && *st.Charset != (muxd.CharsetState{}) {
		return false // a designation the window's SO or SS2 will reach for (`ESC ) 0` then `SO qq` is ──)
	}
	for _, ln := range st.Lines {
		if ln.Text != "" || len(ln.Spans) > 0 {
			return false
		}
	}
	return true
}

var errViewerGone = errors.New("viewer unsubscribed")

// restoreBytes is what a terminal must execute to show the restore point: the prelude (any state
// → a known blank one), the keyframe unless pristine, the session's bracketed-paste mode, then the
// window verbatim — or, without a keyframe, the ring replay. Device queries are stripped from the
// verbatim part only; nothing the server writes contains any.
//
// The paste mode goes BEFORE the window, where the parser is provably at ground (the keyframe ends
// cleanly), not after it: the window can end in the middle of a sequence whose remainder is the
// next live frame, and a mode inserted there would cancel it and print the rest as text. Before is
// also right on the facts — the session's paste state is the latest one observed, and if the
// window itself switches the mode, the window's own switch lands after the assertion and wins.
func restoreBytes(vs viewerSync, pasteEnabled, pasteKnown bool) (body []byte, keyframeBytes int) {
	body = append(body, restorePrelude...)
	if vs.rp == nil {
		body = append(body, pasteModeAssertion(pasteEnabled, pasteKnown)...)
		return append(body, stripDeviceQueries(vs.ring)...), 0
	}
	if !pristine(vs.rp.state, vs.rp.scrollback) {
		key := serializeKeyframe(vs.rp.state, vs.rp.scrollback)
		keyframeBytes = len(key)
		body = append(body, key...)
	}
	body = append(body, pasteModeAssertion(pasteEnabled, pasteKnown)...)
	// Pending is the first half of whatever sequence the window opens with; the two are one run
	// of bytes to the browser. Stripped together so a query split across them still goes.
	tail := make([]byte, 0, len(vs.rp.state.Pending)+len(vs.rp.window))
	tail = append(append(tail, vs.rp.state.Pending...), vs.rp.window...)
	return append(body, stripDeviceQueries(tail)...), keyframeBytes
}

// restoreViewer brings one connection's terminal to the present: the grid, a clear, then the bytes
// that draw the restore point (or, without one, the ring replay). The ONE way a browser is brought
// up to date — on connect, and again whenever it fell behind and lost output. `reason` is "connect"
// or "resync", for the log.
//
// boundary tracks the browser parser's position in what this connection has sent; it is reset and
// advanced past the restore here, so the writer knows when a server-originated insertion is safe.
func (s *Server) restoreViewer(ctx, logCtx context.Context, conn *websocket.Conn, sess *Session, subID, reason string, boundary *parserBoundary) (pasteKnown bool, err error) {
	started := time.Now()
	// An observer tab already has an xterm instance and only needs the authoritative current
	// screen. Replaying the full 5000-line history on every tab return was the other half of the
	// reported black-screen/"正在恢复终端内容" delay. Initial connects and ordinary stale viewers
	// retain the full history contract; observer returns use a bounded recent tail unless
	// the browser is reading history and needs its previous viewport restored.
	var vs viewerSync
	var ok bool
	if reason == "resync-fast" {
		vs, ok = sess.syncViewer(subID, observerRestoreLines)
	} else {
		vs, ok = sess.syncViewer(subID)
	}
	if !ok {
		return false, errViewerGone
	}
	// Only what was OBSERVED goes into the restore. Inferring the mode means asking the process
	// table and tmux, and both answer under locks no deadline reaches (a ps in flight holds one for
	// half a second, a tmux topology probe for up to seconds) — the first screen would wait on a
	// fact it does not need. An unknown mode is resolved off this path and pushed after it (see
	// latePasteModeFrame).
	pasteEnabled, pasteKnown := sess.PasteMode()

	replay, keyframeBytes := restoreBytes(vs, pasteEnabled, pasteKnown)
	mode, lines := "ring", 0
	if vs.rp != nil {
		mode, lines = "keyframe", len(vs.rp.scrollback)
	}
	prepared := time.Since(started)

	// The grid BEFORE the bytes: they were drawn for it, and a browser still on another size
	// would reflow them first — see the resized handling in the frontend.
	payload, _ := json.Marshal(ResizedPayload{Cols: vs.grid.Cols, Rows: vs.grid.Rows})
	msg, _ := json.Marshal(WSControlMessage{Type: MsgTypeResized, Payload: payload})
	if err := writeWS(ctx, conn, websocket.MessageText, msg); err != nil {
		return pasteKnown, err
	}
	// Then: everything after this frame is a fresh screen, not a continuation. Sent even when
	// there is nothing to draw — whatever the browser shows was drawn for a moment it has left.
	msg, _ = json.Marshal(WSControlMessage{Type: MsgTypeReplayReset})
	if err := writeWS(ctx, conn, websocket.MessageText, msg); err != nil {
		return pasteKnown, err
	}
	terminalWSReplayBytesTotal.Add(uint64(len(replay)))
	if err := writeWS(ctx, conn, websocket.MessageBinary, replay); err != nil {
		return pasteKnown, err
	}
	// The prelude's CAN put the browser's parser at ground; from there it has parsed exactly
	// `replay`, which can end mid-sequence (the window's last chunk is whatever the PTY read).
	*boundary = parserBoundary{}
	boundary.feed(replay)
	terminalLogger.Info(logCtx, "cli ws replay sent",
		"session_id", sess.ID,
		"sub_id", subID,
		"reason", reason,
		"mode", mode,
		"replay_bytes", len(replay),
		"keyframe_bytes", keyframeBytes,
		"scrollback_lines", lines,
		"ring_truncated", vs.truncated,
		// D5 速度预算的观测点：服务端能控制的两段——准备（同步+序列化）与写出。
		"prepare_ms", prepared.Milliseconds(),
		"replay_ms", (time.Since(started) - prepared).Milliseconds())
	return pasteKnown, nil
}
