import { describe, expect, it } from 'bun:test'
import type { Terminal } from '@xterm/xterm'
import { captureTerminalViewport, restoreTerminalViewport } from '../terminalViewportRestore'

function terminal(lines: string[], baseY: number, viewportY: number, type = 'normal') {
  const calls: (number | 'bottom')[] = []
  const term = {
    rows: 5,
    buffer: { active: { type, baseY, viewportY, getLine: (row: number) => lines[row] === undefined ? undefined : { translateToString: () => lines[row] } } },
    scrollToBottom: () => calls.push('bottom'), scrollToLine: (row: number) => calls.push(row),
  } as unknown as Terminal
  return { term, calls }
}
const logs = (from: number, count: number) => Array.from({ length: count }, (_, i) => `记录 ${from + i}`)

describe('viewport across a destructive terminal restore', () => {
  it('follows the new bottom only when the old viewport was at the bottom', () => {
    const before = terminal(logs(0, 100), 95, 95)
    const after = terminal(logs(50, 150), 145, 0)
    restoreTerminalViewport(after.term, captureTerminalViewport(before.term))
    expect(after.calls).toEqual(['bottom'])
  })
  it('finds the same content after the restore rebases all row numbers', () => {
    const before = terminal(logs(0, 1000), 995, 917)
    const after = terminal(logs(450, 550), 545, 0)
    restoreTerminalViewport(after.term, captureTerminalViewport(before.term))
    expect(after.calls).toEqual([467])
  })
  it('keeps the history being read when background output adds new lines', () => {
    const before = terminal(logs(0, 100), 95, 20)
    const after = terminal(logs(0, 150), 145, 0)
    restoreTerminalViewport(after.term, captureTerminalViewport(before.term))
    expect(after.calls).toEqual([20])
  })
  it('disambiguates repeated content by its previous distance from the bottom', () => {
    const before = terminal(Array(100).fill('等待结果'), 95, 20)
    const after = terminal(Array(150).fill('等待结果'), 145, 0)
    restoreTerminalViewport(after.term, captureTerminalViewport(before.term))
    expect(after.calls).toEqual([70])
  })
  it('clamps an evicted anchor and never applies normal history to an alternate screen', () => {
    const before = terminal(logs(0, 100), 95, 20)
    const anchor = captureTerminalViewport(before.term)
    const after = terminal(logs(100, 10), 5, 0)
    restoreTerminalViewport(after.term, anchor)
    expect(after.calls).toEqual([0])
    const alternate = terminal(logs(100, 5), 0, 0, 'alternate')
    restoreTerminalViewport(alternate.term, anchor)
    expect(alternate.calls).toEqual([])
  })
})
