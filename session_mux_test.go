package terminal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// TestCloseAllDetachesRatherThanDestroys pins the single most important behavioural
// difference this whole change introduces, at the unit level.
//
// Why it exists separately from the end-to-end test: Server.Close (which calls CloseAll)
// is the shutdown path an EMBEDDING HOST uses — deepwork-pro calls it when it tears the
// terminal subsystem down. The e2e test kills a process instead, so it never exercises
// this call at all. Without this test, "shutdown no longer destroys sessions" would be
// verified only for the standalone binary, and pro could regress silently.
func TestCloseAllDetachesRatherThanDestroys(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")

	sess, err := sm.Create("survivor")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pid := sess.ShellPID()
	if pid == 0 {
		t.Fatal("no shell pid reported — cannot prove anything about survival")
	}

	if err := sm.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}

	if err := syscallKillZeroForTest(pid); err != nil {
		t.Fatalf("shell %d died on CloseAll: shutting the server down must not end the user's work (%v)", pid, err)
	}

	// And the session is still there to be picked back up, with the same id.
	if err := sm.Restore(); err != nil {
		t.Fatalf("Restore after CloseAll: %v", err)
	}
	found := false
	for _, s := range sm.List() {
		if s.ID == sess.ID {
			found = true
			if s.ShellPID() != pid {
				t.Errorf("shell pid changed %d → %d: the session was respawned, not preserved", pid, s.ShellPID())
			}
		}
	}
	if !found {
		t.Errorf("session %s did not come back after CloseAll + Restore", sess.ID)
	}
}

