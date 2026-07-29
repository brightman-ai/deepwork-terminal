package terminal

import (
	"context"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// These cover the PTY source added to the notifier: the transition that makes a non-tmux
// user's phone ring at all, and the two ways it must NOT ring (twice for one event, or
// never again after the first).

// notifierRig is a notifier with no goroutine attached — tick() is driven by hand and the
// world is handed in, so the transition rule can be tested without a real agent process.
func newNotifierRig() *agentNotifier {
	// A real (empty) manager, so the PTY source genuinely reports "no sessions" rather than
	// being skipped — the sweep must be able to tell those two apart.
	srv := &Server{signals: newSignalGate(), mgr: NewSessionManager(4096, "/bin/sh")}
	return &agentNotifier{
		server:       srv,
		prev:         map[string]agentintel.AgentStatus{},
		meta:         map[string]targetMeta{},
		lastNotified: map[string]time.Time{},
		pending:      map[string]bool{},
		baseline:     map[string]agentintel.SessionSummary{},
		archived:     map[string]archivedRec{},
	}
}

func sessionEntry(id, tool, status string) SessionOverviewEntry {
	return SessionOverviewEntry{ID: id, Title: "终端 1", CWD: "/nonexistent-project", AgentTool: tool, AgentStatus: status}
}

// feed runs one PTY-source pass over the given entries.
func (n *agentNotifier) feed(t *testing.T, at time.Time, entries ...SessionOverviewEntry) {
	t.Helper()
	n.observeSessions(entries, at, agentintel.NewProjectLocator(), map[string]bool{})
}

func TestNotifierSessionSource_TurnEndTriggers(t *testing.T) {
	n := newNotifierRig()
	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	// A fresh agent seen for the first time while already idle must NOT fire: we did not
	// witness a turn end, and firing here is how a server restart notifies about nothing.
	n.feed(t, base, sessionEntry("s1", "codex", "idle"))
	if len(n.pending) != 0 {
		t.Fatalf("startup-existing idle fired: %v", n.pending)
	}

	n.feed(t, base.Add(time.Second), sessionEntry("s1", "codex", "running"))
	if len(n.pending) != 0 {
		t.Fatalf("a running agent fired: %v", n.pending)
	}

	// The event this whole source exists for: it finished, nobody is watching the tab.
	n.feed(t, base.Add(2*time.Second), sessionEntry("s1", "codex", "idle"))
	if !n.pending[ptyKey("s1")] {
		t.Fatalf("running→idle did not queue a notification: %v", n.pending)
	}
	m := n.meta[ptyKey("s1")]
	if m.tool != "codex" || m.location != "终端 1" {
		t.Fatalf("meta = %+v, want the tab's tool + title as its address", m)
	}
	// The tmux source's keys must stay reachable alongside: one namespace, no collisions.
	if _, isPTY := ptySessionID("main:0:0"); isPTY {
		t.Fatal("a tmux pane id was mistaken for a PTY session id")
	}
}

func TestNotifierSessionSource_SharesCooldownWithExplicitSignal(t *testing.T) {
	n := newNotifierRig()
	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	// The agent rang a bell (session_signal.go already fanned that out and consumed the
	// session's outbound clock) and one second later its transcript shows the turn ended.
	// That is ONE event; the phone must buzz once, not twice.
	if !n.server.signals.allowNotify("s1", base) {
		t.Fatal("the explicit path could not take the clock")
	}
	n.feed(t, base.Add(time.Second), sessionEntry("s1", "claude", "running"))
	n.feed(t, base.Add(2*time.Second), sessionEntry("s1", "claude", "idle"))
	if len(n.pending) != 0 {
		t.Fatalf("the poller re-notified an event the bell already reported: %v", n.pending)
	}

	// Same clock in the other direction: the poller fires first, the bell is then swallowed.
	n2 := newNotifierRig()
	n2.feed(t, base, sessionEntry("s2", "claude", "running"))
	n2.feed(t, base.Add(time.Second), sessionEntry("s2", "claude", "waiting"))
	if !n2.pending[ptyKey("s2")] {
		t.Fatal("running→waiting did not queue a notification")
	}
	if n2.server.signals.allowNotify("s2", base.Add(2*time.Second)) {
		t.Fatal("an explicit signal went out on top of the poller's — two messages, one event")
	}
}

func TestNotifierSessionSource_CooldownClearsWhenUserAnswers(t *testing.T) {
	n := newNotifierRig()
	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	n.feed(t, base, sessionEntry("s1", "claude", "running"))
	n.feed(t, base.Add(time.Second), sessionEntry("s1", "claude", "idle"))
	if !n.pending[ptyKey("s1")] {
		t.Fatal("first completion did not queue")
	}
	n.pending = map[string]bool{} // pretend the batch flushed

	// The user replies: idle→running. A back-and-forth must ping on every turn, so the next
	// completion has to come through even though the 2-minute cooldown has not elapsed.
	n.feed(t, base.Add(2*time.Second), sessionEntry("s1", "claude", "running"))
	n.feed(t, base.Add(3*time.Second), sessionEntry("s1", "claude", "idle"))
	if !n.pending[ptyKey("s1")] {
		t.Fatal("after the user replied, the next completion was swallowed by a stale cooldown")
	}

	// And with no reply in between, the cooldown does hold.
	n.pending = map[string]bool{}
	n.prev[ptyKey("s1")] = agentintel.StatusRunning // a running→idle edge without a preceding reply
	n.feed(t, base.Add(4*time.Second), sessionEntry("s1", "claude", "idle"))
	if len(n.pending) != 0 {
		t.Fatalf("the per-session cooldown did not hold: %v", n.pending)
	}
}

// TestNotifierSweep pins what a vanished target means per source: a tab the manager no
// longer lists is genuinely gone, while a tmux read that FAILED says nothing about its panes
// — filing those as closed would inflate the next notification's archive count.
func TestNotifierSweep(t *testing.T) {
	n := newNotifierRig()
	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	n.feed(t, base, sessionEntry("s1", "claude", "running"))
	n.prev["main:0:0"] = agentintel.StatusRunning // a tmux pane tracked from an earlier tick
	n.meta["main:0:0"] = targetMeta{tool: "claude", cwd: "/nonexistent-project", session: "main"}

	// The session manager lists nothing (the tab really is closed) and the tmux source cannot
	// be read at all (no provider) — the two must be treated differently.
	n.tick(context.Background())
	if _, ok := n.prev[ptyKey("s1")]; ok {
		t.Fatal("a session that disappeared kept its tracked status")
	}
	if _, ok := n.prev["main:0:0"]; !ok {
		t.Fatal("an unreadable tmux topology was treated as proof its panes closed")
	}
	if len(n.archived) != 0 {
		t.Fatalf("a live pane was archived on a failed read: %v", n.archived)
	}
}

// TestEnsureNotifierWithoutTmux is the regression that started this: the notifier used to
// refuse to start at all without tmux, so a non-tmux user got no completion notification
// ever. It must now start, tick, and stop cleanly on a machine with no tmux at all.
func TestEnsureNotifierWithoutTmux(t *testing.T) {
	srv, err := NewServer(WithConfig(Config{
		Addr: ":0", DefaultShell: "/bin/sh", BufferSize: 4096, MaxSessions: 10,
		AuthCode: testAuthCode, DataDir: t.TempDir(),
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// No provider at all = the "tmux is not installed" world (tmuxInstalled reads false).
	srv.tmuxProvider = nil
	if srv.tmuxInstalled() {
		t.Fatal("test precondition: the rig must look like a machine without tmux")
	}

	srv.ensureNotifier()
	if !srv.notifierRunning() {
		t.Fatal("the notifier refused to start without tmux — non-tmux users get no notifications")
	}
	srv.notifierMu.Lock()
	n := srv.notifier
	srv.notifierMu.Unlock()
	n.tick(context.Background()) // must not touch the nil provider
	srv.stopNotifier()
	if srv.notifierRunning() {
		t.Fatal("stopNotifier left the poller running")
	}
}

// TestSessionRefLocation pins how a PTY session addresses itself in a message: a tab has no
// tmux coordinates, and rendering "窗口0 · 面板0" for it would invent a topology.
func TestSessionRefLocation(t *testing.T) {
	ref := sessionRef(liveSession{
		key: ptyKey("abc"), location: "终端 1", tool: "codex", status: agentintel.StatusIdle,
	}, true)
	if ref.Location != "终端 1" {
		t.Fatalf("location = %q, want the tab title alone", ref.Location)
	}
	// Two tabs can share a title; identity must come from the notifier's key, or they would
	// collapse into one entry in the batch.
	a := liveSession{key: ptyKey("a"), location: "终端", session: "终端"}
	b := liveSession{key: ptyKey("b"), location: "终端", session: "终端"}
	if paneKey(a) == paneKey(b) {
		t.Fatal("two different tabs produced the same batch identity")
	}
}
