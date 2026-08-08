package agentintel

import "time"

// SurfaceUnit is the ONE description of a terminal surface unit's agent facts.
//
// ── What this replaces ───────────────────────────────────────────────────────────────────────
// deepwork-terminal shows the same kind of thing through two feeds: a tmux pane (tmux_state) and a
// non-tmux session card (sessions_overview). One model, two sources, one set of semantics — except
// that the third of those was held together by a REQUEST. Each payload hand-wrote the same field
// names and each carried a comment asking the other to please stay in step ("Field names mirror the
// tmux pane/window payload so the frontend can normalize both sources into ONE card model"). A
// comment cannot hold two structs equal, and it did not: `statusRule` shipped on one side for a
// while before the other, and the tail cap was two constants whose comment claimed they matched
// while one was 5× the other.
//
// Embedding this type is what turns that request into a fact. Add a surface fact here and BOTH
// payloads gain it, with the same key, the same tag and the same position, because there is only
// one declaration. Adding it to one payload alone is no longer something a careful reviewer has to
// catch — see the coverage assertions in surface_unit_test.go, which fail on any unit-semantic
// field that grows outside this struct.
//
// ── One representation, deliberately ─────────────────────────────────────────────────────────
// An earlier cut of this type carried three type parameters, because the two payloads had drifted
// in their Go spelling AND, for AwaitingSince, on the wire: the card sent a pre-formatted string
// that vanished when empty, the pane sent a time.Time that could not vanish (encoding/json's
// `omitempty` has no effect on a struct) and therefore shipped a literal "0001-01-01T00:00:00Z"
// sentinel on every pane that was not waiting. The parameters reproduced both shapes exactly,
// which is what a round with a frozen frontend needed — but parameterising a disagreement is not
// the same as ending it, and the sentinel was never a design, only what omitempty happened to do.
//
// `omitzero` (Go 1.24) is the thing that was missing: a zero time now omits its key, so BOTH feeds
// say "undated" the same way — by not saying anything. The frontend needed no change for this: its
// `isDatedSince` predicate already treats an absent value and the 0001-01-01 sentinel identically,
// so removing the sentinel is invisible to it. What it buys is that the next reader does not have
// to learn a magic prefix to understand "not waiting".
//
// ── What this still does NOT unify ───────────────────────────────────────────────────────────
// The STRUCTURE. tmux is a session→window→pane tree and the non-tmux feed is a flat card list; they
// describe genuinely different shapes and forcing them together would trade a solved problem for a
// harder one. `Tail` therefore stays outside: it hangs off the tmux WINDOW and off the non-tmux
// ENTRY — the same concept at two different levels of the tree, which cannot be one field here. Its
// LENGTH is already shared (OverviewTailLines), which is the part that could be.
type SurfaceUnit struct {
	// AgentTool / AgentStatus are the surface's agent identity and its current verdict. Both are
	// omitempty: a surface with no agent carries neither key rather than a pair of empty strings.
	//
	// The domain types, not bare strings, on both feeds. The card used to hold strings because the
	// deprecated host-supplied detector (Hooks.AgentDetect) hands back untyped values — but that is
	// an argument for converting at that ONE boundary, not for letting half the system describe an
	// agent with a type that cannot tell a tool from a status.
	AgentTool   AgentTool   `json:"agentTool,omitempty"`
	AgentStatus AgentStatus `json:"agentStatus,omitempty"`
	// AwaitingUser is the needs-you dot: the agent finished a turn or is blocked and has not been
	// answered. Distinct from AgentStatus==idle, which also covers a surface that never ran a turn.
	AwaitingUser bool `json:"awaitingUser,omitempty"`
	// AwaitingSince is the transcript time of the completion behind AwaitingUser, zero when there
	// is none. It is transcript-derived and therefore RELOAD-PROOF, which is what lets the frontend
	// key its per-surface "seen" dismissal on it: a cleared dot stays cleared across F5, and a new
	// turn brings it back.
	//
	// `omitzero`, not `omitempty` — the latter does nothing to a struct, which is exactly how the
	// 0001-01-01 sentinel got onto the wire in the first place.
	AwaitingSince time.Time `json:"awaitingSince,omitzero"`
	// EndedOnQuestion says the completed turn ended on a free-text question. It refines the SAME
	// dot's label ("有提问" vs "已完成") and never raises its severity — an agent at an empty prompt
	// is not blocked.
	EndedOnQuestion bool `json:"endedOnQuestion,omitempty"`
	// StatusRule is the single rule that produced this surface's verdict ("transcript.running",
	// "screen.approval", …), present on EVERY decision, green included.
	//
	// It used to be gated on the attention states, on the reasoning that a running surface accuses
	// nobody and so needs no defence. A surface stuck GREEN while its agent waits accuses nobody
	// and gets nobody's attention either — the failure you cannot notice — and it was undiagnosable
	// by construction, since five separate rules return Running and none left a mark. The rule is
	// stable while the status is, and both frames are diff-suppressed, so shipping it always costs
	// nothing between polls.
	//
	// StatusEvidence — the matched screen line, scrubbed and truncated — stays confined to
	// attention decisions: it changes every poll (spinner frames, token counters) and would defeat
	// that suppression for no diagnostic gain, since on a green surface the rule already says it
	// all. Callers enforce that gate; see StatusDecision.IsAttention.
	//
	// Diagnostic only — nothing renders them. They exist so a wrong dot can be TRACED instead of
	// re-argued. See status_decision.go.
	StatusRule     string `json:"statusRule,omitempty"`
	StatusEvidence string `json:"statusEvidence,omitempty"`
	// ActivityAt is when this surface's agent last WROTE to its transcript — not when the server
	// last looked. It is the age of the evidence behind AgentStatus, and it is shipped because a
	// status with no age cannot be sanity-checked by the person reading it.
	//
	// The bug that put it here: a pane read "running" for ten hours off a transcript nothing had
	// touched since the night before. Every layer was individually plausible, and the one fact that
	// would have made it obvious at a glance — "运行中 · 10 小时前" — was the one fact the UI did not
	// have.
	//
	// It lived on the tmux pane ALONE until now, which meant a non-tmux card could tell exactly the
	// same ten-hour lie with nothing to catch it. That asymmetry was not a decision; it was where
	// the feature happened to stop. Being in this struct is what makes "half of it" unavailable as
	// an option.
	ActivityAt time.Time `json:"activityAt,omitzero"`
}
