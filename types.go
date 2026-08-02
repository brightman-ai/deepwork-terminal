// Package terminal implements the BS-08 Terminal subsystem.
// It provides PTY management, WebSocket-based terminal I/O, and session lifecycle
// for browser-based terminal access. All state is held in memory (IR-03).
package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/brightman-ai/deepwork-terminal/ansisignal"
	"github.com/creack/pty"
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
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Title      string        `json:"title"`
	Engine     string        `json:"engine"`
	CWD        string        `json:"cwd"`
	ShellPath  string        `json:"-"`
	PTY        *os.File      `json:"-"`
	Cmd        *exec.Cmd     `json:"-"`
	Buffer     *RingBuffer   `json:"-"`
	Status     SessionStatus `json:"status"`
	CreatedAt  time.Time     `json:"createdAt"`
	LastActive time.Time     `json:"lastActive"`

	// subscribers holds active WebSocket connections for this session.
	// Protected by subMu.
	subscribers map[string]chan []byte
	subMu       sync.RWMutex

	// done is closed when the PTY read loop exits (shell exited or error).
	done     chan struct{}
	doneOnce sync.Once

	// waitOnce guards the single legitimate call to Cmd.Wait — see Session.reap.
	waitOnce sync.Once

	// exitCode stores the shell exit code once the process exits.
	exitCode int

	// TmuxDetected indicates whether the shell is running inside tmux.
	// Set after session creation by checking /proc/{pid}/environ.
	// [Ref: BUG-6, DDC-13]
	TmuxDetected bool `json:"tmuxDetected"`

	// ptyCols/ptyRows are the PTY's CURRENT window size — the size the program on the other
	// end believes it is drawing into. Seeded from spawnPTY's initial winsize and updated by
	// SetPTYSize on every resize.
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
	ptyCols int
	ptyRows int

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

	mu sync.Mutex // protects Status, LastActive, exitCode, TmuxDetected, lastSignal*, ptyCols/ptyRows
}

// SetPTYSize resizes the PTY **and** records the new size on the session.
//
// The two halves are one operation on purpose. Every resize path used to call pty.Setsize
// directly and drop the numbers on the floor (three call sites: the REST handler, the WS
// control message, and InProcessService.Resize), which is how the screen replay ended up
// guessing. Making the setter own both means a fourth resize path cannot forget the second
// half — there is no way to change the PTY's size without the session learning it.
//
// Bounds are the caller's business (all three already reject <1 or >500); this only refuses
// obvious nonsense so a bad value can't poison the replay grid.
func (s *Session) SetPTYSize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return fmt.Errorf("pty size: cols/rows must be positive (%d×%d)", cols, rows)
	}
	s.mu.Lock()
	ptyFile := s.PTY
	s.mu.Unlock()
	if ptyFile == nil {
		return fmt.Errorf("session %s has no PTY", s.ID)
	}
	if err := pty.Setsize(ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return err
	}
	s.mu.Lock()
	s.ptyCols, s.ptyRows = cols, rows
	s.mu.Unlock()
	return nil
}

// PTYSize returns the PTY's current window size. Falls back to the spawn size when a session
// predates any resize — never zero, because the replay grid must always have dimensions.
func (s *Session) PTYSize() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptyCols > 0 && s.ptyRows > 0 {
		return s.ptyCols, s.ptyRows
	}
	return spawnCols, spawnRows
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

// reap waits for the shell process, exactly once.
//
// os/exec.Cmd.Wait is NOT safe for concurrent use, and two callers legitimately want to reap:
// readLoop (the PTY hit EOF on its own) and Destroy (we killed it deliberately). Racing them
// corrupts ProcessState and double-closes the same descriptors — `go test -race` caught it.
//
// sync.Once doubles as the barrier that makes this correct rather than merely deduplicated:
// Once.Do guarantees no call returns until the single call to f has returned, so whichever
// caller loses the race still blocks until Wait is done and may safely read ProcessState after.
func (s *Session) reap() {
	if s.Cmd == nil {
		return
	}
	s.waitOnce.Do(func() { _ = s.Cmd.Wait() })
}

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

// GetStatus returns the session status (thread-safe).
func (s *Session) GetStatus() SessionStatus {
	s.mu.Lock()
	st := s.Status
	s.mu.Unlock()
	return st
}

// ShellPID returns the PID of the shell process running in the PTY.
func (s *Session) ShellPID() int {
	if s.Cmd != nil && s.Cmd.Process != nil {
		return s.Cmd.Process.Pid
	}
	return 0
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
)

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