// TestRestoreRebuildsMetadataFromDaemon checks the other half: the daemon stores an
// opaque blob, and the server has to get the tab's identity back out of it. If this
// regressed, sessions would survive a restart as anonymous, un-named terminals — present
// but useless, which reads to a user as "my tabs are gone" anyway.
func TestRestoreRebuildsMetadataFromDaemon(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")

	created, err := sm.CreateWithOptions(CreateOptions{
		Name:   "named-tab",
		Title:  "My Title",
		Engine: "shell",
		Shell:  "/bin/sh",
		CWD:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	// Drop every trace of the session from the server side, keeping the daemon — exactly
	// the state a freshly started server is in.
	sm.sessions.Delete(created.ID)
	if len(sm.List()) != 0 {
		t.Fatal("failed to clear the server-side view")
	}

	if err := sm.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	list := sm.List()
	if len(list) != 1 {
		t.Fatalf("restored %d sessions, want 1", len(list))
	}
	got := list[0]
	if got.ID != created.ID {
		t.Errorf("id = %q, want %q", got.ID, created.ID)
	}
	if got.Name != "named-tab" {
		t.Errorf("name = %q, want %q — the metadata blob did not survive", got.Name, "named-tab")
	}
	if got.Title != "My Title" {
		t.Errorf("title = %q, want %q", got.Title, "My Title")
	}
	if got.CWD != created.CWD {
		t.Errorf("cwd = %q, want %q", got.CWD, created.CWD)
	}
	if got.CreatedAt.IsZero() {
		t.Error("createdAt is zero — tab ordering would scramble after a restart")
	}
}

// TestRestoreCarriesScrollbackAndGeometry covers the replay path: after a restart the
// server holds no buffer of its own, so both the scrollback AND the grid it must be
// replayed onto have to come back from the daemon.
//
// The geometry half matters as much as the bytes: a TUI paints by absolute cursor
// addressing, so replaying onto a differently-sized grid produces a different screen, not
// a reconstruction. A second hardcoded guess is how that drifted before.
func TestRestoreCarriesScrollbackAndGeometry(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")

	sess, err := sm.Create("replay")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// No browser is attached in this fixture, so this server declares nothing and the
	// control-plane fallback is what sizes the session. See RequestPTYSize.
	if err := sess.RequestPTYSize(133, 41); err != nil {
		t.Fatalf("RequestPTYSize: %v", err)
	}
	waitForPTYSize(t, sess, 133, 41, 10*time.Second)
	marker := "RESTORE-MARKER-42"
	if err := sess.WriteInput([]byte("echo " + marker + "\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	waitForBufferContains(t, sess, marker, 10*time.Second)

	sm.sessions.Delete(sess.ID)
	if err := sm.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	list := sm.List()
	if len(list) != 1 {
		t.Fatalf("restored %d sessions, want 1", len(list))
	}
	restored := list[0]

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(restored.Buffer.ReadTail(1<<16)), marker) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(restored.Buffer.ReadTail(1<<16)), marker) {
		t.Errorf("scrollback did not come back from the daemon (missing %q)", marker)
	}

	if got := restored.PTYSize(); got != (muxd.Grid{Cols: 133, Rows: 41}) {
		t.Errorf("geometry = %s, want 133x41 — the replay grid would not match the real terminal", got)
	}
}

// TestDetachedStreamIsNotReportedAsExited guards a failure that is easy to write and
// impossible to notice: distinguishing "the session ended" from "we lost the connection".
//
// A closed channel is always ready, so a `select { case <-Exit: ... default: }` takes the
// exit branch the moment the connection drops and reports a perfectly healthy shell as
// exited with code 0. The user sees "session exited" for something that is still running —
// which is the same experience as losing it, delivered by the code meant to prevent that.
func TestDetachedStreamIsNotReportedAsExited(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")

	sess, err := sm.Create("detach-not-exit")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pid := sess.ShellPID()

	// Drop the stream the way a lost daemon connection does, without ending the session.
	sess.mu.Lock()
	stream := sess.stream
	sess.mu.Unlock()
	if stream == nil {
		t.Fatal("session has no stream to detach")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}

	// Give pumpStream time to observe the closed stream and decide what it means.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sess.GetStatus() == StatusExited {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := sess.GetStatus(); got == StatusExited {
		t.Errorf("status = %v after a mere disconnect; the shell (pid %d) is still running", got, pid)
	}
	if err := syscallKillZeroForTest(pid); err != nil {
		t.Fatalf("precondition failed: the shell really did die (%v) — this test proves nothing", err)
	}
	select {
	case <-sess.Done():
		t.Error("Done() fired on a disconnect; consumers would treat a live session as finished")
	default:
	}
}

// TestGhostSessionIsMarkedEndedWhenDaemonDies covers the state the server is left in
// after the daemon dies: it still holds a VIEW of sessions whose PTYs went with it.
//
// Without the fix, the tab strip keeps showing those as running. The user clicks one,
// types, and nothing happens — a ghost tab that looks alive and swallows every keystroke.
//
// The daemon must be SIGKILLed, not asked to destroy the session: a polite destroy sends
// an exit notification and the session is marked ended through the ordinary path, which
// tests nothing about this one. (The first version of this test did exactly that and
// stayed green with the fix removed.)
func TestGhostSessionIsMarkedEndedWhenDaemonDies(t *testing.T) {
	sm, sock := newSpawnedDaemonManager(t)

	sess, err := sm.Create("ghost")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.GetStatus() != StatusRunning {
		t.Fatalf("precondition: status = %v, want running", sess.GetStatus())
	}

	killDaemon(t, sock)

	// pumpStream sees the stream drop with no exit, re-attaches, and the (freshly spawned)
	// daemon answers not_found — at which point the session must be reported as ended.
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if sess.GetStatus() == StatusExited {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if got := sess.GetStatus(); got != StatusExited {
		t.Errorf("status = %v, want %v — the tab would keep looking alive though its PTY died with the daemon",
			got, StatusExited)
	}
	select {
	case <-sess.Done():
	default:
		t.Error("Done() never fired; consumers waiting on it would hang forever on a ghost")
	}

	// And nothing may have RESTARTED the daemon to reach that conclusion.
	//
	// An earlier version re-attached with connect-or-spawn, so it got its not_found from
	// a brand-new empty daemon: the right answer for the wrong reason, and a stray daemon
	// process left running afterwards. Verified in the wild — one such daemon was found
	// eight hours later, spawned by a re-attach goroutine that outlived the test that
	// started it. "No daemon" is itself the answer; asking a fresh one is both slower and
	// a leak.
	if muxd.SocketAlive(sock) {
		t.Error("a daemon is listening again — re-attach resurrected the daemon it was supposed to find missing")
	}
}

// TestCrossHostSessionBecomesVisible is the acceptance test for the per-user daemon
// decision: one daemon, shared by every host on the machine.
//
// The standalone build and the embedded one are separate servers over the SAME daemon, so
// a tab created in one is a real session the other must show. Before the event
// subscription, the second server only learned about it the next time something happened
// to call List — meaning a terminal opened in one window sat invisible in the other.
func TestCrossHostSessionBecomesVisible(t *testing.T) {
	hostA, sock := newSpawnedDaemonManager(t)

	// A second manager on the SAME socket: the other host.
	hostB := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(hostB.DestroyAll)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostB.WatchDaemon(ctx)

	// Force hostB to connect (and start watching) before anything is created.
	if err := hostB.Restore(); err != nil {
		t.Fatalf("hostB initial restore: %v", err)
	}
	if n := len(hostB.List()); n != 0 {
		t.Fatalf("hostB starts with %d sessions, want 0", n)
	}

	created, err := hostA.CreateWithOptions(CreateOptions{Name: "from-host-a", Shell: "/bin/sh", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("hostA create: %v", err)
	}
	_ = sock

	// hostB must see it WITHOUT anyone calling List on its behalf.
	deadline := time.Now().Add(15 * time.Second)
	var seen *Session
	for time.Now().Before(deadline) {
		for _, s := range hostB.List() {
			if s.ID == created.ID {
				seen = s
			}
		}
		if seen != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if seen == nil {
		t.Fatalf("host B never saw session %s created by host A — the shared daemon is not actually shared in practice",
			created.ID)
	}
	if seen.Name != "from-host-a" {
		t.Errorf("adopted session name = %q, want %q (metadata did not travel)", seen.Name, "from-host-a")
	}
}

// TestDaemonHealthExplainsAnIncompatibleDaemon is the acceptance test for the upgrade
// experience, from the only vantage point the user has.
//
// The scenario: dw-terminal is upgraded while the previous daemon is still resident,
// holding live shells. The two cannot speak, so the tab strip is empty and every new tab
// fails. Everything the product could show at that moment comes from this one struct — so
// it must contain the pid holding the sessions and the command that resolves it. If it
// carried only "connect failed", the user's only path forward would be to guess.
//
// The stand-in listens on the socket and refuses like a daemon of a different vintage.
func TestDaemonHealthExplainsAnIncompatibleDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, sock)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(sock))

	ln, err := muxd.Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, _, err := muxd.ReadFrame(c); err != nil {
					return
				}
				_ = muxd.WriteJSON(c, muxd.MsgError, muxd.ErrorPayload{
					Code: muxd.ErrCodeVersion,
					Msg:  "daemon speaks protocol v0",
				})
			}()
		}
	}()

	sm := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(sm.DestroyAll)

	h := sm.DaemonHealth()
	if h.Connected {
		t.Fatal("health reports connected against a daemon this build cannot talk to")
	}
	if h.Socket != sock {
		t.Errorf("socket = %q, want %q", h.Socket, sock)
	}
	if h.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d — the user cannot be told which process holds their sessions",
			h.PID, os.Getpid())
	}
	if h.Remedy != "dw-terminal muxd --restart" {
		t.Errorf("remedy = %q; without it the failure looks unrecoverable", h.Remedy)
	}
	if !strings.Contains(h.Problem, "protocol") {
		t.Errorf("problem = %q; it does not say what is actually wrong", h.Problem)
	}
}

