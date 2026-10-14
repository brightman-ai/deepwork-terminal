package muxd

// vt_reflow.go — resizing the way xterm.js does, so that a model and a browser terminal that saw
// the same bytes and the same resizes still show the same thing.
//
// A resize in xterm is not "crop or pad the grid". Narrower, a soft-wrapped line is re-wrapped and
// the rows it gains push the top of the screen into the scrollback; wider, wrapped lines are joined
// back and rows are pulled DOWN out of the scrollback; fewer rows push from the top when the cursor
// is at the bottom, more rows pull history back into view. Cropping instead loses whatever was to
// the right of the new margin — permanently: a phone visit at 60 columns would cut every line of a
// 189-column session in the model, while the browser that was watching shows them intact.
//
// The functions below are a port of xterm.js 6.0.0 — Buffer.resize / _reflowLarger /
// _reflowSmaller (common/buffer/Buffer.ts) and common/buffer/BufferReflow.ts — kept in the same
// shape and order so the two can be compared line by line, minus what has no bearing on content
// (markers, memory cleanup, Windows-pty modes). Divergence from that file is a bug here.
//
// The model keeps the scrollback in a compact store rather than as rows, so a resize works on a
// temporary buffer: the newest logical lines of the scrollback (at least enough to fill the screen at
// the new size whatever the reflow does, and as many as anything reads back — reflowReachLines)
// followed by the screen. Older lines are not re-wrapped by the resize; they are brought up to the
// current width when they are read (History.layOutScrollback).

// reflowReachLines is how many of the newest logical lines of the scrollback a resize re-wraps,
// when the screen alone would need fewer. xterm re-wraps all of its history at every resize, and the
// layout it ends up with is not always what one re-wrap from a much older width gives (a wide
// character that did not fit leaves a gap cell, and which gaps survive depends on the widths in
// between). Keeping the part of history that anything reads re-wrapped at every width, as xterm
// does, makes that part exact; the rest is brought up to the current width when it is read (see
// History.layOutScrollback), which is exact except for those gap cells. The number is the deepest
// restore the server asks of a model (keyframeLines in session_screen.go); a resize at that depth
// costs on the order of 15 ms.
const reflowReachLines = 5000

// scrollback is what a reflowing resize needs from the store behind the screen.
type scrollback interface {
	// tail returns the rows of the newest n LOGICAL lines, oldest first — so the first row does not
	// continue another and no line is cut in half. (Lines, not rows: n wrapped rows can be a single
	// line, and a resize needs enough lines to fill a screen whatever the reflow joins.)
	tail(n int) []xline
	// dropTail removes the newest n rows.
	dropTail(n int)
	// push appends one row (the same as a row scrolling off the top).
	push(row []cell, wrapped bool)
	// clear empties it (ED 3, RIS).
	clear()
}

// xline is one buffer line: its cells (as many as the columns it was last sized to) and whether it
// continues the line above it.
type xline struct {
	cells   []cell
	wrapped bool
}

var xnull = cell{r: nullRune, style: DefaultStyleID}

func (l *xline) hasContent(i int) bool { return i < len(l.cells) && l.cells[i].r > 0 }
func (l *xline) width(i int) int {
	if i < 0 || i >= len(l.cells) {
		return 1
	}
	return cellWidth(l.cells[i])
}

// trimmedLength is BufferLine.getTrimmedLength: the end of the last cell with content.
func (l *xline) trimmedLength() int {
	for i := len(l.cells) - 1; i >= 0; i-- {
		if l.hasContent(i) {
			return i + l.width(i)
		}
	}
	return 0
}

func (l *xline) resize(cols int) {
	if cols == len(l.cells) {
		return
	}
	if cols < len(l.cells) {
		l.cells = l.cells[:cols:cols]
		return
	}
	for len(l.cells) < cols {
		l.cells = append(l.cells, xnull)
	}
}

