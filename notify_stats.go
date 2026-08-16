package terminal

// Notification session rendering — turns the tmux/PTY sessions the notifier tracks
// into the host-agnostic notify.SessionRef list + Title the coordinator fans out.
// Three live categories (waiting / idle / running) are listed with their identity
// (tool + where); the title names the single session that triggered a batch, or
// falls back to a count summary when several finished at once. All pure + testable:
// the notifier fills the SessionSummary (by parsing transcripts) then calls these.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

// liveSession is one agent target the notifier tracks (a tmux pane or a PTY session), with
// its computed metrics.
type liveSession struct {
	// key is the target's identity within a batch (the notifier's tracking key). Empty for
	// a tmux-shaped value built without one, which then falls back to its coordinates.
	key string
	// location is a pre-rendered "where". Set for PTY sessions, which have no window/pane
	// coordinates to render; empty for tmux panes, which do.
	location   string
	tool       string
	session    string
	window     int
	windowName string
	pane       int
	status     agentintel.AgentStatus
}

// sessionLocation is the readable "where" of a session (no transcript uuid — the
// user finds it unreadable). e.g. "main · 窗口3 editor · 面板0".
//
// A PTY session brings its own: it has no window/pane coordinates, so its tab title is the
// entire address and rendering it as "title · 窗口0 · 面板0" would invent a topology that
// does not exist.
func sessionLocation(s liveSession) string {
	if s.location != "" {
		return sanitizeField(s.location)
	}
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

// paneKey is the stable identity of a target within a notification batch. The notifier's own
// key wins when present — two PTY tabs can share a title, and their tmux-shaped coordinates
// are both 0, so deriving identity from those would silently merge them into one 🆕 marker.
func paneKey(s liveSession) string {
	if s.key != "" {
		return s.key
	}
	return fmt.Sprintf("%s:%d:%d", s.session, s.window, s.pane)
}

// triggeredFirst stable-sorts the just-changed panes to the front of a list (they
// lead it and carry the 🆕 marker), preserving relative order otherwise.
func triggeredFirst(sessions []liveSession, trig map[string]bool) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return trig[paneKey(sessions[i])] && !trig[paneKey(sessions[j])]
	})
}

// buildNotifyEvent assembles the structured notify.Event the coordinator fans out.
// Header counts AND the session lists derive from the SAME `live` slice (single
// source — §12.2): every actionable session is represented (the renderer caps for
// length), the just-changed panes sorted first and marked 🆕. A single triggering
// session NAMES itself in the title (which pane, which tool, done or waiting — the
// user does not have to open the notification to know); several at once fall back
// to a count summary, which still fits a banner. The body lists show the full
// inventory either way.
func buildNotifyEvent(triggered, live []liveSession, deepURL string) notify.Event {
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
	switch {
	case len(triggered) == 1:
		title, kind = triggerTitle(triggered[0])
	case trigW > 0:
		title, kind = fmt.Sprintf("❓ %d 个会话需要回答", trigW), notify.KindWaiting
	case trigI > 0:
		title, kind = fmt.Sprintf("✅ %d 个会话已完成", trigI), notify.KindDone
	}
	return notify.Event{
		Title:    title,
		Kind:     kind,
		Counts:   counts,
		Sessions: sessions,
		DeepURL:  deepURL,
	}
}

// triggerTitle names the ONE session that woke this batch — terse enough for a
// lock-screen banner, which the user otherwise cannot tell apart from any other
// without opening the app first.
func triggerTitle(s liveSession) (string, notify.Kind) {
	ident := triggerIdentity(s)
	if s.status == agentintel.StatusWaiting {
		return "❓ " + ident + " 需要回答", notify.KindWaiting
	}
	return "✅ " + ident + " 已完成", notify.KindDone
}

// triggerIdentity is a shorter "where" than sessionLocation() — right-sized for a
// one-line title rather than a body row. Thin adapter over identityTag (see there for
// why this exists as its own shared function rather than being inlined here).
func triggerIdentity(s liveSession) string {
	return identityTag(s.tool, s.location, s.windowName, s.window)
}

// identityTag says which of the two session kinds this is, not just where: a tmux
// window and a plain (non-tmux) PTY tab look the same in a bare "tool·name" string,
// which is exactly the ambiguity that prompted this. Format: "tmux.<window-index>.
// <name>.<tool>" for a tmux pane, "tab.<title>.<tool>" for a tab. tmux's window index
// is the same number tmux itself shows in its status line — stable, user-visible. A
// PTY tab has no equivalent: its position in any session list is NOT stable (a closed
// or reordered tab reuses/shifts it — this repo already moved off index-based tab
// identity elsewhere for exactly that reason), so a tab is named without one rather
// than with a number that would lie on the next notification.
//
// SHARED across every notification title in this codebase (buildNotifyEvent's
// inferred running→idle/waiting transitions AND session_signal.go's explicit
// bell/OSC signals) on purpose: these two Title-building paths already drifted apart
// once — the bell path kept shipping a bare "🔔 需要你 · <title>" for a full round
// after the tick path grew this identity format — precisely because each had its own
// copy of the string-building logic. One function, one format, everywhere a
// notification names a session.
func identityTag(tool, location, windowName string, window int) string {
	if tool == "" {
		tool = "agent"
	}
	if location != "" { // PTY tab — no window/pane coordinates to show
		return "tab." + sanitizeField(location) + "." + tool
	}
	if wn := sanitizeField(windowName); wn != "" {
		return fmt.Sprintf("tmux.%d.%s.%s", window, wn, tool)
	}
	return fmt.Sprintf("tmux.%d.%s", window, tool)
}
