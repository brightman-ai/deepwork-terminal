import { describe, it, expect } from 'bun:test'
import { summarize } from '../terminalRenderMetrics'

/**
 * The summary exists to answer "is the browser the slow half, and is the forced full-grid repaint
 * paying for itself" — so the two things it must never do are hide a tail behind an average, and
 * lose the forced-repaint count.
 */
describe('summarize', () => {
  it('reports percentiles, not averages — a rare long stall must stay visible', () => {
    // 19 fast frames and one 200ms stall: an average says 11ms and looks fine.
    const parse = [...Array(19).fill(1), 200]
    const s = summarize(20, 20_000, 3, parse, parse)
    expect(s.parseP50).toBe(1)
    expect(s.parseMax).toBe(200)
    expect(s.parseP95).toBeGreaterThan(s.parseP50)
  })

  it('carries the forced-repaint count through untouched', () => {
    expect(summarize(10, 100, 7, [1], [2]).forcedRepaints).toBe(7)
  })

  it('is safe on an empty window', () => {
    const s = summarize(0, 0, 0, [], [])
    expect(s.parseP50).toBe(0)
    expect(s.renderMax).toBe(0)
  })

  it('does not mutate the caller’s sample arrays (they are reused between windows)', () => {
    const parse = [5, 1, 3]
    summarize(3, 30, 0, parse, [])
    expect(parse).toEqual([5, 1, 3])
  })
})
