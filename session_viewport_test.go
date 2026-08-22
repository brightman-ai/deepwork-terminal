package terminal

import (
	"testing"
	"time"
)

// TestViewportMinimumIsComponentWise is the pure rule, with no daemon in the way.
//
// The expected values deliberately match NEITHER input: 80×30 is not viewer A's box and it
// is not viewer B's. That is the whole point of a component-wise minimum, and it is also
// what makes the assertion sensitive — an implementation that simply picked one viewer, or
// that let the last writer win, would produce one of the inputs and be caught here.
func TestViewportMinimumIsComponentWise(t *testing.T) {
	s := &Session{viewers: map[string]*viewer{
		"wide":     {cols: 100, rows: 30},
		"tall":     {cols: 80, rows: 50},
		"observer": {cols: 0, rows: 0},
	}}
	cols, rows := s.Viewport()
	if cols != 80 || rows != 30 {
		t.Fatalf("viewport = %dx%d, want 80x30 (min per axis, observers ignored)", cols, rows)
	}
}

// TestViewportIgnoresObservers: a session watched only by observers has no declared size,
// which is what lets the daemon's fallback apply and what stops an idle server from
// constraining a PTY nobody is looking at.
func TestViewportIgnoresObservers(t *testing.T) {
	s := &Session{viewers: map[string]*viewer{
		"a": {cols: 0, rows: 0},
		"b": {cols: 0, rows: 0},
	}}
	if cols, rows := s.Viewport(); cols != 0 || rows != 0 {
		t.Fatalf("viewport = %dx%d, want 0x0 — observers must impose no constraint", cols, rows)
	}
}

// TestViewportHalfWithdrawalIsFull: one zero is not "constrain the other axis only". No
// window ever means that, and treating it literally would let a viewport of 0×50 pin the
// session's rows while claiming no opinion on columns.
func TestViewportHalfWithdrawalIsFull(t *testing.T) {
	sess := &Session{ID: "x", viewers: map[string]*viewer{"a": {cols: 90, rows: 40}}}
	if err := sess.SetViewerSize("a", 0, 40); err != nil {
		t.Fatalf("SetViewerSize: %v", err)
	}
	if cols, rows := sess.Viewport(); cols != 0 || rows != 0 {
		t.Fatalf("viewport = %dx%d, want 0x0 — half a withdrawal is a withdrawal", cols, rows)
	}
}

// TestTwoBrowsersSmallestWins is the endgame end-to-end: two browsers on ONE server, whose
// sizes the daemon cannot see, must still be aggregated before the PTY is sized.
//
// This is the case the previous design got wrong. The server kept one shared size, so
// whichever browser resized last simply overwrote the other — open a phone on a session and
// the desktop already watching it reflowed to 40 columns.
//
// READ THIS BEFORE TRUSTING THE NAME. It subscribes directly, and in doing so it steps past
// SetActiveConn — the admission rule that still allows exactly ONE WebSocket per session and
// preempts the previous one ("Another client connected"). So two browsers cannot in fact be
// attached at once today, and this test exercises the aggregation MECHANISM, not a situation
// live users can currently reach.
//
// The mechanism is not therefore idle: with one viewer the minimum is that viewer, and the
// half that matters every day is withdrawal (the two tests below). Lifting the one-socket
// rule is a product decision — it changes what happens when you open a second device — and
// smallest-wins is what would make it safe, since the two windows would letterbox instead of
// fighting. That decision is not this test's to make.
func TestTwoBrowsersSmallestWins(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("two-browsers")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, _, unsubDesktop := sm.Subscribe(sess, "desktop")
	defer unsubDesktop()
	_, _, _, unsubPhone := sm.Subscribe(sess, "phone")
	defer unsubPhone()

	if err := sess.SetViewerSize("desktop", 100, 30); err != nil {
		t.Fatalf("desktop declare: %v", err)
	}
	waitForPTYSize(t, sess, 100, 30, 10*time.Second)

	// The phone is narrower but taller. Neither window's box is the answer.
	if err := sess.SetViewerSize("phone", 80, 50); err != nil {
		t.Fatalf("phone declare: %v", err)
	}
	waitForPTYSize(t, sess, 80, 30, 10*time.Second)
}

