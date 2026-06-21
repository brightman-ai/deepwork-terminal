package terminal

// Notification statistics body — aggregates the per-session metrics (turns /
// tokens / cost) that the agentintel transcript parser already computes, scoped to
// the tmux pane sessions the notifier tracks. Three live categories (waiting /
// idle / running) are listed with per-session stats; archived (closed-pane)
// sessions are shown as a COUNT only. All pure + testable: the notifier fills the
// SessionSummary (by parsing transcripts) then calls these.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

// tokens is the 4-way token split the user asked for: in / out / cache-create / cache-read.
type tokens struct{ in, out, cc, cr int }

func (t tokens) add(o tokens) tokens { return tokens{t.in + o.in, t.out + o.out, t.cc + o.cc, t.cr + o.cr} }
func (t tokens) isZero() bool        { return t == tokens{} }

func summaryTokens(s agentintel.SessionSummary) tokens {
	return tokens{s.InputTokens, s.OutputTokens, s.CacheCreateTokens, s.CacheReadTokens}
}

// liveSession is one tmux pane the notifier tracks, with its computed metrics.
type liveSession struct {
	tool       string
	session    string
	window     int
	windowName string
	pane       int
	status     agentintel.AgentStatus
	summary    agentintel.SessionSummary
	activeToday bool // transcript modified today
}

// statBlock is a rolled-up metrics window (the since-last-notification delta or "today").
type statBlock struct {
	sessions int
	turns    int
	tok      tokens
	cost     float64
	hasCost  bool
}

func (b *statBlock) addSummary(s agentintel.SessionSummary) {
	b.sessions++
	b.turns += s.TurnCount
	b.tok = b.tok.add(summaryTokens(s))
	if s.TotalCost != nil {
		b.cost += *s.TotalCost
		b.hasCost = true
	}
}

// computeToday sums the full metrics of live sessions active today plus archived
// sessions closed today. archivedToday = the summaries of closed-pane sessions.
func computeToday(live []liveSession, archivedToday []agentintel.SessionSummary) statBlock {
	var b statBlock
	for _, s := range live {
		if s.activeToday {
			b.addSummary(s.summary)
		}
	}
	for _, s := range archivedToday {
		b.addSummary(s)
	}
	return b
}

// computeDelta sums (current − baseline) over live sessions, keyed by a stable key
// (transcript path) so a session is diffed against its own prior snapshot. Only
// sessions with positive activity contribute.
func computeDelta(live map[string]agentintel.SessionSummary, baseline map[string]agentintel.SessionSummary) statBlock {
	var b statBlock
	for key, cur := range live {
		base := baseline[key] // zero value when newly seen
		d := statBlock{}
		dTurns := cur.TurnCount - base.TurnCount
		dTok := tokens{
			cur.InputTokens - base.InputTokens,
			cur.OutputTokens - base.OutputTokens,
			cur.CacheCreateTokens - base.CacheCreateTokens,
			cur.CacheReadTokens - base.CacheReadTokens,
		}
		if dTurns <= 0 && dTok.isZero() {
			continue // no activity since last notification
		}
		d.sessions = 1
		if dTurns > 0 {
			d.turns = dTurns
		}
		d.tok = dTok
		if cur.TotalCost != nil {
			dc := *cur.TotalCost
			if base.TotalCost != nil {
				dc -= *base.TotalCost
			}
			if dc > 0 {
				d.cost = dc
				d.hasCost = true
			}
		}
		b.sessions += d.sessions
		b.turns += d.turns
		b.tok = b.tok.add(d.tok)
		b.cost += d.cost
		b.hasCost = b.hasCost || d.hasCost
	}
	return b
}

// ── formatters ──────────────────────────────────────────────────────────────

