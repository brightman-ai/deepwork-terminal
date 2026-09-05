package muxd

import (
	"strings"
	"unicode/utf8"
)

// A minimal terminal SCREEN model — the thing that turns a byte STREAM into what a person sees.
//
// ── Why it exists ───────────────────────────────────────────────────────────────────────────────
// A stream is not a screen. A TUI repaints by moving the cursor and overwriting cells, so deleting
// the escape sequences and keeping the text produces one long line of interleaved garbage. Two
// features need the real thing:
//
//   - the Agent Overview card, which shows what a terminal LOOKS like (this is where the model
//     started life, in the server's screen.go);
//   - scrollback, which needs to know WHICH LINES LEFT THE SCREEN, and what colour they were.
//
// The second one is why this file now lives in muxd rather than in the server. Scrollback has to
// be built where the bytes are complete and where the process outlives a rebuild — which is the
// daemon, and only the daemon (the server's copy of the stream is second-hand and can have holes;
// see Stream.Holed). The server keeps a thin forwarder so the overview keeps calling one function.
//
// ── Two modes, one parser ───────────────────────────────────────────────────────────────────────
// RenderScreen is the ONE-SHOT mode: replay a slice of the ring, read the grid back as text, throw
// the model away. That is the overview's usage and it runs once a second per client.
//
// newVT is the INCREMENTAL mode: one long-lived model per session, fed every byte the PTY ever
// produces, emitting each line as it scrolls off the top. That is scrollback's usage.
//
// The two differ in exactly one behaviour, `trackAlt`, and the difference is deliberate — see
// there. Everything else is shared, because two parsers would drift and the overview and the
// scrollback would then disagree about what the terminal said.
//
// ── Deliberately not a conformant emulator ──────────────────────────────────────────────────────
// No charset translation, no mouse/bracketed-paste state, no reflow on resize, no OSC-8 hyperlink
// capture, no double-height lines. What IS handled is the set of operations that decide where text
// ends up and what colour it is — because those are the two questions both callers ask.

// maxScreenRows/maxScreenCols bound what a caller may ask for, so a bogus resize cannot make the
// replay allocate an absurd grid. Well above any real terminal, and only a ceiling — the ACTUAL
// size comes from the session's PTY.
const (
	maxScreenRows = 500
	maxScreenCols = 1000
)

// maxPendingBytes bounds the partial-sequence buffer of an incremental vt.
//
// A PTY read can land in the middle of an escape sequence, so the tail has to be held until the
// rest arrives. A stream that sends `ESC [` and then a megabyte of digits would otherwise make
// that buffer grow without limit inside the daemon. Nothing real approaches 4 KB — the longest
// sequences in practice are OSC-52 clipboard writes, which are chunked well below this — so
// overflowing it means the stream is broken, and the recovery (drop the ESC, resume scanning at
// the next byte) is the same thing a terminal does with an unterminated sequence.
const maxPendingBytes = 4096

// cell is one character position: what is drawn there and how it looks.
//
// The style is an ID, not a Style: eight bytes per cell would make a 50×220 grid 88 KB and a
// scrolled-off line 1.7 KB, which is the arithmetic that makes coloured scrollback look
// unaffordable. See vt_style.go.
type cell struct {
	r     rune
	style StyleID
}

