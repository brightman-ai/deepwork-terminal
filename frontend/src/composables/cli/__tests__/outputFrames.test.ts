import { describe, expect, it } from 'bun:test'
import type { FrameScheduler } from '../framePaint'
import { handleControlFrame, handleOutputFrame, type OutputFrameDeps } from '../outputFrames'
import { createLatencyTracker } from '../latencyProbe'
import {
  RESTORE_PAINT_FALLBACK_MS,
  RESYNC_OVERLAY_DELAY_MS,
  createDelayedReveal,
  createRestoreGate,
  prefixBytes,
  restoreOverlayDue,
} from '../restoreGate'

const enc = new TextEncoder()
const dec = new TextDecoder()
const RESET = enc.encode('<RESET>')

/**
 * 一个假的"终端 + 浏览器"：write 只排队，parse() 才解析（调用写回调），frame() 才跑一帧 rAF；
 * 时间、定时器、rAF 全由测试手动推进。遮罩的显隐用真实的 restoreOverlayDue + createDelayedReveal。
 */
function harness(opts: { active?: boolean } = {}) {
  let now = 0
  let nextId = 1
  const rafs = new Map<number, () => void>()
  const timers = new Map<number, { at: number; cb: () => void }>()
  const log: string[] = []
  const writes: { text: string; onParsed?: () => void }[] = []
  const restoreFlags: boolean[] = []
  let hidden = false
  let epoch = 0
  let active = opts.active ?? true
  let status: 'connecting' | 'connected' = 'connecting'

  const sched: FrameScheduler = {
    raf: (cb) => { const id = nextId++; rafs.set(id, cb); return id },
    cancelRaf: (id) => { rafs.delete(id) },
    setTimeout: (cb, ms) => { const id = nextId++; timers.set(id, { at: now + ms, cb }); return id },
    clearTimeout: (id) => { timers.delete(id as number) },
  }
  const revealTimers = {
    setTimeout: sched.setTimeout,
    clearTimeout: sched.clearTimeout,
  }

  let overlay = false
  const reveal = createDelayedReveal(RESYNC_OVERLAY_DELAY_MS, (v) => { overlay = v; log.push(v ? 'overlay:show' : 'overlay:hide') }, revealTimers)
  const latency = createLatencyTracker()
  const gate = createRestoreGate(() => sync())
  function sync() {
    reveal.set(restoreOverlayDue({ owed: gate.owed, active, status }))
  }

  const deps: OutputFrameDeps = {
    gate,
    latency,
    write: (data, onParsed, restoring) => {
      restoreFlags.push(!!restoring)
      const text = dec.decode(data)
      writes.push({ text, onParsed })
      log.push(`write:${text}`)
    },
    sched,
    now: () => now,
    visibilityEpoch: () => epoch,
    hidden: () => hidden,
    resetBytes: RESET,
  }

  const h = {
    deps,
    gate,
    latency,
    writes,
    restoreFlags,
    log,
    get overlay() { return overlay },
    get now() { return now },
    /** 还排着的 rAF + 定时器数。 */
    pendingSchedules: () => rafs.size + timers.size,
    setStatus(s: 'connecting' | 'connected') { status = s; sync() },
    setActive(a: boolean) { active = a; sync() },
    setHidden(v: boolean) { hidden = v },
    bumpEpoch() { epoch++ },
    connect() { gate.connecting(); sync() },
    advance(ms: number) {
      const end = now + ms
      for (;;) {
        const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0]
        if (!due) break
        now = Math.max(now, due[1].at)
        timers.delete(due[0])
        due[1].cb()
      }
      now = end
    },
    /** xterm 解析完队列里最早的一次写入。 */
    parse() {
      const w = writes.shift()
      if (w) { log.push('parsed'); w.onParsed?.() }
    },
    /** 跑一帧 rAF（此时 xterm 的渲染已先于我们的回调完成）。 */
    frame() {
      const all = [...rafs.values()]
      rafs.clear()
      all.forEach(cb => cb())
    },
    binary(text: string) { handleOutputFrame(deps, enc.encode(text)) },
    control(type: string) { handleControlFrame(deps, type) },
  }
  return h
}

