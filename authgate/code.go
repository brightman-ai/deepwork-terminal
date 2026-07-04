package authgate

import (
	"crypto/subtle"
	"strings"
	"unicode"
)

// NormalizeCode canonicalizes an auth code for comparison: upper-cases it and
// drops every hyphen and whitespace rune. The display form is grouped for
// readability (e.g. "E3X1-M6T2"), but a user may type it lowercase, without the
// dash, or with a stray space — all denote the same code.
//
// This drops NO entropy: the generated alphabet is uppercase A–Z/2–9 (no spaces
// or hyphens), so the separators and case carry no information.
//
// SSOT: this is the ONE canonical form. Every auth boundary — the standalone
// terminal authWrap and deepwork-pro's WebUI middleware — normalizes through here
// so they can never disagree on what a code means.
func NormalizeCode(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range code {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// CodeMatches reports whether provided equals expected after normalization, using
// a constant-time comparison so a remote attacker can't time-probe the code
// byte-by-byte (the mesh/tunnel widens who can guess at it). An empty expected
// never matches — an unset code must not be satisfiable by empty input.
func CodeMatches(provided, expected string) bool {
	e := NormalizeCode(expected)
	if e == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(NormalizeCode(provided)), []byte(e)) == 1
}
