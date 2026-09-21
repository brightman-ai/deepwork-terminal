package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// The tab/card and agent-state feed must use the same process-bound transcript.
// A separate cwd-newest watcher can report idle (and another model's token totals)
// while the tab's actual runtime is working in a nested shell's different directory.
func (s *Server) newAgentIntelMonitor() *agentintel.AgentIntelMonitorManager {
	return agentintel.NewAgentIntelMonitorManagerWithResolver(nil, nil, s.sessionAgentSnapshot)
}

func (s *Server) sessionAgentSnapshot(ctx context.Context, sessionID string) (agentintel.AgentIntelResponse, error) {
	resp := agentintel.AgentIntelResponse{Notifications: []agentintel.AgentState{}}
	if _, err := s.mgr.Get(sessionID); err != nil {
		return resp, err
	}
	for _, entry := range s.overviewSnapshot(ctx).entries {
		if entry.ID != sessionID || entry.AgentTool == agentintel.ToolNone {
			continue
		}
		state := s.sessionAgent.monitor.Snapshot(sessionID)
		state.Tool = entry.AgentTool
		state.Status = entry.AgentStatus
		state.AwaitingUser = entry.AwaitingUser
		state.AwaitingSince = entry.AwaitingSince
		state.EndedOnQuestion = entry.EndedOnQuestion
		if state.Status == agentintel.StatusRunning {
			state.WaitReason = agentintel.WaitNone
		}
		resp.Current = &state
		if state.AwaitingUser || state.Status == agentintel.StatusWaiting {
			resp.Notifications = append(resp.Notifications, state)
		}
		break
	}
	return resp, nil
}

// agentStateSnapshotWait bounds how long GET /sessions/{id}/agent-state waits for a first
// value. A watcher that is already running (the WS for this session subscribed moments
// ago) replays its last response the instant a new subscriber attaches, so the normal cost
// here is microseconds; the wait only matters for a session whose watcher is starting cold,
// and even then the WS push will deliver the state anyway. So this is short on purpose —
// holding the request open longer buys a snapshot the client is about to receive for free.
const agentStateSnapshotWait = time.Second

// handleAgentStateSnapshot serves GET /sessions/{id}/agent-state.
//
// The shared frontend fetches this once on mount (useAgentIntel's fetchSnapshot) to fill
// the UI before the first WS agent_state frame arrives. deepwork-pro serves it; standalone
// deepwork-terminal did not, so it 404'd — invisibly, because the caller wraps it in
// `catch { /* endpoint may not exist yet */ }`. The cost was small and permanent: every
// freshly opened session showed no agent state until something changed enough to push.
//
// It reads through s.hooks.AgentStatePush rather than reaching for the agentintel monitor
// directly, so it answers from whichever provider is actually in force — the native monitor
// standalone, or a host-injected push when embedded. One source, no second opinion.
func (s *Server) handleAgentStateSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}
	if s.hooks.AgentStatePush == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent state not available"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), agentStateSnapshotWait)
	defer cancel()
	ch, release, err := s.hooks.AgentStatePush(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	defer release()

	select {
	case raw, ok := <-ch:
		if ok && len(raw) > 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
	case <-ctx.Done():
	}
	// No state yet is a normal answer, not an error: an agent that has not spoken has no
	// state to report. The empty response is the same shape the stream sends, so the client
	// applies it through exactly one code path.
	writeJSON(w, http.StatusOK, agentintel.AgentIntelResponse{Notifications: []agentintel.AgentState{}})
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
