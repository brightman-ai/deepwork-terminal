// Package terminal implements the BS-08 Terminal subsystem.
// It provides PTY management, WebSocket-based terminal I/O, and session lifecycle
// for browser-based terminal access. All state is held in memory (IR-03).
package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brightman-ai/deepwork-terminal/ansisignal"
	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// SessionStatus represents the lifecycle state of a terminal session.
// [Ref: T5-B3, CAP-session-lifecycle S2]
type SessionStatus string

const (
	StatusRunning SessionStatus = "running"
	StatusExited  SessionStatus = "exited"
)

// Session represents a single terminal session backed by a PTY.
// [Ref: T5-B3]
type Session struct {
	bracketedPaste atomic.Bool

	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Title     string      `json:"title"`
	Engine    string      `json:"engine"`
	CWD       string      `json:"cwd"`
	ShellPath string      `json:"-"`
	Buffer    *RingBuffer `json:"-"`

	// Origin is which deployment's tab list owns this session — see sessionMeta.Origin for
	// why one daemon serves two tab lists. Carried on the struct (not just in the meta blob)
	// because SetName rewrites the whole meta from these fields: a rename would otherwise
	// quietly blank it, and a session with no origin is a session orphan adoption has to guess
	// about.
	Origin string `json:"origin,omitempty"`

	// mux is the daemon connection this session is reached through, and stream is this
	// session's attached output channel. The PTY itself lives in the daemon — the server
	// deliberately holds no file descriptor for it, which is what lets the server be
	// restarted (or replaced) without the shell noticing.
	mux    *muxd.Client
	stream *muxd.Stream

	// shellPID is reported by the daemon. The server cannot see the process, so this must
	// arrive over the protocol rather than be re-derived from the process tree.
	shellPID   int
	Status     SessionStatus `json:"status"`
	CreatedAt  time.Time     `json:"createdAt"`
	LastActive time.Time     `json:"lastActive"`

	// viewers holds the active WebSocket connections watching this session, keyed by
	// subscription id. Protected by subMu.
	//
	// The registry carries BOTH halves of what a viewer is — where its bytes go, and how big
	// its window is — because they have exactly the same lifetime. Keeping the sizes in a
	// second map next to this one would let the two drift, and the drift has a name: a viewer
	// that disconnected but whose size is still constraining the session (see viewportLocked).
	viewers map[string]*viewer
	// activeViewer is the subscription id of the ONE viewer that currently sizes this
	// session — the browser holding the active WebSocket (see SetActiveConn). Empty when no
	// browser is attached. Protected by subMu, in the same map's lock, for the same reason
	// the sizes are: a stale id here is a viewer sizing a session it has already left.
	activeViewer string
	// ownerEpoch is the connection epoch that last set activeViewer (see
	// SessionManager.SetActiveConn). It only goes up, so a connection that was preempted
	// mid-handshake cannot take the grid back from the one that replaced it.
	ownerEpoch uint64
	subMu      sync.RWMutex

	// geomMu serializes "re-derive the smallest window and declare it to the daemon".
	//
	// It is a separate lock from mu and subMu on purpose: the declaration is I/O, and the
	// two ends of a resize race must not be reordered on the wire. Whoever holds this lock
	// reads the current minimum and pushes it, so the LAST push always carries the LATEST
	// minimum. Lock order is geomMu → subMu / mu, never the reverse.
	geomMu sync.Mutex
	// declared is the size this server last declared on its own attachment; the zero Grid
	// means "declared nothing", i.e. this server is an observer. Guarded by geomMu.
	declared muxd.Grid

	// done is closed when the PTY read loop exits (shell exited or error).
	done     chan struct{}
	doneOnce sync.Once

	// exitCode stores the shell exit code once the process exits.
	exitCode int

	// TmuxDetected indicates whether the shell is running inside tmux.
	// Set after session creation by checking /proc/{pid}/environ.
	// [Ref: BUG-6, DDC-13]
	TmuxDetected bool `json:"tmuxDetected"`

	// pty is the PTY's CURRENT window size — the size the program on the other end believes
	// it is drawing into.
	//
	// It is a CACHE OF THE DAEMON'S ANSWER, never of our request. The daemon owns the size:
	// it fits the session to every client watching it (smallest-wins), so the size a browser
	// asks for and the size the session actually enters are routinely different numbers. This
	// field used to be written optimistically at request time, which meant that in the exact
	// case per-attachment geometry exists to handle — two windows of different sizes — the
	// replay grid recorded a size the terminal never had. It is now written only where the
	// answer arrives: AttachAck, MsgResized, and the reconcile summary.
	//
	// Why the session has to remember this at all: the Agent Overview reconstructs each card's
	// preview by REPLAYING the PTY byte stream onto a character grid (screen.go). A TUI paints
	// by absolute cursor addressing, so that grid must be the SAME SIZE as the real terminal or
	// the replay is not a reconstruction — it's a different screen. It used to be a hardcoded
	// 48x200 while the PTY was born 50x220 and then resized to whatever the browser was: every
	// row past 48 got clamped onto the last row and OVERWROTE whatever was already there, so the
	// bottom of a card was several unrelated screen rows mashed into one ("Debug" surviving as
	// "ebug" after another row ate its D — observed live), and any line longer than 200 columns
	// wrapped here but not in reality, shifting every row below it.
	//
	// Protected by mu.
	pty muxd.Grid

	// gridSeq is the ring buffer's byte count AT THE MOMENT the grid last changed — the
	// boundary between bytes drawn for the previous window size and bytes drawn for the
	// current one.
	//
	// The ring is a byte stream, not a screen. Replaying it means re-executing every escape
	// sequence it holds against ONE grid: the one the browser is on now. Bytes written when
	// the session was 110 columns wide carry absolute cursor addresses, erase-to-end-of-line
	// and wrap points that only mean what they meant at 110 columns. Re-run them at 219 and
	// each one lands somewhere else — a status bar redrawn at the old width does not overwrite
	// the new one, it settles a row below it, and the screen accumulates a copy of itself per
	// resize. That is the "duplicated bottom line" and the scrambled screen, and it is not a
	// transient: nothing repaints a scrollback.
	//
	// So a replay starts HERE, never earlier. Losing the scrollback above the last resize is a
	// visible, honest loss; a screen assembled out of two incompatible grids is a plausible
	// picture of something that never existed.
	//
	// WHAT ABOUT THE MODES THE CUT ALSO DROPS — measured, and the answer is "nothing". A byte
	// stream carries state as well as content: mouse reporting (DECSET 1000/1002/1003/1006),
	// the alternate screen, bracketed paste. A cut past the byte that enabled one of them
	// looks like it would leave a correct screen whose wheel is dead.
	//
	// It does not, because of what moves this mark: only a resize does, a resize is a
	// SIGWINCH, and a full-screen program answering SIGWINCH repaints — tmux re-issues its
	// private modes as part of that repaint, so they land AFTER the cut and survive. Verified
	// end to end on an isolated fixture: replay truncated from 52045 bytes to 2369, and the
	// wheel still reached tmux (pane_in_mode 0 → 1) with no compensation of any kind.
	//
	// A version of this code carried the dropped prefix's modes forward. It was deleted: it
	// guarded a scenario nobody has observed, and code that exists for an unobserved scenario
	// reads to the next person as though the scenario is real. If a program is ever found that
	// repaints on SIGWINCH WITHOUT restating its modes, this is the place — scan the dropped
	// prefix for `ESC [ ? … h|l`, re-send the final state of each, both directions (autowrap
	// and cursor visibility default to ON, so a dropped DECRST matters too).
	//
	// Protected by mu.
	gridSeq uint64

	// lastSignal is the most recent UNANSWERED explicit signal from the program in this
	// session — a BEL or an OSC desktop notification (see ansisignal). It is deliberately
	// sticky: unlike a transient event it stays until the user actually responds (any input
	// to this session clears it), so a bell that rang while the tab was closed is still
	// waiting when they come back. Zero Kind means "nothing pending".
	lastSignal   ansisignal.Signal
	lastSignalAt time.Time
	// lastSignalSeq increments on every recorded signal. Two identical bells are
	// indistinguishable by content, so the sequence number is what tells a client "this is a
	// NEW one" rather than the same one still standing.
	lastSignalSeq uint64

	mu sync.Mutex // protects Status, LastActive, exitCode, TmuxDetected, lastSignal*, pty, gridSeq, Name/Title
}