// copyCellsFrom is BufferLine.copyCellsFrom (the order only matters when src and dst are one line).
//
// xterm copies between typed arrays, where reading past the end gives zero and writing past it is
// dropped. That happens: a wide character left with its lead in a row's last column (a narrowing
// resize cuts the continuation off) makes the row's trimmed length one more than its width. A zero
// cell is "no content, no width", which is a continuation here.
func (l *xline) copyCellsFrom(src *xline, srcCol, destCol, n int, reverse bool) {
	copyOne := func(c int) {
		if destCol+c < 0 || destCol+c >= len(l.cells) {
			return
		}
		if srcCol+c < 0 || srcCol+c >= len(src.cells) {
			l.cells[destCol+c] = cell{r: 0, style: DefaultStyleID}
			return
		}
		l.cells[destCol+c] = src.cells[srcCol+c]
	}
	if reverse {
		for c := n - 1; c >= 0; c-- {
			copyOne(c)
		}
		return
	}
	for c := 0; c < n; c++ {
		copyOne(c)
	}
}

func blankXLine(cols int, wrapped bool) *xline {
	l := &xline{cells: make([]cell, cols), wrapped: wrapped}
	for i := range l.cells {
		l.cells[i] = xnull
	}
	return l
}

// xbuffer is the slice of xterm's Buffer state a resize reads and writes.
type xbuffer struct {
	lines         []*xline
	maxLength     int
	ybase, ydisp  int
	x, y          int
	savedX        int
	savedY        int // ABSOLUTE (ybase-relative), as xterm keeps it
	cols, rows    int
	hasScrollback bool
}

// get is CircularList.get: an index is not checked against the length, so a line popped from the
// end is still there to be read until something is pushed over it. reflowSmaller relies on it by
// accident — its viewport adjustment pops lines while the loop is still walking the old length — so
// the lines it then reads past the end are reflowed (and given new rows) like any other.
func (b *xbuffer) get(i int) *xline {
	if i < 0 || i >= cap(b.lines) {
		return nil
	}
	return b.lines[:cap(b.lines)][i]
}

func (b *xbuffer) trimStart(n int) {
	if n <= 0 {
		return
	}
	if n > len(b.lines) {
		n = len(b.lines)
	}
	b.lines = b.lines[n:]
}

// push is CircularList.push: at capacity, the oldest line falls off the front.
func (b *xbuffer) push(l *xline) {
	b.lines = append(b.lines, l)
	if len(b.lines) > b.maxLength {
		b.trimStart(len(b.lines) - b.maxLength)
	}
}

// resize is Buffer.resize.
func (b *xbuffer) resize(newCols, newRows int) {
	newMaxLength := newRows
	if b.hasScrollback {
		newMaxLength = b.maxLength - b.rows + newRows // "rows + scrollback", with the scrollback fixed
	}
	if newMaxLength > b.maxLength {
		b.maxLength = newMaxLength
	}

	if len(b.lines) > 0 {
		// Deal with columns increasing (reducing needs to happen after reflow).
		if b.cols < newCols {
			for _, l := range b.lines {
				l.resize(newCols)
			}
		}

		// Resize rows in both directions as needed.
		addToY := 0
		if b.rows < newRows {
			for y := b.rows; y < newRows; y++ {
				if len(b.lines) < newRows+b.ybase {
					if b.ybase > 0 && len(b.lines) <= b.ybase+b.y+addToY+1 {
						// There is room above the buffer and no empty lines below: scroll up.
						b.ybase--
						addToY++
						if b.ydisp > 0 {
							b.ydisp--
						}
					} else {
						b.push(blankXLine(newCols, false))
					}
				}
			}
		} else {
			for y := b.rows; y > newRows; y-- {
				if len(b.lines) > newRows+b.ybase {
					if len(b.lines) > b.ybase+b.y+1 {
						// A blank line below the cursor: remove it.
						b.lines = b.lines[:len(b.lines)-1]
					} else {
						// The line is the cursor: scroll down.
						b.ybase++
						b.ydisp++
					}
				}
			}
		}

		// Reduce max length if needed after adjustments.
		if newMaxLength < b.maxLength {
			amountToTrim := len(b.lines) - newMaxLength
			if amountToTrim > 0 {
				b.trimStart(amountToTrim)
				b.ybase = max(b.ybase-amountToTrim, 0)
				b.ydisp = max(b.ydisp-amountToTrim, 0)
				b.savedY = max(b.savedY-amountToTrim, 0)
			}
			b.maxLength = newMaxLength
		}

		// Make sure that the cursor stays on screen.
		b.x = min(b.x, newCols-1)
		b.y = min(b.y, newRows-1)
		if addToY != 0 {
			b.y += addToY
		}
		b.savedX = min(b.savedX, newCols-1)
	}

	if b.hasScrollback { // _isReflowEnabled
		b.reflow(newCols, newRows)
		// Trim the end of the line off if cols shrunk.
		if b.cols > newCols {
			for _, l := range b.lines {
				l.resize(newCols)
			}
		}
	}
	b.cols, b.rows = newCols, newRows
}

