package muxd

// Bounded-queue admission, in one place.
//
// Four queues in this codebase carry a mix of expendable and non-expendable elements to a
// consumer that may stall: the daemon's per-subscriber frames, the server's per-viewer
// frames, and the client's two event streams. All four had the same two policies written out
// by hand, and the sameness is not a coincidence — it follows from one fact about all four:
// the producer holds a lock, or is the PTY read loop, and MUST NOT BLOCK. A slow consumer
// therefore costs something, and the only question is what.
//
// The answer is the same everywhere, so it lives here once:
//
//   - Expendable elements (raw output) are refused AT THE TAIL, before the queue is truly
//     full — see AdmitExpendable. Losing them is recoverable: the consumer re-attaches and
//     replays.
//   - Everything else is admitted by discarding the OLDEST queued element — see
//     PushNewestOnFull. Nothing schedules a re-send of a resize, a gap notice or an exit, so
//     these cannot be refused; and when the choice is which one to lose, the newest is the one
//     that describes the world the consumer will still be living in after it drains.
//
// WHY THE MECHANISM IS SHARED AND THE REASONS ARE NOT. Each caller keeps its own comment
// explaining why ITS queue needs this, because those reasons genuinely differ — a lost resize
// scrambles a screen, a lost change-notification leaves a stale list forever. What does not
// differ is the twelve lines that implement it, and having written those twelve lines four
// times is how an invariant drifts: three copies get fixed and the fourth keeps the bug.

// AdmitExpendable reports whether a queue has room for one more element that the system can
// afford to lose, holding `reserve` slots at the tail for ones it cannot.
//
// Refusing before the queue is full is the entire point. The obvious alternative — fill it up,
// then evict to make room — can evict an EARLIER control element in favour of a later one,
// which reorders the stream rather than merely thinning it: [resize→80, output-drawn-at-80]
// becomes [output-at-80, resize→100], and the consumer paints those bytes onto a grid it was
// never told about.
func AdmitExpendable[T any](ch chan T, reserve int) bool {
	room := cap(ch) - reserve
	if room < 1 {
		// A queue smaller than its own reserve would otherwise admit NOTHING — a silent,
		// total loss of output, which is worse than the backpressure the reserve exists to
		// avoid. Production queues are far larger; this only keeps a small one honest.
		room = 1
	}
	return len(ch) < room
}

// PushNewestOnFull delivers v without ever blocking, making room by discarding the OLDEST
// queued element if the queue is full.
//
// Discarding the oldest — rather than refusing v — is what makes a loss self-announcing.
// Losses on these queues arrive as a contiguous tail: a consumer that falls behind receives
// 1..N and then silence, so what it holds looks unbroken and the gap only becomes visible on
// the next element to arrive. Drop the newest and that element never comes; the consumer sits
// on a stale view indefinitely. Drop the oldest and the newest always lands, carrying the
// highest sequence number, so the gap is visible the instant the consumer drains — no extra
// protocol, no timer, no second goroutine.
//
// The three selects are all non-blocking on purpose: the drain may find the queue already
// emptied by the consumer between the first and second select, and the final send may still
// lose a race with a producer on another goroutine. Losing that race is acceptable — it means
// something even newer got there first — but blocking is not, because the caller is holding a
// lock or is the PTY read loop.
func PushNewestOnFull[T any](ch chan T, v T) {
	select {
	case ch <- v:
		return
	default:
	}
	select {
	case <-ch: // make room by discarding the oldest
	default:
	}
	select {
	case ch <- v:
	default:
	}
}