// vt is a character grid the PTY stream is replayed onto, sized to the REAL terminal.
//
// The size is not a tuning knob, it is a correctness requirement. A TUI paints by absolute cursor
// addressing — "put the status line at row 49" — so replaying onto a grid of a different height
// does not produce a smaller version of the screen, it produces a WRONG one: every row past the
// end is clamped onto the last row and overwrites what was already there. Same for columns: a line
// longer than the grid wraps here but not in reality, shifting every row below it.
type vt struct {
	cells [][]cell
	rows  int
	cols  int
	row   int
	col   int

	// cur is the style subsequent characters are drawn with; curID is its interned id, cached so
	// the table is consulted once per SGR sequence rather than once per character.
	cur    Style
	curID  StyleID
	styles *styleTable

	// top/bot are the scrolling region, 0-based and inclusive. Default is the whole screen.
	//
	// It is load-bearing for scrollback, not just for painting: a line only enters history when it
	// leaves the TOP OF THE SCREEN, and a program that has set an interior region is scrolling
	// inside a window, not pushing anything off the screen. Getting this wrong fills history with
	// re-drawn status lines.
	top int
	bot int

	// trackAlt decides whether `ESC [ ? 1049h` and friends switch to a separate screen.
	//
	// This is the ONE place the two callers differ, and both answers are right for their caller:
	//
	//   - Scrollback (true): the alternate screen has no scrollback in any real terminal, so a
	//     full-screen TUI must contribute NOTHING to history — otherwise a day of Claude Code
	//     fills it with repainted frames and the actual shell output is evicted.
	//
	//   - Overview (false): the card renders a SLICE of the ring, which routinely starts after the
	//     TUI already switched and can contain the switch BACK. Honouring the switch would restore
	//     a saved screen this replay never saw, and the card would go blank exactly when there is
	//     something to look at. Flattening everything onto one grid is what makes the preview show
	//     the TUI.
	trackAlt bool
	alt      bool
	// altSaved holds the normal screen while the alternate one is displayed.
	altSaved *savedScreen

	// savedRow/savedCol/savedStyle back `ESC 7` / `CSI s`.
	savedRow, savedCol int
	savedStyle         Style

	// pending holds a partial escape sequence or UTF-8 rune from the end of the previous Write.
	// Always empty in one-shot mode, where there is no "next" chunk.
	pending []byte

	// dropped counts bytes thrown away as an over-long unterminated sequence (see Write). A
	// session reporting any is a session whose history may have a garbled patch, which is worth
	// being able to see and is not worth a log line per occurrence.
	dropped int64

	// params is scratch for the CSI parameter parser, reused across sequences.
	//
	// A full-screen TUI emits tens of thousands of sequences a second; allocating a slice for each
	// one puts the terminal's repaint rate into the garbage collector's hands. The result is only
	// ever read before consumeCSI returns, so one buffer per model is safe.
	params []int

	// onScrollOff is called with each row as it leaves the top of the screen, in order.
	//
	// The slice is only valid FOR THE DURATION OF THE CALL: its backing array is immediately
	// recycled as the new bottom row, which is what keeps a scrolling terminal from allocating a
	// row per line. Implementations copy what they need.
	onScrollOff func(row []cell)
}

// savedScreen is the normal screen, parked while the alternate screen is displayed.
type savedScreen struct {
	cells    [][]cell
	row, col int
	style    Style
	top, bot int
}

func newVT(rows, cols int, styles *styleTable) *vt {
	if rows < 1 {
		rows = int(DefaultRows)
	}
	if cols < 1 {
		cols = int(DefaultCols)
	}
	if rows > maxScreenRows {
		rows = maxScreenRows
	}
	if cols > maxScreenCols {
		cols = maxScreenCols
	}
	if styles == nil {
		styles = newStyleTable()
	}
	v := &vt{rows: rows, cols: cols, styles: styles, top: 0, bot: rows - 1}
	v.cells = make([][]cell, rows)
	for i := range v.cells {
		v.cells[i] = v.blankRow()
	}
	return v
}

// blankStyle is what an erase or a freshly scrolled-in line is filled with.
//
// It carries the current BACKGROUND and nothing else — this is "background colour erase" (BCE),
// which real terminals implement and programs rely on to paint coloured regions by clearing them.
// Dropping it would turn a program's coloured panel into an uncoloured one; keeping it means a
// program that sets a background and never resets it stores coloured padding in history, which is
// exactly what its screen looks like.
func (v *vt) blankStyle() StyleID {
	if v.cur.BG.IsDefault() {
		return DefaultStyleID
	}
	return v.styles.intern(Style{BG: v.cur.BG})
}

func (v *vt) blankRow() []cell {
	row := make([]cell, v.cols)
	v.fillBlank(row)
	return row
}

func (v *vt) fillBlank(row []cell) {
	blank := cell{r: ' ', style: v.blankStyle()}
	for i := range row {
		row[i] = blank
	}
}

func (v *vt) clampRow(r int) int {
	if r < 0 {
		return 0
	}
	if r >= v.rows {
		return v.rows - 1
	}
	return r
}

func (v *vt) clampCol(c int) int {
	if c < 0 {
		return 0
	}
	if c >= v.cols {
		return v.cols - 1
	}
	return c
}

