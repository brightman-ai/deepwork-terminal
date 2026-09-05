package muxd

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Session is one PTY the daemon owns. It outlives every client connection — that is
// the entire point of this program.
type Session struct {
	ID        string
	CreatedAt time.Time

	// history is this session's scrollback: the lines that have left the top of the screen, with
	// their colour. Nil when disabled.
	//
	// Set once at spawn and never reassigned, so it needs no lock of its own to READ — and it has
	// its own lock INSIDE for its contents, deliberately not this one: a client paging through
	// history must not be able to hold the lock the PTY's read loop takes. See History.
	history *History

	mu   sync.Mutex
	pty  *os.File
	cmd  *exec.Cmd
	ring *RingBuffer
	meta []byte // opaque; the daemon never looks inside
	// size is what the PTY is currently running at — the grid its scrollback was written
	// in, and therefore the grid a replay must be rendered onto.
	size     Grid
	alive    bool
	exitCode int
	shellPID int
	// ptyClosed says the master descriptor has been closed. It is NOT redundant with
	// `alive`: a raw-fd operation is only safe while the fd number still belongs to this
	// PTY, and that stops being true at the moment of close — not at the moment the
	// process is reaped. See closePTY.
	ptyClosed bool

	// subs is the set of ATTACHMENTS, and each one carries the size of the terminal it is
	// being displayed in. That is what makes geometry ownable: the session's size is no
	// longer whatever the last caller happened to ask for, it is a function of who is
	// actually looking. See effectiveSizeLocked.
	subs      map[int]*subscription
	nextSubID int

	// manual is the size set by a control-plane resize — a caller that is not attached and
	// therefore has no view to fit. It is the FALLBACK, used only while no attachment
	// declares a size, because a claim from something that is watching always beats a
	// claim from something that is not.
	manual Grid

	onExit func(id string, code int)

	// waitOnce guards the single legitimate call to cmd.Wait.
	//
	// os/exec.Cmd.Wait is NOT safe for concurrent use, and two callers legitimately want
	// to reap: readLoop (the PTY hit EOF on its own) and Destroy (we killed it
	// deliberately). Racing them corrupts ProcessState and double-closes descriptors.
	//
	// sync.Once doubles as the barrier that makes this correct rather than merely
	// deduplicated: Do guarantees no call returns until the single call has returned, so
	// whichever caller loses the race still blocks until Wait is done and may safely read
	// ProcessState afterwards.
	waitOnce sync.Once
}

// SpawnOptions describes a PTY to create.
type SpawnOptions struct {
	Argv []string
	Cwd  string
	Env  []string // when nil, the daemon's own environment is used as the base
	Cols uint16
	Rows uint16
	Meta []byte
	Cap  int // ring capacity; 0 means DefaultBufferCapacity
	// HistoryLines is how many scrolled-off lines to keep. 0 means DefaultHistoryLines;
	// NEGATIVE disables scrollback for this session entirely, which is what tests that only care
	// about the byte stream use so they do not pay for a screen model they never read.
	HistoryLines int
}

// DefaultCols/DefaultRows are the geometry a PTY is born with, before a client attaches
// and resizes it.
//
// They are EXPORTED, and the server uses these exact constants rather than its own, for a
// specific reason: a session that never resized still has to be replayed onto a grid of
// its actual size, and a second hardcoded guess elsewhere is exactly how the overview's
// screen replay drifted before (rows past the guessed height were clamped onto the last
// row and overwrote it). One definition, one number. AttachAck then carries the real
// per-session size so nothing downstream has to guess at all.
const (
	DefaultCols uint16 = 220
	DefaultRows uint16 = 50
)

// Internal aliases keep this file readable.
const (
	defaultCols = DefaultCols
	defaultRows = DefaultRows
)

// PTYFactory creates the PTY behind a session. Production uses realPTY; tests inject a
// pipe-backed one so they can drive terminal output deterministically.
//
// It lives here, on the daemon side, because the daemon is the ONLY thing that makes
// PTYs now. There is no second code path back in the server — an in-process daemon in a
// test is the same code as a spawned one, just hosted differently.
type PTYFactory func(opts SpawnOptions) (*os.File, *exec.Cmd, error)

// Spawn starts a PTY and begins pumping its output into the ring.
func Spawn(id string, opts SpawnOptions, onExit func(string, int)) (*Session, error) {
	return SpawnWith(id, opts, RealPTY, onExit)
}

