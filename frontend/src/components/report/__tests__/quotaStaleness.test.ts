import { describe, it, expect } from 'bun:test'
import { stalePresentation, humanAge, COLLAPSE_AFTER_SECONDS } from '../quotaStaleness'

const base = { allInferred: false, runtime: 'codex', canProbe: true }

describe('stalePresentation', () => {
  it('新鲜读数：不降权、不折叠、无徽标', () => {
    const p = stalePresentation({ ...base, stale: false, ageSeconds: 30 })
    expect(p).toEqual({ dim: false, collapse: false, badge: '', hint: '' })
  })

  it('过期就降权 —— 过期数字不该和新鲜数字长得一样', () => {
    const p = stalePresentation({ ...base, stale: true, ageSeconds: 15 * 3600 })
    expect(p.dim).toBe(true)
    expect(p.badge).toBe('数据已过期')
  })

  it('提示先说为什么、再说怎么办', () => {
    const p = stalePresentation({ ...base, stale: true, ageSeconds: 15 * 3600 })
    expect(p.hint).toContain('15 小时')   // 为什么
    expect(p.hint).toContain('codex')
    expect(p.hint).toContain('点击')      // 怎么办
  })

  it('查不了就不写"点击" —— 点了没反应的提示比没有更糟', () => {
    const p = stalePresentation({ ...base, stale: true, ageSeconds: 3600, canProbe: false })
    expect(p.hint).not.toContain('点击')
    expect(p.hint).toContain('本机运行')
  })

  it('长期无数据 + 值全是推断 → 折叠（那行绿条上的 100% 是缺省猜测，不是事实）', () => {
    const p = stalePresentation({
      ...base, stale: true, allInferred: true, ageSeconds: 15 * 24 * 3600,
    })
    expect(p.collapse).toBe(true)
  })

  it('只是过期、但值是实测的 → 仍要显示，不折叠', () => {
    const p = stalePresentation({
      ...base, stale: true, allInferred: false, ageSeconds: 15 * 24 * 3600,
    })
    expect(p.collapse).toBe(false)
  })

  it('推断值但还很新 → 不折叠（刚重置本来就该显示满格）', () => {
    const p = stalePresentation({
      ...base, stale: true, allInferred: true, ageSeconds: COLLAPSE_AFTER_SECONDS - 1,
    })
    expect(p.collapse).toBe(false)
  })
})

describe('humanAge', () => {
  it('粗粒度即可 —— 要回答的是"多旧"，不是"精确多旧"', () => {
    expect(humanAge(90)).toBe('2 分钟')
    expect(humanAge(15 * 3600)).toBe('15 小时')
    expect(humanAge(15 * 24 * 3600)).toBe('15 天')
  })
  it('不合法输入给空串，不编造', () => {
    expect(humanAge(-1)).toBe('')
    expect(humanAge(NaN)).toBe('')
  })
})

// ── 被取代的家族 ──────────────────────────────────────────────────────────────────
// 这几条同时是**跨仓契约的守卫**：它们钉住"顶层 family = 当前生效家族"这个来自 kit 的假设。
// 哪天 kit 改了 QuotaInfo.family 的含义，这里会红，而不是让用户又一次静默地读一个作废数字。
import { isSupersededFamily, supersededNote } from '../quotaStaleness'

describe('isSupersededFamily', () => {
  it('家族与当前生效的不一致 → 已被取代（Human 实测那条 43% 的 codex 行）', () => {
    expect(isSupersededFamily('codex', 'premium')).toBe(true)
  })

  it('就是当前家族 → 不是历史', () => {
    expect(isSupersededFamily('premium', 'premium')).toBe(false)
  })

  it('不知道当前是哪个家族 → 一律 false（不知道 ≠ 已作废）', () => {
    expect(isSupersededFamily('codex', '')).toBe(false)
    expect(isSupersededFamily('', 'premium')).toBe(false)
    expect(isSupersededFamily('', '')).toBe(false)
  })
})

describe('supersededNote', () => {
  it('说清它是什么、以及该看哪一行', () => {
    const n = supersededNote('codex', 'premium')
    expect(n).toContain('codex')
    expect(n).toContain('premium')
    expect(n).toContain('历史读数')
  })
})
