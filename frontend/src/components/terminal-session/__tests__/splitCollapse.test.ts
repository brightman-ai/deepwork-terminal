import { describe, it, expect } from 'bun:test'
import {
  acceptViewSize,
  clampDragRatio,
  collapsedPane,
  dragRatioBounds,
  estimateCellMetrics,
  MIN_PANE_COLS,
  paneGrids,
  paneSwitcherModel,
  renderRatio,
  shouldCollapse,
  splitLayout,
  usableRatioRange,
  type SplitLayout,
  type ViewSize,
} from '../splitCollapse'
import { claimsPaneFocus, type PaneId, type SplitOrientation } from '../splitControl'
import { isViewer, viewportIntent } from '../../../composables/cli/viewportDeclaration'

const PHONE = estimateCellMetrics(true)
const DESKTOP = estimateCellMetrics(false)
const size = (width: number, height: number): ViewSize => ({ width, height })
const PANES: PaneId[] = ['first', 'second']
const ORIENTATIONS: SplitOrientation[] = ['row', 'column']

describe('REQ-SPL-01 容器放不下两个可用面板就折叠（按实际尺寸，不按设备类型）', () => {
  it('手机竖屏（390 宽）左右分屏：50/50 每格只有 ~25 列 → 折叠', () => {
    expect(paneGrids(size(390, 700), 'row', 0.5, PHONE)[1].cols).toBeLessThan(MIN_PANE_COLS)
    expect(shouldCollapse(size(390, 700), 'row', PHONE)).toBe(true)
  })

  it('同一台手机横屏（844 宽）够宽 → 并排，不是"手机就折叠"', () => {
    expect(shouldCollapse(size(844, 390), 'row', PHONE)).toBe(false)
  })

  it('窄桌面窗口同理：700 宽折叠，800 宽并排', () => {
    expect(shouldCollapse(size(700, 800), 'row', DESKTOP)).toBe(true)
    expect(shouldCollapse(size(800, 800), 'row', DESKTOP)).toBe(false)
  })

  it('门槛：50/50 每格恰好 40 列并排，少 1px 就折叠（浮点噪声不能把 40 判成 39）', () => {
    // 每格 = 40 列 × 8.4 + 14 滚动条 = 350px；两格 + 4px 分隔条 = 704
    expect(paneGrids(size(704, 800), 'row', 0.5, DESKTOP)[0].cols).toBe(40)
    expect(shouldCollapse(size(704, 800), 'row', DESKTOP)).toBe(false)
    expect(shouldCollapse(size(703, 800), 'row', DESKTOP)).toBe(true)
  })

  it('上下排看行数：够高并排（堆叠），矮了折叠', () => {
    expect(shouldCollapse(size(1000, 500), 'column', DESKTOP)).toBe(false)
    expect(shouldCollapse(size(1000, 380), 'column', DESKTOP)).toBe(true)
  })

  it('每根轴只看分屏造成变窄的那一根：上下排不因为窄而折叠，左右排不因为矮而折叠', () => {
    expect(shouldCollapse(size(200, 900), 'column', DESKTOP)).toBe(false)
    expect(shouldCollapse(size(1500, 100), 'row', DESKTOP)).toBe(false)
  })

  it('容器尺寸未知（≤0）→ 不折叠、不夹比例，沿用今天的行为', () => {
    expect(shouldCollapse(size(0, 0), 'row', PHONE)).toBe(false)
    expect(shouldCollapse(size(390, 0), 'row', PHONE)).toBe(false)
    expect(renderRatio(size(0, 0), 'row', PHONE, 0.9)).toBe(0.9)
  })

  it('单调：宽度只增不减时，一旦并排就不会再折叠（不会在中间反复横跳）', () => {
    let sideBySide = false
    for (let w = 300; w <= 2400; w += 7) {
      const collapsed = shouldCollapse(size(w, 800), 'row', DESKTOP)
      if (sideBySide) expect(collapsed).toBe(false)
      if (!collapsed) sideBySide = true
    }
    expect(sideBySide).toBe(true)
  })

  it('字符格估计与 XtermTerminal 的字号（手机 12 / 桌面 14）和 terminalFit.ts 实测的 8.4×16.8 一致', () => {
    expect(DESKTOP.width).toBeCloseTo(8.4, 6)
    expect(DESKTOP.height).toBeCloseTo(16.8, 6)
    expect(PHONE.width).toBeCloseTo(7.2, 6)
    expect(PHONE.height).toBeCloseTo(14.4, 6)
  })
})

