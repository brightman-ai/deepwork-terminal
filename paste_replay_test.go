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
		browser.feed(replayWithPasteMode([]byte("\x1b[2Jrepaint"), current), false, func(string, int64, bool) {})
		if browserState != enabled {
			t.Fatalf("reconnected browser paste mode=%t want %t", browserState, enabled)
		}
	}
}
