package muxd

import (
	"strings"
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
// No mouse/bracketed-paste state, no reflow on resize (except in the terminal-model mode, which
// ports xterm's — see vt_reflow.go), no OSC-8 hyperlink capture, no double-height lines. What IS
// handled is the set of operations that decide where text ends up and what colour it is — because
// those are the two questions both callers ask — which includes the character sets (vt_charset.go),
// the marks that stack on the character before them (vt_combine.go), and the way xterm.js reads
// bytes that are not UTF-8 or that interrupt a control sequence (vt_parse.go).

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
	// ext is the id of the marks stacked on r (see vt_combine.go); 0 for the cell nothing was stacked
	// on, which is nearly all of them. It occupies what would be padding, so a cell stays eight bytes.
	ext uint16
}

// nullRune marks a cell nothing was ever written to — or that was erased. A terminal keeps the
// difference between that and a written space ("content" in xterm's terms), and the difference
// decides where a line ends when it is re-wrapped on resize: an erased tail is not part of the
// line, typed spaces are. Rendered as a space everywhere text is read out. (0 is taken: it marks
// the second cell of a double-width character.)
const nullRune rune = -1

// cellWidth is how many columns a cell's content spans, as a terminal counts it: 2 for the lead
// cell of a double-width character, 0 for its continuation, 1 for everything else (a null cell
// included — it is one empty column). A cell holding a zero-width character ON ITS OWN — a mark with
// nothing before it to stack on — has width 0 too, and, unlike a continuation, content: it takes a
// column (the cursor moved past it) but spans none, and a row that ends in one is that much shorter
// to a reader that measures by width.
func cellWidth(c cell) int {
	switch {
	case c.r == 0:
		return 0
	case c.r < 0:
		return 1
	case c.ext&extWide != 0:
		return 2
	default:
		return runeWidth(c.r)
	}
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
	//
	// savedRow is xterm.js's savedY minus ybase, not a screen row. xterm keeps the saved row
	// ABSOLUTE in its buffer (ybase + y) and DECRC subtracts the CURRENT ybase, so every line that
	// scrolls into the scrollback moves the saved position one row up the screen with its content —
	// and past the top it restores to row 0. A model that held a screen row would put the cursor
	// somewhere else the moment a program saved, printed past the bottom, and restored. It is
	// therefore signed, decremented by scrollUp, and clamped to the screen only when it is used.
	savedRow, savedCol int
	savedStyle         Style
	// savedCharset is the character set DECSC kept (xterm's Buffer.savedCharset — per screen, like the
	// rest of the slot): the set in use at the time, as a final byte, 0 for the US set.
	savedCharset byte

	// ybase is the number of rows the NORMAL screen has scrolled into its scrollback since that was
	// last cleared — xterm's Buffer.ybase — capped at sbCap (xterm stops counting when the
	// scrollback is full, because the oldest line is recycled instead of the base moving). It is
	// kept even by a model that emits nowhere, because DECRC depends on it (see savedRow).
	ybase int
	sbCap int // 0 = uncapped
	// altAbove/altAboveWrap/altMaxLen are the part of xterm's alternate BUFFER that is not the screen.
	// The alternate buffer is meant to hold exactly the screen, but it keeps the line capacity it had
	// when it was last cleared — and a terminal that was made smaller since (the buffer is only
	// cleared when the alternate screen is LEFT) enters it with room to spare. Lines that scroll off
	// the top then stay above the screen, and a later enlargement pulls them back into view. altMaxLen
	// is that capacity; altAbove the lines held above the screen, oldest first.
	altAbove     [][]cell
	altAboveWrap []bool
	altMaxLen    int
	// sbFresh is how many of the newest scrollback rows are laid out at the current width: the ones
	// the last resize re-wrapped, plus every row that has scrolled off since. Older rows still have
	// the width they were last given (see History.layOutScrollback).
	sbFresh int

	// modes is the last observed state of every DEC private mode (`ESC [ ? n h|l`) that is STATE
	// rather than structure — mouse reporting, bracketed paste, cursor keys, cursor visibility —
	// and modeSeq orders them by when each last changed.
	//
	// None of it moves a character, which is why this model ignored it while its only callers were
	// a preview card and scrollback. A terminal RESTORED from this model is a third caller, and to
	// it these are the difference between a correct screen whose wheel, paste and arrow keys work
	// and a correct screen whose wheel, paste and arrow keys silently do not. Order matters because
	// some modes supersede others (1000/1002/1003 are one setting spelled three ways): re-asserting
	// them in the order they last changed reproduces the final state without modelling that.
	//
	// Only OBSERVED modes are recorded. A mode this model never saw set is unknown, not "off" — the
	// same rule paste_mode.go learned the hard way.
	modes   map[int]modeEntry
	modeSeq uint64
	// cursorShape is the last DECSCUSR (`ESC [ n SP q`) parameter, -1 until one is seen.
	cursorShape int

	// noWrap is DECAWM reset (`ESC [ ? 7 l`): a character at the right margin overwrites the last
	// cell instead of wrapping. Recorded in modes like any other, but unlike the others it moves
	// characters, so the model must obey it — a restore drawn from a model that wrapped where the
	// terminal did not is a different screen.
	noWrap bool

	// insertMode is IRM (`CSI 4 h`): a printed character shifts the rest of the row right instead of
	// overwriting it.
	insertMode bool
	// lnm is LNM (`CSI 20 h`), which xterm.js stores as its convertEol option: a line feed also
	// returns the carriage. It is an option rather than a mode there, so RIS does not clear it. The
	// browser's terminal starts with it on (convertEol: true), and so does NewTerminalModel.
	lnm bool

	// repWidth is the width of the last character printed — 0 once any control, escape or CSI
	// sequence has run since. It is what REP (`CSI n b`) repeats: xterm keeps the parser's join
	// state of the preceding character and clears it on every dispatch except the print itself.
	//
	// It is also what a zero-width character reads to know whether there is anything to stack on: a
	// mark joins the cell before it exactly when this is non-zero, so a mark that follows a control
	// or a sequence — however short — stands in a cell of its own.
	repWidth int
	// charset is the G0..G3 designations and the set in use (vt_charset.go).
	charset charsetState
	// combos is the table the cells' ext ids point into (vt_combine.go).
	combos *comboTable
	// run is scratch for the printable run feed gathers, reused across calls; rep is REP's.
	run []rune
	rep []rune

	// otherSlot is the DECSC slot of the screen NOT displayed. A terminal keeps one per screen —
	// `ESC 7` on the alternate screen must not clobber the normal screen's — and swaps them with
	// the screen (see enterAlt/exitAlt). savedRow/savedCol/savedStyle are the displayed screen's.
	otherSlot cursorSlot

	// pending holds a partial escape sequence or UTF-8 rune from the end of the previous Write.
	// Always empty in one-shot mode, where there is no "next" chunk.
	pending []byte
	// sq is scratch for the escape or control sequence being read (see vt_parse.go); carry, when
	// hasCarry, replaces the unconsumed tail as what is held for the next write.
	sq       seqScratch
	carry    []byte
	hasCarry bool

	// dropped counts bytes thrown away as an over-long unterminated sequence (see Write). A
	// session reporting any is a session whose history may have a garbled patch, which is worth
	// being able to see and is not worth a log line per occurrence.
	dropped int64

	// params is scratch for the CSI parameter parser, reused across sequences.
	//
	// A full-screen TUI emits tens of thousands of sequences a second; allocating a slice for each
	// one puts the terminal's repaint rate into the garbage collector's hands. The result is only
	// ever read before dispatchCSI returns, so one buffer per model is safe.
	params []int
	// subs holds the colon sub-parameters of the sequence just parsed, all of them in one slice, and
	// subAt[i] is the [lo, hi) range of them that belong to params[i] (see parseParams).
	subs  []int
	subAt [][2]int

	// wrap[r] says row r CONTINUES row r-1: the cursor ran off the right margin and the next
	// character carried on here (xterm's isWrapped). It is what lets a line be re-wrapped when the
	// width changes — and what a restore needs to tell a typed line break from a soft one.
	// Index-aligned with cells; every operation that moves rows moves it too.
	wrap []bool

	// reflow makes resize behave as xterm.js does — re-wrap soft-wrapped lines to the new width,
	// trade lines with the scrollback as rows come and go — and makes ED 3 / RIS clear the
	// scrollback. A model that must stay indistinguishable from the browser's terminal (the
	// server's restore point) sets it; the daemon's historian does not, because its scrollback is
	// an append-only record whose line numbers clients page by. See vt_reflow.go.
	reflow bool
	// sb is the scrollback a reflowing resize reads back from and rewrites. Set with reflow.
	sb scrollback

	// onScrollOff is called with each row as it leaves the top of the screen, in order, with its
	// wrap flag.
	//
	// The slice is only valid FOR THE DURATION OF THE CALL: its backing array is immediately
	// recycled as the new bottom row, which is what keeps a scrolling terminal from allocating a
	// row per line. Implementations copy what they need.
	onScrollOff func(row []cell, wrapped bool)
}

