/** Browser system check using the installed xterm and the production helpers.
 * Bundle: bun build scripts/terminal-path-layout-harness.ts --target browser --outfile <fixture>/harness.js
 * Load from a fixture HTML containing #terminal and #result. No product state is mocked.
 */
import { Terminal } from '../frontend/node_modules/@xterm/xterm'
import { enableUnicode11 } from '../frontend/src/components/terminal-session/terminalUnicode'
import { editedDiffLineLinkFromBuffer, findWrappedTerminalFileLinks, terminalCellRange, wrappedTerminalRows, terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../frontend/src/components/terminal-session/terminalFileLinks'

const samples = [
  { text: '  - 交流提纲 v2（现行草稿，8 页加 1 页可选）：/home/ubuntu/code/automation-design/docs/topics/TH-1022-中行AI安全交流/中行AI安全交流-交流提纲-v2-20261022.md', path: '/home/ubuntu/code/automation-design/docs/topics/TH-1022-中行AI安全交流/中行AI安全交流-交流提纲-v2-20261022.md', selected: '/home/ubuntu/code/automation-design/docs/topics/TH-1022-中行AI安全交流/中行AI安全交流-交流提纲-v2-20261022.md', unprefixed: true },
  { text: '提纲（现行）：docs/报告(final).md:21', path: 'docs/报告(final).md', selected: 'docs/报告(final).md:21', line: 21, unprefixed: true },
  { text: '(optional):docs/report.md', path: 'docs/report.md', selected: 'docs/report.md', unprefixed: true },
  { text: 'Draft [optional]:README.md', path: 'README.md', selected: 'README.md', unprefixed: true },
  { text: '  完整研究记录 (docs/.tg/work/20261019-124419-ios-keyboard-structural-repair/web-research-560557684-20261020.md)已保存。', path: 'docs/.tg/work/20261019-124419-ios-keyboard-structural-repair/web-research-560557684-20261020.md', selected: 'docs/.tg/work/20261019-124419-ios-keyboard-structural-repair/web-research-560557684-20261020.md', unprefixed: true },
  { text: 'The result (docs/report.md).', path: 'docs/report.md', selected: 'docs/report.md', unprefixed: true },
  { text: '3 +[产研组织的原始对话对照](case-产研组织.md)', path: 'case-产研组织.md', selected: 'case-产研组织.md', unprefixed: true },
  { text: '**docs/报告.md**', path: 'docs/报告.md', selected: 'docs/报告.md' },
  { text: '**docs/报告.md 已更新**', path: 'docs/报告.md', selected: 'docs/报告.md' },
  { text: '**附带**：CONFIG.md', path: 'CONFIG.md', selected: 'CONFIG.md' },
  { text: '文件:docs/report.md：说明', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: '文件：（来源：docs/report.md）', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: '时间：2026-10-16｜来源：scratchpad/盲评.out（说明）', path: 'scratchpad/盲评.out', selected: 'scratchpad/盲评.out' },
  { text: '`[Ref: guide/troubleshooting/ime-shift-double-toggle.md]`', path: 'guide/troubleshooting/ime-shift-double-toggle.md', selected: 'guide/troubleshooting/ime-shift-double-toggle.md' },
  { text: '`> 记录 work/topic.log.md：更新到 #617｜整份理到 #615`', path: 'work/topic.log.md', selected: 'work/topic.log.md' },
  { text: '`python3 -m py_compile skills/demo/scripts/run.py`', path: 'skills/demo/scripts/run.py', selected: 'skills/demo/scripts/run.py' },
  { text: '"docs/name=report.md"', path: 'docs/name=report.md', selected: 'docs/name=report.md' },
  { text: '路径：（docs/中文 report.md）', path: 'docs/中文 report.md', selected: 'docs/中文 report.md' },
  { text: '文件：topics/TH-0628-xge-NDR-X-Graph引擎细化与场景验证/讲解/S1商品分档与KA报预算/S1商品分档与KA报预算-自动档卖给谁多宽怎么收-v4-20261019.md（v1 到 v3 在同一目录）', path: 'topics/TH-0628-xge-NDR-X-Graph引擎细化与场景验证/讲解/S1商品分档与KA报预算/S1商品分档与KA报预算-自动档卖给谁多宽怎么收-v4-20261019.md', selected: 'topics/TH-0628-xge-NDR-X-Graph引擎细化与场景验证/讲解/S1商品分档与KA报预算/S1商品分档与KA报预算-自动档卖给谁多宽怎么收-v4-20261019.md' },
  { text: '文件:docs/report.md(v1 and v3 in the same directory)', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: 'docs/report.md【附注】', path: 'docs/report.md', selected: 'docs/report.md' },
  { text: 'docs/report.md(final).txt:121', path: 'docs/report.md(final).txt', line: 121 },
  { text: '  - v3：topics/TH-0628-xge-NDR-X-Graph引擎细化与场景验证/讲解/S1商品分档与KA报预算/S1商品分档与KA报预算-自动档卖给谁多宽怎么收-v3-20261019.md', path: 'topics/TH-0628-xge-NDR-X-Graph引擎细化与场景验证/讲解/S1商品分档与KA报预算/S1商品分档与KA报预算-自动档卖给谁多宽怎么收-v3-20261019.md', selected: 'topics/TH-0628-xge-NDR-X-Graph引擎细化与场景验证/讲解/S1商品分档与KA报预算/S1商品分档与KA报预算-自动档卖给谁多宽怎么收-v3-20261019.md' },
  { text: '  - v3:topics/TH-0628/讲解/报告.md', path: 'topics/TH-0628/讲解/报告.md', selected: 'topics/TH-0628/讲解/报告.md' },
  { text: '  - 第三场纪要：docs/meetings/20261019-S1商品分档-快路径定位调整3/20261019-S1商品分档-快路径定位调整3.md', path: 'docs/meetings/20261019-S1商品分档-快路径定位调整3/20261019-S1商品分档-快路径定位调整3.md', selected: 'docs/meetings/20261019-S1商品分档-快路径定位调整3/20261019-S1商品分档-快路径定位调整3.md' },
  { text: '  - 第三场纪要:docs/meetings/纪要.md', path: 'docs/meetings/纪要.md', selected: 'docs/meetings/纪要.md' },
  { text: '文件:docs/report.md:121', path: 'docs/report.md', line: 121, selected: 'docs/report.md:121' },
  { text: 'docs/报告：2026.md', path: 'docs/报告：2026.md' },
  { text: 'Bash(until [ -s "docs/meetings/20261019-S1商品分档-快路径定位调整3/asr/meeting.qwen.txt" ]; do sleep 5; done; cat "docs/meetings/20261019-S1商品分档-快路径定位调整3/asr/meeting.qwen.txt")', path: 'docs/meetings/20261019-S1商品分档-快路径定位调整3/asr/meeting.qwen.txt', selected: 'docs/meetings/20261019-S1商品分档-快路径定位调整3/asr/meeting.qwen.txt' },
  { text: '(collab/work/whiteboard-ng自举-20261001/设计逐层校准-20261019/系统设计的层次与校准.md)已落盘。', path: 'collab/work/whiteboard-ng自举-20261001/设计逐层校准-20261019/系统设计的层次与校准.md', selected: 'collab/work/whiteboard-ng自举-20261001/设计逐层校准-20261019/系统设计的层次与校准.md' },
  { text: '文件：.../UEBA工程架构评审/UEBA基线-v2-20261019.md', path: '.../UEBA工程架构评审/UEBA基线-v2-20261019.md', selected: '.../UEBA工程架构评审/UEBA基线-v2-20261019.md' },
  { text: '（docs/报告（终稿）.md）已生成', path: 'docs/报告（终稿）.md', selected: 'docs/报告（终稿）.md' },
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
const negativeSamples = [
  'https://<host>/docs/report.md', 'work/<件名>-专家读.md', '${ROOT}/report.md',
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
      assert(printed.includes('respo') && printed.includes('nse-3.md'), `${cols}/${phase}: rendered selection lost a fragment`)
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

async function run(): Promise<void> {
  for (const cols of widths) {
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
    await write('Edited topics/中文目录/讲解/long-report.md (+2, -2)\r\n20 + source\r\n21 + next\r\n')
    let gutter = false
    for (let row = 0; row < term.buffer.active.length; row++) {
      const link = editedDiffLineLinkFromBuffer(term.buffer.active, row)
      if (link?.lineStart === 21 && link.path === 'topics/中文目录/讲解/long-report.md') gutter = true
    }
    if (!gutter) failures.push(`${cols}: wrapped edit header lost its file`)
    term.reset()
    term.resize(cols, 40)
    await write('⏺ Update(topics/中文目录/讲解/long-report.md)\r\n  ⎿  Added 6 lines, removed 4 lines\r\n      20  context\r\n      21 +next\r\n')
    let claudeGutter = false
    for (let row = 0; row < term.buffer.active.length; row++) {
      const link = editedDiffLineLinkFromBuffer(term.buffer.active, row)
      if (link?.lineStart === 21 && link.path === 'topics/中文目录/讲解/long-report.md') claudeGutter = true
    }
    if (!claudeGutter) failures.push(`${cols}: Claude edit summary/wrapped header lost its file`)
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
    for (const header of ['• Added docs/audit/notes.md (+65 -0)', '  └ docs/audit/notes.md (+65 -0)', '• Added docs/audit/no\r\ntes.md (+65 -0)', '  └ docs/audit/no \r\n  tes.md (+65 -0)']) {
      try {
        term.reset()
        term.resize(cols, 40)
        // Explicit CRLF and a blank gutter are renderer wrapping, not xterm wrapping.
        await write(`${header}\r\n    44 +\r\n    45 +FAQ 成品见 [第三轮答复](faq-run-a/workspace/\x1b[32mrespo\x1b[0m \r\n        nse-3.md)。根执行者核了三版 FAQ 及问题树、解树： \r\n        旧条件只在来源／历史保留；\r\n    46 +\r\n`)
        inspectRendered(cols, 'write', 'docs/audit/notes.md', './docs/audit/faq-run-a/workspace/response-3.md')
        for (const changed of [32, 120, cols]) {
          term.resize(changed, 40)
          inspectRendered(changed, 'reflow', 'docs/audit/notes.md', './docs/audit/faq-run-a/workspace/response-3.md')
        }
      } catch (error) { failures.push(String(error)) }
    }
    const prefix = 'topics/TH-0923-gjlj-攻击路径治理商业设计与效果价值范式/数据飞轮壁垒/数据'
    const suffix = '飞轮壁垒-分析梳理-v5-20261019.md'
    for (const prose of [
      {text: '文件：/home/ubuntu/code/automation-design/collab/work/w\r\n  hiteboard-ng自举-20261001/交接-20261024-Fable独立重审/\r\n  独立重审-whiteboard-tg设计分析与方案-Fable-v1-20261024.\r\n  md（393 行，第一屏五段是全部结论，证据行号在附录 A）。\r\n', path: '/home/ubuntu/code/automation-design/collab/work/whiteboard-ng自举-20261001/交接-20261024-Fable独立重审/独立重审-whiteboard-tg设计分析与方案-Fable-v1-20261024.md', selected: '/home/ubuntu/code/automation-design/collab/work/w\n  hiteboard-ng自举-20261001/交接-20261024-Fable独立重审/\n  独立重审-whiteboard-tg设计分析与方案-Fable-v1-20261024.\n  md'},
      {text: '  文件：./docs/long-report.doc\r\n  x（已保存）\r\n', path: './docs/long-report.docx', selected: './docs/long-report.doc\n  x'},
      {text: `  我已核读这些会话的用户与助手消息、历版分析、业务线原始材料及 CEO 文档。完整结果保存为《数据飞轮壁垒·分析梳理 v5》 (\x1b[32m${prefix}\x1b[0m\r\n  ${suffix})，包含原始会话定位和逐项依据。\r\n`, path: prefix + suffix, selected: prefix + '\n  ' + suffix},
      {text: '  记录 "docs/my \r\n   report.md" 已保存。\r\n', path: 'docs/my  report.md', selected: 'docs/my \n   report.md'},
      {text: '• 记录 [报告](docs/数\r\n  据飞轮/报\r\n  告.md)已保存。\r\n', path: 'docs/数据飞轮/报告.md', selected: 'docs/数\n  据飞轮/报\n  告.md'},
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
  const checks = results.length + documentResults.length + negativeResults.length + renderedResults.length + proseResults.length
  const report = { widths, sample_count: samples.length, negative_sample_count: negativeSamples.length, checks, document_context_checks: documentResults.length, negative_checks: negativeResults.length, rendered_record_checks: renderedResults.length, prose_record_checks: proseResults.length, failures, results, documentResults, negativeResults, renderedResults, proseResults,
    method: 'Installed xterm with Unicode 11; actual writes, ANSI, buffer cells, resize/reflow; production path helpers.' }
  ;(window as unknown as { pathLayoutReport: unknown }).pathLayoutReport = report
  result.textContent = JSON.stringify({ checks, failures }, null, 2)
  result.dataset.complete = 'true'
}
void run().catch(error => { result.textContent = String(error); result.dataset.complete = 'failed' })
