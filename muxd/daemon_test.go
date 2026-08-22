package muxd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildDaemonBinary compiles the real dw-terminal binary once per package run so the
// spawn path under test is the actual one users get, not a stand-in.
var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func daemonBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dwmux-bin-")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "dw-terminal")
		cmd := exec.Command("go", "build", "-o", binPath, "../cmd/dw-terminal")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			binErr = err
			binPath = stderr.String()
		}
	})
	if binErr != nil {
		t.Fatalf("build dw-terminal: %v\n%s", binErr, binPath)
	}
	return binPath
}

// isolatedDaemon points both the socket and the daemon binary at test-owned paths.
// Isolation is not optional here: without it these tests would reach the user's real
// daemon and could take their live terminals down with them.
func isolatedDaemon(t *testing.T) string {
	t.Helper()
	path := isolatedSocket(t)
	t.Setenv(EnvDaemonBin, daemonBin(t))
	return path
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

// TestDaemonConnectOrSpawnStartsDaemon covers the zero-setup promise: no service
// manager, no install step — the first client brings the daemon up, like tmux.
func TestDaemonConnectOrSpawnStartsDaemon(t *testing.T) {
	path := isolatedDaemon(t)
	if SocketAlive(path) {
		t.Fatal("a daemon was already listening on an isolated path")
	}

	c, err := ConnectOrSpawn(path)
	if err != nil {
		t.Fatalf("ConnectOrSpawn: %v (log: %s)", err, readLog(path))
	}
	defer func() { _ = c.Close() }()
	t.Cleanup(func() { stopDaemon(path) })

	sessions, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("fresh daemon holds %d sessions, want 0", len(sessions))
	}
}

// TestDaemonSurvivesClientDisconnect is the core promise in miniature: closing the
// client — which is what a server shutdown does — must leave the session running.
func TestDaemonSurvivesClientDisconnect(t *testing.T) {
	path := isolatedDaemon(t)
	c, err := ConnectOrSpawn(path)
	if err != nil {
		t.Fatalf("ConnectOrSpawn: %v (log: %s)", err, readLog(path))
	}
	t.Cleanup(func() { stopDaemon(path) })

	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := c.List()
	if err != nil {
		t.Fatalf("List before: %v", err)
	}
	var shellPID int
	for _, s := range before {
		if s.ID == id {
			shellPID = s.ShellPID
		}
	}
	if shellPID == 0 {
		t.Fatal("daemon reported no shell pid — the server cannot see the process itself, so this must come over the wire")
	}

	// Close the control connection: this is exactly what shutting the HTTP server down
	// now does. Before dw-muxd, that killed every shell.
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := syscallKillZero(shellPID); err != nil {
		t.Fatalf("shell %d died when the client disconnected: %v", shellPID, err)
	}

	c2, err := Connect(path)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer c2.Close()
	after, err := c2.List()
	if err != nil {
		t.Fatalf("List after: %v", err)
	}
	found := false
	for _, s := range after {
		if s.ID == id {
			found = true
			if !s.Alive {
				t.Error("session is no longer alive after a client reconnect")
			}
			if s.ShellPID != shellPID {
				t.Errorf("shell pid changed %d → %d: the session was respawned, not preserved",
					shellPID, s.ShellPID)
			}
		}
	}
	if !found {
		t.Errorf("session %s vanished after the client disconnected", id)
	}
}

// TestDaemonConcurrentSpawnIsSingleton is the race guard. Two servers starting at once
// must not each get their own daemon: that would split the session set in half, with
// half the user's terminals unreachable and no error printed anywhere.
func TestDaemonConcurrentSpawnIsSingleton(t *testing.T) {
	path := isolatedDaemon(t)
	t.Cleanup(func() { stopDaemon(path) })

	const racers = 4
	var wg sync.WaitGroup
	clients := make([]*Client, racers)
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], errs[i] = ConnectOrSpawn(path)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v (log: %s)", i, err, readLog(path))
		}
		defer clients[i].Close()
	}

	if n := countDaemons(path); n != 1 {
		t.Fatalf("%d daemons alive for one socket — the session set would be split", n)
	}

	// The check above passes with or without the spawn lock, because Listen's atomic
	// bind is what keeps the daemon a singleton. What the lock prevents is a spawn
	// storm: N racers each forking a daemon, N-1 of which lose the bind and exit. The
	// daemon log is where that shows up, so assert on it directly — otherwise this
	// test would silently stop covering the lock at all.
	log := readLog(path)
	if started := strings.Count(log, "listening on"); started != 1 {
		t.Errorf("daemon log shows %d successful starts, want exactly 1 — spawn storm:\n%s", started, log)
	}
	if lost := strings.Count(log, "already listening"); lost != 0 {
		t.Errorf("%d daemons lost the bind race, want 0 — the spawn lock is not serialising:\n%s", lost, log)
	}

	// All racers must see the same daemon: create through one, observe through another.
	id, _, err := clients[0].Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	list, err := clients[racers-1].List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	seen := false
	for _, s := range list {
		if s.ID == id {
			seen = true
		}
	}
	if !seen {
		t.Error("a session created through one client is invisible to another — two daemons are running")
	}
}

