// Differential vectors for muxd's terminal model: random streams + resizes run through xterm.js
// headless 6.0.0 (the frontend's version); the expected state is whatever xterm ends up showing.
//
//   node gen_vectors.mjs <out.json> resize|noresize [count] [splits] [firstSeed]
//                                       (count default 400, splits 0.06, firstSeed 1)
//
// `noresize` leaves the resizes out, which isolates the base semantics (what each sequence does)
// from the reflow. muxd/xterm_parity_test.go replays the output through the model; the committed set
// is muxd/testdata/xterm_reflow_vectors.json (`resize`, 300). A seed is a vector: the same seed gives
// the same stream, so a failing "seedN" can be re-run alone.
//
// Streams are written to xterm as BYTES (a Uint8Array), as the browser does (useTerminalChannel.ts
// writes `new Uint8Array(data)`), so xterm's UTF-8 decoder is in play: an op is a string (encoded as
// UTF-8) or a Buffer (raw bytes, invalid UTF-8 included).
import { writeFileSync } from 'node:fs'
const NORESIZE = process.argv[3] === 'noresize'
const SPLITS = Number(process.argv[5] ?? 0.06)   // the share of writes cut in two (0 for none)
const FIRST_SEED = Number(process.argv[6] ?? 1)   // seeds run FIRST_SEED, FIRST_SEED+1, ... (to look further than the committed set)
import { newTerminal } from './xterm_env.mjs'

function rng(seed) { return () => { seed |= 0; seed = seed + 0x6D2B79F5 | 0; let t = Math.imul(seed ^ seed >>> 15, 1 | seed); t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t; return ((t ^ t >>> 14) >>> 0) / 4294967296 } }
const pick = (r, a) => a[Math.floor(r() * a.length)]
const int = (r, lo, hi) => lo + Math.floor(r() * (hi - lo + 1))

// What an agent's output is full of: emoji that Unicode 11 draws at two columns (the robot, the money bag,
// the brain, the keycap's base, a thumbs-up and its skin tone, a flag's regional indicators, a ZWJ family),
// and the symbols it draws at one, with and without a variation selector (the record button, the
// teardrop asterisk, the warning sign).
const EMOJI = [
  '\u{1f916}', '\u{1f4b0}', '\u{1f9e0}', '\u2728', '\u2705', '\u23fa', '\u273b', '\u26a0\ufe0f', '\u26a0', '\u2b50',
  '\u{1f44d}\u{1f3fd}', '\u{1f1e6}\u{1f1e7}', '1\ufe0f\u20e3', '\u{1f468}\u200d\u{1f469}\u200d\u{1f467}', '\u2764\ufe0f',
]

function word(r) {
  const kind = r()
  if (kind < 0.15) return '\u4e2d\u6587\u5bbd\u5b57'.slice(0, int(r, 1, 4))
  if (kind < 0.2) return ' '.repeat(int(r, 1, 3))
  if (kind < 0.26) return pick(r, EMOJI) + (r() < 0.5 ? pick(r, EMOJI) : '')
  const letters = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let w = ''
  for (let i = int(r, 1, 9); i > 0; i--) w += letters[int(r, 0, letters.length - 1)]
  return w
}
function line(r, maxLen) { let s = ''; while (s.length < maxLen) s += word(r) + (r() < 0.7 ? ' ' : ''); return s }

