import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

/**
 * useEdgeDrag — make a fixed, edge-anchored affordance VERTICALLY draggable along
 * its viewport edge so it can be moved off the text it would otherwise cover.
 *
 * Contract / why it is shaped this way:
 *  - The handle is `position: fixed` and clings to one vertical edge (left/right).
 *    This composable owns ONLY its vertical placement: it drives an absolute `top`
 *    (in px) via a reactive style object the caller binds with `:style`. The caller
 *    keeps the horizontal anchoring (left:0 / right:0) in CSS.
 *  - It deliberately does NOT use `transform: translateY(-50%)` for centering — the
 *    persisted `top` is an absolute px value, so a centering transform would fight
 *    the stored offset. The initial position is computed as a centered px `top` on
 *    first mount (when nothing is persisted) instead.
 *  - Tap-vs-drag: pointer/touch movement under THRESHOLD px is a tap and the caller's
 *    original click still fires (we never call preventDefault on a tap, and we expose
 *    `wasDragged()` for an optional click guard). A movement over the threshold is a
 *    drag: we reposition, clamp to the viewport, persist, and suppress the click.
 *  - Touch + mouse via Pointer Events (single unified path). Passive=false on the
 *    move so we can preventDefault while dragging (stops the page from scrolling).
 *
 * Reused by every fixed edge handle (ResourceDrawer `.rd-handle`, the standalone
 * HUD edge tab) — one source of truth, no copy-paste.
 */

export interface EdgeDragOptions {
  /** localStorage key the vertical offset is persisted under (per-handle). */
  storageKey: string
  /** px of pointer travel below which the interaction counts as a tap, not a drag. */
  threshold?: number
  /** px kept clear of the top/bottom viewport edges when clamping. */
  margin?: number
}

export interface EdgeDragBinding {
  /** Bind to the handle element ref. */
  el: Ref<HTMLElement | null>
  /** Bind with `:style` — drives the handle's vertical position. */
  style: Ref<Record<string, string>>
  /**
   * Optional `@click.capture` guard: returns true (and was suppressed) if the last
   * pointer interaction was a drag, so the caller can `if (edge.shouldSuppressClick()) return`.
   */
  shouldSuppressClick: () => boolean
}

const DEFAULT_THRESHOLD = 6
const DEFAULT_MARGIN = 8

