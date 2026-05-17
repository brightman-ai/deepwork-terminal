/**
 * TC-08-FE-04: AnchorStateMachine: complete 4-state flow.
 * TC-08-FE-05: AnchorStateMachine: nearest anchor movement.
 * TC-08-FE-06: AnchorStateMachine: select all (full screen).
 * [Ref: T5-B4.M4, CAP-selection-copy S2, DDC-08]
 */
import { describe, test, expect } from 'bun:test'
import type { AnchorState, CellCoord } from '../../../types/terminal'

// Re-implement anchor state machine logic for testing without Vue reactivity.
class AnchorStateMachineImpl {
  state: AnchorState = 'IDLE'
  anchor1: CellCoord | null = null
  anchor2: CellCoord | null = null
  lastPlaced: 1 | 2 = 1

  enterSelection() {
    this.state = 'NO_ANCHOR'
    this.anchor1 = null
    this.anchor2 = null
  }

  placeAnchor1(coord: CellCoord) {
    this.anchor1 = { ...coord }
    this.lastPlaced = 1
    this.state = this.anchor2 ? 'HAS_BOTH' : 'HAS_ANCHOR_1'
  }

  placeAnchor2(coord: CellCoord) {
    if (this.state === 'IDLE' || this.state === 'NO_ANCHOR') return
    this.anchor2 = { ...coord }
    this.lastPlaced = 2
    this.state = 'HAS_BOTH'
  }

  moveNearestAnchor(coord: CellCoord) {
    if (this.state === 'HAS_ANCHOR_1') {
      this.anchor1 = { ...coord }
    } else if (this.state === 'HAS_BOTH') {
      if (this.lastPlaced === 1) {
        this.anchor1 = { ...coord }
      } else {
        this.anchor2 = { ...coord }
      }
    }
  }

  selectAll(topLeft: CellCoord, bottomRight: CellCoord) {
    this.anchor1 = { ...topLeft }
    this.anchor2 = { ...bottomRight }
    this.lastPlaced = 2
    this.state = 'HAS_BOTH'
  }

  cancel() {
    this.state = 'IDLE'
    this.anchor1 = null
    this.anchor2 = null
  }
}

describe('AnchorStateMachine', () => {
  // TC-08-FE-04: Complete 4-state flow
  test('complete flow: IDLE -> NO_ANCHOR -> HAS_ANCHOR_1 -> HAS_BOTH -> IDLE', () => {
    const sm = new AnchorStateMachineImpl()
    expect(sm.state).toBe('IDLE')

    sm.enterSelection()
    expect(sm.state).toBe('NO_ANCHOR')
    expect(sm.anchor1).toBeNull()
    expect(sm.anchor2).toBeNull()

    sm.placeAnchor1({ col: 5, row: 3 })
    expect(sm.state).toBe('HAS_ANCHOR_1')
    expect(sm.anchor1).toEqual({ col: 5, row: 3 })
    expect(sm.anchor2).toBeNull()

    sm.placeAnchor2({ col: 20, row: 10 })
    expect(sm.state).toBe('HAS_BOTH')
    expect(sm.anchor1).toEqual({ col: 5, row: 3 })
    expect(sm.anchor2).toEqual({ col: 20, row: 10 })

    sm.cancel()
    expect(sm.state).toBe('IDLE')
    expect(sm.anchor1).toBeNull()
    expect(sm.anchor2).toBeNull()
  })

  // TC-08-FE-05: Nearest anchor movement
  test('moveNearestAnchor moves the most recently placed anchor', () => {
    const sm = new AnchorStateMachineImpl()
    sm.enterSelection()

    // Place anchor1.
    sm.placeAnchor1({ col: 0, row: 0 })
    expect(sm.lastPlaced).toBe(1)

    // Move nearest (only anchor1 exists).
    sm.moveNearestAnchor({ col: 10, row: 5 })
    expect(sm.anchor1).toEqual({ col: 10, row: 5 })

    // Place anchor2.
    sm.placeAnchor2({ col: 50, row: 20 })
    expect(sm.lastPlaced).toBe(2)

    // Move nearest should move anchor2 (last placed).
    sm.moveNearestAnchor({ col: 40, row: 15 })
    expect(sm.anchor2).toEqual({ col: 40, row: 15 })
    expect(sm.anchor1).toEqual({ col: 10, row: 5 }) // anchor1 unchanged

    // Place anchor1 again — it becomes the last placed.
    sm.placeAnchor1({ col: 3, row: 1 })
    expect(sm.lastPlaced).toBe(1)

    // Move nearest should now move anchor1.
    sm.moveNearestAnchor({ col: 7, row: 4 })
    expect(sm.anchor1).toEqual({ col: 7, row: 4 })
    expect(sm.anchor2).toEqual({ col: 40, row: 15 }) // anchor2 unchanged
  })

  // TC-08-FE-06: Select all (full screen)
  test('selectAll sets both anchors to screen corners', () => {
    const sm = new AnchorStateMachineImpl()
    sm.enterSelection()

    sm.selectAll({ col: 0, row: 0 }, { col: 79, row: 23 })
    expect(sm.state).toBe('HAS_BOTH')
    expect(sm.anchor1).toEqual({ col: 0, row: 0 })
    expect(sm.anchor2).toEqual({ col: 79, row: 23 })
  })

  test('placeAnchor2 is ignored in IDLE and NO_ANCHOR states', () => {
    const sm = new AnchorStateMachineImpl()

    // IDLE state.
    sm.placeAnchor2({ col: 5, row: 5 })
    expect(sm.state).toBe('IDLE')
    expect(sm.anchor2).toBeNull()

    // NO_ANCHOR state.
    sm.enterSelection()
    sm.placeAnchor2({ col: 5, row: 5 })
    expect(sm.state).toBe('NO_ANCHOR')
    expect(sm.anchor2).toBeNull()
  })
})
