package terminal

import "context"

// Hooks defines optional integration points for a host application.
// All fields are optional — nil means standalone behavior.
type Hooks struct {
	OnSessionStart func(ctx context.Context, id string, meta SessionMeta) error
	OnSessionEnd   func(ctx context.Context, id string, exitCode int) error
	OnOutput       func(ctx context.Context, id string, data []byte) error
	OnCommand      func(ctx context.Context, id string, cmd string) (handled bool, err error)
	Store          SessionStore

	// AgentDetect optionally enriches session list with agent tool/status.
	// Injected by the host to avoid import cycles.
	AgentDetect AgentDetectFunc

	// AgentStatePush optionally subscribes to agent state changes for WS push.
	// Injected by the host to avoid import cycles.
	AgentStatePush AgentStatePushFunc
}

// AgentDetectFunc detects an AI agent running under the given shell PID.
// Returns (tool, status) strings; empty tool means no agent detected.
type AgentDetectFunc func(ctx context.Context, shellPID int, cwd string) (tool, status string)

// SessionMeta is passed to OnSessionStart.
type SessionMeta struct {
	Shell string
	CWD   string
}

// SessionStore is optional persistence. nil = in-memory only (data lost on restart).
type SessionStore interface {
	SaveSession(ctx context.Context, s *SessionSnapshot) error
	ListSessions(ctx context.Context) ([]SessionSummary, error)
}

// SessionSnapshot is a point-in-time session state for persistence.
type SessionSnapshot struct {
	ID     string
	Name   string
	Shell  string
	CWD    string
	Status string
}

// SessionSummary is a brief session descriptor.
type SessionSummary struct {
	ID     string
	Name   string
	Status string
}
