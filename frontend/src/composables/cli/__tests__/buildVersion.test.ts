import { describe, it, expect } from 'bun:test'
import { formatShortVersion, formatFullVersion } from '../buildVersion'

describe('formatShortVersion', () => {
  it('发版的干净 tag 原样显示', () => {
    expect(formatShortVersion('v0.7.14')).toBe('v0.7.14')
    expect(formatShortVersion('0.7.14')).toBe('v0.7.14')
  })

  // 刻意的契约变更（2026-07-30）：以前把 `-3-g<hash>` 整段丢掉，于是一个"在 tag 之后又走了 3 个
  // 提交"的 WIP 构建显示得和正式发版**一模一样**。而本地部署（pro 天天把 WIP 推到 8087）正是这个
  // 状态。现在保留一个 `+`：一个字符，说清"这不是那个 tag 本身"。
  it('git describe：留 vX.Y.Z，但用 + 保住"领先于该 tag"这个事实', () => {
    expect(formatShortVersion('v0.7.14-3-gb2535a0')).toBe('v0.7.14+')
    expect(formatShortVersion('v0.2.0-dirty')).toBe('v0.2.0+')
  })

  it('正好在 tag 上 → 没有 +（发版和 WIP 必须一眼可分）', () => {
    expect(formatShortVersion('v0.7.16')).toBe('v0.7.16')
  })

  it('裸 go build：dev- 前缀删掉，只留 7 位 hash', () => {
    expect(formatShortVersion('dev-b2535a0')).toBe('b2535a0')
  })

  it('工作区脏 → 尾巴一个 *，不是 -dirty', () => {
    // 这就是 Human 抱怨太长的那一串：17 字符 → 8 字符。
    expect(formatShortVersion('dev-b2535a0-dirty')).toBe('b2535a0*')
    expect(formatShortVersion('dev-b2535a0-dirty').length).toBeLessThan(
      'dev-b2535a0-dirty'.length,
    )
  })

  it('长 hash 截到 7 位', () => {
    expect(formatShortVersion('dev-b2535a0f1e2d3c4')).toBe('b2535a0')
  })

  it('空 → 空（调用方据此把徽标整个藏掉，而不是显示空框）', () => {
    expect(formatShortVersion('')).toBe('')
    expect(formatShortVersion('   ')).toBe('')
  })

  it('认不出的形态原样显示，绝不瞎截出一个错身份', () => {
    expect(formatShortVersion('nightly-2026-07-29')).toBe('nightly-2026-07-29')
    expect(formatShortVersion('dev')).toBe('dev')
  })
})

describe('formatFullVersion', () => {
  it('给 title 用的完整串 —— 一个字符都不丢', () => {
    expect(formatFullVersion('dev-b2535a0-dirty')).toBe('dev-b2535a0-dirty')
    expect(formatFullVersion('v0.7.14-3-gb2535a0')).toBe('v0.7.14-3-gb2535a0')
  })

  it('数字开头的补 v', () => {
    expect(formatFullVersion('0.7.14')).toBe('v0.7.14')
  })
})