type cursorSlot struct {
	row, col int
	style    Style
	charset  byte
}

type modeEntry struct {
	on  bool
	seq uint64
}

// structuralModes are private modes that are not restorable as a flag: the alternate screen is
// restored as a screen (LiveState.Alt/Parked), 1048 is an action (save/restore cursor), 2026 is a
// bracket around one frame — re-asserting a dangling 2026h would freeze a restored terminal's
// renderer — and 3 (DECCOLM) resizes and clears the screen it is sent to. 6 (DECOM) is left out
// because re-asserting it homes the cursor and makes every later CUP region-relative, and this model
// does not implement origin addressing to match; none of the real sessions it was measured against
// ever set it (2026-10-13).
var structuralModes = map[int]bool{3: true, 6: true, 47: true, 1047: true, 1048: true, 1049: true, 2026: true}

func (v *vt) noteMode(mode int, on bool) {
	if structuralModes[mode] {
		return
	}
	if mode == 7 {
		v.noWrap = !on
	}
	if v.modes == nil {
		v.modes = make(map[int]modeEntry)
	}
	v.modeSeq++
	v.modes[mode] = modeEntry{on: on, seq: v.modeSeq}
}

// savedScreen is the normal screen, parked while the alternate screen is displayed.
type savedScreen struct {
	cells    [][]cell
	wrap     []bool
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
	v := &vt{rows: rows, cols: cols, styles: styles, top: 0, bot: rows - 1, cursorShape: -1, altMaxLen: rows, combos: newComboTable()}
	v.cells = make([][]cell, rows)
	for i := range v.cells {
		v.cells[i] = v.blankRow()
	}
	v.wrap = make([]bool, rows)
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
	fillRow(row, cell{r: nullRune, style: v.blankStyle()})
}

// blankedRow is what a row that just left the grid becomes when it comes back in as a blank one.
// xterm builds that line new, at the screen's width, so a row that has kept a wider width (the
// alternate screen after a narrowing resize) is replaced rather than recycled.
func (v *vt) blankedRow(row []cell) []cell {
	if len(row) != v.cols {
		return v.blankRow()
	}
	v.fillBlank(row)
	return row
}

