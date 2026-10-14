package muxd

// Reading the byte stream the way xterm.js 6.0.0 does — what it decodes, what it does with a control
// character in the middle of a sequence, and which sequence a given run of bytes is.
//
// This is where the model's input meets the browser's terminal, and the browser reads BYTES
// (useTerminalChannel.ts hands `new Uint8Array(data)` to xterm), so it is xterm's UTF-8 decoder
// (common/input/TextDecoder.ts, Utf8ToUtf32) that decides what a byte that is not UTF-8 means — not
// the replacement character a string decoder would give, but nothing at all — and its parser
// (common/parser/EscapeSequenceParser.ts) that decides when a sequence ends.
//
// ── What differs from a scan for "the final byte" ───────────────────────────────────────────────
// The old reading of a control sequence was ESC, then bytes up to a final one, abandoned if
// anything unexpected turned up. xterm's parser is a state machine (Paul Williams' DEC parser,
// vt100.net/emu/dec_ansi_parser) and differs in ways a real stream hits:
//
//   - A C0 control (LF, CR, BS, ... BEL, SO, SI) inside ESC/CSI is CARRIED OUT and the sequence goes
//     on: `ESC [ 1 LF A` is a line feed, then cursor-up 1. CAN and SUB abandon the sequence (and are
//     carried out, doing nothing); ESC abandons it and begins another; DEL is ignored.
//   - Intermediate bytes (0x20-0x2f) may follow ESC as well — `ESC ( 0` is ESC, the intermediate
//     '(' and the final '0' — and a sequence that does not name a function is read to its end and
//     ignored (it used to print its tail as text).
//   - A parameter byte after an intermediate, or a private marker after a parameter, makes a CSI
//     sequence IGNORED to its final byte, not dispatched.
//   - A printable that is not ASCII inside ESC/CSI ends the sequence and is dropped.
//   - U+0080..U+009F, valid UTF-8 as C2 80..C2 9F, are the C1 controls: IND and NEL act, CSI, OSC,
//     DCS, SOS, PM and APC begin sequences, the rest do nothing.
//
// ── Held sequences ──────────────────────────────────────────────────────────────────────────────
// A write can end inside a sequence. The model then holds the bytes (vt.pending) and reads them
// again with the next write — except that a control already seen inside the sequence has taken
// effect in xterm, and reading it again would carry it out twice. It is carried out now and cut
// from the held bytes (vt.carry).

// decodeResult is what decodeXterm made of the bytes at the front of a slice.
type decodeResult uint8

const (
	decRune decodeResult = iota // a code point
	decSkip                     // bytes that mean nothing; n of them are to be dropped
	decMore                     // the front of a character the slice ends before the end of
)

