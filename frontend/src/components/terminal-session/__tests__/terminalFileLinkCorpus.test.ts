import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks, findWrappedTerminalFileLinks } from '../terminalFileLinks'

// Model-reviewed classes from the frozen 2026-10-16..20 transcript audit.
// Business text and machine-specific roots are shortened; syntax is preserved.
// The expected path AND exact clickable text are independent acceptance oracles.
const cases: Array<{ name: string; text: string; paths: string[]; printed?: string[]; line?: number }> = [
  { name: 'reported v4 fullwidth note', text: '文件：topics/讲解/报告-v4.md（v1 到 v3 在同一目录）', paths: ['topics/讲解/报告-v4.md'] },
  { name: 'ASCII note', text: '文件:docs/report.md(v1 and v3)', paths: ['docs/report.md'] },
  { name: 'bare basename with note', text: '读 SKILL.md（1.3 万字）', paths: ['SKILL.md'] },
  { name: 'trailing prose colon', text: '现在写主交付件 output/answer.md：', paths: ['output/answer.md'] },
  { name: 'adjacent prose after colon', text: 'SKILL.md：读者是谁', paths: ['SKILL.md'] },
  { name: 'ASCII prose colon', text: 'output/answer.md:done', paths: ['output/answer.md'] },
  { name: 'strong markdown', text: '**TOPIC.md**', paths: ['TOPIC.md'] },
  { name: 'strong starts before filename and ends after prose', text: '**~/.codex/AGENTS.md 被跳过了，这是对的。**', paths: ['~/.codex/AGENTS.md'] },
  { name: 'strong filename then colon', text: '- **TOPIC.md:** line 20', paths: ['TOPIC.md'] },
  { name: 'label follows decorated prose', text: '**附带**：CONFIG.md 过时接口已修', paths: ['CONFIG.md'] },
  { name: 'label in adjacent unmatched prose wrapper', text: '**27 个原语**（源：assert.go 注册表）', paths: ['assert.go'] },
  { name: 'label and wrappers alternate', text: '文件：（来源：docs/report.md）', paths: ['docs/report.md'] },
  { name: 'underscore decoration', text: '__docs/report.md__', paths: ['docs/report.md'] },
  { name: 'literal underscore directory', text: '__cache__/report.md', paths: ['__cache__/report.md'] },
  { name: 'literal underscore filename', text: 'docs/__draft__.md', paths: ['docs/__draft__.md'] },
  { name: 'inline reference marker', text: '统一标记 `[Ref: guide/troubleshooting/ime-shift-double-toggle.md]`', paths: ['guide/troubleshooting/ime-shift-double-toggle.md'] },
  { name: 'inline quoted Markdown prose', text: '`> 记录 work/topic.log.md：更新到 #617｜整份理到 #615`', paths: ['work/topic.log.md'] },
  { name: 'inline command with one file', text: '`python3 -m py_compile skills/demo/scripts/run.py`', paths: ['skills/demo/scripts/run.py'] },
  { name: 'inline command with separate operands', text: '`python3 scripts/check.py docs/report.md`', paths: ['scripts/check.py', 'docs/report.md'] },
  { name: 'inline command with a quoted spaced path', text: '`cat "docs/中文 report.md"`', paths: ['docs/中文 report.md'] },
  { name: 'literal quoted spaced path', text: '"docs/中文 report.md"', paths: ['docs/中文 report.md'] },
  { name: 'standalone fullwidth spaced path', text: '路径：（docs/中文 report.md）', paths: ['docs/中文 report.md'] },
  { name: 'literal filename containing equals', text: '"docs/name=report.md"', paths: ['docs/name=report.md'] },
  { name: 'literal filename internal colons', text: 'docs/报告.md：附件.md', paths: ['docs/报告.md：附件.md'] },
  { name: 'literal internal parentheses with location', text: 'docs/report.md(final).txt:121', paths: ['docs/report.md(final).txt'], printed: ['docs/report.md(final).txt:121'], line: 121 },
  { name: 'source line before annotation', text: 'docs/report.md:121（对应源码）', paths: ['docs/report.md'], printed: ['docs/report.md:121'], line: 121 },
  { name: 'format extension inside a whitespace-clipped prose note', text: '文件：docs/report.md（.docx 重出），643', paths: ['docs/report.md'], printed: ['docs/report.md'] },
  { name: 'literal filename brackets followed by a format note', text: 'docs/report.md(final).txt（.pdf 重出）', paths: ['docs/report.md(final).txt'], printed: ['docs/report.md(final).txt'] },
  { name: 'custom extension and fullwidth field separator', text: '时间：2026-10-16｜来源：scratchpad/blind/盲评.out（Sonnet，匿名）', paths: ['scratchpad/blind/盲评.out'] },
  { name: 'shell option value', text: 'Bash(cat --output=docs/out.txt;cat "docs/in.txt")', paths: ['docs/out.txt', 'docs/in.txt'] },
  { name: 'standalone angle wrapper', text: '<docs/report.md>', paths: ['docs/report.md'] },
  { name: 'Markdown label owns only destination', text: '[报告](docs/report.md#L121)', paths: ['docs/report.md'], printed: ['docs/report.md#L121'], line: 121 },
  { name: 'template does not expose a basename tail', text: 'work/<件名>-专家读.md', paths: [] },
  { name: 'HTTP template does not expose local tail', text: 'https://<host>/docs/report.md', paths: [] },
  { name: 'assignment is code, not a filename', text: 'target=d/path.name', paths: [] },
  { name: 'inline expression is not a quoted filename', text: '`target = d / target.name`', paths: [] },
  { name: 'measurement expression is not a path', text: '595.304pt×150/72=1240.22（Δ0.78 ceil）', paths: [] },
  { name: 'function expression is not an annotated file', text: 'datetime.datetime.fromtimestamp(t/1000).strftime("%H:%M:%S")', paths: [] },
  { name: 'regular expression is not a path to a dotfile', text: 'text(ALL_TOOLS.filter(x=>/browser|tool_search/.test(x.name)))', paths: [] },
  { name: 'regular expression flags are not directories', text: 'const r = /browser|claude/i.test(x.name)', paths: [] },
  { name: 'regular expression with escaped slash', text: '/docs\\/report.md/gi.exec(text)', paths: [] },
  { name: 'dynamic template does not invent an absolute tail', text: '`sed -n "1,220p" ${JSON.stringify(base+"/docs/report.md")}`', paths: [] },
  { name: 'static command operand beside an interpolation', text: '`sed -n ${range} docs/report.md`', paths: ['docs/report.md'] },
  { name: 'shell variable is not expanded', text: '${ROOT}/report.md', paths: [] },
  { name: 'glob is not a file', text: 'docs/**/*.md', paths: [] },
  { name: 'foreign URL is not a file', text: 'Bash(curl "https://example.com/docs/report.md")', paths: [] },
]

