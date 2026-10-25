import { Unicode11Addon } from '@xterm/addon-unicode11'
import type { Terminal } from '@xterm/xterm'
import type { TerminalBufferLike } from '../../terminalFileLinks'

export type TranscriptLayout = 'claude-read' | 'claude-update' | 'claude-write-pair'
  | 'codex-edited' | 'codex-added' | 'prose-label' | 'prose-bare' | 'prose-markdown'

// Capture the addon's public provider registration; layout measurements use the
// installed Unicode 11 table, independently of the production path parser.
let unicode: {wcwidth(codepoint: number): number}
new Unicode11Addon().activate({unicode: {register(provider: typeof unicode) { unicode = provider }}} as unknown as Terminal)
export const cellWidth = (text: string): number => [...text].reduce((n, c) => n + unicode.wcwidth(c.codePointAt(0)!), 0)

export function cliHardWrap(text: string, columns: number, gutter: number): string[] {
  if (!columns) return [text]
  const rows: string[] = []
  let row = '', width = 0
  for (const char of text) {
    const size = unicode.wcwidth(char.codePointAt(0)!)
    if (size && width + size > columns && width > gutter) {
      rows.push(row); row = ' '.repeat(gutter); width = gutter
    }
    row += char; width += size
  }
  rows.push(row)
  return rows
}

export function renderTranscriptPath(layout: TranscriptLayout, path: string, columns = 0): string[] {
  const wrap = (text: string, gutter: number) => cliHardWrap(text, columns, gutter)
  switch (layout) {
    case 'claude-read': return wrap(`● Read(${path} · lines 12-20)`, 6)
    case 'claude-update': return wrap(`● Update(${path})`, 8)
    case 'claude-write-pair': return [...wrap(`● Write(${path})`, 7), ...wrap(`  ⎿ \u00a0Wrote 4 lines to ${path}`, 5)]
    case 'codex-edited': return wrap(`  • Edited ${path} (+4, -2)`, 2)
    case 'codex-added': return wrap(`  • Added ${path} (+4 -0)`, 2)
    case 'prose-label': return wrap(`文件：${path}（已保存）`, 2)
    case 'prose-bare': return wrap(`● 已保存 ${path}。`, 2)
    case 'prose-markdown': return wrap(`● 已保存 [文档](${path})，可查看。`, 2)
  }
}

/** Literal printed CLI rows projected into xterm-like cells. The browser suite
 * independently writes the same rows into the installed, real xterm. */
export function transcriptCellBuffer(rows: readonly string[], columns = 0): TerminalBufferLike {
  const physical: Array<{isWrapped: boolean; cells: Array<{chars: string; width: number}>}> = []
  for (const text of rows) {
    let cells: Array<{chars: string; width: number}> = [], wrapped = false
    for (const char of text) {
      const width = unicode.wcwidth(char.codePointAt(0)!)
      if (width === 0 && cells.length) {
        const base = cells.length > 1 && cells[cells.length - 1].width === 0 ? cells.length - 2 : cells.length - 1
        cells[base].chars += char
        continue
      }
      if (columns && cells.length + width > columns) {
        while (cells.length < columns) cells.push({chars: '', width: 1})
        physical.push({cells, isWrapped: wrapped}); cells = []; wrapped = true
      }
      cells.push({chars: char, width})
      if (width === 2) cells.push({chars: '', width: 0})
    }
    if (columns) while (cells.length < columns) cells.push({chars: '', width: 1})
    physical.push({cells, isWrapped: wrapped})
  }
  return {length: physical.length, getLine: (row) => {
    const line = physical[row]
    return line && {isWrapped: line.isWrapped, length: line.cells.length,
      getCell: (col) => line.cells[col] && ({getChars: () => line.cells[col].chars, getWidth: () => line.cells[col].width})}
  }}
}
