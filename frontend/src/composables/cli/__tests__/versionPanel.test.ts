import { describe, it, expect } from 'bun:test'
import { pageLine, programLine, badgeNeedsAttention } from '../versionPanel'

describe('pageLine — 浏览器页面这一块', () => {
  it('页面旧了 → 警告 + 一个「刷新」动作（本地、瞬时）', () => {
    const l = pageLine(true)
    expect(l.tone).toBe('warn')
    expect(l.action?.kind).toBe('refresh')
  })
  it('页面是最新的 → 一句确认，没有动作（没事可做就别给按钮）', () => {
    const l = pageLine(false)
    expect(l.tone).toBe('ok')
    expect(l.action).toBeUndefined()
  })
})

describe('programLine — 服务端程序这一块', () => {
  it('有新发布版 → 唯一给外链的一档', () => {
    const l = programLine('outdated', 'v0.7.16', 'https://github.com/x/releases/tag/v0.7.16')
    expect(l.tone).toBe('warn')
    expect(l.text).toContain('v0.7.16')
    expect(l.action?.kind).toBe('open')
    expect(l.action?.href).toContain('v0.7.16')
  })

  it('已是最新 → 确认，无动作', () => {
    expect(programLine('current').tone).toBe('ok')
    expect(programLine('current').action).toBeUndefined()
  })

  it('本地构建 → 解释为什么不比，绝不说"有新版本"', () => {
    const l = programLine('local')
    expect(l.tone).toBe('muted')
    expect(l.text).toContain('本地构建')
    expect(l.action).toBeUndefined()
  })

  // 这条是整个设计的良心：查不到就说查不到。
  it('没查到 ≠ 已是最新 —— 措辞与 tone 都必须与 current 不同', () => {
    const unknown = programLine('unknown')
    const current = programLine('current')
    expect(unknown.text).not.toBe(current.text)
    expect(unknown.tone).not.toBe('ok')
    expect(unknown.action).toBeUndefined()
  })
})

describe('badgeNeedsAttention — 小圆点', () => {
  it('只有需要用户做点什么时才亮', () => {
    expect(badgeNeedsAttention(true, 'current')).toBe(true)   // 页面旧了
    expect(badgeNeedsAttention(false, 'outdated')).toBe(true) // 程序旧了
  })
  it('没事时一律不亮 —— 常亮的点会被学会忽略，真有事时就失效了', () => {
    expect(badgeNeedsAttention(false, 'current')).toBe(false)
    expect(badgeNeedsAttention(false, 'local')).toBe(false)
    expect(badgeNeedsAttention(false, 'unknown')).toBe(false)
  })
})
