package muxd

// Character sets: what ESC ( 0 and SO mean.
//
// A program that draws a box on a terminal that has no line-drawing characters of its own does not
// send U+250C; it designates DEC Special Graphics into G0 (`ESC ( 0`) and sends 'l', or designates it
// into G1 (`ESC ) 0`) and shifts out (SO, 0x0E) for the length of the border. The bytes are ASCII and
// what lands on the screen is not, so a model that ignores the designation stores 'lqqk' where the
// browser's terminal shows a corner and a line. This is a port of xterm.js 6.0.0 — common/data/Charsets.ts,
// common/services/CharsetService.ts and the handlers in InputHandler.ts — down to its habits (the
// Dutch set maps '[' to the two letters "ij" and xterm keeps the first of them; '|' to a plain 'f').
//
// The state is four designations (G0..G3), the one in use (GL, a level 0..3), and a copy of the
// ACTIVE set that DECSC saves. Like xterm, DECRC puts back the set that was in use then, not the
// designations and not the level.

// charsetTable maps an ASCII character to what a set draws in its place; 0 means "itself".
type charsetTable [128]rune

// charsets is every set xterm knows, by the final byte that designates it. The US set is absent:
// xterm models it as no table at all, and so does every use of this map.
var charsets = map[byte]*charsetTable{}

func init() {
	def := func(table string, flags ...byte) {
		t := new(charsetTable)
		rs := []rune(table)
		for i := 0; i+1 < len(rs); i += 2 {
			t[rs[i]] = rs[i+1]
		}
		for _, f := range flags {
			charsets[f] = t
		}
	}
	// DEC Special Character and Line Drawing Set — the table from xterm's output under vttest.
	def("`◆a▒b␉c␌d␍e␊f°g±h␤i␋j┘k┐l┌m└n┼o⎺p⎻q─r⎼s⎽t├u┤v┴w┬x│y≤z≥{π|≠}£~·", '0')
	def("#£", 'A')                         // British
	def("#£@¾[i\\½]|{¨|f}¼~´", '4')        // Dutch
	def("[Ä\\Ö]Å^Ü`é{ä|ö}å~ü", 'C', '5')   // Finnish
	def("#£@à[°\\ç]§{é|ù}è~¨", 'R')        // French
	def("@à[â\\ç]ê^î`ô{é|ù}è~û", 'Q')      // French Canadian
	def("@§[Ä\\Ö]Ü{ä|ö}ü~ß", 'K')          // German
	def("#£@§[°\\ç]é`ù{à|ò}è~ì", 'Y')      // Italian
	def("@Ä[Æ\\Ø]Å^Ü`ä{æ|ø}å~ü", 'E', '6') // Norwegian/Danish
	def("#£@§[¡\\Ñ]¿{°|ñ}ç", 'Z')          // Spanish
	def("@É[Ä\\Ö]Å^Ü`é{ä|ö}å~ü", 'H', '7') // Swedish
	def("#ù@à[é\\ç]ê^î_è`ô{ä|ö}ü~û", '=')  // Swiss
	// 'B' (US ASCII) designates "no table" and is handled by designate itself.
}

// charsetState is the model's character-set state (xterm's CharsetService).
type charsetState struct {
	g     [4]byte // designation of G0..G3 as the final byte that was sent; 0 for the US set
	level int     // GL: which of g is in use
	gl    byte    // the set in use — g[level] as of the last shift or designation, or what DECRC put back
}

// knownCharset reports whether xterm has a handler for designating `flag` (it registers one for
// every key of its table, the US set included).
func knownCharset(flag byte) bool {
	_, ok := charsets[flag]
	return ok || flag == 'B'
}

// designate is `ESC <intermediate> <flag>`. Only the intermediates that name a G set do anything,
// and only a flag xterm registered a handler for (anything else leaves the sequence ignored).
func (c *charsetState) designate(intermediate, flag byte) {
	g := -1
	switch intermediate {
	case '(':
		g = 0
	case ')', '-':
		g = 1
	case '*', '.':
		g = 2
	case '+':
		g = 3
	}
	if g < 0 || !knownCharset(flag) {
		return // ESC / is "TODO: supported?" in xterm: a handler that does nothing
	}
	if flag == 'B' {
		flag = 0
	}
	c.g[g] = flag
	if c.level == g {
		c.gl = flag
	}
}

// shift is a locking shift (SI, SO, LS2, LS3 ...): GL becomes G<level>.
func (c *charsetState) shift(level int) {
	c.level = level
	c.gl = c.g[level]
}

// reset is RIS and DECSTR.
func (c *charsetState) reset() { *c = charsetState{} }

// vt100Mode is `CSI ? 2 h`: all four designations back to the US set (GL stays where it is, on
// whichever of them it was — now the US set).
func (c *charsetState) vt100Mode() {
	for g := range c.g {
		c.designate("()*+"[g], 'B')
	}
}

// defaultSets is `ESC % @` and `ESC % G`, and the VT100-mode switch `CSI ? 2 h` (which does it to
// all four): the US set in G0 and GL back on it.
func (c *charsetState) defaultSets() {
	c.shift(0)
	c.designate('(', 'B')
}

// translate is what a printed character becomes under the set in use. xterm only looks below 127.
func (c *charsetState) translate(r rune) rune {
	if c.gl == 0 || r >= 127 {
		return r
	}
	if t := charsets[c.gl]; t != nil {
		if m := t[r]; m != 0 {
			return m
		}
	}
	return r
}

// CharsetState is the part of the character-set state a restore has to put back for the next bytes
// to land as they would have: a TUI that is mid-way through a border drawn with `ESC ( 0` has not
// said how to get back, and a restored terminal that is not in the same set draws letters.
// Zero values are the power-on state (US in all four, GL = G0), so a stream that never designated
// anything serialises as nothing.
type CharsetState struct {
	// G holds the designation of G0..G3 as the final byte that was sent ('0' DEC Special Graphics,
	// 'A' British, ...), 0 for the US set.
	G [4]byte `json:"g"`
	// Level is the set in GL: 0 after SI, 1 after SO, 2 and 3 after the locking shifts.
	Level int `json:"level,omitempty"`
	// InUse is the set GL drew with at this moment. It is G[Level] unless a DECRC put another back
	// without touching the designations, which xterm allows.
	InUse byte `json:"in_use,omitempty"`
	// Saved is the set DECSC kept for this screen's slot.
	Saved byte `json:"saved,omitempty"`
}

func (c charsetState) export(saved byte) CharsetState {
	return CharsetState{G: c.g, Level: c.level, InUse: c.gl, Saved: saved}
}

func (c *charsetState) restore(s CharsetState) {
	c.g, c.level, c.gl = s.G, clampInt(s.Level, 0, 3), s.InUse
}

// IsZero reports the power-on state, so a TermState of a stream that never touched a set carries
// nothing for it.
func (s CharsetState) IsZero() bool { return s == CharsetState{} }
