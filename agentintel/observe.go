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
	tmuxProbeLogs    = obs.NewLogCoalescer(30 * time.Second)
)

// slowTmuxProbe is the line between "a poll" and "a stall the user can feel". The tmux topology
// probe is on a 1s cycle, so anything past this fraction of that cycle means the next tick starts
// late — and before the producer was split off the WS writer (handlers.go), it also meant that
// many milliseconds of keystroke-echo lag. Deliberately well under 1s so the log fires while the
// problem is still "slow", not once it is already "broken".
const slowTmuxProbe = 300 * time.Millisecond

// Metrics — incremented by agent_intel_routes.go.
var (
	DetectTotal                 = obs.NewCounter("agent_intel_detect_total")
	DetectDuration              = obs.NewHistogram("agent_intel_detect_duration_seconds", obs.DefaultBuckets())
	AgentIntelWatchersActive    = obs.NewGauge("agent_intel_watchers_active")
	AgentIntelSubscribersActive = obs.NewGauge("agent_intel_subscribers_active")
	AgentIntelStatePushTotal    = obs.NewCounter("agent_intel_state_push_total")
	AgentIntelStateDropTotal    = obs.NewCounter("agent_intel_state_drop_total")
	AgentIntelWatcherErrors     = obs.NewCounter("agent_intel_watcher_errors_total")

	// TmuxProbeDuration is how long ONE topology rebuild took. This is the number that had to be
	// measured with a throwaway benchmark to find the lag that shipped for months; keeping it here
	// means the next regression shows up as a histogram rather than as a user saying "it's slow".
	TmuxProbeDuration = obs.NewHistogram("tmux_probe_duration_seconds", obs.DefaultBuckets())
	// TmuxProbeTotal counts rebuilds; compared against TmuxProbeServedTotal it shows how much work
	// the shared memo is actually saving (served = answered from cache, no tmux commands at all).
	TmuxProbeTotal       = obs.NewCounter("tmux_probe_total")
	TmuxProbeServedTotal = obs.NewCounter("tmux_probe_served_from_cache_total")
	// TmuxProbeDeferredTotal counts rebuilds skipped because the user was mid-interaction. A
	// healthy number is non-zero while someone is typing and flat when nobody is: sustained growth
	// on an idle machine would mean the interaction clock is being set by something that is not a
	// user (see NoteInteraction's call sites).
	TmuxProbeDeferredTotal = obs.NewCounter("tmux_probe_deferred_for_interaction_total")
)

// LogTmuxProbe records the cost of one topology rebuild, and says so out loud when it crosses
// slowTmuxProbe. Coalesced on the "is it slow" verdict so a persistently slow machine logs once
// per interval instead of once per second, while the transition into slow is immediate.
func LogTmuxProbe(ctx context.Context, elapsed time.Duration, panes, windows int) {
	TmuxProbeTotal.Inc()
	TmuxProbeDuration.Observe(elapsed.Seconds())
	slow := elapsed >= slowTmuxProbe
	if !slow {
		return
	}
	tmuxProbeLogs.Info(ctx, Logger, "tmux-probe", "slow", "tmux topology probe is slow",
		"elapsed_ms", elapsed.Milliseconds(),
		"threshold_ms", slowTmuxProbe.Milliseconds(),
		"panes", panes,
		"windows", windows,
		// The two things that actually drive the cost, so the log names its own cause.
		"overview_tail_capture", "see SetOverviewActive",
		"note", "each window's tail capture is a separate tmux command on a single-threaded server")
}

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
