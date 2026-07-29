package terminal

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/deepwork-terminal/ansisignal"
)

// These cover the two halves that decide whether the feature helps or annoys: the tap that
// turns PTY bytes into session state, and the false-positive defenses that keep a plain
// shell's beeping out of it.

// signalTestRig is a server whose sessions are pipe-backed, so a test can push exact bytes
// through the REAL readLoop (not around it) and watch what the tap makes of them.
type signalTestRig struct {
	srv       *Server
	sm        *SessionManager
	writeEnds *SafeWriteEnds
	created   int
}

func newSignalTestRig(t *testing.T) *signalTestRig {
	t.Helper()
	factory, writeEnds := pipePTYFactoryFunc()
	sm := NewSessionManagerWithFactory(4096, "/bin/sh", factory)
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     testAuthCode,
		// A REAL DataDir would make the notify coordinator read the developer's own
		// channel config (and their iLink login). Temp dir = defaults = nothing enabled
		// that can reach the network.
		DataDir: t.TempDir(),
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.mgr = sm
	// NewServer wires this on the manager it built; the pipe-backed replacement needs the
	// same wiring or the tap under test is simply absent.
	sm.OnSignal = srv.onSessionSignal
	t.Cleanup(func() {
		sm.DestroyAll()
		writeEnds.closeAll()
	})
	return &signalTestRig{srv: srv, sm: sm, writeEnds: writeEnds}
}

// newSession creates a session and returns it with the pipe end that feeds its readLoop.
func (r *signalTestRig) newSession(t *testing.T, name string) (*Session, *os.File) {
	t.Helper()
	sess, err := r.sm.Create(name)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w := r.writeEnds.Get(r.created) // pipe ends are appended in creation order
	if w == nil {
		t.Fatal("no pipe write end for the new session")
	}
	r.created++
	return sess, w
}

func writePTY(t *testing.T, w *os.File, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("write to pty: %v", err)
	}
}

// waitForSignal polls the session for a pending signal (the tap is asynchronous — it runs on
// the PTY read goroutine).
func waitForSignal(t *testing.T, sess *Session) (ansisignal.Signal, bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sig, _, _, ok := sess.PendingSignal(); ok {
			return sig, true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ansisignal.Signal{}, false
}

func TestSignalTap_OSCNotificationBecomesPendingSignal(t *testing.T) {
	rig := newSignalTestRig(t)
	sess, w := rig.newSession(t, "worker")
	// Split across two writes on purpose: PTY read boundaries land anywhere, and this is
	// the end-to-end proof that the scanner's cross-chunk state survives the real path.
	writePTY(t, w, "\x1b]777;notify;Claude Code;")
	writePTY(t, w, "需要你的授权\x07")

	sig, ok := waitForSignal(t, sess)
	if !ok {
		t.Fatal("no signal recorded — an explicit OSC notification was dropped")
	}
	if sig.Kind != ansisignal.KindNotify || sig.Title != "Claude Code" || sig.Body != "需要你的授权" {
		t.Fatalf("recorded %#v, want a notify with that title/body", sig)
	}
}

func TestSignalTap_BellWithoutAgentIsIgnored(t *testing.T) {
	rig := newSignalTestRig(t)
	sess, w := rig.newSession(t, "plain-shell")
	// A bare BEL from a session with no agent running: readline does this for every
	// ambiguous tab-completion. It must NOT become a "needs you".
	writePTY(t, w, "\x07")
	time.Sleep(150 * time.Millisecond)
	if sig, _, _, ok := sess.PendingSignal(); ok {
		t.Fatalf("a plain shell's beep was recorded as %#v — this would flood the user", sig)
	}
}

func TestSignalTap_BellWithAgentIsRecorded(t *testing.T) {
	rig := newSignalTestRig(t)
	// The counterpart of the test above: with an agent running, the same bell IS the signal.
	// Without this, "no bell ever qualifies" would pass the whole suite. Set before the first
	// session exists — the same contract the production wiring honours.
	rig.srv.hasAgent = func(*Session) bool { return true }
	sess, w := rig.newSession(t, "agent-shell")
	writePTY(t, w, "\x07")

	sig, ok := waitForSignal(t, sess)
	if !ok {
		t.Fatal("an agent's bell was dropped — the only first-party 'I need you' we ever get")
	}
	if sig.Kind != ansisignal.KindBell {
		t.Fatalf("recorded %#v, want a bell", sig)
	}

	// Debounce: a second bell inside the window must not re-fire (a new seq would make the
	// client toast again).
	_, _, seq, _ := sess.PendingSignal()
	writePTY(t, w, "\x07")
	time.Sleep(150 * time.Millisecond)
	if _, _, seq2, _ := sess.PendingSignal(); seq2 != seq {
		t.Fatalf("bell seq advanced %d → %d inside the debounce window", seq, seq2)
	}
}

func TestSignalTap_OutputStreamIsNotConsumed(t *testing.T) {
	rig := newSignalTestRig(t)
	sess, w := rig.newSession(t, "passthrough")
	const raw = "before\x1b]9;hello\x07after\x07"
	writePTY(t, w, raw)
	if _, ok := waitForSignal(t, sess); !ok {
		t.Fatal("no signal recorded")
	}
	// The pure-observer contract: the browser must still receive every original byte,
	// including the BEL and the whole OSC sequence.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if string(sess.Buffer.ReadTail(4096)) == raw {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stream was altered: buffer = %q, want %q", string(sess.Buffer.ReadTail(4096)), raw)
}

func TestSignalGate_BellDebounceAndNotifyCooldown(t *testing.T) {
	g := newSignalGate()
	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	if !g.allowBell("s1", base) {
		t.Fatal("first bell must pass")
	}
	if g.allowBell("s1", base.Add(signalBellDebounce-time.Millisecond)) {
		t.Fatal("second bell inside the debounce window must be swallowed")
	}
	if !g.allowBell("s2", base) {
		t.Fatal("the debounce is per session, not global")
	}
	if !g.allowBell("s1", base.Add(signalBellDebounce)) {
		t.Fatal("bell after the window must pass again")
	}

	if !g.allowNotify("s1", base) {
		t.Fatal("first outbound notification must pass")
	}
	if g.allowNotify("s1", base.Add(signalNotifyCooldown-time.Second)) {
		t.Fatal("outbound notification inside the cooldown must be suppressed")
	}
	if !g.allowNotify("s1", base.Add(signalNotifyCooldown)) {
		t.Fatal("outbound notification after the cooldown must pass")
	}

	g.prune(map[string]bool{"s2": true})
	if !g.allowBell("s1", base.Add(time.Second)) {
		t.Fatal("prune must forget a destroyed session's clocks")
	}
}

func TestNoteUserInput(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		clear bool
	}{
		{"typed text", "y\r", true},
		{"single keystroke", "a", true},
		{"enter", "\r", true},
		{"ctrl-c", "\x03", true},
		{"arrow key", "\x1b[B", true}, // user-generated CSI: not a reply, must clear
		{"cursor position report", "\x1b[24;1R", false},
		{"device attributes reply", "\x1b[?62;c", false},
		{"device status report", "\x1b[0n", false},
		{"osc color reply", "\x1b]11;rgb:0000/0000/0000\x07", false},
		{"replies plus a keystroke", "\x1b[24;1Rq", true},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := &Session{ID: "s"}
			sess.RecordSignal(ansisignal.Signal{Kind: ansisignal.KindBell}, time.Now())
			noteUserInput(sess, []byte(c.in))
			_, _, _, pending := sess.PendingSignal()
			if cleared := !pending; cleared != c.clear {
				t.Fatalf("input %q: cleared = %v, want %v", c.in, cleared, c.clear)
			}
		})
	}
}

