package agentintel

// One decision, both sources.
//
// ── What this replaces ───────────────────────────────────────────────────────────────────────
// "Which agent state is this surface in" was implemented twice: TmuxStateService.paneDecision for
// panes, sessionAgentTracker.State for PTY sessions. Both read the same transcript monitor, both
// confirmed a running verdict against the same screen analyser, and both carried a comment saying
// they must not drift from each other.
//
// They had drifted, in three places, and every one of them was invisible from either side alone:
//
//   - The SPINNER VETO (a transcript-idle surface whose screen is visibly still working gets
//     vetoed back to Running) existed only for panes. The same agent, in a plain terminal, read
//     "idle" while it was demonstrably running.
//   - RuleTranscriptUnlocatable ("no transcript found — assuming busy") vs RuleTranscriptWriting
//     ("the transcript is being written") was distinguished only for panes. A PTY session with no
//     locatable transcript claimed a file was being written when there was no file — sending
//     whoever followed that rule to look in a place that does not exist. A rule whose entire
//     purpose is to be traceable had become a wrong pointer.
//   - EndedOnQuestion was gated on a dated AwaitingSince for sessions and ungated for panes.
//
// None of these are subtle bugs in one implementation. They are what two implementations DO.
//
// ── The one real difference, made into a parameter ───────────────────────────────────────────
// The sources differ in exactly one way: where the screen comes from. A pane's screen must be
// captured from tmux (a subprocess, fallible); a PTY session's is already in memory, replayed for
// the card's tail. That difference is this file's only parameter, and everything else is shared.
type screenFunc = func() (lines []string, ok bool)

// SurfaceProbe is everything needed to decide ONE surface unit.
type SurfaceProbe struct {
	// Key is the stable identity the transcript binding is cached under — a pane's PID, a
	// session's id. It must never be reused across two different surfaces, or one inherits the
	// other's transcript.
	Key string
	// CWD is where the agent is working; the transcript locator's starting point.
	CWD  string
	Tool AgentTool
	// ProcessPID is the agent process itself (not the shell), used for the PID→transcript
	// lookups that make binding a lookup rather than a guess.
	ProcessPID int
	// Screen reads this unit's visible screen.
	//
	// `ok=false` means COULD NOT READ, which is not the same as "read it, it was empty" — that
	// distinction is I1 (「观察不到 ≠ 不存在」) at this layer, and it is in the signature rather
	// than in a comment because both callers previously got it wrong in opposite directions: the
	// pane path treated a failed capture as "assume running" under a rule that named the wrong
	// cause, and the session path treated an absent screen as evidence of idleness.
	//
	// Called at most once per decision, memoised below. The pane path used to capture TWICE for
	// a single verdict (once to disambiguate transcripts, once to read the prompt); it now pays
	// for one, and a session pays for none.
	Screen screenFunc
}

// memoScreen makes Screen callable freely inside the decision without paying twice.
func memoScreen(f screenFunc) screenFunc {
	var (
		lines []string
		ok    bool
		done  bool
	)
	return func() ([]string, bool) {
		if !done {
			done = true
			if f != nil {
				lines, ok = f()
			}
		}
		return lines, ok
	}
}

// DecideSurface resolves one surface unit's complete agent facts.
//
// It returns the SurfaceUnit that goes on the wire AND the decision behind it: the unit carries
// the rule for diagnosis, the decision carries `Awaiting` and IsAttention() for the log. Two
// values rather than one because the log must not be reconstructed from the payload — that is
// how a log starts disagreeing with the thing it describes.
//
// The caller supplies Tool: detection (which process is an agent) is a different question, asked
// against a process tree that only the caller has. A ToolNone probe returns a zero unit.
func (m *PaneAgentMonitor) DecideSurface(p SurfaceProbe) (SurfaceUnit, StatusDecision) {
	if m == nil || p.Tool == ToolNone || p.Key == "" {
		return SurfaceUnit{}, StatusDecision{}
	}
	screen := memoScreen(p.Screen)
	decision := m.decideStatus(p, screen)

	unit := SurfaceUnit{
		AgentTool:      p.Tool,
		AgentStatus:    decision.Status,
		StatusRule:     string(decision.Rule),
	}
	// EVIDENCE only on the decisions that ask something of the user. It is a live screen line —
	// it churns on every poll (spinner frames, token counters) and would defeat both frames' diff
	// suppression for no diagnostic gain, since on a green surface the rule already says it all.
	if decision.IsAttention() {
		unit.StatusEvidence = decision.Evidence
	}

	snap := m.Snapshot(p.Key)
	// Needs-you: an explicit block always counts; an idle surface counts only when the driver
	// says a turn actually COMPLETED (not a fresh agent that never ran one).
	unit.AwaitingUser = decision.Status == StatusWaiting ||
		(decision.Status == StatusIdle && snap.AwaitingUser)
	// The completion's transcript time — reload-proof, which is what lets the frontend's "seen"
	// layer dismiss against it. Undated waits (a PTY-only permission prompt; every Codex wait)
	// carry no time at all, and EndedOnQuestion rides with it: a question we cannot date is a
	// question we cannot tell apart from the next one.
	if unit.AwaitingUser && !snap.AwaitingSince.IsZero() {
		unit.AwaitingSince = snap.AwaitingSince.UTC()
		unit.EndedOnQuestion = snap.EndedOnQuestion
	}
	// How old the evidence is. Read AFTER the status resolution above, which is what binds this
	// surface to a transcript this cycle — cache-only, so it costs one stat and never a scan.
	unit.ActivityAt = m.TranscriptWrittenAt(p.Key)

	decision.Awaiting = unit.AwaitingUser
	return unit, decision
}

