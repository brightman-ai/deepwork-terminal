package terminal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// A restore replayed into the screen model must leave the model where it was: every cell of every
// row — written or never written, in which style, continuing the row above or not — and the cursor,
// pen, region, modes and parked screen. keyframe_xterm_roundtrip_test.go proves the same against
// xterm itself but needs node; this runs everywhere, on the vectors muxd's parity test holds equal
// to xterm, so a restore that goes wrong in a way only a later resize would show is caught here.
//
// A row the current width cannot hold (see representable in the xterm test) is compared by what
// lies inside the grid and counted by reason, not skipped silently.

func TestKeyframe_RoundTripsEveryCellThroughTheModel(t *testing.T) {
	file := filepath.Join("muxd", "testdata", "xterm_reflow_vectors.json")
	if f := os.Getenv("XTERM_VECTORS"); f != "" {
		file = f
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vectors []rtVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}

	failed := 0
	reasons := map[string]int{}
	for _, v := range vectors {
		a := muxd.NewTerminalModel(muxd.Grid{Cols: v.Cols, Rows: v.Rows}, 100_000)
		cols, rows := v.Cols, v.Rows
		for _, op := range v.Ops {
			if len(op.Resize) == 2 {
				cols, rows = op.Resize[0], op.Resize[1]
				a.Resize(muxd.Grid{Cols: cols, Rows: rows})
				continue
			}
			b, _ := base64.StdEncoding.DecodeString(op.W)
			a.Write(b)
		}
		stA, scrollA, ok := a.State(100_000)
		if !ok {
			t.Fatalf("%s: model broke", v.Name)
		}
		b := muxd.NewTerminalModel(muxd.Grid{Cols: cols, Rows: rows}, 100_000)
		b.Write([]byte(restorePrelude + string(serializeKeyframe(stA, scrollA))))
		stB, scrollB, ok := b.State(100_000)
		if !ok {
			t.Fatalf("%s: model broke on the restore", v.Name)
		}
		if diffs := compareModelStates(stA, scrollA, stB, scrollB, reasons); len(diffs) > 0 {
			failed++
			if failed <= 40 {
				t.Errorf("%s: %s", v.Name, strings.Join(diffs, "; "))
			}
		}
	}
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("not representable at the current width, compared within the grid: %d x %s", reasons[k], k)
	}
	if failed > 0 {
		t.Errorf("%d of %d restores differ from the state they were made from", failed, len(vectors))
	}
}