// SpawnWith is Spawn with an explicit factory.
func SpawnWith(id string, opts SpawnOptions, factory PTYFactory, onExit func(string, int)) (*Session, error) {
	if factory == nil {
		factory = RealPTY
	}
	if len(opts.Argv) == 0 || opts.Argv[0] == "" {
		return nil, fmt.Errorf("muxd: spawn needs a command")
	}
	if opts.Cols == 0 {
		opts.Cols = defaultCols
	}
	if opts.Rows == 0 {
		opts.Rows = defaultRows
	}

	ptmx, cmd, err := factory(opts)
	if err != nil {
		return nil, fmt.Errorf("muxd: spawn pty: %w", err)
	}

	s := &Session{
		ID:        id,
		CreatedAt: time.Now(),
		pty:       ptmx,
		cmd:       cmd,
		ring:      NewRingBuffer(opts.Cap),
		meta:      append([]byte(nil), opts.Meta...),
		size:      gridFromWire(opts.Cols, opts.Rows),
		alive:     true,
		exitCode:  -1,
		subs:      map[int]*subscription{},
		onExit:    onExit,
	}
	if opts.HistoryLines >= 0 {
		s.history = NewHistory(s.size, opts.HistoryLines)
	}
	// cmd is nil for pipe-backed factories (there is no child process to report a pid
	// for), so both hops must be checked — not just the inner one.
	if cmd != nil && cmd.Process != nil {
		s.shellPID = cmd.Process.Pid
	}
	go s.readLoop()
	return s, nil
}

// RealPTY is the production factory: a genuine PTY running the requested command.
func RealPTY(opts SpawnOptions) (*os.File, *exec.Cmd, error) {
	newCmd := func() *exec.Cmd {
		c := exec.Command(opts.Argv[0], opts.Argv[1:]...)
		if opts.Cwd != "" {
			c.Dir = opts.Cwd
		}
		// cmd.Environ() rather than os.Environ(): Go only keeps PWD in sync with Cmd.Dir
		// while Env is nil, so assigning Env explicitly turns that sync off — and the
		// child's $PWD would then point at a directory it is not in, quietly misleading
		// the prompt, the startup scripts, and every tool that reads it. Environ() is
		// "the environment with Dir already applied", which is what PTYEnv must scrub.
		base := opts.Env
		if base == nil {
			base = c.Environ()
		}
		c.Env = PTYEnv(base)
		return c
	}
	cmd := newCmd()
	// Setpgid isolates the child's signal group: a SIGINT aimed at the daemon must not
	// travel to every hosted shell. (It does NOT make the child survive its parent —
	// nothing about process groups does. Surviving is what this daemon is for.)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		// Retry without Setpgid: some restricted environments (containers, seccomp) deny
		// it, and a terminal that runs beats one that refuses to start.
		cmd = newCmd()
		ptmx, err = pty.StartWithSize(cmd, &pty.Winsize{Cols: opts.Cols, Rows: opts.Rows})
		if err != nil {
			return nil, nil, err
		}
	}
	return ptmx, cmd, nil
}

// readLoop pumps PTY output into the ring and out to every attached client.
func (s *Session) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.mu.Lock()
			_, _ = s.ring.Write(chunk)
			for _, sub := range s.subs {
				// Never block the PTY on a slow client: a stalled websocket must not
				// apply backpressure all the way to the shell. A client that cannot
				// keep up drops bytes from its live stream and can re-attach to get
				// the ring's contents again.
				//
				// admitOutput, not a bare send: the top of the queue belongs to control
				// frames, which have nothing to re-send them. See controlReserve.
				if admitOutput(sub.ch) {
					select {
					case sub.ch <- subFrame{Data: chunk}:
						continue
					default:
					}
				}
				// Too far behind to hold this chunk. Dropping is correct — the shell
				// must never wait on a websocket — but the drop has to be ANNOUNCED, or
				// the client's byte count quietly diverges from the ring and its next
				// resume asks for the wrong offset. The notice takes priority over
				// queued output: it is the one frame that must not itself be lost.
				pushControl(sub.ch, subFrame{Gap: true})
			}
			// Scrollback last, and deliberately so.
			//
			// AFTER the fan-out, because parsing the chunk into a screen model is the only
			// non-trivial work in this loop and no byte may wait behind it on its way to a
			// terminal. INSIDE the lock, because applySizeLocked hands the model its resizes from
			// under this same mutex, and a resize that overtakes the output it was supposed to
			// follow lays the next lines out on the wrong width.
			//
			// It cannot fail loudly: History.Write recovers from anything the model or the store
			// does and switches that session's history off. By this point the bytes are already in
			// the ring and already queued to every client, so a broken historian costs history and
			// nothing else. (History.Write takes its own lock; nothing ever takes this one while
			// holding that one, so the nesting is one-directional.)
			s.history.Write(chunk)
			s.mu.Unlock()
		}
		if err != nil {
			s.finish()
			return
		}
	}
}

