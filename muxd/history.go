package muxd

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Scrollback: the lines that have left the top of a session's screen, kept in LINES rather than
// bytes, with their colour.
//
// ── Why lines and not a bigger byte ring ────────────────────────────────────────────────────────
// The daemon already keeps a byte ring per session (see ring_buffer.go); the obvious way to give
// people more history is to make it bigger. That does not work, for three reasons that only show
// up once you try to USE the extra bytes:
//
//   - A browser terminal can only append. Handing it earlier RAW BYTES means re-rendering from an
//     earlier point, which is the "reload everything" cost the whole feature exists to avoid.
//   - Bytes are not lines, and the ratio is wildly unstable: ~80 B for a plain line, 3–5× that
//     with colour, and TENS OF KILOBYTES for one repaint of a full-screen TUI that should
//     contribute no history at all. "Give me 200 more lines" is unanswerable in bytes.
//   - Redraw traffic and real output compete for the same budget, so most of a big ring can be
//     spent on frames nobody will ever scroll back to.
//
// Storing rendered LINES answers all three: paging is exact, a full-screen TUI costs nothing
// (see vt.trackAlt), and the memory is predictable.
//
// ── Why colour is nearly free ───────────────────────────────────────────────────────────────────
// Naively, colour is what makes this unaffordable: style per cell is 13 bytes, so 50 000 lines is
// 130 MB per session. Three things collapse that to roughly the plain-text cost:
//
//  1. Styles are INTERNED per session — a day's work uses tens of distinct ones, so a cell's style
//     is a 2-byte id, not 12 bytes of colour (vt_style.go).
//  2. A line stores its style as RUN-LENGTH SPANS, not per character. Most lines have none at all
//     (entirely default), and a heavily coloured one has a handful.
//  3. Lines live in CHUNK ARENAS, not as individual objects. A `struct{ text string; spans []Span }`
//     costs 40 bytes of slice/string headers before any content — more than a short line's text —
//     and 50 000 of them is 100 000 allocations for the garbage collector to walk. Packed into
//     1024-line chunks, per-line overhead is 8 bytes of index and per-line allocation is zero.
//
// Measured on 50 000 lines of realistic log output (muxd/history_test.go): 74 B/line plain,
// 95 B/line coloured — a 1.28× premium for keeping every colour and attribute, and 4.5 MB for a
// session's whole scrollback. (The premium tracks how many style RUNS a line has, not how many
// coloured characters: a four-colour log line pays 21 bytes, a syntax-highlighted diff pays more.)
// Eviction is by whole chunk, which is O(1) and never fragments.
//
// ── What this is NOT ────────────────────────────────────────────────────────────────────────────
// Not persistent: it lives in the daemon's memory, so `muxd --restart` and a reboot clear it. That
// is a deliberate, accepted trade for this round (a disk-backed store is the next knob, and would
// also make 200 000 lines cheap). Nothing in the UI may imply history survives a daemon restart.

const (
	// DefaultHistoryLines is how many scrolled-off lines a session keeps.
	//
	// 50 000 is 25× tmux's default (2 000) and costs 3.5 MB plain / 4.5 MB coloured per session
	// (measured, see TestHistory_FiftyThousandLinesFitTheBudget), so a dozen busy sessions land
	// near 50 MB — inside the 60 MB ceiling this change was accepted under. It is a default, not a
	// limit: see Daemon.SetHistoryLines.
	DefaultHistoryLines = 50_000

	// historyChunkLines is how many lines share one arena.
	//
	// Chunking is what removes per-line allocation, and the chunk is also the eviction unit, so the
	// number trades two things off: bigger chunks mean the stored count overshoots the configured
	// maximum by more (up to one chunk), smaller chunks mean more per-chunk overhead and a longer
	// index. 1024 lines overshoots by ~2% of the default budget and keeps the chunk index at ~50
	// entries, which a binary search crosses in six steps.
	historyChunkLines = 1024

	// maxHistoryReadLines bounds one Read call, so a client asking for "everything" cannot make
	// the daemon materialise a session's entire history into one response.
	maxHistoryReadLines = 2000
)

// Span marks where a style starts within a line's text, as a BYTE offset.
//
// Byte offsets rather than character or cell offsets because the text is stored as UTF-8 and every
// consumer inside this process slices it as UTF-8; converting once, at the edge that talks to a
// client, is one conversion in one place instead of an offset convention every caller has to get
// right. (The wire format goes further and sends pre-split segments, so no offset convention
// crosses the process boundary at all.)
type Span struct {
	Start uint16
	Style StyleID
}

// Line is one scrolled-off line as a reader sees it.
//
// Spans is nil for a line drawn entirely in the default style, which is most of them. That is not
// an optimisation detail leaking out — it is the meaning: "no span" and "a default-styled span
// covering everything" are the same picture, and the nil case is what makes an uncoloured session
// cost nothing extra.
type Line struct {
	// N is the line's absolute number. It counts every line the session has ever scrolled off, so
	// it keeps increasing after eviction and is therefore usable directly as a paging cursor.
	N     int64
	Text  string
	Spans []Span
	// Wrapped says this row CONTINUES the previous one — the terminal wrapped at the right margin
	// rather than the program starting a new line. A reader joining rows into lines (a restore that
	// lets the browser re-wrap them at its own width) needs it; one showing rows does not.
	Wrapped bool `json:"wrapped,omitempty"`
	// Content is how many columns of this row hold written characters — where the terminal
	// considers the row to end, which is not where its text ends: a background-coloured erase is
	// stored as coloured spaces, yet holds nothing. 0 means "not recorded" (an older daemon), and
	// readers fall back to the text's width.
	Content int `json:"content,omitempty"`
	// Nulls lists the runs of cells INSIDE the content that were never written (a cursor jump or a
	// tab skipped them), as [start, end) column pairs. The text shows them as spaces, as a screen
	// does, but the terminal tells them apart from typed spaces — and a re-wrapping resize decides
	// where a row ends by that difference. Nil for the common row that has none.
	Nulls []uint16 `json:"nulls,omitempty"`
	// Hidden holds the cells that sit where the second half of a wide character should be and are
	// not one: a null (the narrowing of a resize cut the character off), or something typed over it
	// (autowrap off writes into the last cell whatever is there). A screen reads past them — the text
	// above skips the cell after a wide character — but a re-wrapping resize moves them like any other
	// cell and measures by them. (A cell behind a character that is NOT a wide one says that a mark
	// widened it: see extWide.) Nil for the common row that has none.
	Hidden []HiddenCell `json:"hidden,omitempty"`
	// Cols is how many columns the row had when it was stored. The text stops where the content
	// does, so without it a row that a later line continues — full width, whatever its tail holds —
	// cannot be put back together exactly, and a re-wrapping resize would join its successor early.
	// 0 means "not recorded".
	Cols int `json:"cols,omitempty"`
	// Lone lists the columns of cells that hold a zero-width character ON THEIR OWN: a combining mark
	// or a joiner that came with nothing before it to stack on (after a control sequence, at the
	// start of a row), which a terminal puts in a cell of its own that has no width and still takes a
	// column. The text shows the character like any other; this says it is not stacked on the one
	// before it, which is what every other zero-width character in Text is. Without it, "a" + U+0301
	// in Text could be one cell or two, and the columns after it shift by one. Nil for nearly every row.
	Lone []uint16 `json:"lone,omitempty"`
}

