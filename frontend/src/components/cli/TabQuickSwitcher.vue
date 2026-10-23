<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CheckCircle2, Search, Unplug, X } from 'lucide-vue-next'
import type { QuickSwitchFilter, QuickSwitchFilterOption, QuickSwitchItem, QuickSwitchStatus } from '../../composables/cli/useTabQuickSwitch'
import { matchesBinding } from '../../composables/cli/useTabShortcuts'
import { STATUS_COLOR, STATUS_MOTION } from '../../composables/cli/useAgentOverview'

const statusLabels: Record<QuickSwitchStatus, string> = {
  waiting: '等待输入', running: '运行中', 'done-unseen': '已完成',
  idle: '空闲', disconnected: '未连接', terminal: '终端',
}

const props = defineProps<{
  open: boolean; query: string; items: QuickSwitchItem[]; selectedId: string
  activeId?: string; binding: string; bindingLabel: string; message: string
  total: number; statusKinds: number; filter: QuickSwitchFilter; filters: QuickSwitchFilterOption[]; filterStale: boolean
}>()
const emit = defineEmits<{
  (e: 'search', query: string): void
  (e: 'move', delta: number): void
  (e: 'select', id: string): void
  (e: 'close'): void
  (e: 'filter', filter: QuickSwitchFilter): void
  (e: 'refresh-filter'): void
}>()
const input = ref<HTMLInputElement>()
const panel = ref<HTMLElement>()
const list = ref<HTMLElement>()
const filterBar = ref<HTMLElement>()
const showFilters = ref(false)
const composing = ref(false)
let returnFocus: HTMLElement | null = null
let committed = false
let finishing: { code: string; kind: 'cancel' | 'select' | 'filter' | 'refresh' | 'clear'; id: string } | null = null
let resizeFrame = 0
function measureFilters(): void {
  // Measure the original list. A short filtered result must never hide its controls.
  if (!props.open || props.query.trim() || props.filter !== 'all' || props.items.length !== props.total || !list.value) return
  const maximum = parseFloat(getComputedStyle(list.value).maxHeight) || Infinity
  const available = Math.min(maximum, list.value.clientHeight + (showFilters.value ? filterBar.value?.offsetHeight ?? 0 : 0))
  showFilters.value = props.statusKinds > 1 && list.value.scrollHeight > available + 1
}
function onResize(): void {
  cancelAnimationFrame(resizeFrame)
  resizeFrame = requestAnimationFrame(measureFilters)
}
function applyFilter(value: QuickSwitchFilter): void { emit('filter', value); void nextTick(() => input.value?.focus({ preventScroll: true })) }
function refreshFilter(): void { emit('refresh-filter'); void nextTick(() => input.value?.focus({ preventScroll: true })) }
function clearSearch(): void { emit('search', ''); void nextTick(() => input.value?.focus({ preventScroll: true })) }
function cancel(): void { committed = false; emit('close') }
function select(id: string): void { committed = true; emit('select', id) }
watch(() => props.open, async open => {
  if (open) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    committed = false
    composing.value = false
    finishing = null
    showFilters.value = false
    await nextTick()
    if (props.open) { measureFilters(); input.value?.focus({ preventScroll: true }) }
  } else if (!committed) {
    await nextTick()
    if (!props.open && returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
  }
})
watch(() => props.selectedId, async () => {
  await nextTick()
  if (props.open) panel.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
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
  if (event.key === 'Enter') {
    event.preventDefault()
    const target = document.activeElement as HTMLElement | null
    const filter = target?.getAttribute('data-switcher-filter')
    const kind = target?.hasAttribute('data-switcher-close') ? 'cancel' : filter ? 'filter'
      : target?.hasAttribute('data-switcher-refresh') ? 'refresh' : target?.hasAttribute('data-switcher-clear') ? 'clear' : 'select'
    finishing = { code: event.code, kind, id: filter ?? props.selectedId }
    return
  }
  if (event.key === 'Tab') {
    event.preventDefault()
    const targets = [...panel.value?.querySelectorAll<HTMLElement>('input, button:not(:disabled):not([role=option])') ?? []]
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
})
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="tab-switcher-scrim" data-testid="tab-quick-switcher" @pointerdown.self.prevent="cancel">
      <section ref="panel" class="tab-switcher" role="dialog" aria-modal="true" aria-label="快速切换标签">
        <div class="tab-switcher__search">
          <Search :size="18" aria-hidden="true" />
          <input ref="input" :value="query" role="combobox" aria-label="搜索标签名称、目录或编号" aria-autocomplete="list" aria-controls="tab-switcher-results" :aria-expanded="true" :aria-activedescendant="selectedId ? `quick-tab-${selectedId}` : undefined" placeholder="搜索标签名称、目录或编号…" data-testid="tab-quick-switcher-query" autocomplete="off" @input="emit('search', ($event.target as HTMLInputElement).value)" @compositionstart="composing = true" @compositionend="composing = false" />
          <button type="button" data-switcher-close data-testid="tab-quick-switcher-close" aria-label="关闭快速切换" @click="cancel"><X :size="16" /></button>
        </div>
        <div class="tab-switcher__heading"><span>{{ query.trim() || filter !== 'all' ? `匹配标签 · ${items.length} / ${total}` : '推荐顺序' }}</span><button v-if="showFilters && filterStale" type="button" data-switcher-refresh data-testid="tab-quick-switcher-refresh" @click="refreshFilter">状态有变化，更新筛选</button><kbd>{{ bindingLabel }}</kbd></div>
        <div v-if="showFilters" ref="filterBar" class="tab-switcher__filters" role="group" aria-label="按状态筛选" data-testid="tab-quick-switcher-filters">
          <button v-for="option in filters" :key="option.id" type="button" :data-switcher-filter="option.id" :data-testid="`tab-quick-switcher-filter-${option.id}`" :aria-pressed="filter === option.id" :disabled="option.id !== 'all' && !option.count" @click="applyFilter(option.id)">{{ option.label }}<span>{{ option.count }}</span></button>
        </div>
        <div v-if="message" class="tab-switcher__message" role="status">{{ message }}</div>
        <div id="tab-switcher-results" ref="list" role="listbox" class="tab-switcher__list" aria-label="终端标签">
          <button v-for="item in items" :id="`quick-tab-${item.id}`" :key="item.id" type="button" role="option" tabindex="-1" :aria-selected="item.id === selectedId" :data-testid="`tab-quick-switcher-item-${item.id}`" class="tab-switcher__item" @mousedown.prevent @click="select(item.id)">
            <span class="tab-switcher__position">{{ item.position ?? '·' }}</span>
            <span class="tab-switcher__identity"><span class="tab-switcher__name"><span class="tab-switcher__title" :title="item.name">{{ item.name }}</span><small v-if="item.id === activeId">当前</small><small v-if="item.collapsed">{{ item.group }} · 已折叠</small></span><span class="tab-switcher__path" :title="item.cwd">{{ item.cwd }}</span><span class="tab-switcher__machine" :title="`${item.machine} · ${item.engine}`">{{ item.machine }} · {{ item.engine }}</span></span>
            <span class="tab-switcher__detail">
              <span class="tab-switcher__status" :data-status="item.status" :title="item.status === 'done-unseen' ? '已完成，尚未查看' : statusLabels[item.status]">
                <CheckCircle2 v-if="item.status === 'done-unseen'" :size="13" aria-hidden="true" />
                <Unplug v-else-if="item.status === 'disconnected'" :size="13" aria-hidden="true" />
                <span v-else class="tab-switcher__status-dot" aria-hidden="true" />
                {{ statusLabels[item.status] }}
              </span>
              <span v-if="item.reason" class="tab-switcher__reason">{{ item.reason }}</span>
            </span>
          </button>
          <div v-if="!items.length" class="tab-switcher__empty" role="status">当前条件没有匹配的标签<button v-if="query" type="button" data-switcher-clear @click="clearSearch">清空搜索</button><button v-if="filter !== 'all'" type="button" data-switcher-filter="all" @click="applyFilter('all')">回到全部</button></div>
        </div>
        <footer>↑ ↓ 选择<span>Enter 切换</span><span>Esc 返回</span></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.tab-switcher-scrim { position: fixed; inset: 0; z-index: 10000; background: #0007; display: flex; justify-content: center; align-items: flex-start; padding: min(15vh, 110px) 16px 24px; }
.tab-switcher { --status-waiting: v-bind('STATUS_COLOR.waiting'); --status-running: v-bind('STATUS_COLOR.running'); --status-done: v-bind("STATUS_COLOR['done-unseen']"); width: min(640px, 100%); max-height: calc(100dvh - min(15vh, 110px) - 24px); display: flex; flex-direction: column; overflow: hidden; color: hsl(var(--foreground)); background: hsl(var(--background)); border: 1px solid hsl(var(--border)); border-radius: 12px; box-shadow: 0 24px 80px #0008; }
.tab-switcher__search { display: flex; align-items: center; gap: 12px; padding: 16px; border-bottom: 1px solid hsl(var(--border)); color: hsl(var(--muted-foreground)); }
.tab-switcher__search input { width: 100%; min-width: 0; background: transparent; color: hsl(var(--foreground)); border: 0; outline: none; font-size: 15px; }
.tab-switcher__search button { display: grid; place-items: center; min-width: 32px; min-height: 32px; border-radius: 6px; }
.tab-switcher__heading { display: flex; flex: 0 0 32px; align-items: center; justify-content: space-between; gap: 8px; padding: 0 16px; color: hsl(var(--muted-foreground)); font-size: 11px; white-space: nowrap; }
.tab-switcher__heading button { color: hsl(var(--primary)); font-size: 11px; text-decoration: underline; text-underline-offset: 3px; }
.tab-switcher__filters { display: flex; flex-shrink: 0; gap: 6px; padding: 2px 16px 10px; overflow-x: auto; overscroll-behavior-x: contain; }
.tab-switcher__filters button { display: inline-flex; flex-shrink: 0; align-items: center; gap: 5px; min-height: 30px; padding: 4px 9px; border: 1px solid hsl(var(--border)); border-radius: 5px; font-size: 12px; white-space: nowrap; }
.tab-switcher__filters button[aria-pressed=true] { background: hsl(var(--accent)); border-color: hsl(var(--primary) / .65); }
.tab-switcher__filters button:disabled { opacity: .4; cursor: default; }
.tab-switcher__filters button span { font-size: 11px; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }
.tab-switcher button:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: -2px; }
kbd { font-family: inherit; font-size: 10px; opacity: .75; }
.tab-switcher__list { overflow-y: auto; min-height: 0; overscroll-behavior: contain; max-height: 440px; padding: 4px 6px 8px; }
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
.tab-switcher__reason { font-size: 11px; color: hsl(var(--muted-foreground)); }
.tab-switcher__detail { display: grid; justify-items: end; flex-shrink: 0; gap: 5px; max-width: 140px; text-align: right; }
.tab-switcher__status { --quick-status-color: hsl(var(--muted-foreground)); display: inline-flex; align-items: center; gap: 6px; padding: 3px 7px; border: 1px solid color-mix(in srgb, var(--quick-status-color), transparent 65%); border-radius: 5px; background: color-mix(in srgb, var(--quick-status-color), transparent 92%); font-size: 12px; font-weight: 500; line-height: 16px; white-space: nowrap; }
.tab-switcher__status[data-status=waiting] { --quick-status-color: var(--status-waiting); }
.tab-switcher__status[data-status=running] { --quick-status-color: var(--status-running); }
.tab-switcher__status[data-status=done-unseen] { --quick-status-color: var(--status-done); }
.tab-switcher__status svg { flex-shrink: 0; color: var(--quick-status-color); }
.tab-switcher__status-dot { flex-shrink: 0; width: 8px; height: 8px; border: 1px solid var(--quick-status-color); border-radius: 50%; }
.tab-switcher__status[data-status=waiting] .tab-switcher__status-dot { background: var(--quick-status-color); --dot-min-opacity: v-bind('STATUS_MOTION.waiting.minOpacity'); animation: status-dot-pulse v-bind('STATUS_MOTION.waiting.duration') v-bind('STATUS_MOTION.waiting.easing') infinite; }
.tab-switcher__status[data-status=running] .tab-switcher__status-dot { background: var(--quick-status-color); --dot-min-opacity: v-bind('STATUS_MOTION.running.minOpacity'); animation: status-dot-pulse v-bind('STATUS_MOTION.running.duration') v-bind('STATUS_MOTION.running.easing') infinite; }
@keyframes status-dot-pulse { 0%, 100% { opacity: var(--dot-min-opacity); } 50% { opacity: 1; } }
@media (prefers-reduced-motion: reduce) { .tab-switcher__status-dot { animation: none !important; opacity: 1; } }
.tab-switcher__message, .tab-switcher__empty { padding: 16px; font-size: 13px; color: hsl(var(--muted-foreground)); }
.tab-switcher__empty { display: grid; gap: 12px; justify-items: center; padding: 32px; }
.tab-switcher__empty button { color: hsl(var(--primary)); }
footer { display: flex; gap: 20px; padding: 10px 16px; border-top: 1px solid hsl(var(--border)); font-size: 11px; color: hsl(var(--muted-foreground)); }
@media (max-width: 600px) { .tab-switcher-scrim { padding: 14px 10px; } .tab-switcher { max-height: calc(100dvh - 28px); } .tab-switcher__detail { max-width: 90px; } .tab-switcher__name { flex-wrap: wrap; gap: 4px; } .tab-switcher__search input { font-size: 16px; } }
</style>
