package muxd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// DefaultIdleTimeout is how long the daemon lingers with NOTHING TO DO before it exits.
// Same shape as tmux: it is there to hold sessions, so once there are none it has no
// reason to keep running. It is not zero because restarting the HTTP server takes a
// moment, and a daemon that vanished in that gap would have to be respawned constantly.
//
// "Nothing to do" means no live sessions AND no connected clients — and the second half
// has a consequence worth stating plainly, because reading only the first half gives the
// wrong model: a RUNNING SERVER PINS THE DAEMON OPEN. Its control connection and its
// event subscription both count, so a server with zero tabs still keeps a daemon
// resident indefinitely. That is intended (someone is attached, so someone wants it), but
// it means this timeout is really about the unattended case: the last server exited, and
// the daemon is now holding nothing for nobody.
const DefaultIdleTimeout = 30 * time.Second

// Daemon owns every session and serves the unix socket.
type Daemon struct {
	mu       sync.Mutex
	sessions map[string]*Session
	subs     map[int]*subscriber
	nextSub  int
	conns    int // in-flight client connections; a daemon with clients is not idle
	lastIdle time.Time

	ringCap     int
	idleTimeout time.Duration
	factory     PTYFactory
	startedAt   time.Time
	stop        chan struct{}
	stopOnce    sync.Once
}

// NewDaemon builds a daemon. ringCap of 0 means DefaultBufferCapacity; idleTimeout of
// 0 means DefaultIdleTimeout, and a negative value disables idle exit (tests).
func NewDaemon(ringCap int, idleTimeout time.Duration) *Daemon {
	return NewDaemonWith(ringCap, idleTimeout, nil)
}

// NewDaemonWith builds a daemon with an explicit PTY factory. A nil factory means real
// PTYs; tests pass a pipe-backed one so they can drive terminal output byte by byte.
func NewDaemonWith(ringCap int, idleTimeout time.Duration, factory PTYFactory) *Daemon {
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	return &Daemon{
		factory:     factory,
		sessions:    map[string]*Session{},
		subs:        map[int]*subscriber{},
		ringCap:     ringCap,
		idleTimeout: idleTimeout,
		lastIdle:    time.Now(),
		startedAt:   time.Now(),
		stop:        make(chan struct{}),
	}
}

// Serve accepts connections until ctx is cancelled, Stop is called, or the daemon has
// been idle past its timeout.
func (d *Daemon) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		select {
		case <-ctx.Done():
		case <-d.stop:
		}
		_ = ln.Close()
	}()
	if d.idleTimeout > 0 {
		go d.watchIdle()
	}

	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-d.stop:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go d.handleConn(c)
	}
}

// Stop shuts the accept loop down. It deliberately does NOT kill sessions: stopping the
// daemon is not the same as destroying what it holds, and conflating the two is the
// exact bug this whole daemon exists to fix (one level up, in the server).
func (d *Daemon) Stop() {
	d.stopOnce.Do(func() { close(d.stop) })
}

// DestroyAll terminates every session. This is the explicit teardown used by tests and
// by a deliberate "kill everything" action — never by a routine shutdown.
func (d *Daemon) DestroyAll() {
	d.mu.Lock()
	all := make([]*Session, 0, len(d.sessions))
	for _, s := range d.sessions {
		all = append(all, s)
	}
	d.sessions = map[string]*Session{}
	d.mu.Unlock()
	for _, s := range all {
		s.Destroy()
	}
}

// identity is what this daemon tells every client about itself in the handshake.
//
// The session count is the load-bearing field: it is what turns "an incompatible daemon
// is running" into "restarting it will end your 7 shells", and a number is the only form
// of that warning a user can weigh. It counts LIVE sessions only — an exited session
// costs nothing to lose, and including it would overstate the price.
func (d *Daemon) identity() Hello {
	d.mu.Lock()
	live := 0
	for _, s := range d.sessions {
		if s.Alive() {
			live++
		}
	}
	d.mu.Unlock()
	return Hello{
		PID:         os.Getpid(),
		Sessions:    live,
		StartedUnix: d.startedAt.Unix(),
		// What this build can do, which the version number deliberately does not say.
		Features: DaemonFeatures,
	}
}

