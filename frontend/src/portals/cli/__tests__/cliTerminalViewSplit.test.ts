/**
 * CliTerminalView 的分屏折叠 —— 视图接线测试（split-narrow，REQ-SPL-01..06）。
 *
 * 判定本身（尺寸 → 折叠/并排、谁是观看者）在 splitCollapse.test.ts 里用纯函数测。这里测的是另一半：
 * 宿主真的把这些判定接到了渲染上——v-show、给 surface 的 `active`、切换器挂在哪、点击/按下后发生什么、
 * 容器变宽后是否原样恢复。真的 CliTerminalView 被挂起来（见 support/ 下的最小渲染器），
 * CliTerminalSurface 换成只记录绑定的替身。
 */
import { afterEach, beforeAll, describe, expect, it } from 'bun:test'
import { nextTick } from 'vue'
import { installVueSfcLoader } from './support/sfcLoader'
import { focusCalls } from './support/stubs'
import { byTestId, classList, fire, findAll, isShown, mountView, type TNode } from './support/testRenderer'
import { dragRatioBounds, estimateCellMetrics, usableRatioRange } from '../../../components/terminal-session/splitCollapse'
import type { Component } from 'vue'
import type { TabSplit } from '../../../types/workbench'

installVueSfcLoader()
let CliTerminalView: Component
beforeAll(async () => {
  CliTerminalView = (await import('../adapters/CliTerminalView.vue')).default
})

// ── 环境：纯 bun 里没有 window / ResizeObserver / requestAnimationFrame，按需补，用完还原 ──────────────
const g = globalThis as unknown as Record<string, unknown>
const SAVED = { window: g.window, ResizeObserver: g.ResizeObserver, requestAnimationFrame: g.requestAnimationFrame }
let resizeCallbacks: Array<() => void> = []
let windowListeners: Record<string, Array<(e: unknown) => void>> = {}
let cleanups: Array<() => void> = []

function setEnv(width: number, height: number, mobile: boolean): void {
  windowListeners = {}
  resizeCallbacks = []
  focusCalls.length = 0
  g.window = {
    innerWidth: width,
    innerHeight: height,
    // 真的 useDeviceDetection 靠"有触屏事件 && 窗口 < 1024"判定手机（useCliRuntimeMode.ts）。
    ...(mobile ? { ontouchstart: null } : {}),
    addEventListener: (type: string, fn: (e: unknown) => void) => { (windowListeners[type] ??= []).push(fn) },
    removeEventListener: (type: string, fn: (e: unknown) => void) => {
      windowListeners[type] = (windowListeners[type] ?? []).filter((f) => f !== fn)
    },
  }
  g.ResizeObserver = class {
    constructor(cb: () => void) { resizeCallbacks.push(cb) }
    observe(): void {}
    disconnect(): void {}
  }
  g.requestAnimationFrame = (cb: (t: number) => void) => { cb(0); return 0 }
}

afterEach(() => {
  cleanups.forEach((fn) => fn())
  cleanups = []
  for (const [k, v] of Object.entries(SAVED)) {
    if (v === undefined) delete g[k]
    else g[k] = v
  }
})

// ── 挂载 ────────────────────────────────────────────────────────────────────────────────────────
interface Opts {
  width: number
  height: number
  mobile: boolean
  split: TabSplit
  splitAllowed?: boolean
}

function mount(o: Opts) {
  setEnv(o.width, o.height, o.mobile)
  const zoomCalls: Array<[string, string]> = []
  const ratioCalls: Array<[string, number]> = []
  const tab = { id: 't1', name: 'fable', sessionId: 'S1', split: o.split }
  const m = mountView(CliTerminalView, {
    loading: false,
    error: null,
    activeTabId: 't1',
    tabsWithSession: [tab],
    splitAllowed: o.splitAllowed ?? false,
    onToggleZoom: (tabId: string, which: string) => { zoomCalls.push([tabId, which]) },
    onSplitRatio: (tabId: string, ratio: number) => { ratioCalls.push([tabId, ratio]) },
  })
  cleanups.push(m.unmount)
  return { ...m, zoomCalls, ratioCalls }
}

