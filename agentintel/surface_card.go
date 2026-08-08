package agentintel

// SurfaceCard is ONE Agent-Overview card, whatever produced it.
//
// ── The layering mismatch this ends ──────────────────────────────────────────────────────────
// The two feeds describe the same card at two different depths:
//
//	tmux:  server → session → window → [pane, pane, …]    card = window, unit = pane
//	PTY:   manager → session                              card = session, unit = session
//
// SurfaceUnit already made a UNIT mean one thing on both sides. It could not make a CARD mean one
// thing, because on the tmux side a card is not a unit — it is several. So every consumer flattened
// the tree itself: the frontend had windowRawStatus / windowAwaiting / windowAwaitingSince /
// windowActivityAt / windowTool / windowCwd, six accessors implementing "what does this card say"
// for one source, while the other source got the same six answers by reading its single unit's
// fields directly. Two implementations of one rule, in the language furthest from the data, and the
// second one existed only because N=1 makes the rule look like an assignment.
//
// A card therefore carries its OWN surface facts, and RollUp below is the single definition of what
// they are. For a card with one unit that is an identity (asserted, not assumed — see
// TestRollUp_OneUnitIsIdentity): **N=1 is not a special case, it is the plain case**, which is the
// whole reason the tmux card and the session card can now be read by the same code.
//
// ── What is deliberately NOT in here ─────────────────────────────────────────────────────────
// IDENTITY. A card's key, its number, its title and whether you are looking at it stay declared by
// each payload, and that is not an oversight to be tidied up later:
//
//   - a tmux window's INDEX is a fact of the tmux server (`#{window_index}`, the prefix+N target),
//     while a PTY card's number is the position of its TAB — something only the browser knows, and
//     which the server would have to invent.
//   - likewise ACTIVE: tmux publishes which window is current; for a PTY card "active" means "the
//     tab you are looking at", which is per-client and changes without the server hearing about it.
//
// A shared type with fields one source structurally cannot fill is not unification, it is a lie
// with a compiler behind it. The frontend passes those four in explicitly (see cardToUnit), so the
// asymmetry is visible at the seam instead of hidden inside a half-empty struct.
//
// Tail, by contrast, IS a card fact on both sides and now says so: it used to hang off
// TmuxWindowState and off SessionOverviewEntry as two separate declarations whose LENGTH was shared
// (OverviewTailLines) but whose existence was not — which is exactly the shape that let one of them
// be 8 while a comment claimed it matched the other's 40.
type SurfaceCard struct {
	// The card's own surface facts. For a card with one unit these ARE that unit's facts; for a
	// tmux window they are RollUp of its panes. Embedded, so a card and a unit answer "what is the
	// agent doing here" with the same field names, in the same order, on the wire.
	SurfaceUnit
	// Tail is the last few lines of REAL output (agent chrome stripped server-side), capped at
	// OverviewTailLines. Absent when there is nothing to show or capture failed — a card then says
	// so rather than rendering blank padding.
	//
	// Card-level on both sides, because a tail belongs to what you LOOK at: a tmux window's tail is
	// its active pane's, not a per-pane collection, and nobody has ever wanted four tails stacked
	// in one card.
	Tail []string `json:"tail,omitempty"`
}

// RollUp is the ONE definition of what a card says when it is made of several units.
//
// `active` is the index of the unit you are focused on within the card (a tmux window's active
// pane), or -1 when there is none. It is a tiebreaker, never an override: a background pane that
// is WAITING still turns the whole card red, because the card's job is to tell you something needs
// you — hiding that behind "but you were looking at the other pane" is how a blocked agent goes
// unnoticed in a split.
//
// The rules, and why each is the one it is:
//
//   - STATUS: any unit waiting → waiting; else any running → running; else the active unit's, else
//     the first unit that has one. Severity wins, in the order the dot colours already imply.
//   - RULE / EVIDENCE come from the unit that DECIDED the status, not from the active one. "Why is
//     this card green" must be answerable, and answering it with a bystander's rule is worse than
//     not answering: it sends the next reader to the wrong pane.
//   - TOOL follows the ACTIVE unit (falling back to the first unit that has one), because the
//     engine badge names what you are looking at. This is the one field where focus beats severity,
//     and it is not a contradiction: a badge is an identity, not an alarm.
//   - AWAITING is any unit's, and the completion it reports (AwaitingSince + EndedOnQuestion) comes
//     from the first unit that carries a DATED one — that timestamp is the key the client's "seen"
//     layer dismisses against, so it has to be a real instant belonging to a real completion, not
//     a merge of several.
//   - ACTIVITY is the newest across all units: a card with any unit still writing is not stale.
//
// A zero-unit card gets the zero unit, which is the honest description of a window whose panes
// have all gone.
func RollUp(units []SurfaceUnit, active int) SurfaceUnit {
	if len(units) == 0 {
		return SurfaceUnit{}
	}
	if active < 0 || active >= len(units) {
		active = -1
	}

	// The DECIDER: the unit whose verdict the card reports. Severity first, focus only as a
	// tiebreaker among units that said nothing alarming.
	decider := firstWith(units, func(u SurfaceUnit) bool { return u.AgentStatus == StatusWaiting })
	if decider < 0 {
		decider = firstWith(units, func(u SurfaceUnit) bool { return u.AgentStatus == StatusRunning })
	}
	if decider < 0 && active >= 0 && units[active].AgentStatus != "" {
		decider = active
	}
	if decider < 0 {
		decider = firstWith(units, func(u SurfaceUnit) bool { return u.AgentStatus != "" })
	}
	if decider < 0 {
		// No unit has a status at all. Fall through to the focused one anyway rather than to the
		// zero value: a unit can carry a rule without a status — an explicit BEL on a bare shell is
		// exactly that — and dropping it here would make RollUp lose information a single-unit card
		// had, which is precisely the identity this type promises to preserve.
		decider = max(active, 0)
	}

	out := SurfaceUnit{
		AgentStatus:    units[decider].AgentStatus,
		StatusRule:     units[decider].StatusRule,
		StatusEvidence: units[decider].StatusEvidence,
	}

	if active >= 0 && units[active].AgentTool != ToolNone {
		out.AgentTool = units[active].AgentTool
	} else if i := firstWith(units, func(u SurfaceUnit) bool { return u.AgentTool != ToolNone }); i >= 0 {
		out.AgentTool = units[i].AgentTool
	}

	for _, u := range units {
		if u.AwaitingUser {
			out.AwaitingUser = true
			break
		}
	}
	if i := firstWith(units, func(u SurfaceUnit) bool {
		return u.AwaitingUser && !u.AwaitingSince.IsZero()
	}); i >= 0 {
		out.AwaitingSince = units[i].AwaitingSince
		out.EndedOnQuestion = units[i].EndedOnQuestion
	}

	for _, u := range units {
		if u.ActivityAt.After(out.ActivityAt) {
			out.ActivityAt = u.ActivityAt
		}
	}
	return out
}

// firstWith is index-of-first-match, -1 for none. A named helper because every rule above is one,
// and spelling each out inline is how two of them end up subtly different.
func firstWith(units []SurfaceUnit, pred func(SurfaceUnit) bool) int {
	for i, u := range units {
		if pred(u) {
			return i
		}
	}
	return -1
}
