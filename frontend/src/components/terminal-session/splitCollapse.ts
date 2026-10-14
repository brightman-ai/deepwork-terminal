/**
 * splitCollapse —— 分屏"放不下两个可用面板"时折叠成单面板的**判定**，与 Vue / DOM 无关，因此可测。
 * （delta：docs/.tg/work/20261014-083646-split-narrow-term/delta.md，REQ-SPL-01..06）
 *
 * ── 为什么要折叠，而不是在手机上把分屏藏掉 ──────────────────────────────────────────────────
 * 分屏状态（方向/比例/第二格 session）是跨设备同步的，手机拿到的就是桌面写下的那份。每一格是一个
 * 独立 session，各自按**自己容器的宽度**向服务端声明终端尺寸；服务端取"所有正在显示它的窗口的
 * 逐轴最小值"。手机竖屏上并排两格，右格只剩几列：那个会话的 PTY 被压成几列，桌面上看同一个会话
 * 的人也跟着被压扁，而且这一格还抢走了桌面对它的连接（一个会话一条 WS，后连者接管）。
 *
 * 所以判据是**容器实际放不放得下两个可用面板**，不是设备类型：手机横屏够宽就并排，窄桌面窗口放不下
 * 就折叠。"放得下"= 存在某个比例让两格都 ≥ 下限；容器放得下、只是存下来的比例太极端（0.9 之类）时
 * 不折叠，而是渲染期把比例夹进可用范围（`renderRatio`）。折叠与夹取都只是**渲染态**，从不写回同步
 * 的分屏模型——桌面不受影响，回到宽屏原样恢复（REQ-SPL-06）。
 *
 * ── 谁算"观看者" ───────────────────────────────────────────────────────────────────────────
 * 看不见的格子对 surface 来说必须是 `active=false`（与隐藏 tab 同一条纪律，REQ-TS-05）：不连接、
 * 不声明尺寸，已经连上的则撤回（viewportDeclaration.ts 的 withdraw）；重新可见时它成为观看者的
 * 边沿，必须声明。折叠藏起的格子和放大（zoom）藏起的格子是同一条规则：可见 ⇔ 观看者。
 */

import { otherPane, panePlace, type PaneId, type SplitOrientation } from './splitControl'

/** 并排时每格至少要有的列数 / 上下排时每格至少要有的行数。△ 估值：Claude Code 等 TUI 在 40 列
 *  以下基本不可读；10 行是"能看见一屏输出 + 提示符"的下限。翻转证据见 delta 决策四件套。 */
export const MIN_PANE_COLS = 40
export const MIN_PANE_ROWS = 10

/** 两格之间分隔条的占位宽度（CliTerminalView 里 .cli-terminal-view__divider 的 4px）。 */
export const DIVIDER_PX = 4
/** xterm 的滚动条占位：FitAddon 从可用宽度里扣掉它再除以字宽（见 terminalFit.ts 头注的算术）。 */
const SCROLLBAR_PX = 14
/** 浮点噪声：恰好等于下限的尺寸不能因为 39.99999999 被判成 39 列。 */
const EPS = 1e-6

export interface ViewSize {
  width: number
  height: number
}

export interface CellMetrics {
  /** 一个字符格的宽/高（CSS px）。 */
  width: number
  height: number
  /** 每格 surface 在终端区域之外占掉的纵向像素（状态行；手机还有底栏）。 */
  chromeY: number
}

/**
 * 字符格尺寸的估计。字号取 XtermTerminal 构造 Terminal 时用的那一对（手机 12 / 桌面 14），字宽
 * 0.6em、行高 1.2em——和 terminalFit.ts 头注里实测的 8.4×16.8（14px）同一个比例。
 *
 * 这是估计而非测量：折叠时被藏起的那一格没有盒子，量不到；而判定要回答的恰恰是"如果并排，每格
 * 有几列"，所以必须从容器尺寸**推**。门槛本身（40 列）就是估值，±3% 的字宽误差不改变结论。
 *
 * △ chromeY：桌面 = 状态行 ≈32px；手机 = 状态行 + 底栏三行（dw 条 / ½↑½↓ / 按键）≈110px。
 */