describe('REQ-TS-16 遮罩显隐时刻 vs 恢复写入回调时刻', () => {
  it('only a replacement frame asks the xterm owner to preserve its viewport', () => {
    const h = harness()
    h.binary('live output')
    h.control('replay_reset')
    h.binary('replacement')
    h.binary('new output')
    expect(h.restoreFlags).toEqual([false, true, false])
  })
  it('Human 报的序列：遮罩撑到恢复"解析完并画出"才撤；清屏与恢复是同一次写入，之前屏幕不被清', () => {
    const h = harness()
    h.connect()
    h.advance(200) // 连接/恢复慢，遮罩亮了
    expect(h.overlay).toBe(true)

    h.setStatus('connected')
    h.control('resized')
    h.control('heartbeat_ack') // 旧实现：任何控制帧都撤遮罩
    h.control('replay_reset')
    expect(h.overlay).toBe(true)
    expect(h.writes).toHaveLength(0) // 清屏没有单独写下去

    h.binary('SCREEN')
    expect(h.writes).toHaveLength(1)
    expect(h.writes[0].text).toBe('<RESET>SCREEN') // 清屏 + 恢复 = 一次 write
    expect(h.overlay).toBe(true)

    h.control('session_meta') // 紧随恢复帧到达，比解析完成还早
    expect(h.overlay).toBe(true)

    h.parse() // 终端把恢复字节解析完
    expect(h.overlay).toBe(true) // 解析完 ≠ 画出
    h.frame() // 下一帧：xterm 已画
    expect(h.overlay).toBe(false)
    expect(h.log.filter(l => l.startsWith('overlay') || l === 'parsed')).toEqual(['overlay:show', 'parsed', 'overlay:hide'])
  })

  it('激活后 150ms 内完成的恢复：遮罩从未出现', () => {
    const h = harness()
    h.connect()
    h.advance(RESYNC_OVERLAY_DELAY_MS - 50)
    h.setStatus('connected')
    h.control('resized')
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    h.advance(1000)
    expect(h.log.some(l => l.startsWith('overlay'))).toBe(false)
  })

  it('从未激活的 tab 第一次被切到：挂载起就欠着账，但遮罩的计时从激活才开始', () => {
    const h = harness({ active: false })
    h.connect()
    h.advance(30_000)
    expect(h.overlay).toBe(false)
    h.setActive(true)
    expect(h.overlay).toBe(false)
    h.advance(100)
    h.setStatus('connected')
    h.control('resized')
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    h.advance(1000)
    expect(h.log.some(l => l.startsWith('overlay'))).toBe(false)
  })

  it('空恢复帧也走完整流程：清屏照样写（屏上留的是上一屏），解析画出后才撤', () => {
    const h = harness()
    h.connect()
    h.advance(300)
    h.setStatus('connected')
    h.control('replay_reset')
    h.binary('')
    expect(h.writes[0].text).toBe('<RESET>')
    h.parse()
    expect(h.overlay).toBe(true)
    h.frame()
    expect(h.overlay).toBe(false)
  })

  it('rAF 被挂起（后台页）：解析完后兜底定时器到点放行，遮罩不会卡死', () => {
    const h = harness()
    h.connect()
    h.advance(300)
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.advance(RESTORE_PAINT_FALLBACK_MS - 1)
    expect(h.overlay).toBe(true)
    h.advance(1)
    expect(h.overlay).toBe(false)
  })

  it('丢帧重同步：已经还清的账重新欠上，同样撑到新恢复画出', () => {
    const h = harness()
    h.connect()
    h.setStatus('connected')
    h.control('replay_reset')
    h.binary('A')
    h.parse()
    h.frame()
    expect(h.gate.owed).toBe(false)

    h.control('replay_reset') // 浏览器消费跟不上，服务端整体重同步
    expect(h.gate.owed).toBe(true)
    h.advance(RESYNC_OVERLAY_DELAY_MS)
    expect(h.overlay).toBe(true)
    h.binary('B')
    expect(h.writes.at(-1)!.text).toBe('<RESET>B')
    h.parse()
    h.frame()
    expect(h.overlay).toBe(false)
  })

  it('replay_reset 后没有恢复帧、先来了 session_meta：补写清屏，画出才撤', () => {
    const h = harness()
    h.connect()
    h.advance(300)
    h.control('replay_reset')
    h.control('session_meta')
    expect(h.writes).toHaveLength(1)
    expect(h.writes[0].text).toBe('<RESET>')
    expect(h.overlay).toBe(true)
    h.parse()
    h.frame()
    expect(h.overlay).toBe(false)
  })

  it('恢复刚发出就 shell_exit：立刻补清屏并撤遮罩，提示行不被盖住', () => {
    const h = harness()
    h.connect()
    h.advance(300)
    h.control('replay_reset')
    h.control('shell_exit')
    expect(h.writes.map(w => w.text)).toEqual(['<RESET>'])
    expect(h.overlay).toBe(false)
  })

  it('恢复之后的实时帧：原样写入，不拼清屏，也不再碰遮罩', () => {
    const h = harness()
    h.connect()
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    h.binary('live')
    expect(h.writes.at(-1)!.text).toBe('live')
  })
})

