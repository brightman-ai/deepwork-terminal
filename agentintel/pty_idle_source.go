package agentintel

import "time"

// PTYIdleSource provides terminal activity signals for idle detection.
// The watcher uses this to supplement JSONL-based state with PTY reality checks.
type PTYIdleSource interface {
	// LastActivity returns the most recent timestamp when the terminal produced output.
	LastActivity() time.Time

	// TailLines returns the last n lines of visible terminal output for output analysis.
	TailLines(n int) []string
}