// finish records the exit status and notifies subscribers.
func (s *Session) finish() {
	code := 0
	if s.cmd != nil {
		s.waitOnce.Do(func() { _ = s.cmd.Wait() })
		if s.cmd.ProcessState != nil {
			code = s.cmd.ProcessState.ExitCode()
		}
	}
	s.mu.Lock()
	if !s.alive {
		s.mu.Unlock()
		return
	}
	s.alive = false
	s.exitCode = code
	for id, sub := range s.subs {
		close(sub.ch)
		delete(s.subs, id)
	}
	onExit := s.onExit
	s.mu.Unlock()
	if onExit != nil {
		onExit(s.ID, code)
	}
}

// Summary renders the session for List.
func (s *Session) Summary() SessionSummary {
	// History's stats come from BEFORE s.mu is taken. Reading them under it would nest this
	// mutex around the historian's, and the only reason to allow that nesting anywhere is the
	// read loop, which has no choice. Nowhere else needs to add to that.
	hist := s.history.Stats()
	s.mu.Lock()
	defer s.mu.Unlock()
	viewers := 0
	for _, sub := range s.subs {
		if !sub.size.Zero() {
			viewers++
		}
	}
	return SessionSummary{
		Attached:  len(s.subs),
		Viewers:   viewers,
		ID:        s.ID,
		Alive:     s.alive,
		ExitCode:  s.exitCode,
		Cols:      uint16(s.size.Cols),
		Rows:      uint16(s.size.Rows),
		Meta:      append([]byte(nil), s.meta...),
		ShellPID:  s.shellPID,
		CreatedAt: s.CreatedAt,

		HistoryEnabled: s.history != nil,
		HistoryLines:   hist.Total - hist.Base,
		HistoryBytes:   hist.Bytes,
		HistoryBroken:  hist.Broken,
	}
}

// Write sends bytes to the PTY.
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	f, alive := s.pty, s.alive
	s.mu.Unlock()
	if !alive || f == nil {
		return fmt.Errorf("muxd: session %s is not running", s.ID)
	}
	_, err := f.Write(p)
	return err
}

// Resize sets the MANUAL size — the fallback used while nothing that is attached has
// declared a size of its own.
//
// It is deliberately not "the size", any more. A caller on the control connection is by
// definition not looking at this session, so it cannot know what will fit; letting it
// overrule the windows that ARE displaying the session is how two clients ended up
// reflowing each other. Attached clients declare their own size (see SetAttachSize) and
// the smallest of those wins.
func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return fmt.Errorf("muxd: resize needs non-zero cols/rows")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.alive {
		// A dead session's recorded geometry is the shape its scrollback was WRITTEN in;
		// it is what the replay must be rendered onto. Letting a late resize overwrite it
		// would re-open the drift this field exists to close: the content is fixed, so
		// changing the grid under it produces a different screen, not a resized one.
		return nil
	}
	s.manual = gridFromWire(cols, rows)
	s.applySizeLocked()
	return nil
}

// Signal sends sig to the session's process group, falling back to the process itself
// when it does not lead its own group.
func (s *Session) Signal(sig syscall.Signal) error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("muxd: session %s has no process", s.ID)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid == cmd.Process.Pid {
		return syscall.Kill(-pgid, sig)
	}
	return cmd.Process.Signal(sig)
}