// scrollUp moves the scrolling region up by n lines.
//
// A line is handed to onScrollOff only when it leaves the top OF THE SCREEN — region top 0 — and
// only when the normal screen is displayed. Both conditions are about history, not about painting:
// a line scrolled out of an interior region is discarded by a real terminal too, and the alternate
// screen has no scrollback at all.
//
// The evicted row's backing array becomes the new bottom row. Without that, a shell printing a
// million lines allocates a million rows; with it, a session's grid allocates exactly `rows` rows
// for its lifetime.
func (v *vt) scrollUp(n int) {
	if n <= 0 {
		return
	}
	if n > v.bot-v.top+1 {
		n = v.bot - v.top + 1
	}
	emits := v.top == 0 && !v.alt && v.onScrollOff != nil
	for k := 0; k < n; k++ {
		evicted := v.cells[v.top]
		if emits {
			v.onScrollOff(evicted)
		}
		copy(v.cells[v.top:v.bot], v.cells[v.top+1:v.bot+1])
		v.fillBlank(evicted)
		v.cells[v.bot] = evicted
	}
}

// scrollDown moves the scrolling region down by n lines. Nothing enters history: the lines pushed
// off the BOTTOM are gone in a real terminal too.
func (v *vt) scrollDown(n int) {
	if n <= 0 {
		return
	}
	if n > v.bot-v.top+1 {
		n = v.bot - v.top + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.bot]
		copy(v.cells[v.top+1:v.bot+1], v.cells[v.top:v.bot])
		v.fillBlank(evicted)
		v.cells[v.top] = evicted
	}
}

// newline moves down one row, scrolling at the bottom of the region.
func (v *vt) newline() {
	if v.row == v.bot {
		v.scrollUp(1)
		return
	}
	if v.row < v.rows-1 {
		v.row++
	}
}

// reverseNewline moves up one row, scrolling at the top of the region (what `ESC M` does).
func (v *vt) reverseNewline() {
	if v.row == v.top {
		v.scrollDown(1)
		return
	}
	if v.row > 0 {
		v.row--
	}
}

// put draws a rune at the cursor and advances it, wrapping at the right edge.
//
// Zero-width runes (combining marks) attach to the PREVIOUS cell and do not move the cursor.
// Double-width runes occupy two cells: the second carries a zero rune so the line encoder knows it
// is a continuation and not a space to be trimmed.
func (v *vt) put(r rune) {
	w := runeWidth(r)
	if w == 0 {
		// Combining mark: it belongs to the cell just written. Nothing to store separately —
		// keeping it would need a per-cell rune slice, which doubles the cell for a case that
		// only affects rendering, never layout or search.
		return
	}
	if v.col+w > v.cols {
		v.col = 0
		v.newline()
	}
	v.cells[v.row][v.col] = cell{r: r, style: v.curID}
	if w == 2 && v.col+1 < v.cols {
		v.cells[v.row][v.col+1] = cell{r: 0, style: v.curID}
	}
	v.col += w
}

func (v *vt) clearRegion(fromRow, fromCol, toRow, toCol int) {
	blank := cell{r: ' ', style: v.blankStyle()}
	for r := fromRow; r <= toRow && r < v.rows; r++ {
		if r < 0 {
			continue
		}
		start, end := 0, v.cols-1
		if r == fromRow {
			start = fromCol
		}
		if r == toRow {
			end = toCol
		}
		for c := start; c <= end && c < v.cols; c++ {
			if c >= 0 {
				v.cells[r][c] = blank
			}
		}
	}
}

// RenderScreen replays raw PTY bytes onto a grid of the given size and returns the visible lines
// with trailing blank cells and trailing blank rows removed.
//
// rows/cols MUST be the size of the PTY that produced `raw` — see the vt type's doc for why a
// mismatch corrupts the result rather than merely truncating it. Non-positive values fall back to
// the spawn size.
//
// This is the one-shot mode: no alternate-screen tracking (see vt.trackAlt), no scrollback, and
// styles are parsed only so they can be dropped — the caller wants plain text.
func RenderScreen(raw string, rows, cols int) []string {
	v := newVT(rows, cols, nil)
	v.feed([]byte(raw), true)
	return v.textLines()
}