// SetName renames the session (thread-safe). Clears Title too: sessionTitle()
// prefers Title over Name, so an explicit user rename must win over whatever
// static Title the session was created with, or the old value would keep
// winning forever no matter what the user renames it to — the exact bug this
// method exists to close (a renamed tab's notifications kept the pre-rename
// name because nothing ever wrote a new value back to the session at all).
func (s *Session) SetName(name string) {
	s.mu.Lock()
	s.Name = name
	s.Title = ""
	meta := sessionMeta{
		Name: name, Engine: s.Engine, CWD: s.CWD,
		ShellPath: s.ShellPath, CreatedAt: s.CreatedAt,
		// Carried through, not re-derived: this rewrites the ENTIRE meta blob, so any field
		// omitted here is a field erased in the daemon. Dropping Origin would make a renamed
		// session look like it belongs to nobody.
		Origin: s.Origin,
	}
	client := s.mux
	s.mu.Unlock()

	// PUSH IT TO THE DAEMON. The name lives in the daemon's metadata blob — that is what
	// makes it survive a restart and what every other client reads — so a rename that only
	// touches this process is a rename that has not really happened: `dw-terminal ls` keeps
	// printing the old name, `attach <new name>` fails, another host never learns, and the
	// next restore brings the old name back.
	//
	// A failure here is logged rather than returned: the tab is renamed on screen either
	// way, and the next SetMeta or restart reconciles it. Refusing the rename over a
	// transient daemon hiccup would be the worse trade.
	if client != nil {
		if err := client.SetMeta(s.ID, meta.encode()); err != nil {
			logger.Warn("could not persist the new name to the daemon; other clients will "+
				"keep showing the old one until the next reconcile", "id", s.ID, "error", err)
		}
	}
}

