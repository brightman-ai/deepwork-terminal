export const CLIPBOARD_MAX_BYTES = 4 * 1024 * 1024
export const CLIPBOARD_TOTAL_BYTES = 20 * 1024 * 1024
export interface ClipboardEntry {
  id: string
  sequence: number
  sessionId: string
  source: string
  text: string
  direction: 'remote' | 'local'
  at: string
  replayed?: boolean
  targetId?: string
  pinned?: boolean
  copiedAt?: string
  action?: string
}
export interface ClipboardTarget {
  id: string
  sessionId: string
  name: string
  paneId?: string
  pid: number
  shellPid: number
  active: boolean
  readCommand: string
}
export function validClipboardEntry(e: ClipboardEntry): boolean {
  return (
    !!e &&
    typeof e.id === 'string' &&
    typeof e.text === 'string' &&
    typeof e.source === 'string' &&
    Number.isFinite(Date.parse(e.at)) &&
    new TextEncoder().encode(e.text).length <= CLIPBOARD_MAX_BYTES
  )
}
export function boundClipboardHistory(
  entries: ClipboardEntry[],
  days = 7,
  now = Date.now(),
): ClipboardEntry[] {
  const seen = new Set<string>()
  const candidates = entries.filter((e) => {
    if (!validClipboardEntry(e) || seen.has(e.id)) return false
    seen.add(e.id)
    return e.pinned || now - Date.parse(e.at) < days * 86400000
  })
  const keep: ClipboardEntry[] = []
  let bytes = 0
  // Preserve pins first, while enforcing the same hard memory bounds on them.
  for (const e of [
    ...candidates.filter((e) => e.pinned),
    ...candidates.filter((e) => !e.pinned),
  ]) {
    const size = new TextEncoder().encode(e.text).length
    if (keep.length < 100 && bytes + size <= CLIPBOARD_TOTAL_BYTES) {
      keep.push(e)
      bytes += size
    }
  }
  const ids = new Set(keep.map((e) => e.id))
  return candidates.filter((e) => ids.has(e.id))
}
export function sameClipboardTarget(
  a: ClipboardTarget,
  b: ClipboardTarget,
): boolean {
  return a.id === b.id && a.pid === b.pid && a.shellPid === b.shellPid
}
