export interface RectLike {
  left: number
  top: number
  right: number
  bottom: number
  width: number
  height: number
}

export interface PopoverPlacement {
  left: number
  top: number
  maxHeight: number
  side: 'above' | 'below'
}

function clamp(value: number, min: number, max: number): number {
  if (max < min) return min
  return Math.min(max, Math.max(min, value))
}

/** Pure viewport-rect placement; works when visualViewport does not start at (0, 0). */
export function placeAnchoredPopover(
  anchor: RectLike,
  popover: Pick<RectLike, 'width' | 'height'>,
  viewport: RectLike,
  margin = 8,
  gap = 6,
): PopoverPlacement {
  const width = Math.min(popover.width, Math.max(0, viewport.width - margin * 2))
  const belowSpace = Math.max(0, viewport.bottom - margin - anchor.bottom - gap)
  const aboveSpace = Math.max(0, anchor.top - viewport.top - margin - gap)
  const side: 'above' | 'below' = popover.height <= belowSpace || belowSpace >= aboveSpace ? 'below' : 'above'
  const available = side === 'below' ? belowSpace : aboveSpace
  const maxHeight = Math.max(0, Math.min(560, Math.floor(viewport.height * 0.72), available))
  const renderedHeight = Math.min(popover.height, maxHeight)
  const left = clamp(anchor.left, viewport.left + margin, viewport.right - margin - width)
  const wantedTop = side === 'below' ? anchor.bottom + gap : anchor.top - gap - renderedHeight
  const top = clamp(wantedTop, viewport.top + margin, viewport.bottom - margin - renderedHeight)
  return { left, top, maxHeight, side }
}
