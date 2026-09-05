package terminal

import "github.com/brightman-ai/deepwork-terminal/muxd"

// The screen model moved to muxd; this is the server's door to it.
//
// ── Why it moved ────────────────────────────────────────────────────────────────────────────────
// It was written here for the Agent Overview card, which has to show what a terminal LOOKS like: a
// stream is not a screen, because a TUI repaints by moving the cursor, so stripping the escapes
// and keeping the text yields interleaved garbage. Replaying onto a grid fixed that.
//
// Scrollback needs the SAME model for a different question — which lines left the screen, and what
// colour were they — and it has to run where the bytes are complete and the process outlives a
// rebuild. That is the daemon: this server's copy of the stream is second-hand and may have holes
// (see Stream.Holed), and it is wiped every time the binary is replaced, which is precisely what
// muxd exists to avoid.
//
// So the model lives in muxd and this file forwards. The alternative — a second copy here — is the
// failure this codebase has already paid for elsewhere: two implementations of one rule drift, and
// then the overview card and the scrollback disagree about what the terminal said, with no way to
// tell which one is lying. Same reason muxd_alias.go exists for the ring buffer.
//
// The overview's behaviour is unchanged by the move. muxd.RenderScreen is the one-shot mode, which
// deliberately does NOT honour the alternate-screen switch: the card renders a SLICE of the ring
// that routinely starts after a TUI has already switched, and honouring the switch back would
// restore a screen this replay never saw and blank the card exactly when there is something to
// look at. Scrollback's incremental mode does honour it, because the alternate screen has no
// history in any real terminal. See vt.trackAlt.
func renderScreen(raw string, rows, cols int) []string {
	return muxd.RenderScreen(raw, rows, cols)
}
