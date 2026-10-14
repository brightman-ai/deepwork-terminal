package terminal

// A reconnect/resize may cut away the DECSET that enabled bracketed paste. The session owns the
// negotiated state (paste_mode.go); reassert it after the bounded screen replay so native
// browser pastes stay atomic.
//
// An UNKNOWN mode is not asserted. "Off" used to be the zero value here, so a restarted server
// — which has not yet seen the program announce anything — told every browser the program had
// switched bracketed paste off, and Codex then received each pasted character as a keystroke.
func replayWithPasteMode(replay []byte, enabled, known bool) []byte {
	if !known {
		return replay
	}
	mode := "\x1b[?2004l"
	if enabled {
		mode = "\x1b[?2004h"
	}
	result := make([]byte, 0, len(replay)+len(mode))
	result = append(result, replay...)
	return append(result, mode...)
}

// pasteModeAssertion is the DECSET/DECRST that states a known bracketed-paste mode, or nothing for
// an unknown one (see replayWithPasteMode for why unknown is never asserted as "off").
func pasteModeAssertion(enabled, known bool) string {
	switch {
	case !known:
		return ""
	case enabled:
		return "\x1b[?2004h"
	default:
		return "\x1b[?2004l"
	}
}
