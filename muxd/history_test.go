package muxd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Scrollback's promises, as tests:
//
//   - lines come back in order, numbered by a cursor that survives eviction;
//   - colour costs almost nothing, which is the claim the whole design rests on;
//   - a full-screen TUI costs nothing at all;
//   - and a historian that breaks cannot take the terminal down with it.
//
// The last one is not a nicety. This runs inside the daemon that owns every live shell on the
// machine, so "the scrollback code panicked" must be a lost feature, never a lost session.

func feed(h *History, chunks ...string) {
	for _, c := range chunks {
		h.Write([]byte(c))
	}
}

// scrollOff writes n numbered lines to a small screen so they all pass through history.
func scrollOff(h *History, n int) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	h.Write([]byte(b.String()))
}

func TestHistory_LinesComeBackInOrder(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 3}, 1000)
	scrollOff(h, 10)
	lines, st := h.Read(0, 100)
	if st.Base != 0 {
		t.Fatalf("base = %d, want 0 — nothing should have been evicted yet", st.Base)
	}
	// A 3-row screen keeps the last few on screen; everything before them is history.
	if len(lines) < 7 {
		t.Fatalf("only %d lines reached history", len(lines))
	}
	for i, l := range lines {
		if l.N != int64(i) {
			t.Fatalf("line %d numbered %d", i, l.N)
		}
		if want := fmt.Sprintf("line %d", i); l.Text != want {
			t.Fatalf("line %d = %q, want %q", i, l.Text, want)
		}
	}
}

// The claim that makes the whole design affordable: an ordinary line carries no style data at all.
// If this ever regresses, every uncoloured session starts paying for a feature it does not use.
func TestHistory_PlainLinesCarryNoSpans(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 3}, 1000)
	scrollOff(h, 20)
	lines, _ := h.Read(0, 100)
	for _, l := range lines {
		if l.Spans != nil {
			t.Fatalf("plain line %q carries spans %v — nil is what makes colour free", l.Text, l.Spans)
		}
	}
}

func TestHistory_SpansMarkStyleChangesAtByteOffsets(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 2}, 1000)
	// Multi-byte on purpose: the offsets are BYTE offsets into UTF-8, and a test with only ASCII
	// cannot tell a byte offset from a character offset.
	feed(h, "\x1b[31m中\x1b[0mx\r\n", "\r\n", "\r\n")
	lines, _ := h.Read(0, 1)
	if len(lines) == 0 {
		t.Fatal("nothing scrolled off")
	}
	l := lines[0]
	if l.Text != "中x" {
		t.Fatalf("text = %q", l.Text)
	}
	if len(l.Spans) != 2 {
		t.Fatalf("spans = %v, want one for the coloured run and one for the reset", l.Spans)
	}
	if l.Spans[0].Start != 0 {
		t.Fatalf("first span starts at %d, want 0", l.Spans[0].Start)
	}
	if l.Spans[1].Start != 3 {
		t.Fatalf("second span starts at %d, want 3 — 中 is three BYTES", l.Spans[1].Start)
	}
	if st := h.Styles()[l.Spans[0].Style]; st.FG != IndexedColor(1) {
		t.Fatalf("first span resolves to %+v, want red", st)
	}
	if h.Styles()[l.Spans[1].Style] != DefaultStyle {
		t.Fatal("the reset span must resolve to the default style")
	}
}

func TestHistory_TrailingBlanksAreTrimmedUnlessTheyAreColoured(t *testing.T) {
	plain := NewHistory(Grid{Cols: 20, Rows: 2}, 100)
	feed(plain, "hi\r\n", "\r\n", "\r\n")
	l, _ := plain.Read(0, 1)
	if l[0].Text != "hi" {
		t.Fatalf("padding was kept: %q", l[0].Text)
	}

	painted := NewHistory(Grid{Cols: 20, Rows: 2}, 100)
	// A coloured bar IS content: a program painted it deliberately by setting a background and
	// clearing. Trimming it would turn a highlighted line into a short one.
	feed(painted, "\x1b[44m\x1b[Khi\r\n", "\r\n", "\r\n")
	l2, _ := painted.Read(0, 1)
	if len(l2[0].Text) <= 2 {
		t.Fatalf("coloured padding was trimmed away: %q", l2[0].Text)
	}
	if len(l2[0].Spans) == 0 {
		t.Fatal("coloured padding lost its style")
	}
}

