import { computed, onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import type { WorkbenchGroup } from '../../types/workbench'
import { fuzzyMatch } from '../../utils/fuzzyMatch'
import { getCliApiPrefix } from './useCliApiPrefix'
import { distinctPaneStatuses, type PaneStatus, type TabPaneStatus } from './tabPanePresentation'
import { createQuickSwitchRanker, decodeQuickSwitchHistory, encodeQuickSwitchHistory, recordQuickSwitchVisit, QUICK_SWITCH_DWELL_MS, type QuickSwitchSignal, type QuickSwitchRecommendation } from './quickSwitchRanking'

export type QuickSwitchStatus = PaneStatus
export type QuickSwitchFilter = 'all' | Exclude<QuickSwitchStatus, 'terminal' | 'unknown'>
export interface QuickSwitchFilterOption { id: QuickSwitchFilter; label: string; count: number }

export interface QuickSwitchItem {
  id: string
  name: string
  cwd: string
  group: string
  machine: string
  engine: string
  position?: number
  collapsed: boolean
  status: QuickSwitchStatus
  panes?: readonly TabPaneStatus[]
  reason?: string
}

export function rankQuickSwitchItems(items: readonly QuickSwitchItem[], query: string, order: readonly string[]): QuickSwitchItem[] {
  const q = query.trim().toLowerCase()
  const index = new Map(order.map((id, i) => [id, i]))
  const score = (item: QuickSwitchItem): number => {
    if (/^[1-9]\d*$/.test(q) && String(item.position) === q) return 0
    const name = item.name.toLowerCase()
    if (name === q) return 1
    if (name.startsWith(q)) return 2
    return name.includes(q) ? 3 : 4
  }
  return items.filter(item => !q || String(item.position) === q || fuzzyMatch(q, `${item.name} ${item.cwd} ${item.group} ${item.machine}`))
    .sort((a, b) => (q ? score(a) - score(b) : 0) || (index.get(a.id) ?? order.length) - (index.get(b.id) ?? order.length))
}

export function useTabQuickSwitch(adapter: {
  open?: Ref<boolean>
  groups: () => WorkbenchGroup[]
  activeId: () => string | undefined
  positions: () => Map<string, number>
  machine: (tab: WorkbenchGroup['tabs'][number]) => string
  status: (id: string) => QuickSwitchStatus
  panes?: (id: string) => readonly TabPaneStatus[]
  select: (id: string) => void
  expand: (id: string) => void
  signals?: () => ReadonlyMap<string, readonly QuickSwitchSignal[]>
  ready?: () => boolean
  isReading?: () => boolean
  storage?: Pick<Storage, 'getItem' | 'setItem'> | null
  now?: () => number
}) {
  const open = adapter.open ?? ref(false)
  const query = ref('')
  const selectedId = ref('')
  const message = ref('')
  const recent = ref<string[]>([])
  const order = ref<string[]>([])
  const reasons = ref(new Map<string, QuickSwitchRecommendation>())
  const filter = ref<QuickSwitchFilter>('all')
  const classification = ref(new Map<string, readonly QuickSwitchStatus[]>())
  const now = adapter.now ?? Date.now
  const storageKey = `dw-quick-switch-history-v1:${getCliApiPrefix() || 'standalone'}`
  function storage() {
    try { return adapter.storage !== undefined ? adapter.storage : typeof localStorage === 'undefined' ? null : localStorage } catch { return null }
  }
  function readHistory() {
    try { return decodeQuickSwitchHistory(storage()?.getItem(storageKey) ?? null, now()) } catch { return new Map() }
  }
  const history = readHistory()
  const ranker = createQuickSwitchRanker()
  const signals = computed(() => adapter.signals?.() ?? new Map<string, readonly QuickSwitchSignal[]>())
  watch(signals, value => ranker.observe([...value.values()].flat(), now()), { immediate: true, deep: true, flush: 'sync' })
  function persist() {
    try { storage()?.setItem(storageKey, encodeQuickSwitchHistory(history, now())) } catch { /* Keep working in memory. */ }
  }
  let dwellTimer: ReturnType<typeof setTimeout> | undefined
  let pendingVisit = ''
  function cancelDwell() { if (dwellTimer !== undefined) clearTimeout(dwellTimer); dwellTimer = undefined }
  function canRead() {
    return !open.value && adapter.isReading?.() !== false && (typeof document === 'undefined' || document.visibilityState === 'visible' && document.hasFocus())
  }
  function scheduleDwell() {
    cancelDwell()
    if (!pendingVisit || adapter.activeId() !== pendingVisit || !canRead()) return
    const id = pendingVisit
    dwellTimer = setTimeout(() => {
      dwellTimer = undefined
      if (pendingVisit === id && adapter.activeId() === id && canRead()) {
        recordQuickSwitchVisit(history, id, now(), true)
        pendingVisit = ''
        persist()
      }
    }, QUICK_SWITCH_DWELL_MS)
  }
  /** Called by the existing user navigation action, never by hydration/polling. */
  function noteVisit(id: string) {
    if (adapter.activeId() !== id || !items.value.some(item => item.id === id)) return
    if (typeof document !== 'undefined' && (document.visibilityState !== 'visible' || !document.hasFocus())) return
    recordQuickSwitchVisit(history, id, now())
    pendingVisit = id
    persist()
    scheduleDwell()
  }
  watch(() => [adapter.activeId(), open.value, adapter.isReading?.()], () => {
    if (pendingVisit && adapter.activeId() !== pendingVisit) pendingVisit = ''
    scheduleDwell()
  }, { flush: 'sync' })
  onMounted(() => {
    if (typeof window === 'undefined') return
    window.addEventListener('focus', scheduleDwell)
    window.addEventListener('blur', cancelDwell)
    document.addEventListener('visibilitychange', scheduleDwell)
  })
  onBeforeUnmount(() => {
    cancelDwell()
    if (typeof window === 'undefined') return
    window.removeEventListener('focus', scheduleDwell)
    window.removeEventListener('blur', cancelDwell)
    document.removeEventListener('visibilitychange', scheduleDwell)
  })
  function visibleReason(id: string): string | undefined {
    const snapshot = reasons.value.get(id)
    if (!snapshot?.promotion) return snapshot?.reason
    // Keep the row in place, but remove a reason whose underlying event ended.
    const current = ranker.recommend(signals.value.get(id) ?? [], history.get(id), now())
    return current.promotion === snapshot.promotion ? snapshot.reason : undefined
  }
  const items = computed<QuickSwitchItem[]>(() => adapter.groups().flatMap(g => g.tabs.map(t => ({
    id: t.id, name: t.name, cwd: t.cwd, group: g.name, machine: adapter.machine(t),
    engine: t.engine, position: adapter.positions().get(t.id), collapsed: g.collapsed, status: adapter.status(t.id),
    panes: adapter.panes?.(t.id),
    reason: visibleReason(t.id),
  }))))
  watch(adapter.activeId, id => {
    if (id) recent.value = [id, ...recent.value.filter(x => x !== id)]
  }, { immediate: true, flush: 'sync' })
  watch(() => ({ ids: items.value.map(x => x.id), ready: adapter.ready?.() !== false }), ({ ids, ready }) => {
    const live = new Set(ids)
    recent.value = recent.value.filter(id => live.has(id))
    if (ready) {
      let removed = false
      for (const id of history.keys()) if (!live.has(id)) { history.delete(id); removed = true }
      if (removed) persist()
    }
    if (open.value && selectedId.value && !live.has(selectedId.value)) {
      selectedId.value = ''
      message.value = '这个标签已被关闭，请重新选择'
    }
  }, { flush: 'sync' })
  const matches = computed(() => rankQuickSwitchItems(items.value, query.value, order.value))
  const statusesOf = (item: QuickSwitchItem) => distinctPaneStatuses(item.panes?.length ? item.panes.map(pane => pane.status) : [item.status])
  const results = computed(() => matches.value.filter(item => filter.value === 'all' || classification.value.get(item.id)?.includes(filter.value)))
  const total = computed(() => items.value.length)
  const statusKinds = computed(() => new Set([...classification.value.values()].flat().filter(status => status !== 'unknown' && status !== 'terminal')).size)
  const filterStale = computed(() => items.value.length !== classification.value.size
    || items.value.some(item => classification.value.get(item.id)?.join('|') !== statusesOf(item).join('|')))
  const filters = computed<QuickSwitchFilterOption[]>(() => {
    const counts = new Map<QuickSwitchStatus, number>()
    for (const item of matches.value) {
      for (const status of classification.value.get(item.id) ?? []) counts.set(status, (counts.get(status) ?? 0) + 1)
    }
    const options: QuickSwitchFilterOption[] = [
      { id: 'all', label: '全部', count: matches.value.length },
      { id: 'waiting', label: '待输入', count: counts.get('waiting') ?? 0 },
      { id: 'done-unseen', label: '完成未读', count: counts.get('done-unseen') ?? 0 },
      { id: 'running', label: '运行中', count: counts.get('running') ?? 0 },
      { id: 'idle', label: '空闲', count: counts.get('idle') ?? 0 },
    ]
    if ([...classification.value.values()].some(statuses => statuses.includes('disconnected'))) options.push({ id: 'disconnected', label: '未连接', count: counts.get('disconnected') ?? 0 })
    return options
  })
  function classify() { classification.value = new Map(items.value.map(item => [item.id, statusesOf(item)])) }
  function refreshFilter(): void {
    classify()
    if (!results.value.some(item => item.id === selectedId.value)) selectedId.value = results.value[0]?.id ?? ''
    message.value = ''
  }
  function setFilter(value: QuickSwitchFilter): void {
    filter.value = value
    refreshFilter()
  }
  function show(): void {
    const ids = items.value.map(x => x.id)
    const active = adapter.activeId()
    // Merge other tabs' local history at the boundary, never rerank under a pointer.
    for (const [id, visit] of readHistory()) {
      if (ids.includes(id) && visit.enteredAt > (history.get(id)?.enteredAt ?? 0)) history.set(id, visit)
    }
    const time = now()
    const recommendations = new Map(ids.map(id => [id, ranker.recommend(signals.value.get(id) ?? [], history.get(id), time)]))
    reasons.value = recommendations
    const fallback = new Map([...new Set([...recent.value, ...ids])].map((id, i) => [id, i]))
    order.value = ids.filter(id => id !== active).sort((a, b) => {
      const ra = recommendations.get(a)!, rb = recommendations.get(b)!
      return ra.tier - rb.tier || rb.score - ra.score || fallback.get(a)! - fallback.get(b)!
    })
    if (active && ids.includes(active)) order.value.push(active)
    query.value = ''
    filter.value = 'all'
    classify()
    selectedId.value = results.value[0]?.id ?? ''
    message.value = ''
    open.value = true
  }
  function search(value: string): void {
    query.value = value
    classify()
    selectedId.value = results.value[0]?.id ?? ''
    message.value = ''
  }
  function move(delta: number): void {
    const list = results.value
    if (!list.length) return
    const i = list.findIndex(item => item.id === selectedId.value)
    selectedId.value = list[(i < 0 ? (delta > 0 ? 0 : list.length - 1) : (i + delta + list.length) % list.length)]!.id
    message.value = ''
  }
  function select(id = selectedId.value): boolean {
    const group = adapter.groups().find(g => g.tabs.some(t => t.id === id))
    if (!id || !group || !results.value.some(item => item.id === id)) {
      message.value = '请从当前列表选择一个标签'
      return false
    }
    if (group.collapsed) adapter.expand(group.id)
    adapter.select(id)
    open.value = false
    return true
  }
  return { open, query, selectedId, message, results, total, statusKinds, filters, filter, filterStale, setFilter, refreshFilter,
    show, search, move, select, noteVisit, close: () => { open.value = false } }
}
