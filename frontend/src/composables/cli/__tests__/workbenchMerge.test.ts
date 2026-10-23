import { describe, it, expect } from 'bun:test'
import { mergeWorkbench } from '../workbenchMerge'
import type { WorkbenchConfig, WorkbenchTab } from '@terminal/types/workbench'

/**
 * 三方合并是那次数据丢失修复里唯一需要逐 case 钉住的逻辑。
 *
 * 事故本体（2026-09-03）：手机上一个从 21:29 之前就开着的页面，内存里那份标签列表没有 PC 后来新建
 * 的标签。手机侧只要发生任何一次存盘（点一下标签，甚至完全自动的 setTabCwd），就把那条标签连同它
 * 绑着的 session 从服务端抹掉了 —— 那个 session（一个正在对话的 claude）其实一直活着，只是再也没有
 * 标签指着它。第一个用例就是这个场景，其余用例守的是"修的时候别把反方向弄坏"。
 */

const G = 'g1'

function tab(id: string, extra: Partial<WorkbenchTab> = {}): WorkbenchTab {
  return { id, groupId: G, name: id, cwd: '~', engine: 'shell', ...extra }
}

function cfg(tabs: WorkbenchTab[], extra: Partial<WorkbenchConfig> = {}): WorkbenchConfig {
  return {
    groups: [{ id: G, name: '默认', tabs, collapsed: false }],
    activeGroupId: G,
    activeTabId: tabs[0]?.id ?? '',
    lastSaved: '2026-09-03T00:00:00.000Z',
    ...extra,
  }
}

function ids(c: WorkbenchConfig): string[] {
  return c.groups.flatMap(g => g.tabs).map(t => t.id)
}

function find(c: WorkbenchConfig, id: string): WorkbenchTab | undefined {
  return c.groups.flatMap(g => g.tabs).find(t => t.id === id)
}

describe('mergeWorkbench — 事故本体', () => {
  it('陈旧副本不再抹掉别的设备新建的标签（连同它绑着的活 session）', () => {
    const base = cfg([tab('t1'), tab('t2')])
    const local = cfg([tab('t1'), tab('t2')]) // 手机：两小时前的副本，没有 t3
    const server = cfg([tab('t1'), tab('t2'), tab('t3', { sessionId: 'claude-alive' })])

    const merged = mergeWorkbench(base, local, server)

    expect(ids(merged)).toEqual(['t1', 't2', 't3'])
    expect(find(merged, 't3')?.sessionId).toBe('claude-alive')
  })

  it('同一个位置在两边绑着不同 session 时，本地没动过就采信服务端的绑定', () => {
    // "PC 的 tab10 和手机的 tab10 不是同一个 session" 的字段级形态。
    const base = cfg([tab('t1', { sessionId: 'old' })])
    const local = cfg([tab('t1', { sessionId: 'old' })]) // 本地没改过绑定
    const server = cfg([tab('t1', { sessionId: 'new' })])

    expect(find(mergeWorkbench(base, local, server), 't1')?.sessionId).toBe('new')
  })
})

describe('mergeWorkbench — 删除意图（两个方向都不能猜错）', () => {
  it('本地关掉的标签保持关闭，不被服务端那份复活', () => {
    const base = cfg([tab('t1'), tab('t2')])
    const local = cfg([tab('t1')]) // 我关了 t2
    const server = cfg([tab('t1'), tab('t2')]) // 服务端还没收到

    expect(ids(mergeWorkbench(base, local, server))).toEqual(['t1'])
  })

  it('别的设备关掉的标签跟着消失', () => {
    const base = cfg([tab('t1'), tab('t2')])
    const local = cfg([tab('t1'), tab('t2')])
    const server = cfg([tab('t1')]) // 别人关了 t2

    expect(ids(mergeWorkbench(base, local, server))).toEqual(['t1'])
  })

  it('本地刚新建、还没上去的标签保留', () => {
    const base = cfg([tab('t1')])
    const local = cfg([tab('t1'), tab('t9')]) // 刚新建
    const server = cfg([tab('t1')])

    expect(ids(mergeWorkbench(base, local, server))).toEqual(['t1', 't9'])
  })
})