// RequestPTYSize asks for a session size on behalf of a caller that is NOT a window —
// the REST endpoint and InProcessService, neither of which has a viewer behind it.
//
// It goes to the daemon's control plane, which treats it as the FALLBACK size: the one used
// while nothing attached has declared a size of its own. That is the honest place for it. A
// caller that is not displaying the session cannot know what will fit in the windows that
// are, so letting it overrule them would re-open the exact bug per-attachment geometry
// closes — one client reflowing another's terminal. When a browser IS watching, this request
// is therefore ignored, deliberately and silently, in the same way `tmux resize-window` is
// clamped by the clients actually attached.
//
// Note what it does NOT do: write the size onto the session. The daemon decides the session's
// size and reports it back (AttachAck / MsgResized); recording our request as if it were the
// answer is what used to make the overview's replay grid disagree with the real terminal.
func (s *Session) RequestPTYSize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return fmt.Errorf("pty size: cols/rows must be positive (%d×%d)", cols, rows)
	}
	s.mu.Lock()
	client := s.mux
	s.mu.Unlock()
	if client == nil {
		return fmt.Errorf("session %s is not attached to a daemon", s.ID)
	}
	return client.Resize(s.ID, uint16(cols), uint16(rows))
}

// setPTYSizeFromDaemon records the size the DAEMON says this session is now running at.
// This is the only writer of s.pty — see the field comment for why there is
// exactly one.
func (s *Session) setPTYSizeFromDaemon(g muxd.Grid) {
	if g.Zero() {
		return
	}
	s.mu.Lock()
	// Only a change BETWEEN two known sizes is an epoch boundary. The first size a session
	// ever learns is not a resize — it is the daemon finally telling us what the PTY was
	// spawned at — and treating it as one would discard everything the shell printed before
	// the AttachAck, which is the entire screen of a session that has just started.
	if !s.pty.Zero() && s.pty != g && s.Buffer != nil {
		// Mark where the old grid's bytes end, BEFORE recording the new size: the ring is
		// still receiving output drawn at the previous width, and the repaint the program is
		// about to do has not started. Erring early keeps the whole repaint inside the new
		// epoch — erring late would clip its leading clear-screen and replay a repaint that
		// starts halfway through. See the gridSeq field for why a replay must not cross this
		// boundary at all.
		s.gridSeq = s.Buffer.Seq()
	}
	s.pty = g
	s.mu.Unlock()
}

