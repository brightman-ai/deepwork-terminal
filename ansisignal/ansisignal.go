// Package ansisignal extracts the out-of-band signals a terminal program emits when it
// wants the human back: the ASCII BEL (0x07) and the OSC desktop-notification sequences
// (iTerm2 OSC 9, the OSC 777 "notify" extension, kitty's OSC 99).
//
// ── Why this exists ─────────────────────────────────────────────────────────────────────
// Everything else in this codebase INFERS whether an agent needs you — from transcripts,
// from process trees, from a rendered screen. Those inferences are heuristics and they are
// wrong sometimes. A BEL or an OSC notification is not a heuristic: the program said it, in
// band-out-of-band, on purpose. It is the only first-party statement of intent we ever get,
// and until now it was silently dropped (the screen model consumed 0x07 as "no layout
// effect" and nothing else ever looked). Reading it turns a guess into a fact.
//
// ── Contract: pure observer ─────────────────────────────────────────────────────────────
// A Scanner NEVER consumes, rewrites, or reorders the byte stream. Feed takes a copy-free
// read-only view, returns what it saw, and that is all — the original bytes still go to the
// browser untouched, so the terminal keeps behaving exactly as it did (a real BEL still
// reaches xterm.js, an OSC 9 still reaches whatever else listens). Any change to this file
// that starts returning bytes to write is a bug, not a feature.
//
// ── Contract: streaming ────────────────────────────────────────────────────────────────
// A PTY read boundary lands wherever the kernel felt like it, so an OSC sequence arrives
// split across two (or twenty) Feed calls routinely. Scanner is therefore a resumable state
// machine, not a per-chunk regexp: state and the partial body survive between calls. The
// partial body is capped (MaxPendingBytes) so a stream that opens an OSC and never closes it
// — malformed output, a binary blob, a truncated sixel — cannot grow memory without bound;
// past the cap the sequence is abandoned and the scanner skips to its terminator.
//
// The package is pure: no imports outside the standard library, no I/O, no globals, no clock.
package ansisignal

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// Kind classifies what the program emitted.
type Kind string

const (
	// KindBell is a bare BEL (0x07). It carries no text — only "something happened here".
	// It is also the noisiest signal (readline emits it for an ambiguous tab-completion),
	// so a consumer MUST qualify it before treating it as attention-worthy; see the caller.
	KindBell Kind = "bell"

	// KindNotify is an explicit desktop notification (OSC 9 / OSC 777 / OSC 99). Unlike a
	// bell, nothing emits one by accident — it is always a deliberate "tell the human".
	KindNotify Kind = "notify"
)

// Signal is one observed out-of-band signal. Title/Body are empty for KindBell and may be
// partially empty for KindNotify (OSC 9 carries a body only; OSC 99 often a title only).
type Signal struct {
	Kind  Kind
	Title string
	Body  string
}

const (
	// MaxPendingBytes caps the OSC body held across chunks. Real notification payloads are
	// tens to hundreds of bytes; 8 KiB is far above any legitimate one and small enough that
	// a hostile or broken stream cannot use it as an allocator. On overflow the sequence is
	// abandoned (no signal) and the scanner skips ahead to its terminator so the NEXT one
	// still parses — dropping one payload must not desynchronise the machine forever.
	MaxPendingBytes = 8 * 1024

	// maxKittyPartials caps how many half-received kitty notifications are held at once.
	// Kitty's OSC 99 may be chunked (d=0 … d=1) and a stream that starts chunks it never
	// finishes would otherwise accumulate one entry per identifier.
	maxKittyPartials = 8

	// maxSkipBytes is the resync escape hatch for the opaque-string states. Their contents
	// are never buffered, so this is not about memory — it is about deafness: a DCS/APC that
	// is opened and never terminated (truncated image, mangled tmux passthrough) would
	// otherwise make the scanner ignore every signal for the rest of the session. Legitimate
	// payloads (a full-screen sixel, a kitty graphics frame) run to a few MiB, so resyncing
	// past that costs nothing real and bounds the blast radius of one malformed sequence.
	maxSkipBytes = 4 << 20
)

const (
	bel = 0x07
	esc = 0x1b
)

// scanState is the resumable position in the escape grammar. Split states (…Esc) exist
// because a chunk can end between ESC and the byte that gives it meaning.
type scanState uint8

