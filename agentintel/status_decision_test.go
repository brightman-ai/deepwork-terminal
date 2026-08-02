package agentintel

import (
	"strings"
	"testing"
	"time"
)

// The point of the provenance layer is that a WRONG verdict can be traced afterwards. These
// pin the two properties that makes possible: every attention verdict names exactly one rule
// and carries the line that produced it, and no more of the user's screen than that.

func TestAnalyzeOutputDetail_NamesTheRuleThatFired(t *testing.T) {
	cases := []struct {
		name     string
		lines    []string
		want     PromptState
		wantRule StatusRule
		evidence string // substring the recorded line must contain ("" = evidence not required)
	}{
		{
			name:     "approval pattern",
			lines:    []string{"Edit file config.go?", "Do you approve? [Y/n]"},
			want:     PromptNeedsPermission,
			wantRule: RuleScreenApproval,
			evidence: "[Y/n]",
		},
		{
			// The loosest rule we have: an unanchored word match, on any of the last 5 lines,
			// with nothing tying it to the current turn. Pinned so its blast radius is visible.
			name:     "bare Allow anywhere in the last lines",
			lines:    []string{"npm warn Allow scripts to run", "installing…"},
			want:     PromptNeedsPermission,
			wantRule: RuleScreenApproval,
			evidence: "Allow",
		},
		{
			name:     "interactive prompt",
			lines:    []string{"Updating index", "Press Enter to continue"},
			want:     PromptNeedsPermission,
			wantRule: RuleScreenInteractive,
			evidence: "Press Enter to continue",
		},
		{
			name:     "numbered choice list",
			lines:    []string{"❯ 1. Yes, auto-accept edits", "  2. Yes, manually approve", "  3. No"},
			want:     PromptNeedsPermission,
			wantRule: RuleScreenChoiceList,
			evidence: "1. Yes",
		},
		{
			name:     "spinner",
			lines:    []string{"working", "⠹ thinking…"},
			want:     PromptRunning,
			wantRule: RuleScreenSpinner,
			evidence: "thinking",
		},
		{
			name:     "prompt confirmed by a model line",
			lines:    []string{"claude-4.6 ready", "❯ "},
			want:     PromptIdle,
			wantRule: RuleScreenPromptIdle,
		},
		{
			name:     "bare prompt",
			lines:    []string{"", "$ "},
			want:     PromptLikelyIdle,
			wantRule: RuleScreenPromptLikely,
		},
		{
			name:     "nothing recognisable",
			lines:    []string{"build finished in 3.2s"},
			want:     PromptUnknown,
			wantRule: RuleScreenQuiet,
		},
		{
			name:     "empty screen",
			lines:    nil,
			want:     PromptUnknown,
			wantRule: RuleScreenQuiet,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AnalyzeOutputDetail(c.lines)
			if got.State != c.want {
				t.Fatalf("state = %v, want %v", got.State, c.want)
			}
			if got.Rule != c.wantRule {
				t.Fatalf("rule = %q, want %q — an unattributed verdict cannot be diagnosed later", got.Rule, c.wantRule)
			}
			if c.evidence != "" && !strings.Contains(got.Line, c.evidence) {
				t.Fatalf("evidence = %q, want it to contain %q", got.Line, c.evidence)
			}
			// The wrapper must stay exactly equivalent: every existing caller reads it.
			if plain := AnalyzeOutput(c.lines); plain != got.State {
				t.Fatalf("AnalyzeOutput = %v but AnalyzeOutputDetail = %v — the wrapper drifted", plain, got.State)
			}
		})
	}
}

// TestEvidenceIsBounded is the privacy ceiling: the matched line is the user's screen, so
// exactly one line, scrubbed and truncated, may leave this package.
func TestEvidenceIsBounded(t *testing.T) {
	long := "Allow " + strings.Repeat("秘密", 400)
	v := AnalyzeOutputDetail([]string{long})
	if v.Rule != RuleScreenApproval {
		t.Fatalf("rule = %q, want the approval rule", v.Rule)
	}
	if n := len([]rune(v.Line)); n > evidenceMaxLen+1 { // +1 for the ellipsis
		t.Fatalf("evidence kept %d runes — the log would carry the user's screen", n)
	}

	scrubbed := truncEvidence("a\x1b[31mb\tc\nd  e")
	if strings.ContainsAny(scrubbed, "\x1b\t\n") {
		t.Fatalf("control characters survived: %q", scrubbed)
	}
	if scrubbed != "a [31mb c d e" {
		t.Fatalf("scrubbed = %q", scrubbed)
	}
}