// ReplayTail returns up to max bytes of scrollback and says how many of them at the END were
// drawn for the CURRENT grid — everything a replay may send without mixing two geometries.
//
// The bytes and the boundary are computed together on purpose. Reading the ring's contents,
// its byte counter, and the grid mark in three separate calls lets a write or a resize land
// between them, and the arithmetic then mixes a length from one moment with a counter from
// another — producing a cut that is off by exactly the bytes that arrived in the gap. The
// ring hands back its data and its counter under one lock (ReadTailAt); the grid mark is read
// AFTER, so the two possible orderings both have an honest answer:
//
//   - mark ≤ seq: the mark is inside what we just read; keep everything after it.
//   - mark > seq: the resize happened after our read, so every byte we hold predates the
//     current grid. Keep none of it, and let the program's own repaint fill the screen.
//
// A session that has never resized has no boundary to respect: its whole ring is one epoch.
func (s *Session) ReplayTail(max int) (data []byte, currentGrid int) {
	if s.Buffer == nil {
		return nil, 0
	}
	data, seq := s.Buffer.ReadTailAt(max)
	s.mu.Lock()
	since := s.gridSeq
	s.mu.Unlock()

	switch {
	case since == 0:
		// Never resized — or the ring was Reset() out from under the mark. Either way the
		// buffer's contents are the only epoch we can honestly claim to know about.
		return data, len(data)
	case since > seq:
		// A resize landed after the read: nothing we are holding belongs to the grid this
		// client is about to be on.
		return data, 0
	}
	n := int(seq - since)
	if n > len(data) {
		// The epoch is older than the slice is long: all of it is current-grid.
		n = len(data)
	}
	return data, n
}

// PTYSize returns the PTY's current window size. Falls back to the spawn size when a session
// predates any resize — never zero, because the replay grid must always have dimensions.
func (s *Session) PTYSize() muxd.Grid {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pty.Zero() {
		return s.pty
	}
	return muxd.Grid{Cols: spawnCols, Rows: spawnRows}
}

// RecordSignal stores an explicit out-of-band signal as this session's pending
// "needs-you" state and returns its sequence number (thread-safe).
func (s *Session) RecordSignal(sig ansisignal.Signal, at time.Time) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSignal = sig
	s.lastSignalAt = at
	s.lastSignalSeq++
	return s.lastSignalSeq
}

// PendingSignal returns the unanswered signal, when the session has one (thread-safe).
func (s *Session) PendingSignal() (sig ansisignal.Signal, at time.Time, seq uint64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSignal.Kind == "" {
		return ansisignal.Signal{}, time.Time{}, 0, false
	}
	return s.lastSignal, s.lastSignalAt, s.lastSignalSeq, true
}

// ClearSignal drops the pending signal — the user answered. The sequence number is NOT
// reset, so the next signal still reads as new to a client that saw the previous one.
func (s *Session) ClearSignal() {
	s.mu.Lock()
	s.lastSignal = ansisignal.Signal{}
	s.lastSignalAt = time.Time{}
	s.mu.Unlock()
}

// (Session.reap is gone: the daemon owns the process, so the daemon does the waiting.
// The server never had a legitimate reason to Wait on a process it no longer parents.)

// Done returns a channel that is closed when the PTY read loop exits.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// GetExitCode returns the shell exit code (thread-safe).
func (s *Session) GetExitCode() int {
	s.mu.Lock()
	code := s.exitCode
	s.mu.Unlock()
	return code
}

// GetTmuxDetected returns whether tmux was detected (thread-safe).
func (s *Session) GetTmuxDetected() bool {
	s.mu.Lock()
	detected := s.TmuxDetected
	s.mu.Unlock()
	return detected
}

