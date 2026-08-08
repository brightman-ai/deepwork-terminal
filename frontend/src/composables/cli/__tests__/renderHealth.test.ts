import { describe, it, expect, beforeEach } from 'bun:test'
import {
  rendererLine, metricsLine, noteRenderer, noteContextLost, noteRenderMetrics,
  useRenderHealth, resetRenderHealthForTest,
} from '../renderHealth'

beforeEach(resetRenderHealthForTest)

describe('rendererLine — 动作按"点下去真的会发生什么"给', () => {
  it('WebGL 在用 → ok，并给一个换到 DOM 的出口', () => {
    const l = rendererLine('webgl', false, 'chosen', '')
    expect(l.tone).toBe('ok')
    expect(l.text).toContain('WebGL')
    expect(l.action).toEqual({ label: '切换为 DOM', kind: 'renderer', to: 'dom' })
  })

  // DOM 现在是默认，所以"怎么拿到 GPU"必须是面板上按得到的一件事，
  // 而不是只有读过源码的人才知道的一个 ?renderer= 查询参数。
  it('DOM 在用 → muted（不是故障），并给一个换到 WebGL 的出口', () => {
    const l = rendererLine('dom', false, 'default', '')
    expect(l.tone).toBe('muted')
    expect(l.text).toContain('DOM')
    expect(l.action).toEqual({ label: '切换为 WebGL', kind: 'renderer', to: 'webgl' })
  })

  it('上下文丢失 → 动作是刷新，不是换渲染器（刷新才是真能修的那个）', () => {
    const l = rendererLine('webgl', true, 'chosen', '')
    expect(l.tone).toBe('warn')
    expect(l.action?.kind).toBe('reload')
    expect(l.detail).toContain('刷新可恢复')
  })

  it('想要 WebGL 却拿不到 → 不给按钮，并说清刷新没用', () => {
    const l = rendererLine('dom', false, 'unavailable', 'WebGL2 not supported')
    expect(l.tone).toBe('muted')
    // 点了只会原地再失败一次 —— 点了没反应的按钮比没有更糟。
    expect(l.action).toBeUndefined()
    expect(l.detail).toContain('WebGL2')
  })

  it('还没有终端挂载过 → 什么都不说，绝不猜一个渲染器，也不给按钮', () => {
    const l = rendererLine('unknown', false, 'default', '')
    expect(l.text).toBe('尚未初始化')
    expect(l.action).toBeUndefined()
  })
})

// 原因不是装饰：DOM 成为默认之后，「你自己选的」「URL 钉的」「默认」三种 DOM 长得一模一样，
// 而使用者的下一个问题恰恰是"那我改的那次到底生效没有"。
describe('rendererLine 的 detail — 回答"为什么是它"', () => {
  it('URL 钉：说清它只在这个标签页里成立', () => {
    expect(rendererLine('webgl', false, 'pinned', '').detail).toContain('?renderer=webgl')
    expect(rendererLine('dom', false, 'pinned', '').detail).toContain('仅本标签页')
  })

  it('自己选的：说清记得住 —— 否则看起来和一次性状态没区别', () => {
    expect(rendererLine('dom', false, 'chosen', '').detail).toContain('记住')
  })

  it('默认：一个字，不抢注意力', () => {
    expect(rendererLine('dom', false, 'default', '').detail).toBe('默认')
  })
})

describe('metricsLine', () => {
  const base = {
    frames: 10, bytes: 1000, forcedRepaints: 0,
    parseP50: 1, parseP95: 2, parseMax: 3,
    renderP50: 11.7, renderP95: 59.1, renderMax: 60.3, renderSlow: 0,
  }

  it('报单帧耗时的分位数，不报帧率', () => {
    const s = metricsLine(base)
    expect(s).toContain('12/59ms')
    expect(s).toMatch(/P50\/P95/)
    // 终端只在有字节时才画，"帧率"对空闲终端毫无意义且会显得像坏了。
    expect(s).not.toMatch(/fps|帧率/)
  })

  // 卡顿活在尾巴上：200 帧里一次 800ms 落在 P99.5，P95 完全看不见它。
  it('报最慢一帧 —— 分位数描述"大多数时候"，看不见单次僵直', () => {
    const s = metricsLine({ ...base, renderMax: 812.4 })
    expect(s).toContain('最慢 812ms')
  })

  it('超阈帧数把"偶发"和"持续"分开（一次 400ms 是 GC，十次才叫卡）', () => {
    expect(metricsLine({ ...base, renderMax: 412, renderSlow: 1 })).toContain('1 帧 >100ms')
    expect(metricsLine({ ...base, renderMax: 412, renderSlow: 12 })).toContain('12 帧 >100ms')
  })

  it('没有超阈帧就不提它 —— 多一句"0 帧"是噪音', () => {
    expect(metricsLine(base)).not.toContain('>100ms')
  })

  it('有整屏重绘才报它（模块自己点名的"最值得盯的数字"）', () => {
    expect(metricsLine(base)).not.toContain('整屏重绘')
    expect(metricsLine({ ...base, forcedRepaints: 4 })).toContain('整屏重绘 4')
  })

  it('没有样本就不说话，绝不显示一排 0', () => {
    expect(metricsLine(null)).toBe('')
    expect(metricsLine({ ...base, frames: 0 })).toBe('')
  })
})

describe('页面级状态', () => {
  it('最后挂载的终端写入渲染器和它的原因', () => {
    noteRenderer('webgl', 'chosen')
    const h = useRenderHealth()
    expect(h.renderer.value).toBe('webgl')
    expect(h.cause.value).toBe('chosen')
  })

  it('WebGL 失败时原话被单独留着 —— 它是 detail 里唯一能指向真凶的东西', () => {
    noteRenderer('dom', 'unavailable', 'WebGL2 not supported')
    const h = useRenderHealth()
    expect(h.cause.value).toBe('unavailable')
    expect(rendererLine(h.renderer.value, false, h.cause.value, h.rendererError.value).detail)
      .toContain('WebGL2 not supported')
  })

  it('上下文丢失是粘性的，且把渲染器改成 dom（说实话，不留旧值）', () => {
    noteRenderer('webgl', 'chosen')
    noteContextLost()
    const h = useRenderHealth()
    expect(h.contextLost.value).toBe(true)
    expect(h.renderer.value).toBe('dom')
    // 之后又有终端挂载并拿到 webgl，也不清除"本页面降级过"这个事实
    noteRenderer('webgl', 'chosen')
    expect(h.contextLost.value).toBe(true)
  })

  it('指标写入即可读', () => {
    noteRenderMetrics({ frames: 5, bytes: 1, forcedRepaints: 0, parseP50: 0, parseP95: 0, parseMax: 0, renderP50: 8, renderP95: 9, renderMax: 9, renderSlow: 0 })
    expect(useRenderHealth().metrics.value?.renderP50).toBe(8)
  })
})
