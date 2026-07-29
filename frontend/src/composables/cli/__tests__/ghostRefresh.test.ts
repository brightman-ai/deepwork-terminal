import { describe, it, expect } from 'bun:test'
import { ghostRefreshWait } from '../ghostRefresh'

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