func fillRow(row []cell, c cell) {
	for i := range row {
		row[i] = c
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

// scrollUp moves the scrolling region up by n lines: what a line feed, an index or an autowrap at
// the bottom of the region does (xterm's BufferService.scroll). SU is NOT this — see scrollRegionUp.
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
	feeds := v.top == 0 && !v.alt
	for k := 0; k < n; k++ {
		evicted := v.cells[v.top]
		if feeds && v.onScrollOff != nil {
			v.onScrollOff(evicted, v.wrap[v.top])
		}
		if feeds {
			v.sbFresh++
		}
		if feeds && (v.sbCap == 0 || v.ybase < v.sbCap) {
			v.ybase++
			v.savedRow-- // the saved row is absolute in xterm; see vt.savedRow
		}
		if v.alt && v.top == 0 {
			v.scrollIntoAltBuffer(evicted, v.wrap[v.top])
		}
		copy(v.cells[v.top:v.bot], v.cells[v.top+1:v.bot+1])
		copy(v.wrap[v.top:v.bot], v.wrap[v.top+1:v.bot+1])
		v.cells[v.bot] = v.blankedRow(evicted)
		v.wrap[v.bot] = false
	}
}

// scrollIntoAltBuffer is what xterm's scroll() does with the row leaving the top of the alternate
// screen: if the buffer has room it keeps the row above the screen (the base moves, and with it the
// absolute saved row); if it is full it drops its OLDEST line — the one above the screen if there is
// one, which is how the screen's own top row ends up above it — and otherwise the row is just gone.
func (v *vt) scrollIntoAltBuffer(row []cell, wrapped bool) {
	kept := append([]cell(nil), row...)
	switch {
	case len(v.altAbove)+v.rows < v.altMaxLen:
		v.altAbove = append(v.altAbove, kept)
		v.altAboveWrap = append(v.altAboveWrap, wrapped)
		v.savedRow--
	case len(v.altAbove) > 0:
		v.altAbove = append(v.altAbove[1:], kept)
		v.altAboveWrap = append(v.altAboveWrap[1:], wrapped)
	}
}

// scrollRegionUp is SU (`CSI n S`). Unlike a line feed it never feeds the scrollback, even with the
// region at the top of the screen: xterm.js splices the top line out of the buffer and a blank one
// in at the bottom ("FIXME: scrolled out lines at top = 1 should add to scrollback", its own
// comment), and a model that kept the line would hold history the browser does not.
func (v *vt) scrollRegionUp(n int) {
	if n > v.bot-v.top+1 {
		n = v.bot - v.top + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.top]
		copy(v.cells[v.top:v.bot], v.cells[v.top+1:v.bot+1])
		copy(v.wrap[v.top:v.bot], v.wrap[v.top+1:v.bot+1])
		v.cells[v.bot] = v.blankedRow(evicted)
		v.wrap[v.bot] = false
	}
}

// scrollDown moves the scrolling region down by n lines. Nothing enters history: the lines pushed
// off the BOTTOM are gone in a real terminal too.
//
// bce says whether the blank rows take the current background. RI does (xterm builds its blank line
// from the erase attributes); SD does not — xterm.js hands scrollDown the default attributes, so a
// program that sets a background and scrolls down gets uncoloured rows.
func (v *vt) scrollDown(n int, bce bool) {
	if n <= 0 {
		return
	}
	if n > v.bot-v.top+1 {
		n = v.bot - v.top + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.bot]
		copy(v.cells[v.top+1:v.bot+1], v.cells[v.top:v.bot])
		copy(v.wrap[v.top+1:v.bot+1], v.wrap[v.top:v.bot])
		if bce {
			evicted = v.blankedRow(evicted)
		} else if len(evicted) != v.cols {
			evicted = make([]cell, v.cols)
			fillRow(evicted, cell{r: nullRune, style: DefaultStyleID})
		} else {
			fillRow(evicted, cell{r: nullRune, style: DefaultStyleID})
		}
		v.cells[v.top] = evicted
		v.wrap[v.top] = false
	}
}

// restrictCursor pulls a pending wrap (col == cols) back onto the last column, as a terminal does
// before any cursor movement that is not printing — a line feed after a full row does NOT land at
// the start of the next one.
func (v *vt) restrictCursor() { v.restrictCursorTo(v.cols - 1) }

// restrictCursorTo is xterm's _restrictCursor(maxCol): both coordinates onto the screen, the column
// no further than maxCol. (ED and EL pass cols, so that a pending wrap survives them.)
func (v *vt) restrictCursorTo(maxCol int) {
	v.col = min(maxCol, max(0, v.col))
	v.row = min(v.rows-1, max(0, v.row))
}

// setCursor is xterm's _setCursor: an absolute position, put on the screen. (Origin mode is not
// modelled, so there is no region offset.)
func (v *vt) setCursor(col, row int) {
	v.col, v.row = col, row
	v.restrictCursor()
}

// moveCursor is xterm's _moveCursor: a relative move that FIRST restricts the cursor to the screen
// and only then adds the offset. The order is observable — from a pending wrap (col == cols), `CSI D`
// lands at cols-2, not cols-1.
func (v *vt) moveCursor(dCol, dRow int) {
	v.restrictCursor()
	v.setCursor(v.col+dCol, v.row+dRow)
}

// cursorUp is CUU: it stops at the top margin when the cursor is below it (xterm: "if the cursor
// would pass the top scroll margin, it will stop there"), and moves freely when it is already above
// the region.
func (v *vt) cursorUp(n int) {
	if d := v.row - v.top; d >= 0 {
		n = min(d, n)
	}
	v.moveCursor(0, -n)
}

// cursorDown is CUD, stopping at the bottom margin the same way.
func (v *vt) cursorDown(n int) {
	if d := v.bot - v.row; d >= 0 {
		n = min(d, n)
	}
	v.moveCursor(0, n)
}

// index moves down one row, scrolling at the bottom of the region (IND, and the second half of NEL).
func (v *vt) index() {
	v.restrictCursor()
	if v.row == v.bot {
		v.scrollUp(1)
		return
	}
	if v.row < v.rows-1 {
		v.row++
	}
}

// lineFeed is LF / VT / FF: an index that also ends the wrap of the row it lands on. An explicit
// line feed says "the next row is a new line" — a soft wrap left there by earlier output no longer
// joins it to this one.
func (v *vt) lineFeed() {
	if v.lnm {
		v.col = 0
	}
	if v.row == v.bot {
		v.scrollUp(1)
	} else if v.row < v.rows-1 {
		v.row++
		v.wrap[v.row] = false
	}
	// A pending wrap is cancelled, last: the cursor comes to rest on the last column — or, after a
	// character with no width that had nowhere to go (see printRune), one past it, on the last.
	if v.col >= v.cols {
		v.col--
	}
}

// reverseNewline moves up one row, scrolling at the top of the region (what `ESC M` does).
func (v *vt) reverseNewline() {
	v.restrictCursor()
	if v.row == v.top {
		v.scrollDown(1, true)
		return
	}
	if v.row > 0 {
		v.row--
	}
}

// wrapLine is the autowrap's move to the next row: like index, but the row it lands on is marked
// as continuing the one it left. Returns the row index the cursor came from as it is AFTER any
// scroll (-1 if that row scrolled out of a one-line region).
func (v *vt) wrapLine() int {
	if v.row == v.bot {
		v.scrollUp(1)
		v.wrap[v.bot] = true
		if v.bot > v.top {
			return v.bot - 1
		}
		return -1
	}
	if v.row < v.rows-1 {
		v.row++
		v.wrap[v.row] = true
		return v.row - 1
	}
	// Below the scrolling region on the last row: the cursor cannot move, and the terminal wraps
	// onto the same row, marking it.
	v.wrap[v.row] = true
	return v.row
}