// TestClosingABrowserGivesTheSizeBack: a constraint that cannot be withdrawn is a ratchet.
// The smallest window that ever looked at a session would own it forever, including long
// after it was closed.
func TestClosingABrowserGivesTheSizeBack(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("withdraw-on-close")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, _, unsubDesktop := sm.Subscribe(sess, "desktop")
	defer unsubDesktop()
	_, _, _, unsubPhone := sm.Subscribe(sess, "phone")

	if err := sess.SetViewerSize("desktop", 120, 40); err != nil {
		t.Fatalf("desktop declare: %v", err)
	}
	if err := sess.SetViewerSize("phone", 60, 20); err != nil {
		t.Fatalf("phone declare: %v", err)
	}
	waitForPTYSize(t, sess, 60, 20, 10*time.Second)

	// The phone closes its tab. Its size must go with its subscription — one registry
	// entry, so there is no way for the two to drift apart.
	unsubPhone()
	waitForPTYSize(t, sess, 120, 40, 10*time.Second)
}

// TestBackgroundTabWithdrawsWithoutDisconnecting covers the other half of withdrawal: a tab
// that is still connected and still wants output, but is no longer displaying the session.
// It has no vote on the size — otherwise a phone left open in a background tab would pin a
// desktop session to 60 columns indefinitely.
func TestBackgroundTabWithdrawsWithoutDisconnecting(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("background-tab")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ch, _, _, unsubDesktop := sm.Subscribe(sess, "desktop")
	defer unsubDesktop()
	_, _, _, unsubPhone := sm.Subscribe(sess, "phone")
	defer unsubPhone()

	if err := sess.SetViewerSize("desktop", 120, 40); err != nil {
		t.Fatalf("desktop declare: %v", err)
	}
	if err := sess.SetViewerSize("phone", 60, 20); err != nil {
		t.Fatalf("phone declare: %v", err)
	}
	waitForPTYSize(t, sess, 60, 20, 10*time.Second)

	if err := sess.SetViewerSize("phone", 0, 0); err != nil {
		t.Fatalf("phone withdraw: %v", err)
	}
	waitForPTYSize(t, sess, 120, 40, 10*time.Second)

	// The desktop must be TOLD, not left to infer it. A viewer that misses a resize paints
	// every subsequent screen onto the wrong grid with nothing to trigger a correction.
	if !sawResize(ch, 120, 40, 10*time.Second) {
		t.Fatal("the remaining viewer was never told the grid grew back")
	}
}

// TestAnonymousResizeLosesToAWatchingBrowser pins the fallback's semantics.
//
// An HTTP/service caller cannot see the session, so it cannot know what fits in the windows
// that can. Letting it win would re-open the exact defect per-attachment geometry closes —
// one client reflowing another's terminal — so it is honoured only while nothing is watching.
func TestAnonymousResizeLosesToAWatchingBrowser(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("anonymous-loses")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Nobody watching: the fallback is the only opinion, so it applies.
	if err := sess.RequestPTYSize(150, 45); err != nil {
		t.Fatalf("RequestPTYSize: %v", err)
	}
	waitForPTYSize(t, sess, 150, 45, 10*time.Second)

	_, _, _, unsub := sm.Subscribe(sess, "browser")
	defer unsub()
	if err := sess.SetViewerSize("browser", 90, 25); err != nil {
		t.Fatalf("browser declare: %v", err)
	}
	waitForPTYSize(t, sess, 90, 25, 10*time.Second)

	// Now the same call must NOT move the grid the browser is drawing into.
	if err := sess.RequestPTYSize(150, 45); err != nil {
		t.Fatalf("RequestPTYSize while watched: %v", err)
	}
	// Give a wrong implementation time to be wrong: without a wait this passes by simply
	// reading the value before the bad write lands.
	time.Sleep(300 * time.Millisecond)
	if cols, rows := sess.PTYSize(); cols != 90 || rows != 25 {
		t.Fatalf("size = %dx%d after an anonymous resize, want 90x25 — the watching browser "+
			"must not be reflowed by a caller that cannot see the session", cols, rows)
	}
}

