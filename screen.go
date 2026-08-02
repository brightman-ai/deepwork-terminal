package terminal

import (
	"strings"
	"unicode/utf8"
)

// A minimal terminal SCREEN model.
//
// ── Why this exists ──────────────────────────────────────────────────────────────────────────
// The Agent Overview card must show what a terminal LOOKS LIKE. tmux gets that for free from
// `capture-pane`, which returns an already-rendered screen. A plain (non-tmux) session only has
// its PTY byte stream, and a stream is NOT a screen: a TUI like Claude Code or Codex repaints by
// moving the cursor and overwriting cells. Simply deleting the escape sequences — which is what
// the first cut did — throws away the positioning that says WHERE each fragment belongs, so every
// repaint concatenates and the card renders one long line of interleaved garbage (observed on
// 8087; that is the defect this file fixes).
//
// So we replay the stream onto a bounded character grid and read the grid back. That is the same
// thing a terminal emulator does, restricted to the operations that actually decide what text
// ends up where. Deliberately NOT a conformant emulator and not a new dependency: no scrollback,
// no styling, no charset translation, no mouse/bracketed-paste state. Colors and other SGR
// attributes are parsed only so they can be discarded — the card shows plain text.
//
// Cost is bounded by construction: a fixed rows×cols grid, one pass over the input, no allocation
// per escape sequence. It runs once per second per connected client, over a capped slice of the
// ring buffer.

// maxScreenRows/maxScreenCols bound what a caller may ask for, so a bogus resize can't make the
// replay allocate an absurd grid. Well above any real terminal (the resize paths already reject
// >500), and only a ceiling — the ACTUAL size comes from the session's PTY.
const (
	maxScreenRows = 500
	maxScreenCols = 1000
)

// screen is a character grid the PTY stream is replayed onto, sized to the REAL terminal.
//
// The size is not a tuning knob, it is a correctness requirement. A TUI paints by absolute
// cursor addressing — "put the status line at row 49" — so replaying onto a grid of a different
// height does not produce a smaller version of the screen, it produces a WRONG one: every row
// past the end is clamped onto the last row and overwrites what was already there. That is
// literally what the user saw — a status line, a mode line and a tmux status bar mashed into one
// row, with "Debug" surviving as "ebug" because another clamped row ate its D. Same for columns:
// a line longer than the grid wraps here but not in reality, shifting every row below it.
type screen struct {
	cells [][]rune
	rows  int
	cols  int
	row   int
	col   int
}

func newScreen(rows, cols int) *screen {
	if rows < 1 {
		rows = spawnRows
	}
	if cols < 1 {
		cols = spawnCols
	}
	if rows > maxScreenRows {
		rows = maxScreenRows
	}
	if cols > maxScreenCols {
		cols = maxScreenCols
	}
	s := &screen{cells: make([][]rune, rows), rows: rows, cols: cols}
	for i := range s.cells {
		s.cells[i] = make([]rune, cols)
		for j := range s.cells[i] {
			s.cells[i][j] = ' '
		}
	}
	return s
}

func (s *screen) clampRow(r int) int {
	if r < 0 {
		return 0
	}
	if r >= s.rows {
		return s.rows - 1
	}
	return r
}

func (s *screen) clampCol(c int) int {
	if c < 0 {
		return 0
	}
	if c >= s.cols {
		return s.cols - 1
	}
	return c
}

// scrollUp discards the top line — what a real terminal does when output passes the last row.
// Without it a long-running shell would keep overwriting the bottom line forever.
func (s *screen) scrollUp() {
	copy(s.cells, s.cells[1:])
	last := make([]rune, s.cols)
	for i := range last {
		last[i] = ' '
	}
	s.cells[s.rows-1] = last
}

func (s *screen) newline() {
	s.row++
	if s.row >= s.rows {
		s.row = s.rows - 1
		s.scrollUp()
	}
}

func (s *screen) put(r rune) {
	if s.col >= s.cols {
		// Line wrap — the next glyph belongs on the following row, like a real terminal.
		s.col = 0
		s.newline()
	}
	s.cells[s.row][s.col] = r
	s.col++
}

func (s *screen) clearRegion(fromRow, fromCol, toRow, toCol int) {
	for r := fromRow; r <= toRow && r < s.rows; r++ {
		start, end := 0, s.cols-1
		if r == fromRow {
			start = fromCol
		}
		if r == toRow {
			end = toCol
		}
		for c := start; c <= end && c < s.cols; c++ {
			if c >= 0 {
				s.cells[r][c] = ' '
			}
		}
	}
}

