import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { lineText, type HistoryLine } from './useTerminalHistory'
import { historySelectionText, type HistoryPoint, type HistorySelection } from './historySelection'

export function useHistorySelection(lines: Ref<HistoryLine[]>, scroller: Ref<HTMLElement | null>, onScroll: () => void) {
  const selection = ref<HistorySelection | null>(null)
  const text = computed(() => historySelectionText(lines.value, selection.value))
  let dragging = false
  let nativeSelecting = false
  let frame = 0
  let pointerX = 0, pointerY = 0

  function fromNode(node: Node | null, offset: number): HistoryPoint | null {
    const element = node instanceof Element ? node : node?.parentElement
    const content = element?.closest<HTMLElement>('.copy-mode__text')
    const row = content?.closest<HTMLElement>('[data-line]')
    if (!content || !row || !node || !scroller.value?.contains(row)) return null
    const range = document.createRange()
    range.selectNodeContents(content)
    range.setEnd(node, offset)
    const line = Number(row.dataset.line)
    const data = lines.value.find(item => item.n === line)
    if (!data) return null
    const length = lineText(data).length
    return { line, column: Math.min(length, range.toString().length) }
  }

  function atPoint(x: number, y: number): HistoryPoint | null {
    const el = scroller.value
    if (!el) return null
    const rect = el.getBoundingClientRect()
    x = Math.max(rect.left + 1, Math.min(rect.right - 16, x))
    y = Math.max(rect.top + 5, Math.min(rect.bottom - 16, y))
    const doc = document as Document & {
      caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null
    }
    const pos = doc.caretPositionFromPoint?.(x, y)
    const position = pos && fromNode(pos.offsetNode, pos.offset)
    if (position) return position
    const range = document.caretRangeFromPoint?.(x, y)
    const point = range && fromNode(range.startContainer, range.startOffset)
    if (point) return point
    const row = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-line]')
    if (!row || !el.contains(row)) return null
    const line = lines.value.find(item => item.n === Number(row.dataset.line))
    if (!line) return null
    const left = row.querySelector('.copy-mode__text')!.getBoundingClientRect().left
    return { line: line.n, column: x <= left ? 0 : lineText(line).length }
  }

  function extend(point: HistoryPoint): void {
    selection.value = { anchor: selection.value?.anchor ?? point, focus: point }
  }

  function updatePointer(): void {
    const point = atPoint(pointerX, pointerY)
    if (point) extend(point)
  }

  async function autoScroll(): Promise<void> {
    if (!dragging || !scroller.value) return
    const el = scroller.value
    const rect = el.getBoundingClientRect()
    const distance = pointerY < rect.top + 28 ? pointerY - rect.top - 28
      : pointerY > rect.bottom - 28 ? pointerY - rect.bottom + 28 : 0
    if (distance) {
      el.scrollTop += Math.max(-24, Math.min(24, distance))
      onScroll()
      await nextTick()
      if (dragging) updatePointer()
    }
    if (dragging) frame = requestAnimationFrame(() => { void autoScroll() })
  }

  function onPointerDown(e: PointerEvent): void {
    if (e.pointerType === 'touch') {
      nativeSelecting = true
      selection.value = null
    }
  }

  function onMouseDown(e: MouseEvent): void {
    if (e.button !== 0) return
    // Using the scrollbar to reach an offscreen endpoint must not reset the anchor.
    if (!(e.target instanceof Element) || !e.target.closest('.copy-mode__line')) return
    // PointerEvent.detail is always zero; click counts belong to MouseEvent.
    if (e.detail > 1 || nativeSelecting) {
      nativeSelecting = true // Keep native touch handles and double/triple-click selection.
      selection.value = null
      return
    }
    const point = atPoint(e.clientX, e.clientY)
    if (!point) return
    e.preventDefault()
    window.getSelection()?.removeAllRanges()
    if (e.shiftKey && selection.value) extend(point)
    else selection.value = { anchor: point, focus: point }
    scroller.value?.focus({ preventScroll: true })
    dragging = true
    pointerX = e.clientX; pointerY = e.clientY
    frame = requestAnimationFrame(() => { void autoScroll() })
  }

  function onPointerMove(e: PointerEvent): void {
    if (!dragging) return
    if (!(e.buttons & 1)) { stopDrag(); return }
    pointerX = e.clientX; pointerY = e.clientY
    updatePointer()
  }

  function captureNative(): void {
    if (dragging || (selection.value && !nativeSelecting)) return
    const native = window.getSelection()
    if (!native) return
    const anchor = fromNode(native.anchorNode, native.anchorOffset)
    const focus = fromNode(native.focusNode, native.focusOffset)
    if (anchor && focus) selection.value = { anchor, focus }
  }

  function stopDrag(): void {
    dragging = false
    cancelAnimationFrame(frame)
    if (nativeSelecting) captureNative()
    nativeSelecting = false
  }

  function clear(): void {
    stopDrag()
    selection.value = null
    window.getSelection()?.removeAllRanges()
  }

  function selectToEnd(): void {
    const last = lines.value.at(-1)
    if (selection.value && last) extend({ line: last.n, column: lineText(last).length })
  }

  // A search can replace the buffer. Appending/prepending keeps stable line coordinates.
  watch(lines, () => {
    if (selection.value && !lines.value.some(line => line.n === selection.value!.anchor.line)) clear()
  })

  onMounted(() => {
    window.addEventListener('pointermove', onPointerMove)
    window.addEventListener('pointerup', stopDrag)
    window.addEventListener('pointercancel', stopDrag)
    window.addEventListener('blur', stopDrag)
    document.addEventListener('selectionchange', captureNative)
  })
  onBeforeUnmount(() => {
    stopDrag()
    window.removeEventListener('pointermove', onPointerMove)
    window.removeEventListener('pointerup', stopDrag)
    window.removeEventListener('pointercancel', stopDrag)
    window.removeEventListener('blur', stopDrag)
    document.removeEventListener('selectionchange', captureNative)
  })
  return { selection, text, onPointerDown, onMouseDown, selectToEnd, stopDrag, clear }
}
