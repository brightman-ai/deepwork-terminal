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
// The agent facts are NOT declared here: they are the embedded SurfaceUnit, the single declaration
// this payload shares with the tmux pane (agentintel/tmux_state.go). This comment used to say
// "field names mirror the tmux pane/window payload … so the frontend can normalize both sources
// into ONE card model" — an accurate description of the intent and a request the compiler could
// not enforce. It is now enforced: there is one field list, so a card and a pane cannot describe
// the same fact under different names, and neither can grow a fact the other lacks.
type SessionOverviewEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	CWD    string `json:"cwd,omitempty"`
	Engine string `json:"engine,omitempty"`
	// Exited marks a dead PTY. Kept explicit rather than inferred from an empty tail: a live shell
	// that has simply printed nothing is NOT the same as one whose process is gone.
	Exited bool `json:"exited,omitempty"`
	// TmuxDetected mirrors Session.TmuxDetected — the tab strip's right-click menu needs it to
	// decide whether "结束卡死进程" (force-kill the foreground process) applies to THIS tab, for
	// every tab, not just the currently-active one. Single-session GET already exposed the same
	// fact (handleGetSession); this is that fact reaching the ALL-sessions feed the menu is built
	// from, so a background tab doesn't have to become active before its menu can be trusted.
	TmuxDetected bool `json:"tmuxDetected,omitempty"`
	// Embedded, not listed: the CARD — its agent facts and its tail, the same declaration the tmux
	// window carries (agentintel/surface_card.go). AgentTool / AgentStatus still come from the same
	// detector the session list uses — literally the same snapshot (handleListSessions reads this
	// struct), so a card and its tab dot cannot disagree even for one tick.
	//
	// This card has exactly ONE unit and that unit is the session itself, so its facts and the
	// unit's are the same values — which is not a shortcut taken here but the general rule applied
	// to N=1 (RollUp is used below rather than assigning around it, and TestRollUp_OneUnitIsIdentity
	// is what makes that safe to rely on).
	agentintel.SurfaceCard
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
		cwd := processCWD(sess.ShellPID())
		sess.mu.Lock()
		if cwd == "" {
			cwd = sess.CWD
		}
		entry := SessionOverviewEntry{
			ID:           sess.ID,
			Title:        sessionTitle(sess),
			CWD:          cwd,
			Engine:       sess.Engine,
			Exited:       sess.Status == StatusExited,
			TmuxDetected: sess.TmuxDetected,
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
		grid := sess.PTYSize()
		var screen []string
		if buf != nil {
			screen = s.sessionScreen(sess.ID, buf, grid.Cols, grid.Rows)
		}

		// One screen, two readers: the tracker inspects the RAW screen because a permission
		// prompt lives in the agent's bottom chrome, and the card shows the STRIPPED screen
		// because that same chrome is noise once you already have a status dot for it.
		//
		// The session's ONE unit arrives whole — status, needs-you, the reload-proof completion
		// time, the rule behind the verdict, the age of the evidence — from the decision shared
		// with the tmux pane. This used to be seven assignments copying it across field by field,
		// including a second, hand-rolled version of the rule/evidence split that DecideSurface
		// already applies; every one of them was a place a new surface fact could be forgotten on
		// this side alone.
		state := s.sessionAgent.State(ctx, sess.ID, sess.ShellPID(), entry.CWD, screen)
		unit := state.SurfaceUnit
		if state.CWD != "" {
			entry.CWD = state.CWD
		}
		if unit.AgentTool == agentintel.ToolNone && s.hooks.AgentDetect != nil && sess.ShellPID() > 0 {
			// Deprecated host override — only reachable when the built-in detector found
			// nothing, so an embedder with an exotic runtime can still contribute a status.
			// Converted at THIS boundary, once. The hook predates the domain types and hands back
			// bare strings; that is a reason to narrow them here, not a reason for the rest of the
			// system to describe an agent with a type that cannot tell a tool from a status.
			if tool, status := s.hooks.AgentDetect(ctx, sess.ShellPID(), entry.CWD); tool != "" {
				unit.AgentTool = agentintel.AgentTool(tool)
				unit.AgentStatus = agentintel.AgentStatus(status)
			}
		}

		// tmux attach 桥（REQ-cli-tabs-009）：两条检测都一无所获时，若这个 tab 的 shell 正 attach
		// 在某个 tmux session 上，agent 事实取那个 session 的全窗口 roll-up（语义与出处见
		// sessions_overview_tmux.go）。填的还是这同一个 unit —— 后面的显式信号覆盖与 N=1 RollUp
		// 原样适用，桥只是在「什么都不知道」时把 tmux_state 早已算好的事实接进来。
		if unit.AgentTool == agentintel.ToolNone {
			if bridged, cwd, ok := tmuxTabRollup(ctx, s.tmuxProvider, sess.ShellPID()); ok {
				unit = bridged
				if cwd != "" {
					entry.CWD = cwd
				}
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
			unit.AwaitingUser = true
			// AwaitingSince is the key the frontend's "seen" layer dismisses against, so a
			// fresh signal MUST advance it past whatever the transcript produced — otherwise
			// a card the user already dismissed would swallow the new signal in silence.
			//
			// A plain time comparison now that both feeds carry a time.Time. This used to parse the
			// string back out of the field it had just formatted — the round trip existed only
			// because the card had been given a pre-formatted string where the pane had a real
			// instant, and it could fail (a parse error was treated as "no time", which silently
			// took the same branch as "advance it").
			if unit.AwaitingSince.IsZero() || at.After(unit.AwaitingSince) {
				unit.AwaitingSince = at.UTC()
			}
			// The signal OUTRANKS whatever the detectors concluded, so it owns the provenance
			// too — otherwise a card raised by a BEL would carry a screen rule that had nothing
			// to do with why it lit up. Evidence is the program's own notification text, held to
			// the same length/sanitisation ceiling as a screen line: it is equally user-content.
			rule := agentintel.RuleSignalNotify
			if sig.Kind == ansisignal.KindBell {
				rule = agentintel.RuleSignalBell
			}
			unit.StatusRule = string(rule)
			unit.StatusEvidence = sanitizeFieldMax(strings.TrimSpace(sig.Title+" "+sig.Body), 120)
		}

		// The card, from its units — all one of them. Deliberately RollUp rather than a plain
		// assignment: a session card is not a different kind of thing from a tmux window card, it
		// is the same thing with N=1, and routing it around the shared rule is exactly how the two
		// would start drifting again the next time that rule gains a clause.
		entry.SurfaceUnit = agentintel.RollUp([]agentintel.SurfaceUnit{unit}, 0)

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
	// Tab badges need agent facts, not the terminal tails from every session. This
	// compact view is cached with the full overview and changes only when facts do.
	statusFrame []byte
	// revision changes if and only if `json` changes. It is what lets a subscriber ask "is this
	// new?" by comparing a uint64 instead of the whole payload — the difference between a cost
	// that scales with CHANGE and one that scales with the number of people watching. Starts at 1,
	// so a fresh subscriber's zero value never accidentally matches a real snapshot.
	revision uint64
}

// overviewBuild is one in-flight rebuild that later arrivals WAIT ON instead of duplicating.
//
// A promise rather than a mutex, and the distinction is the whole point: **waiting on a channel is
// cancellable, waiting on a mutex is not.** `sync.Mutex.Lock()` cannot be abandoned, so a rebuild
// that wedges pins every waiter for as long as it lasts — including WS producer goroutines whose
// browser tab has already closed and whose context is long cancelled. They cannot exit. That is a
// goroutine leak that no amount of care at the call sites can undo.
type overviewBuild struct {
	done chan struct{}
	snap overviewSnapshot
	ok   bool
	// elapsed is how long THIS rebuild took — a property of this rebuild, not a reading taken
	// from a global counter afterwards.
	//
	// The distinction is not academic. The histogram is process-wide while rebuilds are
	// deliberately detached from whoever asked for them (see overviewSnapshot), so "the count
	// went up by one" is a claim about the whole process during a window, not about this build.
	// Anything else running concurrently makes it wrong, and it will be wrong intermittently —
	// the worst way to be wrong.
	elapsed time.Duration
}

// awaitOverview waits for the in-flight build, or leaves when the caller's own context ends.
//
// Leaving is free and correct: the build keeps running and publishes for whoever asks next, and the
// departing caller gets the last good snapshot — which is the honest answer to "what do we know
// right now", not a placeholder.
func awaitOverview(ctx context.Context, b *overviewBuild, lastGood overviewSnapshot) overviewSnapshot {
	finished := func() overviewSnapshot {
		if b.ok {
			return b.snap
		}
		return lastGood
	}
	// A finished build beats an expired context. The answer is already in hand, and handing back a
	// staler one merely because the caller is in a hurry would be gratuitous. Asked FIRST and
	// non-blockingly because Go's select picks at random when both cases are ready — leaving it to
	// the select below would make the result a coin flip.
	select {
	case <-b.done:
		return finished()
	default:
	}
	select {
	case <-b.done:
		return finished()
	case <-ctx.Done():
		return lastGood
	}
}

// overviewSnapshot returns the current snapshot, rebuilding it at most once per tick.
//
// ── One builder, N waiters, and every waiter free to leave ───────────────────────────────────
// The payload is GLOBAL (it describes every session) while the callers are PER-CONNECTION, and they
// arrive together by construction: N connections each ticking at 1s miss the same expired entry
// within the same millisecond. So the one moment the answer is expensive is exactly the moment N of
// them would compute it in parallel. That herd has to collapse to one.
//
// The obvious way to collapse it — hold the cache mutex across the rebuild — was WRONG, and this is
// the second version. The rebuild is not made of cancellable work: `ps` and `tmux` run under
// contexts, but the transcript reads underneath (agentintel's JSONLReader: os.Open, bufio) and
// liveCWD's /proc read are **plain blocking syscalls, which Go cannot preempt with a context**. The
// budget below therefore does not bound them, and jsonl_reader.go documents a 19-second first pass
// over a 4 GB rollout as a condition this codebase has actually met. Under the mutex version, one
// such read froze every WS overview push and the whole REST /sessions endpoint, and stranded every
// waiting goroutine past the death of its own connection.
//
// So the mutex guards the CACHE — briefly — and a promise deduplicates the COMPUTATION. Same
// collapse, and a wedged rebuild now costs exactly one stuck goroutine instead of all of them.
func (s *Server) overviewSnapshot(ctx context.Context) overviewSnapshot {
	s.overviewCacheMu.Lock()
	now := time.Now()
	if s.overviewCacheAt.Add(sessionsOverviewCacheTTL).After(now) {
		snap := s.overviewCache
		s.overviewCacheMu.Unlock()
		return snap
	}
	lastGood := s.overviewCache
	if b := s.overviewInFlight; b != nil {
		s.overviewCacheMu.Unlock()
		terminalOverviewRebuildSharedTotal.Inc()
		return awaitOverview(ctx, b, lastGood)
	}
	// Rate-limit ATTEMPTS, not just successes. Without this, a rebuild that keeps failing never
	// stamps overviewCacheAt, so every single caller starts another one immediately and the server
	// does nothing but retry — the pathological case being a machine slow enough that the rebuild
	// legitimately cannot finish, where the old code turned "slow but eventually right" into
	// "permanently empty, at full CPU". One attempt per TTL is the same cadence as healthy
	// operation, so this costs nothing when things work.
	if s.overviewAttemptAt.Add(sessionsOverviewCacheTTL).After(now) {
		s.overviewCacheMu.Unlock()
		return lastGood
	}
	b := &overviewBuild{done: make(chan struct{})}
	s.overviewInFlight = b
	s.overviewAttemptAt = now
	s.overviewCacheMu.Unlock()

	go s.buildOverview(ctx, b)
	return awaitOverview(ctx, b, lastGood)
}

// buildOverview performs one rebuild and publishes it, or publishes nothing and says so.
func (s *Server) buildOverview(parent context.Context, b *overviewBuild) {
	// What this rebuild cost, measured on EVERY exit path. Until this existed the non-tmux
	// overview had counters for which path ran and none at all for how long it took — so the one
	// question a stalled dashboard raises ("is the rebuild eating the tick?") had no answer but a
	// guess, which is precisely the mistake this scope already made once. See logOverviewRebuild.
	start := time.Now()
	sessions := 0
	defer func() {
		// Recorded on b BEFORE close(b.done): the channel close is what publishes this build to
		// its waiters, so anything set after it is a race.
		b.elapsed = time.Since(start)
		s.overviewCacheMu.Lock()
		s.overviewInFlight = nil
		s.overviewCacheMu.Unlock()
		close(b.done)
		// Logged on the PARENT context: it carries the tick's log fields, and this is a
		// measurement of work that has already finished, so the caller's lifetime is irrelevant
		// to it — the same reason the rebuild itself runs under WithoutCancel below.
		logOverviewRebuild(parent, time.Since(start), sessions)
	}()

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
	//
	// The budget bounds the SUBPROCESS half only (ps/tmux/lsof, which run under this context). It
	// cannot bound a blocking file read; saying otherwise would be the kind of comment that makes
	// the next person trust a guarantee that is not there.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(parent), overviewRebuildBudget)
	defer cancel()

	entries := s.sessionsOverview(bctx)
	sessions = len(entries)
	if bctx.Err() != nil {
		// Out of budget: what we hold is a partial read of a loaded machine, not a description of
		// it. Publish nothing — and COUNT it, because a rebuild that quietly gives up looks exactly
		// like a quiet machine from the outside. Waiters fall back to the last good snapshot.
		terminalOverviewRebuildAbandonedTotal.Inc()
		terminalLogger.Warn(bctx, "overview rebuild abandoned: out of budget",
			"budget_ms", overviewRebuildBudget.Milliseconds(),
			"sessions", len(entries),
			"effect", "serving the last good snapshot; the cards are stale, not wrong")
		return
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		// Publish nothing, stamp no revision: a revision no payload corresponds to would tell
		// subscribers something changed and then have nothing to send them.
		terminalOverviewRebuildAbandonedTotal.Inc()
		return
	}

	s.overviewCacheMu.Lock()
	snap := overviewSnapshot{entries: entries, json: raw, revision: s.overviewCache.revision}
	// The revision moves only when the ANSWER moves. A rebuild that reproduces the same bytes is a
	// rebuild nobody needs to hear about — that is the whole content of「变化才有代价」at this layer.
	if !bytes.Equal(raw, s.overviewCache.json) {
		snap.revision = s.overviewCache.revision + 1
		snap.frame, _ = json.Marshal(WSControlMessage{Type: MsgTypeSessionsOverview, Payload: raw})
	} else {
		snap.frame = s.overviewCache.frame
	}
	compact := append([]SessionOverviewEntry(nil), entries...)
	for i := range compact {
		compact[i].Tail = nil
	}
	compactJSON, _ := json.Marshal(compact)
	snap.statusFrame, _ = json.Marshal(WSControlMessage{Type: MsgTypeSessionsOverview, Payload: compactJSON})
	s.overviewCache = snap
	s.overviewCacheAt = time.Now()
	s.overviewCacheMu.Unlock()

	// Written before the deferred close(b.done) publishes it — the channel close is what makes
	// these visible to waiters, so nothing here needs its own lock.
	b.snap, b.ok = snap, true
}

// sessionAgentStatuses is the session-id → (tool, status) view the REST list renders. Same
// snapshot as the cards by construction.
//
// Strings, because that is what this view IS: the shape handleListSessions marshals. Widening
// happens here, once, at the edge where the domain leaves the program — not by keeping the domain
// itself untyped for the convenience of its last consumer.
func (s *Server) sessionAgentStatuses(ctx context.Context) map[string][2]string {
	snap := s.overviewSnapshot(ctx)
	out := make(map[string][2]string, len(snap.entries))
	for _, e := range snap.entries {
		if e.AgentTool != "" {
			out[e.ID] = [2]string{string(e.AgentTool), string(e.AgentStatus)}
		}
	}
	return out
}
