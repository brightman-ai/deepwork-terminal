import { describe, it, expect, beforeEach } from 'bun:test'
import {
  reopenDetachedTabs,
  reopenNoticeLine,
  shouldAutoReopen,
  type ReopenCandidate,
  type ReopenPorts,
} from '../reopenDetached'
import {
  postTerminalNotice,
  postReopenNotice,
  takePendingTerminalNotice,
  reopenNoticeOf,
  noteUserInput,
  forgetTerminalNotice,
} from '../terminalNotice'

/**
 * 这组测试锁的是**留痕**：进程已结束的标签现在会被自动重开一个 shell，而这件事与被删掉的静默
 * 重建（服务重启后偷偷 POST /sessions 顶一个空 PTY 上去，屏幕上和「恢复成功」一模一样）唯一的
 * 区别，就是用户看得见它发生了。所以「开了新 shell」和「留下了痕迹」必须在测试里绑死：
 * 任何一次重构只要把痕迹弄丢，这里就得红。
 */
function harness(created: Record<string, string | null>) {
  const adopted: Array<{ tabId: string; sessionId: string; notice: string }> = []
  const createdWith: Array<{ id: string; name: string; cwd?: string }> = []
  const ports: ReopenPorts = {
    createSession: (tab) => {
      createdWith.push({ id: tab.id, name: tab.name, cwd: tab.cwd })
      return Promise.resolve(created[tab.id] ?? null)
    },
    adopt: (tabId, sessionId, notice) => { adopted.push({ tabId, sessionId, notice }) },
  }
  return { ports, adopted, createdWith }
}

const detachedTab = (over: Partial<ReopenCandidate> = {}): ReopenCandidate => ({
  id: 'tab-1', name: '终端 1', cwd: '/home/u/proj', liveness: 'detached', ...over,
})

describe('reopenDetachedTabs — 自动重开必须留痕', () => {
  it('接上新 shell 的同时一定交出那行说明（两件事在同一次调用里，省不掉）', async () => {
    const h = harness({ 'tab-1': 'new-session' })

    await reopenDetachedTabs([detachedTab()], h.ports)

    expect(h.adopted).toHaveLength(1)
    expect(h.adopted[0].sessionId).toBe('new-session')
    // 痕迹本身：说清「之前那个哪去了」+「现在这个是什么」+「在哪个目录」。
    expect(h.adopted[0].notice).toContain('上一个进程已随服务重启结束')
    expect(h.adopted[0].notice).toContain('新的 shell')
    expect(h.adopted[0].notice).toContain('/home/u/proj')
  })

  it('端口集合只有「开 shell」和「接上+留痕」两个口子——没有能往 PTY 里送命令的口子', () => {
    // 约束 ②「绝不执行任何命令」由端口形状保证，而不是靠调用方自觉。
    const h = harness({})
    expect(Object.keys(h.ports).sort()).toEqual(['adopt', 'createSession'])
  })

  it('新 shell 开在原来的目录、沿用原来的名字（不是把人丢回主目录）', async () => {
    const h = harness({ 'tab-1': 's1' })

    await reopenDetachedTabs([detachedTab({ cwd: '/srv/app', name: '构建' })], h.ports)

    expect(h.createdWith).toEqual([{ id: 'tab-1', name: '构建', cwd: '/srv/app' }])
  })

  it('开不成就保持原样：不 adopt、不重试、不假装成功', async () => {
    const h = harness({ 'tab-1': null })

    await reopenDetachedTabs([detachedTab()], h.ports)

    expect(h.createdWith).toHaveLength(1)
    expect(h.adopted).toEqual([])
  })

  it('unreachable 不碰（那台机器上的 agent 可能正跑着）', async () => {
    const h = harness({ 'tab-1': 's1' })
    await reopenDetachedTabs([detachedTab({ liveness: 'unreachable' })], h.ports)
    expect(h.createdWith).toEqual([])
  })

  it('远程标签不碰（不在别人机器上未经允许开进程）', async () => {
    const h = harness({ 'tab-1': 's1' })
    await reopenDetachedTabs([detachedTab({ remotePeerId: 'peer-a' })], h.ports)
    expect(h.createdWith).toEqual([])
  })

  it('live 的标签当然不碰', async () => {
    const h = harness({ 'tab-1': 's1' })
    await reopenDetachedTabs([detachedTab({ liveness: 'live' })], h.ports)
    expect(h.createdWith).toEqual([])
  })

  it('shouldAutoReopen 的四档判定（判定与动作分开，这里单独钉住）', () => {
    expect(shouldAutoReopen(detachedTab())).toBe(true)
    expect(shouldAutoReopen(detachedTab({ liveness: 'live' }))).toBe(false)
    expect(shouldAutoReopen(detachedTab({ liveness: 'unreachable' }))).toBe(false)
    expect(shouldAutoReopen(detachedTab({ remotePeerId: 'p' }))).toBe(false)
  })
})

