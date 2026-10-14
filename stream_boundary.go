package terminal

// stream_boundary.go — where in a byte stream something else may be inserted.
//
// A terminal parser is a state machine, and output reaches it in chunks cut wherever a PTY read
// happened to end: one frame can stop after `ESC [ 3` and the next begin with `1 m`. Anything the
// server inserts BETWEEN two such frames lands inside the sequence — `ESC [ ? 2004 h` there cancels
// the half-read SGR, and the `1m` that follows is printed as text. So a server-originated
// insertion into a live stream must wait for a point where the parser is idle.
//
// parserBoundary follows just enough of the parser to answer that: is the stream so far at
// ground — outside every escape sequence, control string and multi-byte character?

type boundaryState uint8

const (
	bGround    boundaryState = iota
	bEscape                  // after ESC
	bCSI                     // ESC [ … waiting for the final byte
	bString                  // OSC / DCS / SOS / PM / APC body, until BEL or ST
	bStringEsc               // ESC inside a string: ST if '\' follows
	bUTF8                    // inside a multi-byte character
)

type parserBoundary struct {
	state boundaryState
	osc   bool // in bString: the string is an OSC, the one kind BEL also ends
	need  int  // continuation bytes still owed in bUTF8
	cp    rune // the code point being assembled in bUTF8
	from  boundaryState
}

// feed advances the tracker over b. Like the browser's parser it works on code points, not
// bytes: a C1 control can arrive UTF-8 encoded (U+009B is CSI, two bytes C2 9B), and xterm acts on
// it exactly as on ESC [. So a multi-byte character is assembled first and then run through the
// state machine, from whatever state it began in.
func (p *parserBoundary) feed(b []byte) {
	for _, c := range b {
		if p.state == bUTF8 {
			if c&0xc0 != 0x80 { // not a continuation: the character was abandoned (xterm drops it)
				p.state = p.from
			} else {
				p.cp = p.cp<<6 | rune(c&0x3f)
				if p.need--; p.need > 0 {
					continue
				}
				p.state = p.from
				p.step(p.cp)
				continue
			}
		}
		switch {
		case c < 0x80:
			p.step(rune(c))
		case c >= 0xc2 && c <= 0xdf:
			p.from, p.state, p.need, p.cp = p.state, bUTF8, 1, rune(c&0x1f)
		case c >= 0xe0 && c <= 0xef:
			p.from, p.state, p.need, p.cp = p.state, bUTF8, 2, rune(c&0x0f)
		case c >= 0xf0 && c <= 0xf4:
			p.from, p.state, p.need, p.cp = p.state, bUTF8, 3, rune(c&0x07)
		default:
			// A stray continuation or an invalid lead byte: the browser's decoder drops it.
		}
	}
}

// step runs one code point through the escape-sequence state machine.
func (p *parserBoundary) step(r rune) {
	// Transitions xterm takes from ANY state: CAN/SUB abandon, ESC starts over, and the C1
	// controls act as their 7-bit spellings do.
	switch {
	case r == 0x18 || r == 0x1a:
		p.state = bGround
		return
	case r == 0x1b:
		if p.state == bString {
			p.state = bStringEsc // ESC \ is ST
		} else {
			p.state = bEscape
		}
		return
	case r == 0x9b:
		p.state = bCSI
		return
	case r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f:
		p.state, p.osc = bString, r == 0x9d // DCS, SOS, OSC, PM, APC
		return
	case r >= 0x80 && r <= 0x9f:
		p.state = bGround // ST and the single-character C1 controls
		return
	}
	switch p.state {
	case bEscape:
		if r < 0x20 || r == 0x7f {
			return // a C0 control inside an escape sequence is executed, and the sequence goes on (xterm)
		}
		switch r {
		case '[':
			p.state = bCSI
		case ']', 'P', 'X', '^', '_':
			p.state, p.osc = bString, r == ']'
		default:
			if r >= 0x20 && r <= 0x2f {
				return // intermediate (ESC ( B …): the final byte follows
			}
			p.state = bGround
		}
	case bCSI:
		if r >= 0x40 && r <= 0x7e {
			p.state = bGround
		}
	case bString:
		if r == 0x07 && p.osc { // BEL ends an OSC; DCS, SOS, PM and APC end only at ST
			p.state = bGround
		}
	case bStringEsc:
		if r == '\\' {
			p.state = bGround
		} else {
			// ESC + anything else ends the string and starts a new sequence.
			p.state = bEscape
			p.step(r)
		}
	}
}

func (p *parserBoundary) atGround() bool { return p.state == bGround }

// restorePrelude puts a terminal of unknown state into the one every restore is drawn on: no
// sequence in progress (CAN), the normal screen, the whole screen as the scrolling region, absolute
// addressing, autowrap on, insert off, line feed returning the carriage (the frontend's convertEol),
// default pen, every character set designation US ASCII with G0 shifted in (1049l's cursor
// restore can bring back a DEC set and an SO), and nothing on the screen or
// in its history. A freshly loaded page is already there; a page that kept its terminal across a
// reconnect, or a viewer being re-synced after it fell behind, can be anywhere — inside a TUI's
// alternate screen, under a TUI's scrolling region — and drawing a keyframe into that is drawing it
// into the wrong place.
//
// Input modes (mouse reporting, bracketed paste, cursor keys) are deliberately NOT reset here: a
// mode this server never observed is unknown, and resetting it would assert "off" for it — the
// alternate screen's wheel scrolling depends on mouse reporting, and the frontend learned the hard
// way not to reset it (see REPLAY_RESET_SEQUENCE). The keyframe restates every mode it did observe.
const restorePrelude = "\x18\x1b[?1049l\x1b[r\x1b[?6l\x1b[?7h\x1b[4l\x1b[20h\x1b[0m\x1b(B\x1b)B\x1b*B\x1b+B\x0f\x1b[H\x1b[2J\x1b[3J"