// TestAgentSignalsJSON pins the exact wire shape the frontend consumes, and the stability the
// diff suppression depends on (an array that reshuffled would push a frame every second).
func TestAgentSignalsJSON(t *testing.T) {
	rig := newSignalTestRig(t)
	srv := rig.srv
	a, _ := rig.newSession(t, "alpha")
	b, _ := rig.newSession(t, "beta")

	if got := string(srv.agentSignalsJSON()); got != `{"signals":[]}` {
		t.Fatalf("quiet machine payload = %s, want an empty signal list", got)
	}
	if string(srv.agentSignalsJSON()) != string(emptyAgentSignals) {
		t.Fatal("the empty payload must equal the seed a fresh connection starts with")
	}

	at := time.Date(2026, 7, 29, 12, 34, 56, 789000000, time.UTC)
	seqA := a.RecordSignal(ansisignal.Signal{Kind: ansisignal.KindNotify, Title: "T", Body: "B"}, at)
	b.RecordSignal(ansisignal.Signal{Kind: ansisignal.KindBell}, at)

	var payload AgentSignalPayload
	raw := srv.agentSignalsJSON()
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, raw)
	}
	if len(payload.Signals) != 2 {
		t.Fatalf("want both sessions represented, got %s", raw)
	}
	if string(raw) != string(srv.agentSignalsJSON()) {
		t.Fatal("payload bytes are unstable between calls — diff suppression would never hold")
	}
	var found AgentSignalEntry
	for _, e := range payload.Signals {
		if e.SessionID == a.ID {
			found = e
		}
	}
	if found.Kind != "notify" || found.Title != "T" || found.Body != "B" || found.Seq != seqA {
		t.Fatalf("entry = %#v, want the recorded notify with seq %d", found, seqA)
	}
	if found.At != "2026-07-29T12:34:56.789Z" {
		t.Fatalf("At = %q, want an RFC3339 millisecond timestamp", found.At)
	}

	a.ClearSignal()
	b.ClearSignal()
	if got := string(srv.agentSignalsJSON()); got != `{"signals":[]}` {
		t.Fatalf("after clearing, payload = %s — the client would never drop the dot", got)
	}
}

// TestSignalRaisesAwaitingUser proves the semantic landing: an explicit signal reaches the
// overview card as needs-you (amber), which is what the frontend renders.
func TestSignalRaisesAwaitingUser(t *testing.T) {
	rig := newSignalTestRig(t)
	sess, _ := rig.newSession(t, "worker")
	sess.RecordSignal(ansisignal.Signal{Kind: ansisignal.KindNotify, Title: "done"}, time.Now())

	entries := rig.srv.sessionsOverview(context.Background())
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if !entries[0].AwaitingUser {
		t.Fatal("an explicit signal did not raise AwaitingUser — the card would stay silent")
	}
	if entries[0].AwaitingSince == "" {
		t.Fatal("AwaitingSince must carry the signal time: it is the key the seen layer dismisses against")
	}
	if entries[0].AgentStatus == "waiting" {
		t.Fatal("a signal must not be escalated to blocked/red — it does not say the agent is blocked")
	}
	// Provenance: a card raised by a BEL/OSC must say SO, not inherit whatever rule the
	// screen detectors last produced. Without this, "why did this light up" is unanswerable
	// after the fact for the one source that never guesses.
	if entries[0].StatusRule != string(agentintel.RuleSignalNotify) {
		t.Fatalf("statusRule = %q, want the explicit-signal rule", entries[0].StatusRule)
	}
	if !strings.Contains(entries[0].StatusEvidence, "done") {
		t.Fatalf("statusEvidence = %q, want the signal's own text", entries[0].StatusEvidence)
	}
}
