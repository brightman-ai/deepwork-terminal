/**
 * useTerminalCoordMapper — Maps between screen coordinates and terminal cell coordinates.
 * Isolates access to xterm.js internal APIs behind a clean interface.
 * [Ref: CAP-touch-mouse S3, DDC-09]
 */
import type { CellCoord } from '@/types/terminal'

export interface TerminalDimensions {
  cols: number
  rows: number
  cellWidth: number
  cellHeight: number
  /** Offset of the terminal element from the page top-left */
  offsetX: number
  offsetY: number
}

export interface TerminalCoordMapper {
  /**
   * Convert a screen (pixel) coordinate to a terminal cell coordinate.
   * Returns clamped values within [0, cols-1] x [0, rows-1].
   */
  screenToCell: (screenX: number, screenY: number) => CellCoord

  /**
   * Convert a terminal cell coordinate to a screen (pixel) coordinate.
   * Returns the center of the cell.
   */
  cellToScreen: (col: number, row: number) => { x: number; y: number }
}

export function useTerminalCoordMapper(getDimensions: () => TerminalDimensions): TerminalCoordMapper {
  function screenToCell(screenX: number, screenY: number): CellCoord {
    const dim = getDimensions()
    if (dim.cellWidth <= 0 || dim.cellHeight <= 0) {
      return { col: 0, row: 0 }
    }

    let col = Math.floor((screenX - dim.offsetX) / dim.cellWidth)
    let row = Math.floor((screenY - dim.offsetY) / dim.cellHeight)

    // Clamp to valid range.
    col = Math.max(0, Math.min(col, dim.cols - 1))
    row = Math.max(0, Math.min(row, dim.rows - 1))

    return { col, row }
  }

  function cellToScreen(col: number, row: number): { x: number; y: number } {
    const dim = getDimensions()
    return {
      x: dim.offsetX + (col + 0.5) * dim.cellWidth,
      y: dim.offsetY + (row + 0.5) * dim.cellHeight,
    }
  }

  return { screenToCell, cellToScreen }
}
