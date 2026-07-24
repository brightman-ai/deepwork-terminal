import { describe, it, expect } from 'bun:test'
import { ghostRefreshWait } from '../ghostRefresh'

/**
 * ghostRefreshWait: the alt-screen ghosting guard's debounce math. Regression guard for the
 * "内容在刷时才花" garble — the old plain 160ms trailing debounce re-armed on every output frame
 * and NEVER fired under a continuous stream, so mid-stream xterm↔tmux divergence stayed visible.
 * The maxWait cap must force a refresh-client at least every ~maxWait even while output keeps
 * flowing, yet still give the full trailing debounce once the stream is fresh.
 */
const DEBOUNCE = 160
const MAXWAIT = 1200

describe('ghostRefreshWait', () => {
  it('gives the full trailing debounce at the start of a burst', () => {
    // now == burstStartedAt → nothing elapsed → wait the full debounce.
    expect(ghostRefreshWait(0, 0, DEBOUNCE, MAXWAIT)).toBe(DEBOUNCE)
  })

  it('stays at the debounce while well inside the maxWait window', () => {
    // 500ms into a continuous burst: 0 + 1200 - 500 = 700 > 160 → still the plain debounce.
    expect(ghostRefreshWait(0, 500, DEBOUNCE, MAXWAIT)).toBe(DEBOUNCE)
  })

  it('shrinks below the debounce as the maxWait cap approaches (forces a mid-stream fire)', () => {
    // 1100ms in: 0 + 1200 - 1100 = 100 < 160 → capped, will fire in 100ms even though output flows.
    expect(ghostRefreshWait(0, 1100, DEBOUNCE, MAXWAIT)).toBe(100)
  })

  it('clamps to 0 at and past the maxWait cap (never negative → fires now)', () => {
    expect(ghostRefreshWait(0, 1200, DEBOUNCE, MAXWAIT)).toBe(0)
    expect(ghostRefreshWait(0, 5000, DEBOUNCE, MAXWAIT)).toBe(0) // long continuous stream
  })

  it('is burst-relative: a fresh burst gets the full debounce again', () => {
    // burst restarted at t=2000, now t=2000 → full debounce (the post-fire re-arm case).
    expect(ghostRefreshWait(2000, 2000, DEBOUNCE, MAXWAIT)).toBe(DEBOUNCE)
  })
})
