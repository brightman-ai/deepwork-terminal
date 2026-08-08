package terminal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The non-tmux overview's whole value is that a card shows what the terminal is ACTUALLY doing.
// These lock in that the entry builder reads real ring content, strips the agent frame, and stays
// honest about sessions that have produced nothing.

func newOverviewTestServer(t *testing.T) (*Server, *SessionManager) {
	t.Helper()
	factory, writeEnds := pipePTYFactoryFunc()
	sm := NewSessionManagerWithFactory(4096, "/bin/sh", factory)
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     testAuthCode,
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.mgr = sm
	t.Cleanup(func() {
		sm.DestroyAll()
		writeEnds.closeAll()
	})
	return srv, sm
}

func TestSessionsOverview_CardCarriesLiveTail(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate PTY output landing in the ring — this is the exact path the writer uses.
	sess.Buffer.Write([]byte("compiling module\nall checks passed\n"))

	entries := srv.sessionsOverview(context.Background())
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.ID != sess.ID {
		t.Fatalf("entry id = %q, want %q", e.ID, sess.ID)
	}
	if len(e.Tail) == 0 {
		t.Fatal("card has no tail — the overview would again be strictly worse than the tab strip")
	}
	joined := strings.Join(e.Tail, "\n")
	if !strings.Contains(joined, "all checks passed") {
		t.Fatalf("tail lost the most recent output: %q", e.Tail)
	}
}

// REGRESSION: the first cut read the ring buffer directly and handed raw PTY bytes to a line
// splitter. The card then rendered cursor-positioning soup ("\x1b[20;2H\x1b[0m\x1b[K…") instead of
// text — caught only on a real 8087 session, never by the pure chrome-stripping tests. The fix
// composes Session.TailOutput (which owns CSI/OSC decoding) before chrome stripping.
func TestSessionsOverview_TailIsDecodedNotRawEscapes(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("ansi")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Colored + cursor-positioned output, exactly what a real PTY writes.
	// CSI colour, an OSC title, and two-char escapes (ESC = keypad, ESC ( B charset) — a real zsh
	// prompt emits all three families. Both texts are written on ADJACENT rows: with a real screen
	// model, text positioned 20 rows apart genuinely would not share the card's last-N-line window
	// (that is correct terminal behavior, verified in screen_test.go, not something to assert here).
	sess.Buffer.Write([]byte("\x1b[32mbuild ok\x1b[0m\r\n\x1b]0;title\x07\x1b[Kdone\x1b=\x1b(B\r\n"))

	entries := srv.sessionsOverview(context.Background())
	joined := strings.Join(entries[0].Tail, "\n")
	if strings.Contains(joined, "\x1b") {
		t.Fatalf("escape sequences leaked into the card: %q", entries[0].Tail)
	}
	if !strings.Contains(joined, "build ok") || !strings.Contains(joined, "done") {
		t.Fatalf("decoding lost the actual text: %q", entries[0].Tail)
	}
}

func TestSessionsOverview_SilentSessionHasEmptyTail(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("quiet"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entries := srv.sessionsOverview(context.Background())
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	// Empty, not fabricated padding — the card renders "no recent output" from this.
	if len(entries[0].Tail) != 0 {
		t.Fatalf("a session that printed nothing must have an empty tail, got %q", entries[0].Tail)
	}
}

func TestSessionsOverview_CoversEverySessionNotJustActive(t *testing.T) {
	// The entire point: a non-tmux user only has a WS on the ACTIVE tab, so the frame must
	// describe the others too or the overview can never show them.
	srv, sm := newOverviewTestServer(t)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := sm.Create(name); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	if got := len(srv.sessionsOverview(context.Background())); got != 3 {
		t.Fatalf("want all 3 sessions in the frame, got %d", got)
	}
}

func TestSessionsOverviewJSON_IsStableForDiffSuppression(t *testing.T) {
	// The 1s ticker only pushes when the bytes change; unstable marshalling would push every
	// second forever and defeat the whole "quiet machine pushes nothing" design.
	//
	// Marshals the builder directly rather than going through sessionsOverviewJSON, whose per-tick
	// cache would mask a genuine content change inside the TTL (that cache has its own test below).
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("steady state\r\n"))

	marshal := func() string {
		b, mErr := json.Marshal(srv.sessionsOverview(context.Background()))
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		return string(b)
	}

	if first, second := marshal(), marshal(); first != second {
		t.Fatalf("unchanged state produced differing JSON:\n%s\n%s", first, second)
	}
	before := marshal()
	sess.Buffer.Write([]byte("new line appeared\r\n"))
	if after := marshal(); after == before {
		t.Fatal("new output did NOT change the payload — the card would never update")
	}
}

