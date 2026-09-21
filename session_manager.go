package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/brightman-ai/deepwork-terminal/ansisignal"
	"github.com/brightman-ai/deepwork-terminal/muxd"
	"github.com/brightman-ai/kit/log"
	"github.com/brightman-ai/kit/obs"
)

var logger = log.Module("terminal")

// CreateOptions describes the product-level terminal session metadata and runtime
// options supplied by the WebUI.
type CreateOptions struct {
	Name   string
	Title  string
	Engine string
	Shell  string
	CWD    string
}

// PTYFactory is the daemon's PTY constructor, re-exported so test fixtures can inject a
// pipe-backed one without importing muxd directly.
//
// The implementation moved to muxd because the daemon is now the ONLY thing that creates
// PTYs. There is deliberately no second construction path left in this package: two PTY
// paths is exactly the shape this repo has been burned by before ("两条 PTY 路径只修一条,
// 等于没修" — shell_cmd.go), and here it would be worse, because only one of the two
// would survive a restart.
type PTYFactory = muxd.PTYFactory

// activeConnEntry tracks the active WS connection for a session (BUG-3 preemption).
type activeConnEntry struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	// epoch orders connections against each other. Preemption, subscribing and taking the
	// grid are three separate steps, so two browsers arriving together can interleave: A
	// registers, B registers and preempts A, then A — still running its own handler —
	// claims the grid it has already lost. A number that only ever goes up lets the loser
	// recognise itself without holding a lock across all three steps. See SetViewerOwner.
	epoch uint64
}

// SessionManager manages terminal sessions with PTY processes.
// All state is held in memory (IR-03: no DB, no persistence).
// [Ref: T5-B3, CAP-session-lifecycle S2, DDC-11]
type SessionManager struct {
	sessions     sync.Map // map[string]*Session
	activeConns  sync.Map // map[string]*activeConnEntry — one per session (BUG-3)
	connEpoch    atomic.Uint64
	bufferSize   int
	defaultShell string

	// mux is the connection to dw-muxd, which owns every PTY. The sessions map is a
	// VIEW of what the daemon holds, rebuilt from it on startup (see Restore) — not a
	// second source of truth. Every mutation goes to the daemon; nothing authoritative
	// is stored only here, which is what makes restarting this process harmless.
	muxMu     sync.Mutex
	mux       *muxd.Client
	muxSocket string
	// detached is set by CloseAll/detachMux and cleared by any deliberate re-entry
	// (Restore or a create). While set, every daemon-facing accessor refuses — see
	// errDetached for what happens without it.
	detached   bool
	ptyFactory PTYFactory // non-nil only for in-process test daemons

	// local* are set only when this manager hosts its own in-process daemon, which is a
	// TEST fixture, not a deployment mode: sessions are still reached over the same
	// socket and the same protocol, the daemon just happens to live in this process.
	localDaemon *muxd.Daemon
	localLn     net.Listener

	// OnSignal is called from the PTY read goroutine for every EXPLICIT out-of-band signal
	// the program emitted (BEL / OSC notification — see ansisignal). It is a pure tap: the
	// bytes are already on their way to the browser by the time it runs, and nothing it does
	// can alter them.
	//
	// Must be set BEFORE the first session is created (NewServer does): read loops run
	// concurrently, so assigning it later is a data race. It runs on the output hot path, so
	// the implementation must not block — see onSessionSignal for how that is honoured.
	OnSignal func(*Session, ansisignal.Signal)

	// Set before Restore/Create; observes bounded clipboard writes without browser ownership.
	OnClipboard func(*Session, string, int64, bool)

	// origin stamps every session this manager creates with "whose tab list owns me" — see
	// sessionMeta.Origin. Set once by the server at construction, before any session exists,
	// for the same reason OnSignal is. Empty in test fixtures, which is correct: a fixture has
	// no shared daemon to disambiguate against.
	origin string

	// EnvSource answers "what environment should the NEXT shell start with". Called per create,
	// never cached, because the whole point of the overlay behind it is that editing it takes
	// effect on the next terminal WITHOUT restarting this process (see env_overlay.go).
	//
	// A hook rather than a direct call so this file keeps knowing nothing about where the
	// overlay lives; nil falls back to this process's own environment, which is what every
	// session got before the overlay existed.
	EnvSource func() []string
}