// Write feeds the next chunk of PTY output to an incremental model.
//
// A chunk boundary can fall anywhere, including inside an escape sequence or a UTF-8 rune, so the
// unconsumed tail is held until the rest arrives. That is the whole difference from the one-shot
// path, and it is not optional: a 32 KB PTY read splitting `ESC [ 3 2 m` between `3` and `2` would
// otherwise print `2m` into the terminal's history and lose the colour.
func (v *vt) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	var buf []byte
	if len(v.pending) > 0 {
		buf = append(v.pending, p...)
		v.pending = nil
	} else {
		buf = p
	}
	n := v.feed(buf, false)
	if n >= len(buf) {
		return
	}
	rest := buf[n:]
	// feed only stops early at a sequence that runs to the END of the buffer, so `rest` is always
	// a truncated escape sequence or rune. Nothing real is 4 KB long, so a tail that big is a
	// broken or hostile stream: drop it rather than hold it, which bounds the buffer in O(1). The
	// next chunk then starts scanning mid-garbage and prints some of it — which is what a real
	// terminal does with an unterminated escape, and is far better than an unbounded buffer inside
	// the daemon that owns every shell on the machine.
	if len(rest) > maxPendingBytes {
		v.dropped += int64(len(rest))
		v.pending = nil
		return
	}
	v.pending = append([]byte(nil), rest...)
}

// feed scans as much of raw as it can and returns how many bytes it consumed.
//
// When `flush` is false it stops at the first INCOMPLETE sequence or rune and reports that
// position, so the caller can hold the tail. When true it consumes everything, treating a
// truncated tail as the end of input — which is right for a one-shot replay of a fixed slice.
func (v *vt) feed(raw []byte, flush bool) int {
	i := 0
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == 0x1b: // ESC
			next, ok := v.consumeEscape(raw, i)
			if !ok {
				if flush {
					return len(raw)
				}
				return i
			}
			i = next
			continue
		case c == '\r':
			v.col = 0
			i++
			continue
		case c == '\n':
			v.newline()
			i++
			continue
		case c == '\b':
			if v.col > 0 {
				v.col--
			}
			i++
			continue
		case c == '\t':
			next := (v.col/8 + 1) * 8
			v.col = v.clampCol(next)
			i++
			continue
		case c == 0x07: // BEL
			i++
			continue
		case c < 0x20:
			i++ // other C0 controls have no bearing on layout
			continue
		}
		if !flush && !utf8.FullRune(raw[i:]) {
			return i // a rune split across chunks
		}
		r, size := utf8.DecodeRune(raw[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		v.put(r)
		i += size
	}
	return len(raw)
}

// consumeEscape handles one escape sequence starting at raw[i] (which is ESC) and returns the
// index just past it. ok=false means the sequence is truncated and more bytes are needed.
func (v *vt) consumeEscape(raw []byte, i int) (int, bool) {
	if i+1 >= len(raw) {
		return i, false
	}
	switch raw[i+1] {
	case '[':
		return v.consumeCSI(raw, i)
	case ']':
		// OSC: ESC ] ... BEL | ESC \ — window titles, clipboard writes. No layout effect.
		j := i + 2
		for j < len(raw) {
			if raw[j] == 0x07 {
				return j + 1, true
			}
			if raw[j] == 0x1b && j+1 < len(raw) && raw[j+1] == '\\' {
				return j + 2, true
			}
			j++
		}
		return i, false
	case 'P', '_', '^', 'X':
		// DCS / APC / PM / SOS: terminated by ST (ESC \). Content is never layout.
		j := i + 2
		for j < len(raw) {
			if raw[j] == 0x1b && j+1 < len(raw) && raw[j+1] == '\\' {
				return j + 2, true
			}
			if raw[j] == 0x07 {
				return j + 1, true
			}
			j++
		}
		return i, false
	case '(', ')', '*', '+', '#':
		// Charset / line-attribute selects: ESC ( B, ESC # 8 …
		if i+2 >= len(raw) {
			return i, false
		}
		return i + 3, true
	case 'M': // RI — reverse index
		v.reverseNewline()
		return i + 2, true
	case 'D': // IND — index (down one line, scrolling)
		v.newline()
		return i + 2, true
	case 'E': // NEL — next line
		v.col = 0
		v.newline()
		return i + 2, true
	case '7': // DECSC — save cursor
		v.saveCursor()
		return i + 2, true
	case '8': // DECRC — restore cursor
		v.restoreCursor()
		return i + 2, true
	case 'c': // RIS — full reset
		v.reset()
		return i + 2, true
	default:
		return i + 2, true // ESC = / ESC > keypad mode and friends
	}
}

