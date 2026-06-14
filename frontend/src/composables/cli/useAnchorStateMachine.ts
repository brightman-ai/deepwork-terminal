/**
 * useAnchorStateMachine — Four-state selection anchor management for mobile.
 * States: IDLE / NO_ANCHOR / HAS_ANCHOR_1 / HAS_BOTH
 * [Ref: T5-B4.M4, CAP-selection-copy S2, DDC-08]
 */
import { ref, readonly, computed } from 'vue'
import type { AnchorState, CellCoord } from '@terminal/types/terminal'

export interface AnchorStateMachine {
  state: ReturnType<typeof readonly<ReturnType<typeof ref<AnchorState>>>>
  anchor1: ReturnType<typeof readonly<ReturnType<typeof ref<CellCoord | null>>>>
  anchor2: ReturnType<typeof readonly<ReturnType<typeof ref<CellCoord | null>>>>
  /** Enter selection mode (NO_ANCHOR) */
  enterSelection: () => void
  /** Place first anchor at the given cell coordinate */
  placeAnchor1: (coord: CellCoord) => void
  /** Place second anchor at the given cell coordinate */
  placeAnchor2: (coord: CellCoord) => void
  /** Move the most recently placed anchor to a new position */
  moveNearestAnchor: (coord: CellCoord) => void
  /** Select entire visible screen: anchor1=top-left, anchor2=bottom-right */
  selectAll: (topLeft: CellCoord, bottomRight: CellCoord) => void
  /** Exit selection mode, reset to IDLE */
  cancel: () => void
  /** Get ordered anchors (start <= end) for selection range */
  orderedAnchors: Readonly<ReturnType<typeof ref<{ start: CellCoord; end: CellCoord } | null>>>
}

export function useAnchorStateMachine(): AnchorStateMachine {
  const state = ref<AnchorState>('IDLE')
  const anchor1 = ref<CellCoord | null>(null)
  const anchor2 = ref<CellCoord | null>(null)
  // Track which anchor was placed most recently (1 or 2).
  const lastPlaced = ref<1 | 2>(1)

  function enterSelection() {
    state.value = 'NO_ANCHOR'
    anchor1.value = null
    anchor2.value = null
  }

  function placeAnchor1(coord: CellCoord) {
    anchor1.value = { ...coord }
    lastPlaced.value = 1
    if (anchor2.value) {
      state.value = 'HAS_BOTH'
    } else {
      state.value = 'HAS_ANCHOR_1'
    }
  }

  function placeAnchor2(coord: CellCoord) {
    if (state.value === 'IDLE' || state.value === 'NO_ANCHOR') {
      // Must have anchor1 first.
      return
    }
    anchor2.value = { ...coord }
    lastPlaced.value = 2
    state.value = 'HAS_BOTH'
  }

  function moveNearestAnchor(coord: CellCoord) {
    if (state.value === 'HAS_ANCHOR_1') {
      // Only anchor1 exists, move it.
      anchor1.value = { ...coord }
      return
    }
    if (state.value === 'HAS_BOTH') {
      // Move the most recently placed anchor.
      if (lastPlaced.value === 1) {
        anchor1.value = { ...coord }
      } else {
        anchor2.value = { ...coord }
      }
    }
  }

  function selectAll(topLeft: CellCoord, bottomRight: CellCoord) {
    anchor1.value = { ...topLeft }
    anchor2.value = { ...bottomRight }
    lastPlaced.value = 2
    state.value = 'HAS_BOTH'
  }

  function cancel() {
    state.value = 'IDLE'
    anchor1.value = null
    anchor2.value = null
  }

  const orderedAnchors = computed(() => {
    if (!anchor1.value || !anchor2.value) return null
    const a = anchor1.value
    const b = anchor2.value
    // Order by bufferRow (scroll-aware), fallback to row.
    const aRow = a.bufferRow ?? a.row
    const bRow = b.bufferRow ?? b.row
    if (aRow < bRow || (aRow === bRow && a.col <= b.col)) {
      return { start: a, end: b }
    }
    return { start: b, end: a }
  })

  return {
    state: readonly(state),
    anchor1: readonly(anchor1),
    anchor2: readonly(anchor2),
    enterSelection,
    placeAnchor1,
    placeAnchor2,
    moveNearestAnchor,
    selectAll,
    cancel,
    orderedAnchors,
  }
}
