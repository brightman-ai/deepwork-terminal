package terminal

import "testing"

func TestReplayCutPreservesNegotiatedPasteMode(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		// The mode was emitted before the retained grid epoch; the repaint does
		// not restate it. A fresh browser's default state would otherwise be wrong.
		current := false
		observer := clipboardScanner{pasteMode: func(value bool) { current = value }}
		original := []byte("\x1b[?2004h\x1b[2Jrepaint")
		if !enabled {
			original = []byte("\x1b[?2004h\x1b[?2004l\x1b[2Jrepaint")
		}
		observer.feed(original, false, func(string, int64, bool) {})
		browserState := !enabled
		browser := clipboardScanner{pasteMode: func(value bool) { browserState = value }}
		browser.feed(replayWithPasteMode([]byte("\x1b[2Jrepaint"), current, true), false, func(string, int64, bool) {})
		if browserState != enabled {
			t.Fatalf("reconnected browser paste mode=%t want %t", browserState, enabled)
		}
	}
}

// A mode nobody has observed must not be sent to the browser as "off".
func TestReplayLeavesUnknownPasteModeUnasserted(t *testing.T) {
	replay := []byte("\x1b[2Jrepaint")
	got := replayWithPasteMode(replay, false, false)
	if string(got) != string(replay) {
		t.Fatalf("unknown paste mode altered the replay: %q", got)
	}
	// And a browser that already believes the mode is on keeps believing it.
	browserState := true
	browser := clipboardScanner{pasteMode: func(value bool) { browserState = value }}
	browser.feed(got, false, func(string, int64, bool) {})
	if !browserState {
		t.Fatal("an unknown mode switched the browser's bracketed paste off")
	}
}