func (v *vt) saveCursor() {
	v.savedRow, v.savedCol, v.savedStyle = v.row, v.col, v.cur
}

func (v *vt) restoreCursor() {
	v.row, v.col = v.clampRow(v.savedRow), v.clampCol(v.savedCol)
	v.setStyle(v.savedStyle)
}

func (v *vt) reset() {
	v.row, v.col = 0, 0
	v.top, v.bot = 0, v.rows-1
	v.setStyle(DefaultStyle)
	v.clearRegion(0, 0, v.rows-1, v.cols-1)
}

func (v *vt) setStyle(s Style) {
	v.cur = s
	v.curID = v.styles.intern(s)
}

// consumeCSI parses ESC [ <params> <final> and applies the ones that move, erase or colour text.
func (v *vt) consumeCSI(raw []byte, i int) (int, bool) {
	// A CSI sequence is: parameter bytes (0x30–0x3F), then intermediate bytes (0x20–0x2F), then
	// ONE final byte in 0x40–0x7E. Scanning for "the next ASCII letter" instead — which is what
	// this did while it only served a text preview — silently skips the finals that are not
	// letters, and `@` (ICH, insert characters) and `` ` `` (CHA) are exactly that. A skipped
	// final does not just lose one operation: the scan runs on to the NEXT letter and swallows
	// everything in between, so one `ESC [ 2 @` eats the text after it too.
	j := i + 2
	start := j
	for j < len(raw) && raw[j] >= 0x20 && raw[j] <= 0x3f {
		j++
	}
	if j >= len(raw) {
		return i, false
	}
	final := raw[j]
	if final < 0x40 || final > 0x7e {
		// Not a final byte at all (a control character mid-sequence, typically). A terminal
		// abandons the sequence and resumes interpreting from here, which also guarantees
		// forward progress: j is at least i+2.
		return j, true
	}
	body := raw[start:j]
	end := j + 1

	// Private sequences (ESC [ ? …) are modes: alternate screen, bracketed paste, cursor
	// visibility, mouse reporting. Only the alternate-screen switch has any bearing here, and
	// only when this model is tracking it — see vt.trackAlt.
	if len(body) > 0 && body[0] == '?' {
		if v.trackAlt && (final == 'h' || final == 'l') {
			for _, p := range v.parseParams(body[1:]) {
				switch p {
				case 1049, 1047, 47:
					if final == 'h' {
						v.enterAlt()
					} else {
						v.exitAlt()
					}
				}
			}
		}
		return end, true
	}
	// Intermediate bytes (ESC [ > c, ESC [ ! p …) are queries and resets we do not model.
	if len(body) > 0 && (body[0] == '>' || body[0] == '=' || body[0] == '!' || body[0] == '<') {
		return end, true
	}

	params := v.parseParams(body)
	p := func(idx, def int) int {
		if idx < len(params) && params[idx] > 0 {
			return params[idx]
		}
		return def
	}

	switch final {
	case 'H', 'f': // CUP — cursor position (1-based)
		v.row = v.clampRow(p(0, 1) - 1)
		v.col = v.clampCol(p(1, 1) - 1)
	case 'A': // CUU
		v.row = v.clampRow(v.row - p(0, 1))
	case 'B': // CUD
		v.row = v.clampRow(v.row + p(0, 1))
	case 'C': // CUF
		v.col = v.clampCol(v.col + p(0, 1))
	case 'D': // CUB
		v.col = v.clampCol(v.col - p(0, 1))
	case 'G', '`': // CHA — column absolute
		v.col = v.clampCol(p(0, 1) - 1)
	case 'd': // VPA — row absolute
		v.row = v.clampRow(p(0, 1) - 1)
	case 'E': // CNL
		v.row = v.clampRow(v.row + p(0, 1))
		v.col = 0
	case 'F': // CPL
		v.row = v.clampRow(v.row - p(0, 1))
		v.col = 0
	case 'J': // ED — erase in display
		switch p(0, 0) {
		case 0:
			v.clearRegion(v.row, v.col, v.rows-1, v.cols-1)
		case 1:
			v.clearRegion(0, 0, v.row, v.col)
		default:
			v.clearRegion(0, 0, v.rows-1, v.cols-1)
		}
	case 'K': // EL — erase in line
		switch p(0, 0) {
		case 0:
			v.clearRegion(v.row, v.col, v.row, v.cols-1)
		case 1:
			v.clearRegion(v.row, 0, v.row, v.col)
		default:
			v.clearRegion(v.row, 0, v.row, v.cols-1)
		}
	case 'X': // ECH — erase n characters at the cursor
		n := p(0, 1)
		v.clearRegion(v.row, v.col, v.row, minInt(v.col+n-1, v.cols-1))
	case 'L': // IL — insert n blank lines at the cursor row
		v.insertLines(p(0, 1))
	case 'M': // DL — delete n lines at the cursor row
		v.deleteLines(p(0, 1))
	case '@': // ICH — insert n blanks, shifting the rest of the line right
		v.insertChars(p(0, 1))
	case 'P': // DCH — delete n characters, shifting the rest of the line left
		v.deleteChars(p(0, 1))
	case 'S': // SU — scroll the region up
		v.scrollUp(p(0, 1))
	case 'T': // SD — scroll the region down
		v.scrollDown(p(0, 1))
	case 'r': // DECSTBM — set the scrolling region
		v.setScrollRegion(p(0, 1)-1, p(1, v.rows)-1)
	case 's': // SCP — save cursor
		v.saveCursor()
	case 'u': // RCP — restore cursor
		v.restoreCursor()
	case 'm': // SGR — select graphic rendition
		v.applySGR(params)
	}
	return end, true
}

