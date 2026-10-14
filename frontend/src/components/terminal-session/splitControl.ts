/**
 * splitControl —— 分屏控件（SplitControlBar）的**判定**，与 Vue 无关，因此可测。
 *
 * ── 硬规矩：方向按钮永远不会关掉任何东西 ─────────────────────────────────────────────────────
 * 2026-10-13 之前，"再点一次已高亮的方向按钮 = 取消分屏"，而取消分屏会结束第二格的 session（里面
 * 跑着的程序、输出全没了）。Human 实测："这个设计有点危险，可能会丢东西……意符和作用不符"——一个
 * 画着版面的按钮，悄悄干的是关窗口。现在：
 *   · 未分屏        → 按哪个方向就分成哪个方向（'split'）
 *   · 已分屏、另一方向 → 原地换排列方向，两格都保留（'rotate'）
 *   · 已分屏、同一方向 → **切换焦点**到另一格（'switch-pane'）；图标里亮的那半 = 当前格
 * 关闭一格只能走一个**单独的、画着 × 的**按钮，且要点两次确认（closePaneTip）。
 */

export type PaneId = 'first' | 'second'
export type SplitOrientation = 'row' | 'column'
export type SplitButtonAction = 'split' | 'rotate' | 'switch-pane'

export interface SplitState {
  orientation: SplitOrientation
  zoomed?: PaneId
}

export interface SplitButtonModel {
  orientation: SplitOrientation
  action: SplitButtonAction
  /** 高亮 = 这就是当前的排列方向。 */
  active: boolean
  label: string
  hint: string
  /** 仅 'switch-pane'：图标里点亮的那半（此刻的当前格）。 */
  current?: PaneId
}

/** 两格在各自方向上的人话名字。不用"第 1 / 第 2 格"——Human 实测看不懂编号。 */
const PANE_PLACE: Record<SplitOrientation, Record<PaneId, string>> = {
  row: { first: '左边', second: '右边' },
  column: { first: '上边', second: '下边' },
}

/** 切换焦点那一下，落点格上闪一下的大字。只在前端画（DOM 浮层），绝不写进终端字节流——否则会
 *  进 scrollback，F5 回放和回看里都会留下这几个字。 */
const PANE_WORD: Record<SplitOrientation, Record<PaneId, string>> = {
  row: { first: 'LEFT', second: 'RIGHT' },
  column: { first: 'UP', second: 'DOWN' },
}

export function otherPane(p: PaneId): PaneId {
  return p === 'first' ? 'second' : 'first'
}

export function panePlace(o: SplitOrientation, p: PaneId): string {
  return PANE_PLACE[o][p]
}

export function paneWord(o: SplitOrientation, p: PaneId): string {
  return PANE_WORD[o][p]
}

/** 此刻的"当前格"：放大态下就是被放大的那格（另一格看不见），否则是最后获得焦点的那格。 */
export function currentPane(split: SplitState, focused: PaneId): PaneId {
  return split.zoomed ?? focused
}

/** 一个方向按钮此刻是什么、按下去做什么。`split` 为 undefined = 未分屏。 */
export function splitButtonModel(
  orientation: SplitOrientation,
  split: SplitState | undefined,
  focused: PaneId,
): SplitButtonModel {
  if (!split) {
    return {
      orientation,
      action: 'split',
      active: false,
      label: orientation === 'row' ? '左右分屏' : '上下分屏',
      hint: orientation === 'row' ? '在右边新开一个终端' : '在下边新开一个终端',
    }
  }
  if (split.orientation !== orientation) {
    return {
      orientation,
      action: 'rotate',
      active: false,
      label: orientation === 'row' ? '改成左右分屏' : '改成上下分屏',
      hint: '两格都保留，只换排列方向',
    }
  }
  const current = currentPane(split, focused)
  const place = PANE_PLACE[orientation][otherPane(current)]
  return {
    orientation,
    action: 'switch-pane',
    active: true,
    current,
    label: `切到${place}那格`,
    hint: split.zoomed
      ? `改为放大显示${place}那格；当前这格仍在后台运行`
      : '键盘输入改到那一格；图标里亮的那半是当前格',
  }
}

/** 关闭一格的 × 按钮提示。armed = 已经点过一次、等确认。 */
export function closePaneTip(
  orientation: SplitOrientation,
  pane: PaneId,
  armed: boolean,
): { label: string; hint: string } {
  return {
    label: armed ? '再点一次确认关闭' : `关闭${PANE_PLACE[orientation][pane]}这格`,
    hint: '这格的终端会被结束，里面运行的程序和输出都会丢失',
  }
}

/** × 第一次点下后等确认的时长。过时自动复原 —— 不让一个"上膛"的按钮一直挂着等误触。 */
export const CLOSE_CONFIRM_MS = 3000

/** 挂在状态行里的分屏控件根节点（SplitControlBar / 折叠时的 SplitPaneSwitcher）。 */
const PANE_CONTROL_SELECTOR = '.split-ctl, .split-switch'

/**
 * 在某一格里按下 / 聚焦，要不要把"当前格"让给它。
 *
 * 分屏控件自己的按钮不算：点"切换焦点"那一下会先让按钮拿到焦点（或先触发 pointerdown），若也算，
 * 当前格就先被改成"按钮所在的这一格"，随后的 click 再往"另一格"切，永远切不走。
 * focusin（键盘/鼠标）和 pointerdown（触屏——点击落在触控球层上，终端的 helper textarea 随即被
 * blur，根本不产生 focusin）共用这一条判定。
 */
export function claimsPaneFocus(target: { closest?: (selector: string) => unknown } | null | undefined): boolean {
  return !target?.closest?.(PANE_CONTROL_SELECTOR)
}
