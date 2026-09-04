package muxd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestStreamDoesNotChargeBytesItDropped is the regression for the one place scrollback
// could be lost permanently.
//
// The stream reader hands live bytes to a buffered channel and drops them when the
// consumer cannot keep up. `consumed` is the resume point a re-attach sends as `since`,
// so charging a dropped byte tells the daemon to skip it — and nothing ever re-syncs the
// server's ring afterwards, so that hole is replayed to every browser and every overview
// render for the life of the session, while the daemon's ring still holds the bytes.
//
// The assertion is end-to-end on purpose: fill the channel, then re-attach from the
// reported resume point and demand every byte back.
func TestStreamDoesNotChargeBytesItDropped(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	var writeEnd *os.File
	factory := func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, w, perr := os.Pipe()
		if perr != nil {
			return nil, nil, perr
		}
		writeEnd = w
		return r, nil, nil
	}
	d := NewDaemonWith(1<<25, -1, factory) // ring must outlast the whole write, or replay truncates
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	stream, err := c.Attach(id, AttachOptions{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Push far more FRAMES than the stream's 256-slot output buffer, and never read them;
	// everything past the buffer is dropped by the reader.
	//
	// Chunk size matches the daemon's PTY read buffer (32 KiB) on purpose: a pipe coalesces,
	// so writing many small chunks yields a handful of large reads and therefore a handful
	// of frames — the first version of this test wrote 700×64B and produced so few frames
	// that nothing was ever dropped. It passed while proving nothing, which is why the
	// precondition below is a Fatal rather than a comment.
	const chunks = 320
	const chunkSize = 32 * 1024
	var sent bytes.Buffer
	for i := 0; i < chunks; i++ {
		payload := bytes.Repeat([]byte{byte('a' + i%26)}, chunkSize)
		sent.Write(payload)
		if _, werr := writeEnd.Write(payload); werr != nil {
			t.Fatalf("write chunk %d: %v", i, werr)
		}
	}
	// Let the daemon read the pipe and the client drop what it cannot hold. The ring's
	// absolute offset is the daemon's own account of how much it has taken in.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := d.Get(id); ok && int64(s.ring.Seq()) >= int64(sent.Len()) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	resume := stream.Consumed()
	if resume >= int64(sent.Len()) {
		t.Fatalf("precondition: consumed=%d covers everything written (%d) — nothing was dropped, "+
			"so this test proves nothing", resume, sent.Len())
	}
	_ = stream.Close()

	// Re-attach exactly the way the server does, and demand the dropped bytes back.
	again, err := c.Attach(id, AttachOptions{Since: &resume})
	if err != nil {
		t.Fatalf("re-Attach: %v", err)
	}
	defer again.Close()

	var got bytes.Buffer
	readDeadline := time.After(5 * time.Second)
	want := sent.Len() - int(resume)
collect:
	for got.Len() < want {
		select {
		case ev, ok := <-again.Events:
			if !ok {
				break collect
			}
			if ev.Data != nil {
				got.Write(ev.Data)
			}
		case <-readDeadline:
			break collect
		}
	}
	if got.Len() < want {
		t.Fatalf("re-attach from consumed=%d returned %d bytes, want %d — the gap between what "+
			"was delivered and what was charged is lost scrollback, and nothing ever re-syncs it",
			resume, got.Len(), want)
	}
	if !bytes.Equal(got.Bytes()[:want], sent.Bytes()[resume:]) {
		t.Errorf("re-attach returned different bytes than were written at offset %d", resume)
	}
}

// TestDetachDoesNotAnnounceAnExit pins a contract defect caught in the freeze window.
//
// A session's subscription channel closes for two unrelated reasons — the process exited,
// or this subscription was cancelled — and the daemon's stream goroutine cannot tell them
// apart from the channel alone. It used to announce MsgExited for both, carrying the -1
// sentinel of a running process. ExitedPayload has no session id, so a client could not
// even attribute the lie.
func TestDetachDoesNotAnnounceAnExit(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<16, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Raw connection: the shipped client never sends MsgDetach, which is exactly why this
	// defect could sit latent. A future client — or a second implementation — would hit it.
	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if err := WriteJSON(conn, MsgAttach, AttachReq{ID: id}); err != nil {
		t.Fatalf("send attach: %v", err)
	}
	if typ, _, rerr := ReadFrame(conn); rerr != nil || typ != MsgAttachAck {
		t.Fatalf("attach ack: type=%d err=%v", typ, rerr)
	}
	if err := WriteJSON(conn, MsgDetach, struct{}{}); err != nil {
		t.Fatalf("send detach: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	sawOK := false
	for {
		typ, payload, rerr := ReadFrame(conn)
		if rerr != nil {
			break // read deadline: nothing further arrived, which is the pass condition
		}
		switch typ {
		case MsgOK:
			sawOK = true
		case MsgExited:
			var p ExitedPayload
			_ = json.Unmarshal(payload, &p)
			t.Fatalf("detach announced MsgExited{code=%d} for a session that is still running; "+
				"the payload has no session id, so a client cannot even tell which session it "+
				"is being lied about", p.ExitCode)
		}
	}
	if !sawOK {
		t.Error("detach was never acknowledged")
	}
	if s, ok := d.Get(id); !ok || !s.Alive() {
		t.Error("precondition: the session died during the test, so it proves nothing")
	}
}

// TestKillRefusesSignalZero: the Unix liveness probe must not be the destroy verb.
//
// kill(pid, 0) sends nothing and is THE canonical "is it still there" check. It used to
// mean "destroy this session" here, undocumented, in the contract file about to be frozen
// — the most destructive operation spelled exactly like the most harmless one.
func TestKillRefusesSignalZero(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<16, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = c.roundTrip(requireDaemon, MsgKill, KillReq{ID: id, Sig: 0}, MsgOK, nil)
	if err == nil {
		t.Fatal("signal 0 was accepted — a liveness probe destroyed a session")
	}
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != ErrCodeBadFrame {
		t.Errorf("err = %v, want a bad_frame ProtocolError", err)
	}
	if s, ok := d.Get(id); !ok || !s.Alive() {
		t.Error("the session was destroyed by signal 0 despite the refusal")
	}

	// And the real verb still works.
	if err := c.Destroy(id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, ok := d.Get(id); ok {
		t.Error("Destroy did not remove the session")
	}
}

// TestDestroyIsBroadcastAsItsOwnEvent covers the cross-host half.
//
// "Exited" and "destroyed" are different facts: an exited session is still a real thing
// the other host should show, greyed out, with its scrollback; a destroyed one must
// disappear everywhere. Destroy used to broadcast nothing at all when the session had
// already exited, and "exited" when it had not — either way the other host kept a tab the
// user had deleted, permanently.
func TestDestroyIsBroadcastAsItsOwnEvent(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<16, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	events, cancel, err := c.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := c.Destroy(id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before a destroyed event arrived")
			}
			if ev.Kind == EventDestroyed && ev.ID == id {
				return // pass
			}
		case <-deadline:
			t.Fatal("no destroyed event within 5s — another host would keep showing a tab the " +
				"user deleted, forever")
		}
	}
}

// TestNoListenerIsNarrow guards the single predicate that two dangerous decisions hang on:
// whether Listen may unlink a socket and bind over it, and whether a dial failure is
// definitive proof that a user's sessions are gone.
//
// Only a PROVEN absence may say yes. "I could not find out" — a local fd exhaustion, a
// peer whose accept backlog is full, a timeout — must not be mistaken for it. Both callers
// previously treated every dial error as absence: one would evict a healthy daemon holding
// the user's shells, the other would mark that daemon's live sessions dead.
func TestNoListenerIsNarrow(t *testing.T) {
	proven := []error{
		syscall.ECONNREFUSED,
		syscall.ENOENT,
		os.ErrNotExist,
		fmt.Errorf("dial unix /x: %w", syscall.ECONNREFUSED), // wrapped, as net returns it
	}
	for _, err := range proven {
		if !noListener(err) {
			t.Errorf("noListener(%v) = false; a stale socket would never be reclaimed", err)
		}
	}

	unknown := []error{
		syscall.EMFILE,           // we ran out of descriptors — says nothing about the peer
		syscall.EAGAIN,           // peer's accept backlog is full — it is very much alive
		syscall.ETIMEDOUT,        // busy, not absent
		context.DeadlineExceeded, // ditto
		errors.New("some io error"),
	}
	for _, err := range unknown {
		if noListener(err) {
			t.Errorf("noListener(%v) = true; this would let a healthy daemon be evicted and its "+
				"live sessions be reported dead", err)
		}
	}
}

// TestEventSequenceMakesDropsDetectable closes the last correctness hole in cross-host
// visibility.
//
// Events are dropped on purpose when a subscriber falls behind — a subscriber must never
// apply backpressure to the daemon, and through it to a shell. Without a sequence number
// that drop is indistinguishable from "nothing happened", so a burst of creates or deletes
// leaves every other host with a permanently wrong picture and no way to know it.
//
// Two properties, and the second is the one that matters: the numbers increment by one
// when nothing is lost, and they SKIP when something is. The skip is what turns an
// invisible loss into an instruction to re-read.
func TestEventSequenceMakesDropsDetectable(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<12, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	// A subscriber that keeps up sees an unbroken run starting at 1.
	events, cancel, err := c.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	for want := uint64(1); want <= 3; want++ {
		select {
		case ev := <-events:
			if ev.Seq != want {
				t.Fatalf("event %d has seq %d, want %d — a receiver cannot tell a gap from a "+
					"reorder if the numbering is not exact", want, ev.Seq, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("event %d never arrived", want)
		}
	}
	cancel()

	// Now a subscriber that does NOT keep up. Subscribe directly on the daemon so the test
	// controls the draining.
	raw, cancelRaw := d.subscribe()
	defer cancelRaw()

	// First establish what this receiver knows, the way a real watcher does: a few events,
	// consumed promptly. Without this baseline there is nothing for a gap to be a gap FROM.
	for i := 0; i < 3; i++ {
		d.broadcast(Event{Kind: EventCreated, ID: fmt.Sprintf("warm%d", i)})
	}
	var lastKnown uint64
	for i := 0; i < 3; i++ {
		select {
		case ev := <-raw:
			lastKnown = ev.Seq
		case <-time.After(2 * time.Second):
			t.Fatal("warm-up event never arrived")
		}
	}
	if lastKnown != 3 {
		t.Fatalf("warm-up left lastKnown=%d, want 3", lastKnown)
	}

	// Then a burst nobody reads, far past the 64-slot buffer.
	const burst = 200
	for i := 0; i < burst; i++ {
		d.broadcast(Event{Kind: EventCreated, ID: fmt.Sprintf("s%d", i)})
	}

	var got []uint64
	for {
		select {
		case ev := <-raw:
			got = append(got, ev.Seq)
			continue
		default:
		}
		break
	}
	if len(got) == 0 {
		t.Fatal("no events buffered at all")
	}
	if len(got) >= burst {
		t.Fatalf("received %d of %d events — nothing was dropped, so this proves nothing about "+
			"detecting drops", len(got), burst)
	}

	// The last delivered must be the NEWEST event, not the 64th. That is the whole reason a
	// full buffer discards its oldest: a receiver left holding the FIRST 64 has an unbroken
	// run and cannot tell anything was lost until some future event arrives — which, if the
	// burst was the last thing to happen, never does.
	if last := got[len(got)-1]; last != lastKnown+uint64(burst) {
		t.Errorf("last delivered seq = %d, want %d — the buffer kept its oldest events and "+
			"dropped the newest, so the loss stays invisible", last, lastKnown+uint64(burst))
	}
	// And the gap is visible against what the receiver already knew — which is exactly the
	// comparison the watcher makes.
	if !MissedEvents(lastKnown, got[0]) {
		t.Errorf("first delivered seq = %d follows lastKnown = %d with no gap; %d events "+
			"vanished without a trace", got[0], lastKnown, burst-len(got))
	}
}

// TestSmallestAttachmentWins is the acceptance test for per-attachment geometry.
//
// Before it, a session had ONE size, set by whoever called resize last. With two clients
// that is not a policy, it is a fight: a phone and a desktop looking at the same session
// each reflow the other every time either one resizes. And "cropped" understates what the
// loser sees — a TUI paints by absolute cursor address, so a grid wider than the window
// does not clip, it wraps, and every line below shifts.
//
// The rule is tmux's, for tmux's reason: the session fits the SMALLEST window watching it,
// because that is the only size everyone can actually display.
func TestSmallestAttachmentWins(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<14, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp", Cols: 200, Rows: 50})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess, _ := d.Get(id)

	// A desktop-sized viewer arrives; the session becomes its size.
	big, err := c.Attach(id, AttachOptions{Cols: 200, Rows: 50})
	if err != nil {
		t.Fatalf("attach big: %v", err)
	}
	defer big.Close()
	waitForSize(t, sess, 200, 50, "the only viewer's size")

	// A second viewer arrives that is NARROWER but TALLER. The expected result — 80x50 —
	// is a size neither client asked for, and that is deliberate: it is the component-wise
	// minimum, which no "one client owns the size" policy can produce. An assertion whose
	// answer equals one of the inputs cannot tell smallest-wins from last-writer-wins, and
	// the first version of this test could not (it passed with the rule sabotaged, on a
	// coin flip, because map iteration order decided which input "won").
	small, err := c.Attach(id, AttachOptions{Cols: 80, Rows: 100})
	if err != nil {
		t.Fatalf("attach small: %v", err)
	}
	defer small.Close()
	waitForSize(t, sess, 80, 50, "the component-wise minimum of both viewers")

	if sz := waitForResizeEvent(t, big, 5*time.Second); sz != (Grid{Cols: 80, Rows: 50}) {
		t.Errorf("the first client was told %s, want 80x50", sz)
	}

	// The narrow viewer leaves. Its constraint leaves with it, and the session grows back
	// rather than staying stuck at the size of a client that is gone.
	small.Close()
	waitForSize(t, sess, 200, 50, "the remaining viewer's size after the narrow one left")

	// A resize from an attached client changes only ITS OWN claim.
	if err := big.Resize(id, 120, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	waitForSize(t, sess, 120, 30, "the resized attachment's size")
}

// TestObserverImposesNoSize pins the escape hatch that smallest-wins needs.
//
// Someone glancing at a session from an 80-column ssh window must not reflow the terminal
// a colleague — or the same person on another screen — is working in. Attaching with no
// declared size says "I am watching, I am not a constraint".
func TestObserverImposesNoSize(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<14, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp", Cols: 240, Rows: 60})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess, _ := d.Get(id)

	worker, err := c.Attach(id, AttachOptions{Cols: 240, Rows: 60})
	if err != nil {
		t.Fatalf("attach worker: %v", err)
	}
	defer worker.Close()
	waitForSize(t, sess, 240, 60, "the working viewer's size")

	// The working viewer is deliberately LARGER than the session default (220x50) in both
	// dimensions. That is what makes this assertion sensitive: if "no declared size" were
	// ever quietly turned into a real size, the most likely substitute is the default, and
	// the default would then constrain — 240x60 would collapse to 220x50. An earlier
	// version used 200x50, which the default happens not to constrain, so it passed with
	// the observer concept entirely removed.
	observer, err := c.Attach(id, AttachOptions{}) // no size == no claim
	if err != nil {
		t.Fatalf("attach observer: %v", err)
	}
	defer observer.Close()

	time.Sleep(300 * time.Millisecond)
	if got := sessionSize(sess); got != (Grid{Cols: 240, Rows: 60}) {
		t.Errorf("session became %s after an OBSERVER attached; merely looking must not "+
			"reflow the terminal someone is working in", got)
	}
	select {
	case ev := <-worker.Events:
		if ev.Resize != nil {
			t.Errorf("the working client was told the grid changed to %dx%d; nothing changed",
				ev.Resize.Cols, ev.Resize.Rows)
		}
	default:
	}
}

func sessionSize(s *Session) Grid {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

func waitForSize(t *testing.T, s *Session, cols, rows uint16, why string) {
	t.Helper()
	want := Grid{Cols: int(cols), Rows: int(rows)}
	deadline := time.Now().Add(5 * time.Second)
	var got Grid
	for time.Now().Before(deadline) {
		got = sessionSize(s)
		if got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session is %s, want %s (%s)", got, want, why)
}

// TestAttachedCountIsReported gives the operator the number that separates two failures
// with identical symptoms.
//
// "This tab is not responding" means either the program inside is busy, or nothing is
// attached and its output is going somewhere nobody is looking. Those need opposite
// responses, and until the daemon reported this there was no way to tell them apart
// except by guessing. It is also the number that explains a surprising resize: with
// smallest-wins, a session shrinks because someone ELSE is watching from a smaller window.
func TestAttachedCountIsReported(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<14, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if n := attachedCount(t, c, id); n != 0 {
		t.Errorf("a freshly created session reports %d viewers, want 0", n)
	}

	a, err := c.Attach(id, AttachOptions{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("attach a: %v", err)
	}
	waitForAttached(t, c, id, 1)

	b, err := c.Attach(id, AttachOptions{}) // an observer is still a viewer
	if err != nil {
		t.Fatalf("attach b: %v", err)
	}
	waitForAttached(t, c, id, 2)

	_ = b.Close()
	waitForAttached(t, c, id, 1)
	_ = a.Close()
	waitForAttached(t, c, id, 0)
}

func attachedCount(t *testing.T, c *Client, id string) int {
	t.Helper()
	sessions, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range sessions {
		if s.ID == id {
			return s.Attached
		}
	}
	t.Fatalf("session %s not listed", id)
	return -1
}

func waitForAttached(t *testing.T, c *Client, id string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	got := -1
	for time.Now().Before(deadline) {
		if got = attachedCount(t, c, id); got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session reports %d viewers, want %d", got, want)
}

// waitForResizeEvent returns the next resize carried on the stream, skipping output.
func waitForResizeEvent(t *testing.T, s *Stream, budget time.Duration) Grid {
	t.Helper()
	deadline := time.After(budget)
	for {
		select {
		case ev, ok := <-s.Events:
			if !ok {
				t.Fatal("the stream ended before a resize arrived")
			}
			if ev.Resize != nil {
				return *ev.Resize
			}
		case <-deadline:
			t.Fatal("the client was never told the grid moved under it — it would keep " +
				"rendering onto a shape the session no longer has")
		}
	}
}

// TestResizeIsOrderedWithTheOutputItAffects is the acceptance test for the one DESIGN
// defect in this round, as opposed to the implementation ones.
//
// Output and resizes used to travel on separate channels, selected between at two
// independent hops. Ordering was therefore not merely unspecified, it was random — and a
// terminal client cannot interpret bytes without knowing which grid they were drawn for. A
// TUI redraw that arrives before its own resize notification is painted onto the old
// geometry, and the result is a scrambled screen that looks like a bug in the program
// inside.
//
// The bytes here are deliberately ordered around the resize: everything before it was
// produced at the old size, everything after at the new one. A stream that delivers them
// in any other order has lost information that no consumer can recover.
func TestResizeIsOrderedWithTheOutputItAffects(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	var writeEnd *os.File
	d := NewDaemonWith(1<<16, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, w, perr := os.Pipe()
		if perr != nil {
			return nil, nil, perr
		}
		writeEnd = w
		return r, nil, nil
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp", Cols: 200, Rows: 50})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess, _ := d.Get(id)

	watcher, err := c.Attach(id, AttachOptions{Cols: 200, Rows: 50})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer watcher.Close()
	waitForSize(t, sess, 200, 50, "the only viewer's size")

	// BEFORE, then the resize, then AFTER — with each step allowed to settle so the
	// ordering under test is the stream's, not a race in the test.
	if _, err := writeEnd.Write([]byte("BEFORE")); err != nil {
		t.Fatalf("write before: %v", err)
	}
	waitForBytes(t, sess, 6)
	sess.SetAttachSize(subIDOf(t, sess), Grid{Cols: 80, Rows: 24})
	waitForSize(t, sess, 80, 24, "the resized attachment")
	if _, err := writeEnd.Write([]byte("AFTER")); err != nil {
		t.Fatalf("write after: %v", err)
	}

	var order []string
	deadline := time.After(10 * time.Second)
collect:
	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				break collect
			}
			switch {
			case ev.Resize != nil:
				order = append(order, fmt.Sprintf("resize=%s", ev.Resize))
			case ev.Data != nil:
				order = append(order, string(ev.Data))
			}
			if len(order) >= 3 {
				break collect
			}
		case <-deadline:
			break collect
		}
	}

	got := strings.Join(order, "|")
	want := "BEFORE|resize=80x24|AFTER"
	if got != want {
		t.Errorf("stream order = %q, want %q — a client cannot tell which grid a byte was "+
			"drawn for if the resize can overtake or trail it", got, want)
	}
}

// subIDOf returns the id of the session's single attachment.
func subIDOf(t *testing.T, s *Session) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subs) != 1 {
		t.Fatalf("expected exactly one attachment, found %d", len(s.subs))
	}
	for id := range s.subs {
		return id
	}
	return -1
}

// waitForBytes waits until the session's ring has taken in at least n bytes.
func waitForBytes(t *testing.T, s *Session, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		seq := int64(s.ring.Seq())
		s.mu.Unlock()
		if seq >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session never took in %d bytes", n)
}

// TestFirstEventGapIsNotSwallowed: a subscription is numbered from 1, so a first event of
// N>1 means N-1 were lost before the consumer started draining — which is exactly what a
// burst between subscribe and the first read produces. Treating the first event as a free
// baseline hid that case permanently, because everything after it is contiguous and no
// later event could reveal it.
func TestFirstEventGapIsNotSwallowed(t *testing.T) {
	if MissedEvents(0, 1) {
		t.Error("the very first event of a subscription is not a gap")
	}
	if !MissedEvents(0, 137) {
		t.Error("a first event of 137 means 136 were lost before anyone was listening; " +
			"nothing later can ever reveal it, so it must be caught here")
	}
	if MissedEvents(0, 0) {
		t.Error("a daemon too old to stamp events must not look like a gap")
	}
	if MissedEvents(5, 6) {
		t.Error("consecutive events are not a gap")
	}
	if !MissedEvents(5, 9) {
		t.Error("a jump of 4 is a gap")
	}
}

// TestALosingClientIsTold covers the answer a "broadcast on change" design cannot give.
//
// Two clients watch one session. The smaller one owns the size. When the larger one resizes
// its window, the session does not move — so there is nothing to broadcast, and under a
// change-only design that client hears nothing at all. It has already reflowed its own
// terminal to the size it asked for, so from that moment it paints every screen onto a grid
// the session never entered, and nothing is scheduled to correct it.
//
// The fix is not a broadcast. It is an ANSWER: a resize request is a question, and the client
// that asked is owed the result even when the result is "no".
func TestALosingClientIsTold(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<14, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp", Cols: 200, Rows: 50})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess, _ := d.Get(id)

	small, err := c.Attach(id, AttachOptions{Cols: 70, Rows: 20})
	if err != nil {
		t.Fatalf("attach small: %v", err)
	}
	defer small.Close()
	waitForSize(t, sess, 70, 20, "the only viewer's size")

	big, err := c.Attach(id, AttachOptions{Cols: 200, Rows: 60})
	if err != nil {
		t.Fatalf("attach big: %v", err)
	}
	defer big.Close()
	// Drain the ack-era frames so the assertion below is about the RESIZE, not about
	// whatever the attach itself produced.
	drainFor(big, 200*time.Millisecond)

	if err := big.Resize(id, 240, 80); err != nil {
		t.Fatalf("big resize: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-big.Events:
			if !ok {
				t.Fatal("stream closed before the answer arrived")
			}
			if ev.Resize == nil {
				continue
			}
			if *ev.Resize != (Grid{Cols: 70, Rows: 20}) {
				t.Fatalf("answered %v, want [70 20] — the size the session is actually at", *ev.Resize)
			}
			return
		case <-deadline:
			t.Fatal("the client that asked for 240x80 and did not get it was never told; its " +
				"terminal is reflowed to 240x80 while the session is 70x20, and no broadcast " +
				"is coming because nothing changed")
		}
	}
}

// drainFor discards whatever a stream delivers for d, so a later assertion is about the
// frame under test rather than about attach-time noise.
func drainFor(s *Stream, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case <-s.Events:
		case <-deadline:
			return
		}
	}
}

// TestResizeDoesNotRaceDestroy pins the descriptor rule that a comment used to claim and the
// code did not honour.
//
// pty.Setsize reaches the kernel through os.File.Fd(), which is the documented escape hatch
// out of os.File's use-after-close protection: Read and Write go through a refcounted poll.FD
// that waits for in-flight work and then returns ErrClosed, but a raw fd number does not. So
// an ioctl racing a close is not a failed resize — it is an ioctl on whatever the kernel
// handed that number to next.
//
// Destroy used to read the descriptor under the lock and close it OUTSIDE, which orders the
// read and nothing else. The old comment asserted the opposite in as many words. Per-attachment
// geometry is what turned this from theory into a race the detector caught: resizes now happen
// on every viewer arrival, departure and window change.
//
// Run this under -race; without it the loop is just exercise.
func TestResizeDoesNotRaceDestroy(t *testing.T) {
	for round := 0; round < 50; round++ {
		s, err := SpawnWith("race-"+strconv.Itoa(round), SpawnOptions{Argv: []string{"/bin/sh"}},
			func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
				r, _, perr := os.Pipe()
				return r, nil, perr
			}, nil)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					_ = s.Resize(uint16(80+w*10+i%7), uint16(24+i%5))
				}
			}(w)
		}
		s.Destroy()
		wg.Wait()
	}
}