// decodeXterm reads one code point from b (len(b) > 0, b[0] >= 0x80 or not — ASCII is handled too)
// exactly as xterm.js 6.0.0's Utf8ToUtf32 does, byte for byte, including its notion of how many bytes
// an invalid sequence takes with it:
//
//   - a continuation byte or one of F8..FF where a character should start is skipped alone;
//   - a lead byte followed by a byte that is not a continuation is skipped alone, and the byte is
//     read again; the same one byte later for the second and third continuation, so a cut sequence
//     takes the lead and the continuations it did have with it;
//   - an overlong form, a surrogate, a value past U+10FFFF and the byte-order mark U+FEFF are decoded
//     and then dropped — all of their bytes. (xterm does not print U+FFFD for any of it. A U+FFFD
//     that really is one, EF BF BD, is an ordinary character and is printed.)
func decodeXterm(b []byte) (r rune, n int, res decodeResult) {
	b0 := b[0]
	switch {
	case b0 < 0x80:
		return rune(b0), 1, decRune
	case b0&0xE0 == 0xC0:
		if len(b) < 2 {
			return 0, 0, decMore
		}
		if b[1]&0xC0 != 0x80 {
			return 0, 1, decSkip
		}
		r = rune(b0&0x1F)<<6 | rune(b[1]&0x3F)
		if r < 0x80 {
			return 0, 1, decSkip
		}
		return r, 2, decRune
	case b0&0xF0 == 0xE0:
		if len(b) < 2 {
			return 0, 0, decMore
		}
		if b[1]&0xC0 != 0x80 {
			return 0, 1, decSkip
		}
		if len(b) < 3 {
			return 0, 0, decMore
		}
		if b[2]&0xC0 != 0x80 {
			return 0, 2, decSkip
		}
		r = rune(b0&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
		if r < 0x800 || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFEFF {
			return 0, 3, decSkip
		}
		return r, 3, decRune
	case b0&0xF8 == 0xF0:
		if len(b) < 2 {
			return 0, 0, decMore
		}
		if b[1]&0xC0 != 0x80 {
			return 0, 1, decSkip
		}
		if len(b) < 3 {
			return 0, 0, decMore
		}
		if b[2]&0xC0 != 0x80 {
			return 0, 2, decSkip
		}
		if len(b) < 4 {
			return 0, 0, decMore
		}
		if b[3]&0xC0 != 0x80 {
			return 0, 3, decSkip
		}
		r = rune(b0&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
		if r < 0x10000 || r > 0x10FFFF {
			return 0, 4, decSkip
		}
		return r, 4, decRune
	default:
		return 0, 1, decSkip
	}
}

// seqScratch is what the sequence being read has collected so far; the slices are reused from one
// sequence to the next (a full-screen program sends tens of thousands a second).
type seqScratch struct {
	inter  []byte // intermediate bytes (0x20-0x2f)
	params []byte // CSI: the private marker, if any, then the parameter bytes
	ctl    []rune // controls met inside the sequence, to be carried out when it ends (C0 only while held)
	ctlAt  []int  // the byte offset of each from where the sequence began
}

// parser states of a sequence, from the DEC parser's diagram (EscapeSequenceParser.ts).
type seqState uint8

const (
	stEscape    seqState = iota // after ESC
	stEscInter                  // ESC and one or more intermediates
	stCSIEntry                  // after CSI
	stCSIParam                  // inside the parameters
	stCSIInter                  // after the intermediates that follow them
	stCSIIgnore                 // a sequence that cannot be one: read to its final byte
)

// feed scans as much of raw as it can and returns how many bytes it consumed.
//
// When `flush` is false it stops at the first INCOMPLETE sequence or rune and reports that
// position, so the caller can hold the tail. When true it consumes everything, treating a
// truncated tail as the end of input — which is right for a one-shot replay of a fixed slice.
func (v *vt) feed(raw []byte, flush bool) int {
	i := 0
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == 0x1b: // ESC
			next, ok := v.consumeSeq(raw, i, stEscape, i+1, flush)
			if !ok {
				if flush {
					return len(raw)
				}
				return i
			}
			i = next
			continue
		case c < 0x20: // the C0 controls, each of which also ends a run of printing
			v.execute(c)
			i++
			continue
		case c == 0x7f: // DEL is ignored in the ground state — and, unlike a control, ends no REP
			i++
			continue
		}
		// A run of printable characters is ONE print in xterm (see vt.print), so gather it whole.
		v.run = v.run[:0]
	run:
		for i < len(raw) {
			c := raw[i]
			if c < 0x20 || c == 0x7f {
				break
			}
			if c < 0x80 {
				v.run = append(v.run, rune(c))
				i++
				continue
			}
			r, n, res := decodeXterm(raw[i:])
			switch res {
			case decMore:
				if !flush {
					v.print(v.run, 1)
					return i // a rune split across chunks
				}
				i = len(raw)
				break run
			case decSkip:
				i += n
				continue
			}
			if r < 0xA0 {
				// A C1 control ends the run like any other control.
				v.print(v.run, 1)
				v.run = v.run[:0]
				next, ok := v.consumeC1(raw, i, r, flush)
				if !ok {
					if flush {
						return len(raw)
					}
					return i
				}
				i = next
				continue
			}
			v.run = append(v.run, r)
			i += n
		}
		v.print(v.run, 1)
	}
	return len(raw)
}