// ForceKillForeground sends SIGKILL to the PTY's current foreground process group — a
// harder-than-Ctrl+C recovery path for when the foreground program ignores SIGINT (the signal
// Ctrl+C sends through the PTY). TIOCGPGRP on the PTY master reads exactly the fact the kernel
// itself consults to route Ctrl+C; this just sends a stronger signal on demand instead of
// waiting for a program that has decided to ignore the polite one.
//
// When the foreground process IS the shell itself (no interactive child running), the shell's
// own process group gets killed too — the tab disconnects rather than no-op'ing. A "kill
// whatever's in front" command with no foreground child left to kill has nothing else
// History reads a page of this session's scrollback from the daemon that owns it.
//
// The server does NOT cache the result. It already keeps a duplicate of every session's byte
// stream (see Session.Buffer); adding a third copy of the same content in a different shape would
// give the same bug three places to disagree, and the daemon can answer a page in microseconds
// from memory it already has.
func (s *Session) History(req muxd.HistoryReq) (muxd.HistoryAck, error) {
	s.mu.Lock()
	client := s.mux
	s.mu.Unlock()
	if client == nil {
		return muxd.HistoryAck{}, fmt.Errorf("session %s is not attached to a daemon", s.ID)
	}
	req.ID = s.ID
	return client.History(req)
}

// SearchHistory finds text in this session's scrollback, IN THE DAEMON.
func (s *Session) SearchHistory(req muxd.HistorySearchReq) (muxd.HistorySearchAck, error) {
	s.mu.Lock()
	client := s.mux
	s.mu.Unlock()
	if client == nil {
		return muxd.HistorySearchAck{}, fmt.Errorf("session %s is not attached to a daemon", s.ID)
	}
	req.ID = s.ID
	return client.SearchHistory(req)
}

// meaningful to do, and pretending to succeed while leaving the user still stuck would be worse.
//
// Not offered for tmux-attached sessions: the PTY's foreground pgid there belongs to tmux's own
// server/client plumbing, not a fact the web UI can act on — tmux has its own recovery tools
// (kill-pane etc.) for that case. Callers should already hide the affordance for those sessions;
// this is the defense-in-depth backend half of that guard.
func (s *Session) ForceKillForeground() error {
	s.mu.Lock()
	client := s.mux
	tmux := s.TmuxDetected
	s.mu.Unlock()
	if tmux {
		return fmt.Errorf("session %s: force-kill not supported for tmux-attached sessions", s.ID)
	}
	if client == nil {
		return fmt.Errorf("session %s is not attached to a daemon", s.ID)
	}
	// The TIOCGPGRP read happens daemon-side now, because the PTY master fd lives there.
	if err := client.KillForeground(s.ID, int(syscall.SIGKILL)); err != nil {
		return fmt.Errorf("session %s: %w", s.ID, err)
	}
	return nil
}

// WriteInput sends bytes to the session's PTY, which lives in the daemon.
//
// It replaces every `sess.PTY.Write(...)` call site. Those used to reach a file
// descriptor this process owned; now the bytes travel over the protocol. Keeping it as
// one method means there is a single place where "type into this session" is defined,
// rather than four callers each holding an fd.
//
// The attached stream is preferred when there is one — it is the hot path and carries
// raw frames — with the control connection as the fallback for callers that never
// attached (the HTTP input endpoint).
func (s *Session) WriteInput(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	s.mu.Lock()
	stream, client := s.stream, s.mux
	s.mu.Unlock()
	if stream != nil {
		if err := stream.Write(data); err == nil {
			s.touch()
			return nil
		}
		// Fall through to the control connection: a broken stream must not swallow input.
	}
	if client == nil {
		return fmt.Errorf("session %s is not attached to a daemon", s.ID)
	}
	if err := client.Input(s.ID, data); err != nil {
		return err
	}
	s.touch()
	return nil
}

// touch records activity.
func (s *Session) touch() {
	s.mu.Lock()
	s.LastActive = time.Now()
	s.mu.Unlock()
}

// GetStatus returns the session status (thread-safe).
func (s *Session) GetStatus() SessionStatus {
	s.mu.Lock()
	st := s.Status
	s.mu.Unlock()
	return st
}

// ShellPID returns the PID of the shell process running in the PTY.
func (s *Session) ShellPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shellPID
}

// setShellPID records the pid the daemon reported for this session.
func (s *Session) setShellPID(pid int) {
	s.mu.Lock()
	s.shellPID = pid
	s.mu.Unlock()
}