func (b *xbuffer) reflow(newCols, newRows int) {
	if b.cols == newCols {
		return
	}
	if newCols > b.cols {
		b.reflowLarger(newCols, newRows)
	} else {
		b.reflowSmaller(newCols, newRows)
	}
}

func (b *xbuffer) reflowLarger(newCols, newRows int) {
	toRemove := reflowLargerGetLinesToRemove(b.lines, b.cols, newCols, b.ybase+b.y)
	if len(toRemove) > 0 {
		layout, countRemoved := reflowLargerCreateNewLayout(len(b.lines), toRemove)
		next := make([]*xline, len(layout))
		for i, idx := range layout {
			next[i] = b.lines[idx]
		}
		b.lines = next
		b.reflowLargerAdjustViewport(newCols, newRows, countRemoved)
	}
}

func (b *xbuffer) reflowLargerAdjustViewport(newCols, newRows, countRemoved int) {
	for adj := countRemoved; adj > 0; adj-- {
		if b.ybase == 0 {
			if b.y > 0 {
				b.y--
			}
			if len(b.lines) < newRows {
				b.push(blankXLine(newCols, false))
			}
		} else {
			if b.ydisp == b.ybase {
				b.ydisp--
			}
			b.ybase--
		}
	}
	b.savedY = max(b.savedY-countRemoved, 0)
}