func (v *vt) setScrollRegion(top, bot int) {
	if bot <= top {
		top, bot = 0, v.rows-1
	}
	v.top = v.clampRow(top)
	v.bot = v.clampRow(bot)
	if v.bot <= v.top {
		v.top, v.bot = 0, v.rows-1
	}
	// DECSTBM homes the cursor. Programs rely on this: after setting a region they draw from
	// the top without an explicit CUP.
	v.row, v.col = v.top, 0
}

// insertLines/deleteLines are the region-local line shuffles a full-screen editor uses. Neither
// feeds history: an inserted line pushes content DOWN (nothing leaves the top) and a deleted line
// is destroyed in place, which is what a real terminal does too.
func (v *vt) insertLines(n int) {
	if v.row < v.top || v.row > v.bot {
		return
	}
	if n > v.bot-v.row+1 {
		n = v.bot - v.row + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.bot]
		copy(v.cells[v.row+1:v.bot+1], v.cells[v.row:v.bot])
		v.fillBlank(evicted)
		v.cells[v.row] = evicted
	}
}

func (v *vt) deleteLines(n int) {
	if v.row < v.top || v.row > v.bot {
		return
	}
	if n > v.bot-v.row+1 {
		n = v.bot - v.row + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.row]
		copy(v.cells[v.row:v.bot], v.cells[v.row+1:v.bot+1])
		v.fillBlank(evicted)
		v.cells[v.bot] = evicted
	}
}

func (v *vt) insertChars(n int) {
	row := v.cells[v.row]
	if n > v.cols-v.col {
		n = v.cols - v.col
	}
	if n <= 0 {
		return
	}
	copy(row[v.col+n:], row[v.col:v.cols-n])
	blank := cell{r: ' ', style: v.blankStyle()}
	for c := v.col; c < v.col+n; c++ {
		row[c] = blank
	}
}

func (v *vt) deleteChars(n int) {
	row := v.cells[v.row]
	if n > v.cols-v.col {
		n = v.cols - v.col
	}
	if n <= 0 {
		return
	}
	copy(row[v.col:], row[v.col+n:])
	blank := cell{r: ' ', style: v.blankStyle()}
	for c := v.cols - n; c < v.cols; c++ {
		row[c] = blank
	}
}