// What full-screen programs move and edit with: relative and absolute motion (xterm stops CUU/CUD
// at the region's margins), save/restore, scrolling the region, index/next-line, autowrap off,
// insert mode, REP (ncurses emits it for runs of one character), and the settings a stream can flip
// under the model's feet: LNM, DECSC through the 1048 spelling, DECSTR and RIS.
function motion(r, st) {
  const n = int(r, 1, Math.max(st.rows, st.cols) - 1)
  switch (int(r, 0, 19)) {
    case 0: return `\x1b[${n}${pick(r, ['A', 'B', 'C', 'D'])}` + (r() < 0.5 ? word(r) : '')
    case 1: return `\x1b[${int(r, 1, st.rows)}${pick(r, ['E', 'F'])}` + word(r)
    case 2: return `\x1b[${int(r, 1, st.cols)}G` + word(r)
    case 3: return `\x1b[${int(r, 1, st.rows)}d` + word(r)
    case 4: return `\x1b[${n}${pick(r, ['a', 'e'])}` + word(r)
    case 5: return pick(r, ['\x1b7', '\x1b[s']) + word(r) + `\x1b[${int(r, 1, st.rows)};${int(r, 1, st.cols)}H` + word(r) + pick(r, ['\x1b8', '\x1b[u']) + word(r)
    case 6: return `\x1b[${int(r, 1, 3)}${pick(r, ['S', 'T'])}`
    case 7: return pick(r, ['\x1bD', '\x1bE']) + word(r)
    case 8: return '\x1b[?7l' + line(r, int(r, 1, st.cols * 2)) + (r() < 0.6 ? '\x1b[?7h' : '')
    case 9: return '\x1b[?7h'
    case 10: return '\x1b[4h' + word(r) + (r() < 0.7 ? '\x1b[4l' : '')
    case 11: return '\x1b[4l'
    case 12: return word(r) + `\x1b[${int(r, 1, st.cols * 2)}b`
    case 13: return pick(r, ['\x1b8', '\x1b[u']) + word(r)
    case 14: return `\x1b[${int(r, 1, st.rows)};${int(r, 1, st.cols)}H` + pick(r, ['\x1b7', '\x1b[s'])
    case 15: return `\x1b[${int(r, 1, 3)}X` + word(r)
    case 16: return pick(r, ['\x1b[?1048h', '\x1b[?1048l']) + word(r)
    case 17: return pick(r, ['\x1b[20h', '\x1b[20l']) + line(r, int(r, 1, st.cols)) + '\n'
    case 18: return '\x1b[!p' + word(r)
    default: if (r() < 0.3) { st.alt = false; return '\x1bc' + word(r) } else return `\x1b[${int(r, 1, 3)}X` + word(r)
  }
}


// ── Four classes of input the base generator above does not reach ─────────────────────────────────
// Combining characters, character sets, bytes that are not UTF-8, and control characters inside a
// control sequence. Each returns a string or a Buffer; the weights are low, so one vector mixes them
// with everything above.

// Zero-width in xterm's Unicode 11 table, so they stack on the character before: acute, diaeresis,
// cedilla, ZWJ, VS16, enclosing keycap, Thai vowel, Arabic fatha, Devanagari stress sign, kana voiced
// mark (inside the wide CJK block), Hangul medial vowel (a letter to Unicode, zero-width to xterm),
// and two outside the BMP.
const MARKS = ['\u0301', '\u0308', '\u0327', '\u200d', '\ufe0f', '\u20e3', '\u0e31', '\u064b', '\u0951', '\u3099', '\u1160', '\u{e0100}', '\u{1d167}']
// Marks to Unicode (versions 12 to 14 added them), but xterm's Unicode 11 table gives them a column: an
// Arabic small high word, an Arabic small low waw, a Combining Diacritical Marks Extended mark, a
// Malayalam candrabindu; and the soft hyphen, which is drawn.
const NOT_MARKS = ['\u0898', '\u08ca', '\u1ac1', '\u0d81', '\u00ad']
// The edges of the width table: angle brackets and a watch (wide), an unassigned slot before the kana, the
// half fill space, the hexagrams (narrow), Hangul Jamo Extended-A (wide), a skin tone (wide), a regional
// indicator (narrow), and the planes that are wide.
const EDGE_WIDTHS = ['\u2329', '\u232a', '\u231a', '\u3040', '\u303f', '\u4dc0', '\ua960', '\u{1f3fb}', '\u{1f1e6}', '\u{20000}', '\u{2fffd}']
const BASES = ['e', 'a', 'z', 'x', '1', ' ', '\u4e2d', '\u5b57', '\u0e01', '\u0627', '\u{1f916}', '\u26a0']
const marks = (r, n) => { let s = ''; for (let i = 0; i < n; i++) s += pick(r, MARKS); return s }
const cluster = r => pick(r, BASES) + marks(r, int(r, 0, 3))