func TestSessionsOverviewJSON_SharesOneSnapshotPerTick(t *testing.T) {
	// The payload is global but every connected WS writer asks for it, so with one surface mounted
	// per terminal the same snapshot would be rebuilt N times a second. Within a tick, all callers
	// must get the SAME bytes off one computation.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("first\r\n"))

	first := string(srv.overviewSnapshot(context.Background()).json)

	// Content changes, but we are still inside the same tick → callers keep sharing the snapshot.
	sess.Buffer.Write([]byte("second\r\n"))
	if cached := string(srv.overviewSnapshot(context.Background()).json); cached != first {
		t.Fatal("a second caller within the same tick recomputed instead of sharing the snapshot")
	}

	// Past the TTL the next tick recomputes and the new output shows up.
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	fresh := string(srv.overviewSnapshot(context.Background()).json)
	if fresh == first {
		t.Fatal("cache never expired — the overview would freeze")
	}
	if !strings.Contains(fresh, "second") {
		t.Fatalf("refreshed snapshot lost the new output: %s", fresh)
	}
}

// ── I2「变化才有代价」 ────────────────────────────────────────────────────────────────────────
//
// The two halves of the invariant, one test each, because they fail independently: a revision that
// moves without a change makes every connection push an identical frame forever; a revision that
// does NOT move on a real change freezes the UI, which is far worse and completely silent.

func TestOverviewRevision_MovesOnlyWhenTheAnswerMoves(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("steady\r\n"))

	first := srv.overviewSnapshot(context.Background())
	if first.revision == 0 {
		t.Fatal("revision 0 is the never-seen sentinel — a real snapshot must not carry it")
	}

	// A REBUILD with no change. Expire the TTL so this is genuinely a second computation, not the
	// cache being served: the point is that recomputing an identical answer is not an event.
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	same := srv.overviewSnapshot(context.Background())
	if same.revision != first.revision {
		t.Fatalf("revision moved %d → %d with nothing changed — every connection would re-push an identical frame once a second",
			first.revision, same.revision)
	}

	sess.Buffer.Write([]byte("something happened\r\n"))
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	moved := srv.overviewSnapshot(context.Background())
	if moved.revision == first.revision {
		t.Fatal("revision did NOT move on real output — the card would never update again")
	}
	if moved.frame == nil || !strings.Contains(string(moved.frame), "something happened") {
		t.Fatalf("frame does not carry the new output: %s", moved.frame)
	}
	if !strings.Contains(string(moved.frame), MsgTypeSessionsOverview) {
		t.Fatalf("frame is not a %s control message: %s", MsgTypeSessionsOverview, moved.frame)
	}
}

func TestOverviewScreen_ReplaysOnlyWhenTheBufferMoved(t *testing.T) {
	// The expensive half of a card is replaying up to 128 KiB of ring onto a full grid. It used to
	// run for every session every tick regardless of whether that session had emitted a byte, so
	// the cost tracked wall clock and tab count rather than anything the user did.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("hello\r\n"))
	cols, rows := sess.PTYSize()

	first := srv.sessionScreen(sess.ID, sess.Buffer, cols, rows)
	again := srv.sessionScreen(sess.ID, sess.Buffer, cols, rows)
	// Identity, not equality: an equal-but-rebuilt grid is exactly the work being skipped, and
	// only pointer identity can tell the two apart.
	if len(first) == 0 || &first[0] != &again[0] {
		t.Fatal("an untouched buffer was replayed twice — the cost still tracks the clock, not the change")
	}

	sess.Buffer.Write([]byte("world\r\n"))
	after := srv.sessionScreen(sess.ID, sess.Buffer, cols, rows)
	if len(after) > 0 && len(first) > 0 && &after[0] == &first[0] {
		t.Fatal("new output served a stale screen — the card would show the past")
	}
	if !strings.Contains(strings.Join(after, "\n"), "world") {
		t.Fatalf("replayed screen lost the new output: %q", after)
	}

	// A resize repaints the SAME bytes onto a different grid, so the seq alone must not be taken
	// as permission to reuse.
	resized := srv.sessionScreen(sess.ID, sess.Buffer, cols/2, rows)
	if len(resized) > 0 && len(after) > 0 && &resized[0] == &after[0] {
		t.Fatal("a resized grid reused the old screen — the card would keep the old wrapping")
	}
}