// print writes one run of printable characters — xterm's InputHandler.print, which the parser calls
// once per unbroken run of them (a control, an escape sequence or the end of the write ends it).
//
// The run is the unit, not the character, because two of print's repairs happen once per RUN: the
// wide character whose second half the first character lands on is emptied before the loop, and a
// stray second half the cursor stops on is emptied after it. They are the same thing per character
// as long as the cursor advances — it is autowrap OFF that tells them apart: the cursor sticks on
// the last column, every later character overwrites the previous one, and xterm never revisits the
// wide character those overwrote the half of. (Which is a mess the browser shows, and so must we.)
//
// The character set is read once per run too: only a control or an escape changes it, and either
// ends the run.
//
// `times` repeats rs as a whole, which is REP: xterm builds one buffer of the repeats and prints it
// in a single call.
func (v *vt) print(rs []rune, times int) {
	if len(rs) == 0 || times <= 0 {
		return
	}
	cs := v.charset
	if row := v.cells[v.row]; v.col > 0 && v.col-1 < len(row) && cellWidth(row[v.col-1]) == 2 {
		row[v.col-1] = cell{r: nullRune, style: v.curID}
	}
	for t := 0; t < times; t++ {
		for _, r := range rs {
			v.printRune(cs.translate(r))
		}
	}
	if row := v.cells[v.row]; v.col < v.cols && row[v.col].r == 0 {
		row[v.col] = cell{r: nullRune, style: v.curID}
	}
}

// printRune draws a rune at the cursor and advances it, wrapping at the right edge.
//
// A rune that xterm's table gives no width (a combining mark, a joiner, a variation selector)
// stacks on the cell before it and does not move the cursor — but only when the character just
// before it in the stream was a printed one (repWidth != 0). Anywhere else, after a control or a
// sequence or at the start of a line, it has nothing to stack on and is written into a cell of its
// own, which has no width and still takes a column: the cursor moves past it.
// Double-width runes occupy two cells: the second carries a zero rune so the line encoder knows it
// is a continuation and not a space to be trimmed.
func (v *vt) printRune(r rune) {
	w := runeWidth(r)
	join := w == 0 && v.repWidth != 0
	if !join {
		v.repWidth = w // set before any early return below — xterm records it before the width checks
	}
	if join && v.col > 0 {
		// Stack on the character just printed: the cell before the cursor, or the one before that
		// when the cursor is past the second half of a wide one. (Past the end of the row, with the
		// wrap pending, the cursor is one past the last cell and this is still that cell.)
		row := v.cells[v.row]
		at := v.col - 1
		if at >= len(row) || cellWidth(row[at]) == 0 {
			at-- // past the end of the row counts as a cell without width, as in xterm
		}
		if at >= 0 && at < len(row) && row[at].r > 0 {
			row[at] = v.combos.with(row[at], r)
			if v.repWidth == 2 && cellWidth(row[at]) == 1 {
				// xterm gives the cell the width of the character the mark follows. That is the cell's
				// own, unless the character was a wide one that autowrap-off dropped at the margin.
				row[at].ext |= extWide
			}
		} else if at >= 0 && at < len(row) {
			// Nothing there to stack on (the wide character the mark follows was dropped, and the cell
			// before the margin is empty): the mark becomes the cell's content, with that width.
			row[at] = cell{r: r, style: row[at].style}
			if v.repWidth == 2 {
				row[at].ext = extWide
			}
		}
		return
	}
	if v.col+w > v.cols {
		if v.noWrap {
			v.col = v.cols - 1 // overwrite at the margin, as the terminal does
			if w == 2 {
				return // a double-width character that cannot fit is dropped, as xterm does
			}
		} else {
			oldCol := v.col
			v.col = 0
			if old := v.wrapLine(); old >= 0 {
				// Whatever the wrapped character did not use at the end of the row it left is
				// emptied: a double-width character that did not fit leaves a null last cell.
				for c := oldCol; c < v.cols; c++ {
					v.cells[old][c] = cell{r: nullRune, style: v.curID}
				}
			}
		}
	}
	row := v.cells[v.row]
	if v.insertMode {
		// IRM: the rest of the row moves right by the character's width, filled with the PEN's
		// null cell (not the erase attributes ICH uses), and a wide character shoved onto the last
		// column — where only half of it would fit — is lost.
		insertCells(row, v.col, w, cell{r: nullRune, style: v.curID})
		if cellWidth(row[v.cols-1]) == 2 {
			row[v.cols-1] = cell{r: nullRune, style: v.curID}
		}
	}
	// A character with no width at the margin, its wrap pending, has no cell to go in: xterm's write
	// past the end of the row goes nowhere and the cursor moves on regardless.
	if v.col < len(row) {
		row[v.col] = cell{r: r, style: v.curID}
	}
	if w == 2 && v.col+1 < v.cols {
		row[v.col+1] = cell{r: 0, style: v.curID}
	}
	v.col += max(w, 1)
}

func (v *vt) clearRegion(fromRow, fromCol, toRow, toCol int) {
	blank := cell{r: nullRune, style: v.blankStyle()}
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
		replaceCells(v.cells[r], max(start, 0), end+1, blank)
	}
}

// resetRows is xterm's _resetBufferLine over rows from..to: every cell of the row, whatever its
// length, becomes an erased one, and the row no longer continues the one above it.
func (v *vt) resetRows(from, to int) {
	blank := cell{r: nullRune, style: v.blankStyle()}
	for r := max(from, 0); r <= to && r < v.rows; r++ {
		fillRow(v.cells[r], blank)
		v.wrap[r] = false
	}
}

