package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/ansisignal"
	"github.com/brightman-ai/deepwork-terminal/notify"
)

// Explicit "I need you" signals — the server-side half of ansisignal.
//
// ── Why this path exists next to the ones we already have ───────────────────────────────
// Every other attention signal in this codebase is INFERRED: agent_notifier.go diffs tmux
// pane statuses, sessions_overview.go reads transcripts and re-renders screens. Both are
// good heuristics and both can be wrong. This path carries no inference at all — the program
// in the PTY emitted a BEL or an OSC desktop notification, which is the one moment it states
// its own intent. That is why it is wired as its own path rather than folded into the
// pollers: it is evidence, not an estimate, and it must not inherit their preconditions.
//
// ── Why it must NOT be gated on tmux ────────────────────────────────────────────────────
// This path never had a tmux dependency and must never grow one: the gate below is "did this
// session's program ask for you", full stop. (agent_notifier.go used to be gated on
// tmuxInstalled() by construction — it could only see what `tmux list-panes` showed it — so a
// user running `claude` in a plain tab got nothing from it. It now polls PTY sessions too, and
// SHARES the per-session cooldown below so the inferred turn-end and an explicit bell for the
// same session can never both go out; see agentNotifier.allowFire.)
//
// ── Why a BEL is treated with suspicion and an OSC is not ───────────────────────────────
// readline rings the bell for an ambiguous tab-completion, for a failed search, for hitting
// the start of the line in vi mode. A plain shell can emit dozens of bells a minute and none
// of them means "come back". Firing a phone notification on those would train the user to
// ignore the channel within a day, which costs us the real signals too. So a BEL only counts
// when an AI agent is actually running in that session (that is when a bell is a deliberate
// "your turn"), and even then at most once per signalBellDebounce. OSC 9/777/99 need no such
// qualification: nothing emits a desktop notification by accident.

const (
	// signalBellDebounce collapses a burst of bells into one signal. A single "your turn"
	// often arrives as two or three bells; and a shell hammering the bell must not become a
	// stream of events.
	signalBellDebounce = 10 * time.Second

	// signalBellConfirmDelay separates the two agent probes a bell must pass. It only needs to
	// exceed the process snapshot's cache TTL so the second probe cannot reuse the first's
	// frame; a second is comfortably past it and imperceptible to a human.
	signalBellConfirmDelay = 1 * time.Second

	// signalNotifyCooldown is the per-session floor between two OUTBOUND notifications
	// (phone/IM). Matches the tmux notifier's per-pane cooldown so both paths feel the same.
	// The in-app state and the WS push are NOT throttled by it — they are free.
	signalNotifyCooldown = 2 * time.Minute

	// signalAgentProbeTimeout bounds the "is an agent running here" probe. It only runs
	// after the debounce, so at most once per session per debounce window.
	signalAgentProbeTimeout = 2 * time.Second

	// signalFanOutTimeout bounds one outbound fan-out (network sends to every enabled channel).
	signalFanOutTimeout = 30 * time.Second
)

// signalGate holds the per-session clocks that keep the two false-positive defenses honest.
// Separate from the Session object because they are policy (server-side), not session state.
type signalGate struct {
	mu         sync.Mutex
	lastBell   map[string]time.Time
	lastNotify map[string]time.Time
}

func newSignalGate() *signalGate {
	return &signalGate{lastBell: map[string]time.Time{}, lastNotify: map[string]time.Time{}}
}

// allowBell reports whether a bell from this session may be considered now, consuming the
// debounce window when it does.
func (g *signalGate) allowBell(id string, now time.Time) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if last, ok := g.lastBell[id]; ok && now.Sub(last) < signalBellDebounce {
		return false
	}
	g.lastBell[id] = now
	return true
}

// allowNotify reports whether an OUTBOUND notification for this session may go out now,
// consuming the cooldown when it does.
func (g *signalGate) allowNotify(id string, now time.Time) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if last, ok := g.lastNotify[id]; ok && now.Sub(last) < signalNotifyCooldown {
		return false
	}
	g.lastNotify[id] = now
	return true
}

// clearNotify forgets a session's outbound cooldown, so the next attention event for it goes
// out immediately. Called when the USER ANSWERS (the session's agent goes idle/waiting →
// running, agent_notifier.go): a back-and-forth exchange should ping on every completion, not
// just the first one inside a 2-minute window. It re-arms both the poller and the explicit
// signal path — they share this clock precisely so they cannot both fire for one event.
func (g *signalGate) clearNotify(id string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.lastNotify, id)
	g.mu.Unlock()
}

// prune drops clocks for sessions that no longer exist. Called from the overview rebuild,
// which already knows the live set.
func (g *signalGate) prune(live map[string]bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for id := range g.lastBell {
		if !live[id] {
			delete(g.lastBell, id)
		}
	}
	for id := range g.lastNotify {
		if !live[id] {
			delete(g.lastNotify, id)
		}
	}
}