describe('REQ-SPL-01（D-3 已决）只在"没有任何比例能让两格都够用"时才折叠；比例太极端 → 渲染期夹进可用范围', () => {
  const RATIOS = Array.from({ length: 81 }, (_, i) => 0.1 + i * 0.01)

  it('折叠 ⇔ 扫遍所有比例都有一格不够用（与 paneGrids 的逐格算术对账，不是自己对自己）', () => {
    const cases: Array<[ViewSize, SplitOrientation, typeof PHONE]> = [
      [size(390, 700), 'row', PHONE], [size(844, 390), 'row', PHONE], [size(703, 800), 'row', DESKTOP],
      [size(704, 800), 'row', DESKTOP], [size(1500, 800), 'row', DESKTOP], [size(1000, 380), 'column', DESKTOP],
      [size(1000, 500), 'column', DESKTOP], [size(390, 700), 'column', PHONE], [size(390, 300), 'column', PHONE],
    ]
    const usable = (g: ReturnType<typeof paneGrids>, o: SplitOrientation): boolean =>
      o === 'row' ? g.every((p) => p.cols >= 40) : g.every((p) => p.rows >= 10)
    for (const [s, o, cell] of cases) {
      const anyRatioWorks = RATIOS.some((r) => usable(paneGrids(s, o, r, cell), o))
      expect(shouldCollapse(s, o, cell)).toBe(!anyRatioWorks)
    }
  })

  it('容器放得下、存下来的比例太极端：不折叠，渲染比例被夹到范围边缘，夹完两格都 ≥ 40 列', () => {
    const s = size(1500, 800)
    expect(shouldCollapse(s, 'row', DESKTOP)).toBe(false)
    const range = usableRatioRange(s, 'row', DESKTOP)!
    expect(range.min).toBeGreaterThan(0.1)
    expect(renderRatio(s, 'row', DESKTOP, 0.9)).toBe(range.max)
    expect(renderRatio(s, 'row', DESKTOP, 0.1)).toBe(range.min)
    for (const stored of [0.1, 0.9]) {
      const [a, b] = paneGrids(s, 'row', renderRatio(s, 'row', DESKTOP, stored), DESKTOP)
      expect(Math.min(a.cols, b.cols)).toBeGreaterThanOrEqual(MIN_PANE_COLS)
    }
  })

  it('范围内的比例原样用（不改用户的摆法）；上下排同理按行数夹', () => {
    expect(renderRatio(size(1500, 800), 'row', DESKTOP, 0.37)).toBe(0.37)
    expect(renderRatio(size(1500, 800), 'row', DESKTOP, 0.5)).toBe(0.5)
    const s = size(1000, 700)
    const r = renderRatio(s, 'column', DESKTOP, 0.9)
    expect(r).toBeLessThan(0.9)
    const [a, b] = paneGrids(s, 'column', r, DESKTOP)
    expect(Math.min(a.rows, b.rows)).toBeGreaterThanOrEqual(10)
  })

  it('刚好放得下 50/50 的容器：可用范围收缩成一个点，夹出来就是 0.5，仍是并排', () => {
    const s = size(704, 800)
    expect(shouldCollapse(s, 'row', DESKTOP)).toBe(false)
    expect(renderRatio(s, 'row', DESKTOP, 0.9)).toBeCloseTo(0.5, 6)
    expect(renderRatio(s, 'row', DESKTOP, 0.1)).toBeCloseTo(0.5, 6)
  })

  it('折叠时没有可夹的范围，比例原样返回（用不上）', () => {
    expect(renderRatio(size(390, 700), 'row', PHONE, 0.9)).toBe(0.9)
  })

  it('夹取是纯函数，只产出渲染用的数；存下来的比例从不被改写，容器变宽后又是原值', () => {
    const stored = 0.9
    expect(renderRatio(size(1500, 800), 'row', DESKTOP, stored)).toBeLessThan(stored)
    expect(renderRatio(size(4000, 800), 'row', DESKTOP, stored)).toBe(0.9)
  })
})

