package agentintel

import "context"

// AgentStateResolver produces the full agent-intel response for one session.
// It is the generic seam between a host's session model and the snapshot
// watcher: the host supplies a resolver that knows how to translate a session
// ID into tmux/process state, and AgentSnapshotWatcher polls it.
//
// The orchestration that drives watchers (AgentIntelMonitorManager) lives in
// this package too — decoupled from any host via the SessionActivity seam — so
// the engine is self-contained with no import cycle when a host depends on it.
type AgentStateResolver func(ctx context.Context, sessionID string) (AgentIntelResponse, error)
