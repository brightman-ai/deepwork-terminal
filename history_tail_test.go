package terminal

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// fakeDaemon answers the way the real one does: a Read XOR a Screen per request, base/total on
// every ack, screen lines numbered from `total`, and a style table that is sent incrementally.
type fakeDaemon struct {
	base, total int64
	screenRows  int
	styles      []muxd.Style
	disabled    bool
	// beforeRead runs before a Read is answered — lets a test move history "between the two reads".
	beforeRead func(d *fakeDaemon)
	calls      []muxd.HistoryReq
}

func (d *fakeDaemon) fetch(req muxd.HistoryReq) (muxd.HistoryAck, error) {
	d.calls = append(d.calls, req)
	if d.disabled {
		return muxd.HistoryAck{Enabled: false}, nil
	}
	if !req.Screen && d.beforeRead != nil {
		d.beforeRead(d)
	}
	ack := muxd.HistoryAck{Enabled: true, Base: d.base, Total: d.total, StylesTotal: len(d.styles)}
	if req.StylesFrom < len(d.styles) {
		ack.Styles = d.styles[req.StylesFrom:]
	}
	if req.Screen {
		for i := 0; i < d.screenRows; i++ {
			ack.Lines = append(ack.Lines, muxd.Line{N: d.total + int64(i), Text: "screen"})
		}
		return ack, nil
	}
	from := max(req.From, d.base)
	for n := from; n < d.total && len(ack.Lines) < req.Count; n++ {
		ack.Lines = append(ack.Lines, muxd.Line{N: n, Text: "hist"})
	}
	return ack, nil
}

func lineNumbers(ls []muxd.Line) []int64 {
	out := make([]int64, len(ls))
	for i, l := range ls {
		out[i] = l.N
	}
	return out
}

func requireContiguous(t *testing.T, ls []muxd.Line) {
	t.Helper()
	for i := 1; i < len(ls); i++ {
		if ls[i].N != ls[i-1].N+1 {
			t.Fatalf("hole between line %d and %d in %v", ls[i-1].N, ls[i].N, lineNumbers(ls))
		}
	}
}

// The whole point: the newest page and the screen arrive together, so the browser needs ONE
// request to open the view instead of probe -> page -> screen.
func TestTailHistoryReturnsNewestPageAndScreenTogether(t *testing.T) {
	d := &fakeDaemon{base: 0, total: 1200, screenRows: 5, styles: make([]muxd.Style, 3)}
	ack, err := tailHistory(d.fetch, 500, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.Lines) != 505 {
		t.Fatalf("got %d lines, want 500 history + 5 screen", len(ack.Lines))
	}
	if first, last := ack.Lines[0].N, ack.Lines[len(ack.Lines)-1].N; first != 700 || last != 1204 {
		t.Fatalf("range %d..%d, want 700..1204", first, last)
	}
	requireContiguous(t, ack.Lines)
	if ack.Total != 1200 || ack.Base != 0 {
		t.Fatalf("base/total = %d/%d; they must describe HISTORY (screen lines start at total)", ack.Base, ack.Total)
	}
	// Style table is incremental from what the client holds, and complete for every line sent.
	if len(ack.Styles) != 2 || ack.StylesTotal != 3 {
		t.Fatalf("styles=%d total=%d, want the 2 entries after the client's 1", len(ack.Styles), ack.StylesTotal)
	}
	// Two LOCAL reads, screen first (its ack names where history ends), page second.
	if len(d.calls) != 2 || !d.calls[0].Screen || d.calls[1].From != 700 || d.calls[1].Count != 500 {
		t.Fatalf("unexpected daemon calls: %+v", d.calls)
	}
}