export function estimateCellMetrics(mobile: boolean): CellMetrics {
  const fontSize = mobile ? 12 : 14
  return { width: fontSize * 0.6, height: fontSize * 1.2, chromeY: mobile ? 110 : 32 }
}

/**
 * 容器尺寸的采纳：量到 0（容器被 display:none / 整个 portal 被藏起）不是"窗口变成 0"，而是"此刻
 * 量不到"——沿用上一次量准的值，免得一次隐藏就把折叠判定翻来翻去（同 terminalFit.ts 的不变量：
 * 量不到的东西没有尺寸主张）。
 */
export function acceptViewSize(prev: ViewSize, next: ViewSize): ViewSize {
  if (!(next.width > 0) || !(next.height > 0)) return prev
  return next
}

function fitCells(px: number, unit: number, reserve: number): number {
  if (!(unit > 0)) return 0
  return Math.max(0, Math.floor((px - reserve) / unit + EPS))
}

/** 沿分屏轴把容器像素按比例分给两格（先扣分隔条，和 CliTerminalView 的 flex 分配一致）。 */
function splitAxis(total: number, ratio: number): [number, number] {
  const avail = Math.max(0, total - DIVIDER_PX)
  return [avail * ratio, avail * (1 - ratio)]
}

export interface PaneGrid {
  cols: number
  rows: number
}

/** 如果并排渲染，两格各自能放下的字符网格。 */
export function paneGrids(
  size: ViewSize,
  orientation: SplitOrientation,
  ratio: number,
  cell: CellMetrics,
): [PaneGrid, PaneGrid] {
  if (orientation === 'row') {
    const [a, b] = splitAxis(size.width, ratio)
    const rows = fitCells(size.height, cell.height, cell.chromeY)
    return [
      { cols: fitCells(a, cell.width, SCROLLBAR_PX), rows },
      { cols: fitCells(b, cell.width, SCROLLBAR_PX), rows },
    ]
  }
  const [a, b] = splitAxis(size.height, ratio)
  const cols = fitCells(size.width, cell.width, SCROLLBAR_PX)
  return [
    { cols, rows: fitCells(a, cell.height, cell.chromeY) },
    { cols, rows: fitCells(b, cell.height, cell.chromeY) },
  ]
}

const HARD_RATIO_RANGE = { min: 0.1, max: 0.9 } as const

/**
 * 让两格都不低于下限的比例范围；容器连 50/50 都放不下 → null。
 * 并排（row）看每格的列数，上下排（column）看每格的行数——另一根轴上两格是同一个值，不是分屏
 * 造成的窄。容器尺寸未知（≤0）→ 原来的 0.1–0.9（= 今天的行为，不折叠不夹取）。
 *
 * 一格的下限换算成像素 `need`，沿分屏轴的可分空间是 `avail`（已扣分隔条），则每格占比至少
 * `f = need / avail`，范围是 [f, 1-f]（再与 0.1–0.9 取交）。f > 0.5 就是放不下。
 */
export function usableRatioRange(
  size: ViewSize,
  orientation: SplitOrientation,
  cell: CellMetrics,
): { min: number; max: number } | null {
  if (!(size.width > 0) || !(size.height > 0)) return HARD_RATIO_RANGE
  const avail = (orientation === 'row' ? size.width : size.height) - DIVIDER_PX
  if (!(avail > 0)) return null
  const needPx = orientation === 'row'
    ? MIN_PANE_COLS * cell.width + SCROLLBAR_PX
    : MIN_PANE_ROWS * cell.height + cell.chromeY
  const f = needPx / avail
  if (f > 0.5 + EPS) return null
  return {
    min: Math.max(HARD_RATIO_RANGE.min, f),
    max: Math.min(HARD_RATIO_RANGE.max, 1 - f),
  }
}

/**
 * REQ-SPL-01：容器放不下两个可用面板吗？——**不看已存的比例**：只要存在某个比例能让两格都够用，
 * 就不折叠（比例太极端由 `renderRatio` 在渲染期夹回来）。
 */
