import { describe, it, expect } from 'bun:test'
import { detectInAppBrowser } from '../useInAppBrowser'

describe('detectInAppBrowser', () => {
  it('flags WeChat (MicroMessenger) UAs', () => {
    const ua = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 MicroMessenger/8.0.57'
    expect(detectInAppBrowser(ua)).toEqual({ isInApp: true, app: '微信' })
  })

  it('flags other CN in-app webviews', () => {
    expect(detectInAppBrowser('... QQ/8.9.1 ...').app).toBe('QQ')
    expect(detectInAppBrowser('... Weibo (iPhone) ...').app).toBe('微博')
    expect(detectInAppBrowser('... DingTalk/7.0 ...').app).toBe('钉钉')
  })

  it('does NOT flag a real mobile Safari / Chrome', () => {
    const safari = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1'
    expect(detectInAppBrowser(safari).isInApp).toBe(false)
    const chrome = 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120.0 Mobile Safari/537.36'
    expect(detectInAppBrowser(chrome).isInApp).toBe(false)
  })

  it('is case-insensitive and empty-safe', () => {
    expect(detectInAppBrowser('micromessenger').isInApp).toBe(true)
    expect(detectInAppBrowser('').isInApp).toBe(false)
  })
})
