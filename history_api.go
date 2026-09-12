package terminal

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

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

// tmuxCaptureCap bounds how deep one capture-pane may reach into tmux's history buffer.
// tmux's own default history-size is 2000; 20000 covers anyone who raised it, at a payload the
// local link shrugs at (only fetched when the user opens the copy view).
const tmuxCaptureCap = 20000

// 打开复制模式的头两秒要串行走三个请求（探测/尾页/当前屏），每个都真抓一次 capture 的话，
// 冷启动就是 6 次 capture + 往返——Human 实测"卡 3 秒才进得去"。缓存 3 秒：同一次进入的
// 三个请求共享一次 capture；3 秒后的下一次进入再真抓（新输出不会缺席太久）。
type tmuxCaptureEntry struct {
	at              time.Time
	history, screen []string
}

var tmuxCaptureCache = struct {
	sync.Mutex
	m map[int]tmuxCaptureEntry
}{m: map[int]tmuxCaptureEntry{}}

const tmuxCaptureTTL = 3 * time.Second

func capturePaneForShellCached(ctx context.Context, cap tmuxPaneCapture, shellPID int) ([]string, []string, error) {
	tmuxCaptureCache.Lock()
	entry, ok := tmuxCaptureCache.m[shellPID]
	tmuxCaptureCache.Unlock()
	if ok && time.Since(entry.at) < tmuxCaptureTTL {
		return entry.history, entry.screen, nil
	}
	history, screen, err := cap.CapturePaneForShell(ctx, shellPID, tmuxCaptureCap)
	if err != nil {
		return nil, nil, err
	}
	tmuxCaptureCache.Lock()
	tmuxCaptureCache.m[shellPID] = tmuxCaptureEntry{at: time.Now(), history: history, screen: screen}
	// 顺手防胀：会话关了缓存条目留着也只是几 MB 的口子，但还是只留最近用过的那些。
	if len(tmuxCaptureCache.m) > 64 {
		for pid, e := range tmuxCaptureCache.m {
			if time.Since(e.at) > time.Minute {
				delete(tmuxCaptureCache.m, pid)
			}
		}
	}
	tmuxCaptureCache.Unlock()
	return history, screen, nil
}

// tmuxPaneCapture is the optional capability the tmux provider carries when it can read pane
// history for a shell. Kept as a narrow interface assertion instead of widening TmuxStateProvider
// for every implementor.
type tmuxPaneCapture interface {
	CapturePaneForShell(ctx context.Context, shellPID, historyCap int) (history []string, screen []string, err error)
}

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

	// source=tmux —— tmux 标签的长程回看源（2026-09-12）。tmux 原地重绘、把长历史存在自己的
	// buffer 里，daemon 的行 scrollback 对 tmux 会话几乎是空的；capture-pane（经 tmux socket
	// 只读，不碰用户 pane）是拿到它的正路。行号 1..N 顺序铺满，screen 接在后面；无样式（v1
	// 不带 -e，纯文本复制已满足需求）。enable=false 的失败会如实说明，让 UI 有话可说。
	if q.Get("source") == "tmux" {
		if cap, ok := s.tmuxProvider.(tmuxPaneCapture); ok {
			history, screen, cerr := capturePaneForShellCached(r.Context(), cap, sess.ShellPID())
			if cerr != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"enabled": false,
					"reason":  "tmux-capture: " + cerr.Error(),
					"lines":   []historyLineJSON{},
					"base":    0, "total": 0,
					"styles": []historyStyleJSON{}, "stylesTotal": 0,
				})
				return
			}
			lines := make([]historyLineJSON, 0, len(history)+len(screen))
			n := 0
			for _, l := range history {
				n++
				lines = append(lines, historyLineJSON{N: int64(n), Seg: []historySegment{{T: l}}})
			}
			total := n
			for _, l := range screen {
				n++
				lines = append(lines, historyLineJSON{N: int64(n), Seg: []historySegment{{T: l}}})
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"enabled": true, "broken": false,
				"lines": lines, "base": 0, "total": total,
				"styles":      []historyStyleJSON{{}},
				"stylesTotal": 1,
			})
			return
		}
	}
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
