import { describe, expect, test } from 'bun:test'
import { resolveDeepLinkTabId, syncTabQuery, resolveDeepLinkPaneIndex, syncPaneQuery } from '../tabDeepLink'

const tabs = [{ id: 'a' }, { id: 'b' }]

describe('tab deep link (cli-tabs)', () => {
  test('resolves only ids that exist right now', () => {
    expect(resolveDeepLinkTabId('b', tabs)).toBe('b')
    expect(resolveDeepLinkTabId('gone', tabs)).toBeUndefined()
    expect(resolveDeepLinkTabId(undefined, tabs)).toBeUndefined()
    expect(resolveDeepLinkTabId('', tabs)).toBeUndefined()
    expect(resolveDeepLinkTabId(42, tabs)).toBeUndefined()
  })

  test('sync writes when URL differs, stays quiet when it matches', () => {
    expect(syncTabQuery('a', 'b')).toBe('a')
    expect(syncTabQuery('a', undefined)).toBe('a')
    expect(syncTabQuery('a', 'a')).toBeUndefined()
    // 无 activeTab（加载中/全关）不动 URL——服务端兜底语义不被 URL 抢跑
    expect(syncTabQuery(undefined, 'a')).toBeUndefined()
  })
})

describe('pane (tmux window) deep link — 双向, 2026-10-11 推翻 v1 的"不做"决定', () => {
  test('parses a non-negative integer, ignores everything else', () => {
    expect(resolveDeepLinkPaneIndex('4')).toBe(4)
    expect(resolveDeepLinkPaneIndex('0')).toBe(0)
    expect(resolveDeepLinkPaneIndex('not-a-number')).toBeUndefined()
    expect(resolveDeepLinkPaneIndex('-1')).toBeUndefined()
    expect(resolveDeepLinkPaneIndex('1.5')).toBeUndefined()
    expect(resolveDeepLinkPaneIndex(undefined)).toBeUndefined()
    expect(resolveDeepLinkPaneIndex(['4'])).toBeUndefined() // repeated query param → array, not a single value
  })

  test('sync writes when URL differs, stays quiet when it matches (no self-triggering loop)', () => {
    expect(syncPaneQuery(4, '1')).toBe('4')
    expect(syncPaneQuery(4, undefined)).toBe('4')
    // 自己刚写回的那次变化：下一轮算出来相等，必须不再触发——否则就是双向绑定的无限环
    expect(syncPaneQuery(4, '4')).toBeUndefined()
  })

  test('switching to a non-tmux tab clears a stale p, but only if one is actually there', () => {
    expect(syncPaneQuery(null, '4')).toBeNull() // 上一个 tmux tab 留下的 p，必须清掉
    expect(syncPaneQuery(null, undefined)).toBeUndefined() // 本来就没有，不必动
  })
})