func TestOverviewScreenCache_DropsClosedSessions(t *testing.T) {
	// The cached grid is the largest thing this file holds; keeping one per session that ever
	// existed is a leak that only shows up on a long-lived server.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("output\r\n"))
	srv.sessionsOverview(context.Background())

	srv.screenCacheMu.Lock()
	held := len(srv.screenCache)
	srv.screenCacheMu.Unlock()
	if held == 0 {
		t.Fatal("nothing cached — the reuse path is not being exercised at all")
	}

	sm.DestroyAll()
	srv.sessionsOverview(context.Background())

	srv.screenCacheMu.Lock()
	defer srv.screenCacheMu.Unlock()
	if len(srv.screenCache) != 0 {
		t.Fatalf("closed session's screen still cached: %d entries", len(srv.screenCache))
	}
}

func TestOverviewSnapshot_CallersCancellationDoesNotPoisonTheSharedRebuild(t *testing.T) {
	// The snapshot describes EVERY session and is served to EVERY caller, but it is built by
	// whichever connection ticks first. When that connection's context was the rebuild's context,
	// closing one tab poisoned everyone's answer — `ps` fails under a dead context, every agent
	// reads as ToolNone, and a well-formed payload claims no session is running an agent. Same lie
	// as an empty pane bar, different door.
	//
	// The dead caller itself is entitled to leave immediately (that is a separate invariant, pinned
	// below). What must NOT happen is the build it kicked off producing a degraded answer for the
	// clients that are still here.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("still here\r\n"))

	dead, cancel := context.WithCancel(context.Background())
	cancel() // the caller is already gone before the rebuild starts
	srv.overviewSnapshot(dead)

	// Whatever that dead caller got, the build it started must land intact for everyone else.
	deadline := time.Now().Add(5 * time.Second)
	var snap overviewSnapshot
	for time.Now().Before(deadline) {
		srv.overviewCacheMu.Lock()
		snap = srv.overviewCache
		srv.overviewCacheMu.Unlock()
		if snap.revision != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snap.entries) != 1 {
		t.Fatalf("the rebuild a dead caller started published %d entries, want 1 — it rode that caller's lifetime",
			len(snap.entries))
	}
	if snap.frame == nil || !strings.Contains(string(snap.frame), "still here") {
		t.Fatalf("frame lost the session's output: %s", snap.frame)
	}
}

func TestOverviewSnapshot_AWaiterLeavesWhenItsOwnCallerGivesUp(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Stand in for a rebuild that has wedged inside an unpreemptable read: an in-flight promise
	// that never completes. This is the state the whole design has to survive.
	stuck := &overviewBuild{done: make(chan struct{})}
	srv.overviewCacheMu.Lock()
	srv.overviewInFlight = stuck
	srv.overviewCacheAt = time.Time{} // expired, so callers take the miss path
	srv.overviewCacheMu.Unlock()
	defer close(stuck.done)

	gone, cancel := context.WithCancel(context.Background())
	done := make(chan overviewSnapshot, 1)
	go func() { done <- srv.overviewSnapshot(gone) }()

	// The caller's connection drops while the rebuild is still stuck.
	cancel()

	select {
	case <-done:
		// Left as soon as its own context ended, which is the entire point.
	case <-time.After(2 * time.Second):
		t.Fatal("a caller whose context was cancelled could not abandon a wedged rebuild — " +
			"this is the goroutine leak a mutex makes unavoidable")
	}
}

// A rebuild that keeps failing must retry at the healthy cadence, not continuously. Without an
// ATTEMPT stamp, a failure never refreshes overviewCacheAt, so the next caller starts another
// rebuild instantly — turning "slow but eventually right" into "permanently empty, at full CPU".
func TestOverviewSnapshot_AFailedRebuildDoesNotSpin(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A build attempt just happened and published nothing (the failure shape).
	srv.overviewCacheMu.Lock()
	srv.overviewAttemptAt = time.Now()
	srv.overviewCacheAt = time.Time{}
	srv.overviewCacheMu.Unlock()

	srv.overviewSnapshot(context.Background())

	srv.overviewCacheMu.Lock()
	spinning := srv.overviewInFlight != nil
	srv.overviewCacheMu.Unlock()
	if spinning {
		t.Fatal("a caller started a fresh rebuild immediately after a failed one — the server would " +
			"do nothing but retry for as long as the condition lasts")
	}

	// …and it does resume once the cadence allows it.
	srv.overviewCacheMu.Lock()
	srv.overviewAttemptAt = time.Now().Add(-2 * sessionsOverviewCacheTTL)
	srv.overviewCacheMu.Unlock()
	if snap := srv.overviewSnapshot(context.Background()); snap.revision == 0 {
		t.Fatal("the retry never produced a snapshot — the overview would stay empty for good")
	}
}
