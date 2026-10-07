package terminal

// A reconnect/resize may cut away the DECSET that enabled bracketed paste.
// The continuous output observer owns the current negotiated state; reassert
// it after the bounded screen replay so native browser pastes stay atomic.
func replayWithPasteMode(replay []byte, enabled bool) []byte {
	mode := "\x1b[?2004l"
	if enabled {
		mode = "\x1b[?2004h"
	}
	result := make([]byte, 0, len(replay)+len(mode))
	result = append(result, replay...)
	return append(result, mode...)
}