// onSessionSignal is SessionManager.OnSignal — the single entry point for every explicit
// signal.
//
// It runs ON THE PTY READ GOROUTINE, which is the terminal's output path: every millisecond
// spent here is a millisecond the user's terminal is not being read, and a long enough stall
// backs up the PTY and throttles the program itself. So only mutex-cheap work happens inline;
// anything that touches the process tree, tmux, or the network is handed to a goroutine.
func (s *Server) onSessionSignal(sess *Session, sig ansisignal.Signal) {
	if s == nil || sess == nil {
		return
	}
	now := nowFunc()

	if sig.Kind == ansisignal.KindBell {
		// Debounce FIRST (one mutex + one comparison), qualification second and off-thread.
		// Besides the stall, this ordering is what keeps a shell hammering the bell from
		// spawning a goroutine per beep: at most one per session per debounce window.
		if !s.signals.allowBell(sess.ID, now) {
			return
		}
		go func() {
			if s.hasAgent == nil {
				return
			}
			// TWO positive probes, not one. The process snapshot behind hasAgent deliberately
			// falls back to a STALE cache when its `ps` is slow or cancelled ("stale is better
			// than nil", process_inspector.go), and right after a server restart the OS recycles
			// the PIDs of everything the restart just killed — so a single probe can read a dead
			// process's identity into a brand-new shell and turn an ordinary shell beep into
			// "the agent needs you". Observed exactly once on the fixture and never reproduced
			// in 5 controlled attempts, which is the signature of a one-frame fluke rather than
			// a logic error. A second probe after the snapshot has certainly been refreshed
			// cannot inherit the same stale frame, so the class stops being possible instead of
			// merely being unlikely. Costs one second on the first bell of a window — nothing,
			// for a signal whose consumer is a human deciding whether to look.
			if !s.hasAgent(sess) {
				return
			}
			select {
			case <-time.After(signalBellConfirmDelay):
			case <-sess.Done():
				return // shell exited meanwhile; nobody is asking for anything
			}
			if !s.hasAgent(sess) {
				return
			}
			s.recordSignal(sess, sig, now)
		}()
		return
	}

	// An OSC notification needs no qualification, and recording is two mutex operations —
	// cheap enough to stay inline, which also makes it visible to the very next WS tick.
	s.recordSignal(sess, sig, now)
}

// recordSignal stores the signal as the session's pending needs-you state and starts the
// outbound fan-out when the per-session cooldown allows it.
func (s *Server) recordSignal(sess *Session, sig ansisignal.Signal, now time.Time) {
	seq := sess.RecordSignal(sig, now)
	logger.Info("explicit agent signal",
		"session_id", sess.ID, "kind", string(sig.Kind), "title", sig.Title, "seq", seq)

	if s.signals.allowNotify(sess.ID, now) {
		go s.fanOutSignal(sess, sig)
	}
}

// sessionHasAgent reports whether an AI agent is actually running in this session — the
// qualification that separates "the agent is asking for you" from "bash beeped".
func (s *Server) sessionHasAgent(sess *Session) bool {
	pid := sess.ShellPID()
	if pid <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), signalAgentProbeTimeout)
	defer cancel()

	// Normal case: the agent is a descendant of this session's shell. Asked through the same
	// tracker the overview uses (one definition of "an agent is here"), and backed by the
	// shared 3s ps snapshot it already refreshes every second, so this is usually free.
	if s.sessionAgent.Tool(ctx, pid) != agentintel.ToolNone {
		return true
	}

	// tmux case: the session's shell is only a `tmux attach` CLIENT — the agent runs under
	// the tmux SERVER and is therefore NOT in this shell's process tree, so the probe above
	// structurally cannot see it. Without this arm the feature would work in a plain
	// terminal and silently do nothing under tmux: the exact "works in one shell, invisible
	// in the other" drift this codebase keeps paying for. Only reached for tmux sessions and
	// only after the debounce, so the tmux round-trip is at worst once per window.
	if !sess.GetTmuxDetected() || s.tmuxProvider == nil {
		return false
	}
	raw, err := s.tmuxProvider.TmuxState(ctx, pid)
	if err != nil || raw == nil {
		return false
	}
	var st agentintel.TmuxState
	if json.Unmarshal(raw, &st) != nil {
		return false
	}
	for _, tsess := range st.Sessions {
		for _, win := range tsess.Windows {
			for _, pane := range win.Panes {
				if pane.AgentTool != agentintel.ToolNone {
					return true
				}
			}
		}
	}
	return false
}

