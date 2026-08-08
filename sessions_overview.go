package terminal

import (
	"bytes"
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

	// overviewRebuildBudget bounds one shared rebuild.
	//
	// It exists because the rebuild is DETACHED from its caller (see overviewSnapshot): dropping the
	// caller's cancellation also drops the only thing that used to stop a wedged `ps` or a stuck
	// transcript read from holding the shared lock forever. Generous, because exceeding it is not a
	// slow machine's normal condition — it is the signal that this rebuild's answer is a partial
	// read and must not be published.
	overviewRebuildBudget = 5 * time.Second
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
			screen = s.sessionScreen(sess.ID, buf, cols, rows)
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
	// Same reason, third map: a closed session's replayed screen is dead weight, and the grid it
	// holds is the largest thing in this file.
	s.pruneScreenCache(live)
	return out
}

// renderedScreen is one session's replayed grid, kept alongside EXACTLY the inputs that produced
// it. All three must match for the cached lines to still be the right answer — the seq alone is
// not enough, because a resize repaints the same bytes onto a different grid.
type renderedScreen struct {
	seq   uint64
	cols  int
	rows  int
	lines []string
}

// sessionScreen returns this session's screen, replaying the ring only when it must.
//
// ── Why this is the point, not an optimisation ───────────────────────────────────────────────
// The invariant this file was violating is「变化才有代价」. A card's screen is up to 128 KiB of the
// ring parsed and replayed onto a full grid — the single most expensive thing per session — and it
// ran once per second per session unconditionally, because the tick was treated as the reason to
// recompute. The honest reason to recompute is that the inputs moved. Ten idle terminals cost ten
// screen replays a second for ten identical answers; the cost tracked the CLOCK and the number of
// terminals you had ever opened, neither of which is a thing the user did.
//
// The comment one level down used to defend this: "deliberately time-based rather than event-based:
// the ticker is already the clock, so this stays a pure optimization with no new invalidation rules
// to keep in sync." The invalidation rule turned out to cost one field on the ring buffer, and the
// thing it bought back is not an optimisation — it is the difference between a machine that idles
// and one that keeps a core warm to redraw screens nobody wrote to.
//
// Note what is NOT skipped: the agent-state read still runs every tick. The transcript is written by
// a DIFFERENT process and moves with no PTY output at all (an agent thinking, a tool running), so
// its freshness cannot be inferred from this buffer. Only the screen replay is conditional, because
// only the screen is a pure function of bytes we can see move.
func (s *Server) sessionScreen(id string, buf *RingBuffer, cols, rows int) []string {
	seq := buf.Seq()
	s.screenCacheMu.Lock()
	cached, ok := s.screenCache[id]
	s.screenCacheMu.Unlock()
	if ok && cached.seq == seq && cached.cols == cols && cached.rows == rows {
		terminalOverviewScreenReuseTotal.Inc()
		return cached.lines
	}

	// Read the tail AFTER sampling seq. The other order would let a write land in between and
	// produce lines newer than the seq they get filed under — the next tick would then see a
	// matching seq and serve a screen it believes is current while the buffer has moved on. Sampling
	// first can only file NEWER content under an OLDER seq, which merely costs one extra replay.
	lines := renderScreen(string(buf.ReadTail(sessionScreenScanBytes)), rows, cols)
	terminalOverviewScreenRenderTotal.Inc()

	s.screenCacheMu.Lock()
	// Created here rather than in NewServer: a Server assembled any other way (tests do, and an
	// embedder may) would otherwise panic on the first write to a nil map — a constructor is a
	// promise, and this is one line that does not need anyone to keep it.
	if s.screenCache == nil {
		s.screenCache = make(map[string]renderedScreen)
	}
	s.screenCache[id] = renderedScreen{seq: seq, cols: cols, rows: rows, lines: lines}
	s.screenCacheMu.Unlock()
	return lines
}

func (s *Server) pruneScreenCache(live map[string]bool) {
	s.screenCacheMu.Lock()
	defer s.screenCacheMu.Unlock()
	for id := range s.screenCache {
		if !live[id] {
			delete(s.screenCache, id)
		}
	}
}

// overviewSnapshot is one tick's answer: the entries, their marshalled form, the finished WS frame,
// and the revision that identifies all three. They are cached TOGETHER so the WS push and the REST
// session list are the SAME computation, not two that agree by convention — a card and its tab dot
// showing different statuses was the exact class of bug this feature kept producing.
type overviewSnapshot struct {
	entries []SessionOverviewEntry
	json    []byte
	// frame is the marshalled WSControlMessage, built ONCE per revision instead of once per
	// connection per tick. The payload is global, so the frame around it is global too; every
	// connection re-encoding the same envelope was N copies of one answer.
	frame []byte
	// revision changes if and only if `json` changes. It is what lets a subscriber ask "is this
	// new?" by comparing a uint64 instead of the whole payload — the difference between a cost
	// that scales with CHANGE and one that scales with the number of people watching. Starts at 1,
	// so a fresh subscriber's zero value never accidentally matches a real snapshot.
	revision uint64
}

