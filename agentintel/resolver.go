package agentintel

import "context"

// AgentStateResolver produces the full agent-intel response for one session.
// It is the generic seam between a host's session model and the snapshot
// watcher: the host supplies a resolver that knows how to translate a session
// ID into tmux/process state, and AgentSnapshotWatcher polls it.
//
// The richer pro orchestration (a monitor manager that adapts a terminal
// SessionInfo getter into a resolver) lives in the host repo to keep this
// package free of any host import — avoiding an import cycle when the host
// depends on agentintel.
type AgentStateResolver func(ctx context.Context, sessionID string) (AgentIntelResponse, error)