function combining(r, st) {
  const row = int(r, 1, st.rows)
  switch (int(r, 0, 12)) {
    case 0: return cluster(r) + word(r) + cluster(r)
    case 1: return '\x1b[0m' + marks(r, int(r, 1, 2)) + word(r)                       // nothing to stack on
    case 2: return pick(r, ['\r', '\r\n', '\n', '\b']) + marks(r, int(r, 1, 2)) + word(r)
    case 3: return `\x1b[${row};${st.cols}H` + 'x' + marks(r, 1) + 'y'               // joins at the margin, then wraps
    case 4: return `\x1b[${row};${st.cols}H` + '\u4e2d' + marks(r, 1) + word(r)      // wide character wrapped, then a mark
    case 5: return cluster(r) + `\x1b[${int(r, 1, 4)}b`                              // REP of a combined cell
    case 6: return '\x1b[4h' + cluster(r) + cluster(r) + (r() < 0.7 ? '\x1b[4l' : '')
    case 7: return cluster(r) + cluster(r) + cluster(r) + `\x1b[${int(r, 1, 3)}D` + pick(r, ['\x1b[P', '\x1b[@', '\x1b[X', '\x1b[K', '\x1b[1K'])
    case 8: return '\x1b[?7l' + cluster(r) + line(r, st.cols * 2) + cluster(r) + (r() < 0.6 ? '\x1b[?7h' : '')
    case 9: return `\x1b[${st.rows};1H` + cluster(r) + cluster(r) + '\r\n' + word(r)   // scrolls a combined row away
    case 10: return pick(r, BASES) + pick(r, ['\x7f', '\x7f\x7f']) + marks(r, 1) + word(r)   // DEL ends the run but not the join
    case 11: return pick(r, NOT_MARKS.concat(EDGE_WIDTHS)) + marks(r, int(r, 0, 1)) + word(r)
    default: return cluster(r) + `\x1b[${int(r, 1, 3)}C` + cluster(r)
  }
}

// G0..G3 designation (ESC ( ) * + - . /), the locking shifts, SI/SO, and DEC Special Graphics.
const CHARSET_FLAGS = ['0', '0', '0', 'B', 'A', '4', 'C', '5', 'R', 'Q', 'K', 'Y', 'E', '6', 'Z', 'H', '7', '=', '1', '2', '<', 'U']
const ascii = (r, n) => { let s = ''; for (let i = 0; i < n; i++) s += String.fromCharCode(int(r, 0x20, 0x7e)); return s }
const drawing = (r, n) => { let s = ''; for (let i = 0; i < n; i++) s += pick(r, 'lqqqkxxmjntuvwa`~{|}yzfgo'); return s }

function charsets(r, st) {
  const sh = pick(r, ['(', ')', '*', '+', '-', '.', '/'])
  switch (int(r, 0, 12)) {
    case 0: return '\x1b(0' + drawing(r, int(r, 2, 12)) + '\x1b(B' + word(r)
    case 1: return '\x1b)0' + '\x0e' + drawing(r, int(r, 2, 8)) + '\x0f' + drawing(r, int(r, 1, 4))
    case 2: return `\x1b${sh}${pick(r, CHARSET_FLAGS)}` + ascii(r, int(r, 3, 14))
    case 3: return `\x1b${pick(r, ['*', '+', '-', '.'])}${pick(r, CHARSET_FLAGS)}` + pick(r, ['\x1bn', '\x1bo', '\x1b}', '\x1b|', '\x1b~', '\x0e']) + ascii(r, int(r, 2, 8))
    case 4: return '\x1b(0' + pick(r, ['\x1b7', '\x1b[s']) + '\x1b(B' + word(r) + pick(r, ['\x1b8', '\x1b[u']) + drawing(r, int(r, 1, 5))   // DECSC keeps the charset
    case 5: return '\x1b(0' + '\x1b)0' + '\x0e' + pick(r, ['\x1b%G', '\x1b%@']) + drawing(r, 4)
    case 6: return '\x1b)0\x0e' + pick(r, ['\x1b[?2h', '\x1b[!p', '\x1b[?2l']) + drawing(r, 4)
    case 7: if (r() < 0.4) { st.alt = false; return '\x1b(0\x0e\x1bc' + drawing(r, 4) } else return '\x1b(0' + pick(r, ['\x1b[!p', '\x1b[0m']) + drawing(r, 4)
    case 8: return pick(r, ['\x1b(0', '\x1b(4', '\x1b(K']) + pick(r, ['x', 'q', '[', '{', '~']) + `\x1b[${int(r, 1, 6)}b`    // REP of a mapped character
    case 9: return '\x1b(0' + '\x1b[4h' + drawing(r, int(r, 2, 6)) + '\x1b[4l' + '\x1b(B'
    case 10: return '\x1b(0' + drawing(r, st.cols + int(r, 0, st.cols)) + '\x1b(B' + word(r)     // wraps
    case 11: return '\x0e' + drawing(r, 3) + '\x0f' + drawing(r, 3) + '\x0e' + ascii(r, 5)
    default: return '\x1b(A' + '#' + ascii(r, 6) + '\x1b(0' + '\x1b7' + '\x1b(B' + '\x1b8' + ascii(r, 4)
  }
}