describe('容器尺寸的采纳：量不到 ≠ 变成 0', () => {
  it('量到 0（容器被藏起）→ 沿用上一次量准的值', () => {
    const prev = size(1200, 800)
    expect(acceptViewSize(prev, size(0, 0))).toBe(prev)
    expect(acceptViewSize(prev, size(1200, 0))).toBe(prev)
    expect(acceptViewSize(prev, size(0, 800))).toBe(prev)
  })

  it('量到正常值 → 采纳', () => {
    expect(acceptViewSize(size(1200, 800), size(390, 700))).toEqual(size(390, 700))
  })
})

describe('REQ-SPL-02 看不见的面板不是观看者（折叠、放大 zoom 同一条规则：可见 ⇔ 观看者）', () => {
  const split = { zoomed: undefined as PaneId | undefined }
  const other = (p: PaneId): PaneId => (p === 'first' ? 'second' : 'first')
  /** surface 的 active = tab 激活 && 这一格可见；页面在前台。 */
  const viewerOf = (lay: SplitLayout, p: PaneId): boolean =>
    isViewer({ pageVisible: true, surfaceActive: true && lay.visible[p] })

  it('折叠：恰好一格显示、一格是观看者，就是同一格；另一格既不显示也不是观看者', () => {
    for (const focus of PANES) {
      const lay = splitLayout(split, true, focus)
      expect(lay.solo).toBe(focus)
      expect(lay.visible[focus]).toBe(true)
      expect(viewerOf(lay, focus)).toBe(true)
      expect(lay.visible[other(focus)]).toBe(false)
      expect(viewerOf(lay, other(focus))).toBe(false)
    }
  })

  it('折叠藏起的那一格对 surface 的视口声明纪律：从不 declare，已连上的只会撤回，没连过的保持沉默', () => {
    const lay = splitLayout(split, true, 'first')
    const hiddenIsViewer = viewerOf(lay, 'second')
    expect(hiddenIsViewer).toBe(false)
    for (const geometryChanged of [true, false]) {
      expect(viewportIntent(true, hiddenIsViewer, geometryChanged)).toBe('withdraw')
      expect(viewportIntent(false, hiddenIsViewer, geometryChanged)).toBe('silent')
    }
    // 显示的那一格：成为观看者的边沿必须声明（按自己的全宽）。
    expect(viewportIntent(false, viewerOf(lay, 'first'), false)).toBe('declare')
  })

  it('并排：两格都是观看者（宽屏行为不变）', () => {
    const lay = splitLayout(split, false, 'second')
    expect(lay.visible).toEqual({ first: true, second: true })
    expect(viewerOf(lay, 'first') && viewerOf(lay, 'second')).toBe(true)
    expect(lay.divider).toBe(true)
    expect(lay.solo).toBeUndefined()
  })

  it('放大（zoom）藏起的那一格也不是观看者：撤回；取消放大后它重新成为观看者，必须重新声明（哪怕网格没变）', () => {
    for (const zoomed of PANES) {
      const hidden = other(zoomed)
      const zoomedLay = splitLayout({ zoomed }, false, 'first')
      expect(zoomedLay.solo).toBe(zoomed)
      expect(zoomedLay.visible[hidden]).toBe(false)
      expect(viewerOf(zoomedLay, hidden)).toBe(false)
      expect(viewerOf(zoomedLay, zoomed)).toBe(true)
      expect(zoomedLay.divider).toBe(false)
      // 放大那一刻：原本在看的它撤回；之后重复评估保持沉默
      expect(viewportIntent(true, viewerOf(zoomedLay, hidden), false)).toBe('withdraw')
      expect(viewportIntent(false, viewerOf(zoomedLay, hidden), true)).toBe('silent')

      // 取消放大：两格又都是观看者；刚回来的那格（was=false）必须 declare，没挪窝的那格（was=true）不吵
      const unzoomed = splitLayout({ zoomed: undefined }, false, 'first')
      expect(viewerOf(unzoomed, hidden)).toBe(true)
      expect(viewportIntent(false, viewerOf(unzoomed, hidden), false)).toBe('declare')
      expect(viewportIntent(true, viewerOf(unzoomed, zoomed), false)).toBe('silent')
    }
  })

  it('折叠 + 放大同时存在：仍然只有显示的那一格是观看者', () => {
    const lay = splitLayout({ zoomed: 'second' }, true, undefined)
    expect(lay.solo).toBe('second')
    expect(lay.visible).toEqual({ first: false, second: true })
  })

  it('任何组合下：只要有独占格（折叠或放大），恰好一格可见 = 一个观看者；分隔条隐藏', () => {
    for (const zoomed of [undefined, ...PANES]) {
      for (const focus of [undefined, ...PANES]) {
        const lay = splitLayout({ zoomed }, true, focus)
        expect(PANES.filter((p) => lay.visible[p])).toHaveLength(1)
        expect(lay.divider).toBe(false)
      }
      if (zoomed) {
        const lay = splitLayout({ zoomed }, false, undefined)
        expect(PANES.filter((p) => lay.visible[p])).toEqual([zoomed])
        expect(lay.divider).toBe(false)
      }
    }
  })
})