// SetOrigin names the deployment this manager belongs to, so sessions it creates can be told
// apart from those of another deployment sharing the same per-user daemon (see
// sessionMeta.Origin). Must be called before the first Create.
func (m *SessionManager) SetOrigin(origin string) { m.origin = origin }

// ptyEnv resolves the environment for one new shell. See the EnvSource field.
func (m *SessionManager) ptyEnv() []string {
	if m.EnvSource != nil {
		if env := m.EnvSource(); env != nil {
			return env
		}
	}
	return os.Environ()
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(bufferSize int, defaultShell string) *SessionManager {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferCapacity
	}
	if defaultShell == "" {
		if shell := os.Getenv("SHELL"); shell != "" {
			defaultShell = shell
		} else {
			defaultShell = "/bin/bash"
		}
	}
	return &SessionManager{
		bufferSize:   bufferSize,
		defaultShell: defaultShell,
	}
}

// NewSessionManagerWithFactory creates a SessionManager backed by an IN-PROCESS daemon
// using the given PTY factory. Test fixtures use it to drive terminal output through a
// pipe.
//
// It is a fixture, not a second deployment mode: the sessions still live in a muxd
// Daemon, reached over a real unix socket with the real protocol. What differs is only
// where that daemon is hosted. (A "run the daemon in-process for production too" option
// was considered and rejected — persistence would hold in one mode and not the other,
// which is two products wearing one name.)
func NewSessionManagerWithFactory(bufferSize int, defaultShell string, factory PTYFactory) *SessionManager {
	sm := NewSessionManager(bufferSize, defaultShell)
	sm.ptyFactory = factory
	return sm
}

// Create creates a new terminal session with a PTY process.
// [Ref: T5-B3, T5-B4.M1, CAP-session-lifecycle S2]
func (m *SessionManager) Create(name string) (*Session, error) {
	return m.CreateWithOptions(CreateOptions{Name: name})
}

