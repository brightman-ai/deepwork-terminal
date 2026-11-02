import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks, terminalFileLinkAtCell, terminalFileLinksAtRow, terminalFileSelectionAtCell } from '../terminalFileLinks'
import { resolveTerminalPath } from '../terminalPathResolver'
import { cliHardWrap, transcriptCellBuffer } from './fixtures/transcriptPathLayouts'

const target = '/work/other-project/skills/example-tool/evals/读数/20300102-中文测试报告-两种情况.md'
const frozenRows = [
  '  文档已保存。文件在',
  '  /work/other-project/skills/example-tool/ev',
  '  als/读数/20300102-中文测试报告-两种情况.md，已完成。',
]
function inspect(rows: string[], columns = 0) {
  const buffer = transcriptCellBuffer(rows, columns)
  const links = Array.from({ length: buffer.length }, (_, row) => terminalFileLinksAtRow(buffer, row)).flat()
  return { buffer, links, paths: [...new Set(links.map(link => link.path))] }
}

describe('plain assistant paragraphs preserve complete file references', () => {
  it('recovers a rooted reference without a paragraph bullet from either hard row', () => {
    for (const columns of [0, 24, 32, 40, 54, 80, 120]) {
      const { buffer, links, paths } = inspect(frozenRows, columns)
      expect(paths).toEqual([target])
      expect(links.some(link => link.bufferRow === 1)).toBe(true)
      for (const link of links) for (let col = link.cellStart - 1; col < link.cellEnd; col++) {
        const hit = terminalFileLinkAtCell(buffer, link.bufferRow, col)
        expect(hit?.path).toBe(target)
        expect(resolveTerminalPath(hit!.path)).toEqual({ kind: 'direct', path: target })
        expect(terminalFileSelectionAtCell(buffer, link.bufferRow, col, columns || 120)).toBeDefined()
      }
    }
  })

  it('recovers absolute, home, parent-relative and project-relative fields at mixed hard and soft wraps', () => {
    for (const path of [target, '~/work/示例目录/报告.md', '../other-project/报告.md', './docs/中文报告.md', 'docs/示例目录/报告.md']) {
      for (const prefix of ['  文件在 ', '  ', '']) for (const width of [24, 28, 32, 40, 54, 60]) {
        const rows = cliHardWrap(prefix + path + '，可阅读。', width, 2)
        for (const columns of [0, 20, 36, 80]) expect(inspect(rows, columns).paths).toEqual([path])
      }
    }
  })

  it('keeps paths complete when a slash, multi-part extension or source range starts a hard row', () => {
    for (const path of [target, '/work/demo/reports/报告.修订说明.md', '/work/demo/reports/报告.md:121-130', '/work/demo/reports/报告.md:121:7']) {
      for (const width of [24, 28, 32, 36, 40, 48, 54]) {
        const expected = path.replace(/:\d.*$/, '')
        const { links, paths } = inspect(cliHardWrap('  文件在 ' + path + '，已保存。', width, 2), 28)
        expect(paths).toEqual([expected])
        if (path.includes(':121')) expect(links.every(link => link.lineStart === 121)).toBe(true)
      }
    }
  })

  it('treats an introductory preposition in prose parentheses as prose while preserving quoted literal spaces', () => {
    for (const introduction of ['在', '位于', '见']) for (const [open, close] of [['（', '）'], ['(', ')']]) {
      for (const path of [target, '/work/demo/my report.md', '~/work/中文目录/my report.md']) {
        const text = '● 报告已保存' + open + introduction + ' ' + path + close + '，可阅读。'
        expect(findTerminalFileLinks(text).map(link => link.path)).toEqual([path])
        for (const width of [24, 32, 40, 54, 80]) expect(inspect(cliHardWrap(text, width, 2), 28).paths).toEqual([path])
      }
    }
    for (const text of ['（docs/中文 report.md）', '(docs/my report.md)', '("在 /work/demo/report.md")', '`在 /work/demo/report.md`']) {
      const expected = text.includes('在 ') ? '在 /work/demo/report.md' : text.includes('中文') ? 'docs/中文 report.md' : 'docs/my report.md'
      expect(findTerminalFileLinks(text).map(link => link.path)).toEqual([expected])
    }
  })

  it('does not join a complete file, blank line, label, new item or different hanging gutter', () => {
    const first = '  /work/other-project/long-directory/'
    for (const next of ['', '    report.md', '  - report.md', '  来源：docs/other.md', '  ● Read(docs/other.md)']) {
      expect(inspect([first, next, '  report.md']).paths).not.toContain('/work/other-project/long-directory/report.md')
    }
    expect(inspect(['  /work/demo/complete.md', '  next.md']).paths).toEqual(['/work/demo/complete.md', 'next.md'])
    expect(inspect(['  /work/demo/short/', '  independent-report.md']).paths).not.toContain('/work/demo/short/independent-report.md')
  })

  it('does not expose a local-looking suffix from wrapped URLs, templates or foreign roots', () => {
    for (const path of ['https://example.org/long-folder/report.md', '${ROOT}/long-folder/report.md', 'docs/<name>/long-folder/report.md', 'C:/work/long-folder/report.md']) {
      for (const width of [24, 28, 32, 40]) expect(inspect(cliHardWrap('  文件在 ' + path + '，可阅读。', width, 2), 28).paths).toEqual([])
    }
  })
})