// Bytes that are not UTF-8, around bytes that are. Always led by ASCII: xterm's decoder keeps a cut
// sequence between writes, and a continuation byte arriving first would meet that state (whose own
// quirks are not what is being compared).
const BAD = [
  [0xff], [0xfe], [0x80], [0xbf], [0xc0, 0x80], [0xc1, 0xbf], [0xc2], [0xe2, 0x82], [0xe2], [0xf0, 0x9f], [0xf0, 0x9f, 0x98], [0xf0, 0xa0, 0x80],
  [0xe0, 0x80, 0x80], [0xed, 0xa0, 0x80], [0xf0, 0x80, 0x80, 0x80], [0xf4, 0x90, 0x80, 0x80], [0xf5, 0x80, 0x80, 0x80],
  [0xf8, 0x88, 0x80, 0x80, 0x80], [0xef, 0xbb, 0xbf], [0xe4, 0xb8], [0xc3, 0x41], [0xe2, 0x82, 0x41], [0xf0, 0x9f, 0x98, 0x41],
]
const GOOD = [[0xc3, 0xa9], [0xe4, 0xb8, 0xad], [0xef, 0xbf, 0xbd], [0xe2, 0x82, 0xac], [0xc3, 0xa9, 0xcc, 0x81]]
const bytes = (...parts) => Buffer.concat(parts.map(p => typeof p === 'string' ? Buffer.from(p, 'utf8') : Buffer.from(p)))

function badUtf8(r, st) {
  const lead = word(r)
  switch (int(r, 0, 6)) {
    case 0: return bytes(lead, pick(r, BAD), word(r))
    case 1: return bytes(lead, pick(r, BAD), pick(r, BAD), pick(r, GOOD), word(r))
    case 2: return bytes(lead, pick(r, GOOD), pick(r, BAD))                       // cut sequence at the end of the write
    case 3: return bytes(lead, '\x1b[3', pick(r, BAD), '1m', word(r))              // inside a CSI
    case 4: return bytes(lead, '\x1b[3', pick(r, GOOD), '1m', word(r))             // a character that is not a parameter
    case 5: return bytes(lead, pick(r, BAD), pick(r, ['\r\n', '\x1b[K', '\x1b[2D']), pick(r, BAD), word(r))
    default: return bytes(lead, '\x1b', pick(r, BAD), '[1;1H', word(r))            // inside an escape
  }
}