// enterAlt switches to the alternate screen, parking the normal one.
//
// The normal screen is COPIED rather than swapped out by reference because the alternate screen
// reuses the same row arrays (the scroll path recycles them), and a shallow save would watch the
// TUI overwrite the very content it was supposed to preserve.
func (v *vt) enterAlt() {
	if v.alt {
		return
	}
	saved := &savedScreen{
		cells: make([][]cell, v.rows),
		row:   v.row, col: v.col, style: v.cur,
		top: v.top, bot: v.bot,
	}
	for i, row := range v.cells {
		saved.cells[i] = append([]cell(nil), row...)
	}
	v.altSaved = saved
	v.alt = true
	v.top, v.bot = 0, v.rows-1
	v.row, v.col = 0, 0
	v.clearRegion(0, 0, v.rows-1, v.cols-1)
}

// exitAlt restores the normal screen. A TUI that exits without one (killed mid-run) leaves the
// model in alt mode, which is correct: nothing said the normal screen came back.
func (v *vt) exitAlt() {
	if !v.alt || v.altSaved == nil {
		v.alt = false
		return
	}
	s := v.altSaved
	// The saved grid may predate a resize. Fit it to the current geometry rather than adopting
	// its shape, or the next write would index past the end of a row.
	for i := 0; i < v.rows; i++ {
		if i < len(s.cells) {
			v.cells[i] = fitRow(s.cells[i], v.cols, cell{r: ' ', style: DefaultStyleID})
		} else {
			v.cells[i] = v.blankRow()
		}
	}
	v.row, v.col = v.clampRow(s.row), v.clampCol(s.col)
	v.top, v.bot = v.clampRow(s.top), v.clampRow(s.bot)
	if v.bot <= v.top {
		v.top, v.bot = 0, v.rows-1
	}
	v.setStyle(s.style)
	v.altSaved = nil
	v.alt = false
}

func fitRow(row []cell, cols int, blank cell) []cell {
	if len(row) == cols {
		return row
	}
	out := make([]cell, cols)
	n := copy(out, row)
	for i := n; i < cols; i++ {
		out[i] = blank
	}
	return out
}

// applySGR updates the current style. An empty parameter list means `ESC [ m`, which is a reset.
//
// 38/48 take a sub-list (`;5;n` for a palette index, `;2;r;g;b` for 24-bit) and are the only
// place the parameters are not independent, which is why this is a loop with an index rather than
// a range.
func (v *vt) applySGR(params []int) {
	if len(params) == 0 {
		v.setStyle(DefaultStyle)
		return
	}
	s := v.cur
	for i := 0; i < len(params); i++ {
		switch n := params[i]; {
		case n == 0:
			s = DefaultStyle
		case n == 1:
			s.Attrs |= AttrBold
		case n == 2:
			s.Attrs |= AttrDim
		case n == 3:
			s.Attrs |= AttrItalic
		case n == 4:
			s.Attrs |= AttrUnderline
		case n == 5 || n == 6:
			s.Attrs |= AttrBlink
		case n == 7:
			s.Attrs |= AttrInverse
		case n == 8:
			s.Attrs |= AttrHidden
		case n == 9:
			s.Attrs |= AttrStrike
		case n == 21 || n == 22:
			s.Attrs &^= AttrBold | AttrDim
		case n == 23:
			s.Attrs &^= AttrItalic
		case n == 24:
			s.Attrs &^= AttrUnderline
		case n == 25:
			s.Attrs &^= AttrBlink
		case n == 27:
			s.Attrs &^= AttrInverse
		case n == 28:
			s.Attrs &^= AttrHidden
		case n == 29:
			s.Attrs &^= AttrStrike
		case n >= 30 && n <= 37:
			s.FG = IndexedColor(uint8(n - 30))
		case n == 38:
			if c, used, ok := extendedColor(params, i); ok {
				s.FG = c
				i += used
			}
		case n == 39:
			s.FG = ColorDefault
		case n >= 40 && n <= 47:
			s.BG = IndexedColor(uint8(n - 40))
		case n == 48:
			if c, used, ok := extendedColor(params, i); ok {
				s.BG = c
				i += used
			}
		case n == 49:
			s.BG = ColorDefault
		case n >= 90 && n <= 97:
			s.FG = IndexedColor(uint8(n - 90 + 8))
		case n >= 100 && n <= 107:
			s.BG = IndexedColor(uint8(n - 100 + 8))
		}
	}
	v.setStyle(s)
}