func TestTranscriptStatusRule(t *testing.T) {
	cases := []struct {
		status AgentStatus
		snap   AgentState
		want   StatusRule
	}{
		{StatusRunning, AgentState{}, RuleTranscriptRunning},
		{StatusWaiting, AgentState{WaitReason: WaitQuestion}, RuleTranscriptElicitation},
		{StatusWaiting, AgentState{WaitReason: WaitPermission}, RuleTranscriptInterrupted},
		{StatusWaiting, AgentState{}, RuleTranscriptWaiting},
		{StatusIdle, AgentState{AwaitingUser: true}, RuleTranscriptTurnEnd},
		{StatusIdle, AgentState{}, RuleTranscriptIdle},
		{StatusDone, AgentState{}, RuleNone},
	}
	for _, c := range cases {
		if got := TranscriptStatusRule(c.status, c.snap); got != c.want {
			t.Errorf("TranscriptStatusRule(%s, %+v) = %q, want %q", c.status, c.snap, got, c.want)
		}
	}
}

func TestStatusDecisionIsAttention(t *testing.T) {
	// Only decisions that ASK something of the user are accounted for; a running agent
	// accuses nobody, so instrumenting it would be pure noise.
	if !(StatusDecision{Status: StatusWaiting}).IsAttention() {
		t.Fatal("a blocked agent is an attention decision")
	}
	if !(StatusDecision{Status: StatusIdle, Awaiting: true}).IsAttention() {
		t.Fatal("a finished turn nobody has answered is an attention decision")
	}
	if (StatusDecision{Status: StatusRunning}).IsAttention() {
		t.Fatal("a running agent must not be logged as needing the user")
	}
	if (StatusDecision{Status: StatusIdle}).IsAttention() {
		t.Fatal("a fresh idle agent has not asked for anything")
	}
}

// ── The reported defect: a tall AskUserQuestion read as "running" ───────────────────────────
//
// Claude Code's AskUserQuestion gained a PREVIEW panel, which pushes the option list far up the
// screen: above the preview box, "Notes: press n to add notes", the "Chat about this" line, the
// input box and the bottom chrome. Measured on the reported pane: 18 lines above the bottom.
//
// Two caps then hid it — the pane capture fetched 14 lines, and the analyzer only scanned the
// last 5 of those — so the "confirm against the pane" veto in paneDecision never saw a prompt
// and the pane stayed GREEN while Claude waited for an answer.
func askUserQuestionScreen() []string {
	return []string{
		`  □ 重排范围`,
		``,
		`  MCP 重写时加了一个新框，没被手拖过的那些框要不要跟着重新排？`,
		``,
		`❯ 1. 只安置新框，其余一律不动          ┌──────────────────────────────┐`,
		`     （推荐）                          │ MCP 加了 node "gpt2"          │`,
		`  2. 没 pin 的全重排                   │                              │`,
		`     只保留手拖过的                    │ 前          后               │`,
		`  3. 模板说了算：按模板分开定          │ ┌A┐ ┌B┐    ┌A┐ ┌B┐          │`,
		`                                       │ ┌C┐        ┌C┐ ┌gpt2┐        │`,
		`                                       │ A/B/C 坐标逐像素不变          │`,
		`                                       └──────────────────────────────┘`,
		``,
		`        Notes: press n to add notes`,
		``,
		`  Chat about this`,
		`──────────────────────────────────────────────────────────────────`,
		`❯ `,
		`──────────────────────────────────────────────────────────────────`,
		`  🗓 Opus 5 | 💰 $668.52 | 🌐 157.6K(16%) | main`,
		`  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
	}
}

func TestAnalyzeOutput_TallAskUserQuestionIsWaiting(t *testing.T) {
	v := AnalyzeOutputDetail(askUserQuestionScreen())
	if v.State != PromptNeedsPermission {
		t.Fatalf("a menu awaiting an answer must read as needs-permission, got %v (rule %q)", v.State, v.Rule)
	}
	if v.Rule != RuleScreenChoiceList {
		t.Errorf("rule = %q, want %q", v.Rule, RuleScreenChoiceList)
	}
}

// The capture bound is part of the answer, not an implementation detail: paneScanLines must be
// large enough to CONTAIN the menu, or widening the analyzer's window buys nothing.
func TestPaneScanLinesCoversATallMenu(t *testing.T) {
	screen := askUserQuestionScreen()
	captured := tailLines(screen, paneScanLines) // same helper the pane path uses
	if v := AnalyzeOutputDetail(captured); v.State != PromptNeedsPermission {
		t.Fatalf("paneScanLines=%d truncates the menu away: got %v (rule %q)", paneScanLines, v.State, v.Rule)
	}
}

// ── "不要乱误": the wider window must not turn ordinary output into a fake "waiting" ─────────
func TestAnalyzeOutput_PrintedListIsNotAMenu(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{"a plan the agent just wrote", []string{
			`I'll do this in three steps:`, ``,
			`1. Read the analyzer and find the window`,
			`2. Widen it, but only for menus`,
			`3. Add a regression test`, ``,
			`Starting now.`, `⠹ working…`,
		}},
		{"a markdown blockquote with a numbered list", []string{
			`The docs say:`, ``,
			`> 1. Install the binary`,
			`> 2. Run it with --help`, ``,
			`Done reading.`,
		}},
		{"numbered items with no cursor anywhere", []string{
			`Findings:`,
			`  1. the cap was 8`,
			`  2. the comment claimed otherwise`,
			`  3. both are fixed`,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v := AnalyzeOutputDetail(c.lines); v.State == PromptNeedsPermission {
				t.Fatalf("printed list misread as a live menu (rule %q, line %q)", v.Rule, v.Line)
			}
		})
	}
}