// decideStatus is the verdict itself, split out so the fact-assembly above reads as one thing.
func (m *PaneAgentMonitor) decideStatus(p SurfaceProbe, screen screenFunc) StatusDecision {
	// Accurate JSONL-derived status: a turn's end is recorded in the transcript (Claude end_turn
	// / Codex task_complete), a Bash/Read tool executing means running. This is what fixed the
	// mtime heuristic's blind spots — a just-written ask card looked "running", a silently
	// running long tool looked "idle".
	//
	// The screen closure is handed to the monitor for ONE purpose: settling which transcript a
	// claude surface owns when its PID lookup missed and two or more files in this cwd are still
	// unclaimed. It is invoked only then, so the common case captures nothing.
	if p.Tool == ToolClaude || p.Tool == ToolCodex {
		tail := func() []string { lines, _ := screen(); return lines }
		if st, ok := m.StatusWithTail(p.Key, p.CWD, p.Tool, tail, p.ProcessPID); ok {
			switch st {
			case StatusRunning:
				// A pending tool may instead be blocked on a permission [Y/n]. That prompt is
				// terminal UI — it never reaches the transcript — so the screen is the only place
				// it exists, and only this direction is checked: a screen can prove "blocked", it
				// cannot prove "not blocked".
				if v, ok := analyzeScreen(screen); ok && v.State == PromptNeedsPermission {
					return StatusDecision{Status: StatusWaiting, Rule: v.Rule, Evidence: v.Line}
				}
				return StatusDecision{Status: StatusRunning, Rule: RuleTranscriptRunning}
			case StatusWaiting:
				return StatusDecision{Status: StatusWaiting,
					Rule: TranscriptStatusRule(StatusWaiting, m.Snapshot(p.Key))}
			case StatusIdle:
				// A transcript-idle can be STALE within one poll: an agent that hit a turn
				// boundary and immediately kept going (auto-continue, queued prompt, next-turn
				// thinking) is still working before its next line lands. The surface's own screen
				// is the tiebreaker — a live spinner can only render where work is actually
				// happening — so a visibly-spinning idle is vetoed back to Running.
				//
				// Gated on Active() so a long-settled surface, which is not spinning anyway, never
				// pays for the read. Only a POSITIVE spinner overrides: an idle or unreadable
				// screen trusts the transcript, so a genuinely finished turn still reads Idle and
				// still fires its "done" notification.
				if m.Active(p.Key, p.CWD, p.Tool, p.ProcessPID) {
					if v, ok := analyzeScreen(screen); ok && v.State == PromptRunning {
						return StatusDecision{Status: StatusRunning, Rule: v.Rule, Evidence: v.Line}
					}
				}
				return StatusDecision{Status: StatusIdle,
					Rule: TranscriptStatusRule(StatusIdle, m.Snapshot(p.Key))}
			}
		}
	}

	// No transcript opinion available. Active() answers true for two DIFFERENT reasons and only
	// one of them is evidence: the transcript was written recently, OR it could not be located at
	// all (in which case it assumes busy rather than misreading a starting agent's screen as an
	// idle prompt). Reporting both as「transcript.writing」claims a file is being written when
	// there is no file, which sends the next reader looking in the wrong place — the exact defect
	// the rule exists to prevent.
	if m.Active(p.Key, p.CWD, p.Tool, p.ProcessPID) {
		if !m.Located(p.Key) {
			return StatusDecision{Status: StatusRunning, Rule: RuleTranscriptUnlocatable}
		}
		return StatusDecision{Status: StatusRunning, Rule: RuleTranscriptWriting}
	}

	// The screen alone decides. This is the one arm where it can declare Waiting with no
	// transcript to confirm against, which is exactly why its verdict carries the matched line.
	v, ok := analyzeScreen(screen)
	if !ok {
		// Could not read it. That is not evidence of idleness, and calling it idle would fire a
		// "finished" notification for a surface we cannot see. Running is the conservative
		// reading, and the rule says plainly that the screen — not the transcript — is what is
		// missing.
		return StatusDecision{Status: StatusRunning, Rule: RuleScreenUnreadable}
	}
	switch v.State {
	case PromptNeedsPermission:
		return StatusDecision{Status: StatusWaiting, Rule: v.Rule, Evidence: v.Line}
	case PromptRunning:
		return StatusDecision{Status: StatusRunning, Rule: v.Rule, Evidence: v.Line}
	default:
		return StatusDecision{Status: StatusIdle, Rule: v.Rule, Evidence: v.Line}
	}
}

// analyzeScreen classifies the screen, propagating "could not read it" instead of letting an
// unread screen look like an empty one.
func analyzeScreen(screen screenFunc) (OutputVerdict, bool) {
	lines, ok := screen()
	if !ok {
		return OutputVerdict{State: PromptUnknown, Rule: RuleNone}, false
	}
	return AnalyzeOutputDetail(lines), true
}
