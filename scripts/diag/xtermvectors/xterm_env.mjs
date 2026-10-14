// The terminal the oracle runs: xterm.js headless 6.0.0 with @xterm/addon-unicode11 loaded and
// selected (`unicode.activeVersion = '11'`), which is what the frontend does. Unicode 11 is where the
// emoji are two columns wide; xterm's built-in default (6) draws them at one, and Claude Code and
// Codex lay their output out for two.
//
// It is also built with convertEol: true, as the frontend builds it (XtermTerminal.vue). In xterm
// that option IS mode 20 (LNM): a line feed returns the carriage, so the terminal starts with it on
// and `CSI 20 l` turns it off.
//
// Everything that builds a terminal for comparison goes through newTerminal, so the width tables and
// the options the model is held to are chosen in one place.
import xh from '@xterm/headless'
import u11 from '@xterm/addon-unicode11'
const { Terminal } = xh
const { Unicode11Addon } = u11

export const UNICODE_VERSION = '11'

export function newTerminal(opts = {}) {
  const term = new Terminal({ scrollback: 100000, allowProposedApi: true, logLevel: 'off', convertEol: true, ...opts })
  term.loadAddon(new Unicode11Addon())
  term.unicode.activeVersion = UNICODE_VERSION
  return term
}