const (
	stGround  scanState = iota // ordinary text
	stEsc                      // saw ESC, waiting for the introducer
	stOSC                      // inside OSC, accumulating the body
	stOSCEsc                   // inside OSC, saw ESC (expecting '\' = ST)
	stSkip                     // inside a string we deliberately ignore (see below)
	stSkipEsc                  // …and saw ESC inside it
)

// Scanner is a streaming, cross-chunk-safe signal extractor. The zero value is ready to use.
// It is NOT safe for concurrent use: give each PTY read loop its own.
type Scanner struct {
	st      scanState
	pending []byte
	// oscSkip records that the string currently being skipped is an over-long OSC rather
	// than a DCS/APC/PM/SOS. It decides which terminators count: an OSC may end on BEL,
	// the others may not (only ST), and getting that backwards is exactly how a BEL buried
	// in binary image data would be reported as a bell.
	oscSkip bool
	skipped int
	kitty   map[string]*kittyPartial
}

// kittyPartial accumulates a chunked OSC 99 notification (kitty sends d=0 for "more coming").
type kittyPartial struct {
	title string
	body  string
}

// Feed scans one chunk of PTY output and returns the signals it contains, in order.
// It never modifies p and never retains it (only parsed copies are kept).
func (s *Scanner) Feed(p []byte) []Signal {
	var out []Signal
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch s.st {
		case stGround:
			switch c {
			case bel:
				// A BEL in ground state is a real bell. A BEL that TERMINATES an OSC is
				// consumed by the stOSC branch instead, so a window-title update
				// (ESC ] 0 ; title BEL) can never be mistaken for one — that
				// misdetection would fire on nearly every shell prompt.
				out = append(out, Signal{Kind: KindBell})
			case esc:
				s.st = stEsc
			}

		case stEsc:
			switch c {
			case ']':
				s.st = stOSC
				s.pending = s.pending[:0]
			case 'P', '_', '^', 'X':
				// DCS / APC / PM / SOS: string sequences whose payload is opaque to us
				// (tmux passthrough, kitty graphics, sixel). We skip their CONTENT rather
				// than scanning it, so a 0x07 byte inside an image payload cannot be
				// reported as a bell.
				s.st = stSkip
				s.skipped, s.oscSkip = 0, false
			case esc:
				// ESC ESC — the second ESC introduces the real sequence; stay put.
			default:
				// CSI (ESC [ …) and the two-byte escapes (ESC = / ESC M / ESC ( B …).
				// Every byte they can contain is printable, so ground state is safe:
				// no bell can hide inside them.
				s.st = stGround
			}

		case stOSC:
			switch {
			case c == bel: // BEL is a legal OSC terminator (xterm)
				out = s.emitOSC(out)
			case c == esc:
				s.st = stOSCEsc
			case c < 0x20:
				// Any other C0 control aborts the string (what xterm does). Prevents a
				// runaway "ESC ]" from swallowing the rest of the session's output.
				s.reset()
			default:
				if len(s.pending) >= MaxPendingBytes {
					// Over budget: abandon the payload but keep tracking the sequence so
					// its terminator is consumed rather than read as a bell.
					s.pending = s.pending[:0]
					s.st = stSkip
					s.skipped, s.oscSkip = 0, true
					continue
				}
				s.pending = append(s.pending, c)
			}

		case stOSCEsc:
			if c == '\\' { // ST = ESC \
				out = s.emitOSC(out)
				continue
			}
			// ESC not followed by '\' aborts the OSC and starts a NEW escape sequence whose
			// introducer is this very byte — rewind one so the stEsc arm sees it.
			s.reset()
			s.st = stEsc
			i--

		case stSkip:
			switch {
			case c == esc:
				s.st = stSkipEsc
			case c == bel && s.oscSkip:
				s.st = stGround // an OSC (even an over-long one) may end on BEL
			case c == 0x18 || c == 0x1a:
				s.st = stGround // CAN / SUB abort any string sequence
			default:
				// Deliberately NOT terminating on BEL for DCS/APC/PM/SOS: those end on ST
				// only, and their payloads are binary. Bounded by maxSkipBytes so a string
				// that is never terminated cannot deafen the scanner permanently.
				if s.skipped++; s.skipped > maxSkipBytes {
					s.st = stGround
				}
			}

		case stSkipEsc:
			if c == '\\' {
				s.st = stGround
				continue
			}
			s.st = stEsc
			i--
		}
	}
	return out
}

// reset returns the machine to ground and drops any partial body.
func (s *Scanner) reset() {
	s.st = stGround
	s.pending = s.pending[:0]
}