// WorkingDir returns the working directory of the session.
func (s *Session) WorkingDir() string {
	return s.CWD
}

// GetLastActive returns when the session last received PTY output (thread-safe).
func (s *Session) GetLastActive() time.Time {
	s.mu.Lock()
	t := s.LastActive
	s.mu.Unlock()
	return t
}

// TailOutput returns the last n lines of terminal output from the RingBuffer.
// Used by agent intel for output analysis in direct (non-tmux) mode.
func (s *Session) TailOutput(n int) []string {
	if s.Buffer == nil {
		return nil
	}
	// ReadTail: only copy last 4KB, not the entire 1MB buffer.
	// This minimizes mutex hold time and avoids blocking the PTY readLoop.
	raw := s.Buffer.ReadTail(4096)
	if len(raw) == 0 {
		return nil
	}
	text := string(raw)
	// Strip ANSI escape sequences (CSI + OSC).
	text = stripANSIForTail(text)
	lines := splitLines(text)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func stripANSIForTail(s string) string {
	// Simple but effective: remove CSI sequences \x1b[...X and OSC \x1b]...\x07
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) {
			if s[i+1] == '[' {
				// CSI: skip until letter
				j := i + 2
				for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
					j++
				}
				if j < len(s) {
					j++ // skip the final letter
				}
				i = j
				continue
			}
			if s[i+1] == ']' {
				// OSC: skip until BEL or ST
				j := i + 2
				for j < len(s) && s[j] != '\x07' {
					if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				if j < len(s) && s[j] == '\x07' {
					j++
				}
				i = j
				continue
			}
			// Two-character escapes (ESC = / ESC > keypad mode, ESC M reverse index, charset
			// selects like ESC ( B …). Not CSI, not OSC, so the branches above skip them and the
			// bare ESC used to survive into the text — visible as a stray "\x1b=" at the end of a
			// zsh prompt line in the Agent Overview card. Drop the pair (plus the extra byte the
			// charset selectors carry) so a tail is plain text.
			if s[i+1] == '(' || s[i+1] == ')' || s[i+1] == '#' {
				i += 3 // ESC ( B, ESC ) 0, ESC # 8 …
				continue
			}
			i += 2
			continue
		}
		result = append(result, s[i])
		i++
	}
	return string(result)
}

func splitLines(s string) []string {
	var lines []string
	var current []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := string(current)
			if len(line) > 0 {
				lines = append(lines, line)
			}
			current = current[:0]
		} else if s[i] == '\r' {
			// skip
		} else {
			current = append(current, s[i])
		}
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines
}

// WSControlMessage represents a JSON control message on the WebSocket.
// Binary frames carry raw terminal I/O; Text/JSON frames carry control messages.
// [Ref: T5-B3, CAP-terminal-io S3, DDC-02]
type WSControlMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ResizePayload is the payload for a "resize" control message.
// [Ref: T5-B3]
type ResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// ShellExitPayload is the payload for a "shell_exit" control message.
type ShellExitPayload struct {
	ExitCode int `json:"exitCode"`
}