// TestAnUnannouncedCapabilityIsTreatedAsAbsent: a daemon predating the Features field is
// indistinguishable from one that supports nothing, and that is the required reading.
//
// This is the ONLY case that matters in practice — every daemon in the field on the day
// this shipped omits the field entirely — so if the check were lenient about the empty
// case it would be lenient about the entire population it exists to detect.
func TestAnUnannouncedCapabilityIsTreatedAsAbsent(t *testing.T) {
	old := Hello{Version: ProtoVersion, PID: 4242, Sessions: 9} // a daemon from before this field
	missing := old.MissingFeatures()
	if len(missing) == 0 {
		t.Fatal("a daemon that advertised nothing was accepted as capable — every daemon " +
			"running at upgrade time looks exactly like this one")
	}
	if !contains(missing, FeaturePerAttachmentGeometry) {
		t.Fatalf("missing = %v, want it to name %q", missing, FeaturePerAttachmentGeometry)
	}
	if old.Has(FeaturePerAttachmentGeometry) {
		t.Fatal("Has reported a capability that was never advertised")
	}
}

// TestThisBuildSatisfiesItsOwnRequirements: the two lists are separate so they CAN diverge
// across versions; they must not diverge within one build, or every client refuses every
// daemon compiled from the same source.
func TestThisBuildSatisfiesItsOwnRequirements(t *testing.T) {
	self := Hello{Version: ProtoVersion, Features: DaemonFeatures}
	if missing := self.MissingFeatures(); len(missing) > 0 {
		t.Fatalf("a daemon from this source cannot satisfy a client from this source: missing %v", missing)
	}
}

// TestHandshakeCarriesCapabilitiesToTheClient is the end-to-end half: the list has to
// survive the wire and land where the decision is made (Client.Peer), not merely exist as
// a constant on both sides.
//
// Sabotage that proves it bites: drop Features from Daemon.identity() and this goes red.
func TestHandshakeCarriesCapabilitiesToTheClient(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(1<<16, -1, nil)
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	peer := c.Peer()
	if peer.PID == 0 {
		t.Fatal("handshake did not reach Client.Peer at all — the rest of this test proves nothing")
	}
	if missing := peer.MissingFeatures(); len(missing) > 0 {
		t.Fatalf("a live daemon from this source was reported as missing %v", missing)
	}
	if !peer.Has(FeaturePerAttachmentGeometry) {
		t.Fatalf("peer features = %v, want %q among them", peer.Features, FeaturePerAttachmentGeometry)
	}

	// Inspect is the other door to the same answer — DaemonHealth uses it, and it dials
	// separately rather than reusing the client, so it can regress on its own.
	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !info.Has(FeaturePerAttachmentGeometry) {
		t.Fatalf("Inspect features = %v, want %q among them", info.Features, FeaturePerAttachmentGeometry)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