// replaceCells fills row[start:end] with fill, keeping double-width characters whole: one cut in
// half at either edge is emptied too (xterm's BufferLine.replaceCells — the repairs come first and
// do not depend on there being anything to fill, and the bound is the ROW's length, which on the
// alternate screen can be more than the screen's width, see vt.resize).
func replaceCells(row []cell, start, end int, fill cell) {
	if start > 0 && start-1 < len(row) && cellWidth(row[start-1]) == 2 {
		row[start-1] = cell{r: nullRune, style: fill.style}
	}
	if end < len(row) && end > 0 && cellWidth(row[end-1]) == 2 {
		row[end] = cell{r: nullRune, style: fill.style}
	}
	for ; start < end && start < len(row); start++ {
		row[start] = fill
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
	v.carry, v.hasCarry = nil, false
	n := v.feed(buf, false)
	if n >= len(buf) {
		return
	}
	rest := buf[n:]
	if v.hasCarry {
		rest = v.carry // the same sequence, less the controls that have already acted
	}
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

// saveCursor is DECSC / SCOSC. The column is saved as it stands — a pending wrap (col == cols)
// included — and the row relative to the scrollback's base (see vt.savedRow), which is the row
// itself at the moment of saving.
func (v *vt) saveCursor() {
	v.savedRow, v.savedCol, v.savedStyle = v.row, v.col, v.cur
	v.savedCharset = v.charset.gl
}

// restoreCursor is DECRC / SCORC: the saved column, the saved row less however far the screen has
// scrolled since (never above the top), the saved pen — then onto the screen, which is what turns a
// saved pending wrap into the last column.
func (v *vt) restoreCursor() {
	v.col, v.row = v.savedCol, max(v.savedRow, 0)
	v.setStyle(v.savedStyle)
	// The set that was in use comes back, as the one GL draws with — not the designations, not the
	// level (xterm: CharsetService.charset is assigned directly).
	v.charset.gl = v.savedCharset
	v.restrictCursor()
}

// softReset is DECSTR (`CSI ! p`): the terminal's settings go back to their defaults — insert mode
// off, autowrap on, the cursor shown, the scrolling region and the pen reset, the saved cursor zeroed
// — while the screen and the cursor's place stay. (xterm.js leaves the mouse modes alone; RIS is the
// one that resets those.)
func (v *vt) softReset() {
	v.top, v.bot = 0, v.rows-1
	v.setStyle(DefaultStyle)
	v.insertMode = false
	v.noWrap = false
	for m := range v.modes {
		switch m {
		case 7, 25:
			v.noteMode(m, true)
		case 1, 6, 45, 66, 1004, 2004, 2026:
			v.noteMode(m, false)
		}
	}
	v.savedRow, v.savedCol, v.savedStyle = 0, 0, DefaultStyle
	// The character sets go back to US ASCII, and so does the one DECSC kept (on the displayed screen).
	v.charset.reset()
	v.savedCharset = 0
}

// repeatPreceding is REP: print the character just printed n more times, through the same path as
// typed text, so wrapping, insert mode and autowrap-off all apply. Nothing happens unless the last
// thing the stream did was print one (see vt.repWidth).
func (v *vt) repeatPreceding(n int) {
	w := v.repWidth
	if w == 0 {
		return
	}
	at := v.col - w
	if at < 0 || at >= v.cols {
		return
	}
	c := v.cells[v.row][at]
	if c.r <= 0 {
		return // xterm's getString of an empty cell is the empty string: there is nothing to repeat
	}
	// The cell's whole string, marks and all — xterm reads it back with getString and prints it as
	// if it had come off the wire, which is why it stacks the marks again on every repeat.
	v.rep = append(v.rep[:0], c.r)
	for _, m := range v.combos.get(c.ext) {
		v.rep = append(v.rep, m)
	}
	v.print(v.rep, n)
}

// reset is RIS. xterm.js builds both buffers afresh, so a full-screen program's alternate screen is
// gone and the normal one — empty, its scrollback cleared, its DECSC slot zeroed — is what is shown.
func (v *vt) reset() {
	v.row, v.col = 0, 0
	v.top, v.bot = 0, v.rows-1
	v.setStyle(DefaultStyle)
	// The parked normal screen is discarded with everything else, not restored: what is displayed
	// afterwards is the blank grid cleared below, as the normal screen.
	v.alt, v.altSaved = false, nil
	v.altAbove, v.altAboveWrap, v.altMaxLen = nil, nil, v.rows
	// Both buffers are built new, so every row is a fresh one at the screen's width — not the
	// displayed rows wiped, which would keep the extra width an alternate screen can have.
	for r := range v.cells {
		v.cells[r] = v.blankRow()
		v.wrap[r] = false
	}
	if v.reflow && v.sb != nil {
		v.sb.clear()
	}
	v.ybase = 0
	v.sbFresh = 0
	v.savedRow, v.savedCol, v.savedStyle = 0, 0, DefaultStyle
	v.otherSlot = cursorSlot{style: DefaultStyle}
	v.charset.reset()
	v.savedCharset = 0
	v.insertMode = false
	// RIS returns every mode to its power-on value. Recorded as an explicit change rather than
	// forgotten: a restore onto a terminal that still has the old mode set must be told to undo it.
	for m := range v.modes {
		v.noteMode(m, m == 7 || m == 25)
	}
	v.noWrap = false
	if v.cursorShape >= 0 {
		v.cursorShape = 0
	}
}

func (v *vt) setStyle(s Style) {
	v.cur = s
	v.curID = v.styles.intern(s)
}

// dispatchCSI carries out one control sequence the parser has read to its final byte: `body` is the
// private-marker prefix (one of < = > ?, if any) followed by the parameter bytes, `inter` the
// intermediate bytes. The parser (vt_parse.go) has already ruled out everything xterm ignores — a
// marker after a parameter, a parameter after an intermediate, a byte that is not part of a sequence.
func (v *vt) dispatchCSI(body []byte, inter string, final byte) {
	// Intermediate bytes (0x20–0x2f) change which function the final byte names: `CSI Ps SP q` is
	// DECSCUSR, `CSI ! p` is DECSTR, `CSI Ps SP @` scrolls left. One this model does not know has to
	// be IGNORED — read as the bare function, `SP @` would insert characters.
	hasPrefix := len(body) > 0 && body[0] >= '<' && body[0] <= '?'
	switch {
	case inter == " " && final == 'q' && !hasPrefix:
		// DECSCUSR — cursor shape. Presentation, not layout; recorded for a restore.
		v.cursorShape = 0
		if ps := v.parseParams(body); len(ps) > 0 {
			v.cursorShape = ps[0]
		}
		return
	case inter == "!" && final == 'p' && !hasPrefix:
		v.softReset()
		return
	case inter != "":
		return
	}

	// Private sequences (ESC [ ? …) are modes: alternate screen, bracketed paste, cursor
	// visibility, mouse reporting. The alternate-screen switch decides what is ON the screen (only
	// when this model is tracking it — see vt.trackAlt); the rest are recorded for a restore —
	// see vt.modes.
	if len(body) > 0 && body[0] == '?' {
		if final == 'h' || final == 'l' {
			for _, p := range v.parseParams(body[1:]) {
				switch p {
				case 1048:
					// Save / restore the cursor, on whichever screen is displayed.
					if final == 'h' {
						v.saveCursor()
					} else {
						v.restoreCursor()
					}
				case 2:
					// DECANM: out of VT52 mode, back to VT100 — which xterm.js does by dropping all four
					// designated sets back to US ASCII (setting it; resetting it does nothing here).
					if final == 'h' {
						v.charset.vt100Mode()
					}
					v.noteMode(p, final == 'h')
				case 1049, 1047, 47:
					if !v.trackAlt {
						continue
					}
					// 1049 is 1047 plus a DECSC before entering and a DECRC after leaving. xterm does
					// both unconditionally — a 1049h that finds the alternate screen already up saves
					// into ITS slot, and a 1049l that finds the normal one restores from the normal
					// slot — so the slot a save lands in depends only on which screen is displayed.
					if final == 'h' {
						if p == 1049 {
							v.saveCursor()
						}
						v.enterAlt()
					} else {
						v.exitAlt()
						if p == 1049 {
							v.restoreCursor()
						}
					}
				default:
					v.noteMode(p, final == 'h')
				}
			}
		}
		return
	}
	// Private-parameter prefixes (ESC [ > c, ESC [ = c …) are queries and resets we do not model.
	if hasPrefix {
		return
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
		v.setCursor(p(1, 1)-1, p(0, 1)-1)
	case 'A': // CUU
		v.cursorUp(p(0, 1))
	case 'B': // CUD
		v.cursorDown(p(0, 1))
	case 'C', 'a': // CUF, and HPR which xterm defines as the same thing
		v.moveCursor(p(0, 1), 0)
	case 'D': // CUB
		v.moveCursor(-p(0, 1), 0)
	case 'G', '`': // CHA / HPA — column absolute
		v.setCursor(p(0, 1)-1, v.row)
	case 'd': // VPA — row absolute
		v.setCursor(v.col, p(0, 1)-1)
	case 'e': // VPR — rows relative, the same as CUD but without the margin stop
		v.moveCursor(0, p(0, 1))
	case 'E': // CNL
		v.cursorDown(p(0, 1))
		v.col = 0
	case 'F': // CPL
		v.cursorUp(p(0, 1))
		v.col = 0
	case 'b': // REP — repeat the preceding character
		v.repeatPreceding(p(0, 1))
	case 'h', 'l': // SM / RM
		for _, m := range params {
			switch m {
			case 4:
				v.insertMode = final == 'h'
			case 20:
				v.lnm = final == 'h'
			}
		}
	case 'J': // ED — erase in display
		// A row erased from its first column, or reset entirely, no longer continues the one
		// above it (xterm clears isWrapped exactly there).
		switch p(0, 0) {
		case 0:
			v.clearRegion(v.row, v.col, v.row, v.cols-1)
			if v.col == 0 {
				v.wrap[v.row] = false
			}
			v.resetRows(v.row+1, v.rows-1)
		case 1:
			v.resetRows(0, v.row-1)
			v.clearRegion(v.row, 0, v.row, v.col)
			v.wrap[v.row] = false
			if v.col+1 >= v.cols {
				// "Deleted entire previous line. This next line can no longer be wrapped." — but
				// xterm indexes the buffer with the screen row, not the screen row plus the base, so
				// it is the line y+1 from the TOP OF THE BUFFER that loses its flag. On the screen
				// that is row y+1-ybase; if it is in the scrollback there is nothing to change here.
				if r := v.row + 1 - v.ybase; r >= 0 && r < v.rows {
					v.wrap[r] = false
				}
			}
		case 2:
			v.resetRows(0, v.rows-1)
		case 3:
			// The scrollback, not the screen — of the normal buffer only (the alternate one has
			// none). The historian keeps its record regardless. xterm drops the lines and moves
			// the base back to the top but leaves the saved row ABSOLUTE, so it now sits further
			// down the screen (see vt.savedRow).
			if v.alt {
				// The alternate buffer's "scrollback" is what it holds above the screen (see altAbove).
				v.savedRow += len(v.altAbove)
				v.altAbove, v.altAboveWrap = nil, nil
			} else {
				v.savedRow += v.ybase
				v.ybase = 0
				v.sbFresh = 0
				if v.reflow && v.sb != nil {
					v.sb.clear()
				}
			}
		}
	case 'K': // EL — erase in line
		switch p(0, 0) {
		case 0:
			v.clearRegion(v.row, v.col, v.row, v.cols-1)
			if v.col == 0 {
				v.wrap[v.row] = false
			}
		case 1:
			v.clearRegion(v.row, 0, v.row, v.col)
		default:
			v.clearRegion(v.row, 0, v.row, v.cols-1)
			v.wrap[v.row] = false
		}
	case 'X': // ECH — erase n characters at the cursor
		v.restrictCursor()
		n := p(0, 1)
		// Bounded by the row, not the screen: on the alternate screen a row can be longer than it
		// (see vt.resize), and xterm's replaceCells runs to the end of the row it is given.
		replaceCells(v.cells[v.row], v.col, v.col+n, cell{r: nullRune, style: v.blankStyle()})
	case 'L': // IL — insert n blank lines at the cursor row
		v.insertLines(p(0, 1))
	case 'M': // DL — delete n lines at the cursor row
		v.deleteLines(p(0, 1))
	case '@': // ICH — insert n blanks, shifting the rest of the line right
		v.insertChars(p(0, 1))
	case 'P': // DCH — delete n characters, shifting the rest of the line left
		v.deleteChars(p(0, 1))
	case 'S': // SU — scroll the region up
		v.scrollRegionUp(p(0, 1))
	case 'T': // SD — scroll the region down
		v.scrollDown(p(0, 1), false)
	case 'r': // DECSTBM — set the scrolling region
		bot := 0
		if len(params) > 1 {
			bot = params[1]
		}
		v.setScrollRegion(p(0, 1), bot)
	case 's': // SCP — save cursor
		v.saveCursor()
	case 'u': // RCP — restore cursor
		v.restoreCursor()
	case 'm': // SGR — select graphic rendition
		v.applySGR(params)
	}
}

// setScrollRegion is DECSTBM with 1-based parameters as received (0 = default). As xterm does: a
// bottom of 0 or past the screen means the last row; a region that is not at least two rows is
// ignored outright; a valid one homes the cursor — to the screen's top-left, since this model has
// no origin mode.
func (v *vt) setScrollRegion(top, bot int) {
	if top < 1 {
		top = 1
	}
	if bot == 0 || bot > v.rows {
		bot = v.rows
	}
	if bot <= top {
		return
	}
	v.top, v.bot = top-1, bot-1
	v.row, v.col = 0, 0
}

// insertLines/deleteLines are the region-local line shuffles a full-screen editor uses. Neither
// feeds history: an inserted line pushes content DOWN (nothing leaves the top) and a deleted line
// is destroyed in place, which is what a real terminal does too.
func (v *vt) insertLines(n int) {
	v.restrictCursor() // a pending wrap is cancelled even outside the region (xterm)
	if v.row < v.top || v.row > v.bot {
		return
	}
	if n > v.bot-v.row+1 {
		n = v.bot - v.row + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.bot]
		copy(v.cells[v.row+1:v.bot+1], v.cells[v.row:v.bot])
		copy(v.wrap[v.row+1:v.bot+1], v.wrap[v.row:v.bot])
		v.cells[v.row] = v.blankedRow(evicted)
		v.wrap[v.row] = false
	}
	v.col = 0
}

func (v *vt) deleteLines(n int) {
	v.restrictCursor() // a pending wrap is cancelled even outside the region (xterm)
	if v.row < v.top || v.row > v.bot {
		return
	}
	if n > v.bot-v.row+1 {
		n = v.bot - v.row + 1
	}
	for k := 0; k < n; k++ {
		evicted := v.cells[v.row]
		copy(v.cells[v.row:v.bot], v.cells[v.row+1:v.bot+1])
		copy(v.wrap[v.row:v.bot], v.wrap[v.row+1:v.bot+1])
		v.cells[v.bot] = v.blankedRow(evicted)
		v.wrap[v.bot] = false
	}
	v.col = 0
}

func (v *vt) insertChars(n int) {
	v.restrictCursor()
	insertCells(v.cells[v.row], v.col, n, cell{r: nullRune, style: v.blankStyle()})
}

// insertCells is BufferLine.insertCells: shift row[pos:] right by n, dropping what falls off the end,
// and put fill in the gap. A wide character cut in half at either edge is emptied too — the one
// whose second half sits at pos, and one pushed onto the last column.
func insertCells(row []cell, pos, n int, fill cell) {
	cols := len(row)
	pos %= cols
	if pos > 0 && cellWidth(row[pos-1]) == 2 {
		row[pos-1] = cell{r: nullRune, style: fill.style} // pos was the second half of a wide char
	}
	if n < cols-pos {
		copy(row[pos+n:], row[pos:cols-n])
		fillRow(row[pos:pos+n], fill)
	} else {
		fillRow(row[pos:], fill)
	}
	if cellWidth(row[cols-1]) == 2 {
		row[cols-1] = cell{r: nullRune, style: fill.style} // a wide char pushed half off the edge
	}
}

func (v *vt) deleteChars(n int) {
	v.restrictCursor()
	deleteCells(v.cells[v.row], v.col, n, cell{r: nullRune, style: v.blankStyle()})
}

// deleteCells is BufferLine.deleteCells: shift row[pos+n:] left to pos and put fill at the end,
// then repair a wide character cut in half where the row was joined — the one whose second half
// is at pos, and a stray second half that has moved into pos.
func deleteCells(row []cell, pos, n int, fill cell) {
	cols := len(row)
	pos %= cols
	if n < cols-pos {
		copy(row[pos:], row[pos+n:])
		fillRow(row[cols-n:], fill)
	} else {
		fillRow(row[pos:], fill)
	}
	if pos > 0 && cellWidth(row[pos-1]) == 2 {
		row[pos-1] = cell{r: nullRune, style: fill.style}
	}
	if cellWidth(row[pos]) == 0 && row[pos].r <= 0 {
		row[pos] = cell{r: nullRune, style: fill.style}
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
		wrap:  append([]bool(nil), v.wrap...),
		row:   v.row, col: v.col, style: v.cur,
		top: v.top, bot: v.bot,
	}
	for i, row := range v.cells {
		saved.cells[i] = append([]cell(nil), row...)
	}
	v.altSaved = saved
	v.alt = true
	v.altAbove, v.altAboveWrap = nil, nil
	v.swapSlots()
	v.top, v.bot = 0, v.rows-1
	// The cursor stays where it was: the alternate screen starts blank, not homed.
	for i := range v.cells {
		v.fillBlank(v.cells[i])
		v.wrap[i] = false
	}
}

// swapSlots exchanges the displayed screen's DECSC slot with the other screen's.
func (v *vt) swapSlots() {
	cur := cursorSlot{row: v.savedRow, col: v.savedCol, style: v.savedStyle, charset: v.savedCharset}
	v.savedRow, v.savedCol, v.savedStyle, v.savedCharset = v.otherSlot.row, v.otherSlot.col, v.otherSlot.style, v.otherSlot.charset
	v.otherSlot = cur
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
			v.cells[i] = fitRow(s.cells[i], v.cols, cell{r: nullRune, style: DefaultStyleID})
			v.wrap[i] = i < len(s.wrap) && s.wrap[i]
		} else {
			v.cells[i] = v.blankRow()
			v.wrap[i] = false
		}
	}
	// The cursor does NOT come back with the screen: xterm copies the alternate buffer's cursor onto
	// the normal one (47 and 1047 leave it where the program put it; 1049 then restores the saved
	// one, see the mode handler). The scrolling region is the normal screen's own, and the pen the
	// terminal's.
	v.top, v.bot = v.clampRow(s.top), v.clampRow(s.bot)
	if v.bot <= v.top {
		v.top, v.bot = 0, v.rows-1
	}
	v.altSaved = nil
	v.alt = false
	v.swapSlots()
	// The alternate buffer is cleared on the way out, which also resets its capacity.
	v.altAbove, v.altAboveWrap, v.altMaxLen = nil, nil, v.rows
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
			// `4:0` is "no underline" and `4:1` .. `4:5` pick a style the model draws as an underline.
			if sub := v.subParams(i); len(sub) > 0 && sub[0] == 0 {
				s.Attrs &^= AttrUnderline
			} else {
				s.Attrs |= AttrUnderline
			}
		case n == 5: // 6 (rapid blink) is not one of xterm's: it does nothing there
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
		case n == 38 || n == 48 || n == 58:
			// The only parameters that take what follows them (the next parameters, or their own
			// sub-parameters); 58 sets the underline colour, which the model does not keep.
			mode, c1, c2, c3, advance := v.extractColor(i)
			i += advance
			var c Color
			switch mode {
			case 2:
				c = RGBColor(uint8(c1), uint8(c2), uint8(c3))
			case 5:
				c = IndexedColor(uint8(c1))
			default:
				continue
			}
			switch n {
			case 38:
				s.FG = c
			case 48:
				s.BG = c
			}
		case n == 39:
			s.FG = ColorDefault
		case n >= 40 && n <= 47:
			s.BG = IndexedColor(uint8(n - 40))
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

