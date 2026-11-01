// Synthetic width-preserving grammar fixtures; names and prose are invented.
/** Browser system check using the installed xterm and the production helpers.
 * Bundle: bun build scripts/terminal-path-layout-harness.ts --target browser --outfile <fixture>/harness.js
 * Load from a fixture HTML containing #terminal and #result. No product state is mocked.
 */
import { Terminal } from '../frontend/node_modules/@xterm/xterm'
import { enableUnicode11 } from '../frontend/src/components/terminal-session/terminalUnicode'
import { editedDiffLineLinkFromBuffer, findWrappedTerminalFileLinks, terminalCellRange, wrappedTerminalRows, terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../frontend/src/components/terminal-session/terminalFileLinks'

import { terminalPathScenarios, type TerminalPathScenario } from '../frontend/src/components/terminal-session/__tests__/fixtures/terminalPathScenarios'

const samples = [
  { text: '稿是原文文的 v2，文另文 v3（同一文文版文文）：文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md（.docx 重出），643', path: '文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md', selected: '文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md', unprefixed: true },
  { text: '稿是原文文的 v2，文另文 v3（同一文文版文文）：文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md（.docx\r\n  重出），643', path: '文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md', selected: '文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md', unprefixed: true },
  { text: 'docs/report.md(final)-old.txt（.pdf 重出）', path: 'docs/report.md(final)-old.txt', selected: 'docs/report.md(final)-old.txt', unprefixed: true },
  { text: '  - 文文文文 v2（现行草稿，8 页文 1 页文文）：/home/tester/work/example-workspace/docs/topics/EX-0003-示例AA目录文文/示例AA目录文文-文文文文-v2-20300000.md', path: '/home/tester/work/example-workspace/docs/topics/EX-0003-示例AA目录文文/示例AA目录文文-文文文文-v2-20300000.md', selected: '/home/tester/work/example-workspace/docs/topics/EX-0003-示例AA目录文文/示例AA目录文文-文文文文-v2-20300000.md', unprefixed: true },
  { text: '文文（现行）：docs/报告(final).md:21', path: 'docs/报告(final).md', selected: 'docs/报告(final).md:21', line: 21, unprefixed: true },
  { text: '(optional):docs/report.md', path: 'docs/report.md', selected: 'docs/report.md', unprefixed: true },
  { text: 'Draft [optional]:README.md', path: 'README.md', selected: 'README.md', unprefixed: true },
  { text: '  文文文文记录 (docs/.tg/work/20300103-000000-example-terminal-layout/sample-notes-123456789-20300108.md)已保存。', path: 'docs/.tg/work/20300103-000000-example-terminal-layout/sample-notes-123456789-20300108.md', selected: 'docs/.tg/work/20300103-000000-example-terminal-layout/sample-notes-123456789-20300108.md', unprefixed: true },
  { text: 'The result (docs/report.md).', path: 'docs/report.md', selected: 'docs/report.md', unprefixed: true },
  { text: '3 +[文文文文的原文对文对文](case-文文文文.md)', path: 'case-文文文文.md', selected: 'case-文文文文.md', unprefixed: true },
  { text: '**docs/报告.md**', path: 'docs/报告.md', selected: 'docs/报告.md' },
  { text: '**docs/报告.md 已文新**', path: 'docs/报告.md', selected: 'docs/报告.md' },
  { text: '**附文**：CONFIG.md', path: 'CONFIG.md', selected: 'CONFIG.md' },
  { text: '文件:docs/report.md：说明', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: '文件：（来源：docs/report.md）', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: '时间：2026-10-16｜来源：scratchpad/文文.out（说明）', path: 'scratchpad/文文.out', selected: 'scratchpad/文文.out' },
  { text: '`[Ref: guide/troubleshooting/ime-shift-double-toggle.md]`', path: 'guide/troubleshooting/ime-shift-double-toggle.md', selected: 'guide/troubleshooting/ime-shift-double-toggle.md' },
  { text: '`> 记录 work/topic.log.md：文新到 #617｜文文文到 #615`', path: 'work/topic.log.md', selected: 'work/topic.log.md' },
  { text: '`python3 -m py_compile skills/demo/scripts/run.py`', path: 'skills/demo/scripts/run.py', selected: 'skills/demo/scripts/run.py' },
  { text: '"docs/name=report.md"', path: 'docs/name=report.md', selected: 'docs/name=report.md' },
  { text: '路径：（docs/中文 report.md）', path: 'docs/中文 report.md', selected: 'docs/中文 report.md' },
  { text: '文件：topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v4-20300103.md（v1 到 v3 在同一目录）', path: 'topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v4-20300103.md', selected: 'topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v4-20300103.md' },
  { text: '文件:docs/report.md(v1 and v3 in the same directory)', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: 'docs/report.md【附注】', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: 'docs/report.md(final).txt:121', path: 'docs/report.md(final).txt', line: 121 },
  { text: '  - v3：topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v3-20300103.md', path: 'topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v3-20300103.md', selected: 'topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v3-20300103.md' },
  { text: '  - v3:topics/EX-0001/文文/报告.md', path: 'topics/EX-0001/文文/报告.md', selected: 'topics/EX-0001/文文/报告.md' },
  { text: '  - 第文场纪要：docs/meetings/20300103-S1文文文文-文路径定位文文3/20300103-S1文文文文-文路径定位文文3.md', path: 'docs/meetings/20300103-S1文文文文-文路径定位文文3/20300103-S1文文文文-文路径定位文文3.md', selected: 'docs/meetings/20300103-S1文文文文-文路径定位文文3/20300103-S1文文文文-文路径定位文文3.md' },
  { text: '  - 第文场纪要:docs/meetings/纪要.md', path: 'docs/meetings/纪要.md', selected: 'docs/meetings/纪要.md' },
  { text: '文件:docs/report.md:121', path: 'docs/report.md', line: 121, selected: 'docs/report.md:121' },
  { text: 'docs/报告：2026.md', path: 'docs/报告：2026.md' },
  { text: 'Bash(until [ -s "docs/meetings/20300103-S1文文文文-文路径定位文文3/asr/meeting.qwen.txt" ]; do sleep 5; done; cat "docs/meetings/20300103-S1文文文文-文路径定位文文3/asr/meeting.qwen.txt")', path: 'docs/meetings/20300103-S1文文文文-文路径定位文文3/asr/meeting.qwen.txt', selected: 'docs/meetings/20300103-S1文文文文-文路径定位文文3/asr/meeting.qwen.txt' },
  { text: '(collab/work/sampletool-ng文文-20300101/文文文文文文-20300103/文文文文的文文与文文.md)已文文。', path: 'collab/work/sampletool-ng文文-20300101/文文文文文文-20300103/文文文文的文文与文文.md', selected: 'collab/work/sampletool-ng文文-20300101/文文文文文文-20300103/文文文文的文文与文文.md' },
  { text: '文件：.../DEMO文文文文文文/DEMO基线-v2-20300103.md', path: '.../DEMO文文文文文文/DEMO基线-v2-20300103.md', selected: '.../DEMO文文文文文文/DEMO基线-v2-20300103.md' },
  { text: '（docs/报告（终稿）.md）已文文', path: 'docs/报告（终稿）.md', selected: 'docs/报告（终稿）.md' },
  { text: 'topics/EX-0001-.../文文/DEMO文文文文文文/DEMO基线-文文文文文-DatabaseA文是DatabaseB-v1-20300103.md:513', path: 'topics/EX-0001-.../文文/DEMO文文文文文文/DEMO基线-文文文文文-DatabaseA文是DatabaseB-v1-20300103.md', line: 513 },
  { text: 'topics/EX-0001-…/文文/📚基线.md:513-515', path: 'topics/EX-0001-…/文文/📚基线.md', line: 513 },
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
const negativeSamples = [
  'https://<host>/docs/report.md', 'work/<件文>-文文读.md', '${ROOT}/report.md',
  '`sed -n "1,220p" ${JSON.stringify(base+"/docs/report.md")}`',
  '`target = d / target.name`', '595.304pt×150/72=1240.22（Δ0.78 ceil）',
  'text(ALL_TOOLS.filter(x=>/browser|tool_search/i.test(x.name)))', 'docs/**/*.md',
]
const widths = [20, 32, 40, 52, 54, 60, 80, 120, 180]
const results: Array<Record<string, unknown>> = []
const documentResults: Array<Record<string, unknown>> = []
const negativeResults: Array<Record<string, unknown>> = []
const renderedResults: Array<Record<string, unknown>> = []
const proseResults: Array<Record<string, unknown>> = []
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

function inspectNegative(text: string, cols: number, phase: string): void {
  for (let row = 0; row < term.buffer.active.length; row++) {
    assert(!terminalFileLinksAtRow(term.buffer.active, row).length, `${cols}/${phase}: expression exposed a local path: ${text}`)
  }
  negativeResults.push({ text, cols, phase })
}

function inspectRendered(cols: number, phase: string, source: string, path: string): void {
  let fragments = 0
  for (let row = 0; row < term.buffer.active.length; row++) {
    for (const link of terminalFileLinksAtRow(term.buffer.active, row).filter(link => link.documentRelative)) {
      assert(link.path === path, `${cols}/${phase}: hanging gutter/padding entered the target: ${link.path}`)
      for (let col = link.cellStart - 1; col < link.cellEnd; col++) {
        assert(terminalFileLinkAtCell(term.buffer.active, row, col)?.path === path, `${cols}/${phase}: rendered fragment lost a clickable cell`)
      }
      const selection = terminalFileSelectionAtCell(term.buffer.active, row, link.cellStart - 1, term.cols)!
      assert(selection?.path === path && selection.endRow > selection.startRow, `${cols}/${phase}: rendered reference selected only one fragment`)
      term.select(selection.startCol, selection.startRow, selection.length)
      const printed = term.getSelection()
      assert(printed.includes('docum') && printed.includes('ent-3.md'), `${cols}/${phase}: rendered selection lost a fragment`)
      term.clearSelection()
      fragments++
    }
  }
  assert(fragments >= 2, `${cols}/${phase}: hard-wrapped Markdown reference disappeared`)
  const gutters = Array.from({length: term.buffer.active.length}, (_, row) => terminalFileLinksAtRow(term.buffer.active, row))
    .flat().filter(link => link.referenceStart === -1)
  assert(gutters.some(link => link.path === source && link.lineStart === 45), `${cols}/${phase}: rendered source record lost its edit base`)
  renderedResults.push({cols, phase, path, fragments})
}

function inspectProse(cols: number, phase: string, path: string, selected: string): void {
  let fragments = 0
  for (let row = 0; row < term.buffer.active.length; row++) {
    for (const link of terminalFileLinksAtRow(term.buffer.active, row)) {
      assert(link.path === path, `${cols}/${phase}: prose gutter or prefix corrupted the target: ${link.path}`)
      for (let col = link.cellStart - 1; col < link.cellEnd; col++) {
        assert(terminalFileLinkAtCell(term.buffer.active, row, col)?.path === path, `${cols}/${phase}: prose fragment lost a clickable cell`)
      }
      const selection = terminalFileSelectionAtCell(term.buffer.active, row, link.cellStart - 1, term.cols)!
      assert(selection?.path === path, `${cols}/${phase}: prose reference has no shared selection`)
      term.select(selection.startCol, selection.startRow, selection.length)
      assert(term.getSelection() === selected, `${cols}/${phase}: prose selection lost physical fragments: ${JSON.stringify(term.getSelection())}`)
      term.clearSelection()
      fragments++
    }
  }
  assert(fragments >= 2, `${cols}/${phase}: renderer-wrapped prose disappeared`)
  proseResults.push({cols, phase, path, fragments})
}

const scenarioResults: Array<{id: string; family: string; cols: number; phase: string; fragments: number}> = []
function inspectScenario(scenario: TerminalPathScenario, cols: number, phase: string): void {
  const seen = new Set<string>()
  let fragments = 0
  for (let row = 0; row < term.buffer.active.length; row++) for (const link of terminalFileLinksAtRow(term.buffer.active, row)) {
    assert(scenario.paths.includes(link.path), `${scenario.id}/${cols}/${phase}: unexpected partial or altered target ${JSON.stringify(link.path)}`)
    seen.add(link.path); fragments++
    for (let col = link.cellStart - 1; col < link.cellEnd; col++) {
      assert(terminalFileLinkAtCell(term.buffer.active, row, col)?.path === link.path, `${scenario.id}/${cols}/${phase}: cell identity differs`)
    }
    assert(terminalFileSelectionAtCell(term.buffer.active, row, link.cellStart - 1, term.cols)?.path === link.path,
      `${scenario.id}/${cols}/${phase}: selection identity differs`)
  }
  assert(JSON.stringify([...seen].sort()) === JSON.stringify([...scenario.paths].sort()), `${scenario.id}/${cols}/${phase}: missing target`)
  scenarioResults.push({id: scenario.id, family: scenario.family, cols, phase, fragments})
}

async function run(): Promise<void> {
  for (const cols of widths) {
    for (const scenario of terminalPathScenarios) {
      try {
        term.reset(); term.resize(cols, 40)
        await write(scenario.rows.join('\r\n') + '\r\n')
        inspectScenario(scenario, cols, 'write')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectScenario(scenario, changed, 'reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
    term.resize(cols, 40)
    for (const sample of samples) {
      try {
        term.reset()
        await write((sample.unprefixed ? '' : '文件 ') + '\x1b[32m' + sample.text + '\x1b[0m\r\n')
        inspect(sample, cols, 'write')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspect(sample, changed, 'reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
    for (const text of negativeSamples) {
      try {
        term.reset()
        await write(text + '\r\n')
        inspectNegative(text, cols, 'write')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectNegative(text, changed, 'reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
    term.reset()
    term.resize(cols, 40)
    await write('Edited topics/中文目录/文文/long-report.md (+2, -2)\r\n20 + source\r\n21 + next\r\n')
    let gutter = false
    for (let row = 0; row < term.buffer.active.length; row++) {
      const link = editedDiffLineLinkFromBuffer(term.buffer.active, row)
      if (link?.lineStart === 21 && link.path === 'topics/中文目录/文文/long-report.md') gutter = true
    }
    if (!gutter) failures.push(`${cols}: wrapped edit header lost its file`)
    term.reset()
    term.resize(cols, 40)
    await write('⏺ Update(topics/中文目录/文文/long-report.md)\r\n  ⎿  Added 6 lines, removed 4 lines\r\n      20  context\r\n      21 +next\r\n')
    let claudeGutter = false
    for (let row = 0; row < term.buffer.active.length; row++) {
      const link = editedDiffLineLinkFromBuffer(term.buffer.active, row)
      if (link?.lineStart === 21 && link.path === 'topics/中文目录/文文/long-report.md') claudeGutter = true
    }
    if (!claudeGutter) failures.push(`${cols}: Claude edit summary/wrapped header lost its file`)
    try {
      const source = 'collab/work/sampletool-ng文文-20300101/新版文文重文文-20300103/audit/本文结文.md'
      const reference = '../workspace/output/DEMO文文文文文/DEMO基线文文与文文文文文文文-v2-20300103.md'
      const expected = './collab/work/sampletool-ng文文-20300101/新版文文重文文-20300103/workspace/output/DEMO文文文文文/DEMO基线文文与文文文文文文文-v2-20300103.md'
      term.reset()
      await write(`Added ${source} (+28 -0)\r\n1 +# 本文结文\r\n2 +\r\n3 +文文是 [DEMO基线 v2](${reference})，文文已同文。\r\n`)
      inspectDocument(cols, 'write', expected, reference)
      for (const changed of [32, 120, cols]) {
        term.resize(changed, 40)
        inspectDocument(changed, 'reflow', expected, reference)
      }
    } catch (error) { failures.push(String(error)) }
    for (const header of ['• Added docs/audit/notes.md (+65 -0)', '  └ docs/audit/notes.md (+65 -0)', '• Added docs/audit/no\r\ntes.md (+65 -0)', '  └ docs/audit/no \r\n  tes.md (+65 -0)']) {
      try {
        term.reset()
        term.resize(cols, 40)
        // Explicit CRLF and a blank gutter are renderer wrapping, not xterm wrapping.
        await write(`${header}\r\n    44 +\r\n    45 +FAQ 文文文 [第文文文文](demo-case/workspace/\x1b[32mdocum\x1b[0m \r\n        ent-3.md)。文文行文文文文版 FAQ 文文文文、文文： \r\n        文文件文在来源／文文保文；\r\n    46 +\r\n`)
        inspectRendered(cols, 'write', 'docs/audit/notes.md', './docs/audit/demo-case/workspace/document-3.md')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectRendered(changed, 'reflow', 'docs/audit/notes.md', './docs/audit/demo-case/workspace/document-3.md')
        }
      } catch (error) { failures.push(String(error)) }
    }
    const prefix = 'topics/EX-0002-demo-文文路径文文文文文文与文果文文文文/文文文文文文/文文'
    const suffix = '文文文文-文文文文-v5-20300103.md'
    for (const prose of [
      {text: '● 文文，文文一文文文文文文文文文；文文文的结文文结一页在 \r\n  ~/code/sample-assets/skills/sampletool-tg2/design/sampl\r\n  etool-t3文文文文文-结文文结与文文文文-v1-20300105.md。\r\n  文和 Codex 说的\r\n', path: '~/code/sample-assets/skills/sampletool-tg2/design/sampletool-t3文文文文文-结文文结与文文文文-v1-20300105.md', selected: '~/code/sample-assets/skills/sampletool-tg2/design/sampl\n  etool-t3文文文文文-结文文结与文文文文-v1-20300105.md'},
      {text: '● 文文：结果在 ./docs/long-\r\n  report.html。\r\n', path: './docs/long-report.html', selected: './docs/long-\n  report.html'},
      {text: '● 已记录\r\n  docs/sampl\r\n  etool/design/long-repor\r\n  t.md。\r\n', path: 'docs/sampletool/design/long-report.md', selected: 'docs/sampl\n  etool/design/long-repor\n  t.md'},
      {text: '  • Saved\r\n    /work/中文目录/long-repo\r\n    rt.md。\r\n', path: '/work/中文目录/long-report.md', selected: '/work/中文目录/long-repo\n    rt.md'},
      {text: '● Update(/work/skills/sampletoo\r\n        l-tg2/design/场文文文-文\r\n        文文.md)\r\n', path: '/work/skills/sampletool-tg2/design/场文文文-文文文.md', selected: '/work/skills/sampletoo\n        l-tg2/design/场文文文-文\n        文文.md'},
      {text: '● Update("docs/my \r\n         report  final.md")\r\n', path: 'docs/my  report  final.md', selected: 'docs/my \n         report  final.md'},
      {text: '● Read(docs/中文文文/long-\r\n      report.md · lines 12-20)\r\n', path: 'docs/中文文文/long-report.md', selected: 'docs/中文文文/long-\n      report.md'},
      {text: '文件：/home/tester/work/example-workspace/collab/work/s\r\n  ampletool-ng文文-20300101/文文-20300104-DemoX文文重文/\r\n  文文重文-sampletool-tg文文文文与文文-DemoX-v1-20300104.\r\n  md（393 行，第一文文文是全文结文，文文行文在附录 A）。\r\n', path: '/home/tester/work/example-workspace/collab/work/sampletool-ng文文-20300101/文文-20300104-DemoX文文重文/文文重文-sampletool-tg文文文文与文文-DemoX-v1-20300104.md', selected: '/home/tester/work/example-workspace/collab/work/s\n  ampletool-ng文文-20300101/文文-20300104-DemoX文文重文/\n  文文重文-sampletool-tg文文文文与文文-DemoX-v1-20300104.\n  md'},
      {text: '  文件：./docs/long-report.doc\r\n  x（已保存）\r\n', path: './docs/long-report.docx', selected: './docs/long-report.doc\n  x'},
      {text: `  文已文读文文文文的文文与文文文文、文版文文、文文线原文文文文 CEO 文文。文文结果保存文《文文文文文文·文文文文 v5》 (\x1b[32m${prefix}\x1b[0m\r\n  ${suffix})，文文原文文文定位和文文文文。\r\n`, path: prefix + suffix, selected: prefix + '\n  ' + suffix},
      {text: '  记录 "docs/my \r\n   report.md" 已保存。\r\n', path: 'docs/my  report.md', selected: 'docs/my \n   report.md'},
      {text: '• 记录 [报告](docs/文\r\n  文文文/报\r\n  告.md)已保存。\r\n', path: 'docs/文文文文/报告.md', selected: 'docs/文\n  文文文/报\n  告.md'},
    ]) {
      try {
        term.reset()
        term.resize(cols, 40)
        await write(prose.text)
        inspectProse(cols, 'write', prose.path, prose.selected)
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectProse(changed, 'reflow', prose.path, prose.selected)
        }
      } catch (error) { failures.push(String(error)) }
    }
    for (const prefix of ['https://example.com/', '${ROOT}/', 'docs/<name>/']) {
      try {
        term.reset()
        term.resize(cols, 40)
        const text = `  输出 "${prefix}\r\n  report.md"\r\n`
        await write(text)
        inspectNegative(text, cols, 'hard-prose')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectNegative(text, changed, 'hard-prose-reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
    for (const prefix of ['https://example.com/', '${ROOT}/', 'docs/<name>/', 'C:/work/']) {
      try {
        term.reset()
        term.resize(cols, 40)
        const text = `● 保存到 ${prefix}long-\r\n  report.md。\r\n`
        await write(text)
        inspectNegative(text, cols, 'bare-paragraph')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectNegative(text, changed, 'bare-paragraph-reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
    for (const prefix of ['https://example.com/', '${ROOT}/', 'docs/<name>/', 'C:/work/']) {
      try {
        term.reset()
        term.resize(cols, 40)
        const text = `  文件：${prefix}long-\r\n  report.md（已保存）\r\n`
        await write(text)
        inspectNegative(text, cols, 'labelled-hard-prose')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectNegative(text, changed, 'labelled-hard-prose-reflow')
        }
      } catch (error) { failures.push(String(error)) }
    }
  }
  const checks = results.length + documentResults.length + negativeResults.length + renderedResults.length + proseResults.length + scenarioResults.length
  const report = { widths, sample_count: samples.length, negative_sample_count: negativeSamples.length, checks, document_context_checks: documentResults.length, negative_checks: negativeResults.length, rendered_record_checks: renderedResults.length, prose_record_checks: proseResults.length, failures, results, documentResults, negativeResults, renderedResults, proseResults, scenarioResults,
    method: 'Installed xterm with Unicode 11; actual writes, ANSI, buffer cells, resize/reflow; production path helpers.' }
  ;(window as unknown as { pathLayoutReport: unknown }).pathLayoutReport = report
  result.textContent = JSON.stringify({ checks, failures }, null, 2)
  result.dataset.complete = 'true'
}
void run().catch(error => { result.textContent = String(error); result.dataset.complete = 'failed' })
