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
}

// lineEnd is one line's exclusive end offsets in its chunk's two arenas. Ends rather than starts,
// so a line's start is the previous line's end and no per-line start needs storing.
type lineEnd struct {
	text uint32
	span uint32
}

// histChunk is a fixed-capacity run of lines sharing three slices.
type histChunk struct {
	first int64 // absolute number of this chunk's first line
	text  []byte
	spans []Span
	ends  []lineEnd
}

func (c *histChunk) lineCount() int { return len(c.ends) }

// bounds returns the arena ranges of the i-th line in this chunk.
func (c *histChunk) bounds(i int) (textLo, textHi, spanLo, spanHi uint32) {
	if i > 0 {
		textLo, spanLo = c.ends[i-1].text, c.ends[i-1].span
	}
	return textLo, c.ends[i].text, spanLo, c.ends[i].span
}

func (c *histChunk) approxBytes() int {
	return cap(c.text) + cap(c.spans)*4 + cap(c.ends)*8
}

// lineStore is the chunked, bounded sequence of scrolled-off lines.
type lineStore struct {
	chunks   []*histChunk
	maxLines int
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
func (s *lineStore) appendRow(row []cell) {
	c := s.tail()
	textStart := uint32(len(c.text))
	spanStart := uint32(len(c.spans))

	// Trim trailing cells that are blank AND unstyled. A trailing space with a background colour
	// is not padding — it is a coloured bar a program deliberately painted, and dropping it would
	// turn a highlighted line into a short one.
	end := len(row)
	for end > 0 {
		cc := row[end-1]
		if cc.style != DefaultStyleID {
			break
		}
		if cc.r != ' ' && cc.r != 0 {
			break
		}
		end--
	}

	prev := DefaultStyleID
	var buf [utf8.UTFMax]byte
	for i := 0; i < end; i++ {
		cc := row[i]
		if cc.style != prev {
			c.spans = append(c.spans, Span{Start: uint16(uint32(len(c.text)) - textStart), Style: cc.style})
			prev = cc.style
		}
		if cc.r == 0 {
			continue // second half of a double-width character; its lead already carried the style
		}
		n := utf8.EncodeRune(buf[:], cc.r)
		c.text = append(c.text, buf[:n]...)
	}
	// A run that turns out to be empty (every cell of it trimmed away) leaves a span pointing at
	// the end of the text; drop those so "no spans" keeps meaning "entirely default".
	for len(c.spans) > int(spanStart) && c.spans[len(c.spans)-1].Start == uint16(uint32(len(c.text))-textStart) {
		c.spans = c.spans[:len(c.spans)-1]
	}
	c.ends = append(c.ends, lineEnd{text: uint32(len(c.text)), span: uint32(len(c.spans))})
	s.total++
	s.evict()
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
			line := Line{N: c.first + int64(i), Text: string(c.text[tLo:tHi])}
			if sHi > sLo {
				line.Spans = append([]Span(nil), c.spans[sLo:sHi]...)
			}
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
	grid := h.vt.cells
	if h.vt.alt && h.vt.altSaved != nil {
		grid = h.vt.altSaved.cells
	}
	tmp := newLineStore(len(grid) + 1)
	for _, row := range grid {
		tmp.appendRow(row)
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
