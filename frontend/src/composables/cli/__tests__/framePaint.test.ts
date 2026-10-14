import { describe, expect, it } from 'bun:test'
import { afterNextPaint, type FrameScheduler } from '../framePaint'

/** 手动推进的 rAF + 定时器。 */
function fakeScheduler() {
  let now = 0
  let nextId = 1
  const rafs = new Map<number, () => void>()
  const timers = new Map<number, { at: number; cb: () => void }>()
  const sched: FrameScheduler = {
    raf: (cb) => { const id = nextId++; rafs.set(id, cb); return id },
    cancelRaf: (id) => { rafs.delete(id) },
    setTimeout: (cb, ms) => { const id = nextId++; timers.set(id, { at: now + ms, cb }); return id },
    clearTimeout: (id) => { timers.delete(id as number) },
  }
  return {
    sched,
    frame() { const all = [...rafs.values()]; rafs.clear(); all.forEach(cb => cb()) },
    advance(ms: number) {
      now += ms
      for (const [id, t] of [...timers]) {
        if (t.at <= now) { timers.delete(id); t.cb() }
      }
    },
    pending: () => rafs.size + timers.size,
  }
}

describe('afterNextPaint', () => {
  it('rAF 先到：调用一次，viaFallback=false，兜底定时器被撤', () => {
    const f = fakeScheduler()
    const calls: boolean[] = []
    afterNextPaint(f.sched, (via) => calls.push(via), 200)
    f.frame()
    f.advance(1000)
    expect(calls).toEqual([false])
    expect(f.pending()).toBe(0)
  })

  it('rAF 被挂起（后台页）：兜底到点调用，viaFallback=true，且只调一次', () => {
    const f = fakeScheduler()
    const calls: boolean[] = []
    afterNextPaint(f.sched, (via) => calls.push(via), 200)
    f.advance(199)
    expect(calls).toEqual([])
    f.advance(1)
    expect(calls).toEqual([true])
    f.frame() // 迟到的 rAF 不能再触发一次
    expect(calls).toEqual([true])
  })

  it('取消后两路都不再触发', () => {
    const f = fakeScheduler()
    const calls: boolean[] = []
    const cancel = afterNextPaint(f.sched, (via) => calls.push(via), 200)
    cancel()
    f.frame()
    f.advance(1000)
    expect(calls).toEqual([])
    expect(f.pending()).toBe(0)
  })
})