// execute carries out one C0 control. Every one of them ends a run of printing (repWidth), which is
// all REP asks about the ones that do nothing else.
func (v *vt) execute(c byte) {
	switch c {
	case '\r':
		v.col = 0
	case '\n', 0x0b, 0x0c: // LF, and VT/FF, which every terminal treats as LF
		v.lineFeed()
	case '\b':
		v.restrictCursor()
		if v.col > 0 {
			v.col--
		}
	case '\t':
		// A pending wrap stays pending (xterm: a tab at or past the margin does nothing).
		if v.col < v.cols {
			v.col = v.clampCol((v.col/8 + 1) * 8)
		}
	case 0x0e: // SO — GL becomes G1
		v.charset.shift(1)
	case 0x0f: // SI — GL becomes G0
		v.charset.shift(0)
	}
	v.repWidth = 0
}

// executeRune is execute for what a sequence collected: a C0 control, or a C1 one (IND and NEL are
// the only two that do anything here; HTS sets a tab stop, which this model does not keep).
func (v *vt) executeRune(c rune) {
	if c < 0x20 {
		v.execute(byte(c))
		return
	}
	switch c {
	case 0x84: // IND
		v.index()
	case 0x85: // NEL
		v.col = 0
		v.index()
	}
	v.repWidth = 0
}

// runCtl carries out, in order, the controls the sequence just read had inside it.
func (v *vt) runCtl() {
	for _, c := range v.sq.ctl {
		v.executeRune(c)
	}
	v.sq.ctl, v.sq.ctlAt = v.sq.ctl[:0], v.sq.ctlAt[:0]
}

// consumeC1 handles a C1 control (code point 0x80..0x9f, which is two bytes, C2 80..C2 9F) at raw[i]
// in the ground state. The ones that begin a sequence are read like their ESC spellings (`C2 9B` is
// `ESC [`, both two bytes long).
func (v *vt) consumeC1(raw []byte, i int, c rune, flush bool) (int, bool) {
	switch c {
	case 0x9b: // CSI
		return v.consumeSeq(raw, i, stCSIEntry, i+2, flush)
	case 0x9d: // OSC
		return v.consumeString(raw, i, i+2, ']')
	case 0x90: // DCS
		return v.consumeString(raw, i, i+2, 'P')
	case 0x98, 0x9e, 0x9f: // SOS, PM, APC
		return v.consumeString(raw, i, i+2, 'X')
	case 0x9c: // ST: ends nothing here, and is not a dispatch either
		return i + 2, true
	}
	v.executeRune(c)
	return i + 2, true
}