const pane = (host: TNode, sid: string): TNode => {
  const found = findAll(host, (n) => n.props['data-sid'] === sid)
  if (found.length !== 1) throw new Error(`expected one pane ${sid}, found ${found.length}`)
  return found[0]!
}
const isActive = (p: TNode): boolean => p.props['data-active'] === 'true'
const switchers = (root: TNode): TNode[] => byTestId(root, 'split-pane-switcher')
const container = (host: TNode): TNode => byTestId(host, 'cli-portal-split-t1')[0]!
const divider = (host: TNode): TNode => byTestId(host, 'cli-portal-split-divider-t1')[0]!
const flexOf = (p: TNode): string => (p.props.style as { flex: string }).flex

/** 谁在屏幕上、谁是观看者：[显示, active]。 */
const state = (host: TNode, sid: string): [boolean, boolean] => [isShown(pane(host, sid)), isActive(pane(host, sid))]

async function settle(): Promise<void> {
  await nextTick()
  await nextTick()
}

const PHONE_PORTRAIT = { width: 390, height: 700, mobile: true }
const row = (extra: Partial<TabSplit> = {}): TabSplit => ({ orientation: 'row', ratio: 0.5, secondSessionId: 'S2', ...extra })

describe('REQ-SPL-01/02/04 手机竖屏：折叠成单面板，藏起的那格不是观看者，屏幕上只有一套外壳', () => {
  it('只有第一格显示且 active；第二格 display:none 且 active=false；分隔条隐藏；第一格占满', () => {
    const { host } = mount({ ...PHONE_PORTRAIT, split: row() })
    expect(container(host).props['data-collapsed']).toBe(true)
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([false, false])
    expect(isShown(divider(host))).toBe(false)
    expect(flexOf(pane(host, 'S1'))).toBe('1.0000 1 0%')
    expect(flexOf(pane(host, 'S2'))).toBe('0.0000 1 0%')
  })

  it('切换器恰好一个，挂在显示着的那格上；手机不给分屏入口（没有分屏控制条），但折叠了就必须有切换器', () => {
    const { host } = mount({ ...PHONE_PORTRAIT, split: row() })
    expect(switchers(host)).toHaveLength(1)
    expect(switchers(pane(host, 'S1'))).toHaveLength(1)
    expect(switchers(pane(host, 'S2'))).toHaveLength(0)
    expect(byTestId(host, 'cli-portal-split-ctl-t1')).toHaveLength(0)
    expect(String(switchers(host)[0]!.props['aria-label'])).toContain('现在是第 1 格，共 2 格')
  })

  it('第二格还没起好（只有占位）：没有"放不下两格"可言，不折叠，照旧渲染', () => {
    const { host } = mount({ ...PHONE_PORTRAIT, split: { orientation: 'row', ratio: 0.5 } })
    expect(container(host).props['data-collapsed']).toBe(false)
    expect(byTestId(host, 'cli-portal-split-pending')).toHaveLength(1)
    expect(switchers(host)).toHaveLength(0)
  })
})

