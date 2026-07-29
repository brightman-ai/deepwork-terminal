import { describe, it, expect } from 'bun:test'
import {
  applySessionsOverviewFrame,
  entrySignalText,
  sessionEntry,
  sessionNoteText,
  sessionRawStatus,
  sessionSignalText,
  useSessionsOverview,
} from '../useSessionsOverview'
import { applyAgentSignalFrame } from '../useAgentSignals'

function entry(id: string, extra: Record<string, unknown> = {}) {
  return { id, title: `终端 ${id}`, cwd: `/repo/${id}`, ...extra }
}

describe('useSessionsOverview', () => {
  it('numbers cards by the tab order, not the server list order', () => {
    applySessionsOverviewFrame([entry('c'), entry('a'), entry('b')])
    const { units } = useSessionsOverview(() => 'a', () => ['a', 'b', 'c'])
    expect(units.value.map((u) => [u.key, u.index])).toEqual([['c', 3], ['a', 1], ['b', 2]])
    // The number IS the tab strip's 终端N and the prefix+N shortcut target, so it has to come
    // from the tab order or "jump to 2" lands somewhere else.
  })

  it('drops sessions that have no tab, instead of giving them a colliding number', () => {
    // Observed live on a fixture: three tabs produced pills reading "1 2 2 3 3 6" because
    // tab-less sessions fell back to their index in the server list. Such a card is also
    // unreachable — there is no tab to switch to.
    applySessionsOverviewFrame([entry('a'), entry('orphan'), entry('b')])
    const { units } = useSessionsOverview(() => 'a', () => ['a', 'b'])
    expect(units.value.map((u) => u.key)).toEqual(['a', 'b'])
    expect(units.value.map((u) => u.index)).toEqual([1, 2])
  })

  it('shows everything when the tab order is not known yet (empty ≠ "no tabs")', () => {
    // Filtering on a not-yet-hydrated order would blank the overview, which looks like a bug.
    applySessionsOverviewFrame([entry('a'), entry('b')])
    const { units } = useSessionsOverview(() => undefined, () => [])
    expect(units.value.map((u) => u.key)).toEqual(['a', 'b'])
  })

  it('carries the backend needs-you signal so the dot is dismissable', () => {
    applySessionsOverviewFrame([
      entry('a', { agentTool: 'claude', agentStatus: 'idle', awaitingUser: true, awaitingSince: '2026-07-28T10:00:00Z' }),
    ])
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    const u = units.value[0]
    expect(u.awaiting).toBe(true)
    // A dated completion is what the seen layer persists a dismissal against; hardcoding false
    // here (as this used to) meant a finished non-tmux terminal showed no "done" dot at all.
    expect(u.awaitingSince).toBe('2026-07-28T10:00:00Z')
    expect(u.signals).toEqual(['Claude 已完成'])
  })

  it('labels a turn that ended on a question differently from a plain completion', () => {
    applySessionsOverviewFrame([
      entry('a', { agentTool: 'claude', agentStatus: 'idle', awaitingUser: true, awaitingSince: 'T', endedOnQuestion: true }),
    ])
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    // Same status/severity as 已完成 — only the wording differs. Escalating this to a red
    // `waiting` is what produced undismissable dots on agents that were merely idle.
    expect(units.value[0].rawStatus).toBe('idle')
    expect(units.value[0].signals).toEqual(['Claude 有提问'])
  })

  it('an exited PTY is idle, never running', () => {
    applySessionsOverviewFrame([entry('a', { agentStatus: 'running', exited: true })])
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    expect(units.value[0].rawStatus).toBe('idle')
  })
})

/**
 * 同一个 session 只能有一个状态真相。
 *
 * 回归的是 Human 实测截到的自相矛盾：终端底部那一行写着红色「Codex 等待输入」，而**同一时刻**顶部
 * 该标签的状态点是绿色「运行中」。根因是两条判定路径 —— 标签点读 sessions_overview 这一帧，底部那行
 * 读的却是 useAgentIntel(sessionId) 的另一条推送。现在底部那行改读 sessionNoteText / sessionSignalText，
 * 和喂标签点的 `units` 同帧同推导，下面这些断言把"逐字相等"钉死。
 */
