package muxd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Client is the server-side half: a control connection to the daemon.
//
// Closing it DETACHES. It never destroys sessions — that separation is the reason this
// package exists, and collapsing the two is how the old code killed every terminal
// whenever the HTTP server shut down.
type Client struct {
	path string

	mu   sync.Mutex // serialises request/response on the single control connection
	conn net.Conn
	// closed distinguishes "the caller is done with this client" from "we have no
	// connection at the moment". Both leave conn nil, and conflating them bricks the
	// client: one failed reconnect nils the conn, and every later operation then reports
	// "client is closed" — a state no retry can leave, which is the "you have to restart
	// the server" experience this whole package exists to abolish.
	closed bool
}

// Connect opens a control connection to an already-running daemon.
func Connect(path string) (*Client, error) {
	c, err := Dial(path)
	if err != nil {
		return nil, err
	}
	return &Client{path: path, conn: c}, nil
}

// SpawnTimeout bounds how long ConnectOrSpawn waits for a freshly started daemon to
// begin listening.
const SpawnTimeout = 10 * time.Second

// ConnectOrSpawn connects to the daemon, starting one if none is listening — the same
// shape as a tmux client, and the reason no setup step or service manager is required.
//
// Concurrency, in two layers — worth stating precisely, because it is easy to credit
// the wrong one:
//
//   - What actually GUARANTEES a single daemon is Listen: binding a unix socket is
//     atomic, and Listen refuses to unlink a socket that something is still answering
//     on. A second daemon therefore cannot displace the first; it fails and exits.
//     (Without that refusal the session set would silently split in two — half the
//     user's terminals unreachable, no error anywhere. See TestProtoListenRefuses…)
//   - What this lock adds is avoiding a SPAWN STORM: without it, N servers starting at
//     once each fork a daemon, and N-1 of them start, lose the bind, and exit. Nothing
//     breaks, but it is N-1 pointless process launches and N-1 alarming log lines.
//
// The connect is retried once the lock is held because the winner will have spawned the
// daemon while the loser waited.
//
// A version mismatch is NOT a reason to spawn: a daemon IS running, it simply speaks a
// different protocol. Spawning a second one would evict nothing and fix nothing, so the
// typed error is returned for the caller to surface ("restarting the daemon will end N
// live sessions").
func ConnectOrSpawn(path string) (*Client, error) {
	conn, err := dialOrSpawn(path)
	if err != nil {
		return nil, err
	}
	return &Client{path: path, conn: conn}, nil
}