// humN renders a token count compactly: 880 → "880", 21345 → "21k", 1_200_000 → "1.2M".
func humN(n int) string {
	switch {
	case n < 0:
		return "0"
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

func fmtTok(t tokens) string {
	return fmt.Sprintf("in %s out %s cc %s cr %s", humN(t.in), humN(t.out), humN(t.cc), humN(t.cr))
}

func fmtCost(cost float64, has bool) string {
	if !has {
		return ""
	}
	return fmt.Sprintf("~$%.2f", cost)
}

// sessionLocation is the readable "where" of a session (no transcript uuid — the
// user finds it unreadable). e.g. "main · 窗口3 editor · 面板0".
func sessionLocation(s liveSession) string {
	var b strings.Builder
	b.WriteString(sanitizeField(s.session))
	if wn := sanitizeField(s.windowName); wn != "" {
		fmt.Fprintf(&b, " · 窗口%d %s", s.window, wn)
	} else {
		fmt.Fprintf(&b, " · 窗口%d", s.window)
	}
	fmt.Fprintf(&b, " · 面板%d", s.pane)
	return b.String()
}

// sessionRef converts an internal liveSession to the host-agnostic notify.SessionRef
// (semantic data; the per-provider renderer formats it — plain line vs rich card).
func sessionRef(s liveSession, justChanged bool) notify.SessionRef {
	return notify.SessionRef{
		Tool:        s.tool,
		Location:    sessionLocation(s),
		Status:      statusString(s.status),
		JustChanged: justChanged,
		Turns:       s.summary.TurnCount,
		Stats:       sessionStats(s),
	}
}

func statusString(st agentintel.AgentStatus) string {
	switch st {
	case agentintel.StatusWaiting:
		return "waiting"
	case agentintel.StatusIdle:
		return "idle"
	case agentintel.StatusRunning:
		return "running"
	default:
		return ""
	}
}

// sessionStats is the compact per-session metrics tail ("in 45k out 12k cc … ~$x").
func sessionStats(s liveSession) string {
	stats := fmtTok(summaryTokens(s.summary))
	if c := fmtCost(deref(s.summary.TotalCost), s.summary.TotalCost != nil); c != "" {
		stats += " · " + c
	}
	return stats
}

// paneKey is the stable identity of a pane within a notification batch.
func paneKey(s liveSession) string {
	return fmt.Sprintf("%s:%d:%d", s.session, s.window, s.pane)
}

// triggeredFirst stable-sorts the just-changed panes to the front of a list (they
// lead it and carry the 🆕 marker), preserving relative order otherwise.
func triggeredFirst(sessions []liveSession, trig map[string]bool) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return trig[paneKey(sessions[i])] && !trig[paneKey(sessions[j])]
	})
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func fmtBlock(b statBlock) string {
	s := fmt.Sprintf("%d 会话 · %d turn · %s", b.sessions, b.turns, fmtTok(b.tok))
	if c := fmtCost(b.cost, b.hasCost); c != "" {
		s += " · " + c
	}
	return s
}

func humDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d秒前", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d分钟前", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d小时前", int(d.Hours()))
	}
}

// buildNotifyEvent assembles the structured notify.Event the coordinator fans out.
// Header counts AND the session lists derive from the SAME `live` slice (single
// source — §12.2): every actionable session is represented (the renderer caps for
// length), the just-changed panes sorted first and marked 🆕. The title leads with
// what TRIGGERED this batch (what woke you); the body lists show the full inventory.
func buildNotifyEvent(triggered, live []liveSession, summary, deepURL string) notify.Event {
	trig := map[string]bool{}
	for _, s := range triggered {
		trig[paneKey(s)] = true
	}
	// Sort the whole live slice triggered-first so, after the renderer filters each
	// status group, the just-changed panes lead their group.
	sorted := append([]liveSession(nil), live...)
	triggeredFirst(sorted, trig)

	var counts notify.Counts
	sessions := make([]notify.SessionRef, 0, len(sorted))
	for _, s := range sorted {
		switch s.status {
		case agentintel.StatusWaiting:
			counts.Waiting++
		case agentintel.StatusIdle:
			counts.Idle++
		case agentintel.StatusRunning:
			counts.Running++
		}
		sessions = append(sessions, sessionRef(s, trig[paneKey(s)]))
	}

	var trigW, trigI int
	for _, s := range triggered {
		switch s.status {
		case agentintel.StatusWaiting:
			trigW++
		case agentintel.StatusIdle:
			trigI++
		}
	}
	title, kind := "⏳ 通知", notify.KindInfo
	if trigW > 0 {
		title, kind = fmt.Sprintf("❓ %d 个会话需要回答", trigW), notify.KindWaiting
	} else if trigI > 0 {
		title, kind = fmt.Sprintf("✅ %d 个会话已完成", trigI), notify.KindDone
	}
	return notify.Event{
		Title:    title,
		Kind:     kind,
		Counts:   counts,
		Sessions: sessions,
		Summary:  summary,
		DeepURL:  deepURL,
	}
}

// buildSummaryBlocks renders the "📊 自上次通知 / 📅 今日快报" stat blocks that head the
// notification body (the per-session lists follow, rendered by notify.PlainText).
func buildSummaryBlocks(delta, today statBlock, archivedSinceNotif, archivedToday int, sinceLast time.Duration, hasBaseline bool) string {
	var b strings.Builder
	if hasBaseline {
		fmt.Fprintf(&b, "📊 自上次通知 (%s)\n   活跃 %s", humDur(sinceLast), fmtBlock(delta))
		if archivedSinceNotif > 0 {
			fmt.Fprintf(&b, "\n   本次 🗄️%d 会话已关闭归档", archivedSinceNotif)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "📅 今日快报\n   %s", fmtBlockToday(today, archivedToday))
	return b.String()
}

// fmtBlockToday adds the live/archived session split when there are archived sessions.
func fmtBlockToday(b statBlock, archived int) string {
	if archived > 0 {
		liveN := b.sessions - archived
		if liveN < 0 {
			liveN = 0
		}
		s := fmt.Sprintf("%d 会话(%d 活跃 + 🗄️%d 归档) · %d turn · %s", b.sessions, liveN, archived, b.turns, fmtTok(b.tok))
		if c := fmtCost(b.cost, b.hasCost); c != "" {
			s += " · " + c
		}
		return s
	}
	return fmtBlock(b)
}

