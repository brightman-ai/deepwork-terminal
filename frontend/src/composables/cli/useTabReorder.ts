import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import type { WorkbenchGroup } from '@terminal/types/workbench'
import { moveTabBefore, sameTabOrder, type TabMove } from './tabReorder'

interface Options {
  strip: Ref<HTMLElement | null>
  groups(): WorkbenchGroup[]
  renamingTabId(): string | null
  request(): number
  commit(moves: TabMove[]): void
  contextMenu(event: MouseEvent, tabId: string): void
  notice(text: string): void
}

interface Press {
  pointerId: number
  tabId: string
  groupId: string
  x: number
  y: number
  element: HTMLElement
  started: boolean
}

/** The strip owns only a temporary preview. Workbench remains the sole owner of
 * committed order. Normal touch gestures stay native; sorting explicitly opts in
 * to touch-action:none, avoiding a race with horizontal scrolling / long press. */
export function useTabReorder(options: Options) {
  const sorting = ref(false)
  const preview = ref<WorkbenchGroup[] | null>(null)
  const moves: TabMove[] = []
  const draggedTabId = ref<string | null>(null)
  const drop = ref<TabMove | null>(null)
  const ghost = ref<{ x: number; y: number; name: string } | null>(null)
  const displayGroups = computed(() => preview.value ?? options.groups())
  let press: Press | null = null
  let point = { x: 0, y: 0 }
  let longPressTimer: ReturnType<typeof setTimeout> | null = null
  let frame = 0
  let suppressClickUntil = 0
  let manualMenuUntil = 0

  function clearLongPress(): void {
    if (longPressTimer !== null) clearTimeout(longPressTimer)
    longPressTimer = null
  }

  function stopDrag(): void {
    clearLongPress()
    if (frame) cancelAnimationFrame(frame)
    frame = 0
    if (press?.element.hasPointerCapture?.(press.pointerId)) {
      press.element.releasePointerCapture(press.pointerId)
    }
    press = null
    draggedTabId.value = null
    ghost.value = null
    drop.value = null
    window.removeEventListener('pointermove', onPointerMove, true)
    window.removeEventListener('pointerup', onPointerUp, true)
    window.removeEventListener('pointercancel', cancelDrag, true)
    window.removeEventListener('blur', cancelDrag)
    if (!sorting.value) document.removeEventListener('keydown', onKeyDown, true)
  }

  function cancelDrag(event?: Event): void {
    if (event?.type === 'pointercancel' && (event as PointerEvent).pointerId !== press?.pointerId) return
    if (press?.started) suppressClickUntil = Date.now() + 700
    stopDrag()
  }

  function cancelSorting(): void {
    sorting.value = false
    preview.value = null
    moves.length = 0
    stopDrag()
  }

  function startSorting(): void {
    cancelSorting()
    if (options.renamingTabId()) return
    preview.value = options.groups().map(g => ({ ...g, tabs: [...g.tabs] }))
    sorting.value = true
    document.addEventListener('keydown', onKeyDown, true)
  }

  function finishSorting(): void {
    const committed = [...moves]
    // End the preview before the parent updates groups; its own commit must not
    // be confused with an external change by the membership/order watcher.
    cancelSorting()
    if (committed.length) options.commit(committed)
  }

  watch(options.request, startSorting)
  watch(
    () => JSON.stringify(options.groups().map(g => [g.id, g.collapsed, g.tabs.map(t => t.id)])),
    () => {
      if (!sorting.value && !press) return
      cancelSorting()
      options.notice('标签列表已变化，已取消本次排序，请重新调整')
    },
    { flush: 'sync' },
  )

  function onKeyDown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || (!sorting.value && !press)) return
    event.preventDefault()
    event.stopPropagation()
    if (sorting.value) cancelSorting()
    else cancelDrag()
  }

  function allowTabAction(event: Event): boolean {
    if (!sorting.value && !draggedTabId.value && Date.now() >= suppressClickUntil) return true
    event.preventDefault()
    event.stopPropagation()
    return false
  }

  function onContextMenu(event: MouseEvent, tabId: string): void {
    clearLongPress()
    if (sorting.value || draggedTabId.value || Date.now() < manualMenuUntil) {
      event.preventDefault()
      return
    }
    stopDrag()
    suppressClickUntil = Date.now() + 700
    options.contextMenu(event, tabId)
  }

  function onPointerDown(event: PointerEvent, tabId: string, groupId: string): void {
    if (event.button !== 0 || event.isPrimary === false || options.renamingTabId()) return
    stopDrag()
    suppressClickUntil = 0
    point = { x: event.clientX, y: event.clientY }
    press = {
      pointerId: event.pointerId, tabId, groupId,
      x: event.clientX, y: event.clientY,
      element: event.currentTarget as HTMLElement, started: false,
    }
    window.addEventListener('pointermove', onPointerMove, { capture: true, passive: false })
    window.addEventListener('pointerup', onPointerUp, true)
    window.addEventListener('pointercancel', cancelDrag, true)
    window.addEventListener('blur', cancelDrag)
    document.addEventListener('keydown', onKeyDown, true)
    if (event.pointerType === 'touch' && !sorting.value) {
      // iOS does not consistently dispatch contextmenu on buttons. Open the same
      // existing menu explicitly, cancelling as soon as a scroll starts.
      longPressTimer = setTimeout(() => {
        if (!press) return
        manualMenuUntil = suppressClickUntil = Date.now() + 900
        stopDrag()
        options.contextMenu(event, tabId)
      }, 500)
    }
  }

  function updateDrop(): void {
    const strip = options.strip.value
    const p = press
    if (!strip || !p?.started) return
    const bounds = strip.getBoundingClientRect()
    const inside = point.x >= bounds.left && point.x <= bounds.right && point.y >= bounds.top && point.y <= bounds.bottom
    const target = inside ? document.elementFromPoint(point.x, point.y)?.closest<HTMLElement>('[data-tab-id]') : null
    const tabId = target?.dataset.tabId
    drop.value = null
    if (!target || !strip.contains(target) || target.dataset.groupId !== p.groupId || !tabId || tabId === p.tabId) return
    const tabs = displayGroups.value.find(g => g.id === p.groupId)?.tabs ?? []
    const others = tabs.filter(t => t.id !== p.tabId)
    const index = others.findIndex(t => t.id === tabId)
    if (index < 0) return
    const rect = target.getBoundingClientRect()
    const beforeTabId = point.x < (rect.left + rect.right) / 2 ? tabId : (others[index + 1]?.id ?? null)
    drop.value = { groupId: p.groupId, tabId: p.tabId, beforeTabId }
  }

  function autoScroll(): void {
    frame = 0
    if (!press?.started) return
    const strip = options.strip.value
    if (strip) {
      const rect = strip.getBoundingClientRect()
      if (point.y >= rect.top && point.y <= rect.bottom && point.x >= rect.left && point.x <= rect.right) {
        const edge = Math.min(36, rect.width / 5)
        const speed = point.x < rect.left + edge ? -12 * (1 - (point.x - rect.left) / edge)
          : point.x > rect.right - edge ? 12 * (1 - (rect.right - point.x) / edge) : 0
        if (speed) { strip.scrollLeft += speed; updateDrop() }
      }
    }
    frame = requestAnimationFrame(autoScroll)
  }

  function onPointerMove(event: PointerEvent): void {
    const p = press
    if (!p || event.pointerId !== p.pointerId) return
    point = { x: event.clientX, y: event.clientY }
    if (!p.started) {
      if (Math.hypot(point.x - p.x, point.y - p.y) < 7) return
      clearLongPress()
      if (event.pointerType === 'touch' && !sorting.value) { stopDrag(); return }
      p.started = true
      draggedTabId.value = p.tabId
      const name = displayGroups.value.find(g => g.id === p.groupId)?.tabs.find(t => t.id === p.tabId)?.name ?? ''
      ghost.value = { ...point, name }
      p.element.setPointerCapture?.(p.pointerId)
      frame = requestAnimationFrame(autoScroll)
    }
    event.preventDefault()
    if (ghost.value) { ghost.value.x = point.x; ghost.value.y = point.y }
    updateDrop()
  }

  function applyPreview(move: TabMove): void {
    const group = preview.value?.find(g => g.id === move.groupId)
    if (!group) return
    const next = moveTabBefore(group.tabs, move.tabId, move.beforeTabId)
    if (sameTabOrder(group.tabs, next)) return
    group.tabs = next
    moves.push(move)
  }

  function onPointerUp(event: PointerEvent): void {
    if (!press || event.pointerId !== press.pointerId) return
    if (!press.started) { stopDrag(); return }
    point = { x: event.clientX, y: event.clientY }
    updateDrop()
    const move = drop.value
    suppressClickUntil = Date.now() + 700
    stopDrag()
    if (!move) return
    if (sorting.value) applyPreview(move)
    else options.commit([move])
  }

  /** Sorting remains usable from a keyboard through the menu entry as well. */
  function onTabKeyDown(event: KeyboardEvent, tabId: string, groupId: string): void {
    if (!sorting.value || !['ArrowLeft', 'ArrowRight'].includes(event.key)) return
    event.preventDefault()
    event.stopPropagation()
    const tabs = preview.value?.find(g => g.id === groupId)?.tabs ?? []
    const index = tabs.findIndex(t => t.id === tabId)
    if (event.key === 'ArrowLeft' && index > 0) applyPreview({ groupId, tabId, beforeTabId: tabs[index - 1]!.id })
    if (event.key === 'ArrowRight' && index >= 0 && index < tabs.length - 1) {
      applyPreview({ groupId, tabId, beforeTabId: tabs[index + 2]?.id ?? null })
    }
  }

  function insertBefore(tabId: string): boolean { return drop.value?.beforeTabId === tabId }
  function insertAfter(tabId: string, group: WorkbenchGroup): boolean {
    const others = group.tabs.filter(t => t.id !== drop.value?.tabId)
    return !!drop.value && drop.value.groupId === group.id && drop.value.beforeTabId === null
      && others[others.length - 1]?.id === tabId
  }

  onBeforeUnmount(cancelSorting)
  return {
    sorting, displayGroups, draggedTabId, ghost, cancelSorting, finishSorting,
    allowTabAction, onPointerDown, onContextMenu, onTabKeyDown, insertBefore, insertAfter,
  }
}