// TestDaemonHealthDoesNotStartADaemon: a probe that spawns the thing it measures always
// reports health, which makes it worthless. "Nothing is running" must stay reportable.
func TestDaemonHealthDoesNotStartADaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, sock)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(sock))
	t.Setenv(muxd.EnvDaemonBin, "/bin/false") // any spawn attempt would be visible as a failure

	sm := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(sm.DestroyAll)

	h := sm.DaemonHealth()
	if h.Connected {
		t.Error("health reports connected with no daemon running")
	}
	if h.Problem == "" {
		t.Error("health gave no reason for being disconnected")
	}
	if muxd.SocketAlive(sock) {
		t.Error("probing health started a daemon — the measurement created what it measured")
	}
}

// TestAttachAfterDetachDoesNotResurrectTheDaemon closes the SECOND door.
//
// muxd's connectPolicy already forbids Client.Attach from starting a daemon. That was not
// enough: attach() asks the SessionManager for a connection first, and that accessor
// used connect-or-spawn. A re-attach goroutine outliving its server therefore still
// brought a daemon into existence — through the manager, not through the client.
//
// Observed in the wild: after a full test run, daemons were still resident, spawned at the
// socket paths of tests that had finished. They were started by exactly this path, because
// shutdown had cleared the cached client and the next re-attach re-created one.
//
// Setting up the state directly (kill the daemon, then detach) is the point: it is the
// state a shutting-down server is in, and it is the state no end-to-end test happens to
// reach.
func TestAttachAfterDetachDoesNotResurrectTheDaemon(t *testing.T) {
	sm, sock := newSpawnedDaemonManager(t)

	sess, err := sm.Create("orphan")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	killDaemon(t, sock)
	// Wait for it to actually be gone. Without this the assertion below can run while the
	// daemon is still shutting down and read a dying socket as a resurrected one — the
	// earlier version of this test got its delay incidentally, from the dial that attach
	// used to perform, and started failing the moment attach learned to refuse faster.
	waitUntil(t, 5*time.Second, "daemon to be gone", func() bool { return !muxd.SocketAlive(sock) })

	// Drop the cached client the way shutdown does, but WITHOUT the detached gate — this
	// test is about the accessor itself, not about the gate that now sits in front of it.
	sm.muxMu.Lock()
	sm.mux = nil
	sm.muxMu.Unlock()

	err = sm.attach(sess, nil)
	if err == nil {
		t.Fatal("attach succeeded with no daemon running — it must have started one")
	}
	if !muxd.IsNoDaemon(err) {
		t.Errorf("err = %v, want a NoDaemonError so the caller can conclude the session is gone", err)
	}
	if muxd.SocketAlive(sock) {
		t.Error("a daemon is listening again — attaching resurrected the daemon, and that process " +
			"outlives whatever asked for it")
	}
}