// TestDaemonIdleExit: with nothing left to hold, the daemon goes away on its own rather
// than lingering as a permanent background process.
func TestDaemonIdleExit(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	d := NewDaemon(4096, 300*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- d.Serve(context.Background(), ln) }()

	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// While a session lives, the daemon must NOT exit even with no clients attached.
	_ = c.Close()
	time.Sleep(600 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("daemon exited while holding a live session: %v", err)
	default:
	}

	c2, err := Connect(path)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if err := c2.Destroy(id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	_ = c2.Close()

	select {
	case <-done: // exited as intended
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit after its last session ended")
	}
}

// TestDaemonMetaRoundTripsOpaque proves at the daemon level what proto_test proves at
// the wire level: metadata comes back byte-identical, including payloads that are not
// valid JSON. This is what lets the server evolve its schema without a daemon upgrade —
// and a daemon upgrade costs live sessions.
func TestDaemonMetaRoundTripsOpaque(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemon(4096, -1)
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	meta := []byte{0x00, 0xff, 'n', 'o', 't', '-', 'j', 's', 'o', 'n'}
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp", Meta: meta})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	list, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range list {
		if s.ID == id && !bytes.Equal(s.Meta, meta) {
			t.Errorf("meta = %v, want %v", s.Meta, meta)
		}
	}

	updated := []byte{0x01, 0x02, 0x03}
	if err := c.SetMeta(id, updated); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	list, err = c.List()
	if err != nil {
		t.Fatalf("List after SetMeta: %v", err)
	}
	for _, s := range list {
		if s.ID == id && !bytes.Equal(s.Meta, updated) {
			t.Errorf("meta after SetMeta = %v, want %v", s.Meta, updated)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func readLog(socketPath string) string {
	b, err := os.ReadFile(LogPath(socketPath))
	if err != nil {
		return "(no daemon log)"
	}
	return strings.TrimSpace(string(b))
}

// countDaemons counts running daemons bound to this exact socket path. It matches on
// the socket argument rather than the program name so it can never see, let alone
// count, the user's real daemon.
func countDaemons(socketPath string) int {
	out, err := exec.Command("pgrep", "-f", "muxd --socket "+socketPath).Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// stopDaemon terminates the daemon for this socket. Scoped to the isolated path for the
// same reason as countDaemons — a broad pkill here would hit the user's live sessions,
// which has actually happened in this repo's history with tmux.
func stopDaemon(socketPath string) {
	out, err := exec.Command("pgrep", "-f", "muxd --socket "+socketPath).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_ = exec.Command("kill", line).Run()
	}
}

// TestDaemonClientSelfHealsAfterDaemonDeath covers the failure that would otherwise force
// the user to restart everything.
//
// The daemon outlives the server, but not the reverse: it can be killed, crash, or be
// restarted for an upgrade. When that happens the server is left holding a dead socket,
// and without self-healing EVERY later operation fails — new tab, list, input — until
// someone restarts the server. That is precisely the "restart everything" experience this
// daemon exists to abolish, so it must not reappear one layer up.
func TestDaemonClientSelfHealsAfterDaemonDeath(t *testing.T) {
	path := isolatedDaemon(t)
	t.Cleanup(func() { stopDaemon(path) })

	c, err := ConnectOrSpawn(path)
	if err != nil {
		t.Fatalf("ConnectOrSpawn: %v (log: %s)", err, readLog(path))
	}
	defer c.Close()

	if _, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Kill the daemon out from under the client, the way a crash or an upgrade does.
	stopDaemon(path)
	waitFor(t, 5*time.Second, "daemon to be gone", func() bool { return countDaemons(path) == 0 })

	// First, what recovery must NOT do: an observer must not create what it observes.
	// List reports the absence definitively and starts nothing. (This assertion is newer
	// than the test: List used to spawn, which made it a fourth path by which a stray
	// daemon could be brought into existence — reachable, through Restore, from an event
	// handler. "Only Create spawns" is the rule now; see connectPolicy.)
	if _, err := c.List(); err == nil {
		t.Fatal("List succeeded with no daemon running — it must have started one")
	} else if !IsNoDaemon(err) {
		t.Fatalf("List error = %v, want a NoDaemonError so the caller can conclude the sessions are gone", err)
	}
	if n := countDaemons(path); n != 0 {
		t.Fatalf("%d daemons after a mere List; listing must never spawn", n)
	}

	// Then the actual promise: the client is usable again WITHOUT restarting the server.
	// Create is the one operation a fresh daemon can satisfy, so Create is what revives it.
	if _, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"}); err != nil {
		t.Fatalf("Create after daemon death did not self-heal: %v (log: %s)", err, readLog(path))
	}
	if n := countDaemons(path); n != 1 {
		t.Errorf("%d daemons after self-heal, want 1", n)
	}
	// The sessions the dead daemon held are gone with it — persistence is process-level by
	// design (same as tmux). What matters is that the client WORKS again.
	list, err := c.List()
	if err != nil {
		t.Fatalf("List against the revived daemon: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("revived daemon reports %d sessions, want 1 (only the one just created)", len(list))
	}

	// And it is fully usable, not merely connected.
	if _, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}, Cwd: "/tmp"}); err != nil {
		t.Errorf("Create after self-heal: %v", err)
	}
}