describe('REQ-SPL-03 切换器：显示当前是哪一个、共两个，只换本机显示不改同步模型', () => {
  it('折叠时显示哪一格：本机自己选的 > 模型里的放大格 > 第一格', () => {
    expect(collapsedPane(undefined, undefined)).toBe('first')
    expect(collapsedPane('second', undefined)).toBe('second')
    expect(collapsedPane('second', 'first')).toBe('first')
    expect(collapsedPane(undefined, 'second')).toBe('second')
  })

  it('切换 = 换本机的 localFocus；输入的分屏模型一个字不动（桌面的放大状态不受影响）', () => {
    const split = Object.freeze({ orientation: 'row' as const, ratio: 0.3, zoomed: 'first' as PaneId })
    const before = JSON.stringify(split)
    let focus: PaneId | undefined
    const seen: (PaneId | undefined)[] = []
    for (let i = 0; i < 4; i++) {
      const lay = splitLayout(split, true, focus)
      seen.push(lay.solo)
      focus = paneSwitcherModel(split.orientation, lay.solo!).target
    }
    // 模型里放大的是 first，本机先看 first，点一下去 second，再点回 first…
    expect(seen).toEqual(['first', 'second', 'first', 'second'])
    expect(JSON.stringify(split)).toBe(before)
  })

  it('切换器的文案：第几个/共几个，目标永远是另一格，用人话的"左边/右边/上边/下边"', () => {
    const row1 = paneSwitcherModel('row', 'first')
    expect([row1.index, row1.total, row1.target]).toEqual([1, 2, 'second'])
    expect(row1.label).toBe('切到右边那格')
    expect(row1.hint).toContain('左边')

    const row2 = paneSwitcherModel('row', 'second')
    expect([row2.index, row2.target, row2.label]).toEqual([2, 'first', '切到左边那格'])

    expect(paneSwitcherModel('column', 'first').label).toBe('切到下边那格')
    expect(paneSwitcherModel('column', 'second').label).toBe('切到上边那格')

    for (const o of ORIENTATIONS) {
      for (const p of PANES) {
        const m = paneSwitcherModel(o, p)
        expect(m.target).not.toBe(p)
        expect(m.total).toBe(2)
      }
    }
  })
})

describe('REQ-SPL-05 触屏点按切焦点：分屏控件自己的按钮不算"点进了这一格"', () => {
  const inside = (selectors: string[]) => ({
    closest: (sel: string) => (selectors.some((s) => sel.split(',').map((x) => x.trim()).includes(s)) ? {} : null),
  })

  it('点在终端 / 普通区域 → 把当前格让给这一格', () => {
    expect(claimsPaneFocus(inside([]))).toBe(true)
  })

  it('点在分屏控件条（.split-ctl）里 → 不抢：否则"切换焦点"按钮永远切不走', () => {
    expect(claimsPaneFocus(inside(['.split-ctl']))).toBe(false)
  })

  it('点在折叠时的面板切换器（.split-switch）里 → 不抢', () => {
    expect(claimsPaneFocus(inside(['.split-switch']))).toBe(false)
  })

  it('事件目标缺失 / 没有 closest（文本节点）→ 照常让出（与改造前 focusin 的行为一致）', () => {
    expect(claimsPaneFocus(null)).toBe(true)
    expect(claimsPaneFocus(undefined)).toBe(true)
    expect(claimsPaneFocus({})).toBe(true)
  })
})