// CreateWithOptions creates a new terminal session with product metadata.
func (m *SessionManager) CreateWithOptions(opts CreateOptions) (*Session, error) {
	var id string
	name := opts.Name
	if name == "" {
		name = opts.Title
	}
	if name == "" {
		name = time.Now().Format("0102-1504") // MMdd-HHmm format
	}

	shellPath := opts.Shell
	if shellPath == "" {
		shellPath = m.defaultShell
	}
	engine := opts.Engine
	if engine == "" {
		engine = "shell"
	}
	cwd := opts.CWD
	// Expand ~ to user home directory.
	if cwd == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		}
	} else if strings.HasPrefix(cwd, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = filepath.Join(home, cwd[2:])
		}
	}
	// Apply default CWD when none specified.
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = filepath.Join(home, "code", "work")
		}
	}
	// Create directory if it doesn't exist.
	if cwd != "" {
		if _, err := os.Stat(cwd); os.IsNotExist(err) {
			if mkErr := os.MkdirAll(cwd, 0755); mkErr != nil {
				return nil, fmt.Errorf("cannot create cwd: %w", mkErr)
			}
		}
	}
	// Validate it is a directory.
	if cwd != "" {
		stat, err := os.Stat(cwd)
		if err != nil {
			return nil, fmt.Errorf("cwd unavailable: %w", err)
		}
		if !stat.IsDir() {
			return nil, fmt.Errorf("cwd is not a directory: %s", cwd)
		}
	}

	client, err := m.client()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	meta := sessionMeta{
		Name:      name,
		Title:     opts.Title,
		Engine:    engine,
		CWD:       cwd,
		ShellPath: shellPath,
		CreatedAt: now,
		Origin:    m.origin,
	}
	prog, args := muxd.SplitShell(shellPath)
	// The daemon assigns the id. That is what makes it survive a restart unchanged: an
	// id minted here would be gone the moment this process is, and the tab would come
	// back as a stranger.
	id, shellPID, err := client.Create(muxd.CreateReq{
		Argv: append([]string{prog}, args...),
		Cwd:  cwd,
		// THIS server's environment, not the daemon's.
		//
		// Omitting it does not mean "no environment" — muxd falls back to the daemon's own,
		// and the daemon is a per-user process that outlives every host restart. So a shell
		// opened today inherited whatever was set in the shell that first started the daemon,
		// possibly days ago and possibly for a different host: a PATH the user has since
		// changed, or credentials scoped to another deployment. Before the daemon existed the
		// PTY was this process's child and got this process's environment; passing it
		// explicitly restores that, and makes the source of a session's environment a
		// decision rather than an accident of who spawned the daemon.
		//
		// muxd sanitises what it forwards (PTYEnv), so host-level session markers do not ride
		// along.
		//
		// Through EnvSource rather than os.Environ() directly: this process's own environment is
		// a snapshot frozen at startup, so a stray `ANTHROPIC_BASE_URL` in whatever shell
		// launched the server used to poison every terminal opened afterwards, with a full
		// restart as the only cure. The overlay behind this hook is the tmux answer — a mutable
		// table consulted at spawn time (env_overlay.go).
		Env:  m.ptyEnv(),
		Cols: muxd.DefaultCols,
		Rows: muxd.DefaultRows,
		Meta: meta.encode(),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	sess := &Session{
		ID:         id,
		Name:       name,
		Title:      opts.Title,
		Engine:     engine,
		CWD:        cwd,
		ShellPath:  shellPath,
		Origin:     m.origin,
		Buffer:     NewRingBuffer(m.bufferSize),
		Status:     StatusRunning,
		CreatedAt:  now,
		LastActive: now,
		viewers:    make(map[string]*viewer),
		done:       make(chan struct{}),
		mux:        client,
	}

	// LoadOrStore, not Store: the daemon broadcasts "created" the instant it spawns the
	// session, so this server's own watcher can receive that event and adopt the session
	// through Restore BEFORE this line runs. Store would then overwrite the adopted
	// struct, leaving an orphaned pump goroutine writing into a Session nobody can reach,
	// two subscriptions on the daemon, and terminalActive counted twice.
	//
	// If adoption won the race, its struct is the one already visible to HTTP handlers, so
	// that is the one to keep and return; it has been attached already, so this path is
	// done.
	if existing, adopted := m.sessions.LoadOrStore(id, sess); adopted {
		prior := existing.(*Session)
		prior.mu.Lock()
		prior.Name, prior.Title, prior.Engine = name, opts.Title, engine
		prior.mu.Unlock()
		logger.Info("session was adopted by this server's own watcher before create finished",
			"id", id)
		return prior, nil
	}
	terminalSpawnTotal.Inc()
	terminalActive.Add(1)

	// BUG-6: Detect tmux in child process environment.
	sess.setShellPID(shellPID)
	if detectTmuxByPID(shellPID) {
		sess.mu.Lock()
		sess.TmuxDetected = true
		sess.mu.Unlock()
		logger.Info("tmux detected in session", "id", id)
		terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalSpawn), "tmux detected in session", "session_id", id)
	}

	// Attach to the daemon stream and start pumping. Attaching (rather than owning an
	// fd) is what lets this whole process go away and come back.
	//
	// A failure here has to be ATOMIC with the create. Returning the error while leaving
	// the session in place gave the caller "creation failed" for a shell that was very
	// much alive — running in the daemon, listed in this map as Running, with no pump, so
	// no output, no exit detection, and no way for the user to reach or remove it. Undo
	// the create instead: if we cannot report on it, we do not keep it.
	if err := m.attach(sess, nil); err != nil {
		m.sessions.Delete(id)
		terminalActive.Sub(1)
		if client != nil {
			if derr := client.Destroy(id); derr != nil {
				logger.Warn("could not clean up a session whose attach failed", "id", id, "error", derr)
			}
		}
		return nil, fmt.Errorf("attach session: %w", err)
	}

	logger.Info("session created",
		"id", id,
		"name", name,
		"title", opts.Title,
		"engine", engine,
		"cwd", cwd,
		"shell", shellPath)
	terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalSpawn), "session created",
		"session_id", id,
		"name", name,
		"title", opts.Title,
		"engine", engine,
		"cwd", cwd,
		"shell", shellPath)

	return sess, nil
}

