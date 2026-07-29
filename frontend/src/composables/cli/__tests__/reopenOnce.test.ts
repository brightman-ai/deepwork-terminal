import { describe, it, expect, beforeEach } from 'bun:test'
import { reconcileTabs, type ReconcilePorts } from '../reconcileTabs'
import { reopenDetachedTabs } from '../reopenDetached'
import {
  postReopenNotice,
  takePendingTerminalNotice,
  reopenNoticeOf,
  noteUserInput,
  forgetTerminalNotice,
} from '../terminalNotice'
import type { TabLiveness } from '../tabLiveness'

/**
 * 锁的是「只打扰一次」。
 *
 * 自动重开必须留痕，但**留痕不能变成每次打开都被打扰**：如果标签里那条已经死掉的 sessionId 没有
 * 被换成新建的那个，下次进来对账又判 detached → 又重开一次 → 又写一行痕。屏幕上的效果就是"每次
 * 一打开就告诉我有个终端死了"。所以「换掉陈旧指针」和「留痕」是同一件事的两半，这里把两次进场
 * 连起来跑：第一次留一行痕，第二次必须**完全安静**。
 *
 * 这个夹具照抄 useCliState.reconcileSessions 的接线（reconcileTabs 判定 → reopenDetachedTabs 动作，
 * adopt = bindSession + 投递痕迹），所以它测的是那条真实路径的形状，而不是另造一条。
 */
interface FakeTab { id: string; name: string; cwd?: string; sessionId?: string }

function workbench(tabs: FakeTab[], server: Set<string>) {
  const created: string[] = []
  const notices: string[] = []
  let seq = 0

  const ports: ReconcilePorts = {
    listLocalSessions: () => Promise.resolve(new Set(server)),
    listPeerSessions: () => Promise.resolve(null),
    setLiveness: (tabId, l) => { liveness[tabId] = l },
    // 真实实现会把这条已失效的 sessionId 从 workbench 配置里清掉（会持久化）。
    unbindSession: (tabId) => { const t = tabs.find((x) => x.id === tabId); if (t) t.sessionId = undefined },
  }
  const liveness: Record<string, TabLiveness> = {}

  /** 跑一次「进场」：对账 → 该重开的重开。 */
  async function open(): Promise<void> {
    await reconcileTabs(tabs.map((t) => ({ id: t.id, sessionId: t.sessionId })), ports)
    await reopenDetachedTabs(
      tabs.map((t) => ({ id: t.id, name: t.name, cwd: t.cwd, liveness: liveness[t.id] ?? 'live' })),
      {
        createSession: (tab) => {
          const id = `new-session-${++seq}`
          created.push(tab.id)
          server.add(id)
          return Promise.resolve(id)
        },
        // useCliState.adoptReopened：投递痕迹 + bindSession（后者会持久化到 workbench 配置）。
        adopt: (tabId, sessionId, notice) => {
          postReopenNotice(sessionId, notice)
          const t = tabs.find((x) => x.id === tabId)
          if (t) t.sessionId = sessionId
          liveness[tabId] = 'live'
        },
      },
    )
  }

  /** 终端连接层做的事：xterm 就绪时取走那行字写进屏幕（一次性）。 */
  function attachTerminals(): void {
    for (const t of tabs) {
      const line = t.sessionId ? takePendingTerminalNotice(t.sessionId) : null
      if (line) notices.push(line)
    }
  }

  return { tabs, ports, liveness, created, notices, open, attachTerminals }
}

describe('自动重开只打扰一次', () => {
  beforeEach(() => {
    for (let i = 1; i <= 4; i++) forgetTerminalNotice(`new-session-${i}`)
  })

  it('第一次进场：重开一次 + 留一行痕；第二次进场：什么都不建、一个字都不写', async () => {
    // 服务重启：标签还记着 dead-1，服务端只剩一个别的 session。
    const wb = workbench([{ id: 'tab-1', name: '终端 1', cwd: '/home/u/proj', sessionId: 'dead-1' }], new Set(['other']))

    await wb.open()
    wb.attachTerminals()

    expect(wb.created).toEqual(['tab-1'])
    expect(wb.notices).toHaveLength(1)
    expect(wb.notices[0]).toContain('/home/u/proj')
    // 陈旧指针已经换成新建的那个（真实实现里 bindSession 会把它持久化进 workbench 配置）。
    expect(wb.tabs[0].sessionId).toBe('new-session-1')

    // ── 第二次进场（F5 / 手机恢复标签页）：那条 session 这次是活的 ──
    await wb.open()
    wb.attachTerminals()

    expect(wb.created).toEqual(['tab-1'])   // 没有第二次新建
    expect(wb.notices).toHaveLength(1)      // 没有第二行痕
    expect(wb.liveness['tab-1']).toBe('live')
  })

  it('标记只到用户第一次输入为止（之后它连标签上都不该再出现）', async () => {
    const wb = workbench([{ id: 'tab-1', name: '终端 1', cwd: '/x', sessionId: 'dead-1' }], new Set())
    await wb.open()
    wb.attachTerminals()

    const sessionId = wb.tabs[0].sessionId as string
    expect(reopenNoticeOf(sessionId)).toBeTruthy()   // 用户还没动手 → 标签上挂着「已重开」

    noteUserInput(sessionId)
    expect(reopenNoticeOf(sessionId)).toBeUndefined() // 动过手 → 标记撤掉

    // 再进场一次也不会因为"标记没了"就重来一遍：标签绑的是活着的那条 session。
    await wb.open()
    wb.attachTerminals()
    expect(wb.created).toEqual(['tab-1'])
    expect(wb.notices).toHaveLength(1)
  })

  it('对账没问到（服务端没答）时按兵不动：不重开、不留痕、不解绑', async () => {
    const tabs: FakeTab[] = [{ id: 'tab-1', name: '终端 1', sessionId: 'maybe-alive' }]
    const liveness: Record<string, TabLiveness> = {}
    const created: string[] = []
    await reconcileTabs(tabs.map((t) => ({ id: t.id, sessionId: t.sessionId })), {
      listLocalSessions: () => Promise.resolve(null),
      listPeerSessions: () => Promise.resolve(null),
      setLiveness: (tabId, l) => { liveness[tabId] = l },
      unbindSession: (tabId) => { const t = tabs.find((x) => x.id === tabId); if (t) t.sessionId = undefined },
    })
    await reopenDetachedTabs(
      tabs.map((t) => ({ id: t.id, name: t.name, cwd: t.cwd, liveness: liveness[t.id] ?? 'live' })),
      { createSession: (t) => { created.push(t.id); return Promise.resolve('x') }, adopt: () => {} },
    )

    expect(liveness['tab-1']).toBe('unreachable')
    expect(created).toEqual([])
    expect(tabs[0].sessionId).toBe('maybe-alive')
  })
})