// HiddenCell is a cell that Line.Text does not show; see Line.Hidden.
type HiddenCell struct {
	Col   uint16
	R     rune // nullRune for a cell nothing was written to
	Style StyleID
}

// lineEnd is one line's exclusive end offsets in its chunk's two arenas. Ends rather than starts,
// so a line's start is the previous line's end and no per-line start needs storing.
//
// Twelve bytes, and it has to stay twelve: it is paid by every line of every session's history, and
// the memory budget (TestHistory_FiftyThousandLinesFitTheBudget) has no room for a thirteenth. That
// is why "wrapped" lives in the top bit of flags rather than in a field of its own.
type lineEnd struct {
	text  uint32
	span  uint32
	flags uint16 // content width in the low 15 bits (see Line.Content), lineWrapped above them
	cols  uint16 // see Line.Cols
}

const lineWrapped = 1 << 15

func (e lineEnd) wrapped() bool { return e.flags&lineWrapped != 0 }
func (e lineEnd) content() int  { return int(e.flags &^ lineWrapped) }

func packFlags(content int, wrapped bool) uint16 {
	f := uint16(min(content, lineWrapped-1))
	if wrapped {
		f |= lineWrapped
	}
	return f
}

// lineExtra locates one line's rarely-needed cell detail (Line.Nulls, Line.Hidden) in its chunk's
// extra arena. Only lines that have any get an entry, kept in line order so a line is found by
// search; a coloured log line costs nothing for them.
type lineExtra struct {
	line uint16 // the line's index in its chunk
	at   uint32 // offset into histChunk.extra
}

// histChunk is a fixed-capacity run of lines sharing their arenas.
type histChunk struct {
	first int64 // absolute number of this chunk's first line
	text  []byte
	spans []Span
	ends  []lineEnd
	// extras/extra hold Line.Nulls, Line.Hidden and Line.Lone for the lines that have them. An entry
	// in extra is: the number of null runs, the runs (start, end), the number of hidden cells, each
	// hidden cell as five words (column, rune low and high, style low and high — a StyleID is 16 bits,
	// the second word is kept so the layout does not depend on that), the number of lone cells and
	// their columns.
	extras []lineExtra
	extra  []uint16
}

func (c *histChunk) lineCount() int { return len(c.ends) }

// bounds returns the arena ranges of the i-th line in this chunk.
func (c *histChunk) bounds(i int) (textLo, textHi, spanLo, spanHi uint32) {
	if i > 0 {
		textLo, spanLo = c.ends[i-1].text, c.ends[i-1].span
	}
	return textLo, c.ends[i].text, spanLo, c.ends[i].span
}

// putExtra records the detail of the line about to be appended (index lineCount()).
func (c *histChunk) putExtra(nulls []uint16, hidden []HiddenCell, lone []uint16) {
	if len(nulls) == 0 && len(hidden) == 0 && len(lone) == 0 {
		return
	}
	c.extras = append(c.extras, lineExtra{line: uint16(c.lineCount()), at: uint32(len(c.extra))})
	c.extra = append(c.extra, uint16(len(nulls)/2))
	c.extra = append(c.extra, nulls...)
	c.extra = append(c.extra, uint16(len(hidden)))
	for _, h := range hidden {
		r, st := uint32(h.R), uint32(h.Style)
		c.extra = append(c.extra, h.Col, uint16(r), uint16(r>>16), uint16(st), uint16(st>>16))
	}
	c.extra = append(c.extra, uint16(len(lone)))
	c.extra = append(c.extra, lone...)
}

// getExtra returns the detail of the i-th line, nil if it has none.
func (c *histChunk) getExtra(i int) (nulls []uint16, hidden []HiddenCell, lone []uint16) {
	k := sort.Search(len(c.extras), func(k int) bool { return int(c.extras[k].line) >= i })
	if k == len(c.extras) || int(c.extras[k].line) != i {
		return nil, nil, nil
	}
	w := c.extra[c.extras[k].at:]
	n := int(w[0])
	if n > 0 {
		nulls = append([]uint16(nil), w[1:1+2*n]...)
	}
	w = w[1+2*n:]
	for h := 0; h < int(w[0]); h++ {
		f := w[1+5*h:]
		hidden = append(hidden, HiddenCell{Col: f[0], R: rune(uint32(f[1]) | uint32(f[2])<<16), Style: StyleID(uint32(f[3]) | uint32(f[4])<<16)})
	}
	w = w[1+5*int(w[0]):]
	if n := int(w[0]); n > 0 {
		lone = append([]uint16(nil), w[1:1+n]...)
	}
	return nulls, hidden, lone
}

// truncate drops everything after the first `keep` lines.
func (c *histChunk) truncate(keep int) {
	if keep == 0 {
		c.text, c.spans, c.ends, c.extras, c.extra = nil, nil, nil, nil, nil
		return
	}
	e := c.ends[keep-1]
	c.text, c.spans, c.ends = c.text[:e.text], c.spans[:e.span], c.ends[:keep]
	k := sort.Search(len(c.extras), func(k int) bool { return int(c.extras[k].line) >= keep })
	if k < len(c.extras) {
		c.extra = c.extra[:c.extras[k].at]
		c.extras = c.extras[:k]
	}
}

func (c *histChunk) approxBytes() int {
	return cap(c.text) + cap(c.spans)*4 + cap(c.ends)*12 + cap(c.extras)*8 + cap(c.extra)*2
}

// lineStore is the chunked, bounded sequence of scrolled-off lines.
type lineStore struct {
	chunks   []*histChunk
	maxLines int
	// combos resolves the marks stacked on the cells of the rows appended (see vt_combine.go) — the
	// table of the model whose rows they are. Nil for a store that is only ever given lines.
	combos *comboTable
	// base is the absolute number of the oldest line still stored; total is the number of lines
	// ever appended. total-base is therefore how many are held, and both survive eviction, which
	// is what makes N a stable cursor.
	base  int64
	total int64
}

func newLineStore(maxLines int) *lineStore {
	if maxLines <= 0 {
		maxLines = DefaultHistoryLines
	}
	return &lineStore{maxLines: maxLines}
}