// A control character inside an escape or control sequence is carried out and the sequence goes on;
// CAN and SUB abandon it, ESC starts another.
const CONTROLS = ['\n', '\r', '\b', '\t', '\x07', '\x00', '\x0e', '\x0f', '\x0b', '\x0c', '\x05', '\x7f', '\x1c']
const ABORTS = ['\x18', '\x1a', '\x1b']
// seq is the part after "ESC [" (or after ESC) as tokens; controls land between tokens.
function withControls(r, seq, pool) {
  let out = ''
  for (let i = 0; i <= seq.length; i++) {
    if (i < seq.length) out += seq[i]
    if (r() < 0.35) out += pick(r, pool)
  }
  return out
}
function controlsInSequence(r, st) {
  const n = int(r, 1, Math.max(st.rows, st.cols) - 1)
  const pool = r() < 0.8 ? CONTROLS : ABORTS
  switch (int(r, 0, 14)) {
    case 0: return '\x1b[' + withControls(r, [...String(n), pick(r, ['A', 'B', 'C', 'D'])], pool) + word(r)
    case 1: return '\x1b[' + withControls(r, [...String(int(r, 1, st.rows)), ';', ...String(int(r, 1, st.cols)), 'f'], pool) + word(r)   // 'f', not 'H': ESC H sets a tab stop, which the model does not keep
    case 2: return '\x1b[' + withControls(r, ['3', String(int(r, 0, 7)), 'm'], pool) + word(r) + '\x1b[0m'
    case 3: {
      const f = pick(r, ['K', 'J'])
      // Not ED 1: the base ops leave it out (it crashes xterm at the bottom-right corner), and the model does not
      // follow xterm's clearing of the wrap flag it leaves on a line of the scrollback.
      return '\x1b[' + withControls(r, [pick(r, f === 'J' ? ['0', '2', ''] : ['0', '1', '2', '']), f], pool) + word(r)
    }
    case 4: return word(r) + '\x1b[' + withControls(r, [String(int(r, 1, 3)), pick(r, ['@', 'P', 'X', 'L', 'M'])], pool) + word(r)
    case 5: return word(r) + '\x1b[' + withControls(r, [String(int(r, 1, 5)), 'b'], pool) + word(r)
    case 6: return '\x1b[' + withControls(r, ['?', '7', pick(r, ['l', 'h'])], pool) + line(r, int(r, 1, st.cols + 3))
    case 7: return '\x1b[' + withControls(r, ['4', pick(r, ['h', 'l'])], pool) + word(r)
    case 8: return '\x1b' + withControls(r, ['(', '0'], pool) + drawing(r, 4) + '\x1b(B'            // inside a charset designation
    case 9: return '\x1b' + withControls(r, [pick(r, ['M', 'D', 'E', '7', '8'])], pool) + word(r)
    case 10: return '\x1b[' + withControls(r, ['2', ' ', 'q'], pool) + word(r)                        // an intermediate byte
    case 11: return word(r) + '\x1b[' + withControls(r, ['3', ';', '2', '$', 'p'], pool) + word(r)    // an intermediate this model does not know
    case 12: return '\x1b[' + withControls(r, [String(n), '\n', pick(r, ['A', 'D'])], pool) + word(r) // a line feed in the middle, for certain
    case 13: return '\x1b[' + withControls(r, ['1', ':', '3', '1', 'm'], pool) + word(r) + '\x1b[0m'
    default: return '\x1b[' + withControls(r, ['1', '?', 'h'], pool) + word(r)                        // a prefix after a parameter: ignored
  }
}

// C1 controls arrive as valid UTF-8 (U+0080..U+009F, C2 80..C2 9F): IND and NEL act, the introducers
// begin sequences, the rest do nothing -- and none of them is ever printed. (Not U+0088, HTS: it sets a
// tab stop, which the model does not keep, any more than it does for ESC H.)
function c1(r, st) {
  const n = int(r, 1, Math.max(st.rows, st.cols) - 1)
  switch (int(r, 0, 9)) {
    case 0: return word(r) + '\u0084' + word(r)
    case 1: return word(r) + '\u0085' + word(r)
    case 2: return word(r) + pick(r, ['\u0080', '\u0081', '\u0086', '\u0087', '\u0097', '\u0099', '\u009a']) + word(r)
    case 3: return '\u009b' + `3${int(r, 1, 7)}m` + word(r) + '\x1b[0m'
    case 4: return '\u009b' + `${n}${pick(r, ['A', 'B', 'C', 'D'])}` + word(r)
    case 5: return '\u009b' + `${int(r, 1, st.rows)};${int(r, 1, st.cols)}H` + word(r)
    case 6: return '\x1b[' + withControls(r, [String(n), pick(r, ['\u0084', '\u0085', '\u008f', '\u0081']), 'A'], CONTROLS) + word(r)   // a C1 control ends a sequence
    case 7: return '\x1b[3' + pick(r, ['\u009b', '\u0084']) + '1m' + word(r)
    case 8: return '\u009b' + withControls(r, [String(n), 'D'], CONTROLS) + word(r)
    default: return '\x1b' + pick(r, ['\u0084', '\u0085', '\u0081']) + word(r)
  }
}

