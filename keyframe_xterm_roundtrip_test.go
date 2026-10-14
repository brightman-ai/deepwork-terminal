package terminal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// The restore's contract end to end: a browser that receives the restore must hold exactly what a
// browser that watched the whole stream holds. muxd's parity test proves the MODEL ends where
// xterm ends; this proves the BYTES serializeKeyframe writes put a fresh xterm there too — the
// screen rows and which of them continue the row above, the cursor, which screen is up, the parked
// screen, the scrollback.
//
// It needs node and @xterm/headless, so it runs only when XTERM_DIAG_DIR points at a directory
// holding restore_dump.mjs and its node_modules (scripts/diag/xtermvectors after `npm ci`).
//
// What a byte stream drawn at the CURRENT width cannot hold is not demanded of it (see
// representable): xterm keeps cells that no output sequence can create, because a resize made
// them. Those rows are compared against what the model holds of them within the grid, and counted
// and logged by reason, so a restore that merely drops more than it must still fails.

type roundTripInput struct {
	Name  string `json:"name"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	B64   string `json:"b64"`
	Start [2]int `json:"start"`
	Ops   []rtOp `json:"ops"`
}

type rtOp struct {
	W      string `json:"w,omitempty"`
	Resize []int  `json:"resize,omitempty"`
}

type roundTripDump struct {
	Alt          bool     `json:"alt"`
	Cursor       [2]int   `json:"cursor"`
	Screen       []rtLine `json:"screen"`
	NormalScreen []rtLine `json:"normalScreen"`
	Scrollback   []rtLine `json:"scrollback"`
}

type rtLine struct {
	T string `json:"t"`
	W bool   `json:"w"`
}

type rtVector struct {
	Name   string        `json:"name"`
	Cols   int           `json:"cols"`
	Rows   int           `json:"rows"`
	Ops    []rtOp        `json:"ops"`
	Expect roundTripDump `json:"expect"`
}

func firstRTDiff(what string, want, got []rtLine) string {
	if len(want) != len(got) {
		return fmt.Sprintf("%s: %d rows, want %d", what, len(got), len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			return fmt.Sprintf("%s row %d: got %q wrapped=%v, want %q wrapped=%v", what, i, got[i].T, got[i].W, want[i].T, want[i].W)
		}
	}
	return ""
}

func TestKeyframeRestoresWhatXtermWatchedThroughTheWholeStream(t *testing.T) {
	dir := os.Getenv("XTERM_DIAG_DIR")
	if dir == "" {
		t.Skip("XTERM_DIAG_DIR not set (needs node + @xterm/headless)")
	}
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

	inputs := make([]roundTripInput, 0, len(vectors))
	states := make([]muxd.TermState, len(vectors))
	scrolls := make([][]muxd.Line, len(vectors))
	for i, v := range vectors {
		h := muxd.NewTerminalModel(muxd.Grid{Cols: v.Cols, Rows: v.Rows}, 100_000)
		cols, rows := v.Cols, v.Rows
		for _, op := range v.Ops {
			if len(op.Resize) == 2 {
				cols, rows = op.Resize[0], op.Resize[1]
				h.Resize(muxd.Grid{Cols: cols, Rows: rows})
				continue
			}
			b, _ := base64.StdEncoding.DecodeString(op.W)
			h.Write(b)
		}
		st, scroll, ok := h.State(100_000)
		if !ok {
			t.Fatalf("%s: model broke", v.Name)
		}
		states[i], scrolls[i] = st, scroll
		restore := restorePrelude + string(serializeKeyframe(st, scroll))
		inputs = append(inputs, roundTripInput{Name: v.Name, Cols: cols, Rows: rows, Start: [2]int{v.Cols, v.Rows}, Ops: v.Ops,
			B64: base64.StdEncoding.EncodeToString([]byte(restore))})
	}

	tmp := t.TempDir()
	in, out := filepath.Join(tmp, "in.json"), filepath.Join(tmp, "out.json")
	raw, _ := json.Marshal(inputs)
	if err := os.WriteFile(in, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(dir, "restore_dump.mjs"), in, out)
	cmd.Dir = dir
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restore_dump.mjs: %v\n%s", err, msg)
	}
	raw, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Vectors []struct {
			Name     string        `json:"name"`
			Expect   roundTripDump `json:"expect"`
			CellDiff string        `json:"cellDiff"`
		} `json:"vectors"`
		Skips map[string]int `json:"skips"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	dumps := result.Vectors

	failed := 0
	var failing []string
	reasons := map[string]int{}
	for i, v := range vectors {
		got := dumps[i].Expect
		want := representable(v.Expect, states[i], scrolls[i], reasons)
		var diffs []string
		if got.Alt != want.Alt {
			diffs = append(diffs, fmt.Sprintf("alt=%v want %v", got.Alt, want.Alt))
		}
		if got.Cursor != want.Cursor {
			diffs = append(diffs, fmt.Sprintf("cursor %v want %v", got.Cursor, want.Cursor))
		}
		if d := firstRTDiff("screen", want.Screen, got.Screen); d != "" {
			diffs = append(diffs, d)
		}
		if want.Alt {
			if d := firstRTDiff("parked normal screen", want.NormalScreen, got.NormalScreen); d != "" {
				diffs = append(diffs, d)
			}
		}
		if d := firstRTDiff("scrollback", want.Scrollback, got.Scrollback); d != "" {
			diffs = append(diffs, d)
		}
		if d := dumps[i].CellDiff; d != "" {
			diffs = append(diffs, "cells: "+d)
		}
		if len(diffs) > 0 {
			failed++
			failing = append(failing, v.Name)
			if failed <= 8 {
				t.Errorf("%s: %s", v.Name, strings.Join(diffs, "; "))
			}
		}
	}
	for _, r := range sortedKeys(result.Skips) {
		t.Logf("cells not compared: %d x %s", result.Skips[r], r)
	}
	for _, r := range sortedKeys(reasons) {
		t.Logf("not representable at the current width, compared within the grid: %d x %s", reasons[r], r)
	}
	if failed > 0 {
		t.Logf("differing: %s", strings.Join(failing, " "))
		t.Errorf("%d of %d restores differ from what xterm watched", failed, len(vectors))
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// representable is what xterm holds, minus what no byte stream can put into a terminal of the
// current width. Three things, each of which xterm only gets from a RESIZE (never from output):
//
//   - an alternate-screen row wider than the grid (a narrowing resize leaves the overhang in the
//     row; it is out of sight, but it is in the text);
//   - a double-width character whose second half the resize cut off, left as a first half in the
//     last column (printing one there wraps instead);
//   - a cursor still waiting to wrap after a row whose last cell has since been erased (every
//     operation that could erase that cell alone also cancels the wait).
//
// Each is found from the model's state, which muxd's parity test holds equal to xterm's.
func representable(want roundTripDump, st muxd.TermState, scroll []muxd.Line, reasons map[string]int) roundTripDump {
	out := want
	out.Screen = adjustRows(want.Screen, st.Lines, st.Cols, reasons)
	if st.Alt && st.Parked != nil {
		out.NormalScreen = adjustRows(want.NormalScreen, st.Parked.Lines, st.Cols, reasons)
	}
	out.Scrollback = adjustRows(want.Scrollback, scroll, st.Cols, reasons)
	if st.CursorCol == st.Cols && st.CursorRow < len(st.Lines) {
		if ln := st.Lines[st.CursorRow]; !writtenAt(ln, st.Cols-1) {
			out.Cursor[0]--
			reasons["cursor waiting to wrap after an erased last cell (cursor one column short)"]++
		}
	}
	return out
}

// writtenAt says whether the cell at col holds something that was written.
func writtenAt(ln muxd.Line, col int) bool {
	if col >= ln.Content {
		return false
	}
	for i := 0; i+1 < len(ln.Nulls); i += 2 {
		if int(ln.Nulls[i]) <= col && col < int(ln.Nulls[i+1]) {
			return false
		}
	}
	return true
}

func adjustRows(want []rtLine, lines []muxd.Line, cols int, reasons map[string]int) []rtLine {
	out := append([]rtLine(nil), want...)
	for i := range out {
		if i >= len(lines) || lines[i].Content <= cols {
			if i < len(lines) && len(lines[i].Hidden) > 0 {
				reasons["cell left where a wide character's second half was (invisible; compared as text only)"]++
			}
			continue
		}
		ln := lines[i]
		if ln.Cols > cols {
			reasons["alternate-screen row wider than the grid (overhang beyond the last column)"]++
		} else {
			reasons["double-width character cut in half by a narrowing resize (first half in the last column)"]++
		}
		out[i].T = visibleText(ln, cols)
	}
	return out
}

// visibleText is translateToString(true) of the part of a row that lies within cols columns: the
// stored text up to the last written cell that is wholly inside.
func visibleText(ln muxd.Line, cols int) string {
	type cell struct {
		r       rune
		written bool
	}
	var cells []cell
	col := 0
	overflow := false
	for _, r := range ln.Text {
		w := muxd.RuneWidth(r)
		if w == 0 {
			continue
		}
		if col+w > cols {
			overflow = true
			break
		}
		cells = append(cells, cell{r, writtenAt(ln, col)})
		col += w
		if w == 2 {
			cells = append(cells, cell{0, writtenAt(ln, col-1)})
		}
	}
	for ; !overflow && col < min(ln.Content, cols); col++ { // written blanks the text dropped
		cells = append(cells, cell{' ', writtenAt(ln, col)})
	}
	end := 0
	for i, c := range cells {
		if c.written {
			end = i + 1
		}
	}
	var b strings.Builder
	for _, c := range cells[:end] {
		switch {
		case c.r == 0:
		case c.written:
			b.WriteRune(c.r)
		default:
			b.WriteByte(' ')
		}
	}
	return b.String()
}