// watchIdle exits the daemon once it has held no live sessions and no client
// connections for idleTimeout.
func (d *Daemon) watchIdle() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-tick.C:
			d.mu.Lock()
			busy := d.conns > 0
			for _, s := range d.sessions {
				if s.Alive() {
					busy = true
					break
				}
			}
			if busy {
				d.lastIdle = time.Now()
				d.mu.Unlock()
				continue
			}
			idleFor := time.Since(d.lastIdle)
			d.mu.Unlock()
			if idleFor >= d.idleTimeout {
				d.Stop()
				return
			}
		}
	}
}

// SessionCount reports how many sessions the daemon holds (alive or exited).
func (d *Daemon) SessionCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sessions)
}

// Get returns a session by id.
func (d *Daemon) Get(id string) (*Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.sessions[id]
	return s, ok
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("s%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// create spawns a session and registers it, returning its id and shell pid.
func (d *Daemon) create(req CreateReq) (string, int, error) {
	id := newSessionID()
	s, err := SpawnWith(id, SpawnOptions{
		Argv: req.Argv,
		Cwd:  req.Cwd,
		Env:  req.Env,
		Cols: req.Cols,
		Rows: req.Rows,
		Meta: req.Meta,
		Cap:  d.ringCap,
	}, d.factory, d.onSessionExit)
	if err != nil {
		return "", 0, err
	}
	d.mu.Lock()
	d.sessions[id] = s
	d.lastIdle = time.Now()
	d.mu.Unlock()
	d.broadcast(Event{Kind: EventCreated, ID: id, Meta: req.Meta})
	return id, s.ShellPID(), nil
}

func (d *Daemon) onSessionExit(id string, code int) {
	d.mu.Lock()
	d.lastIdle = time.Now()
	d.mu.Unlock()
	d.broadcast(Event{Kind: EventExited, ID: id, ExitCode: code})
}

// subscriber is one event stream plus the counter that makes its losses detectable.
//
// The counter is PER SUBSCRIBER, not global, because the loss is per subscriber: each has
// its own bounded channel and each falls behind independently. A global counter would tell
// a receiver that the daemon emitted N events, not which of them reached it.
type subscriber struct {
	ch  chan Event
	seq uint64
}

func (d *Daemon) broadcast(ev Event) {
	// Stamp under the lock and deliver under it too. The send is non-blocking, so holding
	// the lock costs nothing here, and it keeps the counter and the channel in step: a
	// numbering assigned outside the lock could be reordered against another broadcast and
	// arrive out of sequence, which a receiver would read as a gap that never happened.
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, sub := range d.subs {
		sub.seq++
		stamped := ev
		stamped.Seq = sub.seq
		// Never block on a slow subscriber — but discard the OLDEST rather than the
		// newest, so the highest sequence number always lands and the gap is visible the
		// moment the receiver drains. See pushNewest.
		pushNewest(sub.ch, stamped)
	}
}

func (d *Daemon) subscribe() (<-chan Event, func()) {
	d.mu.Lock()
	id := d.nextSub
	d.nextSub++
	sub := &subscriber{ch: make(chan Event, 64)}
	d.subs[id] = sub
	d.mu.Unlock()
	return sub.ch, func() {
		d.mu.Lock()
		if existing, ok := d.subs[id]; ok {
			delete(d.subs, id)
			close(existing.ch)
		}
		d.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Connection handling.
// ---------------------------------------------------------------------------

// waitDone blocks until the forwarder signals it stopped, with a ceiling.
//
// The ceiling is not defensive hand-waving: the forwarder can be parked in a socket write
// to a peer that has stopped reading, and this runs on the goroutine that would otherwise
// read the peer's next frame. Waiting forever would deadlock the connection to prevent a
// misattributed frame on it — trading a certain hang for a rare one.
func waitDone(done chan struct{}) {
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func (d *Daemon) handleConn(c net.Conn) {
	d.mu.Lock()
	d.conns++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.conns--
		d.lastIdle = time.Now()
		d.mu.Unlock()
		_ = c.Close()
	}()

	if err := ServerHandshake(c, d.identity()); err != nil {
		return // the client has been told who we are; it decides whether to continue
	}

	// A write mutex is required because an attached stream pumps output from another
	// goroutine while this loop may still answer control frames.
	var wmu sync.Mutex
	write := func(t MsgType, v any) error {
		wmu.Lock()
		defer wmu.Unlock()
		return WriteJSON(c, t, v)
	}
	writeRaw := func(t MsgType, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return WriteFrame(c, t, b)
	}

	var (
		attached  *Session
		detachSub func()
		// attachedSub is THIS connection's attachment record. A resize arriving on an
		// attached connection means "the window I am showing this in changed", so it has to
		// be routed to this attachment rather than to the session as a whole.
		attachedSub *subscription
		// attachedDone is closed by the current forwarder when it stops. Attaching again on
		// this connection waits for it, so two forwarders can never share one socket.
		attachedDone chan struct{}
	)
	defer func() {
		if detachSub != nil {
			detachSub()
		}
	}()

	fail := func(code, msg string) {
		_ = write(MsgError, ErrorPayload{Code: code, Msg: msg})
	}

	for {
		t, payload, err := ReadFrame(c)
		if err != nil {
			return
		}

		switch t {
		case MsgCreate:
			var req CreateReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			id, pid, err := d.create(req)
			if err != nil {
				fail(ErrCodeSpawnFail, err.Error())
				continue
			}
			_ = write(MsgCreateAck, CreateAck{ID: id, ShellPID: pid})

		case MsgList:
			d.mu.Lock()
			out := make([]SessionSummary, 0, len(d.sessions))
			for _, s := range d.sessions {
				out = append(out, s.Summary())
			}
			d.mu.Unlock()
			_ = write(MsgListAck, ListAck{Sessions: out})

		case MsgAttach:
			var req AttachReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			s, ok := d.Get(req.ID)
			if !ok {
				fail(ErrCodeNotFound, "no such session: "+req.ID)
				continue
			}
			// Re-attaching on a connection that is already attached must not leave the OLD
			// forwarder running: it writes to this same socket, so a buffered chunk of the
			// previous session — or its MsgExited, which carries no session id — would
			// surface after the new AttachAck and be attributed to the new session. Cancel
			// closes its channel; waitDone is what makes "it has stopped" true rather than
			// merely requested.
			if detachSub != nil {
				detachSub()
				detachSub = nil
			}
			waitDone(attachedDone)
			replay, offset, grid, sub, cancel := s.Subscribe(req.Since, gridFromWire(req.Cols, req.Rows))
			attached, detachSub = s, cancel
			attachedSub = sub
			attachedDone = make(chan struct{})
			ackCols, ackRows := grid.Wire()
			_ = write(MsgAttachAck, AttachAck{
				Cols: ackCols, Rows: ackRows, Offset: offset, Alive: s.Alive(),
				ReplayBytes: int64(len(replay)),
			})
			if len(replay) > 0 {
				_ = writeRaw(MsgOutput, replay)
			}
			if sub != nil {
				go func(sub *subscription, sess *Session, done chan struct{}) {
					defer close(done)
					// ONE ordered loop. Output, resizes and gap notices arrive on a single
					// channel in the order the session produced them, and they go onto the
					// wire in that same order — which is the only way a client can know
					// which grid a given byte was drawn for.
					for f := range sub.ch {
						switch {
						case f.Gap:
							if err := write(MsgGap, struct{}{}); err != nil {
								return
							}
						case f.Resize != nil:
							c, r := f.Resize.Wire()
							if err := write(MsgResized, ResizedPayload{Cols: c, Rows: r}); err != nil {
								return
							}
						default:
							if err := writeRaw(MsgOutput, f.Data); err != nil {
								return
							}
						}
					}
					// The channel closes for TWO reasons — the process exited, or this
					// subscription was cancelled (detach, or a re-attach on the same
					// connection) — and the channel alone cannot tell them apart. Ask the
					// session. Announcing an exit on a detach would report a live session
					// dead, with the -1 sentinel as its code, on a payload that carries no
					// session id for the client to sanity-check it against.
					if !sess.Alive() {
						_ = write(MsgExited, ExitedPayload{ExitCode: sess.Summary().ExitCode})
					}
				}(sub, s, attachedDone)
			} else {
				_ = write(MsgExited, ExitedPayload{ExitCode: s.Summary().ExitCode})
			}

		case MsgInput:
			// Bare data-plane frame: applies to whatever this connection is attached to.
			if attached == nil {
				fail(ErrCodeNotFound, "input before attach")
				continue
			}
			if err := attached.Write(payload); err != nil {
				fail(ErrCodeInternal, err.Error())
			}

		case MsgInputTo:
			var req InputReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			if err := d.controlInput(req); err != nil {
				fail(ErrCodeNotFound, err.Error())
				continue
			}
			_ = write(MsgOK, struct{}{})

		case MsgDetach:
			if detachSub != nil {
				detachSub()
				detachSub = nil
			}
			attached = nil
			_ = write(MsgOK, struct{}{})

		case MsgResize:
			var req ResizeReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			// On an ATTACHED connection this is "my window is now this big", not "set the
			// session to this size". The difference is the entire point of per-attachment
			// geometry: the session fits everyone watching, and no client can reflow
			// another's terminal by resizing its own.
			if attached != nil && attachedSub != nil && (req.ID == "" || req.ID == attached.ID) {
				// SetAttachSize also ANSWERS this client when it did not get what it asked
				// for — on its subscription, so the answer stays in order with its output,
				// and under the session lock, so it cannot race the channel's close.
				attached.SetAttachSize(attachedSub.id, gridFromWire(req.Cols, req.Rows))
				_ = write(MsgOK, struct{}{})
				continue
			}
			s, ok := d.Get(req.ID)
			if !ok {
				fail(ErrCodeNotFound, "no such session: "+req.ID)
				continue
			}
			if err := s.Resize(req.Cols, req.Rows); err != nil {
				fail(ErrCodeInternal, err.Error())
				continue
			}
			_ = write(MsgOK, struct{}{})

		case MsgKill:
			var req KillReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			s, ok := d.Get(req.ID)
			if !ok {
				fail(ErrCodeNotFound, "no such session: "+req.ID)
				continue
			}
			if req.Sig == 0 {
				// Refused, not honoured. kill(pid, 0) is the Unix liveness probe and used
				// to mean "destroy" here — the most destructive operation spelled like the
				// most harmless one. Destroy is MsgDestroy.
				fail(ErrCodeBadFrame, "signal 0 is a liveness probe, not a destroy; use MsgDestroy")
				continue
			}
			if req.Foreground {
				if err := s.KillForeground(syscall.Signal(req.Sig)); err != nil {
					fail(ErrCodeInternal, err.Error())
					continue
				}
			} else if err := s.Signal(syscall.Signal(req.Sig)); err != nil {
				fail(ErrCodeInternal, err.Error())
				continue
			}
			_ = write(MsgOK, struct{}{})

		case MsgDestroy:
			var req SimpleReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			s, ok := d.Get(req.ID)
			if !ok {
				fail(ErrCodeNotFound, "no such session: "+req.ID)
				continue
			}
			d.mu.Lock()
			delete(d.sessions, req.ID)
			d.mu.Unlock()
			s.Destroy()
			// Tell every other host the tab is GONE, not merely finished. Without this,
			// destroy propagated as nothing at all when the session had already exited,
			// and as "exited" when it had not — either way the other host kept a tab the
			// user deleted, permanently.
			d.broadcast(Event{Kind: EventDestroyed, ID: req.ID})
			_ = write(MsgOK, struct{}{})

		case MsgSetMeta:
			var req SetMetaReq
			if err := json.Unmarshal(payload, &req); err != nil {
				fail(ErrCodeBadFrame, err.Error())
				continue
			}
			s, ok := d.Get(req.ID)
			if !ok {
				fail(ErrCodeNotFound, "no such session: "+req.ID)
				continue
			}
			s.SetMeta(req.Meta)
			d.broadcast(Event{Kind: EventMetaChanged, ID: req.ID, Meta: req.Meta})
			_ = write(MsgOK, struct{}{})

		case MsgSubscribe:
			evs, cancelEvents := d.subscribe()
			defer cancelEvents()
			go func() {
				for ev := range evs {
					if err := write(MsgEvent, ev); err != nil {
						return
					}
				}
			}()
			_ = write(MsgOK, struct{}{})

		default:
			fail(ErrCodeBadFrame, fmt.Sprintf("unexpected frame %d", t))
		}
	}
}

// controlInput handles input addressed by id on a non-attached connection (the HTTP
// fallback path posts keystrokes without attaching).
func (d *Daemon) controlInput(req InputReq) error {
	s, ok := d.Get(req.ID)
	if !ok {
		return fmt.Errorf("muxd: no such session: %s", req.ID)
	}
	return s.Write(req.Data)
}
