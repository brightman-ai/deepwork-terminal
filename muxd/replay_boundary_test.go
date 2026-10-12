package muxd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TC-ESC-01: proposed cut lands clean (on a plain text byte, no escape nearby) — unchanged.
func TestEscSafeTailStart_CleanCutUnchanged(t *testing.T) {
	data := []byte("hello world, nothing special here")
	got := escSafeTailStart(data, 6) // "world, nothing special here"
	assert.Equal(t, 6, got)
}

// TC-ESC-02: the exact bug reported 2026-10-12 — a 24-bit truecolor SGR sequence
// (ESC[38;2;122;122;122;49m) cut mid-sequence must skip past it entirely, not open with
// "8;2;122;122;122;49m" as raw text.
func TestEscSafeTailStart_TruecolorSGRCutMidSequence_SkipsWholeSequence(t *testing.T) {
	seq := "\x1b[38;2;122;122;122;49m"
	data := []byte(seq + "rest of the line")
	// Propose a cut right after "\x1b[3" — i.e. inside the sequence, at the position that used
	// to surface as "8;2;122;122;122;49mrest..." (the exact shape from the user's screenshot).
	proposed := len("\x1b[3")
	got := escSafeTailStart(data, proposed)
	assert.Equal(t, len(seq), got, "must land exactly at the sequence's own end, not before or after it")
	assert.Equal(t, "rest of the line", string(data[got:]))
}

// TC-ESC-03: cut proposed exactly ON the sequence's first byte (ESC itself) is already safe and
// must NOT move — data[proposedStart:] already carries the ESC and everything after it, so the
// sequence rides along whole; there is nothing to skip.
func TestEscSafeTailStart_CutAtESCItself_Unchanged(t *testing.T) {
	seq := "\x1b[1;31m"
	data := []byte(seq + "red text")
	got := escSafeTailStart(data, 0)
	assert.Equal(t, 0, got)
}

// TC-ESC-04: cut proposed exactly at a sequence boundary (right after it ends) — already safe,
// must not move at all (no needless shrinking of the replay).
func TestEscSafeTailStart_CutRightAfterSequence_Unchanged(t *testing.T) {
	seq := "\x1b[0m"
	data := []byte(seq + "plain")
	got := escSafeTailStart(data, len(seq))
	assert.Equal(t, len(seq), got)
}

// TC-ESC-05: OSC sequence (e.g. a terminal title or hyperlink) cut mid-sequence, BEL-terminated.
func TestEscSafeTailStart_OSCBelTerminated_SkipsWholeSequence(t *testing.T) {
	seq := "\x1b]0;window title\x07"
	data := []byte(seq + "after")
	proposed := len("\x1b]0;window")
	got := escSafeTailStart(data, proposed)
	assert.Equal(t, len(seq), got)
	assert.Equal(t, "after", string(data[got:]))
}

// TC-ESC-06: OSC sequence terminated by the two-byte String Terminator (ESC \) instead of BEL.
func TestEscSafeTailStart_OSCStringTerminated_SkipsWholeSequence(t *testing.T) {
	seq := "\x1b]8;;https://example.com/x\x1b\\"
	data := []byte(seq + "link text")
	proposed := len("\x1b]8;;https")
	got := escSafeTailStart(data, proposed)
	assert.Equal(t, len(seq), got)
	assert.Equal(t, "link text", string(data[got:]))
}

// TC-ESC-07: simple two-byte escape (save/restore cursor, charset shift, …) cut right between
// the two bytes — must not keep the lone second byte as text.
func TestEscSafeTailStart_TwoByteEscape_SkipsWholeSequence(t *testing.T) {
	data := []byte("\x1b7restored") // ESC 7 = save cursor
	got := escSafeTailStart(data, 1)
	assert.Equal(t, 2, got)
	assert.Equal(t, "restored", string(data[got:]))
}

// TC-ESC-08: multi-byte UTF-8 rune (3-byte Chinese character) cut mid-character must not open
// with a bare continuation byte.
func TestEscSafeTailStart_UTF8RuneCutMidCharacter_SkipsWholeRune(t *testing.T) {
	data := []byte("安全BG") // 安 = E5 AE 89 (3 bytes), 全 = E5 85 A8 (3 bytes), then ASCII
	proposed := 1            // lands on the 2nd byte of 安 (continuation byte)
	got := escSafeTailStart(data, proposed)
	assert.Equal(t, 3, got, "must land after the full rune 安, not mid-byte")
	assert.Equal(t, "全BG", string(data[got:]))
}

