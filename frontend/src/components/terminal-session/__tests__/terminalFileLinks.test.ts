import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks, terminalCellRange } from '../terminalFileLinks'

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

  it('leaves Codex @./ references for the Codex input handler', () => {
    expect(findTerminalFileLinks('@./tmp/clip/10-18-02/0237001-12631d7e0ff8d571.png')).toEqual([])
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
})
