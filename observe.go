// Package terminal — observability declarations (BS-08 Terminal).
package terminal

import "github.com/brightman-ai/kit/obs"

// STG constants for terminal session lifecycle phases.
const (
	stgTerminalSpawn     = "terminal/spawn"
	stgTerminalAttach    = "terminal/attach"
	stgTerminalDetach    = "terminal/detach"
	stgTerminalTerminate = "terminal/terminate"
	stgTerminalClipboard = "terminal/clipboard"
	stgTerminalInput     = "terminal/input"
	stgTerminalOutput    = "terminal/output"
)

// Package-level logger.
var terminalLogger = obs.Module("terminal")

// Terminal session metrics.
var (
	terminalActive                  = obs.NewGauge("terminal_active")
	terminalSpawnTotal              = obs.NewCounter("terminal_spawn_total")
	terminalErrorsTotal             = obs.NewCounter("terminal_errors_total")
	terminalDuration                = obs.NewHistogram("terminal_duration_seconds", obs.DefaultBuckets())
	terminalClipboardUploadsTotal   = obs.NewCounter("terminal_clipboard_uploads_total")
	terminalClipboardUploadErrors   = obs.NewCounter("terminal_clipboard_upload_errors_total")
	terminalClipboardUploadBytes    = obs.NewCounter("terminal_clipboard_upload_bytes_total")
	terminalClipboardUploadDuration = obs.NewHistogram("terminal_clipboard_upload_duration_seconds", obs.DefaultBuckets())
	// The zero-copy half of paste (clipboard_native.go). Instrumented from the first commit
	// on purpose: the bug this endpoint fixes was invisible for weeks because the ONLY
	// signal that the native path had failed was a 404 buried in the client log, after
	// which the upload fallback quietly did the wrong thing. probes-vs-hits is the ratio
	// that says whether zero-copy is actually carrying local pastes; rejected/errors say
	// why when it isn't.
	terminalClipboardNativeProbes   = obs.NewCounter("terminal_clipboard_native_probes_total")
	terminalClipboardNativeHits     = obs.NewCounter("terminal_clipboard_native_hits_total")
	terminalClipboardNativeRejected = obs.NewCounter("terminal_clipboard_native_rejected_total")
	terminalClipboardNativeErrors   = obs.NewCounter("terminal_clipboard_native_errors_total")
	terminalClipboardNativeDuration = obs.NewHistogram("terminal_clipboard_native_duration_seconds", obs.DefaultBuckets())
	// How much of an upload was slowed down to keep the terminal responsive (upload_pacing.go).
	// Zero on an idle machine by construction; a rising paced_bytes with a flat upload count is
	// the signal that someone is pasting large files WHILE working, which is the only situation
	// the pacer is supposed to exist in. Without these, a mysteriously slow upload would have no
	// way to be attributed to the mechanism that deliberately slowed it.
	terminalUploadPacedBytes   = obs.NewCounter("terminal_upload_paced_bytes_total")
	terminalUploadPacedDelay   = obs.NewHistogram("terminal_upload_paced_delay_seconds", obs.DefaultBuckets())
	terminalInputFramesTotal   = obs.NewCounter("terminal_input_frames_total")
	terminalInputBytesTotal    = obs.NewCounter("terminal_input_bytes_total")
	terminalInputSubmitsTotal  = obs.NewCounter("terminal_input_submits_total")
	terminalOutputFramesTotal  = obs.NewCounter("terminal_output_frames_total")
	terminalOutputBytesTotal   = obs.NewCounter("terminal_output_bytes_total")
	terminalWSConnectionsTotal = obs.NewCounter("terminal_ws_connections_total")
	terminalWSPreemptionsTotal = obs.NewCounter("terminal_ws_preemptions_total")
	terminalWSReplayBytesTotal = obs.NewCounter("terminal_ws_replay_bytes_total")
	// A status frame (tmux_state / sessions_overview / agent_signal) superseded before the writer
	// could send it. Expected to be non-zero on a machine where the tmux probe overruns its 1s
	// tick; alarming only if it climbs while the UI's dashboards look stale, which would mean the
	// writer itself is stuck rather than the producer being fast.
	terminalStatusFramesDroppedTotal = obs.NewCounter("terminal_status_frames_dropped_total")

	// The Agent Overview's per-session screen replay, split by whether it had to be done.
	//
	// These two are the readable form of「变化才有代价」on the non-tmux side. A card's screen is a
	// replay of up to 128 KiB of the ring onto a full grid, and it used to run for EVERY session
	// EVERY second, whether or not that session had emitted a single byte — the cost tracked WALL
	// CLOCK, which is exactly the shape that gets worse the more terminals you keep open and the
	// less any of them is doing. Reuses should dominate on a quiet machine; renders should track
	// output. Reuses staying at zero while the machine is idle means the marker they key on
	// (RingBuffer.Seq) has stopped being trustworthy, and that is worth knowing before the CPU bill
	// is what tells you.
	terminalOverviewScreenRenderTotal = obs.NewCounter("terminal_overview_screen_render_total")
	terminalOverviewScreenReuseTotal  = obs.NewCounter("terminal_overview_screen_reuse_total")

	// A caller that arrived while a rebuild was already in flight and waited for ITS result instead
	// of starting a second one. Every one of these used to be a duplicate full rebuild: the cache
	// released its lock before building, so N connections ticking together all missed together.
	terminalOverviewRebuildSharedTotal = obs.NewCounter("terminal_overview_rebuild_shared_total")
)
