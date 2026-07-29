/**
 * useVisibleTabOrder — pure "visible tab order → position number" logic, shared between
 * standalone (deepwork-terminal, tabs nested in collapsible WorkbenchGroup[]) and pro
 * (deepwork-pro, tabs already a flat PortalTab[]). Deliberately data-model-agnostic: the
 * caller flattens its own tab tree into an ordered id array (respecting collapsed groups
 * on the standalone side) BEFORE calling in here — this composable never sees a group.
 *
 * Position numbers must match the Alt+1..9 switch-to-tab-N shortcuts 1:1, so numbering here
 * is the single source of truth both the tab-bar UI and useTabShortcuts read from.
 */

export interface VisibleTabOrder {
  /** 1-based position of each tab id, in the order given. */
  position: Map<string, number>
  /** Tab id at 1-based position N (Alt+N switch target), or undefined if out of range. */
  idAtPosition(n: number): string | undefined
  /** Next tab id after currentId, wrapping to the first tab past the last. */
  nextId(currentId: string): string | undefined
  /** Previous tab id before currentId, wrapping to the last tab before the first. */
  prevId(currentId: string): string | undefined
}

export function computeVisibleTabOrder(orderedIds: string[]): VisibleTabOrder {
  const position = new Map<string, number>()
  orderedIds.forEach((id, index) => position.set(id, index + 1))

  function idAtPosition(n: number): string | undefined {
    return orderedIds[n - 1]
  }

  function nextId(currentId: string): string | undefined {
    if (orderedIds.length === 0) return undefined
    const idx = orderedIds.indexOf(currentId)
    if (idx === -1) return orderedIds[0]
    return orderedIds[(idx + 1) % orderedIds.length]
  }

  function prevId(currentId: string): string | undefined {
    if (orderedIds.length === 0) return undefined
    const idx = orderedIds.indexOf(currentId)
    if (idx === -1) return orderedIds[orderedIds.length - 1]
    return orderedIds[(idx - 1 + orderedIds.length) % orderedIds.length]
  }

  return { position, idAtPosition, nextId, prevId }
}