describe('REQ-SPL-03 切换器：换本机显示的那格，不改同步的分屏模型', () => {
  it('点一下 → 另一格显示且成为观看者、原来的撤下；再点回来；全程不发 toggle-zoom，模型不变', async () => {
    const { host, props, zoomCalls } = mount({ ...PHONE_PORTRAIT, split: row() })
    const before = JSON.stringify(props.tabsWithSession)

    fire(switchers(host)[0]!, 'onClick')
    await settle()
    expect(state(host, 'S1')).toEqual([false, false])
    expect(state(host, 'S2')).toEqual([true, true])
    expect(switchers(pane(host, 'S2'))).toHaveLength(1)
    expect(switchers(pane(host, 'S1'))).toHaveLength(0)
    expect(String(switchers(host)[0]!.props['aria-label'])).toContain('现在是第 2 格，共 2 格')

    fire(switchers(host)[0]!, 'onClick')
    await settle()
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([false, false])

    expect(zoomCalls).toEqual([])
    expect(JSON.stringify(props.tabsWithSession)).toBe(before)
  })

  it('桌面把第二格放大了：手机先看第二格；点切换器去第一格，仍不发 toggle-zoom、不改模型（桌面不受影响）', async () => {
    const { host, props, zoomCalls } = mount({ ...PHONE_PORTRAIT, split: row({ zoomed: 'second' }) })
    const before = JSON.stringify(props.tabsWithSession)
    expect(state(host, 'S2')).toEqual([true, true])
    expect(state(host, 'S1')).toEqual([false, false])

    fire(switchers(host)[0]!, 'onClick')
    await settle()
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([false, false])
    expect(zoomCalls).toEqual([])
    expect(JSON.stringify(props.tabsWithSession)).toBe(before)
  })

  it('手机上切格子不去 focus 终端（那会顶出软键盘）；桌面窄窗口会 focus 到新显示的那格', async () => {
    const phone = mount({ ...PHONE_PORTRAIT, split: row() })
    fire(switchers(phone.host)[0]!, 'onClick')
    await settle()
    expect(focusCalls).toEqual([])
    phone.unmount()
    cleanups = []

    const desktop = mount({ width: 600, height: 800, mobile: false, split: row(), splitAllowed: true })
    fire(switchers(desktop.host)[0]!, 'onClick')
    await settle()
    expect(focusCalls).toEqual(['S2'])
  })

  it('窄桌面窗口（有分屏入口）：切换器与分屏控制条并存；控制条里的"切换焦点"在折叠态也不发 toggle-zoom', async () => {
    const { host, zoomCalls } = mount({
      width: 600, height: 800, mobile: false, split: row({ zoomed: 'first' }), splitAllowed: true,
    })
    expect(container(host).props['data-collapsed']).toBe(true)
    expect(switchers(host)).toHaveLength(1)
    const ctl = byTestId(host, 'cli-portal-split-ctl-t1')
    expect(ctl).toHaveLength(1)
    // SplitControlBar 的"切换焦点"按钮 = 与当前方向相同的那个方向按钮（高亮态）
    const sw = findAll(ctl[0]!, (n) => n.props['data-testid'] === 'split-ctl-row')[0]!
    fire(sw, 'onClick')
    await settle()
    expect(state(host, 'S2')).toEqual([true, true])
    expect(zoomCalls).toEqual([])
  })
})

describe('REQ-SPL-06 回到宽屏恢复并排，比例、方向、焦点不丢', () => {
  it('窄 → 宽 → 窄：容器变宽（ResizeObserver）后两格并排、比例 0.37/0.63、方向、本机焦点原样；再变窄回到同一格', async () => {
    const { host, props, zoomCalls } = mount({ width: 600, height: 800, mobile: false, split: row({ ratio: 0.37 }) })
    const rootEl = byTestId(host, 'cli-portal-terminal-view')[0]!
    const before = JSON.stringify(props.tabsWithSession)
    expect(container(host).props['data-collapsed']).toBe(true)

    // 本机选了第二格
    fire(switchers(host)[0]!, 'onClick')
    await settle()
    expect(state(host, 'S2')).toEqual([true, true])

    // 容器变宽
    rootEl.clientWidth = 1400
    rootEl.clientHeight = 900
    resizeCallbacks.forEach((cb) => cb())
    await settle()
    expect(container(host).props['data-collapsed']).toBe(false)
    expect(classList(container(host))).toContain('row')
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([true, true])
    expect(isShown(divider(host))).toBe(true)
    expect(flexOf(pane(host, 'S1'))).toBe('0.3700 1 0%')
    expect(flexOf(pane(host, 'S2'))).toBe('0.6300 1 0%')
    expect(classList(pane(host, 'S2'))).toContain('is-focused-pane') // 焦点没丢
    expect(classList(pane(host, 'S1'))).not.toContain('is-focused-pane')
    expect(switchers(host)).toHaveLength(0)

    // 再变窄：回到刚才选的那格
    rootEl.clientWidth = 500
    resizeCallbacks.forEach((cb) => cb())
    await settle()
    expect(container(host).props['data-collapsed']).toBe(true)
    expect(state(host, 'S2')).toEqual([true, true])
    expect(state(host, 'S1')).toEqual([false, false])

    expect(zoomCalls).toEqual([])
    expect(JSON.stringify(props.tabsWithSession)).toBe(before)
  })

  it('容器量到 0（整个 portal 被藏起）不当真：沿用上一次量准的尺寸，不翻来翻去', async () => {
    const { host } = mount({ width: 1400, height: 900, mobile: false, split: row() })
    const rootEl = byTestId(host, 'cli-portal-terminal-view')[0]!
    rootEl.clientWidth = 0
    rootEl.clientHeight = 0
    resizeCallbacks.forEach((cb) => cb())
    await settle()
    expect(container(host).props['data-collapsed']).toBe(false)
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([true, true])
  })
})

