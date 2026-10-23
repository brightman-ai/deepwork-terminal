/** A pending user move, replayed over the latest workbench after a revision conflict.
 * Indices belong to a stale snapshot; stable tab ids express the actual destination. */
export interface TabMove {
  groupId: string
  tabId: string
  beforeTabId: string | null
}

/** Pure, identity-preserving insertion. Missing/cross-group anchors cancel the move;
 * a concurrently closed tab must never be recreated by a pending reorder. */
export function moveTabBefore<T extends { id: string }>(
  tabs: readonly T[], tabId: string, beforeTabId: string | null,
): T[] {
  const from = tabs.findIndex(t => t.id === tabId)
  if (from < 0 || beforeTabId === tabId) return [...tabs]
  if (beforeTabId !== null && !tabs.some(t => t.id === beforeTabId)) return [...tabs]
  const next = tabs.filter(t => t.id !== tabId)
  const to = beforeTabId === null ? next.length : next.findIndex(t => t.id === beforeTabId)
  next.splice(to, 0, tabs[from]!)
  return next
}

export function sameTabOrder(a: readonly { id: string }[], b: readonly { id: string }[]): boolean {
  return a.length === b.length && a.every((t, i) => t.id === b[i]?.id)
}

/** Replay ONLY explicit, unacknowledged moves. Ordinary saves from an old browser
 * have no moves, and therefore cannot restore its old order over a newer one. */
export function replayTabMoves<T extends { id: string; tabs: Array<{ id: string }> }>(
  groups: T[], moves: readonly TabMove[],
): void {
  for (const move of moves) {
    const group = groups.find(g => g.id === move.groupId)
    if (group) group.tabs = moveTabBefore(group.tabs, move.tabId, move.beforeTabId)
  }
}