// TC-ESC-09: unterminated sequence that runs all the way to the end of the buffer (the ring was
// cut off mid-repaint) — must discard the dangling remainder rather than emit it as text.
func TestEscSafeTailStart_UnterminatedSequenceAtBufferEnd_DiscardsRemainder(t *testing.T) {
	data := []byte("before\x1b[38;2;1;2;") // never closes
	proposed := len("before\x1b[3")
	got := escSafeTailStart(data, proposed)
	assert.Equal(t, len(data), got, "nothing safe to replay past the dangling sequence")
}

// TC-ESC-10: a plain digit at the proposed cut (legitimate text, e.g. a line that happens to
// start with a number) must NOT be mistaken for stray escape-sequence parameters when there is
// no ESC within the lookback window.
func TestEscSafeTailStart_PlainDigitsNotMistakenForEscapeParams(t *testing.T) {
	data := []byte("line one\n12345 is just a number\nline three")
	proposed := len("line one\n")
	got := escSafeTailStart(data, proposed)
	assert.Equal(t, proposed, got)
}

// TC-ESC-11: realistic repaint burst — many SGR sequences back to back (as a full-screen "flicker"
// TUI like codex/claude-code emits) — cutting at EVERY byte offset must never produce a result
// that starts with a continuation/parameter byte belonging to a still-open sequence.
func TestEscSafeTailStart_DenseRepaintBurst_NoOffsetProducesStraySequenceFragment(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("\x1b[38;2;122;122;122;49m段落文字\x1b[0m")
	}
	data := []byte(b.String())
	for proposed := 0; proposed < len(data); proposed++ {
		got := escSafeTailStart(data, proposed)
		if got >= len(data) {
			continue // entire tail discarded — always safe (empty replay)
		}
		assert.Falsef(t, isUTF8Continuation(data[got]), "offset %d -> %d starts mid-rune", proposed, got)
		// A result starting with an escape param/intermediate byte with no ESC immediately
		// before it would be exactly the user's bug; assert the byte at got is never a
		// leftover CSI parameter/intermediate UNLESS it is itself the ESC of a fresh sequence
		// or a genuine printable character boundary. The strongest cheap check: walking
		// forward from got using the SAME grammar must never immediately hit a CSI final byte
		// (0x40-0x7E) with no preceding ESC in this slice — i.e. got is never strictly inside
		// an unclosed sequence that started before it.
		for p := got - 1; p >= 0 && p >= got-escSafeLookback; p-- {
			if data[p] != 0x1B {
				continue
			}
			end, _ := ansiSequenceEnd(data, p)
			assert.LessOrEqualf(t, end, got, "offset %d -> %d still lands inside sequence starting at %d", proposed, got, p)
			break
		}
	}
}

// TC-ESC-12: idempotent — re-applying the adjustment to an already-safe start is a no-op.
func TestEscSafeTailStart_Idempotent(t *testing.T) {
	data := []byte("\x1b[38;2;122;122;122;49m段落\x1b[0m文字")
	for proposed := 0; proposed <= len(data); proposed++ {
		once := escSafeTailStart(data, proposed)
		twice := escSafeTailStart(data, once)
		assert.Equal(t, once, twice, "proposed=%d", proposed)
	}
}

// TC-ESC-13: out-of-range inputs pass through unchanged rather than panicking — callers compute
// proposedStart from arithmetic that can legitimately produce 0 or len(data).
func TestEscSafeTailStart_OutOfRangeInputsPassThrough(t *testing.T) {
	data := []byte("abc")
	assert.Equal(t, 0, escSafeTailStart(data, 0))
	assert.Equal(t, 3, escSafeTailStart(data, 3))
	assert.Equal(t, 5, escSafeTailStart(data, 5)) // beyond len(data): unchanged, caller's problem
	assert.Equal(t, -1, escSafeTailStart(data, -1))
}
