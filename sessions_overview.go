package terminal

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/ansisignal"
)

// Non-tmux Agent Overview feed.
//
// The tmux overview gets its cards from the pushed `tmux_state` frame, which describes the WHOLE
// tmux topology (every window's status + live tail) even though it rides on the single attached
// session's WebSocket. A user WITHOUT tmux has no such stream — each terminal tab is its own PTY
// session and only the ACTIVE one has a WS at all — so before this the non-tmux overview could
// only show what the tab strip already showed, making it strictly worse than the strip.
//
// This is the exact structural twin: one `sessions_overview` frame describing EVERY session,
// pushed on the active session's existing WS by the same 1s ticker, diff-suppressed. That keeps
// the house rules intact (no frontend polling, ZERO additional connections — [Ref: TH-0501-m9j
// 铁律 v2.0 Rule 1+2]) while giving both tmux and non-tmux users the same live-preview overview.

const (
	// sessionScreenScanBytes is how much of the ring is replayed to reconstruct the screen. It has
	// to cover at least a full repaint cycle — a TUI redrawing an 48×200 grid with colour codes
	// runs tens of KB — or the replay starts mid-frame and the top of the card looks torn. Bounded
	// so the 1s rebuild stays cheap no matter how large the buffer grows.
	sessionScreenScanBytes = 128 * 1024

	// sessionsOverviewCacheTTL keeps one tick's snapshot shared across every connected writer.
	// Slightly under the 1s ticker so a tick never reuses the previous tick's answer.
	sessionsOverviewCacheTTL = 900 * time.Millisecond
)

// SessionOverviewEntry is one card in the non-tmux Agent Overview.
//
// Field names mirror the tmux pane/window payload (agentTool / agentStatus / tail) so the frontend
// can normalize both sources into ONE card model instead of maintaining two shapes.
type SessionOverviewEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	CWD    string `json:"cwd,omitempty"`
	Engine string `json:"engine,omitempty"`
	// AgentTool / AgentStatus come from the same detector the session list uses — literally the
	// same snapshot (handleListSessions reads this struct), so a card and its tab dot cannot
	// disagree even for one tick.
	AgentTool   string `json:"agentTool,omitempty"`
	AgentStatus string `json:"agentStatus,omitempty"`
	// AwaitingUser / AwaitingSince / EndedOnQuestion mirror the tmux pane payload so the shared
	// overview state machine treats a session and a pane identically: needs-you, the reload-proof
	// completion the "seen" layer dismisses against, and whether that turn ended on a question.
	AwaitingUser    bool   `json:"awaitingUser,omitempty"`
	AwaitingSince   string `json:"awaitingSince,omitempty"`
	EndedOnQuestion bool   `json:"endedOnQuestion,omitempty"`
	// StatusRule explains WHY this session has the status it has — the single rule behind the
	// verdict ("transcript.running", "screen.approval", "signal.notify"…). Present on EVERY
	// decision, including a plain green one.
	//
	// It used to ride only on waiting/awaiting, reasoning that those are the verdicts that can be
	// wrong in a way the user feels. They are not the only ones: a session stuck GREEN while its
	// agent waits is the failure nobody is told about, and it left no trace at all — five separate
	// rules can return Running, so the question "why is it green" had no answer but a guess.
	// Shipping the rule unconditionally is free here because it is stable while the status is, and
	// this frame is diff-suppressed: it only changes when the REASON changes.
	//
	// StatusEvidence is the screen line that matched, scrubbed and truncated — and stays confined
	// to attention decisions, because it churns on every tick (spinner frames, token counters) and
	// would defeat that suppression for no gain: on a green session the rule already says it all.
	//
	// Diagnostic only — nothing renders them; they exist so a wrong dot can be TRACED rather than
	// re-argued. See agentintel/status_decision.go.
	StatusRule     string `json:"statusRule,omitempty"`
	StatusEvidence string `json:"statusEvidence,omitempty"`
	// Exited marks a dead PTY. Kept explicit rather than inferred from an empty tail: a live shell
	// that has simply printed nothing is NOT the same as one whose process is gone.
	Exited bool `json:"exited,omitempty"`
	// Tail is the last few lines of REAL output (agent chrome stripped). Empty when the session has
	// produced nothing yet — the card then says so rather than rendering blank padding.
	Tail []string `json:"tail,omitempty"`
}