// appendRow encodes one screen row into the arena and evicts if the store is over budget.
//
// The row is only borrowed: the caller recycles its backing array immediately (see vt.scrollUp),
// so everything kept is copied here.
func (s *lineStore) appendRow(row []cell, wrapped bool) {
	c := s.tail()
	textStart := uint32(len(c.text))
	spanStart := uint32(len(c.spans))

	// Trim trailing cells that are blank AND unstyled. A trailing space with a background colour
	// is not padding — it is a coloured bar a program deliberately painted, and dropping it would
	// turn a highlighted line into a short one.
	end := len(row)
	for end > 0 {
		cc := row[end-1]
		if cc.style != DefaultStyleID || cc.ext != 0 {
			break // a space with a mark on it is not padding either
		}
		if cc.r != ' ' && cc.r != 0 && cc.r != nullRune {
			break
		}
		end--
	}
	content := 0
	for i := len(row) - 1; i >= 0; i-- {
		if row[i].r > 0 {
			content = i + cellWidth(row[i])
			break
		}
	}
	// Never-written cells inside the content: the text below spells them as spaces, so say where.
	var nulls []uint16
	var hidden []HiddenCell
	// (content can overshoot the row by one: a wide character cut by a narrowing resize keeps its
	// lead in the last column, as xterm leaves it.)
	for i, lim := 0, min(content, len(row)); i < lim; i++ {
		if row[i].r != nullRune {
			continue
		}
		j := i
		for j < lim && row[j].r == nullRune {
			j++
		}
		nulls = append(nulls, uint16(i), uint16(j))
		i = j
	}
	// A wide character is followed by its second half, which carries nothing. When what follows is
	// anything else, keep it: the text will not show it (see Line.Hidden).
	for i := 0; i+1 < len(row); i++ {
		if cellWidth(row[i]) == 2 && row[i+1].r != 0 {
			hidden = append(hidden, HiddenCell{Col: uint16(i + 1), R: row[i+1].r, Style: row[i+1].style})
		}
	}

	prev := DefaultStyleID
	var buf [utf8.UTFMax]byte
	var lone []uint16
	// Read the row out the way xterm's translateToString does: a cell is followed by the cell its
	// width says, so the one after a wide character is skipped whatever it holds (autowrap off can
	// overwrite a continuation, and a narrowing resize can leave a lead without one), and an empty
	// cell that is landed on is a space. A cell is its character and the marks stacked on it.
	for i := 0; i < end; {
		cc := row[i]
		if cc.style != prev {
			c.spans = append(c.spans, Span{Start: uint16(uint32(len(c.text)) - textStart), Style: cc.style})
			prev = cc.style
		}
		r := cc.r
		if r <= 0 {
			r = ' '
		}
		n := utf8.EncodeRune(buf[:], r)
		c.text = append(c.text, buf[:n]...)
		w := cellWidth(cc)
		if cc.ext != 0 {
			c.text = append(c.text, s.combos.get(cc.ext)...)
		}
		if cc.r > 0 && runeWidth(cc.r) == 0 {
			lone = append(lone, uint16(i)) // a zero-width character with nothing to stack on
		}
		i += max(w, 1)
	}
	// A run that turns out to be empty (every cell of it trimmed away) leaves a span pointing at
	// the end of the text; drop those so "no spans" keeps meaning "entirely default".
	for len(c.spans) > int(spanStart) && c.spans[len(c.spans)-1].Start == uint16(uint32(len(c.text))-textStart) {
		c.spans = c.spans[:len(c.spans)-1]
	}
	c.putExtra(nulls, hidden, lone)
	c.ends = append(c.ends, lineEnd{text: uint32(len(c.text)), span: uint32(len(c.spans)), flags: packFlags(content, wrapped), cols: uint16(len(row))})
	s.total++
	s.evict()
}

// appendLine stores a line that is already encoded — a restore's scrollback — mapping its span
// styles through resolve (the line may come from another model's style table).
func (s *lineStore) appendLine(ln Line, resolve func(StyleID) StyleID) {
	c := s.tail()
	textStart := uint32(len(c.text))
	c.text = append(c.text, ln.Text...)
	for _, sp := range ln.Spans {
		c.spans = append(c.spans, Span{Start: sp.Start, Style: resolve(sp.Style)})
	}
	content := lineContent(ln)
	_ = textStart
	var hidden []HiddenCell
	for _, h := range ln.Hidden {
		h.Style = resolve(h.Style)
		hidden = append(hidden, h)
	}
	c.putExtra(ln.Nulls, hidden, ln.Lone)
	c.ends = append(c.ends, lineEnd{text: uint32(len(c.text)), span: uint32(len(c.spans)), flags: packFlags(content, ln.Wrapped), cols: uint16(ln.Cols)})
	s.total++
	s.evict()
}

// lineContent is how many columns of ln hold written characters. A Content of 0 means "not
// recorded" only for a line that does not say how wide it was either; one that does is a row of
// styled blanks (a coloured erase), which holds nothing.
func lineContent(ln Line) int {
	if ln.Content == 0 && ln.Cols == 0 {
		return textCells(ln.Text)
	}
	return ln.Content
}

func textCells(text string) int {
	n := 0
	for _, r := range text {
		n += runeWidth(r)
	}
	return n
}

// dropTail removes the newest n lines (a reflowing resize pulling rows back onto the screen).
func (s *lineStore) dropTail(n int) {
	for n > 0 && len(s.chunks) > 0 {
		c := s.chunks[len(s.chunks)-1]
		k := min(n, c.lineCount())
		keep := c.lineCount() - k
		if keep == 0 {
			s.chunks[len(s.chunks)-1] = nil
			s.chunks = s.chunks[:len(s.chunks)-1]
		} else {
			c.truncate(keep)
		}
		s.total -= int64(k)
		n -= k
	}
	if s.base > s.total {
		s.base = s.total
	}
}

// clearAll empties the store; numbering continues where it was.
func (s *lineStore) clearAll() {
	s.chunks = nil
	s.base = s.total
}

// rowsTail decodes the newest n LOGICAL lines back into cells, oldest first: the rows that make them
// up, so the first row returned is the start of a line (it does not continue another) unless that
// is where the store begins.
//
// Logical lines rather than rows, because that is what a resize can promise to fill a screen from:
// a line is at least one row at any width, but n wrapped rows can be one line and no more. The scan
// is capped (maxTailRows) — one enormous unbroken line is not worth materialising for a resize,
// and the rows beyond the cap are exactly the ones too far up to reach the screen.
func (s *lineStore) rowsTail(n int, styles *styleTable) []xline {
	if n <= 0 || s.total == s.base {
		return nil
	}
	maxTailRows := max(4*maxScreenRows, 2*n)
	from := max(s.total-int64(n), s.base)
	lines := s.readAll(from, s.total)
	for len(lines) < maxTailRows && from > s.base {
		starts := 0
		for _, ln := range lines {
			if !ln.Wrapped {
				starts++
			}
		}
		if starts >= n && !lines[0].Wrapped {
			break
		}
		// Each further row adds at most one line start, so at least the shortfall is needed — and
		// one, when only the head of the first line is missing.
		step := min(int64(max(n-starts, 1)), from-s.base, int64(maxTailRows-len(lines)))
		if step <= 0 {
			break
		}
		from -= step
		lines = append(s.readAll(from, from+step), lines...)
	}
	out := make([]xline, len(lines))
	for i, ln := range lines {
		out[i] = xline{cells: decodeRow(ln, s.combos), wrapped: ln.Wrapped}
	}
	return out
}

