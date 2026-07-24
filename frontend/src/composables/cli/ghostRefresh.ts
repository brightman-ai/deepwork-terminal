/**
 * ghostRefreshWait — the debounce-with-maxWait math for the alt-screen ghosting guard, pulled out
 * as a PURE function so the timer behavior is unit-testable without a DOM/fake-clock.
 *
 * The guard issues a `tmux refresh-client` (full-cell resend) to clear residue that accumulates
 * when xterm's buffer diverges from tmux under a fullscreen TUI. It was a plain 160ms TRAILING
 * debounce — reset on every output frame — which meant that under a CONTINUOUS stream (spinner
 * animating ~every 100ms, tokens faster) it re-armed forever and NEVER fired until the stream
 * paused. That is exactly the "内容在刷时才花" garble: mid-stream divergence stayed visible.
 *
 * The fix caps the trailing debounce with a maxWait: the timer still coalesces a burst, but can be
 * delayed no more than `maxWait` past the burst's FIRST frame, so a continuous stream still gets a
 * refresh-client roughly every maxWait (~1.2s) — bounding how long any divergence can linger —
 * while a stream that stops still gets its clean trailing repaint after `debounce`.
 *
 * @param burstStartedAt  timestamp (ms) of the first output frame of the current burst
 * @param now             current timestamp (ms)
 * @param debounce        trailing quiet-period before firing (ms)
 * @param maxWait         hard cap on delay past burstStartedAt (ms)
 * @returns setTimeout delay (ms), always in [0, debounce]
 */
export function ghostRefreshWait(
  burstStartedAt: number,
  now: number,
  debounce: number,
  maxWait: number,
): number {
  return Math.max(0, Math.min(debounce, burstStartedAt + maxWait - now))
}
