import { describe, expect, it } from 'bun:test'
import {
  RESYNC_OVERLAY_DELAY_MS,
  createDelayedReveal,
  createRestoreGate,
  restoreOverlayDue,
  type RevealTimers,
} from '../restoreGate'

/** 手动推进的定时器。 */
function fakeTimers() {
  let now = 0
  let nextId = 1
  const timers = new Map<number, { at: number; cb: () => void }>()
  const t: RevealTimers = {
    setTimeout: (cb, ms) => { const id = nextId++; timers.set(id, { at: now + ms, cb }); return id },
    clearTimeout: (id) => { timers.delete(id as number) },
  }
  return {
    t,
    advance(ms: number) {
      now += ms
      for (const [id, tm] of [...timers]) {
        if (tm.at <= now) { timers.delete(id); tm.cb() }
      }
    },
    pending: () => timers.size,
  }
}

describe('REQ-TS-16 restoreGate：欠账以"恢复字节已解析并画出"为准，不是"收到第一帧"', () => {
  it('服务端的恢复序列：resized / replay_reset / 恢复帧 / session_meta —— 每一步遮罩都还欠着，画出才还清', () => {
    const owedLog: boolean[] = []
    const gate = createRestoreGate(o => owedLog.push(o))
    gate.connecting()
    expect(gate.owed).toBe(true)

    // 旧实现的缺陷：任何控制帧都算"已同步"。resized 是第一帧，它一到遮罩就撤了。
    gate.control('resized')
    expect(gate.owed).toBe(true)

    gate.replayReset()
    expect(gate.owed).toBe(true)

    const v = gate.binary()
    expect(v.restore).toBe(true)
    expect(v.prefixReset).toBe(true)
    // 恢复帧写给终端了，但还在解析：此刻撤遮罩 = 让使用者看到清屏 + 逐步刷屏。
    expect(gate.owed).toBe(true)

    // session_meta 紧跟在恢复帧之后到达，常常早于解析完成
    expect(gate.control('session_meta').flushReset).toBe(false)
    expect(gate.owed).toBe(true)

    expect(gate.restorePainted(v.epoch)).toBe(true)
    expect(gate.owed).toBe(false)
    // 只在状态变化时通知：初始 true 之后不重复发 true
    expect(owedLog).toEqual([false])
  })

  it('空恢复帧（服务端对没有内容的会话也会发一个空二进制帧）同样走完整流程', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.control('resized')
    gate.replayReset()
    const v = gate.binary() // 调用方写入的是 [清屏] + 空字节
    expect(v.restore).toBe(true)
    expect(gate.owed).toBe(true)
    gate.restorePainted(v.epoch)
    expect(gate.owed).toBe(false)
  })

  it('恢复之后的实时帧不是恢复帧，也不再拼清屏；迟到的 paste 模式帧同理', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.replayReset()
    const restore = gate.binary()
    gate.restorePainted(restore.epoch)
    const live = gate.binary()
    expect(live).toMatchObject({ restore: false, prefixReset: false })
  })

  it('恢复帧之后、画出之前又来了帧（迟到的 paste 模式帧）：不是恢复帧，欠账不变', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.replayReset()
    const restore = gate.binary()
    expect(gate.binary().restore).toBe(false)
    expect(gate.owed).toBe(true)
    gate.restorePainted(restore.epoch)
    expect(gate.owed).toBe(false)
  })

  it('过期纪元的"画出"回调被忽略：期间又重连/重同步了，旧恢复的画出不能还清新账', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.replayReset()
    const old = gate.binary()
    // 恢复还在画的时候，丢帧触发了一次重同步
    gate.replayReset()
    expect(gate.owed).toBe(true)
    expect(gate.restorePainted(old.epoch)).toBe(false)
    expect(gate.owed).toBe(true)
    const fresh = gate.binary()
    expect(gate.restorePainted(fresh.epoch)).toBe(true)
    expect(gate.owed).toBe(false)
  })

  it('连接中途的重同步（replay_reset）重新欠账——"恢复确实还没画好"才出现，与是不是首次无关', () => {
    const owedLog: boolean[] = []
    const gate = createRestoreGate(o => owedLog.push(o))
    gate.connecting()
    gate.replayReset()
    gate.restorePainted(gate.binary().epoch)
    expect(gate.owed).toBe(false)
    gate.replayReset() // 浏览器消费跟不上 → 服务端整体重同步
    expect(gate.owed).toBe(true)
    expect(owedLog).toEqual([false, true])
  })

  it('重连清空上一次的一切：重连之后的二进制帧在 replay_reset 到来之前不算恢复帧', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.replayReset() // 清屏被延后，恢复帧还没到
    gate.connecting() // 连接断了又重连
    expect(gate.owed).toBe(true)
    expect(gate.binary()).toMatchObject({ restore: false, prefixReset: false })
  })

  it('replay_reset 之后没有恢复帧、却来了 session_meta：把延后的清屏补写，画出才还清', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.replayReset()
    const v = gate.control('session_meta')
    expect(v).toMatchObject({ flushReset: true, awaitPaint: true })
    expect(gate.owed).toBe(true)
    gate.restorePainted(v.epoch)
    expect(gate.owed).toBe(false)
  })

  it('不走 replay_reset 分帧的对端：session_meta 即"初始爆发结束"，无账可还；resized 仍不算', () => {
    const gate = createRestoreGate()
    gate.connecting()
    gate.control('resized')
    gate.control('tmux_state')
    expect(gate.owed).toBe(true)
    gate.control('session_meta')
    expect(gate.owed).toBe(false)
  })

  it('shell_exit / preempted / error 立刻放行（遮罩不能盖住紧随其后的提示行），并补写延后的清屏', () => {
    for (const type of ['shell_exit', 'preempted', 'error']) {
      const gate = createRestoreGate()
      gate.connecting()
      gate.replayReset()
      const v = gate.control(type)
      expect(v).toMatchObject({ flushReset: true, awaitPaint: false })
      expect(gate.owed).toBe(false)
      // 之后到的"画出"回调是空操作
      expect(gate.restorePainted(v.epoch)).toBe(false)
    }
  })
})

