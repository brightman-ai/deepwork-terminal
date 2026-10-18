import { describe, expect, it } from 'bun:test'
import { diffLineNumber, editedDiffLineLink, editedFileHeader, findTerminalFileLinks, parseFileLocationSuffix, terminalCellRange } from '../terminalFileLinks'

describe('findTerminalFileLinks', () => {
  it('finds supported relative paths in prose and parentheses', () => {
    const line = 'prototype/vocabulary/index.html (docs/product/voicemore-reading/prototype/vocabulary/index.html)'
    expect(findTerminalFileLinks(line).map((link) => link.path)).toEqual([
      'prototype/vocabulary/index.html',
      'docs/product/voicemore-reading/prototype/vocabulary/index.html',
    ])
  })

  it('keeps dot-prefixed directory names intact', () => {
    const path = 'docs/.tg/work/20261014-091418-five-feature-ui-redesign/implementation/FIVE-NEXT/SC-REVIEW/HANDOFF-20261017.md'
    expect(findTerminalFileLinks(`(${path})`).map((link) => link.path)).toEqual([path])
  })

  it('keeps Unicode directory and filename segments intact', () => {
    const path = 'collab/work/whiteboard-ng自举-2026001/交接-2026017-交流转换重构/reviews/wbng_fable_review_prompt_20261018.md'
    expect(findTerminalFileLinks(`< '${path}' >`).map((link) => link.path)).toEqual([path])
  })

  it('makes rendered Codex @./ references clickable while preserving the real relative path', () => {
    expect(findTerminalFileLinks('@./tmp/clip/10-18-02/0237001-12631d7e0ff8d571.png')).toEqual([{
      path: './tmp/clip/10-18-02/0237001-12631d7e0ff8d571.png', start: 0, end: 48, displayLength: 49,
    }])
  })

  it('maps a path after wide CJK cells to the visual xterm columns', () => {
    const cells = [{ chars: '中', width: 2 }, { chars: ' ', width: 1 }, { chars: 'a', width: 1 }, { chars: '.', width: 1 }, { chars: 'm', width: 1 }, { chars: 'd', width: 1 }]
    expect(terminalCellRange(cells, 2, 4)).toEqual({ start: 3, end: 6 })
  })

  it('ignores unsupported extensions and URLs', () => {
    expect(findTerminalFileLinks('https://example.com/report.html backup.bin')).toEqual([])
  })

  it('keeps range indexes aligned with the source line', () => {
    const line = 'open docs/readme.md now'
    expect(findTerminalFileLinks(line)).toEqual([{ path: 'docs/readme.md', start: 5, end: 18 }])
  })

  it('extracts a one-based source line from a colon suffix without changing the path', () => {
    expect(findTerminalFileLinks('open workflow_tg/WF-TG-GUARDRAILS.md:48 now')).toEqual([
      { path: 'workflow_tg/WF-TG-GUARDRAILS.md', start: 5, end: 35, displayLength: 34, lineStart: 48, lineEnd: 48 },
    ])
  })

  it('extracts an inclusive line range from a colon suffix', () => {
    expect(findTerminalFileLinks('open src/app.ts:20:22')).toEqual([
      { path: 'src/app.ts', start: 5, end: 14, displayLength: 16, lineStart: 20, lineEnd: 22 },
    ])
  })

  it('rejects malformed or zero line locations', () => {
    expect(parseFileLocationSuffix(':0')).toEqual({})
    expect(parseFileLocationSuffix(':20:19')).toEqual({})
    expect(findTerminalFileLinks('src/app.ts:20x')).toEqual([
      { path: 'src/app.ts', start: 0, end: 9 },
    ])
  })

  it('recognizes edited-file headers and their following diff line gutter', () => {
    const header = '• Edited docs/.tg/work/implementation-notes.md (+2 −2)'
    expect(editedFileHeader(header)).toBe('docs/.tg/work/implementation-notes.md')
    expect(diffLineNumber('20  −')).toBe(20)
    expect(diffLineNumber('22  −─ changed')).toBe(22)
    expect(diffLineNumber('21  +')).toBe(21)
    expect(diffLineNumber('not a diff row')).toBeUndefined()
    expect(editedDiffLineLink('22  −─ changed', [
      '21  +─ changed',
      header,
    ])).toMatchObject({ path: 'docs/.tg/work/implementation-notes.md', lineStart: 22, lineEnd: 22, displayLength: 2 })
    expect(editedDiffLineLink('22  −─ changed', ['', header])).toBeUndefined()
  })
})