export function shouldCollapse(
  size: ViewSize,
  orientation: SplitOrientation,
  cell: CellMetrics,
): boolean {
  return usableRatioRange(size, orientation, cell) === null
}

/**
 * 并排渲染时实际用的比例：存下来的比例夹进可用范围。**只在渲染期夹，绝不写回同步的分屏模型**——
 * 窗口回到更宽，存下来的 0.9 就又是 0.9。容器放不下两格（折叠）时没有可夹的范围，原样返回
 * （此时比例本来就用不上）。
 */
export function renderRatio(
  size: ViewSize,
  orientation: SplitOrientation,
  cell: CellMetrics,
  stored: number,
): number {
  const range = usableRatioRange(size, orientation, cell)
  return range ? clampDragRatio(stored, range) : stored
}

/**
 * 分隔条拖拽允许的比例范围 = 渲染用的可用范围（同一个函数，所以拖到哪、渲染就在哪，不会出现
 * "拖到 0.15 松手后被夹成 0.2"的跳变）。容器本来就放不下两格（已折叠、没有分隔条可拖）→ 0.1–0.9。
 */
export function dragRatioBounds(
  size: ViewSize,
  orientation: SplitOrientation,
  cell: CellMetrics,
): { min: number; max: number } {
  return usableRatioRange(size, orientation, cell) ?? HARD_RATIO_RANGE
}

export function clampDragRatio(raw: number, bounds: { min: number; max: number }): number {
  return Math.min(bounds.max, Math.max(bounds.min, raw))
}

/** 一个 tab 的分屏此刻怎么渲染。全是派生值——不写回同步的分屏模型。 */
export interface SplitLayout {
  collapsed: boolean
  /** 独占整个区域的那一格：放大（zoom）或折叠所致；undefined = 两格并排。 */
  solo?: PaneId
  /**
   * 这一格要不要显示（v-show），**同时也是它算不算观看者**（与 tab 激活取 AND 后喂给 surface 的
   * `active`）：看不见的格子不连接、不声明尺寸、已连上的撤回（REQ-SPL-02，放大藏起的同理）。
   */
  visible: Record<PaneId, boolean>
  /** 并排时每格实际用的比例（存下来的比例夹进可用范围后）；独占时用不上。 */
  ratio: number
  /** 分隔条要不要显示。 */
  divider: boolean
}

/**
 * 折叠时显示哪一格：本设备自己选的（localFocus）优先；没选过就跟着模型里的放大格（桌面上用户放大了
 * 哪格，手机就先看哪格）；都没有就第一格。**只读模型**——手机上切格子绝不改放大状态（REQ-SPL-03）。
 */
export function collapsedPane(zoomed: PaneId | undefined, localFocus: PaneId | undefined): PaneId {
  return localFocus ?? zoomed ?? 'first'
}

export function splitLayout(
  split: { zoomed?: PaneId; ratio?: number },
  collapsed: boolean,
  localFocus?: PaneId,
  ratio: number = split.ratio ?? 0.5,
): SplitLayout {
  const solo = collapsed ? collapsedPane(split.zoomed, localFocus) : split.zoomed
  return {
    collapsed,
    solo,
    visible: { first: !solo || solo === 'first', second: !solo || solo === 'second' },
    ratio,
    divider: !solo,
  }
}

/** 折叠时状态行里的面板切换器：显示"当前是哪一个、共两个"，点一下切到另一格（REQ-SPL-03）。 */
export interface PaneSwitcherModel {
  index: 1 | 2
  total: 2
  /** 点下去会切到的那一格。 */
  target: PaneId
  label: string
  hint: string
}

export function paneSwitcherModel(orientation: SplitOrientation, visible: PaneId): PaneSwitcherModel {
  const target = otherPane(visible)
  return {
    index: visible === 'first' ? 1 : 2,
    total: 2,
    target,
    label: `切到${panePlace(orientation, target)}那格`,
    hint: `屏幕放不下两格并排，现在只显示${panePlace(orientation, visible)}那格（共两格）；`
      + '只换本机显示，不影响其它设备',
  }
}