// sessionsOverview builds the current card set for every live session.
//
// Cost note: this runs once per tick (memoized below), so it stays on the cheap path — a bounded
// ring read, one screen replay, and an INCREMENTAL transcript read per agent session (only the
// bytes appended since the last tick, via the tracker's cached driver). It deliberately does NOT
// do the full per-session analysis /sessions/{id}/overview does, which is orders of magnitude
// more expensive and belongs on an explicit request.
func (s *Server) sessionsOverview(ctx context.Context) []SessionOverviewEntry {
	sessions := s.mgr.List()
	out := make([]SessionOverviewEntry, 0, len(sessions))
	live := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		// LIVE cwd, not the creation cwd. A terminal is almost always `cd`'d somewhere within
		// seconds of being opened, so `sess.CWD` ("~" for every UI-created tab) answers "where did
		// this tab start", which is not a question anyone asks. Everything downstream wants "where
		// is this terminal now": the card's footer, the action bar, the右键 copy-path, and — the
		// reason this was found — the workbench's stored cwd, which is the ONLY copy that survives
		// a server restart and therefore decides where an auto-reopened shell lands. liveCWD was
		// already here and already documented for exactly this; the overview just never used it.
		// Falls back to the creation cwd when /proc is unavailable (non-Linux) or the shell is gone.
		cwd := liveCWD(sess.ShellPID())
		sess.mu.Lock()
		if cwd == "" {
			cwd = sess.CWD
		}
		entry := SessionOverviewEntry{
			ID:     sess.ID,
			Title:  sessionTitle(sess),
			CWD:    cwd,
			Engine: sess.Engine,
			Exited: sess.Status == StatusExited,
		}
		buf := sess.Buffer
		sess.mu.Unlock()
		live[sess.ID] = true

		// renderScreen REPLAYS the PTY stream onto a grid instead of deleting escape codes.
		// That distinction is the whole feature: an agent TUI repaints by moving the cursor,
		// so stripping the positioning concatenates every frame into one unreadable line
		// (observed on 8087). Replaying reconstructs what the terminal actually shows.
		//
		// The grid is sized to THIS session's PTY, not to a constant. A TUI addresses rows
		// absolutely, so replaying a 52-row screen onto a 48-row grid doesn't crop it — rows
		// 49-52 all clamp onto row 48 and overwrite each other into one mashed line.
		cols, rows := sess.PTYSize()
		var screen []string
		if buf != nil {
			screen = renderScreen(string(buf.ReadTail(sessionScreenScanBytes)), rows, cols)
		}

		// One screen, two readers: the tracker inspects the RAW screen because a permission
		// prompt lives in the agent's bottom chrome, and the card shows the STRIPPED screen
		// because that same chrome is noise once you already have a status dot for it.
		agent := s.sessionAgent.State(ctx, sess.ID, sess.ShellPID(), entry.CWD, screen)
		if agent.Tool != "" {
			entry.AgentTool = string(agent.Tool)
			entry.AgentStatus = string(agent.Status)
			entry.AwaitingUser = agent.AwaitingUser
			entry.AwaitingSince = agent.AwaitingSince
			entry.EndedOnQuestion = agent.EndedOnQuestion
			// Same split as the tmux pane payload, and deliberately kept identical to it: the
			// RULE rides on every decision (a green session that should be amber is the silent
			// failure, and it used to leave no trace at all), the EVIDENCE only on the ones
			// asking for the user (it is a live screen line — it churns every tick and would
			// defeat this frame's diff suppression). See tmux_state.go for the full reasoning;
			// these two paths must not drift, or "why is it green" gets two different answers
			// depending on whether you run tmux.
			entry.StatusRule = string(agent.Decision.Rule)
			if agent.Decision.IsAttention() {
				entry.StatusEvidence = agent.Decision.Evidence
			}
		} else if s.hooks.AgentDetect != nil && sess.ShellPID() > 0 {
			// Deprecated host override — only reachable when the built-in detector found
			// nothing, so an embedder with an exotic runtime can still contribute a status.
			if tool, status := s.hooks.AgentDetect(ctx, sess.ShellPID(), entry.CWD); tool != "" {
				entry.AgentTool = tool
				entry.AgentStatus = status
			}
		}

		// An EXPLICIT signal (session_signal.go) outranks everything above: the detectors
		// INFER whether you are needed, a BEL/OSC notification is the program SAYING SO. So
		// it can only ever raise the card to needs-you, never lower it.
		//
		// It lands on AwaitingUser (amber, dismissable) and deliberately NOT on
		// AgentStatus="waiting" (red, blocked): a bell may mean "approve this" or "I'm done",
		// and we cannot tell which. Amber is the honest severity for "come look".
		if sig, at, _, ok := sess.PendingSignal(); ok {
			entry.AwaitingUser = true
			// AwaitingSince is the key the frontend's "seen" layer dismisses against, so a
			// fresh signal MUST advance it past whatever the transcript produced — otherwise
			// a card the user already dismissed would swallow the new signal in silence.
			if prev, perr := time.Parse(time.RFC3339Nano, entry.AwaitingSince); entry.AwaitingSince == "" || perr != nil || at.After(prev) {
				entry.AwaitingSince = at.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
			}
			// The signal OUTRANKS whatever the detectors concluded, so it owns the provenance
			// too — otherwise a card raised by a BEL would carry a screen rule that had nothing
			// to do with why it lit up. Evidence is the program's own notification text, held to
			// the same length/sanitisation ceiling as a screen line: it is equally user-content.
			rule := agentintel.RuleSignalNotify
			if sig.Kind == ansisignal.KindBell {
				rule = agentintel.RuleSignalBell
			}
			entry.StatusRule = string(rule)
			entry.StatusEvidence = sanitizeFieldMax(strings.TrimSpace(sig.Title+" "+sig.Body), 120)
		}

		if screen != nil {
			// TailFromLines removes the agent's pinned chrome exactly as the tmux overview
			// does, and OverviewTailLines is literally the tmux overview's cap — the two feeds
			// land in the same card grid at the same height, so "how many lines a card carries"
			// is ONE decision, not two that agree by convention. (They didn't: this used to be a
			// local `sessionTailLines = 8`, so a non-tmux card showed 8 lines in a box sized for
			// ~40 and the rest was blank. See agentintel.OverviewTailLines.)
			entry.Tail = agentintel.TailFromLines(
				screen, agentintel.AgentTool(entry.AgentTool), agentintel.OverviewTailLines)
		}
		out = append(out, entry)
	}
	// Release transcript bindings held by closed sessions — otherwise they keep excluding a live
	// session from claiming that file (bindings are mutually exclusive on purpose).
	s.sessionAgent.Prune(live)
	// Same reason, different map: drop the signal debounce/cooldown clocks of dead sessions.
	s.signals.prune(live)
	return out
}

