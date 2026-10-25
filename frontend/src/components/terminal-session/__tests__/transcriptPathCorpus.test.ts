import { describe, expect, it } from 'bun:test'
import corpus from './fixtures/transcriptPathCorpus.json'
import { cliHardWrap, renderTranscriptPath, transcriptCellBuffer, type TranscriptLayout } from './fixtures/transcriptPathLayouts'
import { terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../terminalFileLinks'

const layouts: TranscriptLayout[] = ['claude-read', 'claude-update', 'claude-write-pair', 'codex-edited', 'codex-added', 'prose-label', 'prose-bare', 'prose-markdown']

export const corpusLayouts = (sample: typeof corpus.cases[number], width: number): string[][] => {
  if ('wrapper' in sample) {
    const reference = sample.wrapper === 'markdown' ? `[文档](${sample.path})` : '`' + sample.path + '`'
    return [cliHardWrap('● ' + reference + '，请查看。', width, 2)]
  }
  return layouts.map(layout => renderTranscriptPath(layout, sample.path, width))
}

describe('200-transcript study: independent, minimized reference identities', () => {
  for (const sample of corpus.cases) it(`${sample.agent}/${sample.operation}/${sample.id}`, () => {
    for (const hardWidth of [0, 32, 54, 80]) for (const rows of corpusLayouts(sample, hardWidth)) {
      for (const softWidth of [0, 24, 39]) {
        const buffer = transcriptCellBuffer(rows, softWidth)
        const seen = new Set<string>()
        for (let row = 0; row < buffer.length; row++) for (const link of terminalFileLinksAtRow(buffer, row)) {
          expect(link.path).toBe(sample.path)
          seen.add(link.path)
          for (const col of [link.cellStart - 1, link.cellEnd - 1]) {
            expect(terminalFileLinkAtCell(buffer, row, col)?.path).toBe(sample.path)
            expect(terminalFileSelectionAtCell(buffer, row, col, softWidth || 256)?.path).toBe(sample.path)
          }
        }
        expect([...seen]).toEqual([sample.path])
      }
    }
  })
})

describe('explicit adversarial controls (not claimed as observed tool targets)', () => {
  for (const path of ['docs/my  report(final).md', '/work/中文 目录/报告（终稿）.md',
    './docs/audit(+4,-2).md', 'docs/cafe\u0301.md']) it(`preserves literal characters: ${path}`, () => {
    for (const layout of layouts.filter(l => !['prose-label', 'prose-bare'].includes(l))) {
      for (const hardWidth of [0, 32, 54]) {
        const buffer = transcriptCellBuffer(renderTranscriptPath(layout, path, hardWidth), 24)
        const seen = new Set(Array.from({length: buffer.length}, (_, row) => terminalFileLinksAtRow(buffer, row)).flat().map(l => l.path))
        expect([...seen]).toEqual([path])
      }
    }
  })
})