// fanOutSignal delivers the signal to every enabled notification channel (phone/IM) through
// the same coordinator the tmux notifier uses — one delivery path, one set of metrics.
func (s *Server) fanOutSignal(sess *Session, sig ansisignal.Signal) {
	if s.coordinator == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), signalFanOutTimeout)
	defer cancel()
	// Nothing enabled → don't manufacture an event nobody receives (it would still land in
	// the delivery metrics as a fan-out with zero results and muddy the troubleshooting view).
	if !s.coordinator.AnyEnabled(ctx) {
		return
	}

	title, body := sanitizeFieldMax(sig.Title, 48), sanitizeFieldMax(sig.Body, 160)
	// The wording stays at "需要你": a bell says the program wants you, not WHETHER that is a
	// permission prompt or a finished turn. Claiming either would be inventing detail we do
	// not have — the same reason the overview marks this AwaitingUser (amber) and never
	// escalates it to the blocked/red status.
	headline := "🔔 需要你"
	if title != "" {
		headline = "🔔 " + title
	}
	tool := string(s.sessionAgent.Tool(ctx, sess.ShellPID()))
	summary := body
	if summary == "" {
		summary = "终端发出了通知信号（" + string(sig.Kind) + "）"
	}

	event := notify.Event{
		Title:  headline + " · " + sanitizeFieldMax(sessionTitle(sess), 48),
		Kind:   notify.KindWaiting,
		Counts: notify.Counts{Waiting: 1},
		Sessions: []notify.SessionRef{{
			Tool:        tool,
			Location:    sanitizeFieldMax(sessionTitle(sess), 48),
			Status:      "waiting",
			JustChanged: true,
		}},
		Summary: summary,
		DeepURL: s.notifyDeepURL(sess.Name),
	}
	rec := s.coordinator.Send(ctx, event)
	logger.Info("explicit signal notify fanned out",
		"session_id", sess.ID, "kind", string(sig.Kind), "providers", len(rec.Results))
}

// ── WS push ─────────────────────────────────────────────────────────────────────────────

// agentSignalsJSON is the pushed `agent_signal` payload: every session with an unanswered
// signal. Returned as raw JSON so each writer can byte-compare it against its last push and
// skip identical frames — the same diff suppression tmux_state and sessions_overview use.
//
// Entries are sorted by session id so the bytes depend only on the CONTENT. Leaving the order
// to sync.Map iteration would make the diff suppression depend on an implementation detail of
// the standard library — the day that order stops being stable, every client gets a frame
// every second and nobody connects the two.
func (s *Server) agentSignalsJSON() []byte {
	sessions := s.mgr.List()
	entries := make([]AgentSignalEntry, 0, len(sessions))
	for _, sess := range sessions {
		sig, at, seq, ok := sess.PendingSignal()
		if !ok {
			continue
		}
		entries = append(entries, AgentSignalEntry{
			SessionID: sess.ID,
			Kind:      string(sig.Kind),
			Title:     sig.Title,
			Body:      sig.Body,
			At:        at.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
			Seq:       seq,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].SessionID < entries[j].SessionID })
	raw, err := json.Marshal(AgentSignalPayload{Signals: entries})
	if err != nil {
		return nil
	}
	return raw
}

// emptyAgentSignals is what a connection starts out believing. Seeding each writer's diff
// state with it means a client that attaches to a quiet machine gets no frame at all, while
// one attaching with a signal already pending gets it on the first tick (a bell that rang
// while the tab was closed is still there when it comes back).
var emptyAgentSignals = func() []byte {
	raw, _ := json.Marshal(AgentSignalPayload{Signals: []AgentSignalEntry{}})
	return raw
}()

// ── clearing: the user answered ─────────────────────────────────────────────────────────

// deviceReplyRe matches the answers a TERMINAL sends back on its own initiative: Device
// Attributes, Device Status Report / cursor position, kitty keyboard flags, DECRPM, OSC color
// and DCS replies. They travel on the same input path as keystrokes but no human typed them.
//
// This is the mirror of replay_strip.go's deviceQueryRe (that one strips the QUERIES from a
// replay; this one recognises the ANSWERS on the way in). Without it, a TUI probing the
// terminal a moment after the bell would silently dismiss the signal the user never saw.
var deviceReplyRe = regexp.MustCompile(
	"\x1b\\[[?>=]?[0-9;]*[Rcnu]" + // CSI … R|c|n|u — cursor position, DA, DSR, kitty flags
		"|\x1b\\[\\?[0-9;]*\\$y" + // CSI ? … $y   — DECRPM (mode report)
		"|\x1b\\][0-9;]*;[^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC … BEL|ST — color/clipboard replies
		"|\x1bP[^\x1b]*\x1b\\\\", // DCS … ST     — XTGETTCAP / XTVERSION replies
)

// noteUserInput clears the session's pending signal when the incoming bytes are a HUMAN
// answering. Called from every input path (WS binary, WS text, HTTP POST) — an iOS client
// posts its keystrokes over HTTP, so clearing only on the WebSocket would leave those users
// with a dot that never goes away.
//
// Everything counts as human input EXCEPT recognised machine replies: that direction of
// doubt is deliberate. Failing to clear costs one stale dot the next keystroke fixes;
// clearing wrongly silently destroys the notification the whole feature exists to deliver.
func noteUserInput(sess *Session, data []byte) {
	if sess == nil || len(data) == 0 {
		return
	}
	if bytes.IndexByte(data, 0x1b) >= 0 && len(bytes.TrimSpace(deviceReplyRe.ReplaceAll(data, nil))) == 0 {
		return // nothing but terminal-generated replies
	}
	sess.ClearSignal()
}