// readAll reads lines [from, to) regardless of the per-call page cap.
func (s *lineStore) readAll(from, to int64) []Line {
	var out []Line
	for from < to {
		page := s.read(from, int(to-from))
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
		from += int64(len(page))
	}
	return out
}

// decodeRow turns a stored line back into cells: written characters up to its content length,
// null cells (keeping their style) after — the inverse of appendRow as far as a terminal can tell.
// The marks stacked on a cell are interned into combos, the table of the model the cells are for.
func decodeRow(ln Line, combos *comboTable) []cell {
	content := lineContent(ln)
	var row []cell
	span, cur := 0, DefaultStyleID
	nulls := ln.Nulls // [start, end) pairs, ascending
	hidden := 0       // next unread entry of ln.Hidden (ascending by column)
	lone := ln.Lone   // columns of cells that hold a zero-width character on their own, ascending
	last := -1        // the cell the last character of the text went into: what a mark stacks on
	isNull := func(col int) bool {
		for len(nulls) >= 2 && int(nulls[1]) <= col {
			nulls = nulls[2:]
		}
		return len(nulls) >= 2 && int(nulls[0]) <= col
	}
	// The cell a character takes after it, when what is stored behind it says it has one (see
	// Line.Hidden): a wide character's second half, or what a mark's widening kept hidden.
	second := func() (cell, bool) {
		for hidden < len(ln.Hidden) && int(ln.Hidden[hidden].Col) < len(row) {
			hidden++
		}
		if hidden < len(ln.Hidden) && int(ln.Hidden[hidden].Col) == len(row) {
			return cell{r: ln.Hidden[hidden].R, style: ln.Hidden[hidden].Style}, true
		}
		return cell{r: 0, style: cur}, false
	}
	for i, r := range ln.Text {
		for span < len(ln.Spans) && int(ln.Spans[span].Start) <= i {
			cur = ln.Spans[span].Style
			span++
		}
		w := runeWidth(r)
		if w == 0 {
			for len(lone) > 0 && int(lone[0]) < len(row) {
				lone = lone[1:]
			}
			if len(lone) > 0 && int(lone[0]) == len(row) {
				// A cell of its own: no width, but the cursor moved past it — unless it was widened.
				row = append(row, cell{r: r, style: cur})
				last = len(row) - 1
				if sec, behind := second(); behind {
					row[last].ext = extWide
					row = append(row, sec)
				}
				continue
			}
			// Stacked on the character before it, whatever its second half holds.
			if last >= 0 && row[last].r > 0 {
				row[last] = combos.with(row[last], r)
			}
			continue
		}
		if len(row) >= content || isNull(len(row)) {
			row = append(row, cell{r: nullRune, style: cur})
			last = -1
			continue
		}
		row = append(row, cell{r: r, style: cur})
		last = len(row) - 1
		sec, behind := second()
		if w == 1 && behind {
			// A cell kept behind a character that is not a wide one: a mark widened it (see extWide).
			row[last].ext = extWide
			w = 2
		}
		if w == 2 {
			// The second half — or what was left in its place (see Line.Hidden).
			row = append(row, sec)
		}
	}
	// appendRow trims trailing blanks of the default style from the text, but they are still part of
	// the line when they were WRITTEN (content counts them): a row that wrapped after a typed space
	// must come back with that space, or re-wrapping it at another width joins what followed it
	// one column early.
	for len(row) < content {
		if isNull(len(row)) {
			row = append(row, cell{r: nullRune, style: DefaultStyleID})
		} else {
			row = append(row, cell{r: ' ', style: DefaultStyleID})
		}
	}
	// ...and the cells after the content, up to the width the row had, were never written.
	for len(row) < ln.Cols {
		row = append(row, cell{r: nullRune, style: DefaultStyleID})
	}
	// A wide character whose second half a narrowing resize cut off keeps its first half in the
	// last column (xterm leaves it so); the text spells it as a whole character, so cut it back.
	if ln.Cols > 0 && len(row) > ln.Cols {
		row = row[:ln.Cols]
	}
	return row
}

// tail returns the chunk to append to, starting a new one when the current is full.
func (s *lineStore) tail() *histChunk {
	if n := len(s.chunks); n > 0 && s.chunks[n-1].lineCount() < historyChunkLines {
		return s.chunks[n-1]
	}
	c := &histChunk{
		first: s.total,
		text:  make([]byte, 0, historyChunkLines*64),
		ends:  make([]lineEnd, 0, historyChunkLines),
	}
	s.chunks = append(s.chunks, c)
	return c
}

// evict drops whole chunks from the front while doing so still leaves at least maxLines stored.
//
// Whole chunks, so eviction is a slice re-slice and a pointer drop — no copying, no fragmentation,
// and the arenas are freed in one piece. The cost is that the stored count overshoots maxLines by
// up to one chunk, which is ~2% at the default and is a far better trade than moving bytes around
// on every single line.
func (s *lineStore) evict() {
	for len(s.chunks) > 1 {
		head := s.chunks[0]
		if s.total-s.base-int64(head.lineCount()) < int64(s.maxLines) {
			return
		}
		s.base += int64(head.lineCount())
		s.chunks[0] = nil // let the arenas go before the slice header does
		s.chunks = s.chunks[1:]
	}
}

// read returns up to count lines starting at absolute line `from`, clamped to what is stored.
func (s *lineStore) read(from int64, count int) []Line {
	if count <= 0 || len(s.chunks) == 0 {
		return nil
	}
	if count > maxHistoryReadLines {
		count = maxHistoryReadLines
	}
	if from < s.base {
		from = s.base
	}
	if from >= s.total {
		return nil
	}
	if int64(count) > s.total-from {
		count = int(s.total - from)
	}
	out := make([]Line, 0, count)
	ci := sort.Search(len(s.chunks), func(i int) bool {
		return s.chunks[i].first+int64(s.chunks[i].lineCount()) > from
	})
	for ; ci < len(s.chunks) && len(out) < count; ci++ {
		c := s.chunks[ci]
		i := int(from + int64(len(out)) - c.first)
		if i < 0 {
			i = 0
		}
		for ; i < c.lineCount() && len(out) < count; i++ {
			tLo, tHi, sLo, sHi := c.bounds(i)
			e := c.ends[i]
			line := Line{N: c.first + int64(i), Text: string(c.text[tLo:tHi]), Wrapped: e.wrapped(), Content: e.content(), Cols: int(e.cols)}
			if sHi > sLo {
				line.Spans = append([]Span(nil), c.spans[sLo:sHi]...)
			}
			line.Nulls, line.Hidden, line.Lone = c.getExtra(i)
			out = append(out, line)
		}
	}
	return out
}

func (s *lineStore) approxBytes() int {
	n := 0
	for _, c := range s.chunks {
		n += c.approxBytes()
	}
	return n
}