func TestTailHistoryNeverReachesBelowTheOldestRetainedLine(t *testing.T) {
	d := &fakeDaemon{base: 1150, total: 1200, screenRows: 3}
	ack, err := tailHistory(d.fetch, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Lines[0].N != 1150 || len(ack.Lines) != 53 {
		t.Fatalf("got %v", lineNumbers(ack.Lines))
	}
	requireContiguous(t, ack.Lines)
	if ack.Base != 1150 {
		t.Fatalf("base = %d; the client needs it to know it has reached the beginning", ack.Base)
	}
}

func TestTailHistoryWithNoHistoryIsJustTheScreen(t *testing.T) {
	d := &fakeDaemon{total: 0, screenRows: 4}
	ack, err := tailHistory(d.fetch, 500, 0)
	if err != nil || len(ack.Lines) != 4 || len(d.calls) != 1 {
		t.Fatalf("lines=%d calls=%d err=%v", len(ack.Lines), len(d.calls), err)
	}
}

func TestTailHistoryPassesThroughADisabledSession(t *testing.T) {
	d := &fakeDaemon{disabled: true}
	ack, err := tailHistory(d.fetch, 500, 0)
	if err != nil || ack.Enabled || len(d.calls) != 1 {
		t.Fatalf("enabled=%t calls=%d err=%v; a session that keeps no history must answer enabled:false", ack.Enabled, len(d.calls), err)
	}
}

// History is not one atomic snapshot across the two reads. If it moved, joining the page to the
// screen would put a hole in the middle of the view — ship only the screen and let the client page
// older lines from there (contiguous by construction).
func TestTailHistoryDoesNotJoinAPageThatNoLongerEndsAtTheScreen(t *testing.T) {
	d := &fakeDaemon{base: 0, total: 1200, screenRows: 3}
	d.beforeRead = func(d *fakeDaemon) { d.base = 1300; d.total = 1300 } // everything evicted meanwhile
	ack, err := tailHistory(d.fetch, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.Lines) != 3 || ack.Lines[0].N != 1200 {
		t.Fatalf("expected only the screen (1200..1202), got %v", lineNumbers(ack.Lines))
	}
	requireContiguous(t, ack.Lines)
}

func TestTailHistoryPropagatesDaemonErrors(t *testing.T) {
	boom := errors.New("daemon gone")
	if _, err := tailHistory(func(muxd.HistoryReq) (muxd.HistoryAck, error) { return muxd.HistoryAck{}, boom }, 500, 0); !errors.Is(err, boom) {
		t.Fatalf("screen error lost: %v", err)
	}
	n := 0
	fail2nd := func(muxd.HistoryReq) (muxd.HistoryAck, error) {
		n++
		if n == 2 {
			return muxd.HistoryAck{}, boom
		}
		return muxd.HistoryAck{Enabled: true, Total: 10}, nil
	}
	if _, err := tailHistory(fail2nd, 500, 0); !errors.Is(err, boom) {
		t.Fatalf("page error lost (must not return a half answer): %v", err)
	}
}

// ── tmux source ─────────────────────────────────────────────────────────────────────────────────

func ns(ls []historyLineJSON) []int64 {
	out := make([]int64, len(ls))
	for i, l := range ls {
		out[i] = l.N
	}
	return out
}

func equalNs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTmuxHistoryLinesHonoursTheWindowTheRequestAsksFor(t *testing.T) {
	history := []string{"h1", "h2", "h3", "h4", "h5"}
	screen := []string{"s1", "s2"}
	cases := []struct {
		name  string
		query string
		want  []int64
	}{
		{"tail is the newest history then the screen", "tail=3", []int64{3, 4, 5, 6, 7}},
		{"tail larger than the history is the whole history", "tail=99", []int64{1, 2, 3, 4, 5, 6, 7}},
		{"screen only", "screen=1", []int64{6, 7}},
		{"the old probe: from=0&count=1 learns total without the lines", "from=0&count=1", []int64{}},
		{"a page", "from=2&count=2", []int64{2, 3}},
		{"a page clamped at the end of history, screen NOT included", "from=4&count=50", []int64{4, 5}},
		{"no paging parameters: the whole capture, as before", "", []int64{1, 2, 3, 4, 5, 6, 7}},
	}
	for _, tc := range cases {
		q, _ := url.ParseQuery(tc.query)
		got := ns(tmuxHistoryLines(q, history, screen))
		if !equalNs(got, tc.want) {
			t.Errorf("%s: ?%s -> %v, want %v", tc.name, tc.query, got, tc.want)
		}
	}
}

// `null` and `[]` are different answers to a client that does lines.length / lines.concat.
func TestTmuxHistoryLinesAlwaysSerialisesAnArray(t *testing.T) {
	q, _ := url.ParseQuery("from=0&count=1")
	b, err := json.Marshal(tmuxHistoryLines(q, nil, nil))
	if err != nil || string(b) != "[]" {
		t.Fatalf("got %s (%v), want []", b, err)
	}
}

// Paging a tmux tab back must be able to reach every line exactly once: the pages the client asks
// for (from/count chained downwards from the tail) tile the history with no gap and no overlap.
func TestTmuxHistoryLinesPagesTileTheHistory(t *testing.T) {
	history := make([]string, 1234)
	for i := range history {
		history[i] = "x"
	}
	seen := map[int64]int{}
	tail, _ := url.ParseQuery("tail=500")
	lines := tmuxHistoryLines(tail, history, nil)
	for _, l := range lines {
		seen[l.N]++
	}
	oldest := lines[0].N
	for oldest > 1 {
		from := max(int64(0), oldest-500)
		q, _ := url.ParseQuery("from=" + strconv.FormatInt(from, 10) + "&count=" + strconv.FormatInt(oldest-from, 10))
		page := tmuxHistoryLines(q, history, nil)
		for _, l := range page {
			seen[l.N]++
		}
		oldest = from
		if from == 0 {
			break
		}
	}
	for n := int64(1); n <= 1234; n++ {
		if seen[n] != 1 {
			t.Fatalf("line %d delivered %d times", n, seen[n])
		}
	}
}
