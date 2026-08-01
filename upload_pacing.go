package terminal

import (
	"io"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// ── uploads yield to the person ──────────────────────────────────────────────────────────────
//
// A terminal is an interactive instrument; an upload is a background errand. When they compete,
// the errand loses. That is the same rule agentintel/interaction.go already applies to the tmux
// probe, and this is the second worker to adopt it — deliberately reading the SAME interaction
// clock rather than inventing a second opinion about when the user is busy.
//
// What was actually measured before writing this (scripts/diag/uploadlat, 42 window switches with
// a 1 GB upload across the middle third, loopback at ~200 MB/s):
//
//	before  first-byte p50=4ms  p90=10ms
//	during  first-byte p50=4ms  p90=5ms      ← no degradation
//	after   first-byte p50=4ms  p90=23ms
//
// So on THIS machine the streaming upload path already leaves the terminal alone, and the honest
// summary is that the pacer is insurance, not a fix for an observed stall. It is here because the
// property should hold on hardware that is not this machine — a 2-core VPS, a spinning disk, an
// upload landing on the same device tmux is logging to — and because "the terminal stays live"
// should be a guarantee of the design rather than a property of a fast laptop. Re-run uploadlat
// after any change here; that is what it is for.
//
// ── why a rate ceiling and not a duty cycle ──────────────────────────────────────────────────
// The obvious alternative is "work X% of the time while the user is typing", which self-adapts to
// machine speed. It also mis-measures: the read that feeds an upload BLOCKS on the network, and
// sleeping in proportion to a blocked read throttles a slow remote uploader that was never
// competing for anything. A ceiling cannot make that mistake — a transfer already slower than the
// ceiling never notices it exists. A 32 MB/s ceiling is ~10× a good consumer uplink, so in
// practice it engages only for the local case, which is exactly the one fast enough to hurt.
const (
	// uploadPacedRate is the byte/second ceiling that applies ONLY while the user is interacting.
	// Idle → no ceiling at all: a 300 MB local paste into an untouched terminal still lands in
	// under two seconds.
	uploadPacedRate = 32 << 20

	// uploadPaceSlice bounds how much may pass between two pacing decisions. Small enough that a
	// burst cannot slip a whole buffer through at full speed the instant a keystroke lands, large
	// enough that the sleeps stay in the tens-of-milliseconds range rather than becoming a
	// syscall-per-kilobyte grinder.
	uploadPaceSlice = 256 << 10
)

// uploadPacer wraps a reader so that the bytes flowing through it observe uploadPacedRate for as
// long as a human is acting on a terminal, and flow at full speed otherwise.
//
// It is applied to the READ side, upstream of every write, which is what makes one wrapper cover
// the whole cost of an upload: the HTTP body wrapper also paces net/http's spill of a large
// multipart part to disk (it can only write what it has read), and the staging wrapper paces the
// copy into the session directory. Pacing the writes instead would have needed a wrapper at each
// of those places, and the one everybody forgets is the one that matters.
type uploadPacer struct {
	src io.Reader
}

// paceUpload wraps src unless it is already paced — the paste path wraps the request body and
// then stages from it, and double-pacing the same bytes would halve the ceiling by accident.
func paceUpload(src io.Reader) io.Reader {
	if src == nil {
		return nil
	}
	if _, ok := src.(*uploadPacer); ok {
		return src
	}
	return &uploadPacer{src: src}
}

// paceUploadBody paces a request body while keeping it a ReadCloser. Close must survive the
// wrapping: net/http closes the body it was handed, and MaxBytesReader's Close is what releases
// the "this connection saw an oversized body" bookkeeping.
func paceUploadBody(rc io.ReadCloser) io.ReadCloser {
	if rc == nil {
		return nil
	}
	return pacedBody{Reader: paceUpload(rc), Closer: rc}
}

type pacedBody struct {
	io.Reader
	io.Closer
}

// uploadInteracting answers "is a human acting on a terminal right now". A variable, not a direct
// call, for one reason: the clock behind it is PROCESS-WIDE, so a test that wants to observe the
// idle path cannot get there by waiting — any other test in the binary that drives terminal input
// re-arms it, and the resulting failure looks like a pacer bug rather than the test-ordering
// artifact it is. Swap it, restore it in t.Cleanup.
var uploadInteracting = func() bool {
	return agentintel.InteractedWithin(agentintel.InteractionQuiet())
}

func (p *uploadPacer) Read(b []byte) (int, error) {
	// The interaction check happens BEFORE the read, on the full buffer, so that an idle transfer
	// pays nothing but one atomic load per buffer.
	if !uploadInteracting() {
		return p.src.Read(b)
	}
	if len(b) > uploadPaceSlice {
		b = b[:uploadPaceSlice]
	}
	started := time.Now()
	n, err := p.src.Read(b)
	if n <= 0 {
		return n, err
	}
	terminalUploadPacedBytes.Add(uint64(n))

	// The read's own duration counts against the budget: a transfer that is already slower than
	// the ceiling has nothing to pay, and a fast one only sleeps for the difference.
	budget := time.Duration(float64(n) / float64(uploadPacedRate) * float64(time.Second))
	if wait := budget - time.Since(started); wait > 0 {
		terminalUploadPacedDelay.Observe(wait.Seconds())
		time.Sleep(wait)
	}
	return n, err
}
