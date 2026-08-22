package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/brightman-ai/deepwork-terminal/ansisignal"
	"github.com/brightman-ai/deepwork-terminal/muxd"
	"github.com/brightman-ai/kit/obs"
)

// sessionMeta is everything the SERVER knows about a session that the daemon does not.
//
// It travels as the daemon's opaque Meta blob. That is the whole trick behind being able
// to upgrade freely: this struct can gain, lose, or reshape fields whenever the product
// needs to, and the daemon — which must stay frozen, because restarting it ends live
// sessions — never has to learn about the change. The daemon stores bytes.
type sessionMeta struct {
	Name      string    `json:"name"`
	Title     string    `json:"title"`
	Engine    string    `json:"engine"`
	CWD       string    `json:"cwd"`
	ShellPath string    `json:"shell"`
	CreatedAt time.Time `json:"created_at"`
}

func (m sessionMeta) encode() []byte {
	b, err := json.Marshal(m)
	if err != nil {
		// A meta blob that cannot be encoded would cost the session its name, not its
		// life; the session is still perfectly usable, so this must not be fatal.
		logger.Warn("encode session meta failed", "error", err)
		return nil
	}
	return b
}

// decodeSessionMeta is tolerant on purpose: a blob written by a NEWER server (unknown
// fields) or an OLDER one (missing fields) must still yield a usable session rather than
// an error. Being strict here would turn "you upgraded while sessions were running" into
// "your tabs came back nameless or not at all".
func decodeSessionMeta(b []byte) sessionMeta {
	var m sessionMeta
	if len(b) == 0 {
		return m
	}
	if err := json.Unmarshal(b, &m); err != nil {
		logger.Warn("decode session meta failed; session kept with defaults", "error", err)
	}
	return m
}

