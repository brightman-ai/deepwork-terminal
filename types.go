// Package terminal implements the BS-08 Terminal subsystem.
// It provides PTY management, WebSocket-based terminal I/O, and session lifecycle
// for browser-based terminal access. All state is held in memory (IR-03).
package terminal

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"
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

	// exitCode stores the shell exit code once the process exits.
	exitCode int

	// TmuxDetected indicates whether the shell is running inside tmux.
	// Set after session creation by checking /proc/{pid}/environ.
	// [Ref: BUG-6, DDC-13]
	TmuxDetected bool `json:"tmuxDetected"`

	mu sync.Mutex // protects Status, LastActive, exitCode, TmuxDetected
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
)

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
