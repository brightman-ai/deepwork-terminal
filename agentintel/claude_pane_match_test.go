package agentintel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTranscriptWithText creates a transcript whose last assistant turn says `text`.
func writeTranscriptWithText(t *testing.T, cwd, sessionID, text string, modAgo time.Duration) string {
	t.Helper()
	dir := NewProjectLocator().ClaudeProjectDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"type": "user", "timestamp": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)},
		{
			"type":      "assistant",
			"timestamp": time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339Nano),
			"message": map[string]any{
				"role":        "assistant",
				"stop_reason": "end_turn",
				"content":     []any{map[string]any{"type": "text", "text": text}},
			},
		},
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	mod := time.Now().Add(-modAgo)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

// renderedPane fakes what the CLI actually puts on screen: the message hard-wrapped at the
// terminal width and indented, markdown emphasis already turned into ANSI (so the asterisks are
// GONE), box-drawing rules, an empty prompt, and a status line truncated with an ellipsis. Every
// one of those is a way the screen differs from the transcript, which is the whole reason
// normalization exists.
func renderedPane(lines ...string) []string {
	out := []string{"\x1b[2m  ⎿ +66 lines\x1b[0m"}
	for _, l := range lines {
		out = append(out, "  "+l)
	}
	return append(out,
		"───────────────────────────────",
		"❯ ",
		"───────────────────────────────",
		"  \x1b[1m🤖 Opus 5\x1b[0m | 💰 $13.11 | 🧠 216.8K(22%) | fast✗ | e: …",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← fo…",
	)
}

// TestNormalizeForMatch pins the one property everything else rests on: the same sentence,
// wrapped and decorated by the renderer, must reduce to what the transcript holds.
func TestNormalizeForMatch(t *testing.T) {
	source := "构建完成，**新代码确认在二进制里**（`speakerSignalIsUsable=1`）。"
	onScreen := "\x1b[1m  构建完成，新代码确认在二进\x1b[0m\n  制里（speakerSignalIsUsable=1）。"

	if normalizeForMatch(source) != normalizeForMatch(onScreen) {
		t.Fatalf("wrapping + markdown + ANSI must normalize away:\n  src=%q\n  scr=%q",
			normalizeForMatch(source), normalizeForMatch(onScreen))
	}
	if normalizeForMatch("── ❯ ⎿ … ⏵⏵ ") != "" {
		t.Fatal("pure chrome must reduce to nothing, or it would match every pane")
	}
}

// TestMatchPaneToTranscript is the tiebreak's contract. It answers ONLY when the answer is
// unambiguous; every other outcome is "" so the caller keeps the behaviour it had before.
func TestMatchPaneToTranscript(t *testing.T) {
	claudeHomeFixture(t)
	cwd := "/tmp/dw-panematch"
	mine := writeTranscriptWithText(t, cwd, "sess-mine",
		"CP0→CP6 全部完成，终态达成。收口 oracle 全部亲跑，go build 绿、测试全绿。", 30*time.Minute)
	theirs := writeTranscriptWithText(t, cwd, "sess-theirs",
		"构建完成，新代码确认在二进制里，Voicemore 正在跑，install_app.sh 会拒绝安装。", 0)

	t.Run("picks the session whose output is on this screen", func(t *testing.T) {
		pane := renderedPane("CP0→CP6 全部完成，终态达成。收口 oracle 全部亲",
			"跑，go build 绿、测试全绿。")
		if got := matchPaneToTranscript(pane, []string{theirs, mine}); got != mine {
			t.Fatalf("got %q, want %q — the newest file is NOT the one on screen", got, mine)
		}
	})

	t.Run("refuses when two sessions both claim the screen", func(t *testing.T) {
		pane := renderedPane(
			"CP0→CP6 全部完成，终态达成。收口 oracle 全部亲跑，go build 绿、测试全绿。",
			"构建完成，新代码确认在二进制里，Voicemore 正在跑，install_app.sh 会拒绝安装。")
		if got := matchPaneToTranscript(pane, []string{theirs, mine}); got != "" {
			t.Fatalf("got %q, want \"\" — evidence that fits both candidates separates neither", got)
		}
	})

	t.Run("refuses when nothing matches", func(t *testing.T) {
		if got := matchPaneToTranscript(renderedPane("完全无关的一段文字，和任何会话都对不上"),
			[]string{theirs, mine}); got != "" {
			t.Fatalf("got %q, want \"\"", got)
		}
	})

	t.Run("refuses on a blank screen", func(t *testing.T) {
		if got := matchPaneToTranscript([]string{"❯ ", "───────"}, []string{theirs, mine}); got != "" {
			t.Fatalf("got %q, want \"\" — chrome alone is not evidence", got)
		}
	})

	t.Run("a too-short turn may not decide", func(t *testing.T) {
		short := writeTranscriptWithText(t, cwd, "sess-short", "好的", 0)
		if got := matchPaneToTranscript(renderedPane("好的"), []string{short}); got != "" {
			t.Fatalf("got %q, want \"\" — 「好的」 appears in every session; a coincidence here is a wrong binding", got)
		}
	})
}

// TestPaneLocate_ContentBeatsMtimeWhenIdentityMisses is the end-to-end shape of the ladder:
// identity missed (no session record), two candidates are free, and the NEWEST one is the wrong
// one. Before the tiebreak this pane took the newest and inherited a stranger's session.
func TestPaneLocate_ContentBeatsMtimeWhenIdentityMisses(t *testing.T) {
	claudeHomeFixture(t) // no sessions/<pid>.json → identity misses by construction
	cwd := "/tmp/dw-panematch-locate"
	mine := writeTranscriptWithText(t, cwd, "sess-mine",
		"这一轮把探针接上了，先量后改，during 阶段没有退化。", 30*time.Minute)
	writeTranscriptWithText(t, cwd, "sess-newest", "另一个会话在说完全不同的事情，和这个 pane 无关。", 0)

	m := NewPaneAgentMonitor(NewProjectLocator())
	tail := func() []string {
		return renderedPane("这一轮把探针接上了，先量后改，during 阶段没", "有退化。")
	}

	if got := m.locate(cwd, ToolClaude, 7001, nil, tail); got != mine {
		t.Fatalf("bound %q, want %q — the pane's own screen outranks whichever file is newest", got, mine)
	}
}

// TestPaneLocate_TailNotConsultedWithoutAmbiguity is the performance contract: reading a pane
// costs a tmux command on a single-threaded server that the USER is also using. With one
// candidate there is nothing to settle, so nothing may be captured.
func TestPaneLocate_TailNotConsultedWithoutAmbiguity(t *testing.T) {
	claudeHomeFixture(t)
	cwd := "/tmp/dw-panematch-single"
	only := writeTranscriptWithText(t, cwd, "sess-only", "只有一个候选，没什么可消歧的。", 0)

	m := NewPaneAgentMonitor(NewProjectLocator())
	captured := 0
	tail := func() []string { captured++; return nil }

	if got := m.locate(cwd, ToolClaude, 7002, nil, tail); got != only {
		t.Fatalf("bound %q, want %q", got, only)
	}
	if captured != 0 {
		t.Fatalf("captured the pane %d time(s) with nothing to disambiguate — that cost must be zero", captured)
	}
}
