import { lineText, type HistoryLine } from './useTerminalHistory'

/** Buffer coordinates survive virtual rows being removed. Columns are DOM/JS UTF-16 offsets. */
export interface HistoryPoint { line: number; column: number }
export interface HistorySelection { anchor: HistoryPoint; focus: HistoryPoint }

export function orderedSelection(selection: HistorySelection): [HistoryPoint, HistoryPoint] {
  const { anchor, focus } = selection
  return anchor.line < focus.line || (anchor.line === focus.line && anchor.column <= focus.column)
    ? [anchor, focus] : [focus, anchor]
}

export function selectionColumns(line: HistoryLine, selection: HistorySelection | null): [number, number] | null {
  if (!selection) return null
  const [start, end] = orderedSelection(selection)
  if (line.n < start.line || line.n > end.line) return null
  return [line.n === start.line ? start.column : 0, line.n === end.line ? end.column : lineText(line).length]
}

export function historySelectionText(lines: HistoryLine[], selection: HistorySelection | null): string {
  if (!selection) return ''
  const [start, end] = orderedSelection(selection)
  const selected = lines.filter(line => line.n >= start.line && line.n <= end.line)
  // Never silently copy a truncated range after a search replaces the loaded history.
  if (selected[0]?.n !== start.line || selected.at(-1)?.n !== end.line
    || selected.some((line, i) => i > 0 && line.n !== selected[i - 1].n + 1)) return ''
  return selected.map(line => {
    const [from, to] = selectionColumns(line, selection)!
    return lineText(line).slice(from, to)
  }).join('\n')
}

export function selectedSegments(line: HistoryLine, selection: HistorySelection | null) {
  const columns = selectionColumns(line, selection)
  let offset = 0
  return line.seg.flatMap(seg => {
    const from = Math.max(0, (columns?.[0] ?? 0) - offset)
    const to = Math.min(seg.t.length, (columns?.[1] ?? 0) - offset)
    offset += seg.t.length
    if (!columns || to <= from) return [{ ...seg, selected: false }]
    return [
      { t: seg.t.slice(0, from), s: seg.s, selected: false },
      { t: seg.t.slice(from, to), s: seg.s, selected: true },
      { t: seg.t.slice(to), s: seg.s, selected: false },
    ].filter(part => part.t.length > 0)
  })
}
