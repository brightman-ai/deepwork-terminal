package agentintel

import (
	"strings"
	"testing"
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
