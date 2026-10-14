/**
 * composePlacement — 悬浮输入框放在哪里（terminal-input REQ-TI-06）。纯算术，没有 DOM。
 *
 * 输入框浮在终端之上、**不占布局**，所以它出现 / 收起 / 被拖动时终端的行列数一格都不变。位置只有两个
 * 来源，按优先级：
 *   1. 用户拖过的位置（按 tab 记住，跨刷新）；
 *   2. 默认位置——出现的那一刻光标所在行；光标不在可见区就贴终端底部。
 *
 * 默认位置只在打开那一刻算一次，不跟着光标走：agent 运行时光标满屏跳，框跟着跳就没法打字了。
 */

export const COMPOSER_MARGIN = 8

export interface Point { x: number; y: number }
export interface Size { w: number; h: number }

const clamp = (v: number, lo: number, hi: number): number => Math.min(Math.max(v, lo), hi)

/**
 * 把位置夹回容器之内（四边各留 margin）。容器比框还小时，贴左上——左上永远比右下更接近用户在看的地方。
 * 窗口缩小后，已保存的位置靠它重新落回可见区；保存的值本身不改写，窗口放回去就复原。
 */
export function clampPlacement(p: Point, box: Size, bounds: Size, margin = COMPOSER_MARGIN): Point {
  const maxX = Math.max(margin, bounds.w - box.w - margin)
  const maxY = Math.max(margin, bounds.h - box.h - margin)
  return { x: clamp(p.x, margin, maxX), y: clamp(p.y, margin, maxY) }
}

/**
 * 默认位置：框的上沿对齐光标所在行的上沿（框盖住当前提示行，像是那一行自己变成了编辑器）；
 * 之后夹回容器——提示在屏幕底部时，框的下沿贴底，上沿随框的高度往上长。
 * anchorTop 为 null（光标不在可见区 / 量不到）：贴底。
 */
export function defaultPlacement(anchorTop: number | null, box: Size, bounds: Size, margin = COMPOSER_MARGIN): Point {
  const y = anchorTop === null ? bounds.h - box.h - margin : anchorTop
  return clampPlacement({ x: margin, y }, box, bounds, margin)
}

/**
 * 光标所在行的上沿，相对终端容器顶部的像素；光标不在当前可见区（用户翻回去了）或量不到 → null。
 * 光标的绝对行是 baseY + cursorY；视口显示 [viewportY, viewportY + rows)。
 */
export function cursorRowTop(g: {
  baseY: number
  cursorY: number
  viewportY: number
  rows: number
  /** .xterm-screen 的上沿与容器上沿的距离（像素）。 */
  screenOffsetTop: number
  /** .xterm-screen 的高度（像素）。 */
  screenHeight: number
}): number | null {
  if (g.rows <= 0 || !(g.screenHeight > 0)) return null
  const absolute = g.baseY + g.cursorY
  if (absolute < g.viewportY || absolute >= g.viewportY + g.rows) return null
  return g.screenOffsetTop + (absolute - g.viewportY) * (g.screenHeight / g.rows)
}

/** 位置的存取格式。读到坏数据一律当没有——用户拖过的位置丢了无所谓，输入框打不开才有所谓。 */
export function parsePlacement(raw: string | null): Point | null {
  if (!raw) return null
  try {
    const v = JSON.parse(raw) as Partial<Point> | null
    if (v && Number.isFinite(v.x) && Number.isFinite(v.y)) return { x: v.x as number, y: v.y as number }
  } catch { /* 坏数据 */ }
  return null
}

export function serializePlacement(p: Point): string {
  return JSON.stringify({ x: Math.round(p.x), y: Math.round(p.y) })
}

/** 按 tab 存：和草稿同一个作用域键（sessionId），两个标签的框互不影响。 */
export function placementStorageKey(scope: string | undefined): string {
  return scope ? `cli-compose-pos:${scope}` : 'cli-compose-pos'
}
