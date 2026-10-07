import { describe, expect, test } from 'bun:test'
import { resolveDeepLinkTabId, syncTabQuery } from '../tabDeepLink'

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