describe('reopenNoticeLine — 那行字本身', () => {
  it('知道目录就说切回了哪里', () => {
    expect(reopenNoticeLine('/home/u/proj')).toContain('已切回 /home/u/proj')
  })

  it('只知道是主目录时不硬凑一个路径', () => {
    expect(reopenNoticeLine('~')).toContain('已回到主目录 ~')
    expect(reopenNoticeLine()).toContain('已回到主目录 ~')
  })

  it('独占一行（前后换行），不会挤在上一段输出屁股后面', () => {
    const line = reopenNoticeLine('/x')
    expect(line.startsWith('\r\n')).toBe(true)
    expect(line.endsWith('\r\n')).toBe(true)
  })
})

describe('terminalNotice 投递箱 — 那行字怎么送到屏幕上', () => {
  // 每条测试用自己的 session id，跨测试不共享状态；仍显式清一遍，避免顺序依赖。
  beforeEach(() => { forgetTerminalNotice('s-a'); forgetTerminalNotice('s-b') })

  it('那行字只写一次：取走即消费，重连不会再冒出来一遍', () => {
    postReopenNotice('s-a', '\r\n[已重开]\r\n')
    expect(takePendingTerminalNotice('s-a')).toBe('\r\n[已重开]\r\n')
    expect(takePendingTerminalNotice('s-a')).toBeNull()
  })

  it('字写完了，标记还挂着（标签栏还得看得见是哪几个）', () => {
    postReopenNotice('s-a', 'x')
    takePendingTerminalNotice('s-a')
    expect(reopenNoticeOf('s-a')).toBe('x')
  })

  it('用户在这个终端里输入 → 标记消失，且不会再把字写第二遍', () => {
    postReopenNotice('s-a', 'x')
    noteUserInput('s-a')
    expect(reopenNoticeOf('s-a')).toBeUndefined()
    expect(takePendingTerminalNotice('s-a')).toBeNull()
  })

  it('别的终端里的输入不会撤掉这个终端的标记', () => {
    postReopenNotice('s-a', 'x')
    noteUserInput('s-b')
    expect(reopenNoticeOf('s-a')).toBe('x')
  })

  it('没被重开过的 session 什么都拿不到（默认零噪音）', () => {
    expect(reopenNoticeOf('s-b')).toBeUndefined()
    expect(takePendingTerminalNotice('s-b')).toBeNull()
    expect(reopenNoticeOf(undefined)).toBeUndefined()
  })

  it('标签关掉后不留残余', () => {
    postReopenNotice('s-a', 'x')
    forgetTerminalNotice('s-a')
    expect(reopenNoticeOf('s-a')).toBeUndefined()
    expect(takePendingTerminalNotice('s-a')).toBeNull()
  })
})

describe('写字 与 挂「已重开」小标 是两件事', () => {
  beforeEach(() => { forgetTerminalNotice('s-a'); forgetTerminalNotice('s-b') })

  it('只写字（postTerminalNotice）：字照样送到，但标签上绝不冒出「已重开」', () => {
    // 这是 pro 进场落到别的终端时走的入口——那个终端并没有被重开，点亮小标会是新的一句谎。
    postTerminalNotice('s-a', '\r\n[你要找的那个已随重启结束，这里是另一个]\r\n')

    expect(takePendingTerminalNotice('s-a')).toContain('已随重启结束')
    expect(reopenNoticeOf('s-a')).toBeUndefined()
  })

  it('重开（postReopenNotice）= 先写字，再挂标记（两件事都做）', () => {
    postReopenNotice('s-b', 'x')
    expect(takePendingTerminalNotice('s-b')).toBe('x')
    expect(reopenNoticeOf('s-b')).toBe('x')
  })

  it('只写字的那行同样只写一次，且用户动手后不再补写', () => {
    postTerminalNotice('s-a', 'x')
    expect(takePendingTerminalNotice('s-a')).toBe('x')
    expect(takePendingTerminalNotice('s-a')).toBeNull()

    postTerminalNotice('s-b', 'y')
    noteUserInput('s-b')
    expect(takePendingTerminalNotice('s-b')).toBeNull()
  })

  it('空 session / 空文案一律不投递（不给不存在的终端排队）', () => {
    postTerminalNotice('', 'x')
    postTerminalNotice('s-a', '')
    expect(takePendingTerminalNotice('s-a')).toBeNull()
    expect(takePendingTerminalNotice('')).toBeNull()
  })
})
