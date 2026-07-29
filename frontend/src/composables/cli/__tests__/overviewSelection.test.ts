import { describe, it, expect } from 'bun:test'
import { selectOverviewCard } from '../overviewSelection'

/**
 * 锁的是「点中一张卡片 = 去那个终端」这件事的完整性：以前点完卡片总览还赖在屏幕上，用户得再按
 * 一次 Esc 或点一下遮罩，等于把一个意图拆成了两步。切换与关闭必须在同一处发生，而且两个壳
 * （standalone / pro）共用这一处——否则「点中要不要关」迟早在两边长成两种行为。
 */
function harness() {
  const calls: string[] = []
  return {
    calls,
    ports: {
      switchTo: (id: string) => { calls.push(`switch:${id}`) },
      closeOverview: () => { calls.push('close') },
    },
  }
}

describe('selectOverviewCard — 选中即切过去并关闭', () => {
  it('点第 2 张卡：切到第 2 个终端，然后关掉总览', () => {
    const h = harness()
    selectOverviewCard(2, ['a', 'b', 'c'], h.ports)
    expect(h.calls).toEqual(['switch:b', 'close'])
  })

  it('先切后关，顺序不能反（先关会让切换发生在一个已经卸载的视图上）', () => {
    const h = harness()
    selectOverviewCard(1, ['a'], h.ports)
    expect(h.calls[0]).toBe('switch:a')
    expect(h.calls[1]).toBe('close')
  })

  it('编号越界：什么都不做——既不切也不关（用户什么也没选中）', () => {
    const h = harness()
    selectOverviewCard(4, ['a', 'b', 'c'], h.ports)
    selectOverviewCard(0, ['a'], h.ports)
    selectOverviewCard(1, [], h.ports)
    expect(h.calls).toEqual([])
  })

  it('编号是 1-based，和「终端N」/ 快捷键的编号是同一套', () => {
    const h = harness()
    selectOverviewCard(1, ['first', 'second'], h.ports)
    expect(h.calls).toEqual(['switch:first', 'close'])
  })
})
