import { describe, it, expect } from 'bun:test'
import {
  closePaneTip,
  currentPane,
  otherPane,
  paneWord,
  splitButtonModel,
  type PaneId,
  type SplitOrientation,
  type SplitState,
} from '../splitControl'

const ORIENTATIONS: SplitOrientation[] = ['row', 'column']
const PANES: PaneId[] = ['first', 'second']

/** 所有可能的分屏状态：未分屏 + 两方向 × (不放大/放大第一格/放大第二格)。 */
const STATES: (SplitState | undefined)[] = [
  undefined,
  ...ORIENTATIONS.flatMap((orientation) =>
    [undefined, 'first', 'second'].map((zoomed) => ({ orientation, zoomed: zoomed as PaneId | undefined })),
  ),
]

describe('方向按钮永远不会关掉任何东西（2026-10-13：取消分屏会结束第二格的 session）', () => {
  it('任何状态、任何焦点下，两个方向按钮的动作只可能是 分屏 / 换方向 / 切换焦点', () => {
    for (const split of STATES) {
      for (const focused of PANES) {
        for (const o of ORIENTATIONS) {
          expect(['split', 'rotate', 'switch-pane']).toContain(splitButtonModel(o, split, focused).action)
        }
      }
    }
  })

  it('已分屏时，再点高亮的那个方向 = 切换焦点（不是取消分屏）', () => {
    const m = splitButtonModel('row', { orientation: 'row' }, 'first')
    expect(m.action).toBe('switch-pane')
    expect(m.active).toBe(true)
    expect(m.label).toBe('切到右边那格')
  })
})

describe('方向按钮 — 三种动作', () => {
  it('未分屏：按哪个就分成哪个方向，都不高亮', () => {
    expect(splitButtonModel('row', undefined, 'first')).toMatchObject({ action: 'split', active: false, label: '左右分屏' })
    expect(splitButtonModel('column', undefined, 'first')).toMatchObject({ action: 'split', active: false, label: '上下分屏' })
  })

  it('已分屏、另一方向：原地换方向，提示说清两格都保留', () => {
    const m = splitButtonModel('column', { orientation: 'row' }, 'first')
    expect(m).toMatchObject({ action: 'rotate', active: false, label: '改成上下分屏' })
    expect(m.hint).toContain('两格都保留')
  })

  it('切换焦点的目标 = 当前格的另一格，用方位说（不用 1/2 编号）', () => {
    expect(splitButtonModel('row', { orientation: 'row' }, 'second').label).toBe('切到左边那格')
    expect(splitButtonModel('column', { orientation: 'column' }, 'first').label).toBe('切到下边那格')
    expect(splitButtonModel('column', { orientation: 'column' }, 'second').label).toBe('切到上边那格')
  })

  it('图标点亮的那半 = 当前格', () => {
    expect(splitButtonModel('row', { orientation: 'row' }, 'second').current).toBe('second')
  })

  it('放大态下"当前格"是被放大的那格，与最后焦点无关；提示说明会改为放大另一格', () => {
    const m = splitButtonModel('row', { orientation: 'row', zoomed: 'second' }, 'first')
    expect(m.current).toBe('second')
    expect(m.label).toBe('切到左边那格')
    expect(m.hint).toContain('放大显示左边那格')
    expect(currentPane({ orientation: 'row', zoomed: 'first' }, 'second')).toBe('first')
  })
})

describe('切换焦点闪字（纯前端浮层）', () => {
  it('左右分屏 LEFT/RIGHT，上下分屏 UP/DOWN，全大写', () => {
    expect(paneWord('row', 'first')).toBe('LEFT')
    expect(paneWord('row', 'second')).toBe('RIGHT')
    expect(paneWord('column', 'first')).toBe('UP')
    expect(paneWord('column', 'second')).toBe('DOWN')
  })

  it('otherPane 是对合', () => {
    for (const p of PANES) expect(otherPane(otherPane(p))).toBe(p)
  })
})

describe('关闭一格：显式、讲清代价、要确认', () => {
  it('未上膛时说清关哪一格；上膛后要求再点一次', () => {
    expect(closePaneTip('row', 'second', false).label).toBe('关闭右边这格')
    expect(closePaneTip('column', 'second', false).label).toBe('关闭下边这格')
    expect(closePaneTip('row', 'second', true).label).toBe('再点一次确认关闭')
  })

  it('提示写明会丢什么', () => {
    expect(closePaneTip('row', 'second', false).hint).toContain('输出都会丢失')
  })
})
