<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Search, X } from 'lucide-vue-next'
import type { QuickSwitchFilter, QuickSwitchFilterOption, QuickSwitchItem } from '../../composables/cli/useTabQuickSwitch'
import { matchesBinding } from '../../composables/cli/useTabShortcuts'
import PaneStatusIndicator from './PaneStatusIndicator.vue'
import TabObservationCards from './TabObservationCards.vue'
import type { OverviewUnit } from '../../composables/cli/useAgentOverview'

const props = defineProps<{
  view: 'list' | 'observe'; units: OverviewUnit[]; paneTotal: number; isMobile: boolean
  open: boolean; query: string; items: QuickSwitchItem[]; selectedId: string
  activeId?: string; binding: string; bindingLabel: string; message: string
  total: number; statusKinds: number; filter: QuickSwitchFilter; filters: QuickSwitchFilterOption[]; filterStale: boolean
}>()
const emit = defineEmits<{
  (e: 'search', query: string): void
  (e: 'move', delta: number): void
  (e: 'select', id: string): void
  (e: 'close'): void
  (e: 'view', view: 'list' | 'observe'): void
  (e: 'pane', id: string, sessionId: string): void
  (e: 'filter', filter: QuickSwitchFilter): void
  (e: 'refresh-filter'): void
}>()
const input = ref<HTMLInputElement>()
const panel = ref<HTMLElement>()
const list = ref<HTMLElement>()
const observationList = ref<HTMLElement>()
let scrollPositions = { list: 0, observe: 0 }
type SwitcherView = 'list' | 'observe'
let scrollAnchors: Partial<Record<SwitcherView, { id: string; offset: number }>> = {}
function scrollItems(view: SwitcherView): HTMLElement[] {
  const root = view === 'list' ? list.value : observationList.value
  return [...root?.querySelectorAll<HTMLElement>(view === 'list' ? '.tab-switcher__item' : '.observation-card') ?? []]
}
function rememberScroll(view: SwitcherView): void {
  const root = view === 'list' ? list.value : observationList.value
  if (!root) return
  scrollPositions[view] = root.scrollTop
  const top = root.getBoundingClientRect().top
  const item = scrollItems(view).find(item => item.getBoundingClientRect().bottom > top)
  if (item) scrollAnchors[view] = { id: item.id || item.dataset.tabId!, offset: item.getBoundingClientRect().top - top }
}
function restoreScroll(view: SwitcherView): void {
  const root = view === 'list' ? list.value : observationList.value
  if (!root) return
  root.scrollTop = scrollPositions[view]
  const anchor = scrollAnchors[view]
  const item = anchor && scrollItems(view).find(item => (item.id || item.dataset.tabId) === anchor.id)
  if (item) root.scrollTop += item.getBoundingClientRect().top - root.getBoundingClientRect().top - anchor!.offset
}
let pendingView: 'list' | 'observe' | undefined
let compositionTimer: ReturnType<typeof setTimeout> | undefined
const filterBar = ref<HTMLElement>()
const showFilters = ref(false)
const composing = ref(false)
let returnFocus: HTMLElement | null = null
let committed = false
let finishing: { code: string; kind: 'cancel' | 'select' | 'filter' | 'refresh' | 'clear' | 'view' | 'pane'; id: string; sessionId?: string } | null = null
let resizeFrame = 0
function measureFilters(): void {
  // Measure the original list. A short filtered result must never hide its controls.
  if (!props.open || props.view !== 'list' || props.query.trim() || props.filter !== 'all' || props.items.length !== props.total || !list.value) return
  const maximum = parseFloat(getComputedStyle(list.value).maxHeight) || Infinity
  const available = Math.min(maximum, list.value.clientHeight + (showFilters.value ? filterBar.value?.offsetHeight ?? 0 : 0))
  showFilters.value = props.statusKinds > 1 && list.value.scrollHeight > available + 1
}
function onResize(): void {
  cancelAnimationFrame(resizeFrame)
  resizeFrame = requestAnimationFrame(measureFilters)
}
function focusSearch(): void { if (!props.isMobile) void nextTick(() => input.value?.focus({ preventScroll: true })) }
function applyFilter(value: QuickSwitchFilter): void { emit('filter', value); focusSearch() }
function refreshFilter(): void { emit('refresh-filter'); focusSearch() }
function clearSearch(): void { emit('search', ''); void nextTick(() => input.value?.focus({ preventScroll: true })) }
function setView(view: 'list' | 'observe'): void {
  if (composing.value) { pendingView = view; input.value?.blur(); return }
  if (view === 'observe') input.value?.blur()
  emit('view', view)
}
function compositionEnd(event: CompositionEvent): void {
  composing.value = false
  emit('search', (event.target as HTMLInputElement).value)
  if (pendingView) {
    const view = pendingView
    pendingView = undefined
    compositionTimer = setTimeout(() => { if (props.open) setView(view) }, 0)
  }
}
function selectPane(id: string, sessionId: string): void { committed = true; emit('pane', id, sessionId) }
function cancel(): void { committed = false; emit('close') }
function select(id: string): void { committed = true; emit('select', id) }
watch(() => props.open, async open => {
  if (compositionTimer) clearTimeout(compositionTimer)
  pendingView = undefined
  if (open) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    committed = false
    composing.value = false
    finishing = null
    showFilters.value = false
    pendingView = undefined
    scrollPositions = { list: 0, observe: 0 }
    scrollAnchors = {}
    await nextTick()
    if (props.open) {
      measureFilters()
      if (list.value) list.value.scrollTop = 0
      if (observationList.value) observationList.value.scrollTop = 0
      ;(props.isMobile ? panel.value : input.value)?.focus({ preventScroll: true })
    }
  } else if (!committed) {
    await nextTick()
    if (!props.open && returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
  }
})
watch(() => props.selectedId, async () => {
  await nextTick()
  if (props.open) (props.view === 'list' ? list.value?.querySelector<HTMLElement>('[aria-selected="true"]') : observationList.value?.querySelector<HTMLElement>('.observation-card.selected'))?.scrollIntoView({ block: 'nearest' })
})
watch(() => [props.query, props.filter], () => {
  scrollPositions = { list: 0, observe: 0 }
  scrollAnchors = {}
  if (list.value) list.value.scrollTop = 0
  if (observationList.value) observationList.value.scrollTop = 0
})
watch(() => props.view, async (view, previous) => {
  rememberScroll(previous)
  await nextTick()
  restoreScroll(view)
})
function onKey(event: KeyboardEvent): void {
  if (!props.open) return
  // This modal owns keyboard input, including when focus was lost to the page.
  // Keep native text insertion, clipboard and IME default actions in its input.
  event.stopImmediatePropagation()
  if (event.isComposing || composing.value || event.keyCode === 229) return
  if (finishing) { event.preventDefault(); return }
  if (event.key === 'Escape' || matchesBinding(event, props.binding)) {
    event.preventDefault(); finishing = { code: event.code, kind: 'cancel', id: '' }; return
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault(); emit('move', event.key === 'ArrowDown' ? 1 : -1); return
  }
  if ((event.key === 'ArrowLeft' || event.key === 'ArrowRight') && (document.activeElement as HTMLElement | null)?.hasAttribute('data-switcher-view')) {
    event.preventDefault()
    const view = props.view === 'list' ? 'observe' : 'list'
    setView(view)
    panel.value?.querySelector<HTMLElement>(`[data-switcher-view="${view}"]`)?.focus()
    return
  }
  if (event.key === 'Enter') {
    event.preventDefault()
    const target = document.activeElement as HTMLElement | null
    const filter = target?.getAttribute('data-switcher-filter')
    const view = target?.getAttribute('data-switcher-view')
    const sessionId = target?.getAttribute('data-switcher-pane')
    const tabId = target?.getAttribute('data-switcher-tab')
    const kind = target?.hasAttribute('data-switcher-close') ? 'cancel' : filter ? 'filter'
      : target?.hasAttribute('data-switcher-refresh') ? 'refresh' : target?.hasAttribute('data-switcher-clear') ? 'clear' : view ? 'view' : sessionId ? 'pane' : 'select'
    finishing = { code: event.code, kind, id: filter ?? view ?? tabId ?? props.selectedId, sessionId: sessionId ?? undefined }
    return
  }
  if (event.key === 'Tab') {
    event.preventDefault()
    const targets = [...panel.value?.querySelectorAll<HTMLElement>('input, button:not(:disabled):not([role=option])') ?? []].filter(target => target.getClientRects().length)
    if (targets.length) {
      const current = targets.indexOf(document.activeElement as HTMLElement)
      targets[(current + (event.shiftKey ? -1 : 1) + targets.length) % targets.length]?.focus()
    }
    return
  }
  if (!panel.value?.contains(document.activeElement)) {
    event.preventDefault()
    input.value?.focus({ preventScroll: true })
  }
}
function onKeyPress(event: KeyboardEvent): void {
  if (!props.open) return
  event.stopImmediatePropagation()
  if (finishing || ['Enter', 'Escape', 'Tab'].includes(event.key)) event.preventDefault()
}
function onKeyUp(event: KeyboardEvent): void {
  if (!props.open || !finishing || event.code !== finishing.code) return
  event.preventDefault()
  event.stopImmediatePropagation()
  const action = finishing
  finishing = null
  // Keep the modal's input ownership through the entire physical key gesture,
  // including char/keypress, before returning focus to a terminal.
  if (action.kind === 'cancel') cancel()
  else if (action.kind === 'filter') {
    const option = props.filters.find(option => option.id === action.id)
    if (option) applyFilter(option.id)
  } else if (action.kind === 'refresh') refreshFilter()
  else if (action.kind === 'clear') clearSearch()
  else if (action.kind === 'view') setView(action.id as 'list' | 'observe')
  else if (action.kind === 'pane' && action.sessionId) selectPane(action.id, action.sessionId)
  else select(action.id)
}
onMounted(() => {
  window.addEventListener('keydown', onKey, { capture: true })
  window.addEventListener('keypress', onKeyPress, { capture: true })
  window.addEventListener('keyup', onKeyUp, { capture: true })
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey, { capture: true })
  window.removeEventListener('keypress', onKeyPress, { capture: true })
  window.removeEventListener('keyup', onKeyUp, { capture: true })
  window.removeEventListener('resize', onResize)
  cancelAnimationFrame(resizeFrame)
  if (compositionTimer) clearTimeout(compositionTimer)
})
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="tab-switcher-scrim" data-testid="tab-quick-switcher" @pointerdown.self.prevent="cancel">
      <section ref="panel" class="tab-switcher" role="dialog" aria-modal="true" aria-label="切换终端" tabindex="-1">
        <div class="tab-switcher__header"><strong>切换终端</strong><span>{{ total }} 个标签 · {{ paneTotal }} 个窗格</span><button type="button" data-switcher-close data-testid="tab-quick-switcher-close" aria-label="关闭快速切换" @click="cancel"><X :size="16" /></button></div>
        <div class="tab-switcher__views" role="tablist" aria-label="切换视图"><button type="button" role="tab" data-switcher-view="list" data-testid="tab-switcher-view-list" :aria-selected="view === 'list'" :aria-controls="'tab-switcher-results'" @click="setView('list')">快速切换</button><button type="button" role="tab" data-switcher-view="observe" data-testid="tab-quick-switcher-observe" :aria-selected="view === 'observe'" aria-controls="tab-switcher-observation" @click="setView('observe')">运行观察<span class="tab-switcher__live" aria-hidden="true">●</span></button></div>
        <div class="tab-switcher__search">
          <Search :size="18" aria-hidden="true" />
          <input ref="input" :value="query" role="combobox" aria-label="搜索标签名称、目录或编号" aria-autocomplete="list" aria-controls="tab-switcher-results" :aria-expanded="true" :aria-activedescendant="view === 'list' && selectedId ? `quick-tab-${selectedId}` : undefined" placeholder="搜索标签名称、目录或编号…" data-testid="tab-quick-switcher-query" autocomplete="off" @input="emit('search', ($event.target as HTMLInputElement).value)" @compositionstart="composing = true" @compositionend="compositionEnd" />
          <button v-if="query" type="button" data-switcher-clear aria-label="清空搜索" @click="clearSearch"><X :size="14" /></button>
        </div>
        <div class="tab-switcher__heading"><span>{{ query.trim() || filter !== 'all' ? `匹配标签 · ${items.length} / ${total}` : '推荐顺序' }}</span><button v-if="showFilters && filterStale" type="button" data-switcher-refresh data-testid="tab-quick-switcher-refresh" @click="refreshFilter">状态有变化，更新筛选</button><kbd>{{ bindingLabel }}</kbd></div>
        <div v-if="showFilters" ref="filterBar" class="tab-switcher__filter-section">
          <p v-if="items.some(item => (item.panes?.length ?? 0) > 1)" class="tab-switcher__filter-help">按标签计数，任一窗格符合即可</p>
          <div class="tab-switcher__filters" role="group" aria-label="按状态筛选" data-testid="tab-quick-switcher-filters">
            <button v-for="option in filters" :key="option.id" type="button" :data-switcher-filter="option.id" :data-testid="`tab-quick-switcher-filter-${option.id}`" :aria-pressed="filter === option.id" :disabled="option.id !== 'all' && !option.count" @click="applyFilter(option.id)">{{ option.label }}<span>{{ option.count }}</span></button>
          </div>
        </div>
        <div v-if="message" class="tab-switcher__message" role="status">{{ message }}</div>
        <div v-show="view === 'list'" id="tab-switcher-results" ref="list" role="listbox" class="tab-switcher__list" aria-label="终端标签">
          <button v-for="item in items" :id="`quick-tab-${item.id}`" :key="item.id" type="button" role="option" tabindex="-1" :aria-selected="item.id === selectedId" :data-testid="`tab-quick-switcher-item-${item.id}`" class="tab-switcher__item" @mousedown.prevent @click="select(item.id)">
            <span class="tab-switcher__position">{{ item.position ?? '·' }}</span>
            <span class="tab-switcher__identity"><span class="tab-switcher__name"><span class="tab-switcher__title" :title="item.name">{{ item.name }}</span><small v-if="item.id === activeId">当前</small><small v-if="item.collapsed">{{ item.group }} · 已折叠</small></span><span class="tab-switcher__path" :title="item.cwd">{{ item.cwd }}</span><span class="tab-switcher__machine" :title="`${item.machine} · ${item.engine}${item.reason ? ` · ${item.reason}` : ''}`">{{ item.machine }} · {{ item.engine }}<span v-if="item.reason" class="tab-switcher__reason"> · {{ item.reason }}</span></span></span>
            <span class="tab-switcher__detail">
              <template v-if="item.panes && item.panes.length > 1">
                <PaneStatusIndicator v-for="pane in item.panes" :key="pane.key" class="tab-switcher__status" :data-testid="`quick-pane-${item.id}-${pane.key}`" :status="pane.status" :label="pane.label" />
              </template>
              <PaneStatusIndicator v-else class="tab-switcher__status" :status="item.status" />
            </span>
          </button>
          <div v-if="!items.length" class="tab-switcher__empty" role="status">当前条件没有匹配的标签<button v-if="query" type="button" data-switcher-clear @click="clearSearch">清空搜索</button><button v-if="filter !== 'all'" type="button" data-switcher-filter="all" @click="applyFilter('all')">回到全部</button></div>
        </div>
        <div v-show="view === 'observe'" id="tab-switcher-observation" ref="observationList" class="tab-switcher__observation" role="region" aria-label="运行观察">
          <TabObservationCards v-if="view === 'observe'" :items="items" :units="units" :selected-id="selectedId" :active-id="activeId" @select="select" @pane="selectPane" />
          <div v-if="!items.length" class="tab-switcher__empty" role="status">当前条件没有匹配的标签<button v-if="query" type="button" data-switcher-clear @click="clearSearch">清空搜索</button><button v-if="filter !== 'all'" type="button" data-switcher-filter="all" @click="applyFilter('all')">回到全部</button></div>
        </div>
        <footer><span class="tab-switcher__keys">↑ ↓ 选择<span>Enter 进入</span><span>Esc 返回</span></span><span class="tab-switcher__touch">点选进入 · 关闭返回</span><span>{{ view === 'observe' ? '实时预览 · 点窗格进入' : '原编号与顶栏一致' }}</span></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.tab-switcher-scrim { position: fixed; inset: 0; z-index: 10000; background: #0007; display: flex; justify-content: center; align-items: flex-start; padding: min(15vh, 110px) 16px 24px; }
.tab-switcher { width: min(840px, 100%); height: min(690px, calc(100dvh - min(15vh, 110px) - 24px)); max-height: calc(100dvh - min(15vh, 110px) - 24px); display: flex; flex-direction: column; overflow: hidden; color: hsl(var(--foreground)); background: hsl(var(--background)); border: 1px solid hsl(var(--border)); border-radius: 12px; box-shadow: 0 24px 80px #0008; }
.tab-switcher__search { display: flex; align-items: center; gap: 12px; padding: 16px; border-bottom: 1px solid hsl(var(--border)); color: hsl(var(--muted-foreground)); }
.tab-switcher__search input { width: 100%; min-width: 0; background: transparent; color: hsl(var(--foreground)); border: 0; outline: none; font-size: 15px; }
.tab-switcher__search button { display: grid; place-items: center; min-width: 32px; min-height: 32px; border-radius: 6px; }
.tab-switcher__heading { display: flex; flex: 0 0 32px; align-items: center; justify-content: space-between; gap: 8px; padding: 0 16px; color: hsl(var(--muted-foreground)); font-size: 11px; white-space: nowrap; }
.tab-switcher__heading button { color: hsl(var(--primary)); font-size: 11px; text-decoration: underline; text-underline-offset: 3px; }
.tab-switcher__filter-section { flex-shrink: 0; }
.tab-switcher__filter-help { margin: 0; padding: 0 16px 6px; font-size: 10px; color: hsl(var(--muted-foreground)); }
.tab-switcher__filters { display: flex; flex-shrink: 0; gap: 6px; padding: 2px 16px 10px; overflow-x: auto; overscroll-behavior-x: contain; }
.tab-switcher__filters button { display: inline-flex; flex-shrink: 0; align-items: center; gap: 5px; min-height: 30px; padding: 4px 9px; border: 1px solid hsl(var(--border)); border-radius: 5px; font-size: 12px; white-space: nowrap; }
.tab-switcher__filters button[aria-pressed=true] { background: hsl(var(--accent)); border-color: hsl(var(--primary) / .65); }
.tab-switcher__filters button:disabled { opacity: .4; cursor: default; }
.tab-switcher__filters button span { font-size: 11px; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }
.tab-switcher button:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: -2px; }
kbd { font-family: inherit; font-size: 10px; opacity: .75; }
.tab-switcher__list { overflow-y: auto; min-height: 0; overscroll-behavior: contain; flex: 1; padding: 4px 6px 8px; }
.tab-switcher__item { display: flex; gap: 12px; align-items: center; width: 100%; padding: 10px; border: 1px solid transparent; border-radius: 7px; text-align: left; }
.tab-switcher__item[aria-selected=true] { background: hsl(var(--accent)); border-color: hsl(var(--border)); }
.tab-switcher__item:hover { background: hsl(var(--accent) / .65); }
.tab-switcher__position { flex: 0 0 24px; text-align: center; color: hsl(var(--muted-foreground)); font-size: 12px; font-variant-numeric: tabular-nums; }
.tab-switcher__identity { min-width: 0; flex: 1; }
.tab-switcher__name { display: flex; align-items: center; gap: 8px; font-size: 14px; }
.tab-switcher__title { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tab-switcher__name small { color: hsl(var(--muted-foreground)); font-size: 10px; white-space: nowrap; }
.tab-switcher__path { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; margin-top: 3px; color: hsl(var(--muted-foreground)); }
.tab-switcher__machine { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-top: 2px; font-size: 10px; color: hsl(var(--muted-foreground)); }
.tab-switcher__reason { font-size: inherit; color: hsl(var(--muted-foreground)); }
.tab-switcher__detail { display: grid; justify-items: end; flex-shrink: 0; gap: 5px; max-width: 140px; text-align: right; }
.tab-switcher__message, .tab-switcher__empty { padding: 16px; font-size: 13px; color: hsl(var(--muted-foreground)); }
.tab-switcher__empty { display: grid; gap: 12px; justify-items: center; padding: 32px; }
.tab-switcher__empty button { color: hsl(var(--primary)); }
footer { display: flex; justify-content: space-between; align-items: center; gap: 16px; flex-shrink: 0; padding: 10px 16px; border-top: 1px solid hsl(var(--border)); font-size: 11px; color: hsl(var(--muted-foreground)); }
@media (max-width: 600px) { .tab-switcher-scrim { padding: 14px 10px; } .tab-switcher { max-height: calc(100dvh - 28px); } .tab-switcher__detail { max-width: 112px; } .tab-switcher__name { flex-wrap: wrap; gap: 4px; } .tab-switcher__search input { font-size: 16px; } }
</style>

<style scoped>
.tab-switcher__header { display: flex; align-items: center; gap: 12px; padding: 12px 16px 8px; flex-shrink: 0; }
.tab-switcher__header strong { font-size: 15px; font-weight: 500; }
.tab-switcher__header > span { font-size: 11px; color: hsl(var(--muted-foreground)); }
.tab-switcher__header button { margin-left: auto; min-height: 36px; min-width: 36px; display: grid; place-items: center; }
.tab-switcher__views { display: flex; gap: 3px; align-self: flex-start; border: 1px solid hsl(var(--border)); border-radius: 6px; margin: 0 16px 8px; padding: 3px; flex-shrink: 0; }
.tab-switcher__views button { padding: 7px 20px; min-height: 36px; border-radius: 4px; font-size: 13px; color: hsl(var(--muted-foreground)); }
.tab-switcher__views button[aria-selected=true] { background: hsl(var(--accent)); color: hsl(var(--foreground)); }
.tab-switcher__live { font-size: 8px; color: hsl(var(--muted-foreground)); margin-left: 7px; }
.tab-switcher__search { padding: 8px 16px 12px; flex-shrink: 0; }
.tab-switcher__observation { min-height: 0; flex: 1; overflow-y: auto; overscroll-behavior: contain; padding: 4px 6px 8px; }
.tab-switcher__keys { display: flex; gap: 16px; }
.tab-switcher__touch { display: none; }
@media (max-width: 600px) {
  .tab-switcher-scrim { padding: max(12px, env(safe-area-inset-top)) 8px max(12px, env(safe-area-inset-bottom)); }
  .tab-switcher { height: calc(100dvh - 28px - env(safe-area-inset-top) - env(safe-area-inset-bottom)); }
  .tab-switcher__header { padding: 8px 12px; gap: 8px; }
  .tab-switcher__header strong { font-size: 14px; }
  .tab-switcher__header > span { font-size: 10px; }
  .tab-switcher__views { align-self: stretch; margin: 0 12px 8px; }
  .tab-switcher__views button { flex: 1; min-height: 38px; }
  .tab-switcher__search { padding: 6px 12px 10px; }
  .tab-switcher__keys { display: none; }
  .tab-switcher__touch { display: inline; }
  footer { gap: 8px; padding: 10px 12px; font-size: 10px; }
}
</style>
