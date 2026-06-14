package agentintel

import (
	"context"
	"fmt"
	"time"

	"github.com/brightman-ai/kit/obs"
)

// STG constants for agent-intel operations.
const (
	StgAgentDetect = "agent-intel/detect"
	StgAgentResult = "agent-intel/result"
	StgAgentStream = "agent-intel/stream"
)

// Logger is the obs-native logger for agent-intel, exported for routes to use.
var Logger = obs.Module("agent-intel")

var (
	stateResolveLogs = obs.NewLogCoalescer(30 * time.Second)
	tmuxScanLogs     = obs.NewLogCoalescer(30 * time.Second)
)

// Metrics — incremented by agent_intel_routes.go.
var (
	DetectTotal                 = obs.NewCounter("agent_intel_detect_total")
	DetectDuration              = obs.NewHistogram("agent_intel_detect_duration_seconds", obs.DefaultBuckets())
	AgentIntelWatchersActive    = obs.NewGauge("agent_intel_watchers_active")
	AgentIntelSubscribersActive = obs.NewGauge("agent_intel_subscribers_active")
	AgentIntelStatePushTotal    = obs.NewCounter("agent_intel_state_push_total")
	AgentIntelStateDropTotal    = obs.NewCounter("agent_intel_state_drop_total")
	AgentIntelWatcherErrors     = obs.NewCounter("agent_intel_watcher_errors_total")
)

// LogStateResolved records high-frequency state probes without flooding TS-OBS.
// It emits immediately when the semantic state changes, otherwise coalesces
// repeated polling observations into one interval summary.
func LogStateResolved(ctx context.Context, sessionID, mode string, state AgentState, elapsed time.Duration) {
	key := sessionID + "|" + mode
	fingerprint := fmt.Sprintf("%s|%s|%s|%s|%s", mode, state.Tool, state.Status, state.WaitReason, state.SignalSource)
	stateResolveLogs.Info(ctx, Logger, key, fingerprint, "agent-state resolved",
		"session_id", sessionID,
		"mode", mode,
		"tool", string(state.Tool),
		"status", string(state.Status),
		"wait_reason", string(state.WaitReason),
		"signal_source", state.SignalSource,
		"tokens", state.TotalTokens,
		"elapsed_ms", elapsed.Milliseconds())
}

// LogTmuxScanComplete is the tmux equivalent of LogStateResolved: topology
// changes are emitted immediately; unchanged scans are summarized per window.
func LogTmuxScanComplete(ctx context.Context, sessionID, sessionName string, panes, agents int) {
	key := sessionID + "|tmux|" + sessionName
	fingerprint := fmt.Sprintf("%s|%d|%d", sessionName, panes, agents)
	tmuxScanLogs.Info(ctx, Logger, key, fingerprint, "tmux scan complete",
		"session_id", sessionID,
		"session", sessionName,
		"panes", panes,
		"agents", agents)
}