// OSC, DCS and the SOS/PM/APC strings, ended every way xterm ends them -- and not by the ways it does
// not (BEL ends an OSC and nothing else).
const OSC_BODY = ['0;title', '2;a b c', '0;', '7;file:///tmp']
const STRING_ENDS = ['\x07', '\x1b\\', '\x18', '\x1a', '\u009c', '\x1b[0m', '\x1b[31m', '\u0085', '\x1b7']
function strings(r, st) {
  const end = () => pick(r, STRING_ENDS)
  switch (int(r, 0, 7)) {
    case 0: return word(r) + '\x1b]' + pick(r, OSC_BODY) + end() + word(r)
    case 1: return word(r) + '\u009d' + pick(r, OSC_BODY) + end() + word(r)
    case 2: return word(r) + '\x1bP' + word(r) + pick(r, ['', '\x07', '\n', '\x7f']) + word(r) + end() + word(r)
    case 3: return word(r) + pick(r, ['\x1bX', '\x1b^', '\x1b_', '\u0098', '\u009e', '\u009f']) + word(r) + pick(r, ['', '\x07', '\r']) + end() + word(r)
    case 4: return word(r) + '\x1b]' + pick(r, OSC_BODY) + pick(r, ['\n', '\r', '\x00', '\x7f']) + pick(r, OSC_BODY) + end() + word(r)
    case 5: return word(r) + '\u0090' + word(r) + end() + word(r)
    case 6: return '\x1b[' + withControls(r, ['1', pick(r, [']', 'P', 'X', '^', '_'])], ABORTS) + word(r) + end() + word(r)   // an introducer after an abandoned CSI
    default: return word(r) + '\x1b]' + pick(r, OSC_BODY) + '\u00e9\u4e2d' + end() + word(r)
  }
}

function exotic(r, st) {
  switch (int(r, 0, 5)) {
    case 0: return combining(r, st)
    case 1: return charsets(r, st)
    case 2: return badUtf8(r, st)
    case 3: return c1(r, st)
    case 4: return strings(r, st)
    default: return controlsInSequence(r, st)
  }
}

function op(r, st) {
  const x = r()
  if (x < 0.30) return line(r, int(r, 1, st.cols * (r() < 0.3 ? 3 : 1))) + pick(r, ['\r\n', '\r\n', '\r\n', '\n', '', '\r'])
  if (x < 0.36) return `\x1b[${int(r, 1, st.rows)};${int(r, 1, st.cols)}H` + word(r)
  if (x < 0.41) return `\x1b[${pick(r, [0, 1, 2])}K`
  if (x < 0.44) return `\x1b[${pick(r, [0, 2])}J`  // ED 1 at the bottom-right corner crashes xterm 6.0.0 itself
  if (x < 0.47) return `\x1b[${int(r, 1, 3)}${pick(r, ['L', 'M', '@', 'P', 'X'])}`
  if (x < 0.50) return `\x1b[4${int(r, 1, 7)}m` + `\x1b[${pick(r, [0, 2])}K` + `\x1b[0m`  // BCE erase
  if (x < 0.53) return `\x1b[3${int(r, 1, 7)}m` + word(r) + '\x1b[0m'
  if (x < 0.55) { const t = int(r, 1, st.rows - 1); return `\x1b[${t};${int(r, t + 1, st.rows)}r` }
  if (x < 0.57) return '\x1b[r'
  if (x < 0.59) return '\t' + word(r)
  // The three spellings of the alternate screen differ in what they save and clear (1049 saves the
  // cursor, 47 and 1047 do not), and xterm does not care which one left what another entered.
  if (x < 0.61 && st.altOK) {
    st.alt = !st.alt
    return st.alt ? pick(r, ['\x1b[?1049h', '\x1b[?1049h', '\x1b[?1047h', '\x1b[?47h']) : pick(r, ['\x1b[?1049l', '\x1b[?1049l', '\x1b[?1047l', '\x1b[?47l'])
  }
  if (x < 0.63) return '\b\b' + word(r)
  if (x < 0.65) return '\x1bM' + word(r)
  if (x < 0.66) return '\x1b[3J'
  if (x < 0.76) return motion(r, st)
  if (x < 0.86) return NORESIZE ? word(r) : 'RESIZE'
  if (x < 0.93) return exotic(r, st)
  return line(r, int(r, 1, st.cols * 2)) + '\r\n'
}

// Some streams drive xterm.js 6.0.0 into a state it cannot be said to have: the viewport has rows with
// no line behind them (seen after the alternate screen is enlarged while it still holds lines above
// the screen), and the next line feed throws from inside its write loop. Such a stream has no state to
// compare against, so it is dropped rather than recorded.
function consistent(term) {
  // lines.length is not on the public API, and getLine() does not check it (a line past the end is
  // whatever the circular list still holds there), so this reads xterm's own bookkeeping.
  const b = term._core.buffer
  return b.lines.length >= b.ybase + term.rows
}