// SessionLabel renders a human name for a session from the daemon's opaque metadata
// blob, falling back to a short id when the blob says nothing useful.
//
// It is exported for ONE reason: there is now a second client. The CLI (`dw-terminal ls`,
// `dw-terminal attach work`) has to resolve the same human names the web UI shows, and
// the only honest way to do that is to decode the blob with the same decoder rather than
// re-implementing its schema in the command package. The daemon still never interprets
// the blob — the split it protects is daemon-vs-server, not server-vs-CLI.
func SessionLabel(meta []byte, id string) string {
	m := decodeSessionMeta(meta)
	if m.Name != "" {
		return m.Name
	}
	if m.Title != "" {
		return m.Title
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// SessionCWD reports the working directory recorded in a session's metadata blob, for the
// same reason as SessionLabel: the CLI has to show what the web UI shows.
func SessionCWD(meta []byte) string { return decodeSessionMeta(meta).CWD }

// client returns the daemon connection, establishing it on first use.
//
// Connect-or-spawn: if no daemon is listening, one is started. There is no fallback to
// an in-process PTY — a daemon that cannot start is a hard, explicit failure. A silent
// downgrade would be worse than an error, because the user would get a terminal that
// works right up until the moment they restart and lose everything, with nothing having
// warned them.
// errDetached is returned by every daemon-facing accessor after the manager has been
// shut down, and it is what stops shutdown from quietly undoing itself.
//
// Detaching closes each session's stream. The pump reads that as "the connection dropped
// but the session did not end" — which is exactly right in every other circumstance — and
// re-attaches about a hundred milliseconds later, connection and all. The manager that
// was told to stop then holds a full set of live streams, pump goroutines and daemon
// connections that nobody will ever close. Standalone hides it (process exit collects
// everything); the embedded host builds and tears this subsystem down repeatedly, so it
// leaks a set per rebuild, and the leaked connections keep the daemon's idle watchdog
// from ever firing.
var errDetached = errors.New("terminal: session manager is detached from the daemon")

func (m *SessionManager) client() (*muxd.Client, error) {
	m.muxMu.Lock()
	defer m.muxMu.Unlock()
	if m.detached {
		return nil, errDetached
	}
	if m.mux != nil {
		return m.mux, nil
	}

	if m.ptyFactory != nil {
		if err := m.startLocalDaemonLocked(); err != nil {
			return nil, err
		}
	}

	path, err := m.socketPathLocked()
	if err != nil {
		return nil, err
	}

	c, err := muxd.ConnectOrSpawn(path)
	if err != nil {
		return nil, fmt.Errorf("terminal: connect to session daemon: %w", err)
	}
	m.mux = c
	return c, nil
}

// existingClient is client() for operations that address a session that ALREADY EXISTS:
// it connects, but it never starts a daemon.
//
// It is the SessionManager-level half of muxd's connectPolicy, and it exists because
// closing that door one layer down was not enough. attach() calls client() before it
// calls Attach, so a re-attach goroutine that outlived its server still brought a daemon
// into being — through client(), not through Attach. Two doors into the same room; both
// had to be shut.
//
// Attaching never legitimately needs a fresh daemon: every attach follows a Create or a
// List against a daemon that was there a moment ago. If it has since gone, the session
// went with it, and a brand-new daemon can only say so more slowly.
func (m *SessionManager) existingClient() (*muxd.Client, error) {
	m.muxMu.Lock()
	defer m.muxMu.Unlock()
	if m.detached {
		return nil, errDetached
	}
	if m.mux != nil {
		return m.mux, nil
	}
	path, err := m.socketPathLocked()
	if err != nil {
		return nil, err
	}
	c, err := muxd.Connect(path)
	if err != nil {
		return nil, err
	}
	m.mux = c
	return c, nil
}

// socketPathLocked resolves the daemon socket once and caches it. Caller holds muxMu.
//
// Both accessors need the path and neither may resolve it twice: a server that adopted
// one path at startup and a different one later would silently split its own view.
func (m *SessionManager) socketPathLocked() (string, error) {
	if m.muxSocket != "" {
		return m.muxSocket, nil
	}
	p, err := muxd.SocketPath()
	if err != nil {
		return "", fmt.Errorf("terminal: resolve muxd socket: %w", err)
	}
	m.muxSocket = p
	return p, nil
}

// DaemonHealth is what the product can SAY about the daemon: is it reachable, who is it,
// how much of the user's work is inside it, and — when something is wrong — what the user
// should do about it.
//
// Problem and Remedy are separate fields on purpose. A UI that only receives prose can
// print it; a UI that receives a remedy can offer a button. The remedy is the half that
// is usually missing, and its absence is what turns a recoverable situation ("your
// daemon is from the previous build; restarting it ends 7 sessions") into an
// unrecoverable-looking one ("failed to connect to session daemon").
// StartedAt is a POINTER because `omitempty` does not omit a zero time.Time — it renders
// "0001-01-01T00:00:00Z", which reads to a consumer as a real timestamp from the year 1.
// A daemon too old to report when it started must be indistinguishable from one that was
// never asked: absent, not epoch-zero. (The wire protocol dodges this by carrying unix
// seconds; this DTO is the same hazard one layer up, and it shipped once before this was
// caught in the live /api/system response.)
type DaemonHealth struct {
	Socket    string     `json:"socket"`
	Connected bool       `json:"connected"`
	PID       int        `json:"pid,omitempty"`
	Proto     int        `json:"proto,omitempty"`
	Sessions  int        `json:"sessions,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Problem   string     `json:"problem,omitempty"`
	Remedy    string     `json:"remedy,omitempty"`
}

// DaemonHealth probes the daemon WITHOUT starting one.
//
// That restraint is the entire point: a health check that spawns the thing it is
// measuring always reports health. "No daemon is running" is a true and useful answer,
// and here it is a benign one — the next session created will start one.
func (m *SessionManager) DaemonHealth() DaemonHealth {
	m.muxMu.Lock()
	path := m.muxSocket
	m.muxMu.Unlock()
	if path == "" {
		p, err := muxd.SocketPath()
		if err != nil {
			return DaemonHealth{Problem: err.Error()}
		}
		path = p
	}

	h := DaemonHealth{Socket: path}
	info, err := muxd.Inspect(path)
	switch {
	case err == nil:
		h.Connected = true
	case muxd.IsNoDaemon(err):
		h.Problem = "no session daemon is running; the next terminal you open will start one"
		return h
	default:
		h.Problem = err.Error()
		var vm *muxd.VersionMismatchError
		if errors.As(err, &vm) {
			h.Remedy = "dw-terminal muxd --restart"
		}
	}
	h.PID, h.Proto, h.Sessions = info.PID, info.Version, info.Sessions
	if started := info.StartedAt(); !started.IsZero() {
		h.StartedAt = &started
	}
	return h
}

// startLocalDaemonLocked hosts a daemon inside this process for test fixtures. Callers
// must hold muxMu.
func (m *SessionManager) startLocalDaemonLocked() error {
	if m.localDaemon != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "dwmux-inproc-")
	if err != nil {
		return err
	}
	path := dir + "/muxd.sock"
	ln, err := muxd.Listen(path)
	if err != nil {
		return err
	}
	d := muxd.NewDaemonWith(m.bufferSize, -1 /* no idle exit */, m.ptyFactory)
	go func() { _ = d.Serve(context.Background(), ln) }()
	m.localDaemon, m.localLn, m.muxSocket = d, ln, path
	return nil
}

// attach opens this session's output stream and starts pumping it into the ring and out
// to WebSocket subscribers.
//
// since carries the ring offset from a previous attach; nil asks for the whole buffer,
// which is what a freshly restarted server wants — it has no scrollback of its own and
// must get the terminal's history back from the daemon.
func (m *SessionManager) attach(sess *Session, since *int64) error {
	// existingClient, not client(): attaching must never start a daemon. See its comment.
	client, err := m.existingClient()
	if err != nil {
		return err
	}
	// What size does this server declare? The smallest window actually watching, and
	// nothing else — see session_viewport.go.
	//
	// This attachment exists for every session the server knows about, browser open or not,
	// because it feeds the local scrollback cache. With no browser on this session that
	// makes it an OBSERVER: a cache is not a window, and under smallest-wins a server that
	// declared a size anyway would constrain a PTY nobody is looking at — two idle servers
	// would even constrain each other.
	//
	// It rides in the attach request rather than following as a resize because a size that
	// arrives second is a size the daemon spent a round trip not knowing: the replay
	// geometry reported in the ack would describe a grid the session is about to leave.
	cols, rows := sess.Viewport()
	stream, err := client.Attach(sess.ID, muxd.AttachOptions{
		Since: since, Cols: uint16(cols), Rows: uint16(rows),
	})
	if err != nil {
		return err
	}
	// Is what we are about to append CONTIGUOUS with what we already hold?
	//
	// The server keeps its own ring — a cache the WebSocket replay and the overview's
	// screen render read without a daemon round trip each. A cache is only sound while it
	// is provably continuous with the authority, and there is exactly one way it stops
	// being so: we ask to resume at `since`, the daemon's ring has meanwhile wrapped past
	// that point, and it replays from later instead. Appending that to the existing buffer
	// splices two segments that were never adjacent, and the result is not visibly corrupt
	// — it is a plausible screen that never existed, replayed to every browser and every
	// overview card for the life of the session.
	//
	// So: if the replay does not begin exactly where we stopped, throw our copy away and
	// let the replay refill it. Losing scrollback is a visible, honest loss; silently
	// inventing it is not.
	if since != nil && stream.ReplayFrom != *since {
		logger.Warn("scrollback is not contiguous with the daemon's; resetting this server's copy",
			"id", sess.ID, "askedFrom", *since, "replayFrom", stream.ReplayFrom)
		sess.Buffer.Reset()
	}

	sess.mu.Lock()
	sess.stream = stream
	sess.mux = client
	sess.mu.Unlock()
	// Adopt the geometry the daemon reports. This is the size the program on the other
	// end actually believes it is drawing into, and the replay grid must match it — a
	// second guess here is precisely how the overview's screen replay drifted before.
	sess.setPTYSizeFromDaemon(int(stream.Cols), int(stream.Rows))
	sess.noteDeclared(cols, rows)
	go m.pumpStream(sess, stream)

	// A viewer may have resized while the attach was in flight — its syncViewport would
	// have found no stream to declare on and returned. Re-deriving now closes that window:
	// this reads the CURRENT minimum, so it is a no-op unless something really changed.
	if _, err := sess.syncViewport(); err != nil {
		logger.Debug("could not re-declare the viewport after attaching", "id", sess.ID, "error", err)
	}
	return nil
}

// pumpStream is the read loop, now fed by the daemon rather than by a file descriptor.
//
// ONE ordered channel carries output, resizes, gaps and the exit. That ordering is not a
// convenience: a resize delivered out of band could land before the bytes drawn at the old
// size, and an exit taken out of band could be handled while the program's final line was
// still queued.
func (m *SessionManager) pumpStream(sess *Session, stream *muxd.Stream) {
	outputLogCtx := obs.WithStage(context.Background(), stgTerminalOutput)
	var signals ansisignal.Scanner
	exited := false
	code := 0

	for ev := range stream.Events {
		switch {
		case ev.Exit != nil:
			exited, code = true, *ev.Exit

		case ev.Resize != nil:
			// The grid moved under us — another client attached, detached, or resized its
			// own window. Record it: the overview replays scrollback onto this size, and a
			// stale value there produces a different screen rather than a resized one.
			// Record it AND pass it on, as one step. Recording it and telling nobody was
			// the half-fix: the browsers watching this session keep painting onto the grid
			// they last chose, so a session shrunk by someone else's window renders as a
			// wrapped, overlapping mess — and it looks like a bug in whatever program is
			// running, not like a resize that never arrived. See applyGrid for why the
			// record and the hand-off cannot be two steps.
			sess.applyGrid(int(ev.Resize[0]), int(ev.Resize[1]))

		case ev.Gap:
			// Nothing to do here — Stream.Holed already records it, and the re-attach path
			// is where it is acted on. Logged because a silent gap is how a wrong screen
			// gets blamed on the program inside.
			logger.Warn("lost session output; the local scrollback copy will be rebuilt",
				"id", sess.ID)

		default:
			data := ev.Data
			observeTerminalOutput(outputLogCtx, sess.ID, data)
			sess.Buffer.Write(data)

			sess.mu.Lock()
			sess.LastActive = time.Now()
			sess.mu.Unlock()

			sess.fanOutData(data)

			// Signal tap — deliberately AFTER the buffer write and the subscriber fan-out,
			// so nothing here can delay a single byte reaching the user's terminal. It is a
			// pure observer: the scanner never consumes or rewrites the stream.
			if m.OnSignal != nil {
				for _, sig := range signals.Feed(data) {
					m.OnSignal(sess, sig)
				}
			}
		}
	}

	if !exited {
		// The connection dropped but the session did not end. Re-attach, resuming from what
		// this stream already delivered — without that offset the daemon would replay the
		// whole ring and the user would watch their scrollback duplicate itself on top of
		// itself.
		logger.Debug("session stream dropped; re-attaching", "id", sess.ID)
		m.reattach(sess, stream.Consumed(), stream.Holed())
		return
	}

	// The session is over, so this stream has nothing left to carry. Closing it releases
	// both ends — a client connection here and a handleConn goroutine in the daemon —
	// which would otherwise be held for as long as the exited tab stays in the list, and
	// each held connection also keeps the daemon's idle watchdog from ever firing.
	_ = stream.Close()
	sess.mu.Lock()
	if sess.stream == stream {
		sess.stream = nil
	}
	sess.mu.Unlock()

	sess.doneOnce.Do(func() {
		sess.mu.Lock()
		sess.Status = StatusExited
		sess.exitCode = code
		sess.mu.Unlock()
		close(sess.done)
		logger.Info("session exited", "id", sess.ID, "exitCode", code)
	})
}

// reattachBackoff is the retry schedule for a dropped stream. It is bounded and short:
// the common cause is a daemon restart, which resolves in a second or two, and a session
// that cannot be reached after this is better reported than retried forever in silence.
var reattachBackoff = []time.Duration{
	100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 4 * time.Second,
}

// reattach re-establishes a dropped output stream, resuming at the given ring offset.
//
// It gives up quietly if the session has meanwhile been destroyed (the user closed the
// tab) — reconnecting to something deliberately ended would resurrect a dead tab.
func (m *SessionManager) reattach(sess *Session, since int64, holed bool) {
	// A stream that lost bytes cannot be resumed from its own count — see Stream.Holed.
	// Take the whole ring again and throw the local copy away: a shorter, honest scrollback
	// beats a longer one with an invisible seam in it.
	resume := &since
	if holed {
		logger.Warn("stream lost bytes; taking a full replay instead of resuming",
			"id", sess.ID, "untrustedResumePoint", since)
		sess.Buffer.Reset()
		resume = nil
	}
	for _, wait := range reattachBackoff {
		time.Sleep(wait)

		if _, live := m.sessions.Load(sess.ID); !live {
			return // destroyed while we were away; nothing to come back to
		}
		if sess.GetStatus() == StatusExited {
			return
		}

		err := m.attach(sess, resume)
		if err == nil {
			logger.Info("re-attached session stream", "id", sess.ID, "resumedAt", since)
			return
		}
		if muxd.IsNotFound(err) || muxd.IsNoDaemon(err) {
			// Definitive news, not a transient failure, and the two forms say the same
			// thing. Either the daemon answered "no such session" (it was destroyed
			// elsewhere), or nothing is listening at all — and since a session exists
			// ONLY in daemon memory, no daemon means no session. Retrying cannot bring
			// either back, and leaving the tab looking alive would give the user a ghost
			// that swallows every keystroke.
			logger.Info("session is gone from the daemon; marking it ended",
				"id", sess.ID, "reason", err)
			m.markEnded(sess)
			return
		}
		logger.Debug("re-attach attempt failed", "id", sess.ID, "error", err)
		continue
	}
	logger.Warn("gave up re-attaching session stream; its output will stop updating until the page reconnects",
		"id", sess.ID)
}

// markEnded records that a session is over, without an exit code.
//
// Three sites move a session to Exited, and they are not interchangeable: the pump does
// it when the daemon SAYS the process exited (with the real code), Destroy does it because
// the user deleted the tab, and this one covers everything else — the session is gone from
// the daemon and no exit notification is ever coming. All three funnel through the same
// sync.Once so the transition happens exactly once, but "single place" was never true and
// claiming it here hid the fact that the three can disagree about the exit code.
func (m *SessionManager) markEnded(sess *Session) {
	sess.doneOnce.Do(func() {
		sess.mu.Lock()
		sess.Status = StatusExited
		sess.mu.Unlock()
		close(sess.done)
	})
}

// WatchDaemon keeps this server's view in step with the daemon for as long as ctx lives.
//
// It exists because the daemon is per-USER, not per-server: the same daemon backs the
// standalone build and the embedded one, so a tab created in one is a real session the
// other should see. Polling would make that "eventually, on the next refresh"; subscribing
// makes it immediate.
//
// Events are treated as a TRIGGER to re-read, never as the new state themselves. The
// daemon stays the single source of truth — an event only says "something changed", and
// the answer to "changed to what" is fetched. That keeps one authority rather than two
// that can disagree, which is the failure this whole design is organised against.
func (m *SessionManager) WatchDaemon(ctx context.Context) {
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if !m.watchOnce(ctx) {
				// Could not subscribe (daemon down or restarting). Wait and retry rather
				// than giving up: a server that silently stops syncing looks fine and
				// quietly drifts.
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}()
}

// watchOnce runs one subscription until it drops. It reports whether the subscription was
// established at all, so the caller knows whether to back off.
func (m *SessionManager) watchOnce(ctx context.Context) bool {
	// existingClient, not client(): watching must never START a daemon. An observer that
	// creates what it observes always finds something, and this particular observer retries
	// forever — so one that outlives its server would spawn daemons indefinitely. It did.
	client, err := m.existingClient()
	if err != nil {
		logger.Debug("watch: no daemon connection", "error", err)
		return false
	}
	events, cancel, err := client.Subscribe()
	if err != nil {
		logger.Debug("watch: subscribe failed", "error", err)
		return false
	}
	defer cancel()

	// Catch up the moment the subscription is established, and again on every
	// re-subscription. A subscription only carries what happens WHILE it is open; a
	// server that was disconnected — because the daemon died, or restarted, or because a
	// burst overflowed a 64-slot event buffer — has a hole that no future event will fill.
	// Without this the view drifts permanently and silently: tabs that were deleted
	// elsewhere stay, tabs created elsewhere never appear, and a session whose daemon died
	// while nothing was attached stays Running forever.
	if err := m.reconcile(); err != nil && !errors.Is(err, errDetached) {
		logger.Warn("watch: could not reconcile after subscribing", "error", err)
	}

	logger.Info("watching daemon for session lifecycle events", "socket", m.muxSocket)
	var lastSeq uint64
	for {
		select {
		case <-ctx.Done():
			return true
		case ev, ok := <-events:
			if !ok {
				return true // connection dropped; caller re-subscribes
			}
			missed := muxd.MissedEvents(lastSeq, ev.Seq)
			if ev.Seq > lastSeq {
				lastSeq = ev.Seq
			}
			m.applyEvent(ev)
			if missed {
				// Events are dropped, by design, at two bounded buffers — a subscriber must
				// never apply backpressure to a shell. Until the sequence existed, those
				// drops were indistinguishable from nothing happening, and the view simply
				// stayed wrong. Now a gap is an instruction: stop trusting the increments
				// and re-read the whole list, which is the only authoritative answer.
				logger.Warn("watch: missed daemon events; re-reading the session list",
					"upTo", ev.Seq)
				if err := m.reconcile(); err != nil && !errors.Is(err, errDetached) {
					logger.Warn("watch: could not reconcile after missing events", "error", err)
				}
			}
		}
	}
}

// applyEvent reacts to one daemon event.
func (m *SessionManager) applyEvent(ev muxd.Event) {
	switch ev.Kind {
	case muxd.EventCreated:
		if _, known := m.sessions.Load(ev.ID); known {
			return // our own create; already in view
		}
		// Someone else (another host, or a previous run) started this. Adopt it.
		if err := m.reconcile(); err != nil && !errors.Is(err, errDetached) {
			logger.Warn("watch: could not adopt a session created elsewhere", "id", ev.ID, "error", err)
		}
	case muxd.EventExited:
		if v, known := m.sessions.Load(ev.ID); known {
			sess := v.(*Session)
			sess.mu.Lock()
			sess.exitCode = ev.ExitCode
			sess.mu.Unlock()
			m.markEnded(sess)
		}
	case muxd.EventDestroyed:
		// Deleted — here or on another host. Distinct from exited: an exited session is
		// still a real tab with real scrollback, a destroyed one must disappear.
		if v, known := m.sessions.Load(ev.ID); known {
			sess := v.(*Session)
			m.sessions.Delete(ev.ID)
			m.markEnded(sess)
			terminalActive.Sub(1)
			logger.Info("session destroyed elsewhere; dropping it from this view", "id", ev.ID)
		}
	case muxd.EventMetaChanged:
		if v, known := m.sessions.Load(ev.ID); known {
			meta := decodeSessionMeta(ev.Meta)
			sess := v.(*Session)
			sess.mu.Lock()
			if meta.Name != "" {
				sess.Name = meta.Name
			}
			sess.Title = meta.Title
			sess.mu.Unlock()
		}
	default:
		// Forward compatibility is a two-way promise: the daemon may add event kinds, and
		// this must tolerate them. Logged rather than ignored so a newer daemon talking to
		// an older server leaves a trace instead of behaving mysteriously.
		logger.Debug("watch: ignoring unknown daemon event kind", "kind", ev.Kind, "id", ev.ID)
	}
}

// Restore rebuilds the session view from what the daemon holds. It is what makes a
// restart invisible: the tabs, their names, their scrollback and their running processes
// were never in this process to begin with, so coming back is a matter of asking.
//
// Sessions the daemon reports but this process has never seen are adopted, which is the
// whole point — they were created by a previous run of the server (or by another host
// entirely, since the daemon is per-user rather than per-server).
//
// It also RECONCILES in the other direction: sessions in the local view that the daemon
// no longer holds are removed. That is what makes cross-host visibility eventual rather
// than best-effort. Events can be missed — a subscriber that falls behind drops them, and
// a server that was disconnected while something happened never sees them at all — and
// without this pass a tab deleted elsewhere, or one whose daemon died while we were not
// looking, stayed on screen forever, swallowing keystrokes.
//
// It never spawns. A daemon that is not running holds no sessions, so there is nothing to
// restore and saying so is the correct answer; the next create will start one.
func (m *SessionManager) Restore() error {
	// Restoring is a DELIBERATE re-entry, so it cancels a previous detach. Only an explicit
	// caller gets to do that: the watcher's own reconcile must not, or a reconcile still in
	// flight when the server shuts down would clear the gate and let the pumps re-attach —
	// which is the shutdown leak, reopened by the thing added to close a different hole.
	m.muxMu.Lock()
	m.detached = false
	m.muxMu.Unlock()
	return m.reconcile()
}

// reconcile makes the local view match the daemon's, WITHOUT reviving a detached manager.
func (m *SessionManager) reconcile() error {
	client, err := m.existingClient()
	if err != nil {
		if muxd.IsNoDaemon(err) {
			return nil
		}
		return err
	}
	summaries, err := client.List()
	if err != nil {
		if muxd.IsNoDaemon(err) {
			return nil
		}
		return fmt.Errorf("terminal: list sessions from daemon: %w", err)
	}
	// Oldest first, so adoption order matches creation order and the tab strip's
	// position-based numbering stays stable across a restart.
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].CreatedAt.Equal(summaries[j].CreatedAt) {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].CreatedAt.Before(summaries[j].CreatedAt)
	})

	held := make(map[string]bool, len(summaries))
	for _, sum := range summaries {
		held[sum.ID] = true
	}

	restored := 0
	for _, sum := range summaries {
		if existing, exists := m.sessions.Load(sum.ID); exists {
			// Known, but not necessarily CURRENT. A reconcile runs precisely when events
			// were missed, and a lost meta-changed event is a name or title that will
			// otherwise stay wrong forever — the daemon's copy is the authority, so take it.
			refreshFromDaemon(existing.(*Session), sum)
			continue
		}
		meta := decodeSessionMeta(sum.Meta)
		createdAt := meta.CreatedAt
		if createdAt.IsZero() {
			createdAt = sum.CreatedAt
		}
		name := meta.Name
		if name == "" {
			name = createdAt.Format("0102-1504")
		}
		engine := meta.Engine
		if engine == "" {
			engine = "shell"
		}
		status := StatusRunning
		if !sum.Alive {
			status = StatusExited
		}
		sess := &Session{
			ID:         sum.ID,
			Name:       name,
			Title:      meta.Title,
			Engine:     engine,
			CWD:        meta.CWD,
			ShellPath:  meta.ShellPath,
			Buffer:     NewRingBuffer(m.bufferSize),
			Status:     status,
			CreatedAt:  createdAt,
			LastActive: time.Now(),
			viewers:    make(map[string]*viewer),
			done:       make(chan struct{}),
			mux:        client,
			shellPID:   sum.ShellPID,
			ptyCols:    int(sum.Cols),
			ptyRows:    int(sum.Rows),
		}
		if !sum.Alive {
			sess.exitCode = sum.ExitCode
			sess.doneOnce.Do(func() { close(sess.done) })
		}
		if detectTmuxByPID(sum.ShellPID) {
			sess.TmuxDetected = true
		}
		m.sessions.Store(sum.ID, sess)
		terminalActive.Add(1)

		if sum.Alive {
			// nil since: this process has no scrollback of its own, so ask for all of it.
			if err := m.attach(sess, nil); err != nil {
				logger.Warn("restore: attach failed", "id", sum.ID, "error", err)
			}
		}
		restored++
	}
	if restored > 0 {
		logger.Info("restored sessions from daemon", "count", restored, "socket", m.muxSocket)
	}
	m.forgetVanished(held)
	return nil
}

// refreshFromDaemon copies the authoritative fields of a summary onto a session we already
// hold. It exists because "events are only a trigger; the daemon is the authority" has to
// be true for sessions we have ALREADY adopted, not just for new ones.
func refreshFromDaemon(sess *Session, sum muxd.SessionSummary) {
	meta := decodeSessionMeta(sum.Meta)
	sess.mu.Lock()
	if meta.Name != "" {
		sess.Name = meta.Name
	}
	sess.Title = meta.Title
	if meta.CWD != "" {
		sess.CWD = meta.CWD
	}
	if sum.Cols > 0 && sum.Rows > 0 {
		sess.ptyCols, sess.ptyRows = int(sum.Cols), int(sum.Rows)
	}
	sess.mu.Unlock()
	if !sum.Alive {
		sess.doneOnce.Do(func() {
			sess.mu.Lock()
			sess.Status = StatusExited
			sess.exitCode = sum.ExitCode
			sess.mu.Unlock()
			close(sess.done)
		})
	}
}

// forgetVanished drops sessions the daemon no longer holds.
//
// Absence from the daemon's list is unambiguous: an EXITED session is still listed (with
// Alive false) until someone destroys it, so a session that is missing entirely was
// destroyed — the user deleted that tab, here or on another host. Keeping it would show a
// tab that cannot be typed into and cannot be closed.
//
// Done() is closed on the way out, because anything waiting on it would otherwise wait
// forever for a session that no longer exists anywhere.
func (m *SessionManager) forgetVanished(held map[string]bool) {
	m.sessions.Range(func(k, v any) bool {
		id := k.(string)
		if held[id] {
			return true
		}
		sess := v.(*Session)
		m.sessions.Delete(id)
		m.markEnded(sess)
		terminalActive.Sub(1)
		logger.Info("session is gone from the daemon; dropping it from this view", "id", id)
		return true
	})
}

// detachMux closes the daemon connection WITHOUT touching the sessions.
//
// This is what server shutdown does. The distinction between this and DestroyAll is the
// entire point of the daemon: before it existed, shutting the server down killed every
// terminal, and that was invisible until the moment a user recompiled and watched all
// their tabs die at once.
func (m *SessionManager) detachMux() error {
	m.muxMu.Lock()
	client := m.mux
	m.mux = nil
	// Set BEFORE the streams are closed: closing them wakes the pumps, and a pump that
	// wins that race would re-attach the manager we are in the middle of shutting down.
	m.detached = true
	m.muxMu.Unlock()

	m.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		sess.mu.Lock()
		stream := sess.stream
		sess.stream = nil
		sess.mu.Unlock()
		if stream != nil {
			_ = stream.Close()
		}
		return true
	})

	if client == nil {
		return nil
	}
	return client.Close()
}

// stopLocalDaemon shuts down an in-process fixture daemon, if this manager hosts one.
//
// It is SEPARATE from detachMux on purpose. Detaching means "let go of the connection,
// leave the sessions running" — for a real daemon and equally for a fixture one. Folding
// the fixture's teardown into it made CloseAll behave differently depending on how the
// daemon happened to be hosted, which is precisely the kind of hidden second meaning this
// whole change exists to remove. (It also broke a test that detached and then tried to
// reconnect, which is exactly what a restart does.)
//
// A real daemon is never touched here — that is the contract.
func (m *SessionManager) stopLocalDaemon() {
	m.muxMu.Lock()
	local, ln := m.localDaemon, m.localLn
	m.localDaemon, m.localLn = nil, nil
	m.muxMu.Unlock()
	if local != nil {
		local.Stop()
	}
	if ln != nil {
		_ = ln.Close()
	}
}

// detectTmuxByPID reports whether the shell with this pid is running inside tmux, by
// reading its environment. The server can still do this even though it no longer owns
// the process: /proc is readable for any process of the same user, which the daemon's
// children are.
func detectTmuxByPID(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		// Cannot read environ (permissions, non-Linux) — assume no tmux.
		return false
	}
	for _, entry := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(entry, "TMUX=") {
			return true
		}
	}
	return false
}
