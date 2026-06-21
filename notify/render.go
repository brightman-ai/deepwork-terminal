package notify

import (
	"fmt"
	"strings"
)

// listCap bounds how many sessions each group lists in a notification. The header
// counts stay TRUE (uncapped); only the displayed list is truncated, with a
// "…还有 N 个" tail. This is what lets us show ALL actionable sessions (the bug fix:
// idle list used to show only the just-triggered pane) without flooding.
const listCap = 5

// group is one status bucket rendered as a labelled list.
type group struct {
	status string
	header string
}

var groups = []group{
	{"waiting", "❓ 等待输入 (需回答)"},
	{"idle", "✅ 已完成 (在 pane,可继续)"},
	{"running", "▶ 运行中"},
}

// PlainText renders an Event to the plain-text BODY used by text-only channels
// (WeChat iLink) and as the web-push body. The Title is a SEPARATE notification
// field (the caller supplies it), so it is not repeated here. Header counts + the
// grouped, capped session lists derive from the SAME Event (single source — §12.2):
// Counts is the true tally, Sessions is the recency-sorted full list this caps.
func PlainText(e Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "❓%d 待回答 · ✅%d 完成 · ▶%d 运行", e.Counts.Waiting, e.Counts.Idle, e.Counts.Running)
	if e.Summary != "" {
		b.WriteString("\n")
		b.WriteString(e.Summary)
	}
	for _, g := range groups {
		writeGroup(&b, g.header, sessionsByStatus(e.Sessions, g.status))
	}
	return b.String()
}

// sessionsByStatus filters (preserving the caller's recency order).
func sessionsByStatus(sessions []SessionRef, status string) []SessionRef {
	out := make([]SessionRef, 0, len(sessions))
	for _, s := range sessions {
		if s.Status == status {
			out = append(out, s)
		}
	}
	return out
}

func writeGroup(b *strings.Builder, header string, sessions []SessionRef) {
	if len(sessions) == 0 {
		return
	}
	b.WriteString("\n\n")
	b.WriteString(header)
	b.WriteString(":")
	shown := sessions
	if len(shown) > listCap {
		shown = shown[:listCap]
	}
	for _, s := range shown {
		b.WriteString("\n")
		b.WriteString(SessionLine(s))
	}
	if extra := len(sessions) - len(shown); extra > 0 {
		fmt.Fprintf(b, "\n  …还有 %d 个", extra)
	}
}

// SessionLine renders one session as a plain-text list item (also reused by rich
// providers that fall back to text). 🆕 marks a session that just changed state.
func SessionLine(s SessionRef) string {
	tool := s.Tool
	if tool == "" {
		tool = "agent"
	}
	parts := []string{tool, s.Location, fmt.Sprintf("%d turn", s.Turns)}
	if s.Stats != "" {
		parts = append(parts, s.Stats)
	}
	prefix := "· "
	if s.JustChanged {
		prefix = "🆕 "
	}
	return prefix + strings.Join(parts, " · ")
}
