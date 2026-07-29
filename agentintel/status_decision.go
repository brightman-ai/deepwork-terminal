package agentintel

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/brightman-ai/kit/obs"
)

// Provenance of an attention decision — "which rule turned this pane red, and on what
// evidence".
//
// ── Why this exists ─────────────────────────────────────────────────────────────────────
// A waiting / needs-you verdict is an ACCUSATION: it tells the user an agent is blocked on
// them, paints the card red and (once a notify channel is on) rings their phone. When it is
// wrong there is currently nothing to go on afterwards — observed 2026-07-29, a Codex pane
// that was self-updating under `--yolo` (screen: "Updating Codex via `bun install -g
// @openai/codex`…") was reported as needing input. Reproducing it by feeding an APPROXIMATED
// screen back into AnalyzeOutput failed, which is the expected outcome: the status is
// recomputed from scratch every poll, the screen it was read from is long overwritten, and
// three unrelated rule families (transcript / screen / explicit signal) can all produce the
// same verdict. Nothing recorded which one did.
//
// So every decision now names the ONE rule that produced it and carries the exact line that
// matched. That turns "it went red and I cannot tell why" into a log line and a state field
// naming e.g. `screen.approval` plus the offending text. This file records the WHY only — it
// changes no heuristic, because a heuristic changed without being able to verify it against
// the real screen is a guess wearing a fix's clothes.
//
// ── Why the evidence is capped ──────────────────────────────────────────────────────────
// The matched line IS the user's screen. At most ONE line ever leaves this package, control
// characters scrubbed and truncated to evidenceMaxLen — enough to recognise which pattern
// fired, far too little to reconstruct a session out of the logs.

// evidenceMaxLen bounds the matched-line fragment carried in decisions (runes, not bytes —
// a screen line is frequently CJK).
const evidenceMaxLen = 120

// StatusRule names the single rule behind a status decision. The strings are stable and
// deliberately hierarchical (`family.rule`): they are grepped in logs months later and
// shipped inside state payloads, so they are part of the diagnostic contract, not debug text.
type StatusRule string

const (
	// RuleNone is "no decision recorded" (no agent, or a path that never classified).
	RuleNone StatusRule = ""

	// ── transcript family: the agent's own JSONL, the accurate source ────────────────
	// RuleTranscriptRunning — the transcript's last event is work in progress.
	RuleTranscriptRunning StatusRule = "transcript.running"
	// RuleTranscriptTurnEnd — the turn ENDED (Claude end_turn / Codex task_complete) and
	// the agent spoke last, i.e. your move. This is the ordinary "跑完了" needs-you.
	RuleTranscriptTurnEnd StatusRule = "transcript.turn_end"
	// RuleTranscriptIdle — idle, but not awaiting: a fresh agent that never ran a turn.
	RuleTranscriptIdle StatusRule = "transcript.idle"
	// RuleTranscriptElicitation — a pending AskUserQuestion / ExitPlanMode: the agent is
	// literally asking, and the CLI cannot proceed without an answer.
	RuleTranscriptElicitation StatusRule = "transcript.elicitation"
	// RuleTranscriptInterrupted — a tool result carried interrupted=true (a permission
	// denial / Esc), recorded by the driver rather than read off the screen.
	RuleTranscriptInterrupted StatusRule = "transcript.interrupted"
	// RuleTranscriptWaiting — the driver said waiting without a reason (defensive: a
	// future driver state that predates its rule name still logs as transcript-driven).
	RuleTranscriptWaiting StatusRule = "transcript.waiting"
	// RuleTranscriptWriting — no parse at all: the transcript file was written within
	// transcriptActiveWindow, so the agent is producing output (the mtime gate).
	RuleTranscriptWriting StatusRule = "transcript.writing"
	// RuleTranscriptUnlocatable — the transcript could not be located (a just-started
	// agent, or Codex before its rollout exists), so the screen decided alone.
	RuleTranscriptUnlocatable StatusRule = "transcript.unlocatable"

	// ── screen family: AnalyzeOutput on the visible terminal ─────────────────────────
	// A permission / selection prompt is terminal UI and appears in NO transcript, which
	// is why the screen is consulted at all. It is also the loosest source we have: the
	// patterns match ANY text on the last lines, with nothing tying the match to the
	// current conversation turn.
	// RuleScreenApproval — an approvalPattern matched ([Y/n], Allow, Approve…).
	RuleScreenApproval StatusRule = "screen.approval"
	// RuleScreenInteractive — an interactivePromptPattern matched ("do you want to",
	// "press enter to"…).
	RuleScreenInteractive StatusRule = "screen.interactive"
	// RuleScreenChoiceList — ≥2 numbered choices ("1. Yes…" / "2. No…").
	RuleScreenChoiceList StatusRule = "screen.choice_list"
	// RuleScreenSpinner — a spinner rune on the last line: positive proof of work, and
	// the only thing allowed to veto a transcript-derived idle back to running.
	RuleScreenSpinner StatusRule = "screen.spinner"
	// RuleScreenPromptIdle — a prompt character confirmed by a model-like line above it.
	RuleScreenPromptIdle StatusRule = "screen.prompt_idle"
	// RuleScreenPromptLikely — a prompt character with no confirming context.
	RuleScreenPromptLikely StatusRule = "screen.prompt_likely"
	// RuleScreenQuiet — the screen said nothing recognisable either way.
	RuleScreenQuiet StatusRule = "screen.quiet"

	// ── signal family: the program said so out loud (BEL / OSC), zero inference ──────
	RuleSignalBell   StatusRule = "signal.bell"
	RuleSignalNotify StatusRule = "signal.notify"
)