// Get returns a session by ID or an error if not found.
func (m *SessionManager) Get(id string) (*Session, error) {
	v, ok := m.sessions.Load(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return v.(*Session), nil
}

// List returns all sessions.
// List returns every live session in STABLE CREATION ORDER (oldest first).
//
// The sort is load-bearing, not cosmetic. sync.Map.Range visits in an unspecified order that
// varies between calls, and pro's CLI derives its whole tab strip straight from this list — so
// an unsorted List() meant:
//   - a newly created terminal could appear anywhere in the strip, including first, where the
//     position-based label renders it as "终端1" while an older tab is also showing "终端1";
//   - the numbers reshuffled on every poll, which silently breaks the two things built on top of
//     them — `prefix+N` jump-to-tab and the overview card numbering (both promise "the number you
//     see is the number you press").
//
// Creation order is also simply what a user means by "the new tab goes on the end". Ties (two
// sessions created inside the same clock tick) fall back to ID so the order is total and never
// flickers between two equally-old sessions.
func (m *SessionManager) List() []*Session {
	var result []*Session
	m.sessions.Range(func(_, value any) bool {
		result = append(result, value.(*Session))
		return true
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// Destroy terminates a session's PTY process and removes it from the manager.
// [Ref: CAP-session-lifecycle S2]
func (m *SessionManager) Destroy(id string) error {
	v, ok := m.sessions.Load(id)
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	sess := v.(*Session)

	// EXPLICIT destroy: the user deleted this tab. The daemon kills the process group
	// and forgets the session; we drop our view of it. This is the one path that is
	// SUPPOSED to end a terminal — shutdown must never come through here (see CloseAll).
	//
	// ORDER MATTERS: ask the daemon FIRST, tear down locally only once it agrees.
	//
	// The reverse order — delete locally, then ask — turns any daemon-side failure into a
	// tab that disappears and then comes back at the next reconcile, because the shell it
	// names is still running. A delete that silently did not happen is worse than one that
	// says so. (not_found is not a failure: it means the session is already gone, which is
	// exactly what was asked for.)
	sess.mu.Lock()
	client := sess.mux
	sess.mu.Unlock()
	if client != nil {
		if err := client.Destroy(id); err != nil && !muxd.IsNotFound(err) {
			logger.Warn("daemon destroy failed; keeping the tab rather than showing a delete that did not happen",
				"id", id, "error", err)
			return fmt.Errorf("terminal: destroy session %s: %w", id, err)
		}
	}

	m.sessions.Delete(id)
	sess.mu.Lock()
	stream := sess.stream
	sess.stream = nil
	sess.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
	sess.doneOnce.Do(func() {
		sess.mu.Lock()
		sess.Status = StatusExited
		sess.mu.Unlock()
		close(sess.done)
	})

	logger.Info("session destroyed", "id", id)
	terminalActive.Sub(1)
	terminalDuration.Observe(time.Since(sess.CreatedAt).Seconds())
	clearTerminalInputTracker(id)
	terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalTerminate), "session destroyed",
		"session_id", id,
		"duration_ms", time.Since(sess.CreatedAt).Milliseconds())
	return nil
}

// Subscribe registers one viewer — a WebSocket connection — and returns its frame channel
// plus the function that removes it.
//
// The new viewer starts as an OBSERVER: it receives output but declares no size until the
// browser measures its own terminal and calls SetViewerSize. Seeding it with a guess would
// let a tab that has not even rendered yet shrink the session for everyone already watching.
//
// Unsubscribing withdraws its size as part of removing it — the two cannot be separated
// because they are one entry. That is what stops a closed window from constraining the
// session forever, and it is why the size lives in this registry rather than beside it.
//
// It also returns the grid the session is running at AS OF the moment this viewer joined.
// That is not a convenience — it is the only value the caller may announce. Reading it
// separately afterwards races applyGrid: a resize landing in between would leave the caller
// announcing the NEW size to a browser whose queue still holds bytes drawn at the old one,
// and old-grid bytes painted onto a new grid is a scrambled screen, not a slightly-off one.
// The daemon's own Subscribe hands back replay and geometry together for the same reason.
func (m *SessionManager) Subscribe(sess *Session, subID string) (<-chan viewerFrame, int, int, func()) {
	v := &viewer{ch: make(chan viewerFrame, 256)}
	sess.subMu.Lock()
	sess.viewers[subID] = v
	sess.mu.Lock()
	grid := sess.pty
	sess.mu.Unlock()
	sess.subMu.Unlock()
	cols, rows := grid.Cols, grid.Rows
	if grid.Zero() {
		cols, rows = spawnCols, spawnRows
	}

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			sess.subMu.Lock()
			existing, ok := sess.viewers[subID]
			if ok {
				delete(sess.viewers, subID)
				close(existing.ch)
			}
			sess.subMu.Unlock()
			if !ok {
				return
			}
			// Losing a viewer can GROW the session back: the constraint it imposed is gone.
			// Outside the lock — this talks to the daemon, and holding a session lock across
			// I/O is how an unresponsive socket becomes a frozen terminal for everyone else.
			if _, err := sess.syncViewport(); err != nil {
				logger.Debug("could not re-declare the viewport after a viewer left",
					"id", sess.ID, "error", err)
			}
		})
	}
	return v.ch, cols, rows, unsub
}