// dialOrSpawn returns a live, handshaken connection, starting a daemon if none answers.
// It is shared by ConnectOrSpawn and by reconnect so "how do I get a connection" has one
// implementation rather than two that drift.
func dialOrSpawn(path string) (net.Conn, error) {
	if conn, err := Dial(path); err == nil {
		return conn, nil
	} else if isVersionMismatch(err) {
		return nil, err
	}

	// The socket directory must exist before either the lock file or the daemon log can
	// be created there. It is created here, explicitly, rather than as a side effect of
	// taking the lock.
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("muxd: create socket dir: %w", err)
	}

	unlock, err := lockSpawn(path)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// Someone may have won the race while we waited for the lock.
	if conn, err := Dial(path); err == nil {
		return conn, nil
	} else if isVersionMismatch(err) {
		return nil, err
	}

	if err := spawnDaemon(path); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(SpawnTimeout)
	for time.Now().Before(deadline) {
		if conn, err := Dial(path); err == nil {
			return conn, nil
		} else if isVersionMismatch(err) {
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("muxd: daemon did not start listening on %s within %s (see %s)",
		path, SpawnTimeout, LogPath(path))
}

func isVersionMismatch(err error) bool {
	var v *VersionMismatchError
	return errors.As(err, &v)
}

// lockSpawn takes an exclusive flock on <socket>.lock. It does ONE thing: mutual
// exclusion. (It used to create the socket directory as a side effect, which made it
// impossible to test the locking in isolation — removing the lock also removed the
// directory, so the failure looked like a missing file rather than a lost race.)
func lockSpawn(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, sockPerm)
	if err != nil {
		return nil, fmt.Errorf("muxd: open spawn lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("muxd: acquire spawn lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// EnvDaemonBin overrides which binary is exec'd as the daemon.
//
// Normally the answer is "this same binary" (os.Executable), which is what guarantees
// the daemon and the server always speak the same protocol version. The override exists
// because a test binary is not the dw-terminal binary, and because a build that lives
// somewhere unusual should still be startable without reinstalling. It reaches only
// what the invoking user could already execute, so it grants no new access.
const EnvDaemonBin = "DW_MUXD_BIN"

func daemonBinary() (string, error) {
	if p := os.Getenv(EnvDaemonBin); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// The second door, and the one that was left unlocked.
	//
	// SocketPath already refuses to hand a test binary the real socket. This refuses the
	// mirror image: exec'ing a TEST BINARY as the daemon. A `go test` binary given the
	// arguments `muxd --socket X` does not serve anything — it ignores them and runs the
	// entire suite again, which then spawns more of itself. Observed in the wild as
	// `deepwork-terminal.test muxd --socket /tmp/TestFoo…` processes still resident hours
	// after the run, and it is a spawn-storm hazard, not merely litter.
	//
	// It reaches here when a fixture sets DW_MUXD_SOCKET (bypassing the first guard) and a
	// goroutine outliving the test spawns after t.Setenv has already restored DW_MUXD_BIN.
	// Failing loudly is the point: silence is what let it run for hours unnoticed.
	if looksLikeTestBinary(exe) {
		return "", fmt.Errorf("muxd: refusing to exec the test binary %q as the daemon "+
			"(it would re-run the test suite, not serve); set %s to a real dw-terminal build", exe, EnvDaemonBin)
	}
	return exe, nil
}

// LogPath is where a spawned daemon writes its stderr. Named so a failure message can
// point at it: a daemon that dies during startup is otherwise completely silent.
func LogPath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "daemon.log")
}

// spawnDaemon starts `<this binary> muxd --socket <path>` detached from the caller.
//
// Setsid is what makes it survive: the daemon leaves the caller's session entirely, so
// the terminal or supervisor that stops the server cannot take the daemon with it. This
// is precisely the mechanism tmux uses, and precisely what the old in-process PTYs
// lacked.
func spawnDaemon(path string) error {
	exe, err := daemonBinary()
	if err != nil {
		return fmt.Errorf("muxd: locate own binary: %w", err)
	}
	logFile, err := os.OpenFile(LogPath(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, sockPerm)
	if err != nil {
		return fmt.Errorf("muxd: open daemon log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "muxd", "--socket", path)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("muxd: start daemon: %w", err)
	}
	// Reap in the background. Without this the exited daemon would linger as a zombie
	// for as long as the server lives; with it, an early crash is collected promptly.
	go func() { _ = cmd.Wait() }()
	return nil
}

// ---------------------------------------------------------------------------
// Control operations.
// ---------------------------------------------------------------------------

// connectPolicy decides whether an operation may bring a NEW daemon into existence when
// the old one is gone. It is stated once, here, and named at every call site so the
// decision is never made by accident.
//
// The rule is one sentence, and it is now as small as it can be: **only Create spawns.**
//
//   - Create is the sole operation whose meaning is satisfied by a brand-new empty daemon
//     — "give me a session" needs somewhere to put one. That is what lets this work with
//     no service manager and no setup step.
//   - Everything else either NAMES AN EXISTING SESSION (attach, input, resize, kill,
//     destroy, set-meta) or ASKS WHAT EXISTS (list, subscribe). A fresh daemon answers
//     the first group with not_found and the second with "nothing" — in both cases the
//     spawn bought nothing and left a process behind.
//
// List used to be on the spawning side, on the reasoning that it means "I need a daemon".
// It does not: a daemon that was just created holds no sessions, so listing against it is
// an expensive way to learn the empty answer we already had. That mistake was the fourth
// distinct path found to resurrect a daemon — after attach, subscribe, and re-attach —
// and it was reachable from an EVENT HANDLER, which made an observer a creator through
// two layers of indirection.
type connectPolicy bool

const (
	spawnIfAbsent connectPolicy = true
	requireDaemon connectPolicy = false
)

// roundTrip sends one request and reads one reply, reconnecting once if the connection
// turns out to be dead.
//
// Why the retry is not optional: the daemon outlives the server, but not necessarily the
// other way round. It can be killed, crash, run out of memory, or be restarted for an
// upgrade — and when that happens the server is left holding a corpse of a socket. Every
// subsequent operation would fail until someone restarted the server, which is precisely
// the "you must restart everything" experience this daemon exists to abolish. Reconnect
// puts the recovery where the knowledge lives: this Client is the only thing that holds
// both the socket path and the ability to start a daemon.
//
// The retry is bounded to ONE attempt and only for connection-level failures. A protocol
// error (MsgError, unexpected frame) is a real answer and is returned as-is; retrying it
// would just ask the same question twice.
//
// A known, accepted limitation: the retry is not idempotent. If a request ARRIVED and its
// reply was lost — a connection reset without the daemon dying — the retry re-issues it. A
// re-issued Create makes a second session (an orphan shell, and an error the user sees); a
// re-issued Destroy gets not_found and reports a failure for a delete that succeeded.
// Fixing it properly needs a request id in the contract, which the protocol deliberately
// does not carry (see the note beside MaxFrameSize). The failure requires a transport reset
// with a healthy daemon on the other side, which is rare enough that a permanent tax on
// every keystroke frame is the worse trade.
func (c *Client) roundTrip(policy connectPolicy, t MsgType, req any, want MsgType, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("muxd: client is closed")
	}
	// No connection because a previous attempt failed to re-establish one. Try again now,
	// under THIS call's policy — a Create may legitimately spawn where the List that lost
	// the connection could not.
	if c.conn == nil {
		if rerr := c.reconnectLocked(policy); rerr != nil {
			return rerr
		}
	}

	err := c.attempt(t, req, want, out)
	if err == nil || !isConnectionLost(err) {
		return err
	}
	if rerr := c.reconnectLocked(policy); rerr != nil {
		return fmt.Errorf("muxd: lost the daemon connection and could not re-establish it: %w", rerr)
	}
	return c.attempt(t, req, want, out)
}

// attempt performs one request/response on the current connection. Caller holds c.mu.
func (c *Client) attempt(t MsgType, req any, want MsgType, out any) error {
	if c.conn == nil {
		return errors.New("muxd: client is closed")
	}
	if err := WriteJSON(c.conn, t, req); err != nil {
		return err
	}
	rt, payload, err := ReadFrame(c.conn)
	if err != nil {
		return err
	}
	if rt == MsgError {
		var e ErrorPayload
		if err := json.Unmarshal(payload, &e); err != nil {
			return fmt.Errorf("muxd: undecodable error frame: %w", err)
		}
		return &ProtocolError{Code: e.Code, Msg: e.Msg}
	}
	if rt != want {
		return fmt.Errorf("muxd: expected frame %d, got %d", want, rt)
	}
	if out != nil {
		return json.Unmarshal(payload, out)
	}
	return nil
}

// reconnectLocked replaces a dead connection with a live one, starting a daemon only if
// the policy allows it. Caller holds c.mu.
func (c *Client) reconnectLocked(policy connectPolicy) error {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	var (
		conn net.Conn
		err  error
	)
	if policy == spawnIfAbsent {
		conn, err = dialOrSpawn(c.path)
	} else {
		conn, err = Dial(c.path)
	}
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// isConnectionLost reports whether an error means "this socket is dead" as opposed to
// "the daemon answered, and the answer was no".
//
// The distinction decides whether retrying could possibly help. Only transport failures
// qualify: a refusal that arrived intact is a real answer, and asking again would just
// get the same one.
func isConnectionLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ENOTCONN) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// Create spawns a session and returns its daemon-assigned id and shell pid.
func (c *Client) Create(req CreateReq) (id string, shellPID int, err error) {
	var ack CreateAck
	if err := c.roundTrip(spawnIfAbsent, MsgCreate, req, MsgCreateAck, &ack); err != nil {
		return "", 0, err
	}
	return ack.ID, ack.ShellPID, nil
}

// List returns every session the daemon holds.
func (c *Client) List() ([]SessionSummary, error) {
	var ack ListAck
	if err := c.roundTrip(requireDaemon, MsgList, struct{}{}, MsgListAck, &ack); err != nil {
		return nil, err
	}
	return ack.Sessions, nil
}

// Input writes bytes to a session without attaching.
func (c *Client) Input(id string, data []byte) error {
	return c.roundTrip(requireDaemon, MsgInputTo, InputReq{ID: id, Data: data}, MsgOK, nil)
}

// Resize sets a session's window size.
func (c *Client) Resize(id string, cols, rows uint16) error {
	return c.roundTrip(requireDaemon, MsgResize, ResizeReq{ID: id, Cols: cols, Rows: rows}, MsgOK, nil)
}

// Signal sends a signal to a session's process group.
func (c *Client) Signal(id string, sig int) error {
	if sig == 0 {
		return errors.New("muxd: signal 0 is reserved for Destroy")
	}
	return c.roundTrip(requireDaemon, MsgKill, KillReq{ID: id, Sig: sig}, MsgOK, nil)
}

// KillForeground sends sig to the PTY's current foreground process group.
func (c *Client) KillForeground(id string, sig int) error {
	return c.roundTrip(requireDaemon, MsgKill, KillReq{ID: id, Sig: sig, Foreground: true}, MsgOK, nil)
}

// Destroy terminates a session and forgets it. This is the EXPLICIT teardown — the one
// a user triggers by deleting a tab. Shutting the server down must never call it.
func (c *Client) Destroy(id string) error {
	return c.roundTrip(requireDaemon, MsgDestroy, SimpleReq{ID: id}, MsgOK, nil)
}

// SetMeta replaces a session's opaque metadata blob.
func (c *Client) SetMeta(id string, meta []byte) error {
	return c.roundTrip(requireDaemon, MsgSetMeta, SetMetaReq{ID: id, Meta: meta}, MsgOK, nil)
}

// Close detaches this client. Sessions keep running — that is the contract.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// SocketPath reports which socket this client is bound to.
func (c *Client) SocketPath() string { return c.path }

// Subscribe opens a connection that receives lifecycle events for EVERY session the
// daemon holds — including ones this process did not create.
//
// That last part is the point. The daemon is per-user, not per-server, so a session
// started in one host (say the standalone build on :18074) is equally real to another
// (the embedded one on :8087). Without this, each host would only learn about the other's
// sessions the next time it happened to List, and a tab created in one window would sit
// invisible in the other until something forced a refresh.
//
// The returned channel closes when the connection drops or cancel is called; callers that
// need to keep watching should re-Subscribe.
func (c *Client) Subscribe() (<-chan Event, func(), error) {
	// Dial, not dialOrSpawn — requireDaemon, like Attach.
	//
	// This started out as spawnIfAbsent, reasoning that a live server ought to have a live
	// daemon and the watcher's retry loop was a fine place to guarantee it. That reasoning
	// was wrong, and measurably so: a watcher retries every couple of seconds forever, so
	// any watcher that outlives its server (a test that never calls Close, a cancelled
	// context that loses a race) spawns daemons indefinitely. Stray daemons found after a
	// test run were traced to exactly this loop.
	//
	// The principle it violated: an OBSERVER must not create what it observes. Sessions
	// justify a daemon; watching does not. Nothing is lost — the moment anything creates a
	// session a daemon exists, and the next retry finds it.
	conn, err := Dial(c.path)
	if err != nil {
		return nil, nil, err
	}
	if err := WriteJSON(conn, MsgSubscribe, struct{}{}); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	// The daemon acks the subscription before any event, so a caller that returns
	// successfully knows it is actually registered rather than merely connected.
	t, payload, err := ReadFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if t == MsgError {
		var e ErrorPayload
		_ = json.Unmarshal(payload, &e)
		_ = conn.Close()
		return nil, nil, &ProtocolError{Code: e.Code, Msg: e.Msg}
	}
	if t != MsgOK {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("muxd: subscribe: unexpected frame %d", t)
	}

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		for {
			t, payload, err := ReadFrame(conn)
			if err != nil {
				return
			}
			if t != MsgEvent {
				continue
			}
			var ev Event
			if err := json.Unmarshal(payload, &ev); err != nil {
				continue
			}
			// Never block the daemon on a slow consumer — but when something must go,
			// discard the OLDEST rather than the newest. See dropOldest.
			pushNewest(events, ev)
		}
	}()
	return events, func() { _ = conn.Close() }, nil
}

// ---------------------------------------------------------------------------
// Attached streams.
// ---------------------------------------------------------------------------

// Stream is a connection attached to one session: scrollback replay followed by live
// output, with keystrokes flowing the other way.
type Stream struct {
	conn   net.Conn
	Cols   uint16
	Rows   uint16
	Offset int64
	Alive  bool

	// ReplayFrom is the ring offset the replay actually STARTED at.
	//
	// It is not always the `since` that was asked for, and the difference matters: if the
	// daemon's ring has since wrapped past that point, those bytes are gone from the daemon
	// too and the replay begins later. A client that appends the replay to what it already
	// had would then stitch two non-adjacent segments into one buffer — which renders as a
	// perfectly plausible screen that never existed.
	ReplayFrom int64

	// Events is everything that happens on this stream, IN ORDER.
	//
	// One channel, not three. Output, resizes and exits used to arrive on separate
	// channels, and a consumer selecting among them gets whatever is ready — so a resize
	// could be delivered before the bytes drawn under the old grid, and an exit could be
	// taken while the program's final line was still sitting in the output queue. Neither
	// is a cosmetic ordering detail: the first decides whether a TUI redraw is interpreted
	// on the right grid, the second decides whether `attach` prints the answer a command
	// just produced.
	Events <-chan StreamEvent

	wmu sync.Mutex

	// cmu guards consumed: the ring offset this stream has delivered up to. A re-attach
	// passes it as `since` so the daemon replays only what was missed instead of the whole
	// buffer — otherwise every reconnect would paste the user's entire scrollback on top
	// of itself.
	cmu      sync.Mutex
	consumed int64
	// holed records that bytes were lost somewhere in this stream — dropped here because
	// the consumer fell behind, or dropped by the daemon and announced with MsgGap.
	//
	// Once it is set, `consumed` is no longer a truthful resume point. It is a SUM of the
	// bytes this stream delivered, not an absolute position, so a loss anywhere but the
	// tail leaves it pointing into the middle of what the client actually holds: resuming
	// there re-delivers bytes it already has (duplication) or skips bytes it never got
	// (a hole), and both render as a screen that never existed. A holed stream must be
	// resumed with a FULL replay and a cleared cache.
	holed bool
}

// Holed reports that this stream lost bytes, so its resume point cannot be trusted.
func (s *Stream) Holed() bool {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return s.holed
}

func (s *Stream) markHoled() {
	s.cmu.Lock()
	s.holed = true
	s.cmu.Unlock()
}

// StreamEvent is one thing that happened on an attached stream. Exactly one field is set.
type StreamEvent struct {
	// Data is PTY output.
	Data []byte
	// Resize is the session's new grid, as of this point in the stream — everything after
	// it was drawn at this size.
	Resize *[2]uint16
	// Exit is the process's exit code. It is the last event on the stream.
	Exit *int
	// Gap means bytes were lost before this point, so the stream's resume point can no
	// longer be trusted (see Holed).
	Gap bool
}

// Consumed reports the ring offset this stream has delivered up to.
func (s *Stream) Consumed() int64 {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return s.consumed
}

func (s *Stream) advance(n int) {
	s.cmu.Lock()
	s.consumed += int64(n)
	s.cmu.Unlock()
}

// Attach opens a NEW connection bound to one session. It is deliberately not the
// control connection: a stream idles indefinitely and carries raw bytes, while the
// control connection stays request/response.
//
// since, when non-nil, is a ring offset from a previous attach — the daemon then
// replays only what happened after it instead of the whole buffer.
// AttachOptions describes how this client will display the session.
//
// Cols/Rows declare the terminal this attachment lives in. Leaving them zero means
// OBSERVER: watch without constraining the session's size, so looking at a session from a
// small window does not reflow the one someone else is working in. See AttachReq.
type AttachOptions struct {
	Since *int64
	Cols  uint16
	Rows  uint16
}

func (c *Client) Attach(id string, opts AttachOptions) (*Stream, error) {
	// Dial, never dialOrSpawn (requireDaemon — see connectPolicy). Attach names a session
	// that must already exist, so a daemon started here could only answer not_found. The
	// two failures a caller must tell apart are "no daemon" and "no such session", and
	// both now arrive as typed errors — *NoDaemonError and a not_found *ProtocolError —
	// which is exactly the same information a spawn would have bought, without the
	// process. An earlier version DID spawn here, and re-attach goroutines racing a
	// shutdown left daemons running for hours with nothing in them.
	conn, err := Dial(c.path)
	if err != nil {
		return nil, err
	}
	if err := WriteJSON(conn, MsgAttach, AttachReq{ID: id, Since: opts.Since, Cols: opts.Cols, Rows: opts.Rows}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	t, payload, err := ReadFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if t == MsgError {
		var e ErrorPayload
		_ = json.Unmarshal(payload, &e)
		_ = conn.Close()
		return nil, &ProtocolError{Code: e.Code, Msg: fmt.Sprintf("attach %s: %s", id, e.Msg)}
	}
	if t != MsgAttachAck {
		_ = conn.Close()
		return nil, fmt.Errorf("muxd: attach %s: unexpected frame %d", id, t)
	}
	var ack AttachAck
	if err := json.Unmarshal(payload, &ack); err != nil {
		_ = conn.Close()
		return nil, err
	}

	events := make(chan StreamEvent, 256)
	s := &Stream{
		conn: conn, Cols: ack.Cols, Rows: ack.Rows, Offset: ack.Offset, Alive: ack.Alive,
		ReplayFrom: ack.Offset - ack.ReplayBytes,
		Events:     events,
	}
	// The replay ends at ack.Offset, so start counting from where it began. Live frames
	// then advance the counter past it.
	s.consumed = s.ReplayFrom
	go func() {
		defer close(events)
		for {
			t, payload, err := ReadFrame(conn)
			if err != nil {
				return
			}
			switch t {
			case MsgOutput:
				// Advance ONLY on a successful hand-off. `consumed` is the resume point a
				// re-attach sends as `since`, so counting a byte we then dropped tells the
				// daemon to skip it — and it is gone for good: the server's ring is fed
				// solely by this channel and never re-syncs, so the hole is replayed to
				// every browser and every overview render, forever, while the daemon's own
				// ring still holds the bytes.
				select {
				case events <- StreamEvent{Data: payload}:
					s.advance(len(payload))
				default:
					// Not advanced, AND flagged. Leaving the counter behind is only correct
					// while the loss is at the tail; a frame dropped in the middle of a run
					// leaves the counter pointing inside what we hold, and no arithmetic can
					// recover from that. The flag says so honestly.
					s.markHoled()
					pushEvent(events, StreamEvent{Gap: true})
				}
			case MsgGap:
				// The daemon dropped output meant for us. Same conclusion as dropping it
				// ourselves: this stream's resume point is no longer trustworthy.
				s.markHoled()
				pushEvent(events, StreamEvent{Gap: true})
			case MsgResized:
				var p ResizedPayload
				if err := json.Unmarshal(payload, &p); err != nil {
					continue
				}
				sz := [2]uint16{p.Cols, p.Rows}
				pushEvent(events, StreamEvent{Resize: &sz})
			case MsgExited:
				var p ExitedPayload
				_ = json.Unmarshal(payload, &p)
				code := p.ExitCode
				pushEvent(events, StreamEvent{Exit: &code})
				return
			}
		}
	}()
	return s, nil
}

// Write sends keystrokes to the attached session.
func (s *Stream) Write(p []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return WriteFrame(s.conn, MsgInput, p)
}

// Resize sets the attached session's size over this stream's connection.
// Resize declares THIS attachment's new size — "the window I am showing the session in is
// now this big". The session re-derives its own size from every attachment; it does not
// simply become what this client asked for. See ResizeReq.
func (s *Stream) Resize(id string, cols, rows uint16) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return WriteJSON(s.conn, MsgResize, ResizeReq{ID: id, Cols: cols, Rows: rows})
}

// Close detaches. The session keeps running.
func (s *Stream) Close() error { return s.conn.Close() }

// pushEvent delivers a stream event that must not be lost, making room by discarding the
// oldest queued one. Resizes, gaps and exits are all in this class: losing a resize leaves
// the consumer painting onto the wrong grid, losing a gap leaves it trusting a bad resume
// point, and losing an exit leaves it waiting for a session that has already ended.
func pushEvent(ch chan StreamEvent, ev StreamEvent) {
	select {
	case ch <- ev:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- ev:
	default:
	}
}

// pushNewest delivers ev, discarding the oldest queued event if the buffer is full.
//
// The obvious implementation drops the NEWEST — and that is what this used to do, and it
// is subtly wrong for a stream whose only job is to say "something changed, go look".
// Losses arrive as a contiguous tail: a consumer that falls behind receives 1..N and then
// silence, so the numbers it holds are unbroken and the gap only becomes visible on the
// next event to arrive. If the burst was the last thing that happened, that event never
// comes and the view stays wrong indefinitely.
//
// Discarding the oldest inverts that. The newest event always lands, it always carries the
// highest sequence number, and so the gap is visible the instant the consumer drains —
// with no extra protocol, no timer, and no second goroutine. For a change-notification
// stream, recency is worth strictly more than completeness: the authoritative answer is
// always a fresh List, and only the prompt to fetch it can be lost.
func pushNewest(ch chan Event, ev Event) {
	select {
	case ch <- ev:
		return
	default:
	}
	select {
	case <-ch: // make room by discarding the oldest
	default:
	}
	select {
	case ch <- ev:
	default: // a concurrent producer refilled it; the sequence still reveals the loss
	}
}
