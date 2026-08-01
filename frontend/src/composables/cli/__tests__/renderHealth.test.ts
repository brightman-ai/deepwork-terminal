import { describe, it, expect, beforeEach } from 'bun:test'
import {
  rendererLine, metricsLine, noteRenderer, noteContextLost, noteRenderMetrics,
  useRenderHealth, resetRenderHealthForTest,
} from '../renderHealth'

beforeEach(resetRenderHealthForTest)

describe('rendererLine — 三档按"用户能不能做点什么"分', () => {
  it('WebGL 正常 → 一句话，无动作', () => {
    const l = rendererLine('webgl', false, '')
    expect(l.tone).toBe('ok')
    expect(l.text).toContain('WebGL')
    expect(l.action).toBeUndefined()
  })

  it('上下文丢失 → 唯一给动作的一档（刷新真能修）', () => {
    const l = rendererLine('webgl', true, '')
    expect(l.tone).toBe('warn')
    expect(l.action?.kind).toBe('reload')
    expect(l.detail).toContain('刷新可恢复')
  })

  it('从一开始就没 WebGL2 → 不给按钮，并说清刷新没用', () => {
    const l = rendererLine('dom', false, 'WebGL2 not supported')
    expect(l.tone).toBe('muted')
    expect(l.action).toBeUndefined()          // 点了没反应的按钮比没有更糟
    expect(l.detail).toContain('WebGL2')
  })

  it('还没有终端挂载过 → 什么都不说，绝不猜一个渲染器', () => {
    const l = rendererLine('unknown', false, '')
    expect(l.text).toBe('尚未初始化')
    expect(l.action).toBeUndefined()
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
  it('最后挂载的终端写入渲染器', () => {
    noteRenderer('webgl')
    expect(useRenderHealth().renderer.value).toBe('webgl')
  })

  it('上下文丢失是粘性的，且把渲染器改成 dom（说实话，不留旧值）', () => {
    noteRenderer('webgl')
    noteContextLost()
    const h = useRenderHealth()
    expect(h.contextLost.value).toBe(true)
    expect(h.renderer.value).toBe('dom')
    // 之后又有终端挂载并拿到 webgl，也不清除"本页面降级过"这个事实
    noteRenderer('webgl')
    expect(h.contextLost.value).toBe(true)
  })

  it('指标写入即可读', () => {
    noteRenderMetrics({ frames: 5, bytes: 1, forcedRepaints: 0, parseP50: 0, parseP95: 0, parseMax: 0, renderP50: 8, renderP95: 9, renderMax: 9, renderSlow: 0 })
    expect(useRenderHealth().metrics.value?.renderP50).toBe(8)
  })
})
