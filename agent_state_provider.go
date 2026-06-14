package terminal

import (
	"context"
	"encoding/json"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// sessionActivity adapts a live *Session to agentintel.SessionActivity, the
// host-agnostic view the agent-intel monitor needs of a session. It is the only
// shim between terminal's session model and the decoupled agentintel engine.
type sessionActivity struct {
	sess *Session
}

func (a sessionActivity) WorkingDir() string       { return a.sess.WorkingDir() }
func (a sessionActivity) Engine() string           { return a.sess.Engine }
func (a sessionActivity) LastActivity() time.Time  { return a.sess.GetLastActive() }
func (a sessionActivity) TailLines(n int) []string { return a.sess.TailOutput(n) }

// sessionActivityGetter exposes the manager's live sessions to agentintel.
func (m *SessionManager) sessionActivityGetter() agentintel.SessionActivityGetter {
	return func(_ context.Context, sessionID string) (agentintel.SessionActivity, bool) {
		sess, err := m.Get(sessionID)
		if err != nil {
			return nil, false
		}
		return sessionActivity{sess: sess}, true
	}
}

// newAgentIntelMonitor builds an agentintel monitor over this server's live
// sessions (JSONL session mode). It is the native default that powers the
// standalone agent-state WS push when no host injects Hooks.AgentStatePush.
func (s *Server) newAgentIntelMonitor() *agentintel.AgentIntelMonitorManager {
	return agentintel.NewAgentIntelMonitorManager(
		s.mgr.sessionActivityGetter(),
		agentintel.NewProjectLocator(),
	)
}

// nativeAgentStatePush adapts the agentintel monitor's typed response stream to
// the AgentStatePushFunc contract (a channel of JSON-encoded responses) used by
// the WS writer. A host-injected Hooks.AgentStatePush always takes precedence;
// this is the standalone fallback.
func nativeAgentStatePush(mon *agentintel.AgentIntelMonitorManager) AgentStatePushFunc {
	return func(ctx context.Context, sessionID string) (<-chan json.RawMessage, func(), error) {
		src, release, err := mon.Subscribe(ctx, sessionID)
		if err != nil {
			return nil, nil, err
		}
		out := make(chan json.RawMessage, cap(src))
		go func() {
			defer close(out)
			for resp := range src {
				raw, err := json.Marshal(resp)
				if err != nil {
					continue
				}
				select {
				case out <- raw:
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, release, nil
	}
}
