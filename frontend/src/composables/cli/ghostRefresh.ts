/**
 * ghostRefreshWait — throttle-floor math for the alt-screen ghosting guard, pulled out as a PURE
 * function so the timer behavior is unit-testable without a DOM/fake-clock.
 *
 * The guard issues a `tmux refresh-client` (full-cell resend) to clear residue that accumulates
 * when xterm's buffer diverges from tmux under a fullscreen TUI.
 *
 * v1 was a plain 160ms TRAILING debounce — reset on every output frame — which meant a CONTINUOUS
 * stream (spinner ~every 100ms, tokens faster) re-armed it forever and it NEVER fired until the
 * stream paused: the "内容在刷时才花" garble, mid-stream divergence stayed visible indefinitely.
 *
 * v2 added a maxWait cap (debounce-with-maxWait): still coalesced into a burst, but forced a fire
 * at least every maxWait past the burst's first frame. This DID fire correctly in production
 * (confirmed live 2026-07-26: ~once/second, ~15ms server cost, 100% success) — but a real user
 * screenshot still caught visible residue: maxWait bounds the delay from the START of a burst, so
 * up to a full maxWait's worth of fresh divergence can accumulate and be visible before the next
 * fire actually happens. Shrinking maxWait only shrinks that window, it doesn't change the shape.
 *
 * v3 (this version) is a LEADING-EDGE THROTTLE instead of a trailing debounce: the very FIRST
 * frame after being idle fires on the next tick (wait=0); once fired, further frames are ignored
 * until minInterval has elapsed since the LAST fire, then the next frame fires again immediately.
 * Given the real per-call cost is ~15ms (measured live), minInterval can be small — this changes
 * the guarantee from "eventually, within maxWait of burst START" to "promptly, within minInterval
 * of the LAST correction," a materially tighter bound on a continuous stream, without just
 * shrinking one "wait longer" knob further. See CliTerminalSurface.vue for the calling side.
 *
 * @param lastFiredAt  timestamp (ms) of the last refresh-client fire, or null if none yet (never
 *                     fired, or the caller has no pending timer and treats this as a fresh start)
 * @param now          current timestamp (ms)
 * @param minInterval  minimum gap (ms) enforced between two fires
 * @returns setTimeout delay (ms) — 0 means "fire on the next tick"
 */
export function ghostRefreshWait(
  lastFiredAt: number | null,
  now: number,
  minInterval: number,
): number {
  if (lastFiredAt === null) return 0
  return Math.max(0, minInterval - (now - lastFiredAt))
}