// SetActiveConn registers a new active WS connection for a session, preempting any existing one.
// BUG-3: Only one WS connection per session is allowed at a time.
func (m *SessionManager) SetActiveConn(sessionID string, conn *websocket.Conn, cancel context.CancelFunc) uint64 {
	epoch := m.connEpoch.Add(1)
	newEntry := &activeConnEntry{conn: conn, cancel: cancel, epoch: epoch}

	if prev, loaded := m.activeConns.Swap(sessionID, newEntry); loaded {
		terminalWSPreemptionsTotal.Inc()
		old := prev.(*activeConnEntry)
		// Send preempted message to old connection before closing.
		payload, _ := json.Marshal(PreemptedPayload{Message: "Another client connected"})
		msg, _ := json.Marshal(WSControlMessage{
			Type:    MsgTypePreempted,
			Payload: payload,
		})
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = old.conn.Write(writeCtx, websocket.MessageText, msg)
		writeCancel()

		// Cancel the old connection's context and close it.
		old.cancel()
		old.conn.Close(websocket.StatusPolicyViolation, "preempted by new connection")
		logger.Info("preempted existing WS connection", "sessionId", sessionID)
		terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalAttach), "cli ws preempted",
			"session_id", sessionID)
	}
	return epoch
}

// ClearActiveConn removes the active connection entry for a session if it matches the given conn.
func (m *SessionManager) ClearActiveConn(sessionID string, conn *websocket.Conn) {
	if v, ok := m.activeConns.Load(sessionID); ok {
		entry := v.(*activeConnEntry)
		if entry.conn == conn {
			m.activeConns.Delete(sessionID)
		}
	}
}

// CloseAll DETACHES from the daemon. Called during server shutdown.
//
// It used to destroy every session, and that was the bug: shutting down the HTTP server
// is not a request to end the user's work. Restarting after a recompile took every tab
// with it — the exact symptom dw-muxd exists to remove. The sessions keep running in the
// daemon and are picked back up by Restore on the next start.
//
// Deliberate destruction still exists and is still reachable — it is DestroyAll, and the
// two must never be collapsed back into one.
func (m *SessionManager) CloseAll() error {
	return m.detachMux()
}

// DestroyAll terminates every session for real.
//
// This is the EXPLICIT teardown: tests cleaning up after themselves, and any deliberate
// "end everything" action. It is intentionally NOT what shutdown calls — see CloseAll.
func (m *SessionManager) DestroyAll() {
	m.sessions.Range(func(key, _ any) bool {
		_ = m.Destroy(key.(string))
		return true
	})
	_ = m.detachMux()
	// Only a fixture-hosted daemon is stopped here; a real one keeps running, since
	// destroying sessions is not a reason to shut the user's daemon down.
	m.stopLocalDaemon()
}
