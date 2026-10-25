import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks, terminalFileLinksAtRow } from '../terminalFileLinks'
import { transcriptCellBuffer } from './fixtures/transcriptPathLayouts'

const paths = (rows: string[]) => {
  const buffer = transcriptCellBuffer(rows)
  return [...new Set(rows.flatMap((_, row) => terminalFileLinksAtRow(buffer, row).map(link => link.path)))]
}

describe('model-reviewed failures from the 200-transcript study', () => {
  it('keeps Codex stats out of a target even when the stats themselves split', () => {
    expect(paths(['  • Edited /work/do', '  cs/报告.md (+4, -', '  2)'])).toEqual(['/work/docs/报告.md'])
    expect(paths(['  • Added /work/bi', '  n/tool (+', '  4 -0)'])).toEqual(['/work/bin/tool'])
  })
  it('retains a Markdown target when its closing delimiter is on a separate CLI row', () => {
    expect(paths(['● 已保存 [文档](/work/docs/报告.md', '  )，可查看。'])).toEqual(['/work/docs/报告.md'])
  })
  it('uses the earliest valid paragraph reference rather than its middle-directory suffix', () => {
    expect(paths(['● 已保存 /work/abcdefgh/', '  ijklmnop', '  qrstuv/docs/repor', '  t.md。'])).toEqual(['/work/abcdefgh/ijklmnopqrstuv/docs/report.md'])
  })
  it('uses the directly preceding Write title to disambiguate a slash-starting receipt continuation', () => {
    expect(paths(['● Write(/work/目录/report.md)', '  ⎿  Wrote 4 lines to /work/目录', '     /report.md'])).toEqual(['/work/目录/report.md'])
  })
  it('owns a complete Codex file field with literal spaces and internal parentheses', () => {
    expect(findTerminalFileLinks('  • Edited docs/my report(final).md (+4, -2)').map(l => l.path)).toEqual(['docs/my report(final).md'])
  })
  it('recognizes a filename in a book-title wrapper adjacent to Chinese prose', () => {
    expect(findTerminalFileLinks('我会读取《派单.md》，按其中要求继续。').map(l => l.path)).toEqual(['派单.md'])
  })
  it('keeps explicit local script names clickable in a backtick command', () => {
    expect(findTerminalFileLinks('先运行 `audit.py stamp`，再核对。').map(l => l.path)).toEqual(['audit.py'])
  })
  it('ends a complete filename before a double em dash prose note', () => {
    expect(findTerminalFileLinks('现在写 answer.md——附带说明。').map(l => l.path)).toEqual(['answer.md'])
    expect(findTerminalFileLinks('文件：/work/answer.md——附带说明。').map(l => l.path)).toEqual(['/work/answer.md'])
    expect(findTerminalFileLinks('文件：/work/answer——附带说明.md。').map(l => l.path)).toEqual(['/work/answer——附带说明.md'])
  })
  it('does not let English apostrophes swallow following quoted references', () => {
    expect(findTerminalFileLinks("The user's file is `build.sh`; I'll open it.").map(l => l.path)).toEqual(['build.sh'])
  })
  it('recognizes explicit references inside quoted prose without joining a prose list into a filename', () => {
    expect(findTerminalFileLinks('他说"读取 `notes/round-N.md`，然后结束"。').map(l => l.path)).toEqual(['notes/round-N.md'])
    expect(findTerminalFileLinks('Updated (evidence file, spec updates, status.yaml).').map(l => l.path)).toEqual(['status.yaml'])
    expect(findTerminalFileLinks("GitHub's warnings mention Node.js 20 actions.").map(l => l.path)).toEqual([])
    expect(findTerminalFileLinks('Read("Node.js")').map(l => l.path)).toEqual(['Node.js'])
  })
  it('keeps multi-part extensions through a split dot, underscore or unknown prefix', () => {
    for (const rows of [
      ['文件：/work/audit.intent.', '  md（已保存）'],
      ['● 已保存 /work/audit.questio', '  n_tree.md。'],
      ['● 已保存 /work/audit.m', '  d。'],
    ]) expect(paths(rows)).toEqual([rows[0].includes('intent') ? '/work/audit.intent.md' : rows[0].includes('questio') ? '/work/audit.question_tree.md' : '/work/audit.md'])
  })
  it('requires fixed-column field geometry and a prose terminator before joining a new root', () => {
    expect(paths(['● 已保存 /work/long-directory', '  /sub/report.md。'])).toEqual(['/work/long-directory/sub/report.md'])
    const independent = paths(['● 已保存 /work/long-directory', '  /sub/report.md'])
    expect(independent).not.toContain('/work/long-directory/sub/report.md')
    expect(paths(['文件：/work/long-directory/ab', '  cdefghijklmnopqrstuv/report.md', '  （已保存）'])).toEqual(['/work/long-directory/abcdefghijklmnopqrstuv/report.md'])
  })
})
