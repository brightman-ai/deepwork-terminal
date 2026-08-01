package terminal

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interacting pins the pacer's view of "is the user acting" for one test. The real clock is
// process-wide and every test in this binary that drives terminal input re-arms it, so waiting
// for quiet is not something a test can do reliably — it must state the condition instead.
func interacting(t *testing.T, active bool) {
	t.Helper()
	prev := uploadInteracting
	uploadInteracting = func() bool { return active }
	t.Cleanup(func() { uploadInteracting = prev })
}

// TestUploadPacer_IdleTransferIsNotSlowed is the property that keeps this mechanism honest: an
// upload into a terminal nobody is touching must cost nothing. If this ever fails, the pacer has
// stopped being insurance and started being a tax.
func TestUploadPacer_IdleTransferIsNotSlowed(t *testing.T) {
	interacting(t, false)
	payload := bytes.Repeat([]byte("x"), 8<<20) // 8 MiB — a quarter-second at the paced ceiling

	started := time.Now()
	got, err := io.ReadAll(paceUpload(bytes.NewReader(payload)))
	elapsed := time.Since(started)

	require.NoError(t, err)
	require.Len(t, got, len(payload), "the pacer must not change what is read, only when")
	assert.Less(t, elapsed, 100*time.Millisecond,
		"an idle transfer must run at full speed; the ceiling would have made this take ~250ms")
}

// TestUploadPacer_YieldsWhileTheUserIsActing pins the actual guarantee: while someone is typing,
// the upload observes the ceiling instead of taking the machine.
func TestUploadPacer_YieldsWhileTheUserIsActing(t *testing.T) {
	interacting(t, true)
	payload := bytes.Repeat([]byte("x"), 4<<20) // 4 MiB → 125ms at 32 MB/s

	started := time.Now()
	got, err := io.ReadAll(paceUpload(bytes.NewReader(payload)))
	elapsed := time.Since(started)

	require.NoError(t, err)
	require.Len(t, got, len(payload))
	// Generous lower bound: the interaction window (400ms) outlasts this read, so every slice is
	// paced, but asserting the exact 125ms would make this a timing test rather than a behaviour
	// test. Half the budget is far outside what an unpaced in-memory read could ever take.
	assert.Greater(t, elapsed, 60*time.Millisecond,
		"a transfer running while the user types must be paced to the ceiling")
}

// TestUploadPacer_DoesNotDoublePace: the paste path wraps the request body and then stages out of
// it, so the same bytes pass two paceUpload calls. Wrapping twice would silently halve the ceiling
// — a bug that would show up only as "uploads are inexplicably slow while typing".
func TestUploadPacer_DoesNotDoublePace(t *testing.T) {
	once := paceUpload(bytes.NewReader([]byte("abc")))
	twice := paceUpload(once)

	assert.Same(t, once, twice, "re-pacing an already-paced reader must be a no-op")
}

// TestUploadPacer_SlowSourcePaysNothing: the ceiling must never punish a transfer that is already
// slower than it — a phone on hotel wifi is not competing with the terminal for anything. This is
// the case a duty-cycle design gets wrong (it sleeps in proportion to a BLOCKED read), and the
// reason the pacer subtracts the read's own duration from the budget.
func TestUploadPacer_SlowSourcePaysNothing(t *testing.T) {
	interacting(t, true)
	// Three reads, 50ms each, of whatever buffer the copier offers — a few KB per 50ms, orders of
	// magnitude under the 32 MB/s ceiling. The expected byte count is taken from the source rather
	// than computed, because how much a Read returns depends on the copier's buffer (io.Discard's
	// ReadFrom offers 8 KiB, io.Copy 32 KiB) and this test is about time, not about that.
	src := &slowReader{remaining: 3, chunk: make([]byte, 16<<10), delay: 50 * time.Millisecond}

	started := time.Now()
	n, err := io.Copy(io.Discard, paceUpload(src))
	elapsed := time.Since(started)

	require.NoError(t, err)
	require.EqualValues(t, src.emitted, n)
	require.Positive(t, n)
	assert.Less(t, elapsed, 200*time.Millisecond,
		"the source's own 150ms is the whole cost; the pacer must add nothing on top")
}

type slowReader struct {
	remaining int
	chunk     []byte
	delay     time.Duration
	emitted   int64
}

func (s *slowReader) Read(b []byte) (int, error) {
	if s.remaining == 0 {
		return 0, io.EOF
	}
	s.remaining--
	time.Sleep(s.delay)
	n := copy(b, s.chunk)
	s.emitted += int64(n)
	return n, nil
}