describe('REQ-TS-17 帧时序 → 分段计时', () => {
  /** 一次按键 → 回显：returns 画出后的摘要。 */
  function typedEcho(h: ReturnType<typeof harness>, o: { net: number; parseMs: number; paintMs: number }) {
    h.latency.noteSend(h.now, 0)
    h.advance(o.net)
    h.binary('x') // 回显帧到达
    h.advance(o.parseMs)
    h.parse() // 解析完
    h.advance(o.paintMs)
    h.frame() // 下一帧
    return h.latency.summary(h.now)
  }

  it('发送 → 到达 → 解析 → 画出：每一段取自对应的那个时刻', () => {
    const h = harness()
    h.connect()
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    const s = typedEcho(h, { net: 790, parseMs: 12, paintMs: 10 })!
    expect(s.p50).toEqual({ total: 812, net: 790, parse: 12, paint: 10 })
  })

  it('恢复帧不是回显：待回显的探针被作废，恢复帧自己不产生样本', () => {
    const h = harness()
    h.connect()
    h.latency.noteSend(0, 0) // 连接前后发的按键
    h.advance(50)
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    expect(h.latency.summary(h.now)).toBeNull()
  })

  it('没有待回显的发送时，实时帧不预约任何绘制回调（热路径零开销）', () => {
    const h = harness()
    h.connect()
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    expect(h.pendingSchedules()).toBe(0)
    h.binary('spinner')
    h.parse()
    // 没有 rAF/定时器被排上：解析完就结束
    expect(h.pendingSchedules()).toBe(0)
    expect(h.latency.summary(h.now)).toBeNull()
  })

  it('主线程卡了一阵之后兜底定时器先跑：这是一次真实的慢绘制，样本保留', () => {
    const h = harness()
    h.connect()
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    h.latency.noteSend(h.now, 0)
    h.advance(40)
    h.binary('x')
    h.parse()
    h.advance(RESTORE_PAINT_FALLBACK_MS + 300) // rAF 没来，定时器到点
    const s = h.latency.summary(h.now)!
    expect(s.n).toBe(1)
    expect(s.p50.paint).toBeGreaterThanOrEqual(RESTORE_PAINT_FALLBACK_MS)
  })

  it('页面在后台时画出的样本不可信：丢弃，不截断', () => {
    const h = harness()
    h.connect()
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    h.latency.noteSend(h.now, 0)
    h.advance(40)
    h.binary('x')
    h.parse()
    h.setHidden(true)
    h.advance(RESTORE_PAINT_FALLBACK_MS)
    expect(h.latency.summary(h.now)).toBeNull()
  })

  it('发送到画出之间切走又切回（可见性纪元变了）：丢弃', () => {
    const h = harness()
    h.connect()
    h.control('replay_reset')
    h.binary('SCREEN')
    h.parse()
    h.frame()
    h.latency.noteSend(h.now, 0)
    h.advance(40)
    h.binary('x')
    h.parse()
    h.bumpEpoch()
    h.frame()
    expect(h.latency.summary(h.now)).toBeNull()
  })
})

describe('prefixBytes', () => {
  it('前缀在前、原字节在后，不改动入参', () => {
    const a = enc.encode('AB')
    const b = enc.encode('CDE')
    const out = prefixBytes(a, b)
    expect(dec.decode(out)).toBe('ABCDE')
    expect(dec.decode(a)).toBe('AB')
    expect(dec.decode(b)).toBe('CDE')
    expect(dec.decode(prefixBytes(a, new Uint8Array(0)))).toBe('AB')
  })
})