// renderScreen replays raw PTY bytes onto a grid of the given size and returns the visible lines
// with trailing blank rows and trailing spaces removed.
//
// rows/cols MUST be the size of the PTY that produced `raw` (Session.PTYSize) — see the screen
// type's doc for why a mismatch corrupts the result rather than merely truncating it. Non-positive
// values fall back to the spawn size.
//
// Handled (the operations that move text around): CSI cursor positioning (H/f), relative moves
// (A/B/C/D), column/row absolute (G/d), erase in display (J) and line (K), CR, LF, backspace, tab,
// and line wrap. Everything else — SGR colors, private mode sets, OSC titles, charset selects —
// is consumed and dropped, which is correct for a plain-text preview.
func renderScreen(raw string, rows, cols int) []string {
	s := newScreen(rows, cols)
	i := 0
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == 0x1b: // ESC
			i = s.consumeEscape(raw, i)
			continue
		case c == '\r':
			s.col = 0
			i++
			continue
		case c == '\n':
			s.newline()
			i++
			continue
		case c == '\b':
			if s.col > 0 {
				s.col--
			}
			i++
			continue
		case c == '\t':
			next := (s.col/8 + 1) * 8
			s.col = s.clampCol(next)
			i++
			continue
		case c == 0x07: // BEL
			i++
			continue
		case c < 0x20:
			i++ // other C0 controls have no bearing on layout
			continue
		}
		r, size := utf8.DecodeRuneInString(raw[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		s.put(r)
		i += size
	}
	return s.lines()
}

// consumeEscape handles one escape sequence starting at raw[i] (which is ESC) and returns the
// index just past it.
func (s *screen) consumeEscape(raw string, i int) int {
	if i+1 >= len(raw) {
		return len(raw)
	}
	switch raw[i+1] {
	case '[':
		return s.consumeCSI(raw, i)
	case ']':
		// OSC: ESC ] ... BEL | ESC \ — window titles etc., no layout effect.
		j := i + 2
		for j < len(raw) {
			if raw[j] == 0x07 {
				return j + 1
			}
			if raw[j] == 0x1b && j+1 < len(raw) && raw[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(raw)
	case '(', ')', '#':
		return i + 3 // charset / line-attribute selects: ESC ( B, ESC # 8 …
	case 'M':
		// Reverse index — move up one row, scrolling at the top.
		if s.row > 0 {
			s.row--
		}
		return i + 2
	default:
		return i + 2 // ESC = / ESC > keypad mode and friends
	}
}

// consumeCSI parses ESC [ <params> <final> and applies the ones that move or erase text.
func (s *screen) consumeCSI(raw string, i int) int {
	j := i + 2
	start := j
	for j < len(raw) && !((raw[j] >= 'A' && raw[j] <= 'Z') || (raw[j] >= 'a' && raw[j] <= 'z')) {
		j++
	}
	if j >= len(raw) {
		return len(raw)
	}
	final := raw[j]
	body := raw[start:j]
	end := j + 1

	// Private sequences (ESC [ ? …) are modes (alt screen, bracketed paste, cursor visibility).
	// They do not reposition text, so drop them — but ignoring the alt-screen switch is why a
	// full-screen TUI's content still lands on this one grid, which is what we want for a preview.
	if strings.HasPrefix(body, "?") {
		return end
	}

	params := parseCSIParams(body)
	p := func(idx, def int) int {
		if idx < len(params) && params[idx] > 0 {
			return params[idx]
		}
		return def
	}

	switch final {
	case 'H', 'f': // cursor position (1-based)
		s.row = s.clampRow(p(0, 1) - 1)
		s.col = s.clampCol(p(1, 1) - 1)
	case 'A':
		s.row = s.clampRow(s.row - p(0, 1))
	case 'B':
		s.row = s.clampRow(s.row + p(0, 1))
	case 'C':
		s.col = s.clampCol(s.col + p(0, 1))
	case 'D':
		s.col = s.clampCol(s.col - p(0, 1))
	case 'G':
		s.col = s.clampCol(p(0, 1) - 1)
	case 'd':
		s.row = s.clampRow(p(0, 1) - 1)
	case 'E':
		s.row = s.clampRow(s.row + p(0, 1))
		s.col = 0
	case 'F':
		s.row = s.clampRow(s.row - p(0, 1))
		s.col = 0
	case 'J': // erase in display
		switch p(0, 0) {
		case 0:
			s.clearRegion(s.row, s.col, s.rows-1, s.cols-1)
		case 1:
			s.clearRegion(0, 0, s.row, s.col)
		default:
			s.clearRegion(0, 0, s.rows-1, s.cols-1)
		}
	case 'K': // erase in line
		switch p(0, 0) {
		case 0:
			s.clearRegion(s.row, s.col, s.row, s.cols-1)
		case 1:
			s.clearRegion(s.row, 0, s.row, s.col)
		default:
			s.clearRegion(s.row, 0, s.row, s.cols-1)
		}
	}
	return end
}

func parseCSIParams(body string) []int {
	if body == "" {
		return nil
	}
	parts := strings.Split(body, ";")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n := 0
		ok := false
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				ok = false
				break
			}
			n = n*10 + int(ch-'0')
			ok = true
		}
		if !ok {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// lines reads the grid back as text: trailing spaces trimmed per row, trailing blank rows dropped.
func (s *screen) lines() []string {
	out := make([]string, 0, s.rows)
	for _, row := range s.cells {
		out = append(out, strings.TrimRight(string(row), " "))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}