func (b *xbuffer) reflowSmaller(newCols, newRows int) {
	type insert struct {
		start    int
		newLines []*xline
	}
	var toInsert []insert
	countToInsert := 0
	// Go backwards as many lines may be trimmed and this will avoid considering them.
	for y := len(b.lines) - 1; y >= 0; y-- {
		nextLine := b.get(y)
		if nextLine == nil || !nextLine.wrapped && nextLine.trimmedLength() <= newCols {
			continue
		}
		// Gather wrapped lines and adjust y to be the starting line.
		wrappedLines := []*xline{nextLine}
		for nextLine.wrapped && y > 0 {
			y--
			nextLine = b.get(y)
			wrappedLines = append([]*xline{nextLine}, wrappedLines...)
		}
		// reflowCursorLine is off (xterm's default): lines holding the cursor are left alone.
		if abs := b.ybase + b.y; abs >= y && abs < y+len(wrappedLines) {
			continue
		}

		lastLineLength := wrappedLines[len(wrappedLines)-1].trimmedLength()
		destLineLengths := reflowSmallerGetNewLineLengths(wrappedLines, b.cols, newCols)
		linesToAdd := len(destLineLengths) - len(wrappedLines)
		var trimmedLines int
		if b.ybase == 0 && b.y != len(b.lines)-1 {
			trimmedLines = max(0, b.y-b.maxLength+linesToAdd)
		} else {
			trimmedLines = max(0, len(b.lines)-b.maxLength+linesToAdd)
		}

		var newLines []*xline
		for i := 0; i < linesToAdd; i++ {
			newLines = append(newLines, blankXLine(newCols, true))
		}
		if len(newLines) > 0 {
			toInsert = append(toInsert, insert{start: y + len(wrappedLines) + countToInsert, newLines: newLines})
			countToInsert += len(newLines)
		}
		wrappedLines = append(wrappedLines, newLines...)

		// Copy buffer data to new locations, backwards to do it in place.
		//
		// A wrapped chain with nothing in it (rows of never-written cells) has no destination rows:
		// xterm's loop then finds `undefined` where a row should be and stops (its "sanity check"),
		// but still counts the chain as shrunk by its length (linesToAdd is negative), which moves
		// the saved row below.
		destLineIndex := len(destLineLengths) - 1
		destCol := 0
		if destLineIndex >= 0 {
			destCol = destLineLengths[destLineIndex]
			if destCol == 0 {
				destLineIndex--
				destCol = destLineLengths[destLineIndex]
			}
		}
		srcLineIndex := len(wrappedLines) - linesToAdd - 1
		srcCol := lastLineLength
		for srcLineIndex >= 0 && destLineIndex >= 0 {
			cellsToCopy := min(srcCol, destCol)
			if destLineIndex < 0 || destLineIndex >= len(wrappedLines) {
				break // the sanity check xterm carries for the same loop
			}
			wrappedLines[destLineIndex].copyCellsFrom(wrappedLines[srcLineIndex], srcCol-cellsToCopy, destCol-cellsToCopy, cellsToCopy, true)
			destCol -= cellsToCopy
			if destCol == 0 {
				destLineIndex--
				if destLineIndex >= 0 {
					destCol = destLineLengths[destLineIndex]
				}
			}
			srcCol -= cellsToCopy
			if srcCol == 0 {
				srcLineIndex--
				srcCol = getWrappedLineTrimmedLength(wrappedLines, max(srcLineIndex, 0), b.cols)
			}
		}

		// Null out the end of the line ends if a wide character wrapped to the following line.
		for i := range wrappedLines {
			if i < len(destLineLengths) && destLineLengths[i] < newCols {
				if destLineLengths[i] < len(wrappedLines[i].cells) {
					wrappedLines[i].cells[destLineLengths[i]] = xnull
				}
			}
		}

		// Adjust viewport as needed.
		for adj := linesToAdd - trimmedLines; adj > 0; adj-- {
			if b.ybase == 0 {
				if b.y < newRows-1 {
					b.y++
					b.lines = b.lines[:len(b.lines)-1]
				} else {
					b.ybase++
					b.ydisp++
				}
			} else if b.ybase < min(b.maxLength, len(b.lines)+countToInsert)-newRows {
				if b.ybase == b.ydisp {
					b.ydisp++
				}
				b.ybase++
			}
		}
		b.savedY = min(b.savedY+linesToAdd, b.ybase+newRows-1)
	}

	// Rearrange lines in the buffer if there are any insertions (one pass, as xterm does).
	if len(toInsert) > 0 {
		original := b.lines
		originalLength := len(original)
		newLength := min(b.maxLength, originalLength+countToInsert)
		next := make([]*xline, newLength)
		originalLineIndex := originalLength - 1
		nextToInsertIndex := 0
		countInsertedSoFar := 0
		for i := min(b.maxLength-1, originalLength+countToInsert-1); i >= 0; i-- {
			if nextToInsertIndex < len(toInsert) && toInsert[nextToInsertIndex].start > originalLineIndex+countInsertedSoFar {
				ins := toInsert[nextToInsertIndex]
				for nextI := len(ins.newLines) - 1; nextI >= 0; nextI-- {
					if i >= 0 {
						next[i] = ins.newLines[nextI]
					}
					i--
				}
				i++
				countInsertedSoFar += len(ins.newLines)
				nextToInsertIndex++
			} else {
				next[i] = original[originalLineIndex]
				originalLineIndex--
			}
		}
		b.lines = next
	}
}