// extendedColor reads the `;5;n` or `;2;r;g;b` tail of a 38/48 parameter, returning the colour and
// how many EXTRA parameters it consumed. A malformed tail returns ok=false and is skipped rather
// than guessed at — a wrong colour that persists is worse than no colour change.
func extendedColor(params []int, i int) (Color, int, bool) {
	if i+1 >= len(params) {
		return 0, 0, false
	}
	switch params[i+1] {
	case 5:
		if i+2 >= len(params) {
			return 0, 0, false
		}
		return IndexedColor(uint8(clampInt(params[i+2], 0, 255))), 2, true
	case 2:
		if i+4 >= len(params) {
			return 0, 0, false
		}
		return RGBColor(
			uint8(clampInt(params[i+2], 0, 255)),
			uint8(clampInt(params[i+3], 0, 255)),
			uint8(clampInt(params[i+4], 0, 255)),
		), 4, true
	}
	return 0, 0, false
}

// parseParams splits a CSI parameter body into numbers, into the model's reusable scratch slice.
//
// `;` and `:` are both separators: `38:5:n` is the sub-parameter spelling of `38;5;n` and means
// the same thing, so normalising here leaves applySGR one shape to handle instead of two. A
// parameter that is not a plain number reads as 0, which is what a terminal does with `ESC [ ;5 m`
// and with garbage alike.
func (v *vt) parseParams(body []byte) []int {
	v.params = v.params[:0]
	if len(body) == 0 {
		return v.params
	}
	n, ok := 0, true
	for _, ch := range body {
		switch {
		case ch == ';' || ch == ':':
			if !ok {
				n = 0
			}
			v.params = append(v.params, n)
			n, ok = 0, true
		case ch >= '0' && ch <= '9':
			n = n*10 + int(ch-'0')
			if n > 1<<20 {
				n = 1 << 20 // a parameter this large is nonsense; clamp rather than overflow
			}
		default:
			ok = false
		}
	}
	if !ok {
		n = 0
	}
	v.params = append(v.params, n)
	return v.params
}

// textLines reads the grid back as plain text: trailing spaces trimmed per row, trailing blank
// rows dropped. This is the overview's view — styles exist in the grid but are not its business.
func (v *vt) textLines() []string {
	out := make([]string, 0, v.rows)
	for _, row := range v.cells {
		out = append(out, strings.TrimRight(rowText(row), " "))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func rowText(row []cell) string {
	var b strings.Builder
	b.Grow(len(row))
	for _, c := range row {
		if c.r == 0 {
			continue // the second half of a double-width character
		}
		b.WriteRune(c.r)
	}
	return b.String()
}

// resize changes the grid, keeping the top-left content and clamping the cursor.
//
// Deliberately does NOT reflow: re-wrapping existing lines to a new width is what tmux only
// learned to do in 3.1, it needs per-line "this line was wrapped" tracking that this model does
// not keep, and getting it half-right corrupts history rather than improving it. A narrower window
// therefore truncates what was already drawn — which is also what happens in a terminal that has
// reflow turned off.
func (v *vt) resize(rows, cols int) {
	if rows < 1 || cols < 1 {
		return
	}
	if rows > maxScreenRows {
		rows = maxScreenRows
	}
	if cols > maxScreenCols {
		cols = maxScreenCols
	}
	if rows == v.rows && cols == v.cols {
		return
	}
	blank := cell{r: ' ', style: DefaultStyleID}
	next := make([][]cell, rows)
	for i := range next {
		if i < len(v.cells) {
			next[i] = fitRow(v.cells[i], cols, blank)
		} else {
			next[i] = make([]cell, cols)
			for j := range next[i] {
				next[i][j] = blank
			}
		}
	}
	v.cells = next
	v.rows, v.cols = rows, cols
	v.row = v.clampRow(v.row)
	v.col = v.clampCol(v.col)
	v.top, v.bot = 0, rows-1
	if v.altSaved != nil {
		// The parked normal screen is fitted lazily on exit (see exitAlt); its row count is
		// clamped there against the then-current geometry.
		v.altSaved.top, v.altSaved.bot = 0, rows-1
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