// KillForeground sends sig to the PTY's current foreground process group.
//
// TIOCGPGRP on the master reads exactly the fact the kernel consults to route Ctrl+C, so
// this is the same target the polite signal would have reached — just with a signal the
// program cannot ignore. When the foreground process IS the shell (nothing interactive
// running), the shell's group is killed and the tab disconnects; a "kill what's in front"
// with nothing in front has nothing else meaningful to do, and pretending to succeed
// while leaving the user stuck would be worse.
func (s *Session) KillForeground(sig syscall.Signal) error {
	// The ioctl happens UNDER the lock, unlike the rest of this file's descriptor use.
	// TIOCGPGRP goes through File.Fd(), which escapes os.File's use-after-close protection,
	// and the number it returns is then used as a SIGNAL TARGET. Racing a close here does
	// not risk a failed resize; it risks killing a process group read off a descriptor that
	// now belongs to something else. An ioctl does not block, so the lock costs nothing.
	s.mu.Lock()
	defer s.mu.Unlock()
	f, alive := s.pty, s.alive
	if !alive || f == nil || s.ptyClosed {
		return fmt.Errorf("muxd: session %s has no active pty", s.ID)
	}
	pgid, err := unix.IoctlGetInt(int(f.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return fmt.Errorf("muxd: session %s TIOCGPGRP: %w", s.ID, err)
	}
	if err := syscall.Kill(-pgid, sig); err != nil {
		return fmt.Errorf("muxd: session %s kill foreground pgid %d: %w", s.ID, pgid, err)
	}
	return nil
}

// Destroy terminates the session's process and closes its PTY. This is the EXPLICIT
// destroy path — used when a user deletes a session, and by tests to clean up after
// themselves. Shutting down a server must NOT call it; that path detaches instead,
// which is the whole reason this daemon exists.
func (s *Session) Destroy() {
	s.mu.Lock()
	cmd, alive := s.cmd, s.alive
	s.mu.Unlock()
	if alive && cmd != nil && cmd.Process != nil {
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil && pgid == cmd.Process.Pid {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = cmd.Process.Kill()
		}
	}
	s.closePTY()
	if !alive {
		return
	}
	s.finish()
}

// closePTY closes the master descriptor UNDER the lock that guards every raw-fd operation
// on it.
//
// Reading the descriptor under the lock and closing it outside — which is what this used to
// do — orders the read and nothing else. os.File normally makes that harmless: Read and
// Write go through a refcounted poll.FD that waits for in-flight operations and then returns
// ErrClosed. But File.Fd() is the escape hatch out of that protection, and both raw-fd users
// here go through it — pty.Setsize for the window size, and the TIOCGPGRP that finds the
// foreground process group. Once the fd number is loose, closing it concurrently means the
// ioctl can land on whatever the kernel handed that number to next: a resize aimed at some
// other file, or — far worse — a SIGKILL aimed at a process group read off it.
//
// The race detector found this on the resize path (Setsize's Fd() against Destroy's Close),
// which per-attachment geometry made routine rather than rare: resizes now happen on every
// viewer arrival, departure and window change instead of once in a while.
//
// Two things guard it, and the sabotage runs showed EITHER ONE alone is enough today: the
// ptyClosed flag, and closing under the lock. That is deliberate rather than redundant. The
// flag suffices only while every raw-fd user runs under this lock — a property no compiler
// checks and which the next one added will not know about — so the close stays inside the
// lock as the guard that does not depend on remembering.
func (s *Session) closePTY() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pty != nil && !s.ptyClosed {
		s.ptyClosed = true
		_ = s.pty.Close()
	}
}

// SetMeta replaces the opaque blob. The daemon stores and returns it verbatim; it has
// no idea what any of it means, which is precisely what lets the server change the
// shape of that data without an upgrade that would cost live sessions.
func (s *Session) SetMeta(b []byte) {
	s.mu.Lock()
	s.meta = append([]byte(nil), b...)
	s.mu.Unlock()
}

// Alive reports whether the process is still running.
func (s *Session) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

// ShellPID returns the PTY child's pid. The server can no longer see this process, so
// it must come across the wire rather than be re-derived by walking the process tree.
func (s *Session) ShellPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shellPID
}

// History is this session's scrollback, or nil if it was spawned without one.
//
// No lock: the field is written once during Spawn, before the session is reachable by anyone else,
// and never reassigned. Taking s.mu here would be worse than pointless — it would put the PTY's
// mutex on the path of every history read, which is the exact coupling History's own lock exists
// to avoid. The returned value is safe for concurrent use and tolerates a nil receiver, so callers
// do not have to branch.
func (s *Session) History() *History { return s.history }