// reflowLargerGetLinesToRemove is BufferReflow.reflowLargerGetLinesToRemove.
func reflowLargerGetLinesToRemove(lines []*xline, oldCols, newCols, bufferAbsoluteY int) []int {
	var toRemove []int
	for y := 0; y < len(lines)-1; y++ {
		i := y
		i++
		nextLine := lines[i]
		if !nextLine.wrapped {
			continue
		}
		wrappedLines := []*xline{lines[y]}
		for i < len(lines) && nextLine.wrapped {
			wrappedLines = append(wrappedLines, nextLine)
			i++
			if i < len(lines) {
				nextLine = lines[i]
			} else {
				break
			}
		}
		// reflowCursorLine is off: lines holding the cursor are left alone.
		if bufferAbsoluteY >= y && bufferAbsoluteY < i {
			y += len(wrappedLines) - 1
			continue
		}

		destLineIndex := 0
		destCol := getWrappedLineTrimmedLength(wrappedLines, destLineIndex, oldCols)
		srcLineIndex := 1
		srcCol := 0
		for srcLineIndex < len(wrappedLines) {
			srcTrimmed := getWrappedLineTrimmedLength(wrappedLines, srcLineIndex, oldCols)
			srcRemaining := srcTrimmed - srcCol
			destRemaining := newCols - destCol
			cellsToCopy := min(srcRemaining, destRemaining)
			wrappedLines[destLineIndex].copyCellsFrom(wrappedLines[srcLineIndex], srcCol, destCol, cellsToCopy, false)
			destCol += cellsToCopy
			if destCol == newCols {
				destLineIndex++
				destCol = 0
			}
			srcCol += cellsToCopy
			if srcCol == srcTrimmed {
				srcLineIndex++
				srcCol = 0
			}
			// Make sure the last cell isn't wide; if it is, copy it to the current dest.
			if destCol == 0 && destLineIndex != 0 {
				if wrappedLines[destLineIndex-1].width(newCols-1) == 2 {
					wrappedLines[destLineIndex].copyCellsFrom(wrappedLines[destLineIndex-1], newCols-1, destCol, 1, false)
					destCol++
					wrappedLines[destLineIndex-1].cells[newCols-1] = xnull
				}
			}
		}
		// Clear out remaining cells or fragments could remain.
		replaceCells(wrappedLines[destLineIndex].cells, destCol, newCols, xnull)

		// Work backwards and remove any rows at the end that only contain null cells.
		countToRemove := 0
		for k := len(wrappedLines) - 1; k > 0; k-- {
			if k > destLineIndex || wrappedLines[k].trimmedLength() == 0 {
				countToRemove++
			} else {
				break
			}
		}
		if countToRemove > 0 {
			toRemove = append(toRemove, y+len(wrappedLines)-countToRemove, countToRemove)
		}
		y += len(wrappedLines) - 1
	}
	return toRemove
}

// reflowLargerCreateNewLayout is BufferReflow.reflowLargerCreateNewLayout.
func reflowLargerCreateNewLayout(n int, toRemove []int) (layout []int, countRemoved int) {
	next := 0
	nextStart := -1
	if len(toRemove) > 0 {
		nextStart = toRemove[0]
	}
	for i := 0; i < n; i++ {
		if nextStart == i {
			next++
			count := toRemove[next]
			i += count - 1
			countRemoved += count
			next++
			if next < len(toRemove) {
				nextStart = toRemove[next]
			} else {
				nextStart = -1
			}
		} else {
			layout = append(layout, i)
		}
	}
	return layout, countRemoved
}

// reflowSmallerGetNewLineLengths is BufferReflow.reflowSmallerGetNewLineLengths.
func reflowSmallerGetNewLineLengths(wrappedLines []*xline, oldCols, newCols int) []int {
	var out []int
	cellsNeeded := 0
	for i := range wrappedLines {
		cellsNeeded += getWrappedLineTrimmedLength(wrappedLines, i, oldCols)
	}
	srcCol, srcLine, cellsAvailable := 0, 0, 0
	for cellsAvailable < cellsNeeded {
		if cellsNeeded-cellsAvailable < newCols {
			out = append(out, cellsNeeded-cellsAvailable)
			break
		}
		srcCol += newCols
		oldTrimmed := getWrappedLineTrimmedLength(wrappedLines, srcLine, oldCols)
		if srcCol > oldTrimmed {
			srcCol -= oldTrimmed
			srcLine++
		}
		endsWithWide := srcLine < len(wrappedLines) && wrappedLines[srcLine].width(srcCol-1) == 2
		if endsWithWide {
			srcCol--
		}
		lineLength := newCols
		if endsWithWide {
			lineLength = newCols - 1
		}
		out = append(out, lineLength)
		cellsAvailable += lineLength
	}
	return out
}