func compareModelStates(a muxd.TermState, scrollA []muxd.Line, b muxd.TermState, scrollB []muxd.Line, reasons map[string]int) []string {
	var diffs []string
	add := func(format string, args ...any) { diffs = append(diffs, fmt.Sprintf(format, args...)) }

	if a.Alt != b.Alt {
		add("alt=%v want %v", b.Alt, a.Alt)
	}
	wantCol := a.CursorCol
	if a.CursorCol == a.Cols && a.CursorRow < len(a.Lines) {
		switch ln := a.Lines[a.CursorRow]; {
		case !writtenAt(ln, a.Cols-1):
			wantCol-- // a cursor still waiting to wrap after an erased cell: not something output can leave
			reasons["cursor waiting to wrap after an erased last cell (cursor one column short)"]++
		case endsWidened(ln, a.Cols):
			wantCol-- // after a mark's widening at the margin the cursor stands on the cell behind it
			reasons["cursor waiting to wrap after a cell a combining mark widened (cursor one column short)"]++
		}
	}
	if b.CursorRow != a.CursorRow || b.CursorCol != wantCol {
		add("cursor (%d,%d) want (%d,%d)", b.CursorRow, b.CursorCol, a.CursorRow, wantCol)
	}
	if b.ScrollTop != a.ScrollTop || b.ScrollBot != a.ScrollBot {
		add("region %d-%d want %d-%d", b.ScrollTop, b.ScrollBot, a.ScrollTop, a.ScrollBot)
	}
	if b.Pen != a.Pen {
		add("pen %v want %v", b.Pen, a.Pen)
	}
	wantSaved := a.SavedRow
	if wantSaved > a.Rows-1 {
		// ESC 7 followed by a clear of the scrollback leaves the saved row below the screen; a
		// restore can only save onto the screen. DECRC clamps to the same row either way.
		wantSaved = a.Rows - 1
		reasons["saved cursor row below the screen (the scrollback was cleared after saving), clamped to the last row"]++
	}
	wantSavedCol := a.SavedCol
	if wantSavedCol > a.Cols {
		wantSavedCol = a.Cols // a narrowing resize leaves the saved column past the margin; DECRC clamps it either way
		reasons["saved cursor column beyond the grid (a narrowing resize), clamped to the margin"]++
	}
	if wantSavedCol == a.Cols && wantSaved < len(a.Lines) && !writtenAt(a.Lines[wantSaved], a.Cols-1) {
		wantSavedCol--
		reasons["cursor waiting to wrap after an erased last cell (cursor one column short)"]++
	}
	if b.SavedRow != wantSaved || b.SavedCol != wantSavedCol || b.SavedPen != a.SavedPen {
		add("saved cursor (%d,%d,%v) want (%d,%d,%v)", b.SavedRow, b.SavedCol, b.SavedPen, wantSaved, wantSavedCol, a.SavedPen)
	}
	wantCS := charsetOf(a)
	if wantCS.InUse != wantCS.G[wantCS.Level] {
		// A DECRC leaves in use the set that DECSC saved, which no designation need hold. Output gets
		// there only by that DECRC, so: not with a third set in the slot, and not with the cursor
		// waiting to wrap over a character the set would change (the character has to be written again
		// under it).
		p := &packer{cols: a.Cols}
		if wantCS.InUse != wantCS.Saved || (a.CursorCol >= a.Cols && !p.lastCellIsUntouchedBySets(a.Lines, a.CursorRow)) {
			wantCS.InUse = wantCS.G[wantCS.Level]
			reasons["character set in use that no designation holds, and no DECRC can put back: the designation wins"]++
		}
	}
	if got := charsetOf(b); got != wantCS {
		add("character sets %+v want %+v", got, wantCS)
	}
	if b.InsertMode != a.InsertMode || b.NewlineMode != a.NewlineMode {
		add("insert/newline mode %v/%v want %v/%v", b.InsertMode, b.NewlineMode, a.InsertMode, a.NewlineMode)
	}
	if got, want := modeSet(b.Modes), modeSet(a.Modes); got != want {
		add("modes %s want %s", got, want)
	}
	if b.CursorShape != a.CursorShape {
		add("cursor shape %d want %d", b.CursorShape, a.CursorShape)
	}
	if d := compareModelRows("screen", a, a.Lines, b, b.Lines, nil, reasons); d != "" {
		add("%s", d)
	}
	if (a.Parked == nil) != (b.Parked == nil) {
		add("parked screen present=%v want %v", b.Parked != nil, a.Parked != nil)
	} else if a.Parked != nil {
		if d := compareModelRows("parked", a, a.Parked.Lines, b, b.Parked.Lines, nil, reasons); d != "" {
			add("%s", d)
		}
		p, q := a.Parked, b.Parked
		wantCol := p.CursorCol
		if wantCol == a.Cols && p.CursorRow < len(p.Lines) && !writtenAt(p.Lines[p.CursorRow], a.Cols-1) {
			wantCol--
			reasons["cursor waiting to wrap after an erased last cell (cursor one column short)"]++
		}
		if p.Charset != q.Charset {
			add("parked screen's saved character set %q want %q", q.Charset, p.Charset)
		}
		if p.CursorRow != q.CursorRow || wantCol != q.CursorCol || p.ScrollTop != q.ScrollTop || p.ScrollBot != q.ScrollBot || p.Pen != q.Pen {
			add("parked cursor/region/pen (%d,%d %d-%d %v) want (%d,%d %d-%d %v)",
				q.CursorRow, q.CursorCol, q.ScrollTop, q.ScrollBot, q.Pen, p.CursorRow, wantCol, p.ScrollTop, p.ScrollBot, p.Pen)
		}
	}
	normalA := a.Lines
	if a.Parked != nil {
		normalA = a.Parked.Lines
	}
	var firstOfScreen *muxd.Line // the row the last scrollback row may continue
	if len(normalA) > 0 {
		firstOfScreen = &normalA[0]
	}
	if d := compareModelRows("scrollback", a, scrollA, b, scrollB, firstOfScreen, reasons); d != "" {
		add("%s", d)
	}
	return diffs
}