// ErrorPayload is the payload for an "error" control message.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HudLogRequest is the request body for POST /api/cli/debug/logs.
// [Ref: CAP-hud-diagnostics S4]
type HudLogRequest struct {
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	UserAgent string          `json:"userAgent"`
	Screen    json.RawMessage `json:"screen"`
	Events    json.RawMessage `json:"events"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

// Control message type constants.
const (
	MsgTypeResize       = "resize"
	MsgTypePresentation = "presentation" // client → server: visible tab and overview subscription
	MsgTypeHeartbeat    = "heartbeat"
	MsgTypeHeartbeatAck = "heartbeat_ack"
	MsgTypePing         = "ping"
	MsgTypePong         = "pong"
	MsgTypeAuthRefresh  = "auth_refresh"
	MsgTypeShellExit    = "shell_exit"
	MsgTypeError        = "error"
	MsgTypePreempted    = "preempted"
	MsgTypeInput        = "input"        // client → server: terminal input as text frame (WKWebView binary frame fix)
	MsgTypeTmuxNav      = "tmux_nav"     // client → server: navigate tmux windows/sessions
	MsgTypeSessionMeta  = "session_meta" // server → client: pushed once after WS handshake
	MsgTypeAgentState   = "agent_state"  // server → client: agent state push (replaces SSE)
	MsgTypeTmuxState    = "tmux_state"   // server → client: tmux topology/prefix/agent-status push (terminal-owned)
	// server → client: every session's status + live tail, for the NON-tmux Agent Overview.
	// Structural twin of tmux_state — one frame describing all units, pushed on the active
	// session's existing WS by the same 1s diff-suppressed ticker. See sessions_overview.go.
	MsgTypeSessionsOverview = "sessions_overview"
	// server → client: the sessions whose program EXPLICITLY asked for the user (BEL / OSC
	// notification). Same one-frame-describes-everything shape as sessions_overview, and for
	// the same reason: a bell can ring in a background session that has no WebSocket of its
	// own. See session_signal.go.
	MsgTypeAgentSignal = "agent_signal"
	// server → client: the session's grid is now this big. NOT an echo of the client's own
	// resize — it is the size the session actually entered after fitting every window
	// watching it, which is a different number whenever a smaller window is also attached.
	// Sent once before the replay (so the replay lands on the right grid) and then whenever
	// the size changes, in stream order with the output it applies to.
	MsgTypeResized = "resized"

	// MsgTypeReplayReset tells a client to clear its terminal because what follows is a
	// REPLAY — the session's screen from the beginning of the current grid epoch — not a
	// continuation of what it is already showing.
	//
	// Without it a browser that switches away and back writes a second copy of the screen
	// underneath the first, and the two copies are what a user reports as "duplicated bottom
	// line". The frames are indistinguishable once they arrive (replay and live output are
	// both binary), so the boundary has to be stated, not inferred.
	//
	// A client that does not know this type ignores it and keeps the old behaviour, which is
	// why it is a separate frame rather than a field on "resized": an old page must not have
	// its resize silently changed shape underneath it.
	MsgTypeReplayReset = "replay_reset"
)

// ResizedPayload is the payload of a "resized" control message.
type ResizedPayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// AgentSignalEntry is one session's currently-unanswered explicit signal.
type AgentSignalEntry struct {
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`            // "bell" | "notify"
	Title     string `json:"title,omitempty"` // OSC notifications only
	Body      string `json:"body,omitempty"`  // OSC notifications only
	At        string `json:"at"`              // RFC3339 (ms precision) when it arrived
	Seq       uint64 `json:"seq"`             // per-session counter; a new value = a NEW signal
}

// AgentSignalPayload is the payload of an "agent_signal" control message. Signals is the
// COMPLETE current set (never a delta), so an empty array is the explicit "nothing pending
// anymore" that clears the client's state.
type AgentSignalPayload struct {
	Signals []AgentSignalEntry `json:"signals"`
}

// AgentStatePushFunc subscribes to agent state changes for a session.
// Returns a channel of JSON-encoded AgentIntelResponse and a cleanup function.
// Injected by the webui layer to avoid terminal → agent_intel import cycle.
type AgentStatePushFunc func(ctx context.Context, sessionID string) (<-chan json.RawMessage, func(), error)

// InputPayload carries terminal input bytes as a JSON text frame.
// [TH-0501-m9j] WKWebView drops rapid binary WS frames; text frames are reliable.
type InputPayload struct {
	Data []byte `json:"data"` // raw terminal bytes (JSON base64-encoded)
}

// TmuxNavPayload is the payload for a "tmux_nav" control message.
// The backend silently ignores the action when TmuxDetected=false.
type TmuxNavPayload struct {
	Action string `json:"action"` // "window_next"|"window_prev"|"session_next"|"session_prev"
}

// SessionMetaPayload is pushed to the client once after the WS replay buffer is sent.
// The client uses TmuxDetected to decide whether to show tmux gesture hints.
type SessionMetaPayload struct {
	TmuxDetected bool `json:"tmux_detected"`
}

// PreemptedPayload is the payload for a "preempted" control message.
type PreemptedPayload struct {
	Message string `json:"message"`
}