describe('mergeWorkbench — 字段级', () => {
  it('本地改过的字段本地赢', () => {
    const base = cfg([tab('t1', { name: 'old' })])
    const local = cfg([tab('t1', { name: 'mine' })])
    const server = cfg([tab('t1', { name: 'theirs' })])

    expect(find(mergeWorkbench(base, local, server), 't1')?.name).toBe('mine')
  })

  it('本地没动过的字段采信服务端（跨设备重命名才传得过来）', () => {
    const base = cfg([tab('t1', { name: 'old' })])
    const local = cfg([tab('t1', { name: 'old' })])
    const server = cfg([tab('t1', { name: 'theirs' })])

    expect(find(mergeWorkbench(base, local, server), 't1')?.name).toBe('theirs')
  })

  it('本地解绑（进程已确认结束）不被服务端的旧绑定盖回去', () => {
    const base = cfg([tab('t1', { sessionId: 'dead' })])
    const local = cfg([tab('t1', { sessionId: undefined })])
    const server = cfg([tab('t1', { sessionId: 'dead' })])

    expect(find(mergeWorkbench(base, local, server), 't1')?.sessionId).toBeUndefined()
  })

  // terminal-split scope: `split` 漏在 TAB_FIELDS 之外会静默丢分屏状态——同文件头注早已点名
  // "漏一个字段会静默丢一次用户改动"，这条就是那条规则的活体回归，不是装饰性覆盖率。
  it('本地刚分的屏不被服务端那份陈旧的未分屏状态盖掉（split 必须在 TAB_FIELDS 里）', () => {
    const base = cfg([tab('t1')])
    const local = cfg([tab('t1', { split: { orientation: 'row', ratio: 0.5, secondSessionId: 's2' } })])
    const server = cfg([tab('t1')]) // 服务端这份还没见过这台设备刚做的分屏

    expect(find(mergeWorkbench(base, local, server), 't1')?.split).toEqual({
      orientation: 'row', ratio: 0.5, secondSessionId: 's2',
    })
  })

  it('本地取消分屏（split=undefined）不被服务端的旧分屏状态盖回去', () => {
    const base = cfg([tab('t1', { split: { orientation: 'row', ratio: 0.5, secondSessionId: 's2' } })])
    const local = cfg([tab('t1', { split: undefined })])
    const server = cfg([tab('t1', { split: { orientation: 'row', ratio: 0.5, secondSessionId: 's2' } })])

    expect(find(mergeWorkbench(base, local, server), 't1')?.split).toBeUndefined()
  })
})

