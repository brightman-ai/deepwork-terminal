import { describe, it, expect } from 'bun:test'
import {
  LIVENESS_COLOR,
  LIVENESS_LABEL,
  LIVENESS_MOTION,
  livenessCopy,
  type TabNotLive,
} from '../tabLiveness'
import { STATUS_COLOR, STATUS_MOTION } from '../useAgentOverview'

const NOT_LIVE: TabNotLive[] = ['detached', 'unreachable']

describe('存活态的表现层常量（STATUS_COLOR / STATUS_MOTION 家族的对等物）', () => {
  it('颜色/文案表对每个非 live 态都是完备的（缺一项 = 某个界面会画出空白）', () => {
    expect(Object.keys(LIVENESS_COLOR).sort()).toEqual(['detached', 'unreachable'])
    expect(Object.keys(LIVENESS_LABEL).sort()).toEqual(['detached', 'unreachable'])
    expect(Object.keys(LIVENESS_MOTION).sort()).toEqual(['detached', 'unreachable'])
  })

  it('两档都是中性灰：R≈G≈B，且不撞任何一个 agent 状态色', () => {
    // 红被 waiting（等你输入）占着 = "你现在得做点什么"；绿/琥珀都表示还在动。
    // 一个进程已经结束的标签不是故障、也没在动，用它们中的任何一个都会说错话。
    const statusHexes = Object.values(STATUS_COLOR).map((h) => h.toLowerCase())
    for (const l of NOT_LIVE) {
      const hex = LIVENESS_COLOR[l]
      expect(hex).toMatch(/^#[0-9a-f]{6}$/i)
      const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
      const spread = Math.max(r, g, b) - Math.min(r, g, b)
      expect(spread).toBeLessThanOrEqual(24) // 近乎无彩度 = 中性灰
      expect(statusHexes).not.toContain(hex.toLowerCase())
    }
  })

  it('两档都不脉冲——脉冲在这套语言里表达的是"我还活着"', () => {
    // 对照：running/waiting 有节奏，done-unseen 明确为 null。这里两档都跟 done-unseen 同侧。
    expect(STATUS_MOTION.running).not.toBeNull()
    expect(STATUS_MOTION['done-unseen']).toBeNull()
    for (const l of NOT_LIVE) expect(LIVENESS_MOTION[l]).toBeNull()
  })

  it('detached 比 unreachable 亮：确定的事实值得看见，还没拿到的事实不该抢注意力', () => {
    const lum = (hex: string) => [1, 3, 5].reduce((s, i) => s + parseInt(hex.slice(i, i + 2), 16), 0)
    expect(lum(LIVENESS_COLOR.detached)).toBeGreaterThan(lum(LIVENESS_COLOR.unreachable))
  })
})

describe('livenessCopy — 按钮文案必须说清点下去会发生什么', () => {
  it('已确认结束：主按钮是"新建"，且把原目录写进文案（不是"恢复"——我们恢复不了）', () => {
    const c = livenessCopy('detached', { cwd: '/repo/deepwork-terminal' })
    expect(c.primary).toBe('在此目录新建终端')
    expect(c.primaryHint).toContain('/repo/deepwork-terminal')
    expect(c.primaryHint).toContain('不会被接回来')
    // "恢复"是承诺不了的动词：那个进程已经没了，接不回来。
    expect(c.primary).not.toContain('恢复')
    expect(c.headline).not.toContain('恢复')
  })

  it('已确认结束：说清"随服务重启结束了 + 没在后台继续跑"', () => {
    const c = livenessCopy('detached', {})
    expect(c.body).toContain('服务重启')
    expect(c.body).toContain('没有在后台继续跑')
  })

  it('目录未知（比如 pro 不持久化每个终端的目录）时不硬凑一个假路径', () => {
    const c = livenessCopy('detached', {})
    expect(c.primary).toBe('在这里新建终端')
    expect(c.primaryHint).not.toContain('undefined')
    // '~' 是占位符不是真目录，不该当成"此目录"展示。
    expect(livenessCopy('detached', { cwd: '~' }).primary).toBe('在这里新建终端')
  })

  it('问不到：主按钮是"重新检查"，且明说不会新建也不会结束进程', () => {
    const c = livenessCopy('unreachable', { remote: true, machineLabel: 'stmac' })
    expect(c.primary).toBe('重新检查一次')
    expect(c.primaryHint).toContain('不会新建')
    expect(c.body).toContain('可能还在')
    expect(c.headline).toContain('stmac')
  })

  it('远程已结束：文案落到那台机器上，而不是含糊的"服务"', () => {
    const c = livenessCopy('detached', { remote: true, machineLabel: 'stmac' })
    expect(c.body).toContain('stmac')
  })

  it('每一段文案都不为空，且不含 emoji', () => {
    for (const l of NOT_LIVE) {
      for (const remote of [false, true]) {
        const c = livenessCopy(l, { remote, machineLabel: 'stmac', cwd: '/tmp/x' })
        for (const [k, v] of Object.entries(c)) {
          expect(v.length, `${l}/${remote}/${k}`).toBeGreaterThan(0)
          expect(/\p{Extended_Pictographic}/u.test(v), `${l}/${remote}/${k} 含 emoji`).toBe(false)
        }
      }
    }
  })
})
