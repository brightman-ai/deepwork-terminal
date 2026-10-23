import { describe, expect, it } from 'bun:test'
import { moveTabBefore, replayTabMoves } from '../tabReorder'

describe('tab insertion by identity', () => {
  const tabs = ['a', 'b', 'c'].map(id => ({ id, sessionId: `session-${id}` }))
  it('moves in both directions and to the end without replacing terminal identities', () => {
    const front = moveTabBefore(tabs, 'c', 'a')
    expect(front.map(t => t.id)).toEqual(['c', 'a', 'b'])
    expect(front[0]).toBe(tabs[2])
    expect(moveTabBefore(front, 'c', null)).toEqual(tabs)
    expect(tabs.map(t => t.id)).toEqual(['a', 'b', 'c'])
  })
  it('rejects a missing source / missing anchor / self without dropping a tab', () => {
    for (const [from, before] of [['missing', 'a'], ['a', 'missing'], ['a', 'a']]) {
      expect(moveTabBefore(tabs, from!, before!)).toEqual(tabs)
    }
  })
  it('does not move across groups or resurrect a concurrently closed tab', () => {
    const groups = [{ id: 'g1', tabs: [tabs[0]!, tabs[1]!] }, { id: 'g2', tabs: [tabs[2]!] }]
    replayTabMoves(groups, [
      { groupId: 'g1', tabId: 'a', beforeTabId: 'c' },
      { groupId: 'g2', tabId: 'a', beforeTabId: null },
      { groupId: 'g1', tabId: 'closed', beforeTabId: 'a' },
    ])
    expect(groups.map(g => g.tabs.map(t => t.id))).toEqual([['a', 'b'], ['c']])
  })
})
