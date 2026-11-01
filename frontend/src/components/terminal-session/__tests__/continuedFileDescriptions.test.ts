import { describe, expect, it } from 'bun:test'
import { terminalFileLinksAtRow, terminalFileLinkAtCell } from '../terminalFileLinks'
import { cliHardWrap, transcriptCellBuffer } from './fixtures/transcriptPathLayouts'

// Authored synthetic examples. Literal rows keep the wrapping oracle separate
// from both the production parser and the simulated CLI formatter.
const targets = [
  '…/中文示例目录/12B-B2B示例报告-100X与9000-v2-20300104.md',
  '…/中文示例目录/work/当前笔记.修订说明.md',
  '…/样例配置设计/子topic-示例数据覆盖/work/子topic-示例数据覆盖.当前笔记.md',
]
const syntheticRows = [
  '  已改的文件：',
  '  - …/中文示例目录/12B-B2B示例报告-100X与90',
  '    00-v2-20300104.md：这里是虚构说明，',
  '    下一行继续描述示例内容。',
  '  - …/中文示例目录/work/当前笔记.',
  '    修订说明.md：此处演示文件名中的点号。',
  '  - …/样例配置设计/子topic-示例数据覆盖/wor',
  '    k/子topic-示例数据覆盖.当前笔记.md：',
  '    这里演示目录名和说明文字的分界。',
]
function links(rows: string[], columns = 0) {
  const buffer = transcriptCellBuffer(rows, columns)
  return { buffer, links: Array.from({ length: buffer.length }, (_, row) => terminalFileLinksAtRow(buffer, row)).flat() }
}
function paths(rows: string[], columns = 0) { return [...new Set(links(rows, columns).links.map(link => link.path))] }

describe('rendered file descriptions retain their complete hanging reference', () => {
  it('restores the synthetic paths instead of exposing filename tails', () => {
    for (const columns of [0, 28, 32, 40, 54, 60, 80, 120]) expect(paths(syntheticRows, columns)).toEqual(targets)
  })
  it('clicks every physical fragment, including the basename, as the complete synthetic reference', () => {
    for (const columns of [32, 40, 60]) {
      const found = links(syntheticRows, columns)
      for (const link of found.links) {
        expect(targets).toContain(link.path)
        for (let col = link.cellStart - 1; col < link.cellEnd; col++) expect(terminalFileLinkAtCell(found.buffer, link.bufferRow, col)?.path).toBe(link.path)
      }
    }
  })
  it('handles ASCII/fullwidth description colons, multiple extensions and source ranges', () => {
    const pathsToTest = [targets[0]!, targets[1]!, '~/code/skills/白文文文/work/当前文文.文文说明.md', 'docs/prototype/report-chain.html', 'docs/中文目录/测试结果.json']
    for (const path of pathsToTest) for (const separator of ['：', ':', ':121-130：', ':121:7：']) for (const prefix of ['- ', '  - ', '  • ']) for (const width of [28, 36, 40, 54, 60, 78]) {
      const rows = cliHardWrap(prefix + path + separator + '文文说明已文新，文文文附录。', width, prefix.length)
      const found = links(rows, 32)
      expect([...new Set(found.links.map(link => link.path))]).toEqual([path])
      if (separator.startsWith(':121')) expect(found.links.every(link => link.lineStart === 121 && link.lineEnd === (separator.startsWith(':121-') ? 130 : 121))).toBe(true)
    }
  })
  it('still stops at a real new label or completed neighboring file', () => {
    for (const label of ['文件：', '定位文件：', '来源:', '另一个：']) {
      expect(paths(['  - docs/some-incomplete-folder/', '    ' + label + 'docs/other.md'])).not.toContain('docs/some-incomplete-folder/docs/other.md')
    }
    expect(paths(['  - docs/complete.md', '    next.md：说明'])).toEqual(['docs/complete.md', 'next.md'])
    expect(paths(['  - docs/long-directory/', '    complete.md', '    next.md：说明'])).toEqual(['docs/long-directory/complete.md', 'next.md'])
    expect(paths(['  - docs/partial-', '', '    result.md：说明'])).not.toContain('docs/partial-result.md')
    expect(paths(['  - docs/partial-', '      result.md：说明'])).not.toContain('docs/partial-result.md')
  })
  it('does not recover local-looking tails from wrapped foreign or opaque targets', () => {
    for (const path of ['https://example.org/中文目录/very-long-report.md', '${ROOT}/中文目录/very-long-report.md', 'docs/*/very-long-report.md']) {
      const rows = cliHardWrap('  - ' + path + '：文文说明。', 32, 4)
      expect(paths(rows, 28)).toEqual([])
    }
  })
})