describe('REQ-SPL-06 回到宽屏恢复并排，比例、方向、焦点不丢', () => {
  it('窄 → 宽 → 窄 → 宽：折叠只取决于当前尺寸，不留状态；恢复后的布局与从未折叠过的完全一样', () => {
    const split = { orientation: 'row' as const, ratio: 0.37 }
    const sizes = [size(1400, 800), size(390, 700), size(1400, 800), size(500, 800), size(1400, 800)]
    const layouts: SplitLayout[] = sizes.map((s) =>
      splitLayout({}, shouldCollapse(s, split.orientation, DESKTOP), 'second'),
    )
    expect(layouts.map((l) => l.collapsed)).toEqual([false, true, false, true, false])
    expect(layouts[2]).toEqual(layouts[0])
    expect(layouts[4]).toEqual(layouts[0])
    // 恢复并排时分隔条回来，两格都显示、都是观看者
    expect(layouts[4]!.divider).toBe(true)
    expect(layouts[4]!.visible).toEqual({ first: true, second: true })
  })

  it('折叠期间本机选的那一格（焦点）不丢：展开后仍记着，再折叠仍是它', () => {
    let focus: PaneId | undefined = 'second'
    expect(splitLayout({}, true, focus).solo).toBe('second')
    expect(splitLayout({}, false, focus).solo).toBeUndefined() // 并排，两格都在
    expect(splitLayout({}, true, focus).solo).toBe('second')
  })
})

describe('拖拽分隔条：夹在两格都够用的范围内（= 渲染用的范围），不会拖进折叠', () => {
  it('范围内的任何比例两格都 ≥ 下限；范围对称；与渲染夹取是同一个范围', () => {
    for (const [s, cell] of [[size(1500, 800), DESKTOP], [size(844, 390), PHONE], [size(900, 800), DESKTOP]] as const) {
      const b = dragRatioBounds(s, 'row', cell)
      expect(b).toEqual(usableRatioRange(s, 'row', cell)!)
      expect(b.min).toBeLessThanOrEqual(0.5)
      expect(b.max).toBeGreaterThanOrEqual(0.5)
      expect(b.min + b.max).toBeCloseTo(1, 9)
      for (let r = b.min; r <= b.max; r += 0.005) {
        const [a, c] = paneGrids(s, 'row', r, cell)
        expect(Math.min(a.cols, c.cols)).toBeGreaterThanOrEqual(MIN_PANE_COLS)
      }
      const [a, c] = paneGrids(s, 'row', b.max, cell)
      expect(Math.min(a.cols, c.cols)).toBeGreaterThanOrEqual(MIN_PANE_COLS)
      // 拖到哪、松手后渲染就在哪：范围内的值经渲染夹取不变
      expect(renderRatio(s, 'row', cell, b.min)).toBe(b.min)
      expect(renderRatio(s, 'row', cell, b.max)).toBe(b.max)
    }
  })

  it('夹取：拖过头被夹回范围边缘', () => {
    const b = dragRatioBounds(size(1500, 800), 'row', DESKTOP)
    expect(clampDragRatio(0.01, b)).toBe(b.min)
    expect(clampDragRatio(0.99, b)).toBe(b.max)
    expect(clampDragRatio(0.5, b)).toBe(0.5)
    expect(b.min).toBeGreaterThan(0.1) // 1500 宽时 0.1 的小格只有 ~16 列，比旧的 0.1 下限更紧
  })

  it('上下排同理按行数夹', () => {
    const s = size(1000, 700)
    const b = dragRatioBounds(s, 'column', DESKTOP)
    for (let r = b.min; r <= b.max; r += 0.005) {
      const [a, c] = paneGrids(s, 'column', r, DESKTOP)
      expect(Math.min(a.rows, c.rows)).toBeGreaterThanOrEqual(10)
    }
  })

  it('容器本来就放不下两格（已折叠、拖不了）→ 退回原来的 0.1–0.9，不产生反向范围', () => {
    expect(shouldCollapse(size(390, 700), 'row', PHONE)).toBe(true)
    expect(dragRatioBounds(size(390, 700), 'row', PHONE)).toEqual({ min: 0.1, max: 0.9 })
  })
})
