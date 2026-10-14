import { describe, expect, it } from 'bun:test'
import {
  LATENCY_MAX_WAIT_MS,
  LATENCY_REPORT_EVERY_MS,
  LATENCY_WINDOW,
  createLatencyTracker,
  expectsEcho,
  formatMs,
  latencyView,
  sameSummary,
  type LatencyTracker,
} from '../latencyProbe'

const bytes = (s: string) => new TextEncoder().encode(s)

/** 一次完整的"发送 → 回显到达 → 解析 → 画出"，返回画出的时刻。 */
function echo(t: LatencyTracker, o: { sentAt: number; net: number; parse: number; paint: number; epoch?: number; hidden?: boolean }): number {
  t.noteSend(o.sentAt, o.epoch ?? 0)
  const arrived = o.sentAt + o.net
  const token = t.noteOutput(arrived, 'live')
  if (token === null) return arrived
  const parsed = arrived + o.parse
  t.noteParsed(token, parsed)
  const painted = parsed + o.paint
  t.notePainted(token, painted, o.epoch ?? 0, o.hidden ?? false)
  return painted
}

describe('REQ-TS-17 expectsEcho：只有预期有可见回应的输入才开探针', () => {
  it('可打印字符、回车、退格：是', () => {
    expect(expectsEcho(bytes('a'))).toBe(true)
    expect(expectsEcho(bytes('hello 世界'))).toBe(true)
    expect(expectsEcho(bytes('\r'))).toBe(true)
    expect(expectsEcho(new Uint8Array([0x7f]))).toBe(true)
  })

  it('方向键 / 功能键 / 单独的 Esc / Alt+键：否（常常没有可见变化）', () => {
    expect(expectsEcho(bytes('\x1b[A'))).toBe(false)
    expect(expectsEcho(bytes('\x1bOP'))).toBe(false)
    expect(expectsEcho(bytes('\x1b[5~'))).toBe(false)
    expect(expectsEcho(bytes('\x1b'))).toBe(false)
    expect(expectsEcho(bytes('\x1bb'))).toBe(false)
  })

  it('终端自动应答与上报：鼠标 / 焦点 / CPR / DA / OSC 颜色应答——否', () => {
    expect(expectsEcho(bytes('\x1b[<0;10;5M'))).toBe(false)
    expect(expectsEcho(bytes('\x1b[I'))).toBe(false)
    expect(expectsEcho(bytes('\x1b[12;40R'))).toBe(false)
    expect(expectsEcho(bytes('\x1b[?1;2c'))).toBe(false)
    expect(expectsEcho(bytes('\x1b]11;rgb:1e1e/1e1e/2e2e\x07'))).toBe(false)
    expect(expectsEcho(bytes('\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\'))).toBe(false)
  })

  it('括号粘贴：转义头尾之间的正文才是回显的由头', () => {
    expect(expectsEcho(bytes('\x1b[200~pasted\x1b[201~'))).toBe(true)
    expect(expectsEcho(bytes('\x1b[200~\x1b[201~'))).toBe(false)
  })

  it('混合：方向键 + 一个字符 → 是；纯控制字符（Ctrl+A）→ 否', () => {
    expect(expectsEcho(bytes('\x1b[Ax'))).toBe(true)
    expect(expectsEcho(new Uint8Array([0x01]))).toBe(false)
  })
})

describe('REQ-TS-17 分段计时的配对与汇总', () => {
  it('一次发送 → 一个回显 → 三段相加等于总时长', () => {
    const t = createLatencyTracker()
    const painted = echo(t, { sentAt: 1000, net: 790, parse: 12, paint: 10 })
    const s = t.summary(painted)!
    expect(s.n).toBe(1)
    expect(s.p50).toEqual({ total: 812, net: 790, parse: 12, paint: 10 })
  })

  it('等待期间的后续发送并入最早那次：粘贴分片 / 快速连敲只算一次等待，且按最早的发送算', () => {
    const t = createLatencyTracker()
    t.noteSend(0, 0)
    t.noteSend(30, 0)
    t.noteSend(60, 0)
    const token = t.noteOutput(100, 'live')!
    t.noteParsed(token, 105)
    t.notePainted(token, 116, 0, false)
    const s = t.summary(116)!
    expect(s.n).toBe(1)
    expect(s.p50.total).toBe(116) // 从 t=0 那一次算起，不是最后一次
    expect(s.p50.net).toBe(100)
  })

  it('回显已在路上（已被配对）时的新发送，开新的探针，不混进旧的', () => {
    const t = createLatencyTracker()
    t.noteSend(0, 0)
    const first = t.noteOutput(50, 'live')!
    t.noteSend(60, 0) // 第一个回显还在解析，第二次按键已经发出
    t.noteParsed(first, 55)
    t.notePainted(first, 66, 0, false)
    const second = t.noteOutput(120, 'live')!
    t.noteParsed(second, 122)
    t.notePainted(second, 133, 0, false)
    const s = t.summary(133)!
    expect(s.n).toBe(2)
    expect(s.maxTotal).toBe(73) // 60 → 133
  })

  it('没有待回显的发送时到达的输出帧（实时输出、动画）不产生样本', () => {
    const t = createLatencyTracker()
    expect(t.noteOutput(10, 'live')).toBeNull()
    expect(t.summary(10)).toBeNull()
  })

  it('恢复帧不是回显：它作废待回显的探针，下一个实时帧不会被误当成这次发送的回显', () => {
    const t = createLatencyTracker()
    t.noteSend(0, 0)
    expect(t.noteOutput(40, 'restore')).toBeNull() // 重同步的恢复帧
    expect(t.noteOutput(500, 'live')).toBeNull() // 之后的实时帧：没有探针了
    expect(t.summary(500)).toBeNull()
  })

  it('超过最长等待仍无输出：不记样本（分不清卡死与"这个键本来就没回显"），计入 unanswered', () => {
    const t = createLatencyTracker()
    t.noteSend(0, 0)
    expect(t.noteOutput(LATENCY_MAX_WAIT_MS + 1, 'live')).toBeNull()
    t.noteSend(0, 0)
    t.noteSend(LATENCY_MAX_WAIT_MS + 1, 0) // 旧的过期，新的接棒
    const token = t.noteOutput(LATENCY_MAX_WAIT_MS + 51, 'live')!
    t.noteParsed(token, LATENCY_MAX_WAIT_MS + 52)
    t.notePainted(token, LATENCY_MAX_WAIT_MS + 60, 0, false)
    expect(t.summary(LATENCY_MAX_WAIT_MS + 60)!.p50.total).toBe(59)
  })

  it('后台页的样本丢弃、不截断：可见性纪元变了，或画出时页面是隐藏的', () => {
    const t = createLatencyTracker()
    echo(t, { sentAt: 0, net: 50, parse: 5, paint: 5, epoch: 0, hidden: true })
    echo(t, { sentAt: 1000, net: 50, parse: 5, paint: 5 })
    // 发送时纪元 0，画出时已是纪元 1（中途切走又切回）
    t.noteSend(2000, 0)
    const token = t.noteOutput(2050, 'live')!
    t.noteParsed(token, 2055)
    t.notePainted(token, 180_000, 1, false)
    const s = t.summary(180_000)
    expect(s).toBeNull() // 2 个丢弃，1 个有效但已过保鲜期
    const kept = t.summary(1060)!
    expect(kept.n).toBe(1)
    expect(kept.p50.total).toBe(60)
  })

  it('p50 取"总时长居中的那条真实样本"，它的三段仍相加等于总数', () => {
    const t = createLatencyTracker()
    let at = 0
    // 总时长：20, 30(解析慢), 800(网络慢), 40, 50 → 居中的是 40
    for (const o of [
      { net: 15, parse: 3, paint: 2 },
      { net: 10, parse: 18, paint: 2 },
      { net: 790, parse: 6, paint: 4 },
      { net: 30, parse: 5, paint: 5 },
      { net: 38, parse: 6, paint: 6 },
    ]) {
      at = echo(t, { sentAt: at + 100, ...o })
    }
    const s = t.summary(at)!
    expect(s.n).toBe(5)
    expect(s.p50).toEqual({ total: 40, net: 30, parse: 5, paint: 5 })
    expect(s.p50.net + s.p50.parse + s.p50.paint).toBe(s.p50.total)
    expect(s.maxTotal).toBe(800)
  })

  it('p95 > 1s 才算 slow：先偏慢的网络不够，要端到端', () => {
    const fast = createLatencyTracker()
    let at = 0
    for (let i = 0; i < 19; i++) at = echo(fast, { sentAt: at + 100, net: 40, parse: 5, paint: 5 })
    at = echo(fast, { sentAt: at + 100, net: 3000, parse: 5, paint: 5 }) // 20 个里一个离群：p95 是第 19 名
    expect(fast.summary(at)!.slow).toBe(false)

    const bad = createLatencyTracker()
    at = 0
    for (let i = 0; i < 17; i++) at = echo(bad, { sentAt: at + 100, net: 40, parse: 5, paint: 5 })
    for (let i = 0; i < 3; i++) at = echo(bad, { sentAt: at + 100, net: 3000, parse: 5, paint: 5 })
    const s = bad.summary(at)!
    expect(s.p95Total).toBeGreaterThan(1000)
    expect(s.slow).toBe(true)
  })

  it('窗口只留最近 LATENCY_WINDOW 条', () => {
    const t = createLatencyTracker()
    let at = 0
    for (let i = 0; i < LATENCY_WINDOW + 7; i++) at = echo(t, { sentAt: at + 50, net: 10 + i, parse: 1, paint: 1 })
    const s = t.summary(at)!
    expect(s.n).toBe(LATENCY_WINDOW)
    // 最老的 7 条（net 10..16）已被挤掉
    expect(s.maxTotal).toBe(10 + LATENCY_WINDOW + 6 + 2)
  })

  it('样本过了保鲜期就不再说话；重连清空', () => {
    const t = createLatencyTracker()
    const at = echo(t, { sentAt: 0, net: 100, parse: 1, paint: 1 })
    expect(t.summary(at + 119_000)).not.toBeNull()
    expect(t.summary(at + 121_000)).toBeNull()
    t.reset()
    expect(t.summary(at)).toBeNull()
  })
})

describe('REQ-TS-17 样本周期汇总进诊断上报', () => {
  it('没有新样本不报；第一次调用只定锚点；满一个周期且有新样本才报，报完清零', () => {
    const t = createLatencyTracker()
    expect(t.takeReport(0)).toBeNull() // 锚点
    const at = echo(t, { sentAt: 1000, net: 100, parse: 2, paint: 3 })
    expect(t.takeReport(at)).toBeNull() // 周期没到
    const r = t.takeReport(LATENCY_REPORT_EVERY_MS)!
    expect(r.newSamples).toBe(1)
    expect(r.p50.total).toBe(105)
    expect(r.discarded).toBe(0)
    expect(t.takeReport(LATENCY_REPORT_EVERY_MS * 2)).toBeNull() // 没有新样本
  })

  it('刚变慢时提前报（距上次 ≥ 最小间隔），不用等满周期；已报过慢就不重复提前报', () => {
    const t = createLatencyTracker()
    t.takeReport(0)
    let at = 0
    for (let i = 0; i < 3; i++) at = echo(t, { sentAt: at + 100, net: 2500, parse: 5, paint: 5 })
    expect(t.takeReport(5_000)).toBeNull() // 距锚点不足最小间隔
    const early = t.takeReport(15_000)!
    expect(early.slow).toBe(true)
    at = echo(t, { sentAt: 20_000, net: 2500, parse: 5, paint: 5 })
    expect(t.takeReport(40_000)).toBeNull() // 仍然慢，不是"刚变慢"
  })

  it('上报里带丢弃数与无回应数', () => {
    const t = createLatencyTracker()
    t.takeReport(0)
    echo(t, { sentAt: 0, net: 10, parse: 1, paint: 1, hidden: true })
    t.noteSend(100, 0)
    t.noteOutput(100 + LATENCY_MAX_WAIT_MS + 1, 'live')
    echo(t, { sentAt: 20_000, net: 10, parse: 1, paint: 1 })
    const r = t.takeReport(LATENCY_REPORT_EVERY_MS)!
    expect(r.discarded).toBe(1)
    expect(r.unanswered).toBe(1)
  })
})

describe('REQ-TS-17 悬停内容：读得懂', () => {
  it('有样本：指示数字是体验延迟 p50；悬停给三段 + 纯网络往返 + p95', () => {
    const t = createLatencyTracker()
    const at = echo(t, { sentAt: 0, net: 790, parse: 12, paint: 10 })
    const v = latencyView(t.summary(at), 38)
    expect(v.chipRtt).toBe(812)
    expect(v.slow).toBe(false)
    expect(v.label).toBe('体验延迟 812ms（近 1 次输入的中位）')
    expect(v.lines).toEqual([
      '网络+程序 790ms',
      '浏览器解析 12ms',
      '绘制 10ms',
      '纯网络往返 38ms（心跳，不含程序处理）',
      'p95 812ms',
    ])
  })

  it('慢：悬停点明"端到端明显偏慢"，slow 标志驱动变色', () => {
    const t = createLatencyTracker()
    const at = echo(t, { sentAt: 0, net: 4600, parse: 20, paint: 12 })
    const v = latencyView(t.summary(at), 40)
    expect(v.slow).toBe(true)
    expect(v.chipRtt).toBe(4632)
    expect(v.lines.at(-1)).toBe('p95 4.6s：端到端明显偏慢')
  })

  it('没有样本：退回纯网络往返，并且说清楚这个数不是体验延迟', () => {
    const v = latencyView(null, 38)
    expect(v.chipRtt).toBe(38)
    expect(v.slow).toBe(false)
    expect(v.label).toBe('网络往返 38ms')
    expect(v.lines[0]).toContain('还没有输入样本')
    expect(latencyView(null, 0).chipRtt).toBe(0)
  })

  it('formatMs 与 sameSummary', () => {
    expect(formatMs(12.4)).toBe('12ms')
    expect(formatMs(999.6)).toBe('1000ms')
    expect(formatMs(1500)).toBe('1.5s')
    expect(formatMs(12_345)).toBe('12s')
    const t = createLatencyTracker()
    const at = echo(t, { sentAt: 0, net: 100, parse: 1, paint: 1 })
    const before = t.summary(at)
    expect(sameSummary(before, t.summary(at + 1))).toBe(true)
    const later = echo(t, { sentAt: at + 10, net: 300, parse: 1, paint: 1 })
    expect(sameSummary(before, t.summary(later))).toBe(false)
    expect(sameSummary(null, null)).toBe(true)
    expect(sameSummary(null, before)).toBe(false)
  })
})