// getWrappedLineTrimmedLength is BufferReflow.getWrappedLineTrimmedLength.
func getWrappedLineTrimmedLength(lines []*xline, i, cols int) int {
	if i == len(lines)-1 {
		return lines[i].trimmedLength()
	}
	endsInNull := !lines[i].hasContent(cols-1) && lines[i].width(cols-1) == 1
	followingStartsWithWide := lines[i+1].width(0) == 2
	if endsInNull && followingStartsWithWide {
		return cols - 1
	}
	return cols
}

// resize changes the grid. With v.reflow it is xterm's resize (above), run on a temporary buffer
// of the scrollback's tail and the screen; without, it keeps the top-left content and clamps —
// the historian's behaviour, whose scrollback must only ever grow at the end.
func (v *vt) resize(rows, cols int) {
	if rows < 1 || cols < 1 {
		return
	}
	rows, cols = min(rows, maxScreenRows), min(cols, maxScreenCols)
	if rows == v.rows && cols == v.cols {
		return
	}
	if !v.reflow {
		v.cropResize(rows, cols)
		return
	}
	oldRows, oldCols := v.rows, v.cols

	// The NORMAL screen reflows against the scrollback, whether it is displayed or parked behind
	// a full-screen program — xterm resizes both buffers.
	normalCells, normalWrap := v.cells, v.wrap
	nx, ny := v.col, v.row
	nsx, nsy := v.savedCol, v.savedRow // the normal screen's DECSC slot, as (x, y - ybase)
	if v.alt && v.altSaved != nil {
		normalCells, normalWrap = v.altSaved.cells, v.altSaved.wrap
		nx, ny = v.altSaved.col, v.altSaved.row
		nsx, nsy = v.otherSlot.col, v.otherSlot.row
	}
	var tail []xline
	if v.sb != nil {
		// Enough history that the new screen is drawn entirely from what is in hand, whatever
		// the reflow joins (one logical line yields at least one row), and that everything a reader
		// asks for stays laid out as xterm has it (see reflowReachLines).
		tail = v.sb.tail(max(rows+oldRows, reflowReachLines))
	}
	nb := &xbuffer{cols: oldCols, rows: oldRows, hasScrollback: true}
	nb.lines = normalizeRows(tail, oldCols, len(normalWrap) > 0 && normalWrap[0], len(normalCells) > 0 && cellWidth(normalCells[0][0]) == 2)
	// Normalising can change the row count (rows stored at another width are laid out again), so the
	// base is what is above the screen now, not how many rows came out of the store.
	nb.ybase, nb.ydisp = len(nb.lines), len(nb.lines)
	for i, row := range normalCells {
		w := i < len(normalWrap) && normalWrap[i]
		nb.lines = append(nb.lines, &xline{cells: append([]cell(nil), row...), wrapped: w})
	}
	nb.maxLength = len(nb.lines) + 2*maxScreenRows // never trims: everything kept is in hand
	nb.x, nb.y = min(nx, oldCols), ny
	nb.savedX = nsx
	// The saved row is absolute in xterm and lives in the whole buffer; here only the scrollback's
	// tail is in hand, so it is measured from the tail's own start. One that sits further up than
	// the tail reaches is held at the tail's top, with the distance remembered and handed back
	// afterwards — the resize moves it by the same lines either way.
	wantY := nb.ybase + nsy
	nb.savedY = max(wantY, 0)
	deficit := nb.savedY - wantY
	nb.resize(cols, rows)
	screen, screenWrap, scrolled := splitScreen(nb, cols, rows, true)
	if v.sb != nil {
		v.sb.dropTail(len(tail))
		for _, l := range scrolled {
			v.sb.push(l.cells, l.wrapped)
		}
		v.sbFresh = len(scrolled)
	}
	nCursorRow, nCursorCol := nb.y, nb.x
	nSavedRow, nSavedCol := nb.savedY-nb.ybase-deficit, nb.savedX
	// The base moved by however many rows the resize pulled back out of the scrollback or pushed
	// into it; the part of the scrollback outside the tail did not move.
	v.ybase = max(v.ybase-len(tail)+nb.ybase, 0)

	if v.alt && v.altSaved != nil {
		v.altSaved.cells, v.altSaved.wrap = screen, screenWrap
		v.altSaved.row, v.altSaved.col = nCursorRow, nCursorCol
		v.altSaved.top, v.altSaved.bot = 0, rows-1
		v.otherSlot.row, v.otherSlot.col = nSavedRow, nSavedCol

		// The alternate screen does not reflow; it still gains and loses rows the way xterm's alt
		// buffer does — including whatever it holds above the screen (see vt.altAbove).
		ab := &xbuffer{cols: oldCols, rows: oldRows, maxLength: v.altMaxLen}
		for i, row := range v.altAbove {
			ab.lines = append(ab.lines, &xline{cells: row, wrapped: v.altAboveWrap[i]})
		}
		ab.ybase, ab.ydisp = len(v.altAbove), len(v.altAbove)
		for i, row := range v.cells {
			ab.lines = append(ab.lines, &xline{cells: append([]cell(nil), row...), wrapped: v.wrap[i]})
		}
		ab.x, ab.y = min(v.col, oldCols), v.row
		ab.savedX, ab.savedY = v.savedCol, max(ab.ybase+v.savedRow, 0)
		ab.resize(cols, rows)
		var above []*xline
		v.cells, v.wrap, above = splitScreen(ab, cols, rows, false)
		v.altAbove, v.altAboveWrap = nil, nil
		for _, l := range above {
			v.altAbove = append(v.altAbove, l.cells)
			v.altAboveWrap = append(v.altAboveWrap, l.wrapped)
		}
		v.altMaxLen = ab.maxLength
		v.row, v.col = ab.y, ab.x
		v.savedRow, v.savedCol = ab.savedY-ab.ybase, ab.savedX
	} else {
		v.cells, v.wrap = screen, screenWrap
		v.row, v.col = nCursorRow, nCursorCol
		v.savedRow, v.savedCol = nSavedRow, nSavedCol
	}
	if !v.alt {
		// The alternate buffer, empty while the normal screen is displayed, still takes the new size:
		// its capacity grows to fit but never shrinks (only leaving the alternate screen resets it).
		v.altMaxLen = max(v.altMaxLen, rows)
	}
	v.rows, v.cols = rows, cols
	v.top, v.bot = 0, rows-1
}