// subParams returns the colon sub-parameters of params[i] (`4:3` has the sub-parameter 3 on the 4); an
// empty one is -1.
func (v *vt) subParams(i int) []int {
	if i < 0 || i >= len(v.subAt) {
		return nil
	}
	return v.subs[v.subAt[i][0]:v.subAt[i][1]]
}

// extractColor reads what follows a 38, 48 or 58 at params[pos] — xterm's _extractColor, line for
// line. The colour is spelled with semicolons (`38;5;n`, `38;2;r;g;b`) or with the colon
// sub-parameters of the 38 itself (`38:5:n`, `38:2::r:g:b` with the colour space slot between, which
// `38:2:r:g:b` lacks and xterm then reads one place early), and the two are read into one normalised
// slot list so that both come out the same. advance is how many MORE parameters it took, and
// nothing else in an SGR sequence takes any: a sub-parameter belongs to the parameter it follows.
func (v *vt) extractColor(pos int) (mode, c1, c2, c3, advance int) {
	params := v.params
	accu := [6]int{0, 0, -1, 0, 0, 0} // target, mode, colour space, three values
	cSpace := 0
	for {
		accu[advance+cSpace] = params[pos+advance]
		if sub := v.subParams(pos + advance); len(sub) > 0 {
			for i := 0; ; {
				if accu[1] == 5 {
					cSpace = 1
				}
				if at := advance + i + 1 + cSpace; at < len(accu) {
					accu[at] = sub[i]
				}
				i++
				if !(i < len(sub) && i+advance+1+cSpace < len(accu)) {
					break
				}
			}
			break
		}
		// Exit early when the semicolons have said enough.
		if (accu[1] == 5 && advance+cSpace >= 2) || (accu[1] == 2 && advance+cSpace >= 5) {
			break
		}
		if accu[1] != 0 {
			cSpace = 1
		}
		advance++
		if !(advance+pos < len(params) && advance+cSpace < len(accu)) {
			break
		}
	}
	for i := 2; i < len(accu); i++ {
		if accu[i] == -1 {
			accu[i] = 0
		}
	}
	return accu[1], accu[3], accu[4], accu[5], advance
}

