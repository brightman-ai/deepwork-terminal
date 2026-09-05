package muxd

import (
	"sort"
	"unicode"
)

// How many columns a rune occupies.
//
// ── Why this is here at all ─────────────────────────────────────────────────────────────────────
// The screen model this file serves replays a byte stream onto a fixed grid. If it advances one
// column per rune, then a line of Chinese — which a real terminal draws at TWO columns per
// character — wraps in our model at twice the character count it wraps at in reality. Everything
// below that line then sits on the wrong row, and a TUI that addresses rows absolutely paints
// into the gap. The existing preview grid has this bug today; it is tolerable there (a preview is
// read as a picture) and NOT tolerable for scrollback that a person will scroll through and copy
// out of. The user's own terminals are full of Chinese.
//
// ── Why not a dependency ────────────────────────────────────────────────────────────────────────
// `golang.org/x/text/width` would do this properly, and go.mod is deliberately small (stdlib plus
// four genuinely load-bearing libraries). Widths are also not a moving target in the range that
// matters: the CJK and Hangul blocks have been stable for two decades. The table below is the
// standard wcwidth East-Asian-Wide/Fullwidth set, and stdlib's `unicode` package already provides
// the combining-mark categories, which is the other half of the problem.
//
// ── Honest limits (△) ───────────────────────────────────────────────────────────────────────────
// Terminals genuinely disagree about emoji and about a handful of "ambiguous width" codepoints
// (line-drawing characters, some Greek and Cyrillic) whose width depends on the font and on a
// terminal setting. We follow the common modern choice: emoji wide, ambiguous narrow — the same
// call xterm.js makes, which is what renders the other side of this system. Where we are wrong,
// we are wrong about WRAPPING only: the characters themselves are stored and returned intact.

// wideRanges lists the codepoint ranges drawn at two columns, sorted and non-overlapping so it can
// be binary-searched. Derived from Unicode's East_Asian_Width = Wide or Fullwidth.
var wideRanges = [...][2]rune{
	{0x1100, 0x115F},   // Hangul Jamo initial consonants
	{0x2E80, 0x303E},   // CJK radicals, Kangxi, CJK symbols and punctuation
	{0x3041, 0x33FF},   // Hiragana, Katakana, Bopomofo, Hangul compat, CJK compat
	{0x3400, 0x4DBF},   // CJK unified ideographs extension A
	{0x4E00, 0x9FFF},   // CJK unified ideographs
	{0xA000, 0xA4CF},   // Yi syllables and radicals
	{0xA960, 0xA97F},   // Hangul Jamo extended-A
	{0xAC00, 0xD7A3},   // Hangul syllables
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFE10, 0xFE19},   // vertical forms
	{0xFE30, 0xFE6F},   // CJK compatibility forms, small form variants
	{0xFF00, 0xFF60},   // fullwidth ASCII forms
	{0xFFE0, 0xFFE6},   // fullwidth signs
	{0x16FE0, 0x16FE4}, // Tangut/Nushu marks
	{0x17000, 0x18AFF}, // Tangut, Tangut components
	{0x1B000, 0x1B12F}, // Kana supplement / extended-A
	{0x1B170, 0x1B2FF}, // Nushu
	{0x1F004, 0x1F004}, // 🀄
	{0x1F0CF, 0x1F0CF}, // 🃏
	{0x1F18E, 0x1F18E}, // 🆎
	{0x1F191, 0x1F19A}, // 🆑–🆚
	{0x1F200, 0x1F320}, // enclosed ideographic supplement + early emoji
	{0x1F32D, 0x1F335},
	{0x1F337, 0x1F37C},
	{0x1F37E, 0x1F393},
	{0x1F3A0, 0x1F3CA},
	{0x1F3CF, 0x1F3D3},
	{0x1F3E0, 0x1F3F0},
	{0x1F3F4, 0x1F3F4},
	{0x1F3F8, 0x1F43E},
	{0x1F440, 0x1F440},
	{0x1F442, 0x1F4FC},
	{0x1F4FF, 0x1F53D},
	{0x1F54B, 0x1F54E},
	{0x1F550, 0x1F567},
	{0x1F57A, 0x1F57A},
	{0x1F595, 0x1F596},
	{0x1F5A4, 0x1F5A4},
	{0x1F5FB, 0x1F64F},
	{0x1F680, 0x1F6C5},
	{0x1F6CC, 0x1F6CC},
	{0x1F6D0, 0x1F6D2},
	{0x1F6EB, 0x1F6EC},
	{0x1F6F4, 0x1F6FC},
	{0x1F7E0, 0x1F7EB},
	{0x1F90C, 0x1F93A},
	{0x1F93C, 0x1F945},
	{0x1F947, 0x1F9FF},
	{0x1FA70, 0x1FAFF},
	{0x20000, 0x2FFFD}, // CJK extensions B–F
	{0x30000, 0x3FFFD}, // CJK extension G
}

// zeroWidth reports whether a rune adds nothing to the cursor: combining marks stack onto the
// PREVIOUS cell, and format characters (ZWJ, ZWNJ, directional overrides) draw nothing at all.
//
// unicode.Mn/Me are the combining categories and unicode.Cf the format one; using the stdlib
// tables rather than a hand-written range list means this half stays correct as Unicode grows.
// The soft hyphen (U+00AD) is in Cf but IS drawn by terminals, so it is excluded explicitly.
func zeroWidth(r rune) bool {
	if r == 0x00AD {
		return false
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf)
}

// runeWidth is how many grid columns a rune occupies: 0 for combining and format characters,
// 2 for East Asian wide and fullwidth ones, 1 for everything else.
//
// Control characters never reach here — the parser handles them before a rune is placed — so
// there is no -1 case to propagate. That is deliberate: a width function that can return "this is
// not printable" makes every caller handle a case the caller cannot actually receive.
func runeWidth(r rune) int {
	if r < 0x1100 {
		// Fast path: all of Latin, Greek, Cyrillic, Hebrew and Arabic, which is the
		// overwhelming majority of terminal bytes. Nothing below U+1100 is wide, and the only
		// zero-width runes there are combining marks.
		if r >= 0x0300 && zeroWidth(r) {
			return 0
		}
		return 1
	}
	if zeroWidth(r) {
		return 0
	}
	i := sort.Search(len(wideRanges), func(i int) bool { return wideRanges[i][1] >= r })
	if i < len(wideRanges) && r >= wideRanges[i][0] {
		return 2
	}
	return 1
}