// StatusDecision is one resolved status WITH the rule that produced it.
//
// Awaiting is carried alongside Status because the two accuse differently: Status=waiting
// means the CLI is modal (red, blocked), while Awaiting on an idle pane means the turn
// finished and it is your move (amber, dismissable). Both can be wrong, so both are
// accounted for.
type StatusDecision struct {
	Status   AgentStatus
	Awaiting bool
	Rule     StatusRule
	// Evidence is the screen line that matched, already scrubbed and truncated. Empty for
	// transcript- and signal-driven rules: there is no screen line to blame.
	Evidence string
}

// IsAttention reports whether this decision ASKS SOMETHING OF THE USER — the decisions
// worth accounting for. Everything else (running, fresh idle) accuses nobody.
func (d StatusDecision) IsAttention() bool {
	return d.Status == StatusWaiting || d.Awaiting
}

// statusDecisionLogs coalesces the decision log. The pollers recompute status every 1–2s per
// connected client, so an uncoalesced line per decision would be pure flood; the coalescer
// emits IMMEDIATELY when the decision changes (the moment worth seeing) and at most once per
// window while it stays the same.
var statusDecisionLogs = obs.NewLogCoalescer(30 * time.Second)

// LogStatusDecision records an attention decision with its provenance.
//
// detector is which path decided ("tmux" / "session" / "watcher") — the same status is
// computed by more than one of them, and knowing which one fired is half the diagnosis.
// target is the human-locatable place ("main:2.0" for a pane, the session id for a PTY tab).
func LogStatusDecision(ctx context.Context, detector, target string, tool AgentTool, d StatusDecision) {
	key := detector + "|" + target
	fingerprint := string(tool) + "|" + string(d.Status) + "|" + strconv.FormatBool(d.Awaiting) + "|" + string(d.Rule)
	statusDecisionLogs.Info(ctx, Logger, key, fingerprint, "agent attention decision",
		"detector", detector,
		"target", target,
		"tool", string(tool),
		"status", string(d.Status),
		"awaiting", d.Awaiting,
		"rule", string(d.Rule),
		"evidence", d.Evidence)
}

// TranscriptStatusRule names WHICH transcript fact produced a driver-derived status. The
// drivers already distinguish them (WaitReason for a block, AwaitingUser for a completed
// turn); this gives each one a stable rule name so a log says "transcript.elicitation"
// rather than an unattributed "waiting".
//
// It lives here, exported, because BOTH detectors (the tmux poller and the per-session
// tracker) ask the same question of the same drivers — a second copy would be one more
// place for the two paths to drift apart, which is the failure mode this package keeps
// paying for. snap is the driver snapshot that produced status.
func TranscriptStatusRule(status AgentStatus, snap AgentState) StatusRule {
	switch status {
	case StatusRunning:
		return RuleTranscriptRunning
	case StatusWaiting:
		switch snap.WaitReason {
		case WaitQuestion:
			return RuleTranscriptElicitation
		case WaitPermission:
			return RuleTranscriptInterrupted
		default:
			return RuleTranscriptWaiting
		}
	case StatusIdle:
		if snap.AwaitingUser {
			return RuleTranscriptTurnEnd
		}
		return RuleTranscriptIdle
	}
	return RuleNone
}

// truncEvidence normalises one screen line into a fragment safe to log and to ship in a
// state payload: control characters (including the ANSI escapes a captured line is full of)
// become spaces, runs of whitespace collapse, and the result is capped at evidenceMaxLen.
func truncEvidence(line string) string {
	line = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, line)
	line = strings.Join(strings.Fields(line), " ")
	if r := []rune(line); len(r) > evidenceMaxLen {
		line = string(r[:evidenceMaxLen]) + "…"
	}
	return line
}