// TestDaemonReattachResumesWithoutDuplicatingScrollback pins the offset bookkeeping.
//
// A re-attach that asked for the whole ring again would paste the user's entire
// scrollback on top of itself — the screen would fill with a duplicate of what was
// already there. The daemon can only avoid that if the client can say where it got to,
// which is what AttachAck.ReplayBytes and Stream.Consumed exist for.
func TestDaemonReattachResumesWithoutDuplicatingScrollback(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	var writeEnd *os.File
	factory := func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		writeEnd = w
		return r, nil, nil
	}
	d := NewDaemonWith(1<<16, -1, factory)
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

	first := []byte("FIRST-CHUNK-AAAA")
	if _, err := writeEnd.Write(first); err != nil {
		t.Fatalf("inject: %v", err)
	}

	s1, err := c.Attach(id, AttachOptions{})
	if err != nil {
		t.Fatalf("attach 1: %v", err)
	}
	got1 := drain(s1, len(first), 3*time.Second)
	if !bytes.Contains(got1, first) {
		t.Fatalf("first attach did not replay %q, got %q", first, got1)
	}
	resume := s1.Consumed()
	_ = s1.Close()

	// More output arrives while nothing is attached.
	second := []byte("SECOND-CHUNK-BBBB")
	if _, err := writeEnd.Write(second); err != nil {
		t.Fatalf("inject 2: %v", err)
	}

	s2, err := c.Attach(id, AttachOptions{Since: &resume})
	if err != nil {
		t.Fatalf("attach 2: %v", err)
	}
	defer s2.Close()
	got2 := drain(s2, len(second), 3*time.Second)

	if !bytes.Contains(got2, second) {
		t.Errorf("re-attach missed output written while detached: want %q in %q", second, got2)
	}
	if bytes.Contains(got2, first) {
		t.Errorf("re-attach replayed already-seen bytes %q — the user's scrollback would duplicate", first)
	}

	// The resume point must be an ABSOLUTE ring offset, not a per-stream byte count.
	//
	// This assertion is the one that actually exercises AttachAck.ReplayBytes. The two
	// checks above cannot: on a fresh ring the first attach has Offset == ReplayBytes, so
	// a client that ignored the field entirely would still compute the right resume point
	// by luck and pass. The divergence only shows up on the SECOND stream, where the
	// replay covers just part of the ring — which is exactly the case a real reconnect
	// hits, and exactly the case that would silently duplicate scrollback.
	wantOffset := int64(len(first) + len(second))
	if got := s2.Consumed(); got != wantOffset {
		t.Errorf("Consumed() after re-attach = %d, want %d (absolute ring offset); "+
			"a third attach would resume from the wrong place and re-deliver bytes the user already saw",
			got, wantOffset)
	}
}

// drain collects at least want bytes from a stream, or gives up at the deadline.
func drain(s *Stream, want int, timeout time.Duration) []byte {
	var out []byte
	deadline := time.After(timeout)
	for len(out) < want {
		select {
		case ev, ok := <-s.Events:
			if !ok {
				return out
			}
			out = append(out, ev.Data...)
		case <-deadline:
			return out
		}
	}
	return out
}
