/** Browser system check using the installed xterm and the production helpers.
 * Bundle: bun build scripts/terminal-path-layout-harness.ts --target browser --outfile <fixture>/harness.js
 * Load from a fixture HTML containing #terminal and #result. No product state is mocked.
 */
import { Terminal } from '../frontend/node_modules/@xterm/xterm'
import { enableUnicode11 } from '../frontend/src/components/terminal-session/terminalUnicode'
import { editedDiffLineLinkFromBuffer, findWrappedTerminalFileLinks, terminalCellRange, wrappedTerminalRows, terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../frontend/src/components/terminal-session/terminalFileLinks'

const samples = [
  { text: 'topics/TH-0628-.../讲解/UEBA工程架构评审/UEBA基线-数据库选型-StarRocks还是Cassandra-v1-20261019.md:513', path: 'topics/TH-0628-.../讲解/UEBA工程架构评审/UEBA基线-数据库选型-StarRocks还是Cassandra-v1-20261019.md', line: 513 },
  { text: 'topics/TH-0628-…/讲解/📚基线.md:513-515', path: 'topics/TH-0628-…/讲解/📚基线.md', line: 513 },
  { text: '"docs/中文 report(final).md"', path: 'docs/中文 report(final).md', selected: 'docs/中文 report(final).md' },
  { text: 'docs/cafe\u0301.md#L20-L25', path: 'docs/cafe\u0301.md', line: 20 },
  { text: 'docs/my\\ report.md', path: 'docs/my report.md' },
  { text: '@./tmp/clip/10-19-13/1348001-4410cc76d54ccf80.png', path: './tmp/clip/10-19-13/1348001-4410cc76d54ccf80.png' },
  { text: '[main.swift:404](</work/src/main.swift#L404-L410>)', path: '/work/src/main.swift', line: 404, selected: '/work/src/main.swift#L404-L410' },
  { text: 'Read(docs/report(final).md)', path: 'docs/report(final).md', selected: 'docs/report(final).md' },
  { text: 'Dockerfile', path: 'Dockerfile' },
  { text: '"docs/\\345\\255\\220.md"', path: 'docs/子.md', selected: 'docs/\\345\\255\\220.md' },
  { text: '[draft].md', path: '[draft].md' },
  { text: '(draft).md', path: '(draft).md' },
  { text: 'Read("docs/my report.md")', path: 'docs/my report.md', selected: 'docs/my report.md' },
  { text: 'file:///work/my%20report.md:20:7', path: '/work/my report.md', line: 20 },
]
const widths = [20, 32, 40, 60, 80, 120, 180]
const results: Array<Record<string, unknown>> = []
const documentResults: Array<Record<string, unknown>> = []
const failures: string[] = []
const host = document.querySelector<HTMLElement>('#terminal')!
const result = document.querySelector<HTMLElement>('#result')!
const term = new Terminal({ cols: 80, rows: 40, scrollback: 200, allowProposedApi: true, fontSize: 14 })
enableUnicode11(term)
term.open(host)
const write = (text: string): Promise<void> => new Promise(resolve => term.write(text, resolve))
const assert = (ok: unknown, message: string): void => { if (!ok) throw new Error(message) }

function inspect(sample: typeof samples[number], cols: number, phase: string): void {
  let links = 0
  const rows = new Set<number>()
  for (let row = 0; row < term.buffer.active.length; row++) {
    const group = wrappedTerminalRows(term.buffer.active, row)
    for (const link of findWrappedTerminalFileLinks(group.rows, group.currentRow)) {
      assert(link.path === sample.path && link.lineStart === sample.line, `${cols}/${phase}: wrong target ${link.path}`)
      const physical = term.buffer.active.getLine(row)!
      const cells = Array.from({ length: physical.length }, (_, col) => {
        const cell = physical.getCell(col)!
        return { chars: cell.getChars(), width: cell.getWidth() }
      })
      const span = terminalCellRange(cells, link.rawStart, link.rawLength)
      assert(span.start >= 1 && span.end <= term.cols, `${cols}/${phase}: range outside terminal`)
      const selected = cells.slice(span.start - 1, span.end).map(c => c.chars || (c.width ? ' ' : '')).join('')
      const printed = group.rows[group.currentRow].text.slice(link.rawStart, link.rawStart + link.rawLength)
      assert(selected === printed, `${cols}/${phase}: cell mapping mismatch ${JSON.stringify({ selected, printed })}`)
      const physicalLink = terminalFileLinksAtRow(term.buffer.active, row).find(candidate => candidate.referenceStart === link.referenceStart)
      assert(physicalLink, `${cols}/${phase}: shared pointer map lost a fragment`)
      const hit = terminalFileLinkAtCell(term.buffer.active, row, physicalLink!.cellStart - 1)
      assert(hit?.path === sample.path, `${cols}/${phase}: pointer target differs from native link`)
      const selection = terminalFileSelectionAtCell(term.buffer.active, row, physicalLink!.cellStart - 1, term.cols)
      assert(selection, `${cols}/${phase}: no whole-reference selection`)
      term.select(selection!.startCol, selection!.startRow, selection!.length)
      assert(term.getSelection() === (sample.selected ?? sample.text), `${cols}/${phase}: selected partial/wrong reference ${JSON.stringify(term.getSelection())}`)
      term.clearSelection()
      links++
      rows.add(row)
    }
  }
  assert(links > 0, `${cols}/${phase}: no link for ${sample.text}`)
  results.push({ cols, phase, path: sample.path, fragments: links, physical_rows: [...rows] })
}

function inspectDocument(cols: number, phase: string, expected: string, printed: string): void {
  let fragments = 0
  for (let row = 0; row < term.buffer.active.length; row++) {
    for (const link of terminalFileLinksAtRow(term.buffer.active, row).filter(link => link.documentRelative)) {
      assert(link.path === expected, `${cols}/${phase}: document target anchored to wrong directory: ${link.path}`)
      const hit = terminalFileLinkAtCell(term.buffer.active, row, link.cellStart - 1)
      assert(hit?.path === expected, `${cols}/${phase}: contextual native/virtual targets disagree`)
      const selection = terminalFileSelectionAtCell(term.buffer.active, row, link.cellStart - 1, term.cols)!
      assert(selection, `${cols}/${phase}: contextual reference has no selection`)
      term.select(selection.startCol, selection.startRow, selection.length)
      assert(term.getSelection() === printed, `${cols}/${phase}: copied resolved path instead of original reference`)
      term.clearSelection()
      fragments++
    }
  }
  assert(fragments > 0, `${cols}/${phase}: contextual Markdown reference lost`)
  documentResults.push({ cols, phase, expected, fragments })
}

async function run(): Promise<void> {
  for (const cols of widths) {
    term.resize(cols, 40)
    for (const sample of samples) {
      try {
        term.reset()
        await write('文件 \x1b[32m' + sample.text + '\x1b[0m\r\n')
        inspect(sample, cols, 'write')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspect(sample, changed, 'reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
    term.reset()
    term.resize(cols, 40)
    await write('Edited topics/中文目录/讲解/long-report.md (+2 -2)\r\n20 + source\r\n21 + next\r\n')
    let gutter = false
    for (let row = 0; row < term.buffer.active.length; row++) {
      const link = editedDiffLineLinkFromBuffer(term.buffer.active, row)
      if (link?.lineStart === 21 && link.path === 'topics/中文目录/讲解/long-report.md') gutter = true
    }
    if (!gutter) failures.push(`${cols}: wrapped edit header lost its file`)
    try {
      const source = 'collab/work/whiteboard-ng自举-20261001/新版隔离重分析-20261019/audit/本轮结论.md'
      const reference = '../workspace/output/UEBA数据库选型/UEBA基线设计与数据库选择依据-v2-20261019.md'
      const expected = './collab/work/whiteboard-ng自举-20261001/新版隔离重分析-20261019/workspace/output/UEBA数据库选型/UEBA基线设计与数据库选择依据-v2-20261019.md'
      term.reset()
      await write(`Added ${source} (+28 -0)\r\n1 +# 本轮结论\r\n2 +\r\n3 +交付是 [UEBA基线 v2](${reference})，入口已同步。\r\n`)
      inspectDocument(cols, 'write', expected, reference)
      for (const changed of [32, 120, cols]) {
        term.resize(changed, 40)
        inspectDocument(changed, 'reflow', expected, reference)
      }
    } catch (error) { failures.push(String(error)) }
  }
  const checks = results.length + documentResults.length
  const report = { widths, sample_count: samples.length, checks, document_context_checks: documentResults.length, failures, results, documentResults,
    method: 'Installed xterm with Unicode 11; actual writes, ANSI, buffer cells, resize/reflow; production path helpers.' }
  ;(window as unknown as { pathLayoutReport: unknown }).pathLayoutReport = report
  result.textContent = JSON.stringify({ checks, failures }, null, 2)
  result.dataset.complete = 'true'
}
void run().catch(error => { result.textContent = String(error); result.dataset.complete = 'failed' })