// normalizeRows lays stored scrollback rows out exactly as xterm holds them at `cols`.
//
// xterm re-wraps its whole scrollback at every resize, so every row it holds is `cols` wide and
// wrapped where `cols` puts the wrap. The store keeps rows as they were when they last were laid
// out — at whatever width that was — and only the newest rows are re-laid at each resize. So before
// the resize runs, each logical line in hand (a row, then the rows that continue it) that is not
// already `cols` wide is joined and wrapped again at `cols`. xterm's reflow of a logical line is
// that re-wrap, so the rows that come out are the rows xterm has. A line whose rows are all `cols`
// wide is left exactly as it is: xterm does not touch what is already the right width, and what it
// holds there is not always what a fresh wrap would make (a wide character cut in half by a
// narrowing resize stays cut).
//
// continuedBelow says the row after the last one (the screen's first) continues it, which makes the
// last one a full-width row like any other continued row; belowStartsWide says that row begins with
// a double-width character (it matters at the edge of the row above, see below).
func normalizeRows(rows []xline, cols int, continuedBelow, belowStartsWide bool) []*xline {
	var out []*xline
	for s := 0; s < len(rows); {
		e := s + 1
		for e < len(rows) && rows[e].wrapped {
			e++
		}
		line := rows[s:e]
		uniform := true
		for _, r := range line {
			if len(r.cells) != cols {
				uniform = false
				break
			}
		}
		if uniform {
			for i := range line {
				r := line[i]
				out = append(out, &r)
			}
		} else {
			below := e == len(rows) && continuedBelow
			out = append(out, relayLine(line, cols, below, belowStartsWide && below, s == 0 && line[0].wrapped)...)
		}
		s = e
	}
	return out
}

