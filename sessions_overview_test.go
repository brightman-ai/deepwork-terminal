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

	first := string(srv.sessionsOverviewJSON(context.Background()))

	// Content changes, but we are still inside the same tick → callers keep sharing the snapshot.
	sess.Buffer.Write([]byte("second\r\n"))
	if cached := string(srv.sessionsOverviewJSON(context.Background())); cached != first {
		t.Fatal("a second caller within the same tick recomputed instead of sharing the snapshot")
	}

	// Past the TTL the next tick recomputes and the new output shows up.
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	fresh := string(srv.sessionsOverviewJSON(context.Background()))
	if fresh == first {
		t.Fatal("cache never expired — the overview would freeze")
	}
	if !strings.Contains(fresh, "second") {
		t.Fatalf("refreshed snapshot lost the new output: %s", fresh)
	}
}