// overviewSnapshot returns the current snapshot, rebuilding it at most once per tick.
//
// ── Why the lock is held ACROSS the rebuild ──────────────────────────────────────────────────
// The payload is GLOBAL (it describes every session) while the callers are PER-CONNECTION. The
// previous version released the lock before building, which made the cache a hit-path optimisation
// only: on a MISS, every caller that arrived in that window started its own full rebuild. And they
// arrive together by construction — N connections each ticking at 1s all miss the same expired
// entry within the same millisecond, so the one moment the answer is expensive is exactly the
// moment N of them compute it in parallel. Holding the lock turns the herd into one builder and
// N waiters who then find the fresh entry. That is the same shape tmux's topologySnapshot already
// uses, and for the same reason.
//
// Waiting is the correct behaviour for every caller here: they all want THIS tick's answer, and a
// second concurrent rebuild would not produce a better one, only an equal one at double the cost.
func (s *Server) overviewSnapshot(ctx context.Context) overviewSnapshot {
	// Sampled BEFORE the lock, deliberately: a builder holds the mutex for the whole rebuild, so by
	// the time we are inside we can no longer tell whether we walked in or queued. Reading it here
	// answers the question that matters — "was someone already building when I arrived", i.e. was I
	// one of the herd this lock exists to collapse. Approximate at the edge (the builder may finish
	// between this read and the Lock) and that is fine for a counter; a flag we could only read
	// after acquiring would be exactly zero forever, which is worse than approximate.
	contended := s.overviewBuilding.Load()

	s.overviewCacheMu.Lock()
	defer s.overviewCacheMu.Unlock()
	if s.overviewCacheAt.Add(sessionsOverviewCacheTTL).After(time.Now()) {
		if contended {
			terminalOverviewRebuildSharedTotal.Inc()
		}
		return s.overviewCache
	}
	s.overviewBuilding.Store(true)
	defer s.overviewBuilding.Store(false)

	// ── The rebuild does not belong to whoever asked for it ──────────────────────────────────
	// This answer describes every session and is served to every caller, but it used to run under
	// the CONTEXT OF ONE CONNECTION — whichever one happened to tick first. A client closing its
	// tab mid-rebuild therefore cancelled a computation the other clients were waiting for, and the
	// cancellation does not surface as an error: `ps` fails, the process snapshot falls back to
	// whatever is cached (nil on a fresh server), every agent reads as ToolNone, and the result is a
	// perfectly well-formed payload saying NO SESSION IS RUNNING AN AGENT. That is the same lie the
	// tmux side told with an empty pane bar ("observed nothing" published as "there is nothing"),
	// arriving through a different door.
	//
	// WithoutCancel keeps the caller's log fields — the rebuild should still be traceable to the
	// tick that triggered it — and drops only its LIFETIME, which was never the right owner.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), overviewRebuildBudget)
	defer cancel()

	entries := s.sessionsOverview(bctx)
	if bctx.Err() != nil {
		// Out of budget. What we hold is a partial read of a loaded machine, not a description of
		// it. Publish nothing, stamp nothing, and deliberately do NOT refresh overviewCacheAt, so
		// the next caller retries immediately instead of serving this for a full TTL.
		return s.overviewCache
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		// Marshal failed: publish nothing, cache nothing. Entries still go back to the REST caller,
		// which does not need the encoded form. Deliberately NOT stamped as a new revision — a
		// revision that no payload corresponds to would tell subscribers something changed and then
		// have nothing to send them.
		return overviewSnapshot{entries: entries, revision: s.overviewCache.revision}
	}

	snap := overviewSnapshot{entries: entries, json: raw, revision: s.overviewCache.revision}
	// The revision moves only when the ANSWER moves. A rebuild that reproduces the same bytes is a
	// rebuild nobody needs to hear about — that is the whole content of「变化才有代价」at this layer.
	if !bytes.Equal(raw, s.overviewCache.json) {
		snap.revision = s.overviewCache.revision + 1
		snap.frame, _ = json.Marshal(WSControlMessage{Type: MsgTypeSessionsOverview, Payload: raw})
	} else {
		snap.frame = s.overviewCache.frame
	}

	s.overviewCache = snap
	s.overviewCacheAt = time.Now()
	return snap
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
