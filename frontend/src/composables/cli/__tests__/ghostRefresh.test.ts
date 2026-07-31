import { describe, it, expect } from 'bun:test'
import {
  GHOST_ECHO_WINDOW,
  GHOST_MAX_STALE,
  GHOST_TYPING_QUIET,
  ghostRefreshDeferredForTyping,
  ghostRefreshSuppressed,
  ghostRefreshWait,
} from '../ghostRefresh'

/**
 * ghostRefreshWait: the alt-screen ghosting guard's leading-edge-throttle math. Regression guard
 * for two garbles: the v1 "内容在刷时才花" bug (a plain trailing debounce re-arms forever under a
 * continuous stream and never fires), and the v2 residue that a real user still caught even while
 * the debounce-with-maxWait cap was firing correctly (bounded from burst START, not from the last
 * correction). v3's throttle fires immediately on the first frame after idle, then at most once
 * per minInterval thereafter — tight from the LAST fire, not the burst start.
 */
const MIN_INTERVAL = 120

describe('ghostRefreshWait', () => {
  it('fires immediately when nothing has fired yet', () => {
    expect(ghostRefreshWait(null, 0, MIN_INTERVAL)).toBe(0)
    expect(ghostRefreshWait(null, 5000, MIN_INTERVAL)).toBe(0)
  })

  it('withholds for the remainder of minInterval right after a fire', () => {
    // fired at t=0, now t=30 → 120 - 30 = 90ms left before the next fire is allowed.
    expect(ghostRefreshWait(0, 30, MIN_INTERVAL)).toBe(90)
  })

  it('shrinks linearly as minInterval elapses', () => {
    expect(ghostRefreshWait(0, 100, MIN_INTERVAL)).toBe(20)
  })

  it('clamps to 0 at and past minInterval (never negative → fires now)', () => {
    expect(ghostRefreshWait(0, 120, MIN_INTERVAL)).toBe(0)
    expect(ghostRefreshWait(0, 5000, MIN_INTERVAL)).toBe(0) // long continuous stream
  })

  it('is relative to the LAST fire, not a burst start — a later fire resets the window', () => {
    // last fired at t=2000, now t=2000 → full interval owed again (the post-fire re-arm case).
    expect(ghostRefreshWait(2000, 2000, MIN_INTERVAL)).toBe(MIN_INTERVAL)
  })
})

/**
 * ghostRefreshSuppressed: the v4 loop breaker. `refresh-client` resends every cell, and those
 * cells arrive as ordinary output frames — so without this check the guard's own correction is
 * read as fresh output and re-arms it, forever. Measured before the fix on an IDLE pane: 7.7
 * fires/s and 15 KB/s of self-inflicted redraw traffic (scripts/diag/wsprobe A/B).
 */
describe('ghostRefreshSuppressed', () => {
  it('does not suppress when nothing has fired yet', () => {
    expect(ghostRefreshSuppressed(null, 0)).toBe(false)
    expect(ghostRefreshSuppressed(null, 999_999)).toBe(false)
  })

  it('suppresses the frames arriving inside the window a fire opened', () => {
    const firedAt = 1_000
    const echoUntil = firedAt + GHOST_ECHO_WINDOW
    // The resent cells land within milliseconds and keep landing for the length of the burst.
    expect(ghostRefreshSuppressed(echoUntil, firedAt + 1)).toBe(true)
    expect(ghostRefreshSuppressed(echoUntil, firedAt + GHOST_ECHO_WINDOW - 1)).toBe(true)
  })

  it('stops suppressing at the window edge, so real output still gets corrected', () => {
    const echoUntil = 1_000 + GHOST_ECHO_WINDOW
    expect(ghostRefreshSuppressed(echoUntil, echoUntil)).toBe(false)
    expect(ghostRefreshSuppressed(echoUntil, echoUntil + 5_000)).toBe(false)
  })

  it('bounds the steady-state rate under a continuous stream', () => {
    // Worst case while output really is flowing: one fire per (minInterval + echo window), vs the
    // ~7.7/s the loop produced on an idle screen before the fix.
    const perSecond = 1000 / (MIN_INTERVAL + GHOST_ECHO_WINDOW)
    expect(perSecond).toBeLessThan(2.5)
  })
})

/**
 * ghostRefreshDeferredForTyping: the v5 rule. A correction costs a full-cell resend (~5KB) plus a
 * whole-grid repaint, so it must not land while the user is mid-keystroke — measured cost of
 * getting this wrong: 17KB of downstream traffic for ~30 bytes of echo over 10s of typing.
 * The staleness ceiling is what keeps "defer" from becoming v1's never-fires bug, re-introduced
 * from the input side.
 */
describe('ghostRefreshDeferredForTyping', () => {
  it('does not defer when the user has never typed', () => {
    expect(ghostRefreshDeferredForTyping(null, null, 10_000)).toBe(false)
  })

  it('does not defer the FIRST correction — never-fired is infinitely stale, not "nothing due"', () => {
    // Regression guard: reading null as "no ceiling to breach" made a session that opens with
    // typing defer forever. The A/B showed it as ZERO corrections where one per ceiling was due.
    const typing = 1_000
    expect(ghostRefreshDeferredForTyping(typing, null, typing + 1)).toBe(false)
    expect(ghostRefreshDeferredForTyping(typing, null, typing + GHOST_TYPING_QUIET - 1)).toBe(false)
  })

  it('defers while a keystroke is recent', () => {
    const typed = 1_000
    expect(ghostRefreshDeferredForTyping(typed, typed, typed + 1)).toBe(true)
    expect(ghostRefreshDeferredForTyping(typed, typed, typed + GHOST_TYPING_QUIET - 1)).toBe(true)
  })

  it('stops deferring once typing goes quiet — the correction lands when you look at the screen', () => {
    const typed = 1_000
    expect(ghostRefreshDeferredForTyping(typed, typed, typed + GHOST_TYPING_QUIET)).toBe(false)
    expect(ghostRefreshDeferredForTyping(typed, typed, typed + 5_000)).toBe(false)
  })

  it('never defers past the staleness ceiling, so sustained typing cannot starve it', () => {
    // Held key: a keystroke every 50ms, so the quiet window never elapses on its own.
    const firedAt = 1
    let now = 1
    let deferredForever = true
    for (let i = 0; i < 200; i++) {
      now += 50
      if (!ghostRefreshDeferredForTyping(now, firedAt, now)) { deferredForever = false; break }
    }
    expect(deferredForever).toBe(false)
    expect(now).toBeLessThanOrEqual(GHOST_MAX_STALE + 50)
  })

  it('a fresh fire re-arms the deferral (the ceiling is measured from the LAST correction)', () => {
    const now = 10_000
    expect(ghostRefreshDeferredForTyping(now, now - GHOST_MAX_STALE - 1, now)).toBe(false)
    expect(ghostRefreshDeferredForTyping(now, now - 10, now)).toBe(true)
  })
})