// relayLine joins one logical line's rows and wraps them again at cols (see normalizeRows).
// firstWrapped keeps the continuation flag of a line whose head is gone.
func relayLine(line []xline, cols int, continuedBelow, belowStartsWide, firstWrapped bool) []*xline {
	// logical holds the line's CELLS, continuation halves and strays included: xterm copies cells,
	// never characters, so a wide character whose second half something overwrote stays exactly that
	// wherever the re-wrap moves it.
	var logical []cell
	for i, r := range line {
		cells := r.cells
		if (i+1 < len(line)) || continuedBelow {
			// A row that is continued counts to its last cell, nulls included (xterm takes such a row at
			// full width); only a line's last row is trimmed. (The store hands such a row back at the
			// width it had.) The exception is the one xterm makes in getWrappedLineTrimmedLength: a row
			// that ends in a never-written cell because the wide character that followed did not fit
			// is one cell short — that cell is where printing left a gap, not part of the line.
			if n := len(cells); n > 0 && cells[n-1].r <= 0 && cellWidth(cells[n-1]) == 1 {
				startsWide := belowStartsWide
				if i+1 < len(line) {
					startsWide = line[i+1].width(0) == 2
				}
				if startsWide {
					cells = cells[:n-1]
				}
			}
		} else {
			cells = cells[:min((&xline{cells: cells}).trimmedLength(), len(cells))]
		}
		logical = append(logical, cells...)
	}
	var out []*xline
	cur := blankXLine(cols, false)
	for i := 0; ; {
		remaining := len(logical) - i
		// reflowSmallerGetNewLineLengths: a row takes `cols` cells, one fewer when the last of them
		// would be the first half of a wide character — that goes to the next row, leaving a gap.
		n := remaining
		if remaining >= cols {
			n = cols
			if cellWidth(logical[i+cols-1]) == 2 {
				n = cols - 1
			}
		}
		copy(cur.cells, logical[i:i+n])
		i += n
		if i >= len(logical) {
			break
		}
		out = append(out, cur)
		cur = blankXLine(cols, true)
	}
	out = append(out, cur)
	out[0].wrapped = firstWrapped
	return out
}

// splitScreen cuts a resized buffer into the screen (the `rows` lines from ybase, each exactly
// `cols` wide) and what lies above it (the scrollback, oldest first).
func splitScreen(b *xbuffer, cols, rows int, exactWidth bool) (screen [][]cell, wrap []bool, above []*xline) {
	for len(b.lines) < b.ybase+rows {
		b.lines = append(b.lines, blankXLine(cols, false))
	}
	above = b.lines[:b.ybase]
	screen = make([][]cell, rows)
	wrap = make([]bool, rows)
	for i := 0; i < rows; i++ {
		l := b.lines[b.ybase+i]
		if exactWidth {
			l.resize(cols)
		}
		screen[i] = l.cells
		wrap[i] = l.wrapped
	}
	return screen, wrap, above
}

// cropResize is the historian's resize: keep the top-left content, clamp the cursor.
func (v *vt) cropResize(rows, cols int) {
	next := make([][]cell, rows)
	nextWrap := make([]bool, rows)
	for i := range next {
		if i < len(v.cells) {
			next[i] = fitRow(v.cells[i], cols, xnull)
			nextWrap[i] = v.wrap[i]
		} else {
			next[i] = make([]cell, cols)
			for j := range next[i] {
				next[i][j] = xnull
			}
		}
	}
	v.cells, v.wrap = next, nextWrap
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