// modeSet spells the private modes a model has been told about, leaving out the two the restore's
// own prelude states (autowrap on, origin mode off: the defaults a model that never heard of them
// is already in).
func charsetOf(st muxd.TermState) muxd.CharsetState {
	if st.Charset == nil {
		return muxd.CharsetState{}
	}
	return *st.Charset
}

func modeSet(ms []muxd.ModeState) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		if (m.Mode == 7 && m.On) || (m.Mode == 6 && !m.On) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d=%v", m.Mode, m.On))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func compareModelRows(what string, a muxd.TermState, rowsA []muxd.Line, b muxd.TermState, rowsB []muxd.Line, after *muxd.Line, reasons map[string]int) string {
	if len(rowsA) != len(rowsB) {
		return fmt.Sprintf("%s: %d rows, want %d", what, len(rowsB), len(rowsA))
	}
	for i := range rowsA {
		want := rowsA[i]
		if want.Cols > a.Cols || want.Content > a.Cols {
			if want.Cols > a.Cols {
				reasons["alternate-screen row wider than the grid (overhang beyond the last column)"]++
			} else {
				reasons["double-width character cut in half by a narrowing resize (first half in the last column)"]++
			}
			continue
		}
		if marked, style, prevSame := lastCellMarkedEmpty(a, want); marked {
			// The last cell can be left empty with a foreground or attribute only by printing the
			// character before it over half of a wide one, in the same style (or by a wide character
			// that did not fit and wrapped, which leaves it in its own); and nothing can wrap from an
			// empty cell, so a wrap onto the next row would have to be made before it.
			next := after
			if i+1 < len(rowsA) {
				next = &rowsA[i+1]
			}
			if next != nil && next.Wrapped {
				p := &packer{styles: a.Styles, cols: a.Cols}
				if kind, first := p.firstCell(*next); kind != cellWide || first != style {
					reasons["row continued below whose last cell is empty yet carries a foreground or attribute (kept: background only)"]++
					continue
				}
			} else if !prevSame {
				reasons["last cell empty yet carries a foreground or attribute, with no character of that style before it (kept: background only)"]++
				continue
			}
		}
		if widenedByMark(want, a.Cols) {
			reasons["cell a combining mark widened to two columns (the widening is not something output can be told to do)"]++
			continue
		}
		if marksBesideOddCells(want, a.Cols) {
			reasons["combining mark in a row that has a never-written or displaced cell (marks on such a cell are not restored)"]++
			continue
		}
		want = withoutMidRowHidden(want, a.Cols, reasons)
		got := withoutWidenedHidden(rowsB[i], b.Cols)
		want = withoutWidenedHidden(want, a.Cols)
		if x, y := canonicalRow(a, want), canonicalRow(b, got); x != y {
			return fmt.Sprintf("%s row %d:\n   got  %s\n   want %s", what, i, y, x)
		}
	}
	return ""
}

// canonicalRow spells out a stored row with its styles resolved, since two models number theirs
// differently.
func canonicalRow(st muxd.TermState, ln muxd.Line) string {
	style := func(id muxd.StyleID) muxd.Style {
		if int(id) < len(st.Styles) {
			return st.Styles[id]
		}
		return muxd.DefaultStyle
	}
	var b strings.Builder
	fmt.Fprintf(&b, "text=%q wrapped=%v content=%d cols=%d nulls=%v", ln.Text, ln.Wrapped, ln.Content, ln.Cols, ln.Nulls)
	for _, sp := range ln.Spans {
		fmt.Fprintf(&b, " span@%d=%v", sp.Start, style(sp.Style))
	}
	for _, h := range ln.Hidden {
		fmt.Fprintf(&b, " hidden@%d=%q/%v", h.Col, h.R, style(h.Style))
	}
	return b.String()
}

// withoutMidRowHidden drops what a resize left beside a wide character away from the last two
// columns. Output can only leave such a cell there (a character typed with autowrap off, a skipped
// wide one), and a resize carries it into the middle of a row, where no output can put one.
func withoutMidRowHidden(ln muxd.Line, cols int, reasons map[string]int) muxd.Line {
	var keep []muxd.HiddenCell
	var gone []uint16
	for _, h := range ln.Hidden {
		if int(h.Col) == cols-1 {
			keep = append(keep, h)
			continue
		}
		reasons["cell left beside a double-width character by a width change (invisible; not in the last two columns)"]++
		gone = append(gone, h.Col)
	}
	if len(gone) == 0 {
		return ln
	}
	ln.Hidden = keep
	return withoutNulls(ln, gone)
}

