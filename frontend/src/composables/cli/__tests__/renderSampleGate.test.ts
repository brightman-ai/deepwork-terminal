import { describe, it, expect } from 'bun:test'
import { isRenderSampleTrustworthy } from '../renderSampleGate'

const good = { epochAtWrite: 3, epochNow: 3, hiddenNow: false, laidOutAtWrite: true }

describe('isRenderSampleTrustworthy', () => {
  it('全程可见且有布局 → 可信', () => {
    expect(isRenderSampleTrustworthy(good)).toBe(true)
  })

  // 这条就是 Human 截图里那个 177556ms 的成因。
  it('中途发生过可见性切换 → 不可信（rAF 被挂起，闲置时间被算进耗时）', () => {
    expect(isRenderSampleTrustworthy({ ...good, epochNow: 4 })).toBe(false)
  })

  it('此刻页面不可见 → 不可信', () => {
    expect(isRenderSampleTrustworthy({ ...good, hiddenNow: true })).toBe(false)
  })

  it('写入时终端没有布局盒子（躺在非当前标签里）→ 不可信', () => {
    expect(isRenderSampleTrustworthy({ ...good, laidOutAtWrite: false })).toBe(false)
  })

  // 判据必须落在成因上，不在数值上：一次真实的 2 秒卡顿正是这个指标存在的意义，
  // 按"太大就丢"筛样本，等于把唯一值得看的那个删掉。
  it('判据与耗时数值无关 —— 大值只要成因干净就必须保留', () => {
    expect(isRenderSampleTrustworthy(good)).toBe(true) // 无论这次耗时是 4ms 还是 2000ms
  })
})

// summarize 的超阈计数：它是"卡不卡"的频次一半，必须只数真正越线的样本。
import { summarize, SLOW_FRAME_MS } from '../terminalRenderMetrics'

describe('summarize.renderSlow', () => {
  it('只数严格超过阈值的帧', () => {
    const r = [10, 99, SLOW_FRAME_MS, 101, 500]
    expect(summarize(5, 0, 0, [], r).renderSlow).toBe(2) // 101 与 500
  })
  it('没有样本时为 0，不为 NaN', () => {
    expect(summarize(0, 0, 0, [], []).renderSlow).toBe(0)
  })
})