describe('reviewed transcript path grammar', () => {
  it('keeps the reported v2 Markdown path and excludes its format note across every soft wrap', () => {
    const path = '讲解/快模型推理加速适用边界/快模型推理加速适用边界-推理快慢原理-卡模型量化与PD分离-v2-20261024.md'
    const text = `稿是原地改的 v2，没另起 v3（同一天三版会乱）：${path}（.docx 重出），643`
    for (let split = 1; split < text.length; split++) {
      const rows = [{text: text.slice(0, split), isWrapped: false}, {text: text.slice(split), isWrapped: true}]
      const links = [0, 1].flatMap(row => findWrappedTerminalFileLinks(rows, row))
      expect([...new Set(links.map(link => link.path))]).toEqual([path])
      expect(links.map(link => rows[link.row].text.slice(link.rawStart, link.rawStart + link.rawLength)).join('')).toBe(path)
    }
  })
  it('keeps the reported parenthesized HTML and Markdown paths separate from adjacent Chinese prose across every soft wrap', () => {
    const base = 'docs/.tg/work/20261017-002300-reading-density-design/article-closure-20261020/feedback-20261024/'
    const paths = [base + 'comparison.html', base + 'design.md']
    const text = `  可以直接看七模型实际输出对照 (${paths[0]})，切换片段和档位比较。方案与词卡草图 (${paths[1]})也已落盘。词卡报`
    const links = findTerminalFileLinks(text)
    expect(links.map(x => x.path)).toEqual(paths)
    expect(links.map(x => text.slice(x.start, x.end + 1))).toEqual(paths)
    for (let split = 1; split < text.length; split++) {
      const rows = [{ text: text.slice(0, split), isWrapped: false }, { text: text.slice(split), isWrapped: true }]
      expect([...new Set([0, 1].flatMap(row => findWrappedTerminalFileLinks(rows, row).map(x => x.path)))]).toEqual(paths)
    }
  })
  it('composes labels, wrappers, decoration and trailing prose without moving path offsets', () => {
    for (const prefix of ['', '文件：', '文件:', '输出：文件：']) {
      for (const [open, close] of [['', ''], ['(', ')'], ['（', '）']]) {
        for (const decoration of ['', '**', '__']) {
          for (const note of ['', '（附注）', '：说明']) {
            for (const path of ['docs/报告.md', 'docs/report(final).md', './docs/app.ts']) {
              const text = prefix + open + decoration + path + decoration + close + note
              const links = findTerminalFileLinks(text)
              expect(links.map(link => link.path)).toEqual([path])
              expect(text.slice(links[0].start, links[0].end + 1)).toBe(path)
            }
          }
        }
      }
    }
  })

  it('separates prose annotations before a colon from the entire file target', () => {
    const reported = '/home/ubuntu/code/automation-design/docs/topics/TH-1022-中行AI安全交流/中行AI安全交流-交流提纲-v2-20261022.md'
    for (const label of ['  - 交流提纲 v2（现行草稿，8 页加 1 页可选）', '  - 提纲（草稿）', '(optional)', 'v2(8pages)', 'Draft [optional]', '说明【现行】', '说明《草稿》', '文件：（来源）', '说明（可选〔补充〕）']) {
      for (const colon of [':', '：']) {
        for (const path of [reported, 'docs/报告(final).md', 'README.md']) {
          const text = label + colon + path + ':21'
          const links = findTerminalFileLinks(text)
          expect(links.map(link => link.path)).toEqual([path])
          expect(links[0].lineStart).toBe(21)
          expect(text.slice(links[0].start, links[0].start + (links[0].displayLength ?? links[0].end - links[0].start + 1))).toBe(path + ':21')
        }
      }
    }
  })

  it('keeps opaque and literal references after annotated labels intact', () => {
    for (const target of ['https://example.com/docs/report.md', 'ssh://host/docs/report.md', 'C:/docs/report.md', '${ROOT}/docs/report.md', 'work/<件名>-report.md', 'target=docs/report.md']) {
      expect(findTerminalFileLinks('提纲（现行）：' + target)).toEqual([])
    }
    for (const path of ['docs/版本（草稿）/report.md', 'docs/report(final).md：附件.md', 'docs/说明【草稿】.md']) {
      const text = '提纲（现行）：' + path
      expect(findTerminalFileLinks(text).map(link => link.path)).toEqual([path])
    }
    expect(findTerminalFileLinks('"说明）：docs/report.md"').map(link => link.path)).toEqual(['说明）：docs/report.md'])
  })

  it('does not reinterpret long heredoc/quoted text as a placeholder path', () => {
    expect(findTerminalFileLinks('text("python3 - <<PY ' + '无路径文字'.repeat(600) + '");')).toEqual([])
  })
  for (const sample of cases) {
    it(sample.name, () => {
      const links = findTerminalFileLinks(sample.text)
      expect(links.map(link => link.path)).toEqual(sample.paths)
      expect(links.map(link => sample.text.slice(link.start, link.start + (link.displayLength ?? link.end - link.start + 1))))
        .toEqual(sample.printed ?? sample.paths)
      if (sample.line) expect(links.every(link => link.lineStart === sample.line)).toBe(true)
    })
  }
})