// withoutNulls takes the given columns out of the row's null runs.
func withoutNulls(ln muxd.Line, gone []uint16) muxd.Line {
	var nulls []uint16
	for i := 0; i+1 < len(ln.Nulls); i += 2 {
		from := ln.Nulls[i]
		for c := ln.Nulls[i]; c < ln.Nulls[i+1]; c++ {
			isGone := false
			for _, g := range gone {
				isGone = isGone || g == c
			}
			if isGone {
				if from < c {
					nulls = append(nulls, from, c)
				}
				from = c + 1
			}
		}
		if from < ln.Nulls[i+1] {
			nulls = append(nulls, from, ln.Nulls[i+1])
		}
	}
	ln.Nulls = nulls
	return ln
}

// withoutWidenedHidden drops the cell kept behind a narrow character that marks widened away from
// the margin, where the widening is not reproduced.
func withoutWidenedHidden(ln muxd.Line, cols int) muxd.Line {
	if len(ln.Hidden) == 0 {
		return ln
	}
	it := newRowReader(ln, cols)
	var gone []uint16
	for {
		col, _, _, _, ok := it.next()
		if !ok {
			break
		}
		if it.owe > 0 {
			gone = append(gone, uint16(col+1))
		}
	}
	if len(gone) == 0 {
		return ln
	}
	var keep []muxd.HiddenCell
	for _, h := range ln.Hidden {
		isGone := false
		for _, g := range gone {
			isGone = isGone || g == h.Col
		}
		if !isGone {
			keep = append(keep, h)
		}
	}
	ln.Hidden = keep
	return withoutNulls(ln, gone)
}

// lastCellMarkedEmpty says the row's last cell is an empty one in a style with more than a
// background (and which), and whether the character before it has that same style.
func lastCellMarkedEmpty(st muxd.TermState, ln muxd.Line) (marked bool, style muxd.Style, prevSame bool) {
	it := newRowReader(ln, st.Cols)
	styleOf := func(id muxd.StyleID) muxd.Style {
		if int(id) < len(st.Styles) {
			return st.Styles[id]
		}
		return muxd.DefaultStyle
	}
	var prev muxd.Style
	prevChar := false
	for {
		col, r, _, id, ok := it.next()
		if !ok {
			return false, muxd.DefaultStyle, false
		}
		if col == st.Cols-1 {
			return r == 0 && isMarked(styleOf(id)), styleOf(id), prevChar && prev == styleOf(id)
		}
		prev, prevChar = styleOf(id), r != 0
	}
}

func isMarked(st muxd.Style) bool { return st.FG != muxd.ColorDefault || st.Attrs != 0 }

// marksBesideOddCells says the row has a zero-width character (a combining mark, a joiner) stacked
// on a cell that was never written, or beside a cell left next to a wide character. xterm gives such
// a cell a character of its own, or a width the text does not show.
func marksBesideOddCells(ln muxd.Line, cols int) bool {
	content := ln.Content
	if content == 0 && ln.Cols == 0 {
		content = cellWidth(ln.Text)
	}
	col := 0
	prevOdd := false
	for _, r := range ln.Text {
		w := muxd.RuneWidth(r)
		if w == 0 {
			if prevOdd {
				return true
			}
			continue
		}
		prevOdd = col >= content || !writtenAt(ln, col)
		for _, h := range ln.Hidden {
			// (a cell behind a mark-widened character in the last two columns is restored; see drawRow)
			prevOdd = prevOdd || (int(h.Col) == col+w && int(h.Col) != cols-1)
		}
		col += w
	}
	return false
}

// widenedByMark says a mark made a one-column cell take two: the model keeps the cell behind it as a
// hidden one (see muxd.Line.Hidden).
func widenedByMark(ln muxd.Line, cols int) bool {
	it := newRowReader(ln, cols)
	for {
		if _, _, _, _, ok := it.next(); !ok {
			return false
		}
		if it.owe > 0 {
			return true
		}
	}
}

// endsWidened says the row's last two columns are a narrow character widened by marks.
func endsWidened(ln muxd.Line, cols int) bool {
	it := newRowReader(ln, cols)
	for {
		if _, _, _, _, ok := it.next(); !ok {
			return false
		}
		if it.widened || it.owe > 0 {
			return true
		}
	}
}