// ── Eviction and paging ─────────────────────────────────────────────────────────────────────────

func TestHistory_EvictionKeepsNumbersStable(t *testing.T) {
	const maxLines = 2000
	h := NewHistory(Grid{Cols: 40, Rows: 3}, maxLines)
	scrollOff(h, 8000)
	lines, st := h.Read(st0(h), 50)

	if st.Base == 0 {
		t.Fatal("nothing was evicted — the bound is not being enforced")
	}
	held := st.Total - st.Base
	if held < maxLines {
		t.Fatalf("holding %d lines, want at least the configured %d", held, maxLines)
	}
	// Eviction is by whole chunk, so the held count overshoots the bound by less than one chunk.
	if held > int64(maxLines+historyChunkLines) {
		t.Fatalf("holding %d lines, more than max+chunk (%d)", held, maxLines+historyChunkLines)
	}
	// The cursor is absolute: after eviction the oldest surviving line still knows which line of
	// the session it is, which is what lets a client page without re-syncing.
	if lines[0].N != st.Base {
		t.Fatalf("oldest held line is numbered %d, base says %d", lines[0].N, st.Base)
	}
	if want := fmt.Sprintf("line %d", st.Base); lines[0].Text != want {
		t.Fatalf("oldest held line is %q, want %q — numbering drifted from content", lines[0].Text, want)
	}
}

func st0(h *History) int64 { return h.Stats().Base }

func TestHistory_ReadClampsInsteadOfFailing(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 3}, 2000)
	scrollOff(h, 8000)
	st := h.Stats()

	// Asking for a line that has been evicted. The caller is scrolling; the honest answer is the
	// oldest line still held, together with the Base that explains why.
	old, _ := h.Read(0, 5)
	if len(old) == 0 || old[0].N != st.Base {
		t.Fatalf("a read below base returned %v, want it clamped to base %d", old, st.Base)
	}
	// Past the end is empty, not an error and not the last page again.
	if got, _ := h.Read(st.Total+10, 5); len(got) != 0 {
		t.Fatalf("a read past the end returned %d lines", len(got))
	}
	// One page never returns unbounded data, however much is asked for.
	if got, _ := h.Read(st.Base, 1_000_000); len(got) > maxHistoryReadLines {
		t.Fatalf("a huge request returned %d lines, cap is %d", len(got), maxHistoryReadLines)
	}
	// A page that spans a chunk boundary is contiguous and correctly numbered.
	page, _ := h.Read(st.Base+historyChunkLines-3, 6)
	for i, l := range page {
		if l.N != st.Base+historyChunkLines-3+int64(i) {
			t.Fatalf("page is not contiguous across the chunk boundary: %+v", page)
		}
	}
}

// ── The live screen ─────────────────────────────────────────────────────────────────────────────

func TestHistory_ScreenContinuesTheNumbering(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 4}, 1000)
	scrollOff(h, 10)
	st := h.Stats()
	screen, _ := h.Screen()
	if len(screen) == 0 {
		t.Fatal("the visible screen came back empty")
	}
	if screen[0].N != st.Total {
		t.Fatalf("the screen starts at %d but history ends at %d — a viewer would render a gap",
			screen[0].N, st.Total)
	}
}

func TestHistory_ScreenShowsTheParkedShellWhileATUIIsRunning(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 4}, 1000)
	feed(h, "shell output\r\n")
	feed(h, "\x1b[?1049h", "\x1b[1;1Hfullscreen frame")
	screen, _ := h.Screen()
	joined := strings.Join(textOf(screen), "\n")
	if strings.Contains(joined, "fullscreen frame") {
		t.Fatalf("the history view shows a TUI frame: %q", joined)
	}
	if !strings.Contains(joined, "shell output") {
		t.Fatalf("the parked shell screen was lost: %q", joined)
	}
}

