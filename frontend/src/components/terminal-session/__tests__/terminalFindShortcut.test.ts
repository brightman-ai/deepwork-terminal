import { describe, it, expect } from 'bun:test'
import { findShortcutIntent, handleFindShortcut } from '../terminalFindShortcut'

/** 一个只记录"被调用过没有"的假 event —— 这条判定用到的全部契约就这两个方法。 */
function fakeEvent() {
  const calls = { preventDefault: 0, stopPropagation: 0 }
  return {
    calls,
    preventDefault() { calls.preventDefault++ },
    stopPropagation() { calls.stopPropagation++ },
  }
}

describe('findShortcutIntent — 先匹配按键，再决定开还是聚焦', () => {
  it('没匹配上按键 = 不关我们的事', () => {
    expect(findShortcutIntent({ active: true, matches: false, open: false })).toBe('ignore')
    expect(findShortcutIntent({ active: true, matches: false, open: true })).toBe('ignore')
  })

  it('不是当前可见的那个 surface = 不关我们的事（每个标签都挂着，都会收到这次 keydown）', () => {
    expect(findShortcutIntent({ active: false, matches: true, open: false })).toBe('ignore')
    expect(findShortcutIntent({ active: false, matches: true, open: true })).toBe('ignore')
  })

  it('匹配上了：没开就开，已开就聚焦', () => {
    expect(findShortcutIntent({ active: true, matches: true, open: false })).toBe('open')
    expect(findShortcutIntent({ active: true, matches: true, open: true })).toBe('refocus')
  })
})

describe('handleFindShortcut — 被接管的快捷键必须每一次都吃掉', () => {
  it('第一次按下：preventDefault + 打开搜索条', () => {
    const e = fakeEvent()
    let opened = 0
    let refocused = 0
    const intent = handleFindShortcut(
      e,
      { active: true, matches: true, open: false },
      { open: () => { opened++ }, refocus: () => { refocused++ } },
    )
    expect(intent).toBe('open')
    expect(opened).toBe(1)
    expect(refocused).toBe(0)
    expect(e.calls.preventDefault).toBe(1)
    expect(e.calls.stopPropagation).toBe(1)
  })

  // 这就是那个 bug：老写法「搜索条已开就早退」让第二次按键走不到 preventDefault，
  // 按键漏给浏览器 → 浏览器自带查找框和我们的搜索条同时出现在屏幕上（Human 实测截图）。
  it('已经打开时再次按下：仍然 preventDefault（绝不漏给浏览器），并把焦点送回输入框', () => {
    const e = fakeEvent()
    let opened = 0
    let refocused = 0
    const intent = handleFindShortcut(
      e,
      { active: true, matches: true, open: true },
      { open: () => { opened++ }, refocus: () => { refocused++ } },
    )
    expect(intent).toBe('refocus')
    expect(e.calls.preventDefault).toBe(1)
    expect(e.calls.stopPropagation).toBe(1)
    expect(refocused).toBe(1)
    expect(opened).toBe(0) // 已经开着的搜索条不该被"再开一次"（那会清掉已输入的查询词）
  })

  it('连按三次：每一次都 preventDefault', () => {
    const e = fakeEvent()
    let open = false
    for (let i = 0; i < 3; i++) {
      handleFindShortcut(
        e,
        { active: true, matches: true, open },
        { open: () => { open = true }, refocus: () => {} },
      )
    }
    expect(e.calls.preventDefault).toBe(3)
  })

  it('不匹配的按键一个字都不碰（否则会吃掉终端自己的键）', () => {
    const e = fakeEvent()
    handleFindShortcut(e, { active: true, matches: false, open: true }, { open: () => {}, refocus: () => {} })
    handleFindShortcut(e, { active: false, matches: true, open: true }, { open: () => {}, refocus: () => {} })
    expect(e.calls.preventDefault).toBe(0)
    expect(e.calls.stopPropagation).toBe(0)
  })
})