// waitUntil polls cond until it holds or the budget runs out.
func waitUntil(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", budget, what)
}

// TestShutdownDoesNotReattachItself pins the leak that made "detach" a lie.
//
// CloseAll closes every stream. Each pump reads that as "the connection dropped but the
// session did not end" — correct in every other circumstance — and re-attaches a moment
// later, connection and all. The manager that was told to stop then holds a full set of
// live streams, pump goroutines and daemon connections nobody will ever close.
//
// Standalone hides it: the process exits and the kernel collects everything. The EMBEDDED
// host builds and tears this subsystem down repeatedly, so it leaks a set per rebuild —
// and every leaked connection also pins the daemon's idle watchdog open forever.
//
// The wait is deliberately longer than the re-attach backoff's first few steps: the bug
// takes about a hundred milliseconds to appear, so a test that checked immediately would
// pass with the defect fully present.
func TestShutdownDoesNotReattachItself(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("shutdown-leak")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sm.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}

	time.Sleep(3 * time.Second)

	sess.mu.Lock()
	stream := sess.stream
	sess.mu.Unlock()
	sm.muxMu.Lock()
	client := sm.mux
	sm.muxMu.Unlock()

	if stream != nil {
		t.Error("the session re-attached after shutdown: a live stream and its pump goroutine " +
			"outlive the manager that was told to stop")
	}
	if client != nil {
		t.Error("shutdown re-opened the daemon connection it had just closed; that connection also " +
			"keeps the daemon's idle watchdog from ever firing")
	}

	// And the promise CloseAll exists for still holds: the shell is untouched.
	if err := syscallKillZeroForTest(sess.ShellPID()); err != nil {
		t.Errorf("the shell died during shutdown (%v) — detaching must never end the user's work", err)
	}
}

