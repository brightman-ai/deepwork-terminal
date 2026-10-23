import { describe, expect, it } from 'bun:test'
import { createQuickSwitchRanker, decodeQuickSwitchHistory, encodeQuickSwitchHistory, recordQuickSwitchVisit, type QuickSwitchHistory, type QuickSwitchSignal } from '../quickSwitchRanking'

const NOW = Date.UTC(2026, 9, 23, 4)
const MINUTE = 60_000, DAY = 86400_000
const signal = (key: string, rawStatus: QuickSwitchSignal['rawStatus'], extra: Partial<QuickSwitchSignal> = {}): QuickSwitchSignal => ({
  key, rawStatus, effectiveStatus: rawStatus, awaitingSince: 0, activityAt: 0, ...extra,
})
describe('quick switch recommendation facts', () => {
  it('prioritizes fresh waiting and unread completion over a highly used tab, then yields once viewed', () => {
    const ranker = createQuickSwitchRanker()
    const waiting = signal('wait', 'waiting', { awaitingSince: NOW - 1000 })
    const finished = signal('done', 'idle', { effectiveStatus: 'done-unseen', awaitingSince: NOW - 2000 })
    ranker.observe([waiting, finished], NOW)
    expect(ranker.recommend([waiting], undefined, NOW).tier).toBe(0)
    expect(ranker.recommend([finished], undefined, NOW)).toMatchObject({ tier: 1, reason: '尚未查看' })
    const visit = { enteredAt: NOW, countedAt: NOW, days: [[Math.floor(NOW / DAY), 1000] as [number, number]] }
    expect(ranker.recommend([], visit, NOW).tier).toBe(2)
    expect(ranker.recommend([waiting], visit, NOW).tier).toBe(2)
    expect(waiting.rawStatus).toBe('waiting')
    expect(ranker.recommend([finished], undefined, NOW + 31 * MINUTE).tier).toBe(2)
  })
  it('does not invent a completion on initial/reconnected idle or infer completion from a running→idle edge', () => {
    const ranker = createQuickSwitchRanker()
    ranker.observe([signal('a', 'idle')], NOW)
    expect(ranker.recommend([signal('a', 'idle')], undefined, NOW).tier).toBe(2)
    ranker.observe([signal('a', 'running')], NOW + 1)
    ranker.observe([signal('a', 'idle')], NOW + 2)
    expect(ranker.recommend([signal('a', 'idle')], undefined, NOW + 2)).toMatchObject({ tier: 1, reason: '刚结束运行' })
    ranker.observe([], NOW + 3)
    ranker.observe([signal('a', 'idle')], NOW + 4)
    expect(ranker.recommend([signal('a', 'idle')], undefined, NOW + 4).tier).toBe(2)
  })
  it('shares the completion-seen outcome, does not replay frames, and gives a new dated turn its own promotion', () => {
    const ranker = createQuickSwitchRanker()
    const done = signal('a', 'idle', { effectiveStatus: 'done-unseen', awaitingSince: NOW - 1000 })
    ranker.observe([done], NOW)
    const seen = { ...done, effectiveStatus: 'idle' as const }
    ranker.observe([seen], NOW + 1)
    expect(ranker.recommend([seen], undefined, NOW + 1).tier).toBe(2)
    for (let i = 0; i < 10; i++) ranker.observe([done], NOW + i)
    const visit = { enteredAt: NOW, countedAt: 0, days: [] }
    expect(ranker.recommend([done], visit, NOW + 10).tier).toBe(2)
    const next = { ...done, awaitingSince: NOW + 20 }
    ranker.observe([next], NOW + 21)
    expect(ranker.recommend([next], visit, NOW + 21).tier).toBe(1)
  })
  it('uses distinct session identities in split tabs and bounds output activity below recent user work', () => {
    const ranker = createQuickSwitchRanker()
    const primary = signal('primary', 'running', { activityAt: NOW })
    const secondary = signal('secondary', 'waiting', { awaitingSince: NOW - 100 })
    ranker.observe([primary, secondary], NOW)
    expect(ranker.recommend([primary, secondary], undefined, NOW).tier).toBe(0)
    const recent = { enteredAt: NOW - MINUTE, countedAt: 0, days: [] }
    expect(ranker.recommend([], recent, NOW).score).toBeGreaterThan(ranker.recommend([primary], undefined, NOW).score)
    expect(ranker.recommend([signal('new-session', 'idle')], undefined, NOW).tier).toBe(2)
  })
  it('lets decaying frequency help older ties without making last week permanently dominant', () => {
    const ranker = createQuickSwitchRanker(), day = Math.floor(NOW / DAY)
    const common = { enteredAt: NOW - 3 * 60 * MINUTE, countedAt: NOW, days: [[day, 15] as [number, number]] }
    const once = { enteredAt: NOW - 2 * 60 * MINUTE, countedAt: NOW, days: [[day, 1] as [number, number]] }
    expect(ranker.recommend([], common, NOW).score).toBeGreaterThan(ranker.recommend([], once, NOW).score)
    expect(ranker.recommend([], common, NOW).reason).toBe('最近常用')
    const fresh = { enteredAt: NOW - MINUTE, countedAt: 0, days: [] }
    expect(ranker.recommend([], fresh, NOW).score).toBeGreaterThan(ranker.recommend([], common, NOW).score)
    expect(ranker.recommend([], common, NOW + 8 * DAY).score).toBeLessThan(0.01)
  })
})

describe('local visits across refresh', () => {
  it('keeps immediate recency separate from qualified counts and debounces repeated visits', () => {
    const history: QuickSwitchHistory = new Map()
    recordQuickSwitchVisit(history, 'tab', NOW)
    expect(history.get('tab')?.days).toEqual([])
    recordQuickSwitchVisit(history, 'tab', NOW + 2000, true)
    recordQuickSwitchVisit(history, 'tab', NOW + 3000)
    recordQuickSwitchVisit(history, 'tab', NOW + 5000, true)
    expect(history.get('tab')?.enteredAt).toBe(NOW + 3000)
    expect(history.get('tab')?.days).toEqual([[Math.floor(NOW / DAY), 1]])
    recordQuickSwitchVisit(history, 'tab', NOW + 33000, true)
    expect(history.get('tab')?.days).toEqual([[Math.floor(NOW / DAY), 2]])
    expect(decodeQuickSwitchHistory(encodeQuickSwitchHistory(history, NOW + 33000), NOW + 34000).get('tab')).toEqual(history.get('tab'))
  })
  it('drops expired/corrupt/future data and treats special object keys as ordinary tab identities', () => {
    expect(decodeQuickSwitchHistory('{bad', NOW).size).toBe(0)
    const raw = JSON.stringify({ version: 1, tabs: [
      ['old', { enteredAt: NOW - 8 * DAY, days: [] }], ['future', { enteredAt: NOW + DAY }],
      ['__proto__', { enteredAt: NOW - MINUTE, countedAt: NOW - MINUTE, days: [null, ['bad', 10], [Math.floor(NOW / DAY), 3], [Math.floor(NOW / DAY) - 10, 500]] }],
    ] })
    const h = decodeQuickSwitchHistory(raw, NOW)
    expect([...h.keys()]).toEqual(['__proto__'])
    expect(h.get('__proto__')?.days).toEqual([[Math.floor(NOW / DAY), 3]])
  })
})