export function useEdgeDrag(options: EdgeDragOptions): EdgeDragBinding {
  const { storageKey } = options
  const threshold = options.threshold ?? DEFAULT_THRESHOLD
  const margin = options.margin ?? DEFAULT_MARGIN

  const el = ref<HTMLElement | null>(null)
  // `top` stays null until we have a concrete px value (restored or first-measured),
  // so the element keeps its CSS-centered position until the user actually drags.
  const top = ref<number | null>(loadTop())
  const style = ref<Record<string, string>>(buildStyle(top.value))

  let dragging = false
  let draggedPastThreshold = false
  let startPointerY = 0
  let startTop = 0
  let activePointerId: number | null = null

  function loadTop(): number | null {
    try {
      const raw = localStorage.getItem(storageKey)
      if (raw == null) return null
      const n = Number(raw)
      return Number.isFinite(n) ? n : null
    } catch {
      return null
    }
  }

  function persistTop(v: number): void {
    try {
      localStorage.setItem(storageKey, String(Math.round(v)))
    } catch {
      /* storage may be unavailable (private mode) — position is still live in-memory */
    }
  }

  function buildStyle(v: number | null): Record<string, string> {
    // While `v` is null we emit NO top/transform so the element's CSS centering
    // (top:50%; transform:translateY(-50%)) stays in effect. Once we have a px value
    // we pin `top` absolutely and neutralise the centering transform.
    if (v == null) return {}
    return { top: `${v}px`, transform: 'none' }
  }

  function clampTop(v: number): number {
    const h = el.value?.offsetHeight ?? 54
    const max = Math.max(margin, window.innerHeight - h - margin)
    return Math.min(max, Math.max(margin, v))
  }

  function currentTop(): number {
    if (top.value != null) return top.value
    // First drag with no stored value: measure where CSS centering put us.
    const rect = el.value?.getBoundingClientRect()
    return rect ? rect.top : Math.round(window.innerHeight / 2)
  }

  function onPointerDown(e: PointerEvent): void {
    // Only the primary button / a touch contact starts a drag.
    if (e.button != null && e.button !== 0) return
    activePointerId = e.pointerId
    dragging = true
    draggedPastThreshold = false
    startPointerY = e.clientY
    startTop = currentTop()
    // Drive the rest of the gesture off WINDOW listeners, not the handle element.
    // On iOS Safari, setPointerCapture() for TOUCH pointers is unreliable, so once the
    // finger leaves the small handle the element would stop receiving pointermove and the
    // drag would freeze. Listening on window tracks the finger anywhere in the viewport
    // regardless of capture support. setPointerCapture is kept as a harmless bonus below.
    window.addEventListener('pointermove', onPointerMove, { passive: false })
    window.addEventListener('pointerup', endDrag)
    window.addEventListener('pointercancel', endDrag)
    el.value?.setPointerCapture?.(e.pointerId)
  }

  function onPointerMove(e: PointerEvent): void {
    if (!dragging || e.pointerId !== activePointerId) return
    const dy = e.clientY - startPointerY
    if (!draggedPastThreshold && Math.abs(dy) < threshold) return
    draggedPastThreshold = true
    // Now we are dragging: stop the page from scrolling / the tap from registering.
    e.preventDefault()
    const next = clampTop(startTop + dy)
    top.value = next
    style.value = buildStyle(next)
  }

  function endDrag(e: PointerEvent): void {
    if (e.pointerId !== activePointerId) return
    if (dragging && draggedPastThreshold && top.value != null) {
      persistTop(top.value)
    }
    dragging = false
    activePointerId = null
    // Tear down the per-gesture window listeners (added on pointerdown).
    window.removeEventListener('pointermove', onPointerMove)
    window.removeEventListener('pointerup', endDrag)
    window.removeEventListener('pointercancel', endDrag)
    if (el.value?.hasPointerCapture?.(e.pointerId)) {
      el.value.releasePointerCapture(e.pointerId)
    }
    // Leave `draggedPastThreshold` set until the click guard reads it: the click
    // event fires AFTER pointerup, so shouldSuppressClick() needs the flag intact.
  }

  // Click fires after pointerup; if the gesture was a drag, swallow it so the
  // handle's @click (open drawer / toggle HUD) doesn't trigger on reposition.
  function onClickCapture(e: MouseEvent): void {
    if (draggedPastThreshold) {
      e.stopPropagation()
      e.preventDefault()
    }
    draggedPastThreshold = false
  }

  function onResize(): void {
    if (top.value == null) return
    const clamped = clampTop(top.value)
    if (clamped !== top.value) {
      top.value = clamped
      style.value = buildStyle(clamped)
    }
  }

  onMounted(() => {
    const node = el.value
    if (!node) return
    // Only `pointerdown` + the click guard live on the handle. The move/up/cancel
    // listeners are attached to WINDOW for the duration of each drag (see onPointerDown),
    // so the gesture survives the finger leaving the small handle on iOS Safari.
    node.addEventListener('pointerdown', onPointerDown)
    node.addEventListener('click', onClickCapture, { capture: true })
    window.addEventListener('resize', onResize)
    // A restored offset may be stale if the viewport shrank since last session.
    if (top.value != null) onResize()
  })

  onBeforeUnmount(() => {
    const node = el.value
    if (node) {
      node.removeEventListener('pointerdown', onPointerDown)
      node.removeEventListener('click', onClickCapture, { capture: true } as EventListenerOptions)
    }
    // Defensively drop any in-flight gesture listeners (e.g. unmount mid-drag).
    window.removeEventListener('pointermove', onPointerMove)
    window.removeEventListener('pointerup', endDrag)
    window.removeEventListener('pointercancel', endDrag)
    window.removeEventListener('resize', onResize)
  })

  return {
    el,
    style,
    shouldSuppressClick: () => draggedPastThreshold,
  }
}
