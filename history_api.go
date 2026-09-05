package terminal

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// The browser's door to a session's scrollback.
//
// ── What this layer is for ──────────────────────────────────────────────────────────────────────
// The history itself lives in the daemon (muxd/history.go), because that is the only process that
// sees every byte and outlives a rebuild. This file is the boundary, and a boundary has exactly
// two jobs here:
//
//  1. Authorise and forward. It does NOT cache: the server already keeps a duplicate of every
//     session's byte stream, and a third copy of the same content in another shape would just give
//     the same fact three places to disagree.
//
//  2. Change the SHAPE of a line for a consumer that cannot use the internal one.
//
// ── Why the shape changes here ──────────────────────────────────────────────────────────────────
// Internally a line is text plus style spans at BYTE offsets, which is the natural form in Go and
// the compact one on the daemon's side. JavaScript strings are UTF-16: slicing them at a byte
// offset cuts Chinese and emoji in half. Rather than publish an offset convention and hope every
// future client converts it correctly, this layer splits each line into segments — text with a
// style id attached — so no offset ever crosses the process boundary. One conversion, in one
// place, that a client cannot get wrong because it is never asked to.
//
// Colours are sent as the terminal MEANT them (default / palette index / rgb), never resolved to
// concrete values. Index 2 means "the terminal's green", and which green that is belongs to the
// viewer's theme — resolving it here would bake today's theme into history that outlives it.

// historySegment is a run of text sharing one style. `s` indexes the style table; 0 is always the
// default style, and a line drawn entirely in it comes back as a single segment with s=0.
type historySegment struct {
	T string `json:"t"`
	S int    `json:"s"`
}

type historyLineJSON struct {
	// N is the line's absolute number: a stable paging cursor that keeps counting after eviction.
	N   int64            `json:"n"`
	Seg []historySegment `json:"seg"`
}

// historyStyleJSON is one style table entry.
//
// Colours are strings rather than numbers because there are three distinct cases and a number
// would need an accompanying tag: "" is the terminal's default, "iN" a palette index, "#rrggbb"
// a 24-bit colour. A client renders the first as "inherit", looks the second up in its own theme,
// and uses the third literally.
type historyStyleJSON struct {
	FG    string `json:"fg,omitempty"`
	BG    string `json:"bg,omitempty"`
	Attrs uint16 `json:"a,omitempty"`
}

func colorJSON(c muxd.Color) string {
	switch {
	case c.IsIndexed():
		return "i" + strconv.Itoa(int(c.Index()))
	case c.IsRGB():
		r, g, b := c.RGB()
		const hex = "0123456789abcdef"
		return string([]byte{'#',
			hex[r>>4], hex[r&0xf],
			hex[g>>4], hex[g&0xf],
			hex[b>>4], hex[b&0xf]})
	default:
		return ""
	}
}

func stylesJSON(styles []muxd.Style) []historyStyleJSON {
	out := make([]historyStyleJSON, len(styles))
	for i, st := range styles {
		out[i] = historyStyleJSON{FG: colorJSON(st.FG), BG: colorJSON(st.BG), Attrs: uint16(st.Attrs)}
	}
	return out
}

// segmentsOf splits one line into style runs.
//
// Spans mark where a style STARTS, so each span runs to the start of the next one and the last to
// the end of the line. A line with no spans is entirely default — the common case, and the reason
// most sessions pay nothing for colour — and becomes one segment.
func segmentsOf(l muxd.Line) []historySegment {
	if len(l.Spans) == 0 {
		if l.Text == "" {
			return []historySegment{}
		}
		return []historySegment{{T: l.Text, S: 0}}
	}
	segs := make([]historySegment, 0, len(l.Spans)+1)
	// Text before the first span is default-styled: a span at offset 0 is only emitted when the
	// line actually starts with a non-default style.
	if first := int(l.Spans[0].Start); first > 0 && first <= len(l.Text) {
		segs = append(segs, historySegment{T: l.Text[:first], S: 0})
	}
	for i, sp := range l.Spans {
		start := int(sp.Start)
		end := len(l.Text)
		if i+1 < len(l.Spans) {
			end = int(l.Spans[i+1].Start)
		}
		// Defensive clamping. These offsets come from another process; a malformed one must
		// produce a short segment, not a panic inside the server that owns every terminal.
		if start < 0 || start > len(l.Text) {
			continue
		}
		if end > len(l.Text) {
			end = len(l.Text)
		}
		if end <= start {
			continue
		}
		segs = append(segs, historySegment{T: l.Text[start:end], S: int(sp.Style)})
	}
	return segs
}

func linesJSON(lines []muxd.Line) []historyLineJSON {
	out := make([]historyLineJSON, len(lines))
	for i, l := range lines {
		out[i] = historyLineJSON{N: l.N, Seg: segmentsOf(l)}
	}
	return out
}

