import type { Terminal } from '@xterm/xterm'

export interface TerminalViewportAnchor {
  follow: boolean
  distance: number
  lines: { offset: number; text: string }[]
}

/** A restore replaces the buffer, so an old row number (or xterm marker) no longer identifies
 * the line the user was reading. Keep a small content anchor, and match after parsing. */
export function captureTerminalViewport(term: Terminal): TerminalViewportAnchor {
  const buffer = term.buffer.active
  const follow = buffer.type === 'alternate' || buffer.viewportY >= buffer.baseY
  const lines: TerminalViewportAnchor['lines'] = []
  if (!follow) {
    for (let offset = 0; offset < Math.min(term.rows, 16) && lines.length < 3; offset++) {
      const text = buffer.getLine(buffer.viewportY + offset)?.translateToString(true) ?? ''
      if (text.trim()) lines.push({ offset, text })
    }
  }
  return { follow, distance: buffer.baseY - buffer.viewportY, lines }
}

export function restoreTerminalViewport(term: Terminal, anchor: TerminalViewportAnchor): void {
  const buffer = term.buffer.active
  if (buffer.type === 'alternate') return
  if (anchor.follow) {
    term.scrollToBottom()
    return
  }
  const fallback = Math.max(0, buffer.baseY - anchor.distance)
  let chosen = fallback
  let distance = Infinity
  if (anchor.lines.length) {
    for (let row = 0; row <= buffer.baseY; row++) {
      if (!anchor.lines.every(line => buffer.getLine(row + line.offset)?.translateToString(true) === line.text)) continue
      const delta = Math.abs(row - fallback)
      if (delta < distance) { chosen = row; distance = delta }
    }
  }
  term.scrollToLine(chosen)
}
