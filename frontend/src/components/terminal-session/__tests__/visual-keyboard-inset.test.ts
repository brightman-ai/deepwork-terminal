import { describe, expect, test } from 'bun:test'
import { computeVisualKeyboardInset, readVisualKeyboardInset } from '../../../composables/cli/useVisualKeyboardInset'

describe('computeVisualKeyboardInset', () => {
  test('subtracts visual viewport offsetTop from keyboard inset', () => {
    expect(computeVisualKeyboardInset({
      innerHeight: 844,
      visualHeight: 500,
      offsetTop: 44,
    })).toBe(300)
  })

  test('suppresses tiny viewport jitter', () => {
    expect(computeVisualKeyboardInset({
      innerHeight: 844,
      visualHeight: 840,
      offsetTop: 0,
    })).toBe(0)
  })

  test('clamps negative inset after browser chrome movement', () => {
    expect(computeVisualKeyboardInset({
      innerHeight: 600,
      visualHeight: 620,
      offsetTop: 20,
    })).toBe(0)
  })

  test('returns zero when keyboard inset handling is disabled', () => {
    expect(readVisualKeyboardInset(false)).toBe(0)
  })
})
