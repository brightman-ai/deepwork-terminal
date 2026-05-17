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
	terminalInputFramesTotal        = obs.NewCounter("terminal_input_frames_total")
	terminalInputBytesTotal         = obs.NewCounter("terminal_input_bytes_total")
	terminalInputSubmitsTotal       = obs.NewCounter("terminal_input_submits_total")
	terminalOutputFramesTotal       = obs.NewCounter("terminal_output_frames_total")
	terminalOutputBytesTotal        = obs.NewCounter("terminal_output_bytes_total")
	terminalWSConnectionsTotal      = obs.NewCounter("terminal_ws_connections_total")
	terminalWSPreemptionsTotal      = obs.NewCounter("terminal_ws_preemptions_total")
	terminalWSReplayBytesTotal      = obs.NewCounter("terminal_ws_replay_bytes_total")
)