// consumeSeq reads an escape or control sequence in state st, scanning from raw[j]; i is where the
// sequence began (what to hold from, and the return for "incomplete"). It returns the index just
// past what it consumed — which may be the index of the byte that abandoned it, to be read afresh —
// and false when the sequence runs past the end of raw.
func (v *vt) consumeSeq(raw []byte, i int, st seqState, j int, flush bool) (int, bool) {
	sq := &v.sq
	sq.inter, sq.params = sq.inter[:0], sq.params[:0]
	sq.ctl, sq.ctlAt = sq.ctl[:0], sq.ctlAt[:0]
	for {
		if j >= len(raw) {
			return v.heldSeq(raw, i, flush)
		}
		b := raw[j]
		if b >= 0x80 {
			r, n, res := decodeXterm(raw[j:])
			switch res {
			case decMore:
				return v.heldSeq(raw, i, flush)
			case decSkip:
				j += n // not a character at all: the parser never sees it
				continue
			}
			switch {
			case r >= 0xA0:
				// "Printable non-ASCII": a sequence that was still being read ends here, and the
				// character is dropped; one already ignored carries on.
				if st == stCSIIgnore {
					j += n
					continue
				}
				v.runCtl()
				return j + n, true
			case r == 0x90 || r == 0x98 || r == 0x9b || r == 0x9d || r == 0x9e || r == 0x9f || r == 0x9c:
				// Introducers and ST begin or end something else: abandon this and read them afresh.
				v.runCtl()
				return j, true
			default:
				// Any other C1 control is executed, and leaves the sequence.
				sq.ctl = append(sq.ctl, r)
				v.runCtl()
				return j + n, true
			}
		}
		switch {
		case b == 0x1b: // ESC: abandons this sequence and begins another, which the caller reads
			v.runCtl()
			return j, true
		case b == 0x18 || b == 0x1a: // CAN, SUB: execute (nothing, but it ends a run of printing) and leave
			sq.ctl = append(sq.ctl, rune(b))
			v.runCtl()
			return j + 1, true
		case b < 0x20: // the other controls act and the sequence goes on
			sq.ctl = append(sq.ctl, rune(b))
			sq.ctlAt = append(sq.ctlAt, j-i)
			j++
			continue
		case b == 0x7f:
			j++
			continue
		}
		switch st {
		case stEscape:
			switch {
			case b <= 0x2f:
				sq.inter = append(sq.inter, b)
				st = stEscInter
			case b == '[':
				st = stCSIEntry
			case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
				v.runCtl()
				return v.consumeString(raw, i, j+1, b)
			default:
				v.runCtl()
				v.dispatchEsc(nil, b)
				v.repWidth = 0
				return j + 1, true
			}
		case stEscInter:
			if b <= 0x2f {
				sq.inter = append(sq.inter, b)
			} else {
				v.runCtl()
				v.dispatchEsc(sq.inter, b)
				v.repWidth = 0
				return j + 1, true
			}
		case stCSIEntry, stCSIParam:
			switch {
			case b <= 0x2f:
				sq.inter = append(sq.inter, b)
				st = stCSIInter
			case b <= 0x3b: // digits, ':' and ';'
				sq.params = append(sq.params, b)
				st = stCSIParam
			case b <= 0x3f: // < = > ?
				if st == stCSIEntry {
					sq.params = append(sq.params, b)
					st = stCSIParam
				} else {
					st = stCSIIgnore
				}
			default:
				v.runCtl()
				v.dispatchCSI(sq.params, string(sq.inter), b)
				v.repWidth = 0
				return j + 1, true
			}
		case stCSIInter:
			switch {
			case b <= 0x2f:
				sq.inter = append(sq.inter, b)
			case b <= 0x3f:
				st = stCSIIgnore
			default:
				v.runCtl()
				v.dispatchCSI(sq.params, string(sq.inter), b)
				v.repWidth = 0
				return j + 1, true
			}
		case stCSIIgnore:
			if b >= 0x40 {
				v.runCtl()
				return j + 1, true // read to its end and not carried out; no dispatch, so no repWidth reset
			}
		}
		j++
	}
}

// heldSeq is "the sequence runs past the end of what was written". Controls met inside it have
// already acted in xterm, so they act now and are cut from the bytes to be read again (see the
// file's header).
func (v *vt) heldSeq(raw []byte, i int, flush bool) (int, bool) {
	sq := &v.sq
	if len(sq.ctl) == 0 {
		return i, false
	}
	if !flush {
		kept := make([]byte, 0, len(raw)-i)
		for k, at := 0, 0; i+k < len(raw); k++ {
			if at < len(sq.ctlAt) && sq.ctlAt[at] == k {
				at++
				continue
			}
			kept = append(kept, raw[i+k])
		}
		v.carry, v.hasCarry = kept, true
	}
	v.runCtl()
	return i, false
}

