import { describe, it, expect, beforeEach } from 'bun:test'
import {
  canMeasureTerminal,
  isPlausibleGrid,
  rememberGridSize,
  lastGoodGridSize,
  resetGridSizeMemo,
} from '../terminalFit'

/** 一个只有几何的假元素 —— 这就是 canMeasureTerminal 需要知道的全部。 */
function el(offsetWidth: number, offsetHeight: number) {
  return { offsetWidth, offsetHeight }
}

describe('canMeasureTerminal', () => {
  it('可见的终端（有盒子）→ 可测量', () => {
    expect(canMeasureTerminal(el(1280, 720))).toBe(true)
  })

  it('display:none 的终端（宽高都归零）→ 不可测量', () => {
    // 这就是标签切走时的样子。修复前这一刻会触发一次 fit，把 10×6 发给服务端。
    expect(canMeasureTerminal(el(0, 0))).toBe(false)
  })

  it('只有一边塌掉也算不可测量（布局还没稳定）', () => {
    expect(canMeasureTerminal(el(1280, 0))).toBe(false)
    expect(canMeasureTerminal(el(0, 720))).toBe(false)
  })

  it('元素还不存在 → 不可测量（不是"当作可见"）', () => {
    expect(canMeasureTerminal(null)).toBe(false)
    expect(canMeasureTerminal(undefined)).toBe(false)
  })

  it('缺字段的对象按 0 处理，不当成可测量', () => {
    expect(canMeasureTerminal({} as { offsetWidth?: number })).toBe(false)
  })
})

// ── 那条不变量的后半句：「它上一次量准的尺寸继续有效」 ──────────────────────────
// 闸门只做了前半句（跳过错误测量），"上一次量准的尺寸"从来没被存过。后果：一个还没可见过的
// 标签停在 xterm 默认 80×24、PTY 停在 spawn 的 220×50，而屏幕上是 52×47 —— 里面的程序按错的
// 宽度排版，切过去才 SIGWINCH，已经写进 scrollback 的换行救不回来。

describe('isPlausibleGrid', () => {
  it('隐藏时算出的 10×6 不配当基准', () => {
    // getComputedStyle 对无盒子元素返回计算值 "100%" → parseInt → 100px → 10×6。
    // 这道闸的全部价值：不让这个垃圾值被存成基准，否则 bug 会传染给每个新终端。
    expect(isPlausibleGrid(10, 6)).toBe(false)
    expect(isPlausibleGrid(2, 1)).toBe(false)
  })

  it('真实终端尺寸通过', () => {
    expect(isPlausibleGrid(52, 47)).toBe(true) // 手机（夹具实测）
    expect(isPlausibleGrid(231, 64)).toBe(true) // 桌面（夹具实测）
  })

  it('NaN / 负数挡掉', () => {
    expect(isPlausibleGrid(NaN, 40)).toBe(false)
    expect(isPlausibleGrid(-5, 40)).toBe(false)
  })
})

describe('grid size memo', () => {
  beforeEach(() => resetGridSizeMemo())

  it('没量准过 → null（此时没有更好的答案可编，调用方只能沿用 xterm 默认）', () => {
    expect(lastGoodGridSize()).toBeNull()
  })

  it('记住最近一次量准的尺寸', () => {
    rememberGridSize(52, 47)
    expect(lastGoodGridSize()).toEqual({ cols: 52, rows: 47 })
    rememberGridSize(231, 64)
    expect(lastGoodGridSize()).toEqual({ cols: 231, rows: 64 })
  })

  it('不合理的尺寸不覆盖已有基准 —— 这是 bug 的传播路径，必须堵死', () => {
    rememberGridSize(52, 47)
    rememberGridSize(10, 6)
    expect(lastGoodGridSize()).toEqual({ cols: 52, rows: 47 })
  })
})