// HistoryStats is what a session's scrollback costs and holds. Reported so the memory ceiling this
// feature was given can be checked against a running daemon rather than trusted.
type HistoryStats struct {
	// Base is the oldest line still held; Total is every line ever scrolled off. Total-Base is how
	// many are available now.
	Base  int64 `json:"base"`
	Total int64 `json:"total"`
	// Bytes is the arenas plus the style table — an estimate, since Go does not report the true
	// footprint of a map.
	Bytes int `json:"bytes"`
	// Styles is how many distinct styles this session has used.
	Styles int `json:"styles"`
	// StyleOverflow counts styles that had to be approximated because the table was full, and
	// DroppedBytes counts bytes discarded as an over-long unterminated escape sequence. Both are
	// zero for every well-behaved session; a non-zero value explains a history that looks slightly
	// wrong without needing a log to have been running at the time.
	StyleOverflow int64 `json:"styleOverflow,omitempty"`
	DroppedBytes  int64 `json:"droppedBytes,omitempty"`
	// Broken is set if the historian hit an internal error and switched itself off. The session's
	// terminal is unaffected — that is the point of the guard — but its history stopped growing.
	Broken bool   `json:"broken,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// History is one session's screen model plus its scrollback.
//
// ── Locking ─────────────────────────────────────────────────────────────────────────────────────
// It has its OWN mutex rather than reusing the session's. The session lock is held on the PTY's
// hot path — every read from the shell takes it — and a client paging through a few hundred lines
// of history under that lock would apply backpressure all the way to the shell. Here the writer
// holds this lock for the length of one append and readers hold it for one page; neither can stall
// the terminal.
//
// ── The guarantee that matters ──────────────────────────────────────────────────────────────────
// A terminal must not be able to break because its historian did. Write recovers from any panic in
// the model or the store, switches history off for that session, and records why. The bytes have
// already gone to the ring and to every attached client by then, so a session whose history is
// broken still shows, scrolls and accepts input exactly as before — it just stops accumulating
// scrollback. This is checked by a fault-injection test, not merely intended.
type History struct {
	mu     sync.Mutex
	vt     *vt
	store  *lineStore
	styles *styleTable
	broken bool
	reason string
}

// storeScrollback is the scrollback interface over a History's line store.
type storeScrollback struct{ h *History }

func (b storeScrollback) tail(n int) []xline         { return b.h.store.rowsTail(n, b.h.styles) }
func (b storeScrollback) dropTail(n int)             { b.h.store.dropTail(n) }
func (b storeScrollback) push(row []cell, wrap bool) { b.h.store.appendRow(row, wrap) }
func (b storeScrollback) clear()                     { b.h.store.clearAll() }

// NewTerminalModel is a History that behaves as the browser's terminal does — xterm.js — down to
// what a resize does to the scrollback (see vt_reflow.go) and what ED 3 and RIS clear. It exists
// for the server's restore point, which must be indistinguishable from a terminal that watched
// the whole stream. Its line numbers are not a stable paging cursor: a resize can take rows back
// out of the scrollback, exactly as the terminal does.
//
// It also starts as the browser's terminal does: with LNM on. The frontend builds xterm with
// convertEol: true, which xterm.js IS mode 20 — a line feed returns the carriage — so a stream that
// never says otherwise lays its lines out with LF as CR LF. (The daemon's archive model, NewHistory,
// keeps the terminal's own default: its job is the bytes as the program wrote them.)
func NewTerminalModel(g Grid, maxLines int) *History {
	h := NewHistory(g, maxLines)
	h.vt.lnm = true
	h.vt.reflow = true
	h.vt.sb = storeScrollback{h}
	h.vt.sbCap = h.store.maxLines // xterm stops counting its base when the scrollback is full
	return h
}

// NewHistory builds a session's history. maxLines <= 0 uses DefaultHistoryLines; the caller
// disables history by not creating one at all.
func NewHistory(g Grid, maxLines int) *History {
	styles := newStyleTable()
	h := &History{
		vt:     newVT(g.Rows, g.Cols, styles),
		store:  newLineStore(maxLines),
		styles: styles,
	}
	h.vt.trackAlt = true
	h.vt.onScrollOff = h.store.appendRow
	h.store.combos = h.vt.combos
	return h
}

// Write feeds PTY output to the model. Safe to call with a partial escape sequence: the tail is
// held until the rest arrives (see vt.Write).
func (h *History) Write(p []byte) {
	if h == nil || len(p) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	if h.vt == nil {
		return // this session's historian has already broken; see guard
	}
	h.vt.Write(p)
}

// Resize tells the model the terminal changed shape. Existing lines are not reflowed — see
// vt.resize.
func (h *History) Resize(g Grid) {
	if h == nil || g.Zero() {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	if h.vt == nil {
		return
	}
	h.vt.resize(g.Rows, g.Cols)
}

// guard is the deferred half of the "a broken historian must not break a terminal" rule. Running
// before the deferred Unlock (defers are LIFO) means the flag is set while the lock is still held,
// so no reader can observe a half-updated model.
//
// It guards the READERS too, not only Write. The reason is symmetry with the rule itself: a panic
// while paging through history would otherwise travel up a client's request path, and there is no
// version of that which is better than "this session's history stopped working".
func (h *History) guard() {
	r := recover()
	if r == nil {
		return
	}
	h.broken = true
	h.reason = fmt.Sprintf("%v", r)
	// The MODEL is dropped: it produced a panic once and must not be fed again.
	//
	// The STORE is kept. Lines already collected are still perfectly good — appendRow writes the
	// text and spans before the index entry that makes a line visible, so a panic mid-append
	// orphans some bytes and nothing more — and losing them would turn "history stopped growing"
	// into "history vanished", which is a worse answer to give someone who was scrolling through
	// it a moment ago. It also keeps Base and Total meaningful, and those are a client's paging
	// cursor.
	h.vt = nil
}

// Read returns up to count lines starting at absolute line number `from`, plus the range currently
// available. A `from` older than what is held is clamped to the oldest line rather than failing:
// the caller is scrolling, and the honest answer to "show me line 10" after eviction is "here is
// the oldest line I still have", together with the Base that says so.
func (h *History) Read(from int64, count int) (lines []Line, stats HistoryStats) {
	if h == nil {
		return nil, HistoryStats{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	return h.store.read(from, count), h.statsLocked()
}

// Screen returns the CURRENT visible grid as lines, numbered as if they continued the scrollback.
//
// A history viewer needs it: scrollback ends where the live screen begins, and without this the
// bottom of the history and the top of the terminal would have a screen-sized hole between them
// whose size the client would have to guess.
func (h *History) Screen() (lines []Line, stats HistoryStats) {
	if h == nil {
		return nil, HistoryStats{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	if h.vt == nil {
		return nil, h.statsLocked()
	}
	// The alternate screen is not part of the scrollback continuum — it is a separate surface with
	// no history — so a viewer is shown the parked normal screen instead of a TUI's frame.
	grid, wrap := h.vt.cells, h.vt.wrap
	if h.vt.alt && h.vt.altSaved != nil {
		grid, wrap = h.vt.altSaved.cells, h.vt.altSaved.wrap
	}
	tmp := newLineStore(len(grid) + 1)
	tmp.combos = h.vt.combos
	for i, row := range grid {
		tmp.appendRow(row, i < len(wrap) && wrap[i])
	}
	out := tmp.read(0, len(grid))
	for i := range out {
		out[i].N = h.store.total + int64(i)
	}
	// Trailing blank rows are the unused bottom of the screen, not content.
	for len(out) > 0 && out[len(out)-1].Text == "" && len(out[len(out)-1].Spans) == 0 {
		out = out[:len(out)-1]
	}
	return out, h.statsLocked()
}

// TermState is everything a terminal must be put back into for a byte stream to continue on it as
// if it had never stopped: what is on the screen — both screens, when a full-screen program has the
// alternate one up — where the cursor is, what the pen draws with, and every mode the stream set.
//
// Screen() answers a different question ("show a viewer the scrollback's continuation", which is the
// parked normal screen); this answers "what is the terminal", which is the alternate screen when one
// is displayed, with the normal one carried alongside so that leaving the TUI finds it intact.
//
// Lines' span ids resolve against Styles (id = index). Rows are NOT renumbered into the scrollback
// continuum — a restore addresses rows absolutely.
type TermState struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`

	// Lines is the displayed grid: the alternate screen when Alt.
	Lines []Line `json:"lines"`
	Alt   bool   `json:"alt,omitempty"`
	// Parked is the normal screen while the alternate is displayed, with the cursor, pen and
	// scrolling region it had — what `ESC [ ? 1049 l` brings back. Nil when !Alt.
	Parked *ParkedScreen `json:"parked,omitempty"`

	// CursorCol may equal Cols: a character written into the last column leaves the cursor there
	// with the wrap still pending, and the next character goes to the start of the next row.
	CursorRow int   `json:"cursor_row"`
	CursorCol int   `json:"cursor_col"`
	ScrollTop int   `json:"scroll_top"`
	ScrollBot int   `json:"scroll_bot"`
	Pen       Style `json:"pen"`

	// The displayed screen's DECSC / `CSI s` slot (each screen has its own).
	SavedRow int   `json:"saved_row"`
	SavedCol int   `json:"saved_col"`
	SavedPen Style `json:"saved_pen"`

	// Modes are the private modes the stream set, in the order each last changed — see vt.modes.
	Modes []ModeState `json:"modes,omitempty"`
	// CursorShape is the last DECSCUSR parameter, -1 if none was seen.
	CursorShape int `json:"cursor_shape"`
	// InsertMode (IRM, `CSI 4 h`) and NewlineMode (LNM, `CSI 20 h`) are the two ANSI modes that move
	// characters: with the first a printed character pushes the rest of the row right, with the
	// second a line feed also returns the carriage. They are not private modes, so Modes does not
	// carry them.
	InsertMode  bool `json:"insert_mode,omitempty"`
	NewlineMode bool `json:"newline_mode,omitempty"`
	// NewlineModeSet says the stream itself chose NewlineMode (`CSI 20 h` or `l`), so that it is not
	// just the default of the model the state came from — the daemon's archive model starts with LNM
	// off, the browser's terminal (and NewTerminalModel) with it on. A restore into a terminal model
	// takes NewlineMode when this is set and keeps its own default when it is not.
	NewlineModeSet bool `json:"newline_mode_set,omitempty"`

	// Pending is the unconsumed tail of the input: a partial escape sequence or UTF-8 rune that the
	// next bytes complete. A restore that drops it turns the first byte after the cut into text.
	Pending []byte `json:"pending,omitempty"`

	// Charset is the character-set state — which sets G0..G3 designate, which one GL draws with, and
	// what DECSC saved — for a terminal that is to take the stream on from here: a program in the
	// middle of a border drawn with `ESC ( 0` has not said how to leave it, and the next bytes are
	// letters to a terminal that was not told. Nil when the stream never touched it.
	Charset *CharsetState `json:"charset,omitempty"`
	// JoinWidth is the width of the character the last byte of the stream printed, 0 if anything else
	// came after it (a control, a sequence). A zero-width character that arrives next stacks on that
	// character when this is non-zero, and takes a cell of its own when it is 0 — and REP repeats it.
	// A terminal restored from this state starts with it 0.
	JoinWidth int `json:"join_width,omitempty"`

	Styles []Style `json:"styles"`
}