describe('宽屏行为不变（MODIFIED: 无）', () => {
  it('宽屏并排：两格都显示且 active，有分隔条，没有切换器，按比例分', () => {
    const { host } = mount({ width: 1400, height: 900, mobile: false, split: row(), splitAllowed: true })
    expect(container(host).props['data-collapsed']).toBe(false)
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([true, true])
    expect(isShown(divider(host))).toBe(true)
    expect(switchers(host)).toHaveLength(0)
    expect(flexOf(pane(host, 'S1'))).toBe('0.5000 1 0%')
    expect(byTestId(host, 'cli-portal-split-ctl-t1')).toHaveLength(1)
    expect(byTestId(host, 'cli-portal-split-ctl-t1__split2')).toHaveLength(1)
  })

  it('宽屏放大（zoom）藏起的那格也不是观看者：display:none 且 active=false；取消放大后恢复显示并重新成为观看者', async () => {
    const { host, props } = mount({ width: 1400, height: 900, mobile: false, split: row({ zoomed: 'second' }) })
    expect(state(host, 'S1')).toEqual([false, false])
    expect(state(host, 'S2')).toEqual([true, true])
    expect(isShown(divider(host))).toBe(false)
    expect(container(host).props['data-collapsed']).toBe(false) // 宽屏，没折叠，纯 zoom

    // 取消放大（模型里 zoomed 被清掉，别处同步过来也一样）：active false → true 就是 surface 里
    // "刚成为观看者"的边沿，由它负责重新声明尺寸（splitCollapse.test.ts 的 viewportIntent 对账）
    ;(props.tabsWithSession as Array<{ split: TabSplit }>)[0]!.split.zoomed = undefined
    await settle()
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([true, true])
    expect(isShown(divider(host))).toBe(true)

    // 再放大第一格：第二格撤下
    ;(props.tabsWithSession as Array<{ split: TabSplit }>)[0]!.split.zoomed = 'first'
    await settle()
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([false, false])
  })

  it('容器放得下、存下来的比例太极端（0.9）：不折叠，渲染期夹进可用范围，模型里的 0.9 不动；容器够宽时又是 0.9', async () => {
    const { host, props } = mount({ width: 1400, height: 900, mobile: false, split: row({ ratio: 0.9 }) })
    const before = JSON.stringify(props.tabsWithSession)
    const range = usableRatioRange({ width: 1400, height: 900 }, 'row', estimateCellMetrics(false))!
    expect(container(host).props['data-collapsed']).toBe(false)
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([true, true])
    expect(flexOf(pane(host, 'S1'))).toBe(`${range.max.toFixed(4)} 1 0%`)
    expect(flexOf(pane(host, 'S2'))).toBe(`${(1 - range.max).toFixed(4)} 1 0%`)
    expect(range.max).toBeLessThan(0.9)

    const rootEl = byTestId(host, 'cli-portal-terminal-view')[0]!
    rootEl.clientWidth = 4000
    rootEl.clientHeight = 900
    resizeCallbacks.forEach((cb) => cb())
    await settle()
    expect(flexOf(pane(host, 'S1'))).toBe('0.9000 1 0%')
    expect(JSON.stringify(props.tabsWithSession)).toBe(before)
  })

  it('手机横屏（844 宽）够宽 → 并排，不是"手机就折叠"', () => {
    const { host } = mount({ width: 844, height: 390, mobile: true, split: row() })
    expect(container(host).props['data-collapsed']).toBe(false)
    expect(state(host, 'S1')).toEqual([true, true])
    expect(state(host, 'S2')).toEqual([true, true])
  })
})

