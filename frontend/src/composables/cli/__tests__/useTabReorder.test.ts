import { afterEach, describe, expect, it } from 'bun:test'
import { defineComponent, nextTick, ref } from 'vue'
import { mountView } from '../../../portals/cli/__tests__/support/testRenderer'
import { useTabReorder } from '../useTabReorder'
import type { TabMove } from '../tabReorder'
import type { WorkbenchGroup } from '../../../types/workbench'

const globals = globalThis as unknown as Record<string, unknown>
const saved = Object.fromEntries(['window', 'document', 'requestAnimationFrame', 'cancelAnimationFrame'].map(k => [k, globals[k]]))
let unmount: (() => void) | undefined
afterEach(() => {
  unmount?.()
  unmount = undefined
  for (const [k, v] of Object.entries(saved)) {
    if (v === undefined) delete globals[k]
    else globals[k] = v
  }
})

function harness() {
  const listeners = new Map<string, Set<(e: any) => void>>()
  const frames = new Map<number, FrameRequestCallback>()
  let frameId = 0
  const events = {
    addEventListener: (key: string, fn: (e: any) => void) => { if (!listeners.has(key)) listeners.set(key, new Set()); listeners.get(key)!.add(fn) },
    removeEventListener: (key: string, fn: (e: any) => void) => { listeners.get(key)?.delete(fn) },
  }
  const rect = { left: 0, right: 300, top: 0, bottom: 36, width: 300 }
  const captured = new Set<number>()
  const elements = ['a', 'b', 'c', 'other'].map((id, i) => ({
    dataset: { tabId: id, groupId: i < 3 ? 'g' : 'h' },
    getBoundingClientRect: () => ({ left: i * 80, right: (i + 1) * 80 }),
    setPointerCapture: (id: number) => { captured.add(id) },
    hasPointerCapture: (id: number) => captured.has(id),
    releasePointerCapture: (id: number) => { captured.delete(id) },
    closest() { return this },
  }))
  let hit: unknown = elements[0]
  const strip = { scrollLeft: 0, getBoundingClientRect: () => rect, contains: (el: unknown) => elements.includes(el as typeof elements[0]) }
  globals.window = events
  globals.document = { ...events, elementFromPoint: () => hit }
  globals.requestAnimationFrame = (fn: FrameRequestCallback) => { frames.set(++frameId, fn); return frameId }
  globals.cancelAnimationFrame = (id: number) => { frames.delete(id) }
  const groups = ref<WorkbenchGroup[]>([{ id: 'g', name: 'g', collapsed: false,
    tabs: ['a', 'b', 'c'].map(id => ({ id, groupId: 'g', name: id, cwd: '/', engine: 'shell' })) }])
  const request = ref(0)
  const rename = ref<string | null>(null)
  const commits: TabMove[][] = []
  const menus: string[] = []
  const notices: string[] = []
  let model!: ReturnType<typeof useTabReorder>
  const m = mountView(defineComponent({ setup() {
    model = useTabReorder({ strip: ref(strip as unknown as HTMLElement), groups: () => groups.value,
      renamingTabId: () => rename.value, request: () => request.value,
      commit: moves => commits.push(moves), contextMenu: (_e, id) => menus.push(id), notice: t => notices.push(t) })
    return () => null
  } }), {})
  unmount = m.unmount
  const event = (x: number, y = 12, pointerType = 'mouse') => ({
    button: 0, pointerId: 1, isPrimary: true, pointerType, clientX: x, clientY: y,
    currentTarget: elements[2], preventDefault() {}, stopPropagation() {},
  }) as unknown as PointerEvent
  const fire = (type: string, e: unknown) => [...(listeners.get(type) ?? [])].forEach(fn => fn(e))
  const drag = (pointerType = 'mouse') => {
    model.onPointerDown(event(210, 12, pointerType), 'c', 'g')
    fire('pointermove', event(20, 12, pointerType))
    fire('pointerup', event(20, 12, pointerType))
  }
  return { model, groups, request, rename, commits, menus, notices, elements, strip, captured, frames, listeners,
    event, fire, drag, hit: (el: unknown) => { hit = el } }
}