// TestWatchDaemonDoesNotStartADaemon closes the THIRD door, and it is the one that stayed
// open longest because it looked reasonable: a live server ought to have a live daemon, so
// why shouldn't the watcher guarantee it?
//
// Because a watcher retries forever. Any watcher that outlives its server — a test that
// never calls Close, a cancelled context losing a race — then spawns daemons indefinitely.
// Stray daemons found after a full test run were traced by stack dump to exactly this
// loop: watchOnce → client() → connect-or-spawn.
//
// The principle: an observer must not create what it observes. Nothing is lost by
// enforcing it — the moment anything creates a session a daemon exists, and the next retry
// finds it.
func TestWatchDaemonDoesNotStartADaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, sock)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(sock))

	// A REAL daemon binary must be reachable, or this test proves nothing: without it the
	// spawn is refused one layer down (daemonBinary declines to exec a test binary) and the
	// assertion passes no matter what the watcher does. Verified by sabotage — with the
	// override missing, restoring the old spawning watcher left this test green.
	t.Setenv(muxd.EnvDaemonBin, buildTestBinary(t))

	sm := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(func() {
		sm.DestroyAll()
		killDaemon(t, sock)
	})
	// The state a server is in after Restore has resolved the socket but no daemon is up.
	sm.muxMu.Lock()
	sm.muxSocket = sock
	sm.muxMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sm.WatchDaemon(ctx)

	// Long enough for several retries of the 2s backoff loop, plus a spawn if one happened
	// (SpawnTimeout gives a spawned daemon 10s to listen, but it listens in milliseconds).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if muxd.SocketAlive(sock) {
			t.Fatal("the watcher started a daemon — an observer must not create what it observes, " +
				"and this one retries forever")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestDaemonHealthOmitsAnUnknownStartTime pins a defect that shipped and was caught only
// by reading the live /api/system response.
//
// `omitempty` does not omit a zero time.Time; it renders "0001-01-01T00:00:00Z". A daemon
// too old to report when it started therefore looked to every consumer like a daemon that
// started in the year 1 — a real-looking timestamp, silently wrong. Absent is the only
// honest encoding of "it did not say".
func TestDaemonHealthOmitsAnUnknownStartTime(t *testing.T) {
	raw, err := json.Marshal(DaemonHealth{Socket: "/x", Connected: true, Proto: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "startedAt") {
		t.Errorf("payload %s carries a startedAt the daemon never reported", raw)
	}

	when := time.Unix(1700000000, 0)
	raw, err = json.Marshal(DaemonHealth{Socket: "/x", Connected: true, Proto: 1, StartedAt: &when})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "startedAt") {
		t.Errorf("payload %s dropped a start time that WAS reported", raw)
	}
}