// emitOSC parses the completed OSC body, appends any resulting signal, and returns to ground.
func (s *Scanner) emitOSC(out []Signal) []Signal {
	body := string(s.pending)
	s.reset()
	if sig, ok := s.parseOSC(body); ok {
		out = append(out, sig)
	}
	return out
}

// parseOSC turns one OSC body (everything between "ESC ]" and the terminator) into a Signal.
// Unknown OSC numbers — titles, colors, hyperlinks, clipboard, shell-integration marks — are
// dropped: they are not statements about the user's attention.
func (s *Scanner) parseOSC(body string) (Signal, bool) {
	num, rest, ok := strings.Cut(body, ";")
	if !ok {
		return Signal{}, false // no argument at all (e.g. OSC 104 palette reset)
	}
	switch num {
	case "9":
		// iTerm2 growl notification: OSC 9 ; <text>.
		//
		// The same number is ConEmu's/Windows-Terminal's multiplexed control channel
		// (9;4 = progress bar, 9;9 = report cwd, …), which some shell integrations emit on
		// EVERY prompt. Those all take the form "<digits>;…", a shape a human-written
		// notification body essentially never starts with, so treat it as the control
		// channel and stay quiet rather than notify once per prompt.
		if isConEmuSubCommand(rest) {
			return Signal{}, false
		}
		return notifySignal("", rest)
	case "777":
		// OSC 777 ; notify ; <title> ; <body>  (urxvt extension, honoured by many emulators).
		parts := strings.SplitN(rest, ";", 3)
		if len(parts) < 2 || !strings.EqualFold(parts[0], "notify") {
			return Signal{}, false
		}
		title := parts[1]
		text := ""
		if len(parts) == 3 {
			text = parts[2] // the body keeps any further ';' — only the first two fields are fixed
		}
		return notifySignal(title, text)
	case "99":
		// kitty desktop notification: OSC 99 ; <metadata> ; <payload>.
		return s.parseKitty(rest)
	}
	return Signal{}, false
}

// notifySignal builds a KindNotify, rejecting the empty case: a notification with neither
// title nor body says nothing, and reporting it as "needs you" would be a false alarm.
func notifySignal(title, body string) (Signal, bool) {
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if title == "" && body == "" {
		return Signal{}, false
	}
	return Signal{Kind: KindNotify, Title: title, Body: body}, true
}

// isConEmuSubCommand reports whether an OSC 9 argument is "<digits>;…" — see parseOSC.
func isConEmuSubCommand(rest string) bool {
	head, _, ok := strings.Cut(rest, ";")
	if !ok || head == "" {
		return false
	}
	_, err := strconv.Atoi(head)
	return err == nil
}

// parseKitty handles OSC 99 ; <k=v:k=v…> ; <payload>.
//
// Keys used: i (notification identifier), p (payload type — "title" by default, per the kitty
// spec), d (0 = more chunks follow), e (1 = payload is base64). Everything else is ignored;
// the point is the human-readable text, not full protocol fidelity.
func (s *Scanner) parseKitty(rest string) (Signal, bool) {
	meta, payload, _ := strings.Cut(rest, ";")
	id, ptype, done, b64 := "", "title", true, false
	for _, kv := range strings.Split(meta, ":") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "i":
			id = v
		case "p":
			ptype = v
		case "d":
			done = v != "0"
		case "e":
			b64 = v == "1"
		}
	}
	if b64 {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return Signal{}, false // undecodable chunk: drop it rather than show mojibake
		}
		payload = string(decoded)
	}

	// Fast path: a complete single-chunk notification needs no bookkeeping.
	if done && s.kitty[id] == nil {
		if ptype == "body" {
			return notifySignal("", payload)
		}
		return notifySignal(payload, "")
	}

	part := s.kitty[id]
	if part == nil {
		if len(s.kitty) >= maxKittyPartials {
			return Signal{}, false // too many unfinished notifications: ignore new ones
		}
		if s.kitty == nil {
			s.kitty = map[string]*kittyPartial{}
		}
		part = &kittyPartial{}
		s.kitty[id] = part
	}
	if ptype == "body" {
		part.body += payload
	} else {
		part.title += payload
	}
	if len(part.title)+len(part.body) > MaxPendingBytes {
		delete(s.kitty, id) // same budget as an OSC body — an endless chunk stream is dropped
		return Signal{}, false
	}
	if !done {
		return Signal{}, false
	}
	delete(s.kitty, id)
	return notifySignal(part.title, part.body)
}
