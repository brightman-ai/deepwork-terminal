package terminal

import "github.com/brightman-ai/deepwork-terminal/muxd"

// Things that moved into the muxd package, kept reachable under their old names.
//
// Why they moved: the daemon is what owns PTYs now, so everything about *making* a PTY
// — the scrollback ring it fills, the environment scrubbing that decides what the child
// inherits, the shell-words tokenizer that turns a configured command into argv —
// belongs on that side of the boundary. The dependency runs one way (terminal imports
// muxd, never the reverse), which is what keeps a single implementation possible.
//
// These aliases are transitional: they exist so the move did not have to rewrite every
// call site at once. They disappear together with the in-process PTY path.
type RingBuffer = muxd.RingBuffer

// DefaultBufferCapacity is the default ring size (1 MB).
const DefaultBufferCapacity = muxd.DefaultBufferCapacity

// NewRingBuffer creates a RingBuffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer { return muxd.NewRingBuffer(capacity) }

// spawnCols/spawnRows are the geometry every PTY is born with.
//
// They ALIAS the daemon's constants rather than restating the numbers. That matters: the
// screen replay reconstructs a session's preview by replaying its byte stream onto a
// character grid, and a TUI paints by absolute cursor addressing — so the grid must be
// the size the program actually believes it has. A second hardcoded pair here is exactly
// how that replay drifted before (rows past the guessed height were clamped onto the last
// row and overwrote it: "Debug" surviving as "ebug"). One definition, two readers.
const (
	spawnCols = int(muxd.DefaultCols)
	spawnRows = int(muxd.DefaultRows)
)
