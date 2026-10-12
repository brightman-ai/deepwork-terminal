package muxd

// escSafeLookback is how far before a proposed cut point this package will look to find the
// start of whatever ANSI/VT escape sequence might straddle it. Real-world sequences a
// full-screen TUI emits for a single repaint (SGR truecolor, cursor moves, OSC title/hyperlink)
// are a handful to a few dozen bytes; 512 leaves wide margin without meaningfully slowing a cut
// that happens once per WebSocket (re)connect, not per frame.
const escSafeLookback = 512

// isUTF8Continuation reports whether b is a UTF-8 continuation byte (10xxxxxx) — one that can
// never start a rune and must never start a replay.
func isUTF8Continuation(b byte) bool {
	return b&0xC0 == 0x80
}

// isUTF8LeadByte reports whether b starts a multi-byte UTF-8 rune (2-, 3-, or 4-byte form) and
// returns the rune's total byte length, or (false, 0) for an ASCII byte or an invalid lead byte.
func isUTF8LeadByte(b byte) (bool, int) {
	switch {
	case b&0xE0 == 0xC0:
		return true, 2
	case b&0xF0 == 0xE0:
		return true, 3
	case b&0xF8 == 0xF0:
		return true, 4
	default:
		return false, 0
	}
}

// ansiSequenceEnd scans the ANSI/VT escape sequence starting at data[escPos] (data[escPos] must
// be ESC, 0x1B) and returns the index just past its terminator, plus whether a terminator was
// found before data ran out. Covers the three shapes a terminal emulator actually emits:
//   - CSI   ESC '[' <params 0x30-0x3F>* <intermediates 0x20-0x2F>* <final 0x40-0x7E>
//   - OSC/DCS/SOS/PM/APC (the "string" introducers ']','P','X','^','_'): run until BEL (0x07)
//     or the two-byte String Terminator ESC '\' .
//   - Everything else: a lone second byte (ESC 'c', ESC '7', ESC '8', ESC '=', ESC '>', …).
//
// Not a validator — it does not reject malformed sequences, it only finds where a well-formed
// one ends, which is all a cut point needs.
func ansiSequenceEnd(data []byte, escPos int) (end int, terminated bool) {
	j := escPos + 1
	if j >= len(data) {
		return len(data), false
	}
	switch data[j] {
	case '[': // CSI
		k := j + 1
		for k < len(data) && data[k] >= 0x30 && data[k] <= 0x3F {
			k++
		}
		for k < len(data) && data[k] >= 0x20 && data[k] <= 0x2F {
			k++
		}
		if k < len(data) && data[k] >= 0x40 && data[k] <= 0x7E {
			return k + 1, true
		}
		return len(data), false
	case ']', 'P', 'X', '^', '_': // OSC / DCS / SOS / PM / APC — string until BEL or ST
		k := j + 1
		for k < len(data) {
			if data[k] == 0x07 {
				return k + 1, true
			}
			if data[k] == 0x1B && k+1 < len(data) && data[k+1] == '\\' {
				return k + 2, true
			}
			k++
		}
		return len(data), false
	default: // two-byte form: ESC + one char
		return j + 1, true
	}
}

// escSafeTailStart adjusts a proposed "keep data[proposedStart:]" cut so it never opens with
// the tail of a cut ANSI escape sequence or a truncated multi-byte UTF-8 rune. data is the full
// chronological byte slice the cut is taken from.
//
// The returned start is always >= proposedStart: an unsafe cut is resolved by skipping forward
// past whatever straddles it, never by reaching further back to include it whole. Widening the
// cut would silently spend more bytes than the caller budgeted for — exactly the "raise the
// cap" non-fix this function exists to replace (raising wsReplayMaxBytes only moves where the
// next cut can land mid-sequence, it cannot stop it landing there). A handful of bytes come off
// the FRONT of the replay this way — never more than the length of one straddling
// sequence/rune — so on the wire this is a strict decrease, never an increase.
func escSafeTailStart(data []byte, proposedStart int) int {
	if proposedStart <= 0 || proposedStart >= len(data) {
		return proposedStart
	}

	i := proposedStart

	// UTF-8: data[i] must not be a continuation byte. If it is, some earlier lead byte (within
	// the 3 preceding bytes — the longest UTF-8 form is 4 bytes total) started a rune that
	// straddles i; skip forward to that rune's end.
	if isUTF8Continuation(data[i]) {
		lowerBound := i - 3
		if lowerBound < 0 {
			lowerBound = 0
		}
		for p := i - 1; p >= lowerBound; p-- {
			if ok, n := isUTF8LeadByte(data[p]); ok {
				if p+n > i { // the rune does straddle i
					i = p + n
				}
				break
			}
			if !isUTF8Continuation(data[p]) {
				break // hit an ASCII byte with no lead — not a straddling rune, give up quietly
			}
		}
	}

	// ANSI: is i inside an escape sequence that started in the lookback window? Find the
	// nearest ESC behind i and replay that sequence's own grammar forward to see whether it
	// already closed before i (safe, i unchanged) or straddles i (skip to its end).
	lookback := i - escSafeLookback
	if lookback < 0 {
		lookback = 0
	}
	for p := i - 1; p >= lookback; p-- {
		if data[p] != 0x1B {
			continue
		}
		end, _ := ansiSequenceEnd(data, p)
		if end > i {
			// This sequence starts before i and ends at or after i: it straddles the proposed
			// cut (or data ran out before a terminator, which is the same thing for our
			// purposes — either way, data[i:] alone cannot be replayed safely). Skip past it.
			i = end
		}
		break // nearest ESC behind i fully explains whatever is at i; stop looking further back
	}

	return i
}