// And the one that must NOT regress: a real menu is still caught when it sits right above the
// input box (the old plan-mode shape the 5-line window was calibrated for).
func TestAnalyzeOutput_BottomAnchoredMenuStillWaiting(t *testing.T) {
	v := AnalyzeOutputDetail([]string{
		`Ready to code?`, `❯ 1. Yes, auto-accept edits`, `  2. Yes, manually approve`, `  3. No, keep planning`,
	})
	if v.State != PromptNeedsPermission {
		t.Fatalf("classic plan-mode menu regressed: %v (rule %q)", v.State, v.Rule)
	}
}

// ── A completed turn stops asking for attention once it is no longer news ────────────────────
//
// Observed live: opening the workbench showed amber "finished, unread" dots for panes whose turns
// had ended 3.2 and 3.3 DAYS earlier, sitting next to genuine ones from 0.9h and 2.2h ago. The
// old dots were not lying — nobody had looked — but a permanent amber dot devalues the fresh ones
// beside it, which is the only way this signal can fail.
func TestExpireStaleAwaiting(t *testing.T) {
	now := time.Now()
	mk := func(status AgentStatus, age time.Duration, ended bool) AgentState {
		return AgentState{
			Status:          status,
			AwaitingUser:    true,
			EndedOnQuestion: ended,
			AwaitingSince:   now.Add(-age),
		}
	}

	t.Run("fresh completion keeps its dot", func(t *testing.T) {
		for _, age := range []time.Duration{time.Minute, 2 * time.Hour, 23 * time.Hour} {
			if got := ExpireStaleAwaiting(mk(StatusIdle, age, false), now); !got.AwaitingUser {
				t.Errorf("age %s: dot withdrawn too early", age)
			}
		}
	})

	t.Run("three-day-old completion is no longer news", func(t *testing.T) {
		got := ExpireStaleAwaiting(mk(StatusIdle, 3*24*time.Hour, true), now)
		if got.AwaitingUser {
			t.Error("a 3-day-old completion must stop claiming attention")
		}
		if got.EndedOnQuestion {
			t.Error("EndedOnQuestion labelled the withdrawn dot; it must not outlive it")
		}
		if got.AwaitingSince.IsZero() {
			t.Error("the completion TIME is still a fact — only the claim expires")
		}
	})

	// A blocked agent is blocked no matter how long: nothing resolved itself while you were away,
	// and the moment you look there is something to do. Age says a completion is stale; it says
	// nothing about a block.
	t.Run("a blocked agent never expires", func(t *testing.T) {
		got := ExpireStaleAwaiting(mk(StatusWaiting, 30*24*time.Hour, false), now)
		if !got.AwaitingUser {
			t.Error("StatusWaiting must survive any age — it is still blocked")
		}
	})

	t.Run("an undatable completion is left alone", func(t *testing.T) {
		s := AgentState{Status: StatusIdle, AwaitingUser: true} // zero AwaitingSince
		if got := ExpireStaleAwaiting(s, now); !got.AwaitingUser {
			t.Error("no timestamp is not evidence of an OLD timestamp")
		}
	})
}
