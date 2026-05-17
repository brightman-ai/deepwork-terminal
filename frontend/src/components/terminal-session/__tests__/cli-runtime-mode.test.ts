import { describe, expect, test } from 'bun:test'
import { computeCliRuntimeProfile, detectCliRuntimeProfile } from '../../../composables/cli/useCliRuntimeMode'

describe('detectCliRuntimeProfile', () => {
  test('defaults to PC browser when no DOM globals are available', () => {
    const profile = detectCliRuntimeProfile()
    expect(profile.mode).toBe('pc-browser')
    expect(profile.usesMobileOverlay).toBe(false)
    expect(profile.usesVisualKeyboardInset).toBe(false)
  })

  test('classifies Wails PC as desktop even when width is narrow', () => {
    const profile = computeCliRuntimeProfile({
      userAgent: 'Mozilla/5.0 AppleWebKit/605.1.15',
      platform: 'MacIntel',
      maxTouchPoints: 0,
      innerWidth: 390,
      hasTouchEvent: false,
      hasWailsRuntime: true,
    })
    expect(profile.mode).toBe('wails-pc')
    expect(profile.usesMobileOverlay).toBe(false)
    expect(profile.usesVisualKeyboardInset).toBe(false)
  })

  test('classifies normal desktop browser as PC browser', () => {
    const profile = computeCliRuntimeProfile({
      userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36',
      platform: 'MacIntel',
      maxTouchPoints: 0,
      innerWidth: 1440,
      hasTouchEvent: false,
      hasWailsRuntime: false,
    })
    expect(profile.mode).toBe('pc-browser')
    expect(profile.usesMobileOverlay).toBe(false)
    expect(profile.usesVisualKeyboardInset).toBe(false)
  })


  test('classifies iOS WebKit as Safari keyboard mode', () => {
    const profile = computeCliRuntimeProfile({
      userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1',
      platform: 'iPhone',
      maxTouchPoints: 5,
      innerWidth: 390,
      hasTouchEvent: true,
      hasWailsRuntime: false,
    })
    expect(profile.mode).toBe('ios-safari')
    expect(profile.usesMobileOverlay).toBe(true)
    expect(profile.usesVisualKeyboardInset).toBe(true)
  })
})