func textOf(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

// ── The guarantee: a broken historian is a lost feature, not a lost session ─────────────────────

func TestHistory_APanicDisablesHistoryAndNothingElse(t *testing.T) {
	h := NewHistory(Grid{Cols: 40, Rows: 3}, 1000)
	scrollOff(h, 10)
	before := h.Stats().Total

	// Corrupt the model the way only a bug could. The point is not this particular corruption —
	// it is that ANY panic below Write is contained.
	h.vt.cells = nil

	feed(h, "this will panic inside the model\r\n") // must not panic out of Write
	st := h.Stats()
	if !st.Broken {
		t.Fatal("history did not mark itself broken")
	}
	if st.Reason == "" {
		t.Fatal("broken history did not record why")
	}
	// What was already collected is STILL readable. "History stopped growing" is a much better
	// answer to give someone who was scrolling through it a moment ago than "history vanished",
	// and Base/Total stay meaningful, which is what a client is using as a paging cursor.
	if lines, _ := h.Read(0, 10); len(lines) == 0 {
		t.Fatal("the break threw away history that had already been collected")
	}
	if st.Total != before {
		t.Fatalf("total moved after the break: %d → %d", before, st.Total)
	}
	feed(h, "more\r\n") // subsequent writes are no-ops, not repeated panics
	h.Resize(Grid{Cols: 10, Rows: 10})
}

// The same guarantee at the level that matters: through a real Session, the byte stream a client
// sees is unaffected by a historian that has broken.
func TestSession_BrokenHistoryDoesNotDisturbTheByteStream(t *testing.T) {
	s, w := pipeSession(t, SpawnOptions{Argv: []string{"/bin/sh"}, HistoryLines: 1000})
	defer s.Destroy()

	if _, err := w.Write([]byte("first\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, 2*time.Second, "first reaches the ring", func() bool { return strings.Contains(string(s.ring.Read()), "first") })

	// Break the historian mid-session.
	s.History().mu.Lock()
	s.History().vt.cells = nil
	s.History().mu.Unlock()

	if _, err := w.Write([]byte("second\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, 2*time.Second, "second reaches the ring", func() bool { return strings.Contains(string(s.ring.Read()), "second") })

	if !s.History().Stats().Broken {
		t.Fatal("the historian should have marked itself broken")
	}
	if got := string(s.ring.Read()); !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("the ring lost bytes when history broke: %q", got)
	}
}

func TestSession_HistoryCanBeDisabled(t *testing.T) {
	s, w := pipeSession(t, SpawnOptions{Argv: []string{"/bin/sh"}, HistoryLines: -1})
	defer s.Destroy()
	if s.History() != nil {
		t.Fatal("HistoryLines < 0 must mean no history at all")
	}
	// The nil History still has to be usable — every call site would otherwise need a branch.
	if _, err := w.Write([]byte("hello\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, 2*time.Second, "hello reaches the ring", func() bool { return strings.Contains(string(s.ring.Read()), "hello") })
	if lines, st := s.History().Read(0, 10); lines != nil || st.Total != 0 {
		t.Fatal("a nil History must read as empty rather than panic")
	}
}

func TestSession_ResizeReachesTheScreenModel(t *testing.T) {
	s, _ := pipeSession(t, SpawnOptions{Argv: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	defer s.Destroy()
	if err := s.Resize(100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	h := s.History()
	h.mu.Lock()
	cols, rows := h.vt.cols, h.vt.rows
	h.mu.Unlock()
	// A model that keeps laying lines out at the old width mis-wraps everything after a resize.
	if cols != 100 || rows != 30 {
		t.Fatalf("model is %dx%d, PTY is 100x30", cols, rows)
	}
}

func pipeSession(t *testing.T, opts SpawnOptions) (*Session, *os.File) {
	t.Helper()
	var w *os.File
	s, err := SpawnWith("hist-"+t.Name(), opts, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, pw, perr := os.Pipe()
		w = pw
		return r, nil, perr
	}, nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return s, w
}

// ── The memory claim, measured ──────────────────────────────────────────────────────────────────

// The design's headline is "colour is nearly free": interning plus run-length spans plus chunk
// arenas keep a coloured line within about 15% of a plain one. That is a number, so it gets a test
// rather than a comment — and the budget it has to fit inside was agreed before the code existed.
func TestHistory_FiftyThousandLinesFitTheBudget(t *testing.T) {
	const lines = DefaultHistoryLines

	plain := NewHistory(Grid{Cols: 200, Rows: 50}, lines)
	colour := NewHistory(Grid{Cols: 200, Rows: 50}, lines)
	var pb, cb strings.Builder
	for i := 0; i < lines+100; i++ {
		fmt.Fprintf(&pb, "2026-09-05 12:00:00 INFO  worker[%05d] finished in 12ms\r\n", i)
		// Roughly what a coloured log line looks like: a handful of runs, not a colour per cell.
		fmt.Fprintf(&cb, "\x1b[90m2026-09-05 12:00:00\x1b[0m \x1b[32mINFO\x1b[0m  worker[%05d] finished in \x1b[1m12ms\x1b[0m\r\n", i)
	}
	plain.Write([]byte(pb.String()))
	colour.Write([]byte(cb.String()))

	ps, cs := plain.Stats(), colour.Stats()
	t.Logf("plain  %d lines → %.1f MB (%.0f B/line)", ps.Total, mb(ps.Bytes), perLine(ps))
	t.Logf("colour %d lines → %.1f MB (%.0f B/line), %d styles", cs.Total, mb(cs.Bytes), perLine(cs), cs.Styles)

	// The per-session budget this feature was given. Twelve sessions at this size is ~60 MB, which
	// is the ceiling the change was accepted under.
	if mb(cs.Bytes) > 5.0 {
		t.Fatalf("coloured history is %.1f MB per session, budget is 5 MB", mb(cs.Bytes))
	}
	// The claim itself: colour is a small premium, not a multiplier.
	if ratio := float64(cs.Bytes) / float64(ps.Bytes); ratio > 1.5 {
		t.Fatalf("colour costs %.2f× plain text; the design claims ~1.15×", ratio)
	}
	if cs.Styles > 20 {
		t.Fatalf("%d styles interned for a stream that uses four — interning is not working", cs.Styles)
	}
}

// A full-screen TUI must cost nothing. This is what keeps a day of Claude Code from evicting the
// shell output someone actually wants to scroll back to.
func TestHistory_AFullScreenTUICostsNothing(t *testing.T) {
	h := NewHistory(Grid{Cols: 200, Rows: 50}, DefaultHistoryLines)
	feed(h, "before the app\r\n")
	baseline := h.Stats()

	var b strings.Builder
	b.WriteString("\x1b[?1049h")
	for frame := 0; frame < 500; frame++ {
		b.WriteString("\x1b[H")
		for row := 0; row < 50; row++ {
			fmt.Fprintf(&b, "\x1b[%d;1H\x1b[32mframe %d row %d\x1b[0m", row+1, frame, row)
		}
	}
	b.WriteString("\x1b[?1049l")
	h.Write([]byte(b.String()))

	after := h.Stats()
	if after.Total != baseline.Total {
		t.Fatalf("a TUI added %d lines to history", after.Total-baseline.Total)
	}
}

func mb(b int) float64 { return float64(b) / (1 << 20) }

func perLine(s HistoryStats) float64 {
	held := s.Total - s.Base
	if held == 0 {
		return 0
	}
	return float64(s.Bytes) / float64(held)
}

// ── Throughput: does the historian slow the terminal down? ──────────────────────────────────────
//
// Every byte a shell produces now goes through a parser as well as a ring write, inside the lock
// the PTY read loop holds. That was the headline risk of putting scrollback in the daemon, so it
// gets measured rather than assumed. Compare the MB/s here against what a PTY can actually deliver
// — a shell flooding output tops out in the low tens of MB/s, and a person reading a terminal
// generates a few KB/s.
//
//	go test ./muxd/ -run XXX -bench 'BenchmarkHistory' -benchmem

func BenchmarkHistoryWritePlain(b *testing.B)    { benchWrite(b, plainStream()) }
func BenchmarkHistoryWriteColour(b *testing.B)   { benchWrite(b, colourStream()) }
func BenchmarkHistoryWriteTUIFrame(b *testing.B) { benchWrite(b, tuiStream()) }

func benchWrite(b *testing.B, stream []byte) {
	h := NewHistory(Grid{Cols: 200, Rows: 50}, DefaultHistoryLines)
	b.SetBytes(int64(len(stream)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Write(stream)
	}
}

func plainStream() []byte {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "2026-09-05 12:00:00 INFO  worker[%05d] finished in 12ms\r\n", i)
	}
	return []byte(b.String())
}

func colourStream() []byte {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "\x1b[90m2026-09-05 12:00:00\x1b[0m \x1b[32mINFO\x1b[0m  worker[%05d] in \x1b[1m12ms\x1b[0m\r\n", i)
	}
	return []byte(b.String())
}

// The worst realistic case: a full-screen TUI repainting by absolute cursor address, which is what
// Claude Code does many times a second and which produces no history at all.
func tuiStream() []byte {
	var b strings.Builder
	b.WriteString("\x1b[?1049h")
	for frame := 0; frame < 4; frame++ {
		for row := 0; row < 50; row++ {
			fmt.Fprintf(&b, "\x1b[%d;1H\x1b[K\x1b[36m│\x1b[0m frame %d row %d", row+1, frame, row)
		}
	}
	return []byte(b.String())
}

// ── Search ──────────────────────────────────────────────────────────────────────────────────────

func searchFixture(t *testing.T) *History {
	t.Helper()
	h := NewHistory(Grid{Cols: 60, Rows: 3}, 10000)
	var b strings.Builder
	for i := 0; i < 100; i++ {
		switch i {
		case 10:
			b.WriteString("alpha MATCH one\r\n")
		case 50:
			b.WriteString("beta match two\r\n")
		case 90:
			b.WriteString("gamma MATCH three\r\n")
		default:
			b.WriteString(fmt.Sprintf("filler %d\r\n", i))
		}
	}
	h.Write([]byte(b.String()))
	return h
}

func TestHistorySearch_ForwardReturnsOldestFirst(t *testing.T) {
	m, _ := searchFixture(t).Search("MATCH", 0, false, 10, false)
	if len(m) != 2 {
		t.Fatalf("got %d matches, want 2 (case-sensitive): %+v", len(m), m)
	}
	if m[0].N != 10 || m[1].N != 90 {
		t.Fatalf("forward search returned lines %d,%d — want 10 then 90", m[0].N, m[1].N)
	}
}

// "Previous match" reads backwards, so the results have to arrive in that order or the caller has
// to reverse them and will eventually forget to.
func TestHistorySearch_BackwardReturnsNewestFirst(t *testing.T) {
	h := searchFixture(t)
	st := h.Stats()
	m, _ := h.Search("MATCH", st.Total, true, 10, false)
	if len(m) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(m), m)
	}
	if m[0].N != 90 || m[1].N != 10 {
		t.Fatalf("backward search returned lines %d,%d — want 90 then 10", m[0].N, m[1].N)
	}
}

func TestHistorySearch_IgnoreCaseFindsAllThree(t *testing.T) {
	m, _ := searchFixture(t).Search("match", 0, false, 10, true)
	if len(m) != 3 {
		t.Fatalf("got %d matches, want 3 with case folding: %+v", len(m), m)
	}
}

func TestHistorySearch_LimitAndClampingHold(t *testing.T) {
	h := searchFixture(t)
	if m, _ := h.Search("filler", 0, false, 3, false); len(m) != 3 {
		t.Fatalf("limit ignored: got %d", len(m))
	}
	// A `from` before the oldest held line, and one past the end. Neither is an error: the caller
	// is scrolling, and both have an obvious right answer.
	if m, _ := h.Search("MATCH", -1000, false, 10, false); len(m) != 2 {
		t.Fatalf("a from below base returned %d matches", len(m))
	}
	if m, _ := h.Search("MATCH", 1<<40, false, 10, false); len(m) != 0 {
		t.Fatalf("a forward search starting past the end returned %d matches", len(m))
	}
	// An empty query is not "match everything" — it is a caller that has not typed anything yet.
	if m, _ := h.Search("", 0, false, 10, false); len(m) != 0 {
		t.Fatalf("an empty query returned %d matches", len(m))
	}
}

func TestHistorySearch_ABrokenHistorianSearchesEmptyRatherThanPanicking(t *testing.T) {
	h := searchFixture(t)
	h.mu.Lock()
	h.vt = nil
	h.broken = true
	h.mu.Unlock()
	// The store survives a break (see guard), so search still works — that is the point of keeping
	// it. What must never happen is a panic travelling up a client's request path.
	if m, _ := h.Search("MATCH", 0, false, 10, false); len(m) != 2 {
		t.Fatalf("a broken historian lost its already-collected history: %d matches", len(m))
	}
}
