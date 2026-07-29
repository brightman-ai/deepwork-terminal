import { describe, it, expect } from 'bun:test'
import { canMeasureTerminal } from '../terminalFit'

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
