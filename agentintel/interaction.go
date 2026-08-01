package agentintel

import (
	"sync/atomic"
	"time"
)

// Interaction awareness for the tmux probe.
//
// ── The contention this exists to resolve ────────────────────────────────────────────────────
// A tmux server executes commands ONE AT A TIME. The status probe issues a dozen of them a second
// (list-sessions, list-panes, a capture-pane per agent pane, another per window when the overview
// is open) and measures 320–385ms on a 6-pane server. The user's own actions — a keystroke echoing,
// `prefix 5` switching a window — go through that same single queue. So the dashboard and the
// person are in direct competition for the one resource, once a second, forever.
//
// The resolution is not "make the probe faster" (it already got 10–60× faster by being shared) but
// "decide who yields". The person yields nothing: they are the reason the process exists. The
// dashboard yields everything: it describes agent state at 1s resolution, and nobody reads it in
// the instant they are typing into a terminal — they are looking at the terminal.
//
// So an interaction buys quiet. The same shape as the frontend's ghost-refresh deferral
// (GHOST_TYPING_QUIET), deliberately: one idea — expensive background work steps aside while the
// user is acting, bounded by a staleness ceiling so "steps aside" can never become "never runs".
const (
	// interactionQuiet is how long after a keystroke the probe declines to run. Long enough to
	// cover the gap between keystrokes at speed (~140ms) plus the redraw the keystroke causes, so
	// sustained typing keeps the tmux server to itself rather than yielding it back between keys.
	interactionQuiet = 400 * time.Millisecond

	// interactionMaxStale bounds how long interaction may defer a rebuild. Past this the probe runs
	// regardless — a dashboard frozen for seconds because someone is holding a key is a bug, not a
	// courtesy. Chosen well above the 1s poll so a normal typing burst defers at most a few ticks.
	interactionMaxStale = 3 * time.Second
)

// lastInteraction is the wall-clock nanos of the most recent user input, or 0 for "never".
// Atomic rather than mutex-guarded: it is written on the input hot path (every keystroke, from
// whichever goroutine serves that request) and read once per probe. There is nothing to serialise
// — a reader that observes a slightly stale timestamp makes the same decision either way.
var lastInteraction atomic.Int64

// NoteInteraction records that the user just acted on a terminal. Call it from wherever input
// enters the process; the probe consults it and yields.
//
// Process-wide, not per-session, because the resource being protected is process-wide: ONE tmux
// server serves every session, so a keystroke in any terminal is competing with a probe on behalf
// of all of them.
func NoteInteraction() {
	lastInteraction.Store(time.Now().UnixNano())
}

// InteractedWithin reports whether a human acted on a terminal within the last d.
//
// Exported because the tmux probe is no longer the only background worker that should step
// aside: a file upload streaming through this process competes for the same CPU and disk as
// the PTY it is streaming past (see the terminal package's uploadPacer). Both need the same
// fact, and it must come from the same clock — two "is the user busy" answers that can differ
// is how one of them ends up subtly wrong and nobody notices.
//
// A zero d, or a process where no input has ever arrived, is false: never-interacted is not
// "interacting right now".
func InteractedWithin(d time.Duration) bool {
	nanos := lastInteraction.Load()
	if nanos == 0 || d <= 0 {
		return false
	}
	return time.Since(time.Unix(0, nanos)) < d
}

// InteractionQuiet is the window that counts as "the user is acting right now" — long enough
// to cover the gap between keystrokes at speed plus the redraw each one causes. Exported so a
// second yielding worker uses the SAME definition rather than picking its own number.
func InteractionQuiet() time.Duration { return interactionQuiet }

// probeDeferredForInteraction reports whether a rebuild should yield to a recent interaction.
//
// lastBuiltAt is when the cached snapshot was built (zero = never). Never-built is treated as
// infinitely stale and never deferred — the first snapshot is what populates the UI, and deferring
// it would leave a user who starts by typing looking at an empty dashboard.
func probeDeferredForInteraction(lastBuiltAt, now time.Time) bool {
	nanos := lastInteraction.Load()
	if nanos == 0 {
		return false
	}
	if now.Sub(time.Unix(0, nanos)) >= interactionQuiet {
		return false
	}
	if lastBuiltAt.IsZero() {
		return false
	}
	return now.Sub(lastBuiltAt) < interactionMaxStale
}
