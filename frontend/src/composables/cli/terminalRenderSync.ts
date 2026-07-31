/**
 * terminalRenderSync — should a write force a full-grid repaint on top of xterm's own?
 *
 * ── What the forced repaint is for ───────────────────────────────────────────────────────────
 * xterm's DOM renderer repaints from damage tracking, and under a fullscreen TUI it could miss
 * cells — the screen ended up showing glyphs that were no longer in the buffer. The mitigation was
 * to call refresh(0, rows-1) after any write that looked like a fullscreen redraw, twice (a rAF one
 * and a trailing one), so the whole grid was re-rendered regardless of what damage tracking thought.
 *
 * ── Why it is now conditional ────────────────────────────────────────────────────────────────
 * That is a DOM-renderer remedy. It was written when the DOM renderer was the only renderer, so
 * "always" and "when the DOM renderer is in use" were the same sentence — and when WebGL arrived,
 * the sentence silently stopped meaning what it said. WebGL uploads the grid to the GPU per frame
 * and does not have the damage-tracking gap this compensates for.
 *
 * The cost of leaving it on is not theoretical. Measured on a real session (cli.render.metrics):
 * it fired on ~30% of frames — 107 whole-screen repaints in 325 frames — because its trigger
 * matches a bare erase-line (`ESC[K`), which is in almost everything a TUI emits. And the TEST is
 * itself expensive: every frame gets fully TextDecoder'd and run through three regexes purely to
 * decide whether to do the expensive thing.
 *
 * So it is bound to the renderer that needs it, rather than kept as a permanent tax with a flag on
 * it. An escape hatch stays for the case this reasoning is wrong on some GPU/driver: `?render_sync=1`
 * forces it back on, `?render_sync=0` forces it off, either sticky for the tab. If residue ever
 * appears under WebGL, that URL turns it back on immediately — no rebuild, no waiting for a fix.
 */

const QUERY_KEY = 'render_sync'
const STORAGE_KEY = 'cli_render_sync'

/** '1' force on · '0' force off · null follow the renderer. Sticky per tab so a reload keeps it. */
export function renderSyncOverride(): string | null {
  if (typeof window === 'undefined') return null
  try {
    const q = new URLSearchParams(window.location.search).get(QUERY_KEY)
    if (q === '1' || q === '0') {
      window.sessionStorage.setItem(STORAGE_KEY, q)
      return q
    }
    const stored = window.sessionStorage.getItem(STORAGE_KEY)
    return stored === '1' || stored === '0' ? stored : null
  } catch {
    return null
  }
}

/**
 * The rule, pure so it is testable and so the reasoning lives in one place:
 * the forced repaint exists for the DOM renderer, an explicit override wins over both.
 */
export function renderSyncEnabled(renderer: string, override: string | null): boolean {
  if (override === '1') return true
  if (override === '0') return false
  return renderer !== 'webgl'
}
