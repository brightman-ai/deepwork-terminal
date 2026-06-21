package terminal

import (
	"strings"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

func costPtr(v float64) *float64 { return &v }

func TestHumN(t *testing.T) {
	cases := map[int]string{0: "0", 880: "880", 12000: "12k", 21345: "21k", 200000: "200k", 1_200_000: "1.2M", 3_000_000: "3.0M"}
	for in, want := range cases {
		if got := humN(in); got != want {
			t.Errorf("humN(%d) = %q, want %q", in, got, want)
		}
	}
}

// describeSession/buildNotifyBody moved into the notify package as SessionLine /
// PlainText; their behavior (incl. the ③ all-idle list fix) is covered by
// notify/notify_test.go. Here we keep the terminal-side stat math + conversion.

func TestSessionRefConversion(t *testing.T) {
	s := liveSession{
		tool: "claude", session: "main", window: 3, windowName: "editor", pane: 0,
		status: agentintel.StatusIdle,
		summary: agentintel.SessionSummary{
			TurnCount: 12, InputTokens: 45000, OutputTokens: 12000,
			CacheCreateTokens: 200000, CacheReadTokens: 3_000_000, TotalCost: costPtr(0.88),
		},
	}
	ref := sessionRef(s, true)
	if ref.Location != "main · 窗口3 editor · 面板0" {
		t.Fatalf("location: %q", ref.Location)
	}
	if ref.Status != "idle" || !ref.JustChanged || ref.Turns != 12 {
		t.Fatalf("ref fields: %+v", ref)
	}
	if ref.Stats != "in 45k out 12k cc 200k cr 3.0M · ~$0.88" {
		t.Fatalf("stats: %q", ref.Stats)
	}
}

func TestComputeDelta(t *testing.T) {
	base := map[string]agentintel.SessionSummary{
		"a.jsonl": {TurnCount: 5, InputTokens: 1000, TotalCost: costPtr(0.10)},
	}
	live := map[string]agentintel.SessionSummary{
		"a.jsonl": {TurnCount: 8, InputTokens: 4000, TotalCost: costPtr(0.30)}, // +3 turn, +3k in, +0.20
		"b.jsonl": {TurnCount: 2, OutputTokens: 500},                            // new session, +2 turn
		"c.jsonl": {TurnCount: 9, InputTokens: 9}, // unchanged vs baseline (no baseline) but has activity
	}
	d := computeDelta(live, base)
	if d.sessions != 3 {
		t.Fatalf("delta sessions: want 3, got %d", d.sessions)
	}
	if d.turns != 3+2+9 {
		t.Fatalf("delta turns: want 14, got %d", d.turns)
	}
	if d.tok.in != 3000+9 {
		t.Fatalf("delta in-tokens: want 3009, got %d", d.tok.in)
	}
}

func TestComputeDeltaSkipsNoActivity(t *testing.T) {
	base := map[string]agentintel.SessionSummary{"a.jsonl": {TurnCount: 5, InputTokens: 1000}}
	live := map[string]agentintel.SessionSummary{"a.jsonl": {TurnCount: 5, InputTokens: 1000}} // identical
	d := computeDelta(live, base)
	if d.sessions != 0 || d.turns != 0 {
		t.Fatalf("unchanged session must not contribute, got %+v", d)
	}
}

// buildNotifyEvent: header counts + the session list derive from the SAME `live`
// slice (single source — §12.2); the title leads with what triggered the batch.
func TestBuildNotifyEvent(t *testing.T) {
	sum := agentintel.SessionSummary{TurnCount: 1}
	var live []liveSession
	for i := 0; i < 7; i++ {
		live = append(live, liveSession{tool: "claude", session: "main", window: i, status: agentintel.StatusIdle, summary: sum})
	}
	triggered := []liveSession{live[3]} // only one just finished → woke the notification

	e := buildNotifyEvent(triggered, live, "", "/?session=main#bootstrap=x")

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
	if !strings.Contains(e.Title, "已完成") || e.Kind != notify.KindDone {
		t.Fatalf("title/kind should reflect the completed trigger: %q %v", e.Title, e.Kind)
	}
	// The rendered plain-text body never leaks a URL.
	body := notify.PlainText(e)
	for _, frag := range []string{"http://", "https://", "#bootstrap="} {
		if strings.Contains(body, frag) {
			t.Fatalf("body must not contain %q:\n%s", frag, body)
		}
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
