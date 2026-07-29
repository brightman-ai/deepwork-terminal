import { describe, it, expect, beforeEach } from 'bun:test'
import { ref } from 'vue'
import { applyAgentSignalFrame, useAgentSignals } from '../useAgentSignals'
import { agentSaidText, useOverviewUnits } from '../useAgentOverview'
import { applySessionsOverviewFrame, useSessionsOverview } from '../useSessionsOverview'

const { signalFor } = useAgentSignals()

/** One wire entry, defaults matching what the backend sends for an OSC notification. */
function sig(sessionId: string, extra: Record<string, unknown> = {}) {
  return { sessionId, kind: 'notify', title: '', body: '', at: '2026-07-29T12:34:56.789Z', seq: 1, ...extra }
}

// Both stores are module-level singletons (one frame describes every session), so each test
// starts from a known-empty set instead of inheriting the previous one's.
beforeEach(() => {
  applyAgentSignalFrame({ signals: [] })
  applySessionsOverviewFrame([])
})

describe('applyAgentSignalFrame', () => {
  it('keeps the words the agent sent, not just the fact that it rang', () => {
    applyAgentSignalFrame({ signals: [sig('s1', { title: 'Codex', body: '任务已完成', seq: 3 })] })
    const s = signalFor('s1')
    expect(s?.title).toBe('Codex')
    expect(s?.body).toBe('任务已完成')
    expect(s?.kind).toBe('notify')
    expect(s?.seq).toBe(3)
  })

  it('treats every frame as the COMPLETE set — an empty one clears', () => {
    applyAgentSignalFrame({ signals: [sig('s1', { body: 'a' }), sig('s2', { body: 'b' })] })
    applyAgentSignalFrame({ signals: [sig('s2', { body: 'b' })] })
    expect(signalFor('s1')).toBeUndefined()
    expect(signalFor('s2')?.body).toBe('b')
    // Disappearance follows the server, full stop — there is no client-side "read" ledger that
    // could keep showing (or hide) something the server no longer lists.
    applyAgentSignalFrame({ signals: [] })
    expect(signalFor('s2')).toBeUndefined()
  })

  it('ignores a malformed payload instead of silently clearing a live signal', () => {
    applyAgentSignalFrame({ signals: [sig('s1', { body: '需要你的授权' })] })
    applyAgentSignalFrame(null)
    applyAgentSignalFrame({})
    applyAgentSignalFrame('nonsense')
    expect(signalFor('s1')?.body).toBe('需要你的授权')
  })

  it('flattens multi-line text and caps a runaway body (it lands in one-line rows)', () => {
    applyAgentSignalFrame({ signals: [sig('s1', { body: '第一行\n  第二行\t尾' }), sig('s2', { body: 'x'.repeat(400) })] })
    expect(signalFor('s1')?.body).toBe('第一行 第二行 尾')
    const long = signalFor('s2')!.body
    expect(long.length).toBe(160)
    expect(long.endsWith('…')).toBe(true)
  })

  it('drops an entry with no session id (nothing could ever show it)', () => {
    applyAgentSignalFrame({ signals: [sig(''), sig('s1', { body: 'ok' })] })
    expect(Object.keys(useAgentSignals().signals.value)).toEqual(['s1'])
  })
})

describe('agentSaidText', () => {
  it('quotes the body and attributes it to the detected engine', () => {
    expect(agentSaidText({ title: 'Codex', body: '任务已完成' }, 'codex')).toBe('Codex 说：“任务已完成”')
  })

  it('falls back to the notification title when no engine was detected', () => {
    // OSC 777 title is the emitting app; with no detected tool it is still a real attribution.
    expect(agentSaidText({ title: 'Claude Code', body: '需要你的授权' }, '')).toBe('Claude Code 说：“需要你的授权”')
  })

  it('quotes a title-only notification', () => {
    expect(agentSaidText({ title: '构建失败' }, 'claude')).toBe('Claude 说：“构建失败”')
  })

  it('names the terminal when nothing identifies the speaker', () => {
    // 中文说话人不补空格（"终端说"），拉丁名才补（"Codex 说"）。
    expect(agentSaidText({ body: 'Claude 需要你确认一个操作' })).toBe('终端说：“Claude 需要你确认一个操作”')
  })

  it('says NOTHING for a bell — no empty quotes, no "undefined"', () => {
    // A bare BEL carries no text; inventing one would be the exact fabrication this path exists
    // to replace. The caller degrades to the existing agentSignalText wording instead.
    expect(agentSaidText({})).toBe('')
    expect(agentSaidText(undefined, 'claude')).toBe('')
    expect(agentSaidText({ title: '   ', body: '' }, 'codex')).toBe('')
  })
})

describe('signals on the overview cards', () => {
  it('puts the agent’s own words on the card, keyed to that session only', () => {
    applySessionsOverviewFrame([
      { id: 'a', agentTool: 'codex', agentStatus: 'idle', awaitingUser: true, awaitingSince: '2026-07-29T12:34:56.789Z' },
      { id: 'b', agentTool: 'claude', agentStatus: 'running' },
    ])
    applyAgentSignalFrame({ signals: [sig('a', { title: 'Codex', body: '任务已完成' })] })
    const { units } = useSessionsOverview(() => 'a', () => ['a', 'b'])
    expect(units.value[0].agentSaid).toBe('Codex 说：“任务已完成”')
    expect(units.value[1].agentSaid).toBe('')
  })

  it('a bell leaves the card on its existing wording (no empty quote line)', () => {
    applySessionsOverviewFrame([
      { id: 'a', agentTool: 'claude', agentStatus: 'idle', awaitingUser: true, awaitingSince: 'T' },
    ])
    applyAgentSignalFrame({ signals: [sig('a', { kind: 'bell' })] })
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    expect(units.value[0].agentSaid).toBe('')
    expect(units.value[0].signals).toEqual(['Claude 已完成'])
  })

  it('the words vanish with the server’s set, not on a client-side timer', () => {
    applySessionsOverviewFrame([{ id: 'a', agentTool: 'claude' }])
    applyAgentSignalFrame({ signals: [sig('a', { body: '需要你的授权' })] })
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    expect(units.value[0].agentSaid).toBe('Claude 说：“需要你的授权”')
    applyAgentSignalFrame({ signals: [] })
    expect(units.value[0].agentSaid).toBe('')
  })

  it('NEVER escalates a signalled session to the red, blocked status', () => {
    // 语义红线：铃分不清"要授权"还是"跑完了"。后端把它落在 awaitingUser（琥珀 done-unseen）上，
    // 前端不许因为多了一句原话就把它读成 waiting（红 = 被阻塞）。
    applySessionsOverviewFrame([
      { id: 'a', agentTool: 'codex', agentStatus: 'idle', awaitingUser: true, awaitingSince: '2026-07-29T12:34:56.789Z' },
    ])
    applyAgentSignalFrame({ signals: [sig('a', { title: 'Codex', body: '需要你确认一个操作' })] })
    const { units } = useSessionsOverview(() => 'a', () => ['a'])
    expect(units.value[0].rawStatus).toBe('idle')
    const { effectiveStatus } = useOverviewUnits(ref(units.value), ref(true))
    expect(effectiveStatus(units.value[0])).toBe('done-unseen')
  })
})
