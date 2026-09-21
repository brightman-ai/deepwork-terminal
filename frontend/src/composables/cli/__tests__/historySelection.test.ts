import { describe, expect, it } from 'bun:test'
import { historySelectionText, selectedSegments, type HistorySelection } from '../historySelection'
import type { HistoryLine } from '../useTerminalHistory'

const lines: HistoryLine[] = [
  { n: 40, seg: [{ t: '开始中文', s: 0 }, { t: '🙂末尾', s: 1 }] },
  { n: 41, seg: [] },
  { n: 42, seg: [{ t: 'last line', s: 0 }] },
]
const range: HistorySelection = { anchor: { line: 40, column: 2 }, focus: { line: 42, column: 4 } }

describe('history selection uses buffer coordinates, independent of virtual rows', () => {
  it('copies partial endpoints, styled Chinese/emoji and empty lines without line numbers', () => {
    expect(historySelectionText(lines, range)).toBe('中文🙂末尾\n\nlast')
    expect(historySelectionText(lines, { anchor: range.focus, focus: range.anchor })).toBe('中文🙂末尾\n\nlast')
  })
  it('retains the same text when older lines are prepended', () => {
    expect(historySelectionText([{ n: 39, seg: [{ t: 'unselected', s: 0 }] }, ...lines], range))
      .toBe('中文🙂末尾\n\nlast')
  })
  it('rejects a missing endpoint or a gap instead of silently copying half the selection', () => {
    expect(historySelectionText(lines.slice(1), range)).toBe('')
    expect(historySelectionText([lines[0], lines[2]], range)).toBe('')
  })
  it('keeps single clicks empty and splits highlights at actual character boundaries', () => {
    expect(historySelectionText(lines, { anchor: range.anchor, focus: range.anchor })).toBe('')
    expect(selectedSegments(lines[0], range)).toEqual([
      { t: '开始', s: 0, selected: false },
      { t: '中文', s: 0, selected: true },
      { t: '🙂末尾', s: 1, selected: true },
    ])
  })
  it('copies thousands of offscreen lines through the actual last character', () => {
    const many = Array.from({ length: 2000 }, (_, n) => ({ n, seg: [{ t: `row ${n}`, s: 0 }] }))
    const text = historySelectionText(many, { anchor: { line: 5, column: 4 }, focus: { line: 1999, column: 8 } })
    expect(text.split('\n')).toHaveLength(1995)
    expect(text.startsWith('5\nrow 6\n')).toBe(true)
    expect(text.endsWith('\nrow 1998\nrow 1999')).toBe(true)
  })
})
