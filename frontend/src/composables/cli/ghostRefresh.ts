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

/**
 * GHOST_ECHO_WINDOW — how long after a fire the guard ignores incoming output frames.
 *
 * ── The feedback edge v1..v3 all missed ──────────────────────────────────────────────────────
 * `tmux refresh-client` RESENDS EVERY CELL, and those cells arrive as ordinary PTY output frames
 * on the very same socket that triggers the guard. So the guard's own correction re-triggers the
 * guard: fire → full-screen resend → output frames → fire again, forever, with nobody typing.
 * Measured on an IDLE 80×24 pane (scripts/diag/wsprobe): 7.7 refresh-client/s, 15 KB/s of PTY output,
 * 20% CPU in dw-terminal + 9% in the tmux server — all of it the loop talking to itself. On a
 * real ~200×50 terminal that is ~5× the bytes, every one of them a full-screen repaint for
 * xterm.js to render. That is the lag: not tmux, not the network, this loop.
 *
 * The fix is to close the loop, not to slow it down (a bigger minInterval would only make the
 * self-feeding cheaper, never stop it). After firing we know the next burst of output IS the
 * redraw we asked for, so it must not count as evidence that another redraw is needed.
 *
 * 400ms is the settle budget for that burst: the server call itself is ~15ms, and the measured
 * inter-frame gap of a resend burst stays under ~130ms at p95. Genuine user output landing inside
 * the window is not lost — it simply doesn't ARM a fire, and a continuous stream's next frame
 * (which arrives within milliseconds) fires as soon as the window closes. Steady state therefore
 * becomes ≤1 fire per (minInterval + window) while output is actually flowing, and ZERO when the
 * screen is quiet — instead of a permanent 7.7/s.
 */
export const GHOST_ECHO_WINDOW = 400

/**
 * ghostRefreshSuppressed — is `now` still inside the echo window opened by the last fire?
 *
 * Pure so the loop-breaking rule is testable on its own; see ghostRefresh.test.ts. An
 * explicitly-caused refresh (reflow, reconnect, buffer switch) passes `force` at the call site
 * and skips this check: those are real events, not the guard's own echo.
 */
export function ghostRefreshSuppressed(echoUntil: number | null, now: number): boolean {
  return echoUntil !== null && now < echoUntil
}

/**
 * GHOST_TYPING_QUIET — how long after the user's last keystroke the guard stays out of the way.
 *
 * A correction is not free: `refresh-client` resends EVERY cell, ~5 KB on a 200×50 grid, and the
 * client then repaints the whole grid. Measured while typing 30 keys over 10s
 * (scripts/diag/wsprobe -typing): 17 KB of downstream traffic for ~30 bytes of actual echo — the rest
 * was three full-screen resends fired by the keystroke echoes themselves.
 *
 * Which is the wrong trade at the wrong moment. Ghosting is a LOOKING problem: stale glyphs matter
 * when the user reads the screen, not while they are still changing it. Correcting mid-keystroke
 * spends the most expensive operation available to fix an image that is about to be overwritten
 * anyway — and spends it exactly when latency is felt.
 *
 * So a keystroke buys quiet: while the user is typing, output frames do not arm a fire. The moment
 * they stop, the pending correction lands. GHOST_MAX_STALE below keeps that from becoming "never"
 * during sustained typing.
 */
export const GHOST_TYPING_QUIET = 350

/**
 * GHOST_MAX_STALE — the ceiling on how long a correction may be deferred by continuous typing.
 *
 * Without it, typing quiet is a starvation bug wearing a different hat: hold a key down, or paste
 * into a shell that echoes, and the guard would never fire (this is v1's "内容在刷时才花" failure,
 * re-introduced from the input side instead of the output side). Past this bound the correction
 * happens regardless of typing — one resend every 2.5s is a cost the user cannot perceive, while
 * visible residue is.
 */
export const GHOST_MAX_STALE = 2500

/**
 * ghostRefreshDeferredForTyping — should this output frame be ignored because the user is typing?
 *
 * True only while BOTH hold: a keystroke landed within GHOST_TYPING_QUIET, and the last correction
 * is younger than GHOST_MAX_STALE. Pure, so both the deferral and its ceiling are testable without
 * a clock; see ghostRefresh.test.ts.
 */
export function ghostRefreshDeferredForTyping(
  lastInputAt: number | null,
  lastFiredAt: number | null,
  now: number,
): boolean {
  if (lastInputAt === null) return false
  if (now - lastInputAt >= GHOST_TYPING_QUIET) return false
  // Never fired yet == infinitely stale, so the ceiling is already breached and the FIRST
  // correction must not wait for a typing pause. Treating "no previous fire" as "nothing is
  // overdue" reads naturally and is exactly backwards: it made a session that started with typing
  // defer forever, which the A/B caught as a suspicious ZERO corrections instead of the expected
  // one-per-ceiling (scripts/diag/wsprobe -typing).
  if (lastFiredAt === null) return false
  // Sustained typing must not starve the correction either.
  if (now - lastFiredAt >= GHOST_MAX_STALE) return false
  return true
}