describe('状态源唯一性：标签点 vs 终端表面那一行', () => {
  const CASES: Array<{ name: string; extra: Record<string, unknown> }> = [
    { name: 'running', extra: { agentTool: 'codex', agentStatus: 'running' } },
    { name: 'waiting', extra: { agentTool: 'codex', agentStatus: 'waiting' } },
    { name: 'idle', extra: { agentTool: 'codex', agentStatus: 'idle' } },
    { name: '已完成（awaitingUser）', extra: { agentTool: 'claude', agentStatus: 'idle', awaitingUser: true, awaitingSince: 'T' } },
    { name: '有提问', extra: { agentTool: 'claude', agentStatus: 'idle', awaitingUser: true, awaitingSince: 'T', endedOnQuestion: true } },
    { name: '进程已退出但帧里还写着 running', extra: { agentTool: 'claude', agentStatus: 'running', exited: true } },
    { name: '没有 agent 的裸 shell', extra: {} },
  ]

  for (const c of CASES) {
    it(`${c.name}：两处的状态字符串逐字相等`, () => {
      applyAgentSignalFrame({ signals: [] }) // 没有显式信号，两边说的都是"我们推断的状态"
      applySessionsOverviewFrame([entry('a', c.extra), entry('b', { agentTool: 'codex', agentStatus: 'running' })])
      const { units } = useSessionsOverview(() => 'a', () => ['a', 'b'])
      const unit = units.value.find((u) => u.key === 'a')!

      // 喂标签点的那个 unit 说的话（signals[0]，没 agent 时为空）
      const dotText = unit.signals[0] ?? ''
      // 终端表面那一行说的话
      expect(sessionSignalText('a')).toBe(dotText)
      expect(entrySignalText(sessionEntry('a'))).toBe(dotText)
      // 状态本身（点的颜色由它决定）也必须是同一条推导
      expect(sessionRawStatus(sessionEntry('a'))).toBe(unit.rawStatus)
    })
  }

  it('截图里那一幕：帧说 running，底部那行绝不会写成「等待输入」', () => {
    applyAgentSignalFrame({ signals: [] })
    applySessionsOverviewFrame([entry('a', { agentTool: 'codex', agentStatus: 'running' })])
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    expect(units.value[0].rawStatus).toBe('running')
    expect(sessionNoteText('a')).toBe('Codex 运行中')
    expect(sessionNoteText('a')).not.toContain('等待输入')
  })

  it('闲着的终端一个字也不说（不留"终端"这种等于没说的占位）', () => {
    applyAgentSignalFrame({ signals: [] })
    applySessionsOverviewFrame([entry('a', { agentTool: 'codex', agentStatus: 'idle' })])
    expect(sessionNoteText('a')).toBe('')
    // 帧里根本没有这个 session 时同理 —— 空字符串，让那一格整个不渲染。
    expect(sessionNoteText('nope')).toBe('')
    expect(sessionNoteText(undefined)).toBe('')
  })

  it('agent 自己喊出原话时，那一行说的和卡片上引用的是同一句', () => {
    applySessionsOverviewFrame([entry('a', { agentTool: 'codex', agentStatus: 'idle', awaitingUser: true, awaitingSince: 'T' })])
    applyAgentSignalFrame({
      signals: [{ sessionId: 'a', kind: 'notify', title: 'Codex', body: '任务已完成', at: 'T', seq: 1 }],
    })
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    // 它说的 > 我们推断的：原话优先，但依然只有一份措辞（agentSaidText），卡片和这一行逐字相同。
    expect(sessionNoteText('a')).toBe('Codex 说：“任务已完成”')
    expect(sessionNoteText('a')).toBe(units.value[0].agentSaid)
    // 原话不改状态：点还是那个点（琥珀色的"已完成"），不因为一条 OSC 就升级成红色 waiting。
    expect(units.value[0].rawStatus).toBe('idle')
    expect(units.value[0].awaiting).toBe(true)
    applyAgentSignalFrame({ signals: [] }) // 复位，避免污染后续用例（帧是全量替换语义）
  })
})
