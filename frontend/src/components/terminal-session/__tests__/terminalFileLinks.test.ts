import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks } from '../terminalFileLinks'

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

  it('ignores unsupported extensions and URLs', () => {
    expect(findTerminalFileLinks('https://example.com/report.html backup.bin')).toEqual([])
  })

  it('keeps range indexes aligned with the source line', () => {
    const line = 'open docs/readme.md now'
    expect(findTerminalFileLinks(line)).toEqual([{ path: 'docs/readme.md', start: 5, end: 18 }])
  })
})