describe('tab dragging gestures and preview ownership', () => {
  it('desktop commits a single move and suppresses the drop click without mutating props', () => {
    const h = harness()
    h.drag()
    expect(h.commits).toEqual([[{ groupId: 'g', tabId: 'c', beforeTabId: 'a' }]])
    expect(h.groups.value[0]!.tabs.map(t => t.id)).toEqual(['a', 'b', 'c'])
    expect(h.model.allowTabAction(h.event(20))).toBe(false)
    expect(h.captured.size).toBe(0)
    expect(h.frames.size).toBe(0)
  })
  it('a small movement remains an ordinary click; rename fields cannot start a drag', () => {
    const h = harness()
    h.model.onPointerDown(h.event(210), 'c', 'g')
    h.fire('pointermove', h.event(213))
    h.fire('pointerup', h.event(213))
    expect(h.commits).toEqual([])
    expect(h.model.allowTabAction(h.event(213))).toBe(true)
    h.rename.value = 'c'
    h.drag()
    expect(h.commits).toEqual([])
  })
  it('normal touch movement stays a native horizontal scroll', () => {
    const h = harness()
    h.drag('touch')
    expect(h.model.draggedTabId.value).toBeNull()
    expect(h.commits).toEqual([])
    expect(h.menus).toEqual([])
  })
  it('normal touch long press opens the existing menu once and suppresses a stray tap', async () => {
    const h = harness()
    h.model.onPointerDown(h.event(210, 12, 'touch'), 'c', 'g')
    await new Promise(resolve => setTimeout(resolve, 520))
    expect(h.menus).toEqual(['c'])
    h.model.onContextMenu(h.event(210), 'c')
    expect(h.menus).toEqual(['c'])
    expect(h.model.allowTabAction(h.event(210))).toBe(false)
  })
  it('mobile sorting previews touches without saving and cancel restores the original list', async () => {
    const h = harness()
    h.request.value++
    await nextTick()
    h.drag('touch')
    expect(h.model.displayGroups.value[0]!.tabs.map(t => t.id)).toEqual(['c', 'a', 'b'])
    expect(h.commits).toEqual([])
    h.model.cancelSorting()
    expect(h.model.displayGroups.value[0]!.tabs.map(t => t.id)).toEqual(['a', 'b', 'c'])
    expect(h.commits).toEqual([])
  })
  it('mobile Done commits exactly once, and the menu can reopen sorting on the same tab', async () => {
    const h = harness()
    h.request.value++
    await nextTick()
    h.drag('touch')
    h.model.finishSorting()
    expect(h.commits).toHaveLength(1)
    expect(h.model.sorting.value).toBe(false)
    h.request.value++
    await nextTick()
    expect(h.model.sorting.value).toBe(true)
    h.model.finishSorting()
    expect(h.commits).toHaveLength(1)
  })
  it('cross-group controls, empty space, and outside the strip are invalid drops', () => {
    for (const [hit, y] of [['other', 12], ['empty', 12], ['a', 90]] as const) {
      const h = harness()
      h.hit(hit === 'empty' ? null : h.elements.find(el => el.dataset.tabId === hit))
      h.model.onPointerDown(h.event(210), 'c', 'g')
      h.fire('pointermove', h.event(20, y))
      h.fire('pointerup', h.event(20, y))
      expect(h.commits).toEqual([])
      unmount!()
      unmount = undefined
    }
  })
  it('Escape / pointer cancel cancel a drag and clean its captures, frames and listeners', () => {
    for (const type of ['keydown', 'pointercancel']) {
      const h = harness()
      h.model.onPointerDown(h.event(210), 'c', 'g')
      h.fire('pointermove', h.event(20))
      expect(h.model.draggedTabId.value).toBe('c')
      h.fire(type, type === 'keydown' ? { key: 'Escape', preventDefault() {}, stopPropagation() {} } : { type, pointerId: 1 })
      expect(h.model.draggedTabId.value).toBeNull()
      expect(h.commits).toEqual([])
      expect(h.captured.size + h.frames.size).toBe(0)
      expect([...h.listeners.values()].reduce((sum, list) => sum + list.size, 0)).toBe(0)
      unmount!()
      unmount = undefined
    }
  })
  it('external list changes cancel the mobile draft without resurrecting the removed tab', async () => {
    const h = harness()
    h.request.value++
    await nextTick()
    h.drag('touch')
    h.groups.value[0]!.tabs = h.groups.value[0]!.tabs.filter(t => t.id !== 'c')
    expect(h.model.sorting.value).toBe(false)
    expect(h.model.displayGroups.value[0]!.tabs.map(t => t.id)).toEqual(['a', 'b'])
    expect(h.commits).toEqual([])
    expect(h.notices).toHaveLength(1)
  })
  it('edge scrolling runs only while dragging and stops on cancellation', () => {
    const h = harness()
    h.model.onPointerDown(h.event(210), 'c', 'g')
    h.fire('pointermove', h.event(297))
    const [id, callback] = [...h.frames.entries()][0]!
    h.frames.delete(id)
    callback(0)
    expect(h.strip.scrollLeft).toBeGreaterThan(0)
    h.fire('pointercancel', { type: 'pointercancel', pointerId: 1 })
    expect(h.frames.size).toBe(0)
  })
  it('keyboard arrows can reorder in sorting mode without switching a terminal', async () => {
    const h = harness()
    h.request.value++
    await nextTick()
    h.model.onTabKeyDown({ key: 'ArrowLeft', preventDefault() {}, stopPropagation() {} } as KeyboardEvent, 'c', 'g')
    expect(h.model.displayGroups.value[0]!.tabs.map(t => t.id)).toEqual(['a', 'c', 'b'])
    expect(h.commits).toEqual([])
    h.model.finishSorting()
    expect(h.commits).toEqual([[{ groupId: 'g', tabId: 'c', beforeTabId: 'b' }]])
  })
})
