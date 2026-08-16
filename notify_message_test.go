package terminal

import (
	"strings"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

// describeSession/buildNotifyBody moved into the notify package as SessionLine /
// PlainText; their behavior (incl. the ③ all-idle list fix) is covered by
// notify/notify_test.go. Here we keep the terminal-side session-ref conversion +
// event/title assembly.

// identityTag is shared by every notification-title-building path in this codebase
// (buildNotifyEvent's inferred transitions AND session_signal.go's explicit bell/OSC
// signals) specifically because those two paths already drifted apart once.
func TestIdentityTagTmuxNamed(t *testing.T) {
	if got := identityTag("codex", "", "bun", 6); got != "tmux.6.bun.codex" {
		t.Fatalf("got %q", got)
	}
}

func TestIdentityTagTmuxUnnamed(t *testing.T) {
	if got := identityTag("claude", "", "", 2); got != "tmux.2.claude" {
		t.Fatalf("got %q", got)
	}
}

func TestIdentityTagTab(t *testing.T) {
	if got := identityTag("agent", "stwork", "", 0); got != "tab.stwork.agent" {
		t.Fatalf("got %q", got)
	}
}

func TestIdentityTagEmptyToolFallsBackToAgent(t *testing.T) {
	if got := identityTag("", "stwork", "", 0); got != "tab.stwork.agent" {
		t.Fatalf("got %q", got)
	}
}

func TestSessionRefConversion(t *testing.T) {
	s := liveSession{
		tool: "claude", session: "main", window: 3, windowName: "editor", pane: 0,
		status: agentintel.StatusIdle,
	}
	ref := sessionRef(s, true)
	if ref.Location != "main · 窗口3 editor · 面板0" {
		t.Fatalf("location: %q", ref.Location)
	}
	if ref.Status != "idle" || !ref.JustChanged {
		t.Fatalf("ref fields: %+v", ref)
	}
}

// buildNotifyEvent: header counts + the session list derive from the SAME `live`
// slice (single source — §12.2); the title leads with what triggered the batch.
func TestBuildNotifyEvent(t *testing.T) {
	var live []liveSession
	for i := 0; i < 7; i++ {
		live = append(live, liveSession{tool: "claude", session: "main", window: i, status: agentintel.StatusIdle})
	}
	triggered := []liveSession{live[3]} // only one just finished → woke the notification

	e := buildNotifyEvent(triggered, live, "/?session=main#bootstrap=x")

	if e.Counts.Idle != 7 {
		t.Fatalf("counts should reflect ALL live idle: %+v", e.Counts)
	}
	if len(e.Sessions) != 7 {
		t.Fatalf("event should carry all 7 sessions (renderer caps), got %d", len(e.Sessions))
	}
	if !e.Sessions[0].JustChanged {
		t.Fatalf("triggered pane should sort first and be marked changed")
	}
	changed := 0
	for _, s := range e.Sessions {
		if s.JustChanged {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("exactly one session just changed, got %d", changed)
	}
	// A single trigger names itself (kind.coords.tool), not just a bare count.
	if e.Title != "✅ tmux.3.claude 已完成" || e.Kind != notify.KindDone {
		t.Fatalf("title/kind should name the completed trigger: %q %v", e.Title, e.Kind)
	}
	// The rendered plain-text body never leaks a URL.
	body := notify.PlainText(e)
	for _, frag := range []string{"http://", "https://", "#bootstrap="} {
		if strings.Contains(body, frag) {
			t.Fatalf("body must not contain %q:\n%s", frag, body)
		}
	}
}

// A tmux window names the "tmux." kind explicitly, its (stable, user-visible) window
// index, its name when set, and the tool — so tmux vs. tab is never ambiguous.
func TestBuildNotifyEventSingleTriggerTmuxNamesKindIndexNameTool(t *testing.T) {
	live := []liveSession{{tool: "codex", session: "main", window: 6, windowName: "bun", pane: 1, status: agentintel.StatusIdle}}
	e := buildNotifyEvent(live, live, "")
	if e.Title != "✅ tmux.6.bun.codex 已完成" {
		t.Fatalf("title should be tmux.<idx>.<name>.<tool>, got %q", e.Title)
	}
}

// An unnamed tmux window still gets "tmux.<idx>.<tool>" — never a blank name segment.
func TestBuildNotifyEventSingleTriggerTmuxUnnamedWindow(t *testing.T) {
	live := []liveSession{{tool: "claude", session: "main", window: 2, status: agentintel.StatusIdle}}
	e := buildNotifyEvent(live, live, "")
	if e.Title != "✅ tmux.2.claude 已完成" {
		t.Fatalf("title should be tmux.<idx>.<tool> when unnamed, got %q", e.Title)
	}
}

// A PTY session has no window/pane coordinates, and (unlike tmux) no stable index to
// show — its pre-rendered location names it instead: "tab.<title>.<tool>".
func TestBuildNotifyEventSingleTriggerPTYUsesTabKind(t *testing.T) {
	live := []liveSession{{tool: "agent", location: "stwork", status: agentintel.StatusWaiting}}
	e := buildNotifyEvent(live, live, "")
	if e.Title != "❓ tab.stwork.agent 需要回答" {
		t.Fatalf("title should be tab.<location>.<tool>, got %q", e.Title)
	}
	if e.Kind != notify.KindWaiting {
		t.Fatalf("kind should be waiting, got %v", e.Kind)
	}
}

// Several sessions finishing in the same coalesced batch can't each name themselves
// in one line — this stays the count summary the banner already had.
func TestBuildNotifyEventMultiTriggerStaysSummary(t *testing.T) {
	live := []liveSession{
		{tool: "claude", session: "main", window: 1, status: agentintel.StatusWaiting},
		{tool: "codex", session: "main", window: 2, status: agentintel.StatusWaiting},
	}
	e := buildNotifyEvent(live, live, "")
	if e.Title != "❓ 2 个会话需要回答" || e.Kind != notify.KindWaiting {
		t.Fatalf("multi-trigger title should stay a count summary, got %q %v", e.Title, e.Kind)
	}
}

func TestSanitizeField(t *testing.T) {
	got := sanitizeField("evil\nname\twith\rcontrol")
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("must strip control chars, got %q", got)
	}
	long := sanitizeField(strings.Repeat("x", 200))
	if len([]rune(long)) > 50 {
		t.Fatalf("must truncate, got %d runes", len([]rune(long)))
	}
}
