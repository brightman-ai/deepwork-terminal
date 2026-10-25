/** Real xterm system check. Bundle for a private fixture page containing
 * #terminal and #result. No transcript data or commands are executed here. */
import { Terminal } from '../frontend/node_modules/@xterm/xterm'
import { enableUnicode11 } from '../frontend/src/components/terminal-session/terminalUnicode'
import { terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../frontend/src/components/terminal-session/terminalFileLinks'
import corpus from '../frontend/src/components/terminal-session/__tests__/fixtures/transcriptPathCorpus.json'
import { cliHardWrap, renderTranscriptPath, type TranscriptLayout } from '../frontend/src/components/terminal-session/__tests__/fixtures/transcriptPathLayouts'

const term = new Terminal({cols: 80, rows: 40, scrollback: 500, allowProposedApi: true})
enableUnicode11(term)
term.open(document.querySelector<HTMLElement>('#terminal')!)
const layouts: TranscriptLayout[] = ['claude-read', 'claude-update', 'claude-write-pair', 'codex-edited', 'codex-added', 'prose-label', 'prose-bare', 'prose-markdown']
const report = {checks: 0, fragments: 0, failures: [] as Array<Record<string, unknown>>, done: false}
;(window as unknown as {corpusReport: typeof report}).corpusReport = report
const write = (text: string) => new Promise<void>(resolve => term.write(text, resolve))
const assert = (value: unknown, message: string) => {if (!value) throw new Error(message)}
function inspect(path: string): void {
  const seen = new Set<string>()
  for (let row = 0; row < term.buffer.active.length; row++) for (const link of terminalFileLinksAtRow(term.buffer.active, row)) {
    assert(link.path === path, `wrong target: ${link.path}`)
    seen.add(link.path); report.fragments++
    for (const col of [link.cellStart - 1, link.cellEnd - 1]) assert(terminalFileLinkAtCell(term.buffer.active, row, col)?.path === path, 'pointer identity differs')
    const selection = terminalFileSelectionAtCell(term.buffer.active, row, link.cellStart - 1, term.cols)
    assert(selection?.path === path, 'selection identity differs')
    term.select(selection!.startCol, selection!.startRow, selection!.length)
    // Soft wraps are copied without newlines; CLI gutters remain printed text.
    const copied = term.getSelection()
    assert(copied.length > 0 && !copied.includes('(+4'), 'empty selection or stats entered path selection')
    term.clearSelection()
  }
  assert(seen.size === 1, 'missing reference')
}
async function run() {
  for (const sample of corpus.cases) for (const hardWidth of [0, 32, 54, 80]) {
    const rowsByLayout = 'wrapper' in sample
      ? [cliHardWrap('● ' + (sample.wrapper === 'markdown' ? `[文档](${sample.path})` : '`' + sample.path + '`') + '，请查看。', hardWidth, 2)]
      : layouts.map(layout => renderTranscriptPath(layout, sample.path, hardWidth))
    for (let layout = 0; layout < rowsByLayout.length; layout++) for (const cols of [24, 80]) {
      term.reset(); term.resize(cols, 40)
      // Completed CLI output: leave the cursor on its following prompt row.
      // xterm defaults reflowCursorLine=false because shells redraw that row.
      // Resizing an unfinished fake cursor line without its shell is a different
      // contract; it can truncate buffer text before the parser ever sees it.
      await write(rowsByLayout[layout].join('\r\n') + '\r\n')
      for (const phase of ['written', 'reflow']) {
        if (phase === 'reflow') term.resize(cols === 24 ? 80 : 39, 40)
        report.checks++
        try {inspect(sample.path)} catch (error) {report.failures.push({id: sample.id, hardWidth, layout, cols: term.cols, phase, message: String(error)})}
      }
    }
  }
  report.done = true
  document.querySelector('#result')!.textContent = JSON.stringify(report)
}
run().catch(error => {report.failures.push({fatal: String(error)}); report.done = true})
