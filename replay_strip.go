package terminal

import (
	"bytes"
	"regexp"
)

// deviceQueryRe matches terminal "report" queries: Device Attributes, Device Status Report
// (incl. cursor-position), XTVERSION, the kitty-keyboard flags query, DECRQM, and OSC color
// queries. Applications and tmux itself emit these to probe the terminal, and the terminal
// answers them. They produce NO visible output.
//
// We strip them from the REPLAY buffer only. Why it matters: the replay is the raw PTY byte
// stream captured for this session; it contains tmux's startup queries. When a client
// reconnects (lock/unlock, F5) the buffer is replayed into the browser terminal, which
// dutifully ANSWERS those queries a SECOND time. Those stray answers travel back to tmux as
// input — and if tmux is in copy-mode they are read as key presses (e.g. a Device Status /
// color answer ends up triggering search-backward → the mysterious "(search up)" prompt that
// needs several Ctrl-C to escape). Live output is never touched; only the one-shot replay.
//
// Only query FORMS are matched, never their responses or visually-meaningful look-alikes:
//   - CSI … c  is only Device Attributes; CSI … n is only DSR — both safe to drop wholesale.
//   - XTVERSION requires the '>' (CSI > … q), so the visual DECSCUSR "CSI Ps SP q" is kept.
//   - the kitty / DECRQM / OSC forms all carry a '?' that a plain "set" never has.
var deviceQueryRe = regexp.MustCompile(
	"\x1b\\[[=>]?[0-9;]*c" + // CSI [=>]? Ps c   — Device Attributes (primary/secondary/tertiary)
		"|\x1b\\[\\??[0-9;]*n" + // CSI ?? Ps n      — Device Status Report (incl. cursor position)
		"|\x1b\\[>[0-9;]*q" + // CSI > Ps q       — XTVERSION
		"|\x1b\\[\\?[0-9;]*u" + // CSI ? Ps u       — kitty keyboard flags query
		"|\x1b\\[\\?[0-9;]*\\$p" + // CSI ? Ps $ p     — DECRQM (mode query)
		"|\x1b\\][0-9;]*\\?(?:\x07|\x1b\\\\)", // OSC Ps ; ? BEL|ST — color queries
)

// stripDeviceQueries removes terminal report-queries from a replay buffer (see deviceQueryRe).
// It is a no-op (and allocation-free) when the buffer has no ESC bytes at all.
func stripDeviceQueries(b []byte) []byte {
	if bytes.IndexByte(b, 0x1b) < 0 {
		return b
	}
	return deviceQueryRe.ReplaceAll(b, nil)
}
