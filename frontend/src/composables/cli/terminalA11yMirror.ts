/**
 * terminalA11yMirror — does each terminal keep an aria-live text mirror of its output?
 *
 * The mirror was a `<pre aria-live="polite">` holding the last 24 000 characters of output, replaced
 * wholesale every 120ms while anything was printing. Nothing reads it: no test, no script, and a
 * screen reader fed raw TUI repaints (spinners, status lines, cursor dances) gets noise, not a
 * transcript — xterm's own screenReaderMode is the mechanism built for that.
 *
 * What it does cost is outside the page. On 2026-10-18 keys typed while an agent was RUNNING waited
 * 1.5–1.9s for the main thread (Event Timing) with no long frame on the page to account for it, on
 * every surface (terminal and compose box alike), and the IME's Shift toggle fired twice. The page
 * was idle; the wait was in the browser. On macOS the IME and the system's input-mode indicator find
 * the caret through the accessibility API, which turns the browser's accessibility support on — and
 * from then on every replacement of a 24 000-character live region is a tree update and a live-region
 * notification handled on the browser's main thread, the same thread that delivers keys to the page
 * and answers the IME (Chromium answers firstRectForCharacterRange with a synchronous wait on the
 * renderer, crbug 40412486 / 503787240). "Only while running" is exactly "only while it updates".
 *
 * So the mirror is off unless asked for: `?a11y_mirror=1` turns it back on (sticky for the tab), which
 * is also the A/B for this reasoning; `?a11y_mirror=0` clears that.
 */

const QUERY_KEY = 'a11y_mirror'
const STORAGE_KEY = 'cli_a11y_mirror'

/** '1' on · '0' off · null default. Sticky per tab so a reload keeps the experiment. */
export function a11yMirrorOverride(): string | null {
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

/** The rule, pure: off unless explicitly turned on. */
export function a11yMirrorEnabled(override: string | null): boolean {
  return override === '1'
}