// overviewSnapshot is one tick's answer: the entries plus their marshalled form. Both are cached
// together so the WS push and the REST session list are the SAME computation, not two that agree
// by convention — a card and its tab dot showing different statuses was the exact class of bug
// this feature kept producing.
type overviewSnapshot struct {
	entries []SessionOverviewEntry
	json    []byte
}

// overviewSnapshot returns the current snapshot, rebuilding it at most once per tick.
//
// Memoized because the payload is GLOBAL (it describes every session) while the callers are
// PER-CONNECTION: with a surface mounted per terminal, N clients would otherwise each rebuild the
// identical answer every second — N× the ring replays, screen renders and transcript reads for one
// shared result. The cache turns that back into one computation per tick, whatever N is.
// Deliberately time-based rather than event-based: the ticker is already the clock, so this stays
// a pure optimization with no new invalidation rules to keep in sync.
func (s *Server) overviewSnapshot(ctx context.Context) overviewSnapshot {
	now := time.Now()
	s.overviewCacheMu.Lock()
	if s.overviewCacheAt.Add(sessionsOverviewCacheTTL).After(now) {
		cached := s.overviewCache
		s.overviewCacheMu.Unlock()
		return cached
	}
	s.overviewCacheMu.Unlock()

	entries := s.sessionsOverview(ctx)
	raw, err := json.Marshal(entries)
	if err != nil {
		return overviewSnapshot{entries: entries}
	}
	snap := overviewSnapshot{entries: entries, json: raw}

	s.overviewCacheMu.Lock()
	s.overviewCache = snap
	s.overviewCacheAt = now
	s.overviewCacheMu.Unlock()
	return snap
}

// sessionsOverviewJSON is the WS push payload. Returning raw JSON lets each writer byte-compare
// against the previous push and skip identical frames — the same diff suppression tmux_state uses,
// and the reason a quiet machine pushes nothing at all.
func (s *Server) sessionsOverviewJSON(ctx context.Context) []byte {
	return s.overviewSnapshot(ctx).json
}

// sessionAgentStatuses is the session-id → (tool, status) view the REST list renders. Same
// snapshot as the cards by construction.
func (s *Server) sessionAgentStatuses(ctx context.Context) map[string][2]string {
	snap := s.overviewSnapshot(ctx)
	out := make(map[string][2]string, len(snap.entries))
	for _, e := range snap.entries {
		if e.AgentTool != "" {
			out[e.ID] = [2]string{e.AgentTool, e.AgentStatus}
		}
	}
	return out
}
