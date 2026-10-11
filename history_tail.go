package terminal

import (
	"net/url"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// historyFetcher is the one daemon call tailHistory is built from; a seam so the stitching can be
// tested without a daemon.
type historyFetcher func(muxd.HistoryReq) (muxd.HistoryAck, error)

// tailHistory answers "show me the end of this session's history" in ONE browser round trip: the
// newest `tail` lines of scrollback followed by the live screen, numbered the way the client's
// merge expects (screen lines continue where history stops).
//
// The copy view used to open with three browser requests in strict sequence — a probe whose only
// purpose was to learn `total`, then the last page, then the screen — and over a link with a few
// hundred milliseconds of latency the click on 回看 paid three round trips before anything could be
// drawn. The daemon is a local socket away, so the same steps cost almost nothing HERE. Every ack
// carries base/total, which is all the probe ever wanted, so two local reads are enough: the screen
// (its ack names where history ends), then the page that ends exactly there.
func tailHistory(fetch historyFetcher, tail, stylesFrom int) (muxd.HistoryAck, error) {
	screen, err := fetch(muxd.HistoryReq{Screen: true, StylesFrom: stylesFrom})
	if err != nil || !screen.Enabled {
		return screen, err
	}
	from := screen.Total - int64(tail)
	if from < screen.Base {
		from = screen.Base
	}
	count := int(screen.Total - from)
	if count <= 0 {
		return screen, nil
	}
	page, err := fetch(muxd.HistoryReq{From: from, Count: count, StylesFrom: stylesFrom})
	if err != nil {
		return muxd.HistoryAck{}, err
	}
	// The two reads are not one atomic snapshot. If history moved between them (the oldest lines
	// were evicted, or the daemon capped the page) the page no longer ends exactly where the screen
	// begins, and joining them would put a hole in the middle of the view. Ship only the screen:
	// the client then pages older lines itself, from the screen's first line, which is contiguous
	// by construction.
	if n := len(page.Lines); n == 0 || page.Lines[0].N != from || page.Lines[n-1].N != screen.Total-1 {
		return screen, nil
	}
	out := screen
	out.Lines = append(append(make([]muxd.Line, 0, len(page.Lines)+len(screen.Lines)), page.Lines...), screen.Lines...)
	// The page was read AFTER the screen, and the style table only ever grows, so its slice of the
	// table is a superset of the one that came with the screen.
	out.Styles, out.StylesTotal = page.Styles, page.StylesTotal
	out.Broken = screen.Broken || page.Broken
	return out, nil
}

// tmuxHistoryLines selects the numbered lines one request wants out of a whole tmux capture.
//
// Numbering is the capture's own: history line i is n=i+1 and the screen continues after the last
// history line. The parameters mean what they mean for the daemon's history (see
// handleSessionHistory), so the client pages both sources identically. With none of them the whole
// capture comes back, which is what every request used to get regardless of what it asked for —
// each page of the copy view cost the full history in JSON.
func tmuxHistoryLines(q url.Values, history, screen []string) []historyLineJSON {
	total := len(history)
	line := func(n int, text string) historyLineJSON {
		return historyLineJSON{N: int64(n), Seg: []historySegment{{T: text}}}
	}
	// History lines with n in [lo, hi). Always a non-nil slice: it is serialised, and `null` is not
	// the same answer as "none".
	hist := func(lo, hi int) []historyLineJSON {
		lo, hi = max(lo, 1), min(hi, total+1)
		out := make([]historyLineJSON, 0, max(hi-lo, 0))
		for n := lo; n < hi; n++ {
			out = append(out, line(n, history[n-1]))
		}
		return out
	}
	onScreen := func(out []historyLineJSON) []historyLineJSON {
		for j, l := range screen {
			out = append(out, line(total+1+j, l))
		}
		return out
	}

	switch tail := min(int(atoi64(q.Get("tail"), 0)), maxHistoryTail); {
	case tail > 0:
		return onScreen(hist(total-tail+1, total+1))
	case q.Get("screen") == "1":
		return onScreen(hist(0, 0))
	case q.Has("from") || q.Has("count"):
		from := int(atoi64(q.Get("from"), 0))
		count := min(max(int(atoi64(q.Get("count"), defaultHistoryPage)), 0), maxHistoryTail)
		return hist(from, from+count)
	default:
		return onScreen(hist(1, total+1))
	}
}