// parseParams splits a CSI parameter body into numbers, into the model's reusable scratch slice.
//
// `;` separates parameters. `:` separates SUB-parameters, which belong to the parameter they follow:
// `1:31` is the parameter 1 carrying the sub-parameter 31 — not the two parameters 1 and 31 — and only
// the few functions that ask for them read them (SGR 4, 38, 48 and 58; see subParams). Returned are
// the parameters alone; their sub-parameters are in v.subs. A parameter that is not a plain number
// reads as 0, which is what a terminal does with `ESC [ ;5 m` and with garbage alike; an empty
// sub-parameter reads as -1.
func (v *vt) parseParams(body []byte) []int {
	v.params, v.subs, v.subAt = v.params[:0], v.subs[:0], v.subAt[:0]
	if len(body) == 0 {
		return v.params
	}
	n, ok := 0, true // the parameter being read
	inSub := false
	sn, sok, sempty := 0, true, true // the sub-parameter being read
	lo := 0                          // where this parameter's sub-parameters begin in v.subs
	endSub := func() {
		if !sok {
			sn = 0
		}
		if sempty {
			sn = -1
		}
		v.subs = append(v.subs, sn)
	}
	endParam := func() {
		if inSub {
			endSub()
			inSub = false
		}
		if !ok {
			n = 0
		}
		v.params = append(v.params, n)
		v.subAt = append(v.subAt, [2]int{lo, len(v.subs)})
		lo = len(v.subs)
		n, ok = 0, true
	}
	for _, ch := range body {
		switch {
		case ch == ';':
			endParam()
		case ch == ':':
			if inSub {
				endSub()
			}
			inSub = true
			sn, sok, sempty = 0, true, true
		case ch >= '0' && ch <= '9':
			if inSub {
				sn, sempty = sn*10+int(ch-'0'), false
				if sn > 1<<20 {
					sn = 1 << 20
				}
			} else {
				n = n*10 + int(ch-'0')
				if n > 1<<20 {
					n = 1 << 20 // a parameter this large is nonsense; clamp rather than overflow
				}
			}
		default:
			if inSub {
				sok, sempty = false, false
			} else {
				ok = false
			}
		}
	}
	endParam()
	return v.params
}

// textLines reads the grid back as plain text: trailing spaces trimmed per row, trailing blank
// rows dropped. This is the overview's view — styles exist in the grid but are not its business.
func (v *vt) textLines() []string {
	out := make([]string, 0, v.rows)
	for _, row := range v.cells {
		out = append(out, strings.TrimRight(rowTextWith(row, v.combos), " "))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func rowText(row []cell) string { return rowTextWith(row, nil) }

// rowTextWith is rowText with the marks stacked on its cells, which live in the model's table.
func rowTextWith(row []cell, combos *comboTable) string {
	var b strings.Builder
	b.Grow(len(row))
	// As xterm's translateToString reads a row: each cell is followed by the cell its width says, so
	// the one after a wide character is skipped whatever it holds, and an empty cell is a space. A
	// cell is its character and the marks stacked on it.
	for i := 0; i < len(row); {
		c := row[i]
		if c.r <= 0 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(c.r)
			b.WriteString(combos.get(c.ext))
		}
		i += max(cellWidth(c), 1)
	}
	return b.String()
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