// Another one: BufferLine.copyCellsFrom copies the combining data of every cell at or after the one it
// starts from, not of the cells it copies, so a resize can leave a cell showing the marks of another
// (an emoji joined to another family member, an "e" with the accents of a "u"). Reflow moves cells and
// may cut some off; the marks that are left, read in buffer order, are a subsequence of what went in. A
// stream whose resize leaves anything else has xterm contradicting itself and is dropped.
function marksInOrder(term) {
  const lines = term._core.buffers.normal.lines
  const out = []
  for (let i = 0; i < lines.length; i++) {
    const l = lines.get(i)
    for (let x = 0; x < l.length; x++) if (l.isCombined(x)) out.push(l.getString(x))
  }
  return out
}
function isSubsequence(after, before) {
  let i = 0
  for (const m of before) if (i < after.length && after[i] === m) i++
  return i === after.length
}

let onCrash = null
process.on('uncaughtException', e => {
  if (!onCrash) throw e
  const f = onCrash
  onCrash = null
  f(e)
})
const write = (term, data) => new Promise((res, rej) => { onCrash = rej; term.write(data, () => { onCrash = null; res() }) })

// Where to cut a write in two, or -1. Not inside a multi-byte character that has a 0x80 byte in it:
// xterm's decoder, handed a character in two pieces, loses track of a continuation byte whose low six
// bits are zero (it stops counting at the first such byte), which is a defect of its own and not what
// is being compared.
function splitPoint(r, b) {
  if (SPLITS === 0 || b.length < 2 || r() >= SPLITS) return -1
  const p = int(r, 1, b.length - 1)
  if ((b[p] & 0xc0) === 0x80) {
    let s = p, e = p
    while (s > 0 && (b[s] & 0xc0) === 0x80) s--
    while (e < b.length && (b[e] & 0xc0) === 0x80) e++
    for (let k = s; k < e; k++) if (b[k] === 0x80) return -1
  }
  return p
}

async function run(seed, altOK) {
  const r = rng(seed)
  const st = { cols: int(r, 8, 30), rows: int(r, 3, 12), alt: false, altOK }
  const term = newTerminal({ cols: st.cols, rows: st.rows })
  const v = { name: `seed${seed}${altOK ? '-alt' : ''}`, cols: st.cols, rows: st.rows, ops: [] }
  const n = int(r, 20, 120)
  for (let i = 0; i < n; i++) {
    const o = op(r, st)
    if (o === 'RESIZE') {
      st.cols = int(r, 6, 34); st.rows = int(r, 3, 14)
      const marks = marksInOrder(term)
      term.resize(st.cols, st.rows)
      if (!consistent(term) || !isSubsequence(marksInOrder(term), marks)) { term.dispose(); return null }
      v.ops.push({ resize: [st.cols, st.rows] })
    } else {
      const whole = typeof o === 'string' ? Buffer.from(o, 'utf8') : o
      // Some writes arrive in two pieces, cut anywhere a read from a pty could cut them.
      const at = splitPoint(r, whole)
      for (const b of at < 0 ? [whole] : [whole.subarray(0, at), whole.subarray(at)]) {
        try { await write(term, new Uint8Array(b)) } catch { term.dispose(); return null }
        if (!consistent(term)) { term.dispose(); return null }
        v.ops.push({ w: b.toString('base64') })
      }
    }
  }
  try { await write(term, '') } catch { term.dispose(); return null }
  const dump = (buf, from, to) => { const out = []; for (let i = from; i < to; i++) { const l = buf.getLine(i); out.push({ t: l ? l.translateToString(true) : '', w: l ? l.isWrapped : false }) } return out }
  const a = term.buffer.active, nb = term.buffer.normal
  v.expect = {
    alt: a.type === 'alternate',
    cursor: [a.cursorX, a.cursorY],
    screen: dump(a, a.baseY, a.baseY + st.rows),
    normalScreen: dump(nb, nb.baseY, nb.baseY + st.rows),
    scrollback: dump(nb, 0, nb.baseY),
  }
  term.dispose()
  return v
}

const COUNT = Number(process.argv[4] || 400)
const vectors = []
let dropped = 0
for (let s = FIRST_SEED; vectors.length < COUNT; s++) {
  const v = await run(s, s % 3 === 0)
  if (v) vectors.push(v)
  else dropped++
}
writeFileSync(process.argv[2], JSON.stringify(vectors))
console.log('wrote', vectors.length, 'vectors', dropped ? `(dropped ${dropped} streams that leave xterm itself inconsistent)` : '')