// TestSessionDestroyedElsewhereDisappearsHere covers the half of cross-host visibility
// that was missing entirely: deletion.
//
// Creation propagated; deletion did not. Destroying a live session broadcast "exited", so
// the other host kept a greyed-out tab for something that no longer existed; destroying an
// already-exited one broadcast nothing at all, so the tab stayed forever. Either way the
// two hosts' tab strips diverged permanently — the user closed a tab in one window and it
// sat there in the other, unclickable and unremovable.
func TestSessionDestroyedElsewhereDisappearsHere(t *testing.T) {
	hostA, _ := newSpawnedDaemonManager(t)

	hostB := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(hostB.DestroyAll)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostB.WatchDaemon(ctx)

	created, err := hostA.CreateWithOptions(CreateOptions{Name: "shared", Shell: "/bin/sh", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("hostA create: %v", err)
	}
	waitUntil(t, 15*time.Second, "host B to see the session", func() bool {
		_, ok := hostB.sessions.Load(created.ID)
		return ok
	})

	if err := hostA.Destroy(created.ID); err != nil {
		t.Fatalf("hostA destroy: %v", err)
	}
	waitUntil(t, 15*time.Second, "host B to drop the destroyed session", func() bool {
		_, ok := hostB.sessions.Load(created.ID)
		return !ok
	})
}

// TestReconcileCatchesUpOnResubscribe covers what a subscription structurally cannot do.
//
// A subscription only carries what happens WHILE it is open. A host that was disconnected
// — daemon restarted, network blip, or simply a burst that overflowed the 64-slot event
// buffer — has a hole that no future event will ever fill. Without a catch-up read on
// re-subscribe the view drifts permanently and silently: this test's session was deleted
// while nobody was listening, and the tab would sit there forever.
func TestReconcileCatchesUpOnResubscribe(t *testing.T) {
	hostA, _ := newSpawnedDaemonManager(t)

	hostB := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(hostB.DestroyAll)
	firstCtx, stopWatching := context.WithCancel(context.Background())
	hostB.WatchDaemon(firstCtx)

	created, err := hostA.CreateWithOptions(CreateOptions{Name: "missed", Shell: "/bin/sh", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("hostA create: %v", err)
	}
	waitUntil(t, 15*time.Second, "host B to see the session", func() bool {
		_, ok := hostB.sessions.Load(created.ID)
		return ok
	})

	// Host B stops listening, and the deletion happens while it is not there to hear it.
	stopWatching()
	time.Sleep(300 * time.Millisecond)
	if err := hostA.Destroy(created.ID); err != nil {
		t.Fatalf("hostA destroy: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, ok := hostB.sessions.Load(created.ID); !ok {
		t.Fatal("precondition: host B dropped the session while unsubscribed, so this test proves nothing")
	}

	// It comes back. The catch-up read — not any event — is what must close the gap.
	secondCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostB.WatchDaemon(secondCtx)
	waitUntil(t, 15*time.Second, "host B to reconcile away a session deleted while it was not listening",
		func() bool {
			_, ok := hostB.sessions.Load(created.ID)
			return !ok
		})
}

// TestNonContiguousReplayResetsTheLocalBuffer covers the one way this server's scrollback
// cache can become a lie.
//
// The server keeps its own ring so the WebSocket replay and the overview's screen render
// do not each cost a daemon round trip. That is sound only while the copy is provably
// CONTINUOUS with the daemon's. It stops being so in exactly one situation: we ask to
// resume at an offset, the daemon's ring has wrapped past it, and the replay therefore
// starts later. Appending that to what we already hold splices two segments that were
// never adjacent — and the result is not visibly broken, it is a plausible screen that
// never existed, served to every browser and every overview card until the session dies.
//
// Throwing the copy away is the honest answer: losing scrollback is visible, inventing it
// is not.
func TestNonContiguousReplayResetsTheLocalBuffer(t *testing.T) {
	// The two rings must be DIFFERENT sizes, and the server's must be the larger one.
	//
	// This is the whole reason the test is built by hand instead of with the usual fixture,
	// which gives both rings the same capacity. With equal rings the splice is invisible:
	// the filler evicts the early marker from the server's own buffer regardless of whether
	// the reset happened, so the assertion passes either way. (It did. The first version of
	// this test stayed green with the fix sabotaged.) A server ring big enough to still
	// hold the early bytes is what makes "did we splice?" observable at all.
	const daemonRing = 8 << 10
	const serverRing = 1 << 20

	sock := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, sock)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(sock))

	ln, err := muxd.Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := muxd.NewDaemonWith(daemonRing, -1, nil) // nil factory = real PTYs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Serve(ctx, ln) }()
	defer d.DestroyAll()

	sm := NewSessionManager(serverRing, "/bin/sh")
	t.Cleanup(sm.DestroyAll)

	sess, err := sm.Create("wrap")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	early := "EARLY-MARKER-4417"
	if err := sess.WriteInput([]byte("echo " + early + "\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	waitForBufferContains(t, sess, early, 10*time.Second)

	// Drop the stream WITHOUT ending the session — the pump re-attaches from where it left
	// off, which is exactly the resume this test is about.
	sess.mu.Lock()
	stream := sess.stream
	sess.mu.Unlock()
	if stream == nil {
		t.Fatal("session has no stream to drop")
	}
	_ = stream.Close()

	// Produce far more than the DAEMON's ring can hold, so the resume point is evicted
	// there and the replay cannot start where we stopped.
	if err := sess.WriteInput([]byte("yes CONTIGUITY-FILLER | head -n 4000\n")); err != nil {
		t.Fatalf("WriteInput filler: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		body := string(sess.Buffer.ReadTail(serverRing))
		if strings.Contains(body, "CONTIGUITY-FILLER") && !strings.Contains(body, early) {
			return // reset, refilled, contiguous
		}
		time.Sleep(100 * time.Millisecond)
	}
	body := string(sess.Buffer.ReadTail(serverRing))
	if strings.Contains(body, early) {
		t.Errorf("the buffer still holds %q after a replay that could not resume there — old "+
			"bytes were spliced onto new ones, producing a screen that never existed", early)
	} else {
		t.Errorf("the buffer never refilled from the replay; got %d bytes", len(body))
	}
}

// TestReconcileDoesNotReviveADetachedManager guards the seam between two fixes that pull
// in opposite directions.
//
// One added a catch-up read so a watcher that missed events re-reads the daemon. The other
// added a gate so shutdown stops the pumps from re-attaching. They meet badly if the
// catch-up clears the gate: a reconcile already in flight when the server shuts down would
// revive the manager, the pumps would re-attach a moment later, and the shutdown leak is
// back — reopened by the code added to close a different hole.
//
// Restore clears the gate because a caller asking to restore is asking to use the manager
// again. reconcile must not, because nobody asked it anything.
func TestReconcileDoesNotReviveADetachedManager(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	if _, err := sm.Create("gate"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sm.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}

	sm.muxMu.Lock()
	wasDetached := sm.detached
	sm.muxMu.Unlock()
	if !wasDetached {
		t.Fatal("precondition: CloseAll did not set the detach gate")
	}

	if err := sm.reconcile(); err == nil {
		t.Error("reconcile succeeded against a detached manager; it must refuse rather than reconnect")
	}
	sm.muxMu.Lock()
	stillDetached, client := sm.detached, sm.mux
	sm.muxMu.Unlock()
	if !stillDetached {
		t.Error("reconcile cleared the detach gate — a catch-up read that happens to be in " +
			"flight during shutdown would revive the manager and the pumps would re-attach")
	}
	if client != nil {
		t.Error("reconcile re-opened the daemon connection shutdown had just closed")
	}

	// And the explicit path still works: Restore IS a deliberate re-entry.
	if err := sm.Restore(); err != nil {
		t.Fatalf("Restore after detach: %v", err)
	}
	sm.muxMu.Lock()
	revived := !sm.detached
	sm.muxMu.Unlock()
	if !revived {
		t.Error("Restore did not clear the gate; an explicit caller must be able to use the manager again")
	}
}

// TestRenamePersistsToTheDaemon closes a metadata SSOT split that the CLI exposed.
//
// The name lives in the daemon's opaque blob — that is what survives a restart and what
// every other client reads. A rename that only touched this process left `dw-terminal ls`
// printing the old name, `attach <new name>` failing, other hosts never learning, and the
// next restore quietly putting the old name back.
func TestRenamePersistsToTheDaemon(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.CreateWithOptions(CreateOptions{Name: "alpha", Shell: "/bin/sh", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess.SetName("beta")

	// Read it back the way another client would: from the daemon, not from this process.
	client, err := sm.existingClient()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sessions, lerr := client.List()
		if lerr != nil {
			t.Fatalf("List: %v", lerr)
		}
		for _, s := range sessions {
			if s.ID == sess.ID && SessionLabel(s.Meta, s.ID) == "beta" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	sessions, _ := client.List()
	for _, s := range sessions {
		if s.ID == sess.ID {
			t.Fatalf("the daemon still calls this session %q; every other client — and this "+
				"one after a restart — will keep showing the old name",
				SessionLabel(s.Meta, s.ID))
		}
	}
	t.Fatal("the session vanished from the daemon")
}