describe('REQ-SPL-05 触屏点按切焦点（pointerdown，不依赖 focusin）', () => {
  const target = (insideControl: boolean) => ({ closest: (sel: string) => (insideControl && sel.includes('.split-ctl') ? {} : null) })
  const down = (insideControl: boolean) => ({
    target: target(insideControl),
    preventDefault: () => { throw new Error('pointerdown must not preventDefault (would break scroll / long-press / touchball)') },
    stopPropagation: () => { throw new Error('pointerdown must not stopPropagation') },
  })

  it('并排时按下某一格 → 它成为当前格；按在分屏控件里不算', async () => {
    const { host } = mount({ width: 844, height: 390, mobile: true, split: row() })
    expect(classList(pane(host, 'S1'))).toContain('is-focused-pane')

    fire(pane(host, 'S2'), 'onPointerdownCapturePassive', down(false))
    await settle()
    expect(classList(pane(host, 'S2'))).toContain('is-focused-pane')
    expect(classList(pane(host, 'S1'))).not.toContain('is-focused-pane')

    fire(pane(host, 'S1'), 'onPointerdownCapturePassive', down(true))
    await settle()
    expect(classList(pane(host, 'S2'))).toContain('is-focused-pane') // 控件里的按下不抢
  })

  it('监听挂在捕获阶段 + passive：内层怎么 stopPropagation 都先收到，且不碍滚动', () => {
    const { host } = mount({ width: 844, height: 390, mobile: true, split: row() })
    for (const sid of ['S1', 'S2']) {
      expect(typeof pane(host, sid).props.onPointerdownCapturePassive).toBe('function')
    }
  })
})

describe('拖拽分隔条：夹在两格都够用的范围内，不会拖进折叠', () => {
  it('拖到最左：松手上抛的比例是范围下限，而不是旧的 0.1', () => {
    const { host, ratioCalls } = mount({ width: 1400, height: 900, mobile: false, split: row() })
    fire(divider(host), 'onPointerdown', {
      currentTarget: { parentElement: { getBoundingClientRect: () => ({ left: 0, top: 0, width: 1400, height: 900 }) } },
      preventDefault: () => {},
    })
    windowListeners.pointermove!.forEach((fn) => fn({ clientX: 10, clientY: 0 }))
    windowListeners.pointerup!.forEach((fn) => fn({}))

    const bounds = dragRatioBounds({ width: 1400, height: 900 }, 'row', estimateCellMetrics(false))
    expect(bounds.min).toBeGreaterThan(0.1)
    expect(ratioCalls).toEqual([['t1', bounds.min]])
  })
})

// Stable overview identity selects the actual pane, including phone-collapsed splits.
for (const orientation of ['row', 'column'] as const) {
  it(`overview can reveal the second ${orientation} pane on mobile without changing shared zoom`, async () => {
    const m = mount({ ...PHONE_PORTRAIT, height: orientation === 'column' ? 240 : 700, split: row({ orientation }) })
    ;(m.exposed as { revealPane(id: string, pane: string): void }).revealPane('t1', 'second')
    await settle()
    expect(state(m.host, 'S1')).toEqual([false, false])
    expect(state(m.host, 'S2')).toEqual([true, true])
    expect(m.zoomCalls).toEqual([])
  })
}
