import { createStatusEdgeDetector } from './statusEdgeDetector'
import type { EffectiveStatus } from './useAgentOverview'

const MINUTE = 60_000
const DAY = 24 * 60 * MINUTE
export const QUICK_SWITCH_HISTORY_DAYS = 7
export const QUICK_SWITCH_DWELL_MS = 2000
export const QUICK_SWITCH_COUNT_GAP_MS = 30_000

/** Structured facts from the existing overview; never infer state from UI labels. */
export interface QuickSwitchSignal {
  key: string
  rawStatus: 'running' | 'waiting' | 'idle'
  effectiveStatus: EffectiveStatus
  awaitingSince: number
  activityAt: number
}
export interface QuickSwitchVisit {
  enteredAt: number
  countedAt: number
  days: [number, number][]
}
export type QuickSwitchHistory = Map<string, QuickSwitchVisit>
export interface QuickSwitchRecommendation { tier: number; score: number; reason: string; promotion?: Change['kind'] }
type Change = { at: number; kind: 'waiting' | 'finished' | 'stopped' }

function validTime(value: unknown, now: number): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 && value <= now
}
function dayOf(now: number): number { return Math.floor(now / DAY) }
function liveDays(days: [number, number][], now: number): [number, number][] {
  const today = dayOf(now)
  return days.filter(([day, count]) => Number.isInteger(day) && day > today - QUICK_SWITCH_HISTORY_DAYS && day <= today && Number.isInteger(count) && count > 0)
}
export function decodeQuickSwitchHistory(raw: string | null, now: number): QuickSwitchHistory {
  const history: QuickSwitchHistory = new Map()
  try {
    const data = JSON.parse(raw || 'null')
    if (data?.version !== 1 || !Array.isArray(data.tabs)) return history
    for (const entry of data.tabs.slice(0, 1000)) {
      if (!Array.isArray(entry) || typeof entry[0] !== 'string' || !entry[1]) continue
      const [id, visit] = entry
      if (!validTime(visit.enteredAt, now) || now - visit.enteredAt >= QUICK_SWITCH_HISTORY_DAYS * DAY) continue
      const days = Array.isArray(visit.days) ? liveDays(visit.days.filter((x: unknown) => Array.isArray(x) && x.length === 2), now) : []
      history.set(id, { enteredAt: visit.enteredAt, countedAt: validTime(visit.countedAt, now) ? visit.countedAt : 0, days })
    }
  } catch { /* Unavailable/old/corrupt history must not prevent switching. */ }
  return history
}
export function encodeQuickSwitchHistory(history: QuickSwitchHistory, now: number): string {
  return JSON.stringify({ version: 1, tabs: [...history].filter(([, v]) => now - v.enteredAt < QUICK_SWITCH_HISTORY_DAYS * DAY)
    .sort((a, b) => b[1].enteredAt - a[1].enteredAt).slice(0, 1000)
    .map(([id, v]) => [id, { ...v, days: liveDays(v.days, now) }]) })
}
/** Enter-time recency and qualified dwell counts are intentionally different facts. */
export function recordQuickSwitchVisit(history: QuickSwitchHistory, id: string, now: number, qualified = false): void {
  const previous = history.get(id)
  const visit: QuickSwitchVisit = { enteredAt: qualified ? previous?.enteredAt ?? now : now, countedAt: previous?.countedAt ?? 0, days: liveDays(previous?.days ?? [], now) }
  if (qualified && (!visit.countedAt || now - visit.countedAt >= QUICK_SWITCH_COUNT_GAP_MS)) {
    const today = dayOf(now)
    const count = visit.days.find(row => row[0] === today)
    if (count) count[1] = Math.min(1000, count[1] + 1)
    else visit.days.push([today, 1])
    visit.countedAt = now
  }
  history.set(id, visit)
}

/** Owns ranking promotions, not agent state or a second completion-seen layer. */
export function createQuickSwitchRanker() {
  const edges = createStatusEdgeDetector<QuickSwitchSignal['rawStatus']>()
  const changes = new Map<string, Change>()
  function observe(signals: readonly QuickSwitchSignal[], now: number): void {
    const raw = new Map(signals.map(s => [s.key, s.rawStatus]))
    const changed = new Map(edges.diff(raw).map(edge => [edge.key, edge]))
    const live = new Set(raw.keys())
    for (const key of changes.keys()) if (!live.has(key)) changes.delete(key)
    for (const signal of signals) {
      const edge = changed.get(signal.key)
      // A dated backend event survives refresh. Unknown-age initial states do not
      // become fake "just finished" events on initial load/reconnect.
      const dated = validTime(signal.awaitingSince, now)
      const observedEdge = edge?.from !== undefined
      if (signal.rawStatus === 'running') continue
      const kind = signal.rawStatus === 'waiting' ? 'waiting'
        : signal.effectiveStatus === 'done-unseen' ? 'finished' : 'stopped'
      if (dated && (kind === 'waiting' || kind === 'finished')) {
        changes.set(signal.key, { at: signal.awaitingSince, kind })
      } else if (observedEdge && (signal.rawStatus === 'waiting' || edge?.from === 'running')) {
        const previous = changes.get(signal.key)
        // Polling/brief state jitter must not refresh the same promotion every frame.
        if (!previous || previous.kind !== kind || now - previous.at >= 5000) changes.set(signal.key, { at: now, kind })
      }
    }
  }
  function recommend(signals: readonly QuickSwitchSignal[], visit: QuickSwitchVisit | undefined, now: number): QuickSwitchRecommendation {
    let attention: QuickSwitchRecommendation | undefined
    for (const signal of signals) {
      if (signal.rawStatus === 'running') continue
      const event = changes.get(signal.key)
      if (!event || event.at <= (visit?.enteredAt ?? 0) || now - event.at >= 30 * MINUTE) continue
      // Shared seen-state dismisses completions. Raw idle transitions remain honest
      // "ended running" hints; visiting never rewrites a still-waiting agent.
      if (event.kind === 'finished' && signal.effectiveStatus !== 'done-unseen') continue
      if (event.kind === 'waiting' && signal.rawStatus !== 'waiting') continue
      const next = { tier: event.kind === 'waiting' ? 0 : 1, score: event.at, promotion: event.kind,
        reason: event.kind === 'waiting' ? '等待你处理' : event.kind === 'finished' ? '尚未查看' : '刚结束运行' }
      if (!attention || next.tier < attention.tier || next.tier === attention.tier && next.score > attention.score) attention = next
    }
    if (attention) return attention
    const age = visit && validTime(visit.enteredAt, now) ? now - visit.enteredAt : Infinity
    const recency = 60 * Math.exp(-age / (30 * MINUTE))
    const frequency = visit ? liveDays(visit.days, now).reduce((sum, [day, count]) => sum + Math.min(count, 20) * Math.exp(-(dayOf(now) - day) / 3), 0) : 0
    const frequent = 20 * (1 - Math.exp(-frequency / 5))
    const activityAt = Math.max(0, ...signals.map(s => validTime(s.activityAt, now) ? s.activityAt : 0))
    const activity = activityAt ? 6 * Math.exp(-(now - activityAt) / (10 * MINUTE)) : 0
    let reason = ''
    if (recency >= Math.max(frequent, activity) && age < DAY) reason = age < MINUTE ? '刚刚查看' : age < 60 * MINUTE ? `${Math.floor(age / MINUTE)}分钟前查看` : '最近查看'
    else if (frequent >= activity && frequency >= 2) reason = '最近常用'
    else if (activity >= 1) reason = '最近有活动'
    return { tier: 2, score: recency + frequent + activity, reason }
  }
  return { observe, recommend }
}
