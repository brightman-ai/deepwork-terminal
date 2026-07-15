import { describe, expect, test } from 'bun:test'
import { placeAnchoredPopover, type RectLike } from '../popoverPlacement'

const rect = (left: number, top: number, width: number, height: number): RectLike => ({
  left, top, width, height, right: left + width, bottom: top + height,
})

describe('placeAnchoredPopover', () => {
  test('flips above a bottom anchor and clamps the right edge', () => {
    const p = placeAnchoredPopover(rect(1810, 1030, 60, 24), rect(0, 0, 340, 500), rect(0, 0, 1920, 1080))
    expect(p.side).toBe('above')
    expect(p.left).toBe(1572)
    expect(p.top).toBeGreaterThanOrEqual(8)
    expect(p.top + Math.min(500, p.maxHeight)).toBeLessThanOrEqual(1024)
  })

  test('uses non-zero visual viewport offsets under zoom or pan', () => {
    const p = placeAnchoredPopover(rect(220, 180, 50, 24), rect(0, 0, 340, 420), rect(120, 100, 600, 500))
    expect(p.left).toBeGreaterThanOrEqual(128)
    expect(p.top).toBeGreaterThanOrEqual(108)
    expect(p.left + 340).toBeLessThanOrEqual(712)
  })

  test('caps height to the larger available side and keeps internal scrolling possible', () => {
    const p = placeAnchoredPopover(rect(200, 260, 40, 24), rect(0, 0, 340, 900), rect(0, 0, 600, 500))
    expect(p.side).toBe('above')
    expect(p.maxHeight).toBe(246)
    expect(p.top).toBe(8)
  })
})