// ParkedScreen is the normal screen held while the alternate one is displayed.
type ParkedScreen struct {
	Lines     []Line `json:"lines"`
	CursorRow int    `json:"cursor_row"`
	CursorCol int    `json:"cursor_col"`
	ScrollTop int    `json:"scroll_top"`
	ScrollBot int    `json:"scroll_bot"`
	Pen       Style  `json:"pen"`
	// Charset is the character set this screen's DECSC slot kept (see CharsetState.Saved); 0 for US.
	Charset byte `json:"charset,omitempty"`
}

// ModeState is one DEC private mode's last observed value.
type ModeState struct {
	Mode int  `json:"m"`
	On   bool `json:"on"`
}

// gridLines encodes a grid row by row through a scratch store, so a screen and a scrolled-off line
// share one encoding (trailing default-style blanks trimmed, wide-character continuations dropped).
func gridLines(grid [][]cell, wrap []bool, combos *comboTable) []Line {
	tmp := newLineStore(len(grid) + 1)
	tmp.combos = combos
	for i, row := range grid {
		tmp.appendRow(row, i < len(wrap) && wrap[i])
	}
	return tmp.read(0, len(grid))
}

// State reads the model's full terminal state plus the newest `scrollback` scrolled-off lines, as
// one consistent read. ok=false means the model has broken (see guard) and there is no state to
// give — the caller must fall back to whatever it did before it had one.
func (h *History) State(scrollback int) (st TermState, lines []Line, ok bool) {
	if h == nil {
		return TermState{}, nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	v := h.vt
	if v == nil {
		return TermState{}, nil, false
	}
	st = TermState{
		Cols: v.cols, Rows: v.rows,
		Lines:     gridLines(v.cells, v.wrap, v.combos),
		Alt:       v.alt,
		CursorRow: v.row, CursorCol: v.col,
		ScrollTop: v.top, ScrollBot: v.bot,
		Pen:      v.cur,
		SavedRow: max(v.savedRow, 0), SavedCol: v.savedCol, SavedPen: v.savedStyle, // DECRC clamps it the same way
		CursorShape:    v.cursorShape,
		InsertMode:     v.insertMode,
		NewlineMode:    v.lnm,
		NewlineModeSet: v.lnmSet,
		Pending:        append([]byte(nil), v.pending...),
		JoinWidth:      v.repWidth,
	}
	if cs := v.charset.export(v.savedCharset); !cs.IsZero() {
		st.Charset = &cs
	}
	if v.alt && v.altSaved != nil {
		s := v.altSaved
		st.Parked = &ParkedScreen{
			Lines:     gridLines(s.cells, s.wrap, v.combos),
			CursorRow: s.row, CursorCol: s.col,
			ScrollTop: s.top, ScrollBot: s.bot,
			Pen: s.style, Charset: v.otherSlot.charset,
		}
	}
	for m, e := range v.modes {
		st.Modes = append(st.Modes, ModeState{Mode: m, On: e.on})
	}
	sort.Slice(st.Modes, func(i, j int) bool {
		return v.modes[st.Modes[i].Mode].seq < v.modes[st.Modes[j].Mode].seq
	})
	// Scrollback last, but inside the same lock: the newest stored line is exactly the one that
	// left the top of the screen just read, so the two halves meet with no line missing or doubled.
	if scrollback > 0 && h.store.total > h.store.base {
		from := h.store.total - int64(scrollback)
		if from < h.store.base {
			from = h.store.base
		}
		if v.reflow {
			// The head of a line cut by the count is needed to lay it out again.
			for guard := 0; from > h.store.base && guard < 4*maxScreenRows; guard++ {
				first := h.store.read(from, 1)
				if len(first) == 0 || !first[0].Wrapped {
					break
				}
				from--
			}
		}
		for from < h.store.total {
			page := h.store.read(from, maxHistoryReadLines)
			if len(page) == 0 {
				break
			}
			lines = append(lines, page...)
			from += int64(len(page))
		}
		if v.reflow {
			lines = h.layOutScrollback(lines, scrollback)
		}
	}
	st.Styles = h.styles.snapshot()
	return st, lines, true
}

// layOutScrollback gives the scrollback the rows xterm would hold at the model's CURRENT width.
//
// A resize re-wraps only the newest part of the store (see vt_reflow.go), so older rows are still
// as wide as they were when they last were laid out. xterm re-wraps all of it at every resize, and a
// reader of the model — the restore point is one — should see that, not the model's shortcut. The
// stale rows (everything older than vt.sbFresh) have each logical line joined and wrapped again,
// exactly as a resize does it; this is done here, on the way out, because a reader asks for a few
// thousand rows once, where a resize would pay for the whole history every time a window edge is
// dragged. The fresh rows are left alone: they are what the last resize made of them, including
// the cases where xterm leaves a line as it is (the one holding the cursor).
func (h *History) layOutScrollback(lines []Line, keep int) []Line {
	v := h.vt
	freshStart := h.store.total - int64(min(v.sbFresh, int(h.store.total-h.store.base)))
	stale := 0
	for stale < len(lines) && lines[stale].N < freshStart {
		stale++
	}
	if stale == 0 {
		return lines
	}
	rows := make([]xline, stale)
	for i, ln := range lines[:stale] {
		rows[i] = xline{cells: decodeRow(ln, v.combos), wrapped: ln.Wrapped}
	}
	// A line cut by the window's top continues into the first fresh row.
	runsOn, startsWide := false, false
	if stale < len(lines) {
		next := lines[stale]
		runsOn = next.Wrapped
		startsWide = len(next.Text) > 0 && runeWidth([]rune(next.Text)[0]) == 2
	}
	laid := normalizeRows(rows, v.cols, runsOn, startsWide)
	tmp := newLineStore(len(laid) + 1)
	tmp.combos = v.combos
	for _, r := range laid {
		tmp.appendRow(r.cells, r.wrapped)
	}
	out := tmp.readAll(0, int64(len(laid)))
	out = append(out, lines[stale:]...)
	if keep > 0 && len(out) > keep {
		out = out[len(out)-keep:]
	}
	for i := range out {
		out[i].N = h.store.total - int64(len(out)) + int64(i)
	}
	return out
}

// Restore replaces the model with the given state and scrollback — the inverse of State. It is how
// a model that has not seen a session's whole stream is given what it missed: from a peer that did
// (a daemon's snapshot), or by grafting a peer's screen onto what this model did see.
//
// Styles in st are re-interned, so st may come from another model's table. The store is rebuilt,
// which restarts line numbering — fine for the in-process callers this serves, whose readers never
// page by number.
func (h *History) Restore(st TermState, scrollback []Line) {
	if h == nil || st.Rows < 1 || st.Cols < 1 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	if h.vt == nil {
		return
	}
	styles := h.styles
	// st's span ids index st.Styles; this model has its own table.
	mapID := func(id StyleID) StyleID {
		if int(id) < len(st.Styles) {
			return styles.intern(st.Styles[id])
		}
		return DefaultStyleID
	}
	v := newVT(st.Rows, st.Cols, styles)
	v.trackAlt = h.vt.trackAlt
	v.reflow, v.sb, v.sbCap = h.vt.reflow, h.vt.sb, h.vt.sbCap
	grid := func(lines []Line) ([][]cell, []bool) {
		g := make([][]cell, v.rows)
		w := make([]bool, v.rows)
		for i := range g {
			if i < len(lines) {
				g[i] = restoreRow(lines[i], v.cols, mapID, v.combos)
				w[i] = lines[i].Wrapped
			} else {
				g[i] = v.blankRow()
			}
		}
		return g, w
	}
	region := func(top, bot int) (int, int) {
		if top < 0 || bot >= v.rows || bot <= top {
			return 0, v.rows - 1
		}
		return top, bot
	}
	v.cells, v.wrap = grid(st.Lines)
	if st.Alt && st.Parked != nil {
		p := st.Parked
		t, b := region(p.ScrollTop, p.ScrollBot)
		v.alt = true
		cells, wrap := grid(p.Lines)
		v.altSaved = &savedScreen{
			cells: cells, wrap: wrap,
			row: v.clampRow(p.CursorRow), col: clampInt(p.CursorCol, 0, v.cols),
			style: p.Pen, top: t, bot: b,
		}
	}
	v.row, v.col = v.clampRow(st.CursorRow), clampInt(st.CursorCol, 0, v.cols)
	v.top, v.bot = region(st.ScrollTop, st.ScrollBot)
	v.setStyle(st.Pen)
	v.savedRow, v.savedCol, v.savedStyle = st.SavedRow, st.SavedCol, st.SavedPen
	if st.Charset != nil {
		v.charset.restore(*st.Charset)
		v.savedCharset = st.Charset.Saved
	}
	v.repWidth = st.JoinWidth
	for _, m := range st.Modes {
		v.noteMode(m.Mode, m.On)
	}
	v.cursorShape = st.CursorShape
	v.insertMode, v.lnm, v.lnmSet = st.InsertMode, st.NewlineMode, st.NewlineModeSet
	if h.vt.reflow && h.vt.lnm && !st.NewlineModeSet {
		// A terminal model runs with convertEol (see NewTerminalModel), which a state taken from the
		// daemon's archive model cannot say: that model's LNM is its own default, off. Unless the stream
		// chose it (NewlineModeSet), keep the receiver's, or a model seeded from a snapshot lays out
		// every later line feed differently from the browser's terminal.
		v.lnm = true
	}
	if st.Alt && st.Parked != nil {
		// The normal screen's DECSC slot while a TUI is up: 1049 saved the cursor and pen there on
		// the way in, which is exactly what the parked screen records.
		v.otherSlot = cursorSlot{row: st.Parked.CursorRow, col: st.Parked.CursorCol, style: st.Parked.Pen, charset: st.Parked.Charset}
	}
	v.pending = append([]byte(nil), st.Pending...)

	// The scrollback is kept as given — whole lines, at whatever width they were drawn — not cut
	// to this grid: a line from a wider window is still that line, and whoever renders it wraps it.
	store := newLineStore(h.store.maxLines)
	store.combos = v.combos
	for _, ln := range scrollback {
		store.appendLine(ln, mapID)
	}
	h.vt, h.store = v, store
	v.onScrollOff = store.appendRow
	v.ybase = len(scrollback) // the base the saved row is measured from (see vt.savedRow)
}

// restoreRow decodes a line into exactly `cols` cells: written characters up to its content
// length, null cells (keeping their style) after, spans mapped into this model's style table. A
// line wider than the grid is cut at the grid's edge, as a terminal would.
func restoreRow(ln Line, cols int, mapID func(StyleID) StyleID, combos *comboTable) []cell {
	mapped := ln
	if len(ln.Spans) > 0 {
		mapped.Spans = make([]Span, len(ln.Spans))
		for i, sp := range ln.Spans {
			mapped.Spans[i] = Span{Start: sp.Start, Style: mapID(sp.Style)}
		}
	}
	row := decodeRow(mapped, combos)
	if len(row) > cols {
		row = row[:cols]
		if cellWidth(row[cols-1]) == 2 {
			row[cols-1] = cell{r: nullRune, style: row[cols-1].style}
		}
	}
	for len(row) < cols {
		row = append(row, xnull)
	}
	return row
}

// Styles is the session's style table, for a client that renders spans. Sent once per session and
// re-sent when its length grows — ids are append-only and never reused, so a client holding a
// prefix of this table can resolve every id it has already seen.
func (h *History) Styles() []Style {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.styles.snapshot()
}

// StylesFrom returns the table entries after the first `have` of them, plus the full length.
//
// The incremental form exists because a viewer re-asks on every page and the table barely changes
// after the first few seconds of a session; sending all of it each time would make the constant
// cost of paging proportional to how colourful the session has been.
func (h *History) StylesFrom(have int) (added []Style, total int) {
	if h == nil {
		return nil, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	total = len(h.styles.list)
	if have < 0 || have >= total {
		return nil, total
	}
	return append([]Style(nil), h.styles.list[have:]...), total
}

// maxHistorySearchScan bounds one search, in lines.
//
// A search runs inside the daemon holding every live shell on the machine, under the lock a
// viewer's page also needs. Scanning 50 000 short lines is well under a millisecond, but the
// bound is what makes that a guarantee rather than a measurement: without it, a future depth
// setting silently becomes a latency setting.
const maxHistorySearchScan = 200_000

// Search scans the scrollback for `query`, returning up to `limit` matches.
//
// It searches in the daemon rather than shipping the history to whoever asked, which is the same
// reasoning that put the history here in the first place: the data is large, the answer is small,
// and moving the large thing to reach the small one is what the line-based design was chosen to
// avoid.
//
// `backward` scans towards OLDER lines and returns matches in that order — newest first — because
// that is the order a person pressing "previous match" reads them in. Forward search returns
// oldest first, symmetrically.
func (h *History) Search(query string, from int64, backward bool, limit int, ignoreCase bool) (matches []HistoryMatch, stats HistoryStats) {
	if h == nil || query == "" {
		return nil, h.Stats()
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	needle := query
	if ignoreCase {
		needle = strings.ToLower(query)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.guard()
	st := h.statsLocked()
	if h.store == nil {
		return nil, st
	}

	// Clamp the starting point into what is held. A viewer that has scrolled to a line since
	// evicted still gets a sensible search rather than an empty one.
	if from < st.Base {
		from = st.Base
	}
	if from > st.Total {
		from = st.Total
	}

	// Page through the store rather than materialising it: read() copies each line's text, so
	// asking for the whole history at once would allocate a copy of the thing we are searching.
	const page = 500
	scanned := 0
	for scanned < maxHistorySearchScan && len(matches) < limit {
		var start int64
		if backward {
			start = from - int64(scanned) - page
			if start < st.Base {
				start = st.Base
			}
			if from-int64(scanned) <= st.Base {
				break
			}
		} else {
			start = from + int64(scanned)
			if start >= st.Total {
				break
			}
		}
		want := page
		if backward {
			if n := from - int64(scanned) - start; n < int64(page) {
				want = int(n)
			}
		}
		if want <= 0 {
			break
		}
		lines := h.store.read(start, want)
		if len(lines) == 0 {
			break
		}
		scanned += len(lines)
		if backward {
			for i := len(lines) - 1; i >= 0 && len(matches) < limit; i-- {
				if col, ok := findIn(lines[i].Text, needle, ignoreCase); ok {
					matches = append(matches, HistoryMatch{N: lines[i].N, Col: col, Text: lines[i].Text})
				}
			}
		} else {
			for _, l := range lines {
				if len(matches) >= limit {
					break
				}
				if col, ok := findIn(l.Text, needle, ignoreCase); ok {
					matches = append(matches, HistoryMatch{N: l.N, Col: col, Text: l.Text})
				}
			}
		}
	}
	return matches, st
}

// findIn is strings.Index with an optional case fold, returning the BYTE offset so it lines up
// with Span.Start's convention.
//
// Lower-casing the haystack can change its byte length for some scripts (a folded rune is not
// always the same width), which would make the returned offset point into the wrong place in the
// ORIGINAL text. Rather than pretend otherwise, the case-insensitive path verifies that the fold
// preserved length and falls back to reporting the start of the line when it did not — an offset
// that is honest about being approximate beats one that is silently wrong.
func findIn(haystack, needle string, ignoreCase bool) (int, bool) {
	if !ignoreCase {
		i := strings.Index(haystack, needle)
		return i, i >= 0
	}
	folded := strings.ToLower(haystack)
	i := strings.Index(folded, needle)
	if i < 0 {
		return 0, false
	}
	if len(folded) != len(haystack) {
		return 0, true
	}
	return i, true
}

// Stats reports what this session's history holds and costs.
func (h *History) Stats() HistoryStats {
	if h == nil {
		return HistoryStats{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statsLocked()
}

func (h *History) statsLocked() HistoryStats {
	st := HistoryStats{
		Styles:        len(h.styles.list),
		StyleOverflow: h.styles.overflow,
		Broken:        h.broken,
		Reason:        h.reason,
	}
	if h.store != nil {
		st.Base, st.Total = h.store.base, h.store.total
		st.Bytes = h.store.approxBytes() + h.styles.approxBytes()
	}
	if h.vt != nil {
		st.DroppedBytes = h.vt.dropped
		st.Bytes += h.vt.rows * h.vt.cols * 8
	}
	return st
}