describe('mergeWorkbench — 顺序与焦点', () => {
  it('a sorting-only write does not overwrite a remote split change through cloned object identity', () => {
    const split = { orientation: 'row' as const, ratio: 0.5, secondSessionId: 'second' }
    const base = cfg([tab('a', { split }), tab('b')])
    const local = JSON.parse(JSON.stringify(base)) as WorkbenchConfig
    local.groups[0]!.tabs.reverse()
    const server = cfg([tab('a', { split: { ...split, ratio: 0.7 } }), tab('b')])
    const result = mergeWorkbench(base, local, server, [{ groupId: G, tabId: 'b', beforeTabId: 'a' }])
    expect(find(result, 'a')?.split?.ratio).toBe(0.7)
    expect(ids(result)).toEqual(['b', 'a'])
  })
  it('replays a pending move over remote additions/rename, preserving ids and focus', () => {
    const base = cfg([tab('a'), tab('b'), tab('c')])
    const local = cfg([tab('c'), tab('a'), tab('b')], { activeTabId: 'b' })
    const server = cfg([tab('a', { name: 'renamed' }), tab('new'), tab('b'), tab('c')], { rev: 7 })
    const result = mergeWorkbench(base, local, server, [{ groupId: G, tabId: 'c', beforeTabId: 'a' }])
    expect(ids(result)).toEqual(['c', 'a', 'new', 'b'])
    expect(find(result, 'a')?.name).toBe('renamed')
    expect(result.activeTabId).toBe('b')
    expect(result.rev).toBe(7)
  })

  it('a remote close wins over a pending move; a closed anchor cancels the move', () => {
    const base = cfg([tab('a'), tab('b'), tab('c')])
    const local = cfg([tab('c'), tab('a'), tab('b')])
    const move = [{ groupId: G, tabId: 'c', beforeTabId: 'a' }]
    expect(ids(mergeWorkbench(base, local, cfg([tab('a'), tab('b')]), move))).toEqual(['a', 'b'])
    expect(ids(mergeWorkbench(base, local, cfg([tab('b'), tab('c')]), move))).toEqual(['b', 'c'])
  })

  it('a stale cwd-only save cannot undo a successfully saved reorder', () => {
    const base = cfg([tab('a'), tab('b'), tab('c')])
    const local = cfg([tab('a', { cwd: '/new' }), tab('b'), tab('c')])
    const result = mergeWorkbench(base, local, cfg([tab('c'), tab('a'), tab('b')]))
    expect(ids(result)).toEqual(['c', 'a', 'b'])
    expect(find(result, 'a')?.cwd).toBe('/new')
  })

  it('simultaneous sorting rebases only the explicit local move onto the current server order', () => {
    const base = cfg([tab('a'), tab('b'), tab('c')])
    const local = cfg([tab('b'), tab('c'), tab('a')])
    const server = cfg([tab('c'), tab('a'), tab('b')])
    expect(ids(mergeWorkbench(base, local, server, [{ groupId: G, tabId: 'a', beforeTabId: null }]))).toEqual(['c', 'b', 'a'])
  })

  it('顺序以服务端为骨架，本地独有的追加在后（两台设备必须收敛到同一编号）', () => {
    const base = cfg([tab('a')])
    const local = cfg([tab('a'), tab('mine')])
    const server = cfg([tab('z'), tab('a')]) // 服务端把 z 放在了前面

    expect(ids(mergeWorkbench(base, local, server))).toEqual(['z', 'a', 'mine'])
  })

  it('焦点是本设备的私事，不被别的设备抢走', () => {
    const base = cfg([tab('t1'), tab('t2')])
    const local = cfg([tab('t1'), tab('t2')], { activeTabId: 't2' })
    const server = cfg([tab('t1'), tab('t2')], { activeTabId: 't1' })

    expect(mergeWorkbench(base, local, server).activeTabId).toBe('t2')
  })

  it('本地焦点指向已经不存在的标签时才退回服务端的选择', () => {
    const base = cfg([tab('t1'), tab('t2')])
    const local = cfg([tab('t1'), tab('t2')], { activeTabId: 't2' })
    const server = cfg([tab('t1')], { activeTabId: 't1' }) // t2 被别人关了

    expect(mergeWorkbench(base, local, server).activeTabId).toBe('t1')
  })

  it('把服务端的 rev 带进结果，否则下一次 PUT 又是陈旧的', () => {
    const merged = mergeWorkbench(cfg([tab('t1')]), cfg([tab('t1')]), cfg([tab('t1')], { rev: 42 }))
    expect(merged.rev).toBe(42)
  })
})

describe('mergeWorkbench — 没有基准时宁可多留，不可再丢', () => {
  it('base 为 null 时取并集（分辨不出删除，就不冒丢一个活 session 的风险）', () => {
    const local = cfg([tab('t1'), tab('mine')])
    const server = cfg([tab('t1'), tab('theirs')])

    expect(ids(mergeWorkbench(null, local, server)).sort()).toEqual(['mine', 't1', 'theirs'])
  })
})

describe('mergeWorkbench — 退化输入不产出不可用的配置', () => {
  it('两边都把分组删空时仍留下一个分组（否则 addTab 无处可去，静默不新建）', () => {
    const empty: WorkbenchConfig = { groups: [], activeGroupId: '', activeTabId: '', lastSaved: '' }
    const merged = mergeWorkbench(cfg([tab('t1')]), cfg([tab('t1')]), empty)
    expect(merged.groups.length).toBeGreaterThan(0)
  })

  it('groupId 指向已不存在的分组时标签挂到第一个分组，而不是被丢掉', () => {
    const base = cfg([tab('t1')])
    const local = cfg([tab('t1'), tab('orphan', { groupId: 'gone' })])
    const server = cfg([tab('t1')])

    const merged = mergeWorkbench(base, local, server)
    expect(ids(merged)).toContain('orphan')
    expect(find(merged, 'orphan')?.groupId).toBe(G)
  })
})
