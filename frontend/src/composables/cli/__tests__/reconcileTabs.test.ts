import { describe, it, expect } from 'bun:test'
import { reconcileTabs, type ReconcilableTab, type ReconcilePorts } from '../reconcileTabs'
import { tabLivenessFrom, type TabLiveness } from '../tabLiveness'

/**
 * 这组测试锁的是一个**已经发生过的谎**：服务重启后，reconcile 会对每个孤儿标签悄悄
 * POST /sessions 开一个全新的空 PTY 绑回去，屏幕上和「恢复成功」完全一致——用户以为 agent 还在，
 * 其实早没了；POST 失败时还 removeTab()，标签凭空消失。
 *
 * 所以除了断言存活态判得对，这里同样重要的是断言 reconcile **什么都没造、什么都没删**。
 */
function harness(opts: {
  local?: ReadonlySet<string> | null
  peers?: Record<string, ReadonlySet<string> | null>
}) {
  const liveness: Record<string, TabLiveness> = {}
  const unbound: string[] = []
  const calls: string[] = []
  const ports: ReconcilePorts = {
    listLocalSessions: () => { calls.push('list:local'); return Promise.resolve(opts.local ?? null) },
    listPeerSessions: (peerId) => { calls.push(`list:${peerId}`); return Promise.resolve(opts.peers?.[peerId] ?? null) },
    setLiveness: (tabId, l) => { liveness[tabId] = l },
    unbindSession: (tabId) => { unbound.push(tabId) },
  }
  return { ports, liveness, unbound, calls }
}

describe('reconcileTabs — 不许假装恢复', () => {
  it('孤儿标签标成 detached，而不是偷偷开一个新 PTY 顶上去', async () => {
    // 服务重启：标签还在（持久化过），它记着的 session 在服务端已经不存在。
    const tabs: ReconcilableTab[] = [{ id: 'tab-1', sessionId: 'dead-session' }]
    const h = harness({ local: new Set(['some-other-live-session']) })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness['tab-1']).toBe('detached')
    // 那条 sessionId 现在指向不存在之物，必须清掉，否则后续 DELETE/WS 会打到空处。
    expect(h.unbound).toEqual(['tab-1'])
    // 只读了一次清单：没有第二次请求，也就没有 POST 的余地。
    expect(h.calls).toEqual(['list:local'])
  })

  it('端口集合里根本没有能造出终端的口子（结构上写不回那个谎）', () => {
    const h = harness({ local: new Set() })
    expect(Object.keys(h.ports).sort()).toEqual([
      'listLocalSessions', 'listPeerSessions', 'setLiveness', 'unbindSession',
    ])
  })

  it('服务端没答 → unreachable，且**保留**绑定（请求失败不是死亡证明）', async () => {
    // 这是反方向的同一个谎：把「没问到」当成空集，服务端一抖动全部标签就被判死，
    // 连接恢复后再也接不回那个其实还活着的 PTY。
    const tabs: ReconcilableTab[] = [{ id: 'tab-1', sessionId: 'maybe-alive' }]
    const h = harness({ local: null })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness['tab-1']).toBe('unreachable')
    expect(h.unbound).toEqual([])
  })

  it('session 还在 → live（正常路径一个字不变）', async () => {
    const tabs: ReconcilableTab[] = [{ id: 'tab-1', sessionId: 's1' }]
    const h = harness({ local: new Set(['s1']) })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness['tab-1']).toBe('live')
    expect(h.unbound).toEqual([])
  })

  it('从来没绑过 session 的标签也算 detached（不再 removeTab 让它凭空消失）', async () => {
    const tabs: ReconcilableTab[] = [{ id: 'tab-1' }]
    const h = harness({ local: new Set(['s1']) })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness['tab-1']).toBe('detached')
    // 没有 sessionId 就没什么可解绑的。
    expect(h.unbound).toEqual([])
  })
})

describe('reconcileTabs — 远程标签（mesh）', () => {
  it('peer 可达但那个 session 没了 → detached（原来的「丢失重建」也删了）', async () => {
    const tabs: ReconcilableTab[] = [{ id: 'tab-r', sessionId: 'dead', remotePeerId: 'peer-a' }]
    const h = harness({ peers: { 'peer-a': new Set(['other']) } })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness['tab-r']).toBe('detached')
    expect(h.unbound).toEqual(['tab-r'])
  })

  it('peer 离线 → unreachable 且保留绑定（那边的 agent 可能正跑着）', async () => {
    const tabs: ReconcilableTab[] = [{ id: 'tab-r', sessionId: 's-remote', remotePeerId: 'peer-a' }]
    const h = harness({ peers: { 'peer-a': null } })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness['tab-r']).toBe('unreachable')
    expect(h.unbound).toEqual([])
  })

  it('本机清单不会被拿去判远程标签（远程 session 本来就不在本机列表里）', async () => {
    // 早期的真实 bug 形状：远程标签拿本机 /sessions 对账 → 每个远程标签都被误判成没了。
    const tabs: ReconcilableTab[] = [
      { id: 'tab-l', sessionId: 's-local' },
      { id: 'tab-r', sessionId: 's-remote', remotePeerId: 'peer-a' },
    ]
    const h = harness({ local: new Set(['s-local']), peers: { 'peer-a': new Set(['s-remote']) } })

    await reconcileTabs(tabs, h.ports)

    expect(h.liveness).toEqual({ 'tab-l': 'live', 'tab-r': 'live' })
  })

  it('同一个 peer 上的多个标签只问一次清单', async () => {
    const tabs: ReconcilableTab[] = [
      { id: 'a', sessionId: 's1', remotePeerId: 'p' },
      { id: 'b', sessionId: 's2', remotePeerId: 'p' },
      { id: 'c', sessionId: 's3', remotePeerId: 'q' },
    ]
    const h = harness({ peers: { p: new Set(['s1']), q: null } })

    await reconcileTabs(tabs, h.ports)

    expect(h.calls).toEqual(['list:p', 'list:q'])
    expect(h.liveness).toEqual({ a: 'live', b: 'detached', c: 'unreachable' })
  })

  it('一个标签都没有时不发任何请求', async () => {
    const h = harness({ local: new Set() })
    await reconcileTabs([], h.ports)
    expect(h.calls).toEqual([])
  })

  it('全是远程标签时不去问本机清单', async () => {
    const h = harness({ peers: { p: new Set(['s1']) } })
    await reconcileTabs([{ id: 'a', sessionId: 's1', remotePeerId: 'p' }], h.ports)
    expect(h.calls).toEqual(['list:p'])
  })
})

describe('tabLivenessFrom — 判定 SSOT', () => {
  it('null（没问到）和空集合（真的一个都没有）不是一回事', () => {
    expect(tabLivenessFrom('s1', null)).toBe('unreachable')
    expect(tabLivenessFrom('s1', new Set())).toBe('detached')
  })

  it('没问到时，连"这个标签根本没绑过 session"都不敢下结论', () => {
    expect(tabLivenessFrom(undefined, null)).toBe('unreachable')
  })

  it('在清单里就是 live', () => {
    expect(tabLivenessFrom('s1', new Set(['s1', 's2']))).toBe('live')
  })
})