// Subscribe registers a live-output channel and returns the replay bytes that precede
// it, the ring offset those bytes end at, plus the session geometry.
//
// Replay and subscription happen under one lock on purpose: taking them separately
// would leave a window where output written in between is in neither, and the client
// would silently lose a chunk of its terminal.
func (s *Session) Subscribe(since *int64, want Grid) (replay []byte, offset int64, grid Grid, sub *subscription, cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := int64(s.ring.Seq())
	if since != nil && *since >= 0 && *since <= seq {
		if want := seq - *since; want <= int64(s.ring.Len()) {
			replay = s.ring.ReadTail(int(want))
		} else {
			replay = s.ring.Read()
		}
	} else {
		replay = s.ring.Read()
	}

	if !s.alive {
		return replay, seq, s.size, nil, func() {}
	}

	id := s.nextSubID
	s.nextSubID++
	sub = &subscription{
		id:   id,
		ch:   make(chan subFrame, 256),
		size: want,
	}
	s.subs[id] = sub
	// A new viewer can shrink the session immediately — that is smallest-wins working, and
	// it must happen before the replay geometry is reported or the client would render the
	// scrollback onto a grid the session is about to leave.
	s.applySizeLocked()
	return replay, seq, s.size, sub, func() {
		s.mu.Lock()
		if existing, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(existing.ch)
			// Losing a viewer can GROW the session back: the constraint it imposed is gone.
			s.applySizeLocked()
		}
		s.mu.Unlock()
	}
}

// subscription is one attached client: its output channel, its out-of-band resize
// notifications, and the size of the terminal it is displayed in.
//
// resized is buffered to one and latest-wins. A resize notification is not news that
// accumulates — only the current size matters — so an old one waiting in a queue is
// worse than useless, it is wrong.
// subFrame is one thing that happened on an attachment, carried in the order it happened.
//
// Output, resizes and gap notices share ONE channel on purpose. They used to have three,
// and three channels cannot express "this redraw was produced at the new size": the reader
// selects among whatever is ready, so a resize could be delivered before the bytes drawn
// under the old grid or after the bytes drawn under the new one. For a TUI — which paints
// by absolute cursor address — that is not a cosmetic ordering detail, it decides whether
// the picture is right.
type subFrame struct {
	Data   []byte // PTY output
	Resize *Grid  // the session's grid changed, as of this point in the stream
	Gap    bool   // output was dropped just before this point
}

type subscription struct {
	id int
	ch chan subFrame
	// size is the window this attachment is displayed in; the zero Grid is an OBSERVER,
	// watching without imposing a constraint.
	size Grid
}

// controlReserve is how many slots at the top of a subscription's queue are held back for
// control frames.
//
// It exists because the obvious policy — "when full, discard the oldest frame" — can
// discard a control frame in favour of a later one, and that reorders the stream rather
// than merely thinning it. Concretely: with [resize→80, output drawn at 80] queued,
// admitting resize→100 by dropping the head leaves [output-at-80, resize→100], and the
// client paints those bytes onto a grid it was never told about.
//
// Reserving instead of evicting means nothing already queued is ever removed, so order is
// preserved exactly: output is refused AT THE TAIL while a control frame always finds room.
const controlReserve = 8

// admitOutput reports whether there is room for one more expendable frame.
//
// Output is refused before the queue is truly full, which is the whole point: a client too
// slow to keep up misses live bytes and re-attaches for them, but a resize or gap notice it
// misses has nothing scheduled to re-send it.
func admitOutput(ch chan subFrame) bool {
	return AdmitExpendable(ch, controlReserve)
}

// pushControl delivers a frame that must not be lost.
//
// The reserve above means this normally just sends. The eviction path survives only for the
// pathological case of a consumer stalled long enough to fill even the reserve with control
// frames — at which point the newest grid is the one worth keeping, because it is the one
// the client will still be wrong about after everything queued has been drained.
func pushControl(ch chan subFrame, f subFrame) {
	PushNewestOnFull(ch, f)
}

// effectiveSizeLocked computes the size the PTY should actually be.
//
// SMALLEST WINS among everything that is watching. The reason is not fairness, it is that
// a terminal cannot show what does not fit: if the session is wider than someone's window,
// their view is not "cropped", it is scrambled — a TUI paints by absolute cursor address,
// so every line past the edge wraps and shifts everything below it. Whoever has the
// smallest window is the constraint, exactly as in tmux.
//
// An attachment that declares 0 is an OBSERVER: it is watching but imposes nothing. That
// is what lets a CLI look at a session from a small window without reflowing the browser
// somebody else is working in.
//
// With nothing attached, the manual size applies; failing that the current size stands. A
// session with no viewers keeps its geometry rather than collapsing, because its
// scrollback was drawn at that size and will be replayed at it.
func (s *Session) effectiveSizeLocked() Grid {
	var g Grid
	for _, sub := range s.subs {
		if sub.size.Zero() {
			continue
		}
		// Minimised per axis, not "pick the smallest window": a 100×24 and an 80×50 yield
		// 80×24, the largest grid that fits in both. Picking one window wholesale would
		// leave the other overflowing on the axis it was smaller on.
		if g.Cols == 0 || sub.size.Cols < g.Cols {
			g.Cols = sub.size.Cols
		}
		if g.Rows == 0 || sub.size.Rows < g.Rows {
			g.Rows = sub.size.Rows
		}
	}
	if g.Zero() {
		if !s.manual.Zero() {
			return s.manual
		}
		return s.size
	}
	return g
}