// consumeString reads an OSC, DCS, SOS, PM or APC string whose content begins at raw[start] (kind is
// the byte after ESC that introduced it: ] P X ^ _). It returns the index just past what ended it.
// None of them has any effect on layout, so only where they end matters.
//
// In xterm a string ends at ESC (which then begins a sequence of its own, so it is not consumed),
// CAN, SUB, and the C1 string terminator U+009C; an OSC string also at BEL. Nothing else ends it:
// BEL inside a DCS, SOS, PM or APC string is just a byte, and ESC \ — the usual spelling of ST —
// is ESC ending the string and then a sequence of its own that does nothing. Controls inside a
// string are not carried out. A C1 control inside one ends it (an introducer begins its own sequence).
//
// Two corners of the parser's table matter because the text after them is text: a character that is
// not ASCII ends an SOS/PM/APC string (it falls through the parser's table, which lists no such
// character for that state) and is dropped, and so it does a DCS before its final byte — the
// parameters and intermediates that come first are a small state machine of their own.
func (v *vt) consumeString(raw []byte, i, start int, kind byte) (int, bool) {
	const (
		dcsEntry = iota
		dcsParam
		dcsInter
		dcsIgnore
		dcsPass
	)
	dcs := dcsEntry
	// A terminator resets the join state when xterm's parser calls it an end (OSC_END, DCS_UNHOOK);
	// the others leave it, CAN and SUB apart, which are controls like any.
	resets := func() bool { return kind == ']' || (kind == 'P' && dcs == dcsPass) }
	for j := start; j < len(raw); {
		b := raw[j]
		switch {
		case b == 0x1b:
			if resets() {
				v.repWidth = 0
			}
			return j, true
		case b == 0x18 || b == 0x1a:
			v.repWidth = 0
			return j + 1, true
		case b == 0x07 && kind == ']':
			v.repWidth = 0
			return j + 1, true
		case b < 0x20 || b == 0x7f:
			j++
			continue
		case b < 0x80:
			if kind == 'P' {
				switch dcs {
				case dcsEntry:
					switch {
					case b <= 0x2f:
						dcs = dcsInter
					case b <= 0x3f:
						dcs = dcsParam
					default:
						dcs = dcsPass
					}
				case dcsParam:
					switch {
					case b <= 0x2f:
						dcs = dcsInter
					case b <= 0x3b:
					case b <= 0x3f:
						dcs = dcsIgnore
					default:
						dcs = dcsPass
					}
				case dcsInter:
					switch {
					case b <= 0x2f:
					case b <= 0x3f:
						dcs = dcsIgnore
					default:
						dcs = dcsPass
					}
				}
			}
			j++
			continue
		}
		r, n, res := decodeXterm(raw[j:])
		switch res {
		case decMore:
			return v.heldString(raw, i, start, kind)
		case decSkip:
			j += n
			continue
		}
		switch {
		case r >= 0xA0:
			if kind == ']' || (kind == 'P' && (dcs == dcsPass || dcs == dcsIgnore)) {
				j += n // content
				continue
			}
			return j + n, true // not a character this state accepts: the string is over, and the character is dropped
		case r == 0x9c:
			if resets() {
				v.repWidth = 0
			}
			return j + n, true
		case r == 0x90 || r == 0x98 || r == 0x9b || r == 0x9d || r == 0x9e || r == 0x9f:
			return j, true // begins something else
		default:
			v.executeRune(r)
			return j + n, true
		}
	}
	return v.heldString(raw, i, start, kind)
}

// heldString is "the string runs past the end of what was written".
func (v *vt) heldString(raw []byte, i, start int, kind byte) (int, bool) {
	if start != i+2 {
		// Controls were met between ESC and the introducer and have been carried out; what is held
		// is the string alone (see heldSeq).
		v.carry, v.hasCarry = append([]byte{0x1b, kind}, raw[start:]...), true
	}
	return i, false
}

// dispatchEsc carries out `ESC <inter> <final>`: the final byte, with whatever intermediates came
// first. Sequences xterm has no handler for are ignored whole.
func (v *vt) dispatchEsc(inter []byte, final byte) {
	switch len(inter) {
	case 0:
		switch final {
		case 'M': // RI — reverse index
			v.reverseNewline()
		case 'D': // IND — index (down one line, scrolling)
			v.index()
		case 'E': // NEL — next line
			v.col = 0
			v.index()
		case '7': // DECSC — save cursor
			v.saveCursor()
		case '8': // DECRC — restore cursor
			v.restoreCursor()
		case 'c': // RIS — full reset
			v.reset()
		case '=': // DECKPAM — application keypad: the same setting as `ESC [ ? 66 h`, so one entry
			v.noteMode(66, true) // in modes keeps the two spellings ordered against each other
		case '>': // DECKPNM — numeric keypad (`ESC [ ? 66 l`)
			v.noteMode(66, false)
		case 'n': // LS2 — GL becomes G2
			v.charset.shift(2)
		case 'o': // LS3
			v.charset.shift(3)
		case '|': // LS3R, which xterm.js treats as LS3
			v.charset.shift(3)
		case '}': // LS2R
			v.charset.shift(2)
		case '~': // LS1R
			v.charset.shift(1)
		}
	case 1:
		switch inter[0] {
		case '(', ')', '*', '+', '-', '.', '/':
			v.charset.designate(inter[0], final)
		case '%':
			if final == '@' || final == 'G' { // select the default character set (UTF-8 is not a choice here)
				v.charset.defaultSets()
			}
		}
	}
}
