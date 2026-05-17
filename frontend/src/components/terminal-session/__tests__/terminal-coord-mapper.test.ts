/**
 * TC-08-FE-07: TerminalCoordMapper: screenToCell normal conversion.
 * TC-08-FE-08: TerminalCoordMapper: boundary clamping.
 * [Ref: CAP-touch-mouse S3, DDC-09]
 */
import { describe, test, expect } from 'bun:test'
import type { CellCoord } from '../../../types/terminal'

// Re-implement coord mapper logic for testing.
interface TerminalDimensions {
  cols: number
  rows: number
  cellWidth: number
  cellHeight: number
  offsetX: number
  offsetY: number
}

function screenToCell(dim: TerminalDimensions, screenX: number, screenY: number): CellCoord {
  if (dim.cellWidth <= 0 || dim.cellHeight <= 0) {
    return { col: 0, row: 0 }
  }
  let col = Math.floor((screenX - dim.offsetX) / dim.cellWidth)
  let row = Math.floor((screenY - dim.offsetY) / dim.cellHeight)
  col = Math.max(0, Math.min(col, dim.cols - 1))
  row = Math.max(0, Math.min(row, dim.rows - 1))
  return { col, row }
}

function cellToScreen(dim: TerminalDimensions, col: number, row: number): { x: number; y: number } {
  return {
    x: dim.offsetX + (col + 0.5) * dim.cellWidth,
    y: dim.offsetY + (row + 0.5) * dim.cellHeight,
  }
}

const mockDim: TerminalDimensions = {
  cols: 80,
  rows: 24,
  cellWidth: 9,
  cellHeight: 18,
  offsetX: 10,
  offsetY: 20,
}

describe('TerminalCoordMapper', () => {
  // TC-08-FE-07: Normal conversion
  test('screenToCell converts pixel coordinates to cell coordinates', () => {
    // Click at pixel (55, 56) → cell col = floor((55-10)/9) = 5, row = floor((56-20)/18) = 2
    const result = screenToCell(mockDim, 55, 56)
    expect(result.col).toBe(5)
    expect(result.row).toBe(2)
  })

  test('screenToCell handles origin correctly', () => {
    // Click at the very start of the terminal.
    const result = screenToCell(mockDim, 10, 20)
    expect(result.col).toBe(0)
    expect(result.row).toBe(0)
  })

  test('cellToScreen returns center of the cell', () => {
    // Cell (0,0) center: (10 + 0.5*9, 20 + 0.5*18) = (14.5, 29)
    const result = cellToScreen(mockDim, 0, 0)
    expect(result.x).toBe(14.5)
    expect(result.y).toBe(29)
  })

  test('round-trip: screenToCell -> cellToScreen is close to original', () => {
    const screenX = 100
    const screenY = 150
    const cell = screenToCell(mockDim, screenX, screenY)
    const screen = cellToScreen(mockDim, cell.col, cell.row)
    // Should be within one cell size of original.
    expect(Math.abs(screen.x - screenX)).toBeLessThan(mockDim.cellWidth)
    expect(Math.abs(screen.y - screenY)).toBeLessThan(mockDim.cellHeight)
  })

  // TC-08-FE-08: Boundary clamping
  test('screenToCell clamps negative coordinates to (0, 0)', () => {
    const result = screenToCell(mockDim, -100, -50)
    expect(result.col).toBe(0)
    expect(result.row).toBe(0)
  })

  test('screenToCell clamps coordinates beyond terminal to max', () => {
    // Way beyond: should clamp to (cols-1, rows-1)
    const result = screenToCell(mockDim, 9999, 9999)
    expect(result.col).toBe(79)  // cols - 1
    expect(result.row).toBe(23)  // rows - 1
  })

  test('handles zero cell dimensions gracefully', () => {
    const zeroDim: TerminalDimensions = {
      cols: 80, rows: 24,
      cellWidth: 0, cellHeight: 0,
      offsetX: 0, offsetY: 0,
    }
    const result = screenToCell(zeroDim, 100, 100)
    expect(result.col).toBe(0)
    expect(result.row).toBe(0)
  })
})