// applySizeLocked recomputes the effective size and, if it changed, resizes the PTY and
// tells every attached client. Caller holds s.mu.
//
// The "if it changed" is load-bearing: pty.Setsize delivers SIGWINCH to the shell and to
// whatever it is running, and programs redraw on it. Re-applying the same size on every
// attach, detach and keystroke-sized window nudge would be a redraw storm nobody asked
// for.
func (s *Session) applySizeLocked() {
	// A DEAD session's geometry is frozen, whichever path asks.
	//
	// Its recorded size is the shape its scrollback was written in and the grid the replay
	// must be rendered onto; changing it after the fact produces a different screen, not a
	// resized one. Enforcing that here rather than only in Resize also closes a narrower
	// hole: Destroy closes the PTY, so a resize racing it would ioctl a dead descriptor,
	// fail, and — before this check — still record and announce a size the terminal never
	// entered. Destroy takes s.mu to read the descriptor, so this check and that close are
	// ordered against each other rather than merely unlikely to collide.
	if !s.alive {
		return
	}
	next := s.effectiveSizeLocked()
	if next.Zero() || next == s.size {
		return
	}
	if s.pty != nil && !s.ptyClosed {
		// Advisory: a pipe-backed session (tests) has no window size to set, and failing to
		// deliver SIGWINCH does not make the LOGICAL grid wrong — that is what the replay
		// reads. The case where it would be actively WRONG — a descriptor that has been
		// closed, whose number the kernel may already have reassigned — is excluded by the
		// flag, not by the liveness check: close happens before the process is reaped.
		c, r := next.Wire()
		_ = pty.Setsize(s.pty, &pty.Winsize{Cols: c, Rows: r})
	}
	s.size = next
	// The screen model has to learn the new shape from the same place the PTY does, or its grid
	// and the terminal's disagree and every subsequent line is laid out at the wrong width.
	s.history.Resize(next)
	// One pointer for every subscriber: a Grid is a value nobody mutates after it is
	// announced, so N copies of the same two numbers would only invite the question of
	// whether they can differ.
	for _, sub := range s.subs {
		pushControl(sub.ch, subFrame{Resize: &next})
	}
}

// SetAttachSize records the size of one attachment, re-derives the session's size, and makes
// sure THAT attachment learns what it actually got.
//
// This is the path a window resize takes: the browser's terminal reflows, or the CLI gets
// SIGWINCH, and each tells the daemon about ITSELF. Nobody sets "the session's size"
// directly any more, which is what stops two clients from fighting over it.
//
// The answer is the half that is easy to leave out. When a request does not move the session
// — another window is smaller — there is no change to broadcast, so a design that only
// announces changes tells this client nothing at all. It has already reflowed its own
// terminal to the size it asked for, and from then on it paints onto a grid the session
// never entered, with nothing scheduled to correct it.
//
// Answering here rather than in the connection loop is not organisational tidiness: the
// subscription's channel is closed under this same lock when the session ends, so a push
// from outside it races that close — send-on-closed-channel, not merely a stale read.
func (s *Session) SetAttachSize(subID int, want Grid) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[subID]
	if !ok {
		return
	}
	before := s.size
	sub.size = want
	s.applySizeLocked()
	if s.size == before && // nothing was broadcast…
		!s.size.Zero() &&
		s.size != want { // …and this client did not get what it asked for
		got := s.size
		pushControl(sub.ch, subFrame{Resize: &got})
	}
}

// Scrollback returns everything the ring currently holds, with the geometry needed to
// render it. Both travel together because a screen replayed onto the wrong grid is the
// drift this API exists to prevent.
func (s *Session) Scrollback() (data []byte, grid Grid) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ring.Read(), s.size
}