// TestPTYSizeReportsTheDaemonsAnswerNotOurRequest: the recorded size feeds the Agent
// Overview's screen replay, which repaints scrollback onto a grid. Recording what we ASKED
// for means that in the one case this machinery exists for — two windows of different sizes
// — the replay grid describes a terminal that never existed.
func TestPTYSizeReportsTheDaemonsAnswerNotOurRequest(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("answer-not-request")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _, _, unsubSmall := sm.Subscribe(sess, "small")
	defer unsubSmall()
	_, _, _, unsubBig := sm.Subscribe(sess, "big")
	defer unsubBig()

	if err := sess.SetViewerSize("small", 70, 20); err != nil {
		t.Fatalf("small declare: %v", err)
	}
	waitForPTYSize(t, sess, 70, 20, 10*time.Second)

	// This viewer asks for 200x60 and will not get it — the other window cannot show it.
	if err := sess.SetViewerSize("big", 200, 60); err != nil {
		t.Fatalf("big declare: %v", err)
	}

	// The assertion is "200x60 must NEVER appear", not "70x20 is showing after a sleep".
	//
	// That distinction is not pedantry — the sleeping version of this test was GREEN against
	// an implementation that wrote the requested size straight onto the session. The daemon's
	// genuine answer for the previous resize happened to arrive during the sleep and scrub
	// the bad value, so the test read a correct number produced by a broken path. 200x60 is
	// the value only a request-path write can produce: the daemon never chooses it, so seeing
	// it even once is proof, and never seeing it cannot be faked by timing.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cols, rows := sess.PTYSize(); cols == 200 || rows == 60 {
			t.Fatalf("size = %dx%d — that is what a viewer ASKED for; the recorded grid must "+
				"only ever be what the daemon says the session entered", cols, rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// …and it must still be the size the session is really running at.
	waitForPTYSize(t, sess, 70, 20, 5*time.Second)
}

// TestResizeReachesViewersInStreamOrder: output and resizes share one channel so a client
// can tell which grid a run of bytes was drawn for. Two channels would leave that to a
// select, and for a TUI painting by absolute cursor address it decides whether the picture
// is right.
func TestResizeReachesViewersInStreamOrder(t *testing.T) {
	sess := &Session{ID: "order", viewers: map[string]*viewer{}}
	v := &viewer{ch: make(chan viewerFrame, viewerControlReserve+8)}
	sess.viewers["a"] = v

	sess.fanOutData([]byte("BEFORE"))
	sess.applyGrid(80, 24)
	sess.fanOutData([]byte("AFTER"))

	var got []string
	for i := 0; i < 3; i++ {
		select {
		case f := <-v.ch:
			if f.Resize != nil {
				got = append(got, "resize")
			} else {
				got = append(got, string(f.Data))
			}
		default:
			t.Fatalf("only %d frames delivered, want 3", i)
		}
	}
	if got[0] != "BEFORE" || got[1] != "resize" || got[2] != "AFTER" {
		t.Fatalf("stream order = %v, want [BEFORE resize AFTER]", got)
	}
}

// TestControlFramesAreNeverEvictedForLaterOnes is the backpressure policy.
//
// "When full, discard the oldest frame" looks like it protects control frames — it always
// makes room for the newest one. It does not: the frame it discards can BE a control frame.
// Queue [resize→80, bytes drawn at 80] and admit resize→100 that way and you get
// [bytes-at-80, resize→100]: the viewer paints those bytes onto whatever grid it was on
// before, which it was never told to leave. That is a reordering, not a thinning.
//
// So output is refused at the TAIL while the top of the queue is reserved, and nothing that
// is already queued is ever removed.
func TestControlFramesAreNeverEvictedForLaterOnes(t *testing.T) {
	sess := &Session{ID: "backpressure", viewers: map[string]*viewer{}}
	// Deliberately small: four output slots under a full reserve, so the queue is driven
	// past its output capacity by the loop below with no room to spare.
	v := &viewer{ch: make(chan viewerFrame, viewerControlReserve+4)}
	sess.viewers["stuck"] = v

	sess.applyGrid(80, 24)
	for i := 0; i < 40; i++ { // far more output than the queue can hold
		sess.fanOutData([]byte("bytes drawn at 80"))
	}
	sess.applyGrid(100, 30)

	var grids [][2]int
	var outputs int
	for len(v.ch) > 0 {
		f := <-v.ch
		if f.Resize != nil {
			grids = append(grids, *f.Resize)
		} else {
			outputs++
		}
	}
	if len(grids) != 2 {
		t.Fatalf("got %d resize frames %v, want both — the earlier one was sacrificed for "+
			"the later one, so the output queued between them paints onto a grid the viewer "+
			"was never given", len(grids), grids)
	}
	if grids[0] != [2]int{80, 24} || grids[1] != [2]int{100, 30} {
		t.Fatalf("grids arrived as %v, want [[80 24] [100 30]] in that order", grids)
	}
	if outputs == 0 {
		t.Fatal("no output got through at all — the reserve swallowed the whole queue")
	}
}

// TestAViewerThatDoesNotGetWhatItAskedForIsTold covers the answer nobody was sending.
//
// A viewer asking for a size it will not get moves neither the aggregate nor the session, so
// no resize is broadcast — and it has already fitted its own terminal to what it asked for.
// Without a direct answer it paints every later screen onto a grid the session never entered,
// with nothing scheduled to correct it. This is the case a "broadcast on change" design
// silently cannot cover: there is no change.
func TestAViewerThatDoesNotGetWhatItAskedForIsTold(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("answer-the-asker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _, _, unsubSmall := sm.Subscribe(sess, "small")
	defer unsubSmall()
	if err := sess.SetViewerSize("small", 70, 20); err != nil {
		t.Fatalf("small declare: %v", err)
	}
	waitForPTYSize(t, sess, 70, 20, 10*time.Second)

	big, _, _, unsubBig := sm.Subscribe(sess, "big")
	defer unsubBig()
	if err := sess.SetViewerSize("big", 200, 60); err != nil {
		t.Fatalf("big declare: %v", err)
	}
	if !sawResize(big, 70, 20, 5*time.Second) {
		t.Fatal("the viewer that lost was never told what it actually got; its terminal is " +
			"fitted to 200x60 and the session is 70x20, and nothing will correct it")
	}
}

// TestSubscribeReadsTheGridUnderTheSameLockAsRegistering is the subscribe/resize race,
// asserted as the invariant rather than as a stress loop.
//
// The hazard: a browser is told the grid once, before its replay. Read that value separately
// from registering the viewer and a resize landing in between makes the announcement NEWER
// than frames already queued for it — the browser sets its terminal to the new grid and is
// then sent bytes drawn at the old one. For a TUI painting by absolute cursor address that is
// a scrambled screen, and it looks like a bug in the program inside.
//
// This is asserted structurally because the stress version could not be made to fail: even
// with 256 bystanders widening applyGrid's hand-off loop and 4000 join attempts, the broken
// ordering stayed green. A race test that cannot lose is not evidence, so this holds the
// grid's lock and checks that a joining viewer is FORCED to wait — which is the property,
// stated directly and with no timing to get lucky with.
func TestSubscribeReadsTheGridUnderTheSameLockAsRegistering(t *testing.T) {
	sess := &Session{ID: "atomic-join", viewers: map[string]*viewer{}}
	sm := &SessionManager{}

	// Stand in for applyGrid holding the grid mid-update.
	sess.mu.Lock()

	joined := make(chan struct{})
	go func() {
		_, _, _, unsub := sm.Subscribe(sess, "joiner")
		unsub()
		close(joined)
	}()

	// If Subscribe reads the grid INSIDE the viewer-registry lock, it is now parked holding
	// that lock, and anything else needing it must wait. If it reads the grid afterwards,
	// the registry lock is already free and this probe sails through.
	blocked := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		probe := make(chan struct{})
		go func() { sess.Viewport(); close(probe) }()
		select {
		case <-probe:
			time.Sleep(10 * time.Millisecond) // Subscribe may not have started yet; retry
		case <-time.After(200 * time.Millisecond):
			blocked = true
		}
		if blocked {
			break
		}
	}
	sess.mu.Unlock()
	<-joined

	if !blocked {
		t.Fatal("a viewer registered and then read the grid separately — a resize in between " +
			"leaves the announcement ahead of the frames already queued for that viewer")
	}
}

// sawResize drains up to timeout waiting for a resize frame announcing cols×rows.
func sawResize(ch <-chan viewerFrame, cols, rows int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return false
			}
			if f.Resize != nil && f.Resize[0] == cols && f.Resize[1] == rows {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
