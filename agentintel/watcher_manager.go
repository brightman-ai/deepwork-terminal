package agentintel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SessionActivity is the host-agnostic view the monitor needs of one live
// session. It is the only seam between this transport- and session-model-
// agnostic engine and whatever session model a host runs (a terminal PTY
// session, a remote mux session, a test fake). Implementors expose just enough
// for JSONL-based agent-state detection: where the agent runs (CWD/Engine) and
// how the terminal is behaving (last activity + recent output tail).
type SessionActivity interface {
	// WorkingDir is the session's working directory, used to locate the
	// agent's JSONL transcript.
	WorkingDir() string

	// Engine names the agent CLI driving the session ("claude", "codex", …);
	// it selects the JSONL driver. Empty means no known agent.
	Engine() string

	// LastActivity returns the most recent time the terminal produced output.
	LastActivity() time.Time

	// TailLines returns the last n lines of visible terminal output.
	TailLines(n int) []string
}

// SessionActivityGetter resolves a session ID to its live activity view.
// The second return is false when the session is unknown. A host supplies this
// to drive the JSONL-watcher path; pass nil to use the resolver path instead.
type SessionActivityGetter func(ctx context.Context, sessionID string) (SessionActivity, bool)

// sessionActivityPTYSource adapts a SessionActivity to PTYIdleSource so the
// JSONL watcher can supplement transcript state with terminal-reality checks.
type sessionActivityPTYSource struct {
	activity SessionActivity
}

func (s *sessionActivityPTYSource) LastActivity() time.Time {
	if s == nil || s.activity == nil {
		return time.Time{}
	}
	return s.activity.LastActivity()
}

func (s *sessionActivityPTYSource) TailLines(n int) []string {
	if s == nil || s.activity == nil {
		return nil
	}
	return s.activity.TailLines(n)
}

type managedWatcher struct {
	watcher agentStateBroadcaster
	refs    int
}

type agentStateBroadcaster interface {
	Start(context.Context)
	Stop()
	subscribe() (<-chan AgentIntelResponse, func())
}

// AgentIntelMonitorManager owns the per-session agent-state watchers behind a
// streaming transport. It ref-counts watchers so concurrent subscribers to one
// session share a single watcher, torn down when the last subscriber leaves.
//
// It runs in one of two modes, chosen at construction:
//   - resolver mode: a SnapshotWatcher polls an AgentStateResolver (tmux/process
//     aware) — the host owns state resolution end to end.
//   - session mode: a JSONL AgentStateWatcher tails the agent transcript located
//     from a SessionActivity, with the terminal output as an idle source.
type AgentIntelMonitorManager struct {
	getter   SessionActivityGetter
	locator  *ProjectLocator
	resolver AgentStateResolver

	mu       sync.Mutex
	watchers map[string]*managedWatcher
}

// NewAgentIntelMonitorManager builds a manager in session mode over getter.
// Pass a nil getter only when you also intend the resolver path (prefer
// NewAgentIntelMonitorManagerWithResolver for that).
func NewAgentIntelMonitorManager(getter SessionActivityGetter, locator *ProjectLocator) *AgentIntelMonitorManager {
	return NewAgentIntelMonitorManagerWithResolver(getter, locator, nil)
}

// NewAgentIntelMonitorManagerWithResolver builds a manager. When resolver is
// non-nil it takes precedence (resolver mode) and getter may be nil; otherwise
// getter drives session mode. locator defaults when nil.
func NewAgentIntelMonitorManagerWithResolver(
	getter SessionActivityGetter,
	locator *ProjectLocator,
	resolver AgentStateResolver,
) *AgentIntelMonitorManager {
	if locator == nil {
		locator = NewProjectLocator()
	}
	return &AgentIntelMonitorManager{
		getter:   getter,
		locator:  locator,
		resolver: resolver,
		watchers: make(map[string]*managedWatcher),
	}
}

// Subscribe returns a channel of state changes for sessionID. The release
// function is idempotent and stops the watcher when the last subscriber leaves.
func (m *AgentIntelMonitorManager) Subscribe(ctx context.Context, sessionID string) (<-chan AgentIntelResponse, func(), error) {
	if sessionID == "" {
		return nil, nil, fmt.Errorf("missing session id")
	}
	if m == nil || (m.getter == nil && m.resolver == nil) {
		return nil, nil, fmt.Errorf("agent intel monitor manager not configured")
	}

	var activity SessionActivity
	if m.resolver == nil {
		var ok bool
		activity, ok = m.getter(ctx, sessionID)
		if !ok {
			return nil, nil, fmt.Errorf("session %q not found", sessionID)
		}
	}

	m.mu.Lock()
	entry, ok := m.watchers[sessionID]
	if !ok {
		var watcher agentStateBroadcaster
		if m.resolver != nil {
			watcher = NewAgentSnapshotWatcher(sessionID, m.resolver)
		} else {
			jsonlWatcher := NewAgentStateWatcher(sessionID, activity.WorkingDir(), toolFromEngine(activity.Engine()), m.locator)
			jsonlWatcher.SetPTYIdleSource(&sessionActivityPTYSource{activity: activity})
			watcher = jsonlWatcher
		}
		watcher.Start(context.Background())
		entry = &managedWatcher{watcher: watcher}
		m.watchers[sessionID] = entry
		AgentIntelWatchersActive.Add(1)
	}
	entry.refs++
	ch, watcherRelease := entry.watcher.subscribe()
	m.mu.Unlock()

	var once sync.Once
	done := make(chan struct{})
	release := func() {
		once.Do(func() {
			close(done)
			watcherRelease()
			var stopWatcher agentStateBroadcaster

			m.mu.Lock()
			if cur, exists := m.watchers[sessionID]; exists {
				cur.refs--
				if cur.refs <= 0 {
					stopWatcher = cur.watcher
					delete(m.watchers, sessionID)
					AgentIntelWatchersActive.Sub(1)
				}
			}
			m.mu.Unlock()

			if stopWatcher != nil {
				stopWatcher.Stop()
			}
		})
	}

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				release()
			case <-done:
			}
		}()
	}

	return ch, release, nil
}

// toolFromEngine maps a session's engine label to the JSONL driver tool.
func toolFromEngine(engine string) AgentTool {
	switch e := strings.ToLower(strings.TrimSpace(engine)); {
	case strings.Contains(e, "claude"):
		return ToolClaude
	case strings.Contains(e, "codex"):
		return ToolCodex
	default:
		return ToolNone
	}
}