// defaultHistoryPage is how many lines one request returns when the caller does not say.
//
// 500 is about ten screens: enough that scrolling does not fetch on every wheel tick, small enough
// that one page is a few tens of kilobytes before compression. The daemon caps it harder anyway.
const defaultHistoryPage = 500

// handleSessionHistory serves GET /sessions/{id}/history.
//
//	?from=N     absolute line to start at (clamped to the oldest line still held)
//	?count=N    how many lines (default 500; the daemon caps it)
//	?screen=1   the live visible grid instead, numbered as if it continued the scrollback
//	?styles=N   how many style-table entries the client already has
//
// The response always carries `base` and `total` so a viewer can tell "you have reached the
// beginning" from "the beginning was evicted" — two states that look identical if you only report
// the lines.
func (s *Server) handleSessionHistory(w http.ResponseWriter, r *http.Request) {
	sess, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	q := r.URL.Query()
	req := muxd.HistoryReq{
		From:       atoi64(q.Get("from"), 0),
		Count:      int(atoi64(q.Get("count"), defaultHistoryPage)),
		Screen:     q.Get("screen") == "1",
		StylesFrom: int(atoi64(q.Get("styles"), 0)),
	}
	ack, err := sess.History(req)
	if err != nil {
		// A daemon older than this binary is not a server error and not a bug the user can act
		// on by retrying. Say what is true — this session has no history available — and let the
		// UI explain the one thing that would change it.
		if errors.Is(err, muxd.ErrNoScrollback) {
			writeJSON(w, http.StatusOK, map[string]any{
				"enabled": false,
				"reason":  "daemon-too-old",
				"lines":   []historyLineJSON{},
				"base":    0, "total": 0,
				"styles": []historyStyleJSON{}, "stylesTotal": 0,
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// No omitempty anywhere in this response: `enabled:false`, `broken:false`, `base:0` and
		// `total:0` are all real answers a viewer has to distinguish, and the encoder deleting
		// them is a defect this repository has already shipped once.
		"enabled":     ack.Enabled,
		"broken":      ack.Broken,
		"lines":       linesJSON(ack.Lines),
		"base":        ack.Base,
		"total":       ack.Total,
		"styles":      stylesJSON(ack.Styles),
		"stylesTotal": ack.StylesTotal,
	})
}

// handleSessionHistorySearch serves GET /sessions/{id}/history/search.
//
//	?q=TEXT       what to look for (required)
//	?from=N       where to start scanning
//	?back=1       scan towards older lines, newest match first
//	?limit=N      how many matches
//	?case=1       case-SENSITIVE (the default is insensitive, which is what people expect of a
//	              search box)
//
// The search runs in the daemon. Fetching the history to search it in the browser would spend
// exactly the bandwidth the line-based design exists to save.
func (s *Server) handleSessionHistorySearch(w http.ResponseWriter, r *http.Request) {
	sess, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	q := r.URL.Query()
	query := q.Get("q")
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
		return
	}
	ack, err := sess.SearchHistory(muxd.HistorySearchReq{
		Query:      query,
		From:       atoi64(q.Get("from"), 0),
		Backward:   q.Get("back") == "1",
		Limit:      int(atoi64(q.Get("limit"), 100)),
		IgnoreCase: q.Get("case") != "1",
	})
	if err != nil {
		if errors.Is(err, muxd.ErrNoScrollback) {
			writeJSON(w, http.StatusOK, map[string]any{
				"enabled": false, "reason": "daemon-too-old",
				"matches": []any{}, "base": 0, "total": 0,
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	matches := make([]map[string]any, len(ack.Matches))
	for i, m := range ack.Matches {
		// `col` is a BYTE offset internally. It is published as a rune index because the client
		// that receives it counts in JavaScript string units — the same reason spans became
		// segments. Converting here keeps the byte convention from ever leaving this process.
		matches[i] = map[string]any{"n": m.N, "col": runeIndexOf(m.Text, m.Col), "text": m.Text}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": ack.Enabled,
		"matches": matches,
		"base":    ack.Base,
		"total":   ack.Total,
	})
}

// runeIndexOf converts a byte offset into a rune index, clamped to the string.
func runeIndexOf(s string, byteOff int) int {
	if byteOff <= 0 {
		return 0
	}
	if byteOff > len(s) {
		byteOff = len(s)
	}
	n := 0
	for i := range s {
		if i >= byteOff {
			break
		}
		n++
	}
	return n
}

// atoi64 parses a query parameter, falling back to a default. A malformed number reads as the
// default rather than as an error: these are all "where am I scrolled to" hints, and refusing the
// whole request over a bad one would break scrolling to fix a typo in a URL.
func atoi64(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}
