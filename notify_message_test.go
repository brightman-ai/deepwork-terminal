package terminal

import (
	"strings"
	"testing"
)

func TestDescribeEvent(t *testing.T) {
	cases := []struct {
		name       string
		ev         notifyEvent
		transcript string
		want       string
	}{
		{
			name:       "full tmux pane (cli in body)",
			ev:         notifyEvent{tool: "claude", session: "main", window: 1, windowName: "editor", pane: 0},
			transcript: "abc123.jsonl",
			want:       "claude · main · 窗口1 editor · 面板0 · abc123.jsonl",
		},
		{
			name: "no window name / no transcript",
			ev:   notifyEvent{tool: "codex", session: "main", window: 2, pane: 3},
			want: "codex · main · 窗口2 · 面板3",
		},
		{
			name:       "plain terminal tab (session = tab name)",
			ev:         notifyEvent{tool: "claude", session: "终端 2", window: 0, pane: 0},
			transcript: "sess.jsonl",
			want:       "claude · 终端 2 · 窗口0 · 面板0 · sess.jsonl",
		},
		{
			name: "missing tool falls back to agent",
			ev:   notifyEvent{session: "x", window: 0, pane: 0},
			want: "agent · x · 窗口0 · 面板0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := describeEvent(c.ev, c.transcript); got != c.want {
				t.Fatalf("describeEvent:\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// User-controllable fields (window name, session, transcript) must never inject
// newlines / control chars or blow up the length — they go straight into WeChat /
// Web Push bodies.
func TestDescribeEventSanitizes(t *testing.T) {
	ev := notifyEvent{
		tool:       "claude",
		session:    "main",
		window:     1,
		windowName: "evil\nname\twith\rcontrol",
		pane:       0,
	}
	got := describeEvent(ev, strings.Repeat("x", 200))
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("description must be single-line, got %q", got)
	}
	if strings.Contains(got, "evil\nname") {
		t.Fatalf("newlines in window name not sanitized: %q", got)
	}
	if len([]rune(got)) > 200 {
		t.Fatalf("over-long transcript not truncated: %d runes", len([]rune(got)))
	}
}

// The notification body must never contain a URL (the user's explicit ask).
func TestDescribeEventHasNoURL(t *testing.T) {
	got := describeEvent(notifyEvent{tool: "claude", session: "main", window: 1, pane: 0}, "t.jsonl")
	for _, frag := range []string{"http://", "https://", "/?session=", "#bootstrap="} {
		if strings.Contains(got, frag) {
			t.Fatalf("description must not contain %q, got %q", frag, got)
		}
	}
}
