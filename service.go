package terminal

import (
	"context"
	"io"
)

// SessionInfo is the service-layer representation of a terminal session.
// It avoids exposing the internal Session struct (with PTY, Cmd, etc.)
// to callers outside the package.
type SessionInfo struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Title        string        `json:"title,omitempty"`
	Engine       string        `json:"engine"`
	CWD          string        `json:"cwd"`
	Status       SessionStatus `json:"status"`
	CreatedAt    string        `json:"created_at"`
	LastActive   string        `json:"last_active"`
	ShellPID     int           `json:"shell_pid,omitempty"`
	TmuxDetected bool          `json:"tmux_detected,omitempty"`
	ExitCode     int           `json:"exit_code,omitempty"`
}

// TerminalSessionService is the service-layer interface for terminal session management.
// Implementations: InProcessService (delegates to SessionManager directly) or a
// future RemoteService (proxies to a TerminalMuxHost over HTTP).
// The webui layer and tests depend only on this interface, enabling the underlying
// transport to be swapped without touching callers.
type TerminalSessionService interface {
	// List returns all active sessions.
	List(ctx context.Context) ([]SessionInfo, error)

	// Create starts a new PTY-backed session with the given options.
	Create(ctx context.Context, opts CreateOptions) (*SessionInfo, error)

	// Get returns the session info for the given ID.
	// Returns an error wrapping "not found" when the session does not exist.
	Get(ctx context.Context, id string) (*SessionInfo, error)

	// Delete destroys the session identified by id, killing the PTY process.
	Delete(ctx context.Context, id string) error

	// Resize sets the terminal window size for the session.
	Resize(ctx context.Context, id string, cols, rows int) error

	// Input writes raw terminal bytes to the session PTY.
	// Used by the HTTP fallback path (WKWebView POST /sessions/:id/input).
	Input(ctx context.Context, id string, data []byte) error

	// PasteUpload saves clipboard content to a temp file under the session CWD
	// and returns the absolute path.  filename is the original filename hint;
	// content is the raw file body; sessionCWD is the working directory to use
	// (pass empty string to use the session's own CWD).
	PasteUpload(ctx context.Context, id string, filename string, content io.Reader, sessionCWD string) (string, error)

	// TailOutput returns the last n lines of terminal output for agent intel.
	TailOutput(ctx context.Context, id string, lines int) ([]string, error)

	// Close shuts down the service and destroys all sessions.
	Close() error
}