describe('REQ-TS-16 遮罩显隐', () => {
  const base = { owed: true, active: true, status: 'connected' as const }

  it('到期条件：欠着 ∧ 正被看着 ∧ 在连/已连', () => {
    expect(restoreOverlayDue(base)).toBe(true)
    expect(restoreOverlayDue({ ...base, status: 'connecting' })).toBe(true)
    expect(restoreOverlayDue({ ...base, status: 'reconnecting' })).toBe(true)
    expect(restoreOverlayDue({ ...base, owed: false })).toBe(false)
    expect(restoreOverlayDue({ ...base, active: false })).toBe(false) // 没人看的 tab 不亮
    expect(restoreOverlayDue({ ...base, status: 'disconnected' })).toBe(false)
    expect(restoreOverlayDue({ ...base, status: 'preempted' })).toBe(false)
  })

  it('150ms 内完成的恢复：遮罩从未出现', () => {
    const shown: boolean[] = []
    const ft = fakeTimers()
    const reveal = createDelayedReveal(RESYNC_OVERLAY_DELAY_MS, v => shown.push(v), ft.t)
    reveal.set(true)
    ft.advance(RESYNC_OVERLAY_DELAY_MS - 1)
    reveal.set(false) // 恢复画出了
    ft.advance(1000)
    expect(shown).toEqual([])
    expect(ft.pending()).toBe(0)
  })

  it('恢复超过 150ms：满 150ms 亮，画出后立刻撤', () => {
    const shown: boolean[] = []
    const ft = fakeTimers()
    const reveal = createDelayedReveal(RESYNC_OVERLAY_DELAY_MS, v => shown.push(v), ft.t)
    reveal.set(true)
    ft.advance(RESYNC_OVERLAY_DELAY_MS)
    expect(shown).toEqual([true])
    reveal.set(false)
    expect(shown).toEqual([true, false])
  })

  it('重复的"到期"不重新计时：连续成立从第一次成立起算', () => {
    const shown: boolean[] = []
    const ft = fakeTimers()
    const reveal = createDelayedReveal(RESYNC_OVERLAY_DELAY_MS, v => shown.push(v), ft.t)
    reveal.set(true)
    ft.advance(100)
    reveal.set(true) // 比如 replay_reset 到了，账还是欠着
    ft.advance(50)
    expect(shown).toEqual([true])
  })

  it('从未激活过的 tab 第一次被切到：防抖从激活那一刻起算，不是从挂载起算', () => {
    const shown: boolean[] = []
    const ft = fakeTimers()
    const reveal = createDelayedReveal(RESYNC_OVERLAY_DELAY_MS, v => shown.push(v), ft.t)
    const gate = createRestoreGate()
    gate.connecting() // 挂载时 ws 状态就是 connecting，账从一开始就欠着
    let active = false
    const sync = () => reveal.set(restoreOverlayDue({ owed: gate.owed, active, status: 'connecting' }))

    sync()
    ft.advance(60_000) // 这个 tab 在后台躺了一分钟
    expect(shown).toEqual([]) // 旧实现：挂载时起的定时器早已到期，一激活遮罩就在

    active = true // 使用者切过来
    sync()
    expect(shown).toEqual([]) // 切过来的那一刻不亮
    ft.advance(100)
    // 100ms 后连接建立、恢复序列走完并画出
    gate.replayReset()
    gate.restorePainted(gate.binary().epoch)
    sync()
    ft.advance(1000)
    expect(shown).toEqual([]) // 激活后 150ms 内完成 → 遮罩从未出现
  })

  it('Human 报的时序：resized 到达那一刻遮罩不撤，要撑到恢复画出', () => {
    const shown: boolean[] = []
    const ft = fakeTimers()
    const reveal = createDelayedReveal(RESYNC_OVERLAY_DELAY_MS, v => shown.push(v), ft.t)
    const gate = createRestoreGate()
    let status: 'connecting' | 'connected' = 'connecting'
    const sync = () => reveal.set(restoreOverlayDue({ owed: gate.owed, active: true, status }))

    gate.connecting()
    sync() // t=0 激活
    ft.advance(200) // 连接慢，遮罩亮了
    expect(shown).toEqual([true])

    status = 'connected'
    gate.control('resized') // 旧实现在这里撤遮罩
    sync()
    expect(shown).toEqual([true])
    gate.replayReset()
    sync()
    expect(shown).toEqual([true]) // 清屏被延后；遮罩下面不会露出空白

    const v = gate.binary()
    sync()
    expect(shown).toEqual([true]) // 恢复字节已交给终端、还在解析
    gate.restorePainted(v.epoch)
    sync()
    expect(shown).toEqual([true, false]) // 画出的那一刻才撤
  })
})
