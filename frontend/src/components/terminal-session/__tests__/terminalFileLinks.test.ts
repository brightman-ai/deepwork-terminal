import { describe, expect, it } from 'bun:test'
import { diffLineNumber, editedDiffLineLink, editedDiffLineLinkFromBuffer, editedFileHeader, findTerminalFileLinks, findWrappedTerminalFileLinks, wrappedTerminalRows, parseFileLocationSuffix, terminalCellRange, terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../terminalFileLinks'

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

  it('skips the zero-width continuation cell immediately before a link', () => {
    const cells = [{ chars: '中', width: 2 }, { chars: '', width: 0 }, { chars: 'a', width: 1 }, { chars: '.', width: 1 }, { chars: 'm', width: 1 }, { chars: 'd', width: 1 }]
    expect(terminalCellRange(cells, 1, 4)).toEqual({ start: 3, end: 6 })
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

  it('extracts an inclusive line range from an explicit hyphen suffix', () => {
    expect(findTerminalFileLinks('open src/app.ts:20-22')).toEqual([
      { path: 'src/app.ts', start: 5, end: 14, displayLength: 16, lineStart: 20, lineEnd: 22 },
    ])
  })

  it('rejects malformed or zero line locations', () => {
    expect(parseFileLocationSuffix(':0')).toEqual({})
    expect(parseFileLocationSuffix(':20-19')).toEqual({})
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

describe('whole references from agent transcript shapes', () => {
  const cellBuffer = (texts: string[], cols: number) => {
    const rows = texts.map((text, row) => {
      const cells = Array.from(text).flatMap(chars => chars === '中'
        ? [{chars, width: 2}, {chars: '', width: 0}] : [{chars, width: 1}])
      while (cells.length < cols) cells.push({chars: '', width: 1})
      return {isWrapped: row > 0, length: cells.length, getCell: (i: number) => cells[i] && ({getChars: () => cells[i].chars, getWidth: () => cells[i].width})}
    })
    return {length: rows.length, getLine: (i: number) => rows[i]}
  }

  it('gives native links and virtual pointers identical cell hits after Chinese and soft wraps', () => {
    const buffer = cellBuffer(['中 a/', 'b.md'], 6)
    expect(terminalFileLinksAtRow(buffer, 0)[0]).toMatchObject({path: 'a/b.md', cellStart: 4, cellEnd: 5})
    expect(terminalFileLinkAtCell(buffer, 0, 2)).toBeUndefined()
    expect(terminalFileLinkAtCell(buffer, 0, 3)?.path).toBe('a/b.md')
    expect(terminalFileLinkAtCell(buffer, 1, 3)?.path).toBe('a/b.md')
    expect(terminalFileSelectionAtCell(buffer, 1, 0, 6)).toEqual({path: 'a/b.md', startCol: 3, startRow: 0, endCol: 3, endRow: 1, length: 7})
  })

  it('selects only the aimed reference when the same filename appears twice', () => {
    expect(terminalFileSelectionAtCell(cellBuffer(['a.md a.md'], 9), 0, 6, 9))
      .toEqual({path: 'a.md', startCol: 5, startRow: 0, endCol: 8, endRow: 0, length: 4})
    expect(terminalFileLinkAtCell(cellBuffer(['a.md'], 9), 0, -1)).toBeUndefined()
  })
  it('never drops a prefix containing Unicode, elision, parentheses, or an escaped space', () => {
    for (const path of [
      'topics/TH-0628-…/讲解/基线.md', 'topics/TH-0628-.../讲解/基线.md',
      'docs/📚笔记.md', 'docs/cafe\u0301.md', 'docs/report(final).md',
      'docs/file[1].md', 'docs/my%20report.md',
      '[draft].md', '(draft).md',
    ]) expect(findTerminalFileLinks(`open ${path}`)[0]?.path).toBe(path)
    expect(findTerminalFileLinks('docs/my\\ report.md')[0]).toMatchObject({
      path: 'docs/my report.md', start: 0, end: 17, displayLength: 18,
    })
  })

  it('recognizes real special names and explicit unknown formats without making arbitrary words links', () => {
    expect(findTerminalFileLinks('Dockerfile .gitignore config/.npmrc output/archive.tar.gz').map(l => l.path))
      .toEqual(['Dockerfile', '.gitignore', 'config/.npmrc', 'output/archive.tar.gz'])
    expect(findTerminalFileLinks('just prose backup.bin')).toEqual([])
  })

  it('uses Markdown targets, including angle-wrapped Codex absolute targets, instead of label fragments', () => {
    expect(findTerminalFileLinks('[app.ts:99](</work/src/actual.ts#L20-L25>)')).toEqual([{
      path: '/work/src/actual.ts', start: 13, end: 31, displayLength: 27, lineStart: 20, lineEnd: 25,
    }])
    expect(findTerminalFileLinks('[app.ts](https://example.com/other.ts)')).toEqual([])
    expect(findTerminalFileLinks('[报告](docs/my report.md)')[0]?.path).toBe('docs/my report.md')
  })

  it('retains the certain line for a colon pair and supports explicit transcript range formats', () => {
    for (const suffix of [':20:7', ':20:22', ':20:19']) {
      expect(parseFileLocationSuffix(suffix)).toEqual({ lineStart: 20, lineEnd: 20 })
    }
    for (const suffix of [':20-25', '#L20-L25']) {
      expect(parseFileLocationSuffix(suffix)).toEqual({ lineStart: 20, lineEnd: 25 })
    }
    expect(findTerminalFileLinks('"docs/my report.md":513')[0]).toMatchObject({path: 'docs/my report.md', lineStart: 513})
    expect(findTerminalFileLinks('（docs/report.md:513）')[0]).toMatchObject({path: 'docs/report.md', lineStart: 513})
  })

  it('never recovers a local tail from foreign URLs, Windows addresses, shell expressions, or globs', () => {
    for (const value of [
      'https://example.com/a.md', 'file://foreign/work/a.md', 'C:\\work\\a.md',
      '${HOME}/docs/a.md', '$(pwd)/docs/a.md', 'src/**/*.ts',
      '"https://example.com/my report.md"',
    ]) expect(findTerminalFileLinks(value)).toEqual([])
  })

  it('decodes local file URIs only as URIs and Git octal bytes only inside double quotes', () => {
    expect(findTerminalFileLinks('file:///work/my%20report.md:20:7')[0]).toMatchObject({path: '/work/my report.md', lineStart: 20, lineEnd: 20})
    expect(findTerminalFileLinks('docs/my%20report.md')[0]?.path).toBe('docs/my%20report.md')
    expect(findTerminalFileLinks('"docs/\\345\\255\\220.md"')[0]?.path).toBe('docs/子.md')
    expect(findTerminalFileLinks('"docs/\\377.md"')).toEqual([])
    expect(findTerminalFileLinks('"docs/it\'s.md"')[0]?.path).toBe("docs/it's.md")
  })

  it('interprets tool and Git wrappers without stripping real a/ directories in prose', () => {
    expect(findTerminalFileLinks('Read(docs/report(final).md)')[0]?.path).toBe('docs/report(final).md')
    expect(findTerminalFileLinks('diff --git a/src/app.ts b/src/app.ts').map(l => l.path)).toEqual(['src/app.ts', 'src/app.ts'])
    expect(findTerminalFileLinks('a/src/app.ts')[0]?.path).toBe('a/src/app.ts')
    expect(findTerminalFileLinks('Read("docs/my report.md")')[0]?.path).toBe('docs/my report.md')
    expect(findTerminalFileLinks('[Read(docs/wrong.md)](docs/right.md)').map(l => l.path)).toEqual(['docs/right.md'])
    expect(findTerminalFileLinks('see[report](docs/right.md)').map(l => l.path)).toEqual(['docs/right.md'])
    expect(findTerminalFileLinks('['.repeat(12000))).toEqual([])
  })

  it('does not attach unrelated numbered output or a statistics sentence to an old Edited header', () => {
    expect(editedFileHeader('cost docs/report.md (+2 -2)')).toBeUndefined()
    expect(editedDiffLineLink('22 | other', ['unrelated output', 'Edited docs/old.md (+2 -2)'])).toBeUndefined()
    expect(editedDiffLineLink('22 | other', ['Read docs/new.md', 'Edited docs/old.md (+2 -2)'])).toBeUndefined()
  })

  it('maps every soft-wrap split of combined references back to the identical target', () => {
    for (const [token, path, lineStart, lineEnd] of [
      ['topics/TH-0628-…/讲解/📚基线.md:513-515', 'topics/TH-0628-…/讲解/📚基线.md', 513, 515],
      ['"docs/中文 report(final).md"', 'docs/中文 report(final).md', undefined, undefined],
      ['docs/cafe\u0301.md#L20-L25', 'docs/cafe\u0301.md', 20, 25],
      ['@./tmp/clip/10-19-13/1348001-4410cc76d54ccf80.png', './tmp/clip/10-19-13/1348001-4410cc76d54ccf80.png', undefined, undefined],
    ] as const) {
      let offset = 0
      for (const char of Array.from(token).slice(0, -1)) {
        offset += char.length
        const rows = [{ text: token.slice(0, offset), isWrapped: false }, { text: token.slice(offset), isWrapped: true }]
        let fragments = 0
        for (const row of [0, 1]) {
          const link = findWrappedTerminalFileLinks(rows, row)[0]
          // A row containing only the wrapper can correctly have no clickable cells.
          if (link) {
            fragments++
            expect([link.path, link.lineStart, link.lineEnd]).toEqual([path, lineStart, lineEnd])
          }
        }
        expect(fragments).toBeGreaterThan(0)
      }
    }
  })

  it('joins wrapped edit headers but rejects clipped logical lines', () => {
    const buffer = (rows: Array<{text: string; isWrapped: boolean}>) => ({
      length: rows.length, getLine: (i: number) => {
        const row = rows[i]
        return row ? { isWrapped: row.isWrapped, length: row.text.length,
          getCell: (j: number) => ({ getChars: () => row.text[j], getWidth: () => 1 }) } : undefined
      },
    })
    expect(editedDiffLineLinkFromBuffer(buffer([
      {text: 'Edited docs/long/', isWrapped: false}, {text: 'report.md (+2 -2)', isWrapped: true},
      {text: '20 + content', isWrapped: false}, {text: '21 + next', isWrapped: false},
    ]), 3)).toMatchObject({path: 'docs/long/report.md', lineStart: 21})
    expect(wrappedTerminalRows(buffer([{text: '/tail.md', isWrapped: true}]), 0).rows).toEqual([])
    for (const length of [64, 65, 66]) {
      const long = Array.from({length}, (_, i) => ({text: i === length - 1 ? 'tail.md' : 'segment/', isWrapped: i > 0}))
      for (const hit of [0, length - 1]) {
        expect(wrappedTerminalRows(buffer(long), hit).rows.length).toBe(length === 64 ? 64 : 0)
      }
    }
  })
})

describe('wrapped terminal file links', () => {
  it('makes both halves of a Chinese path activate the complete file and source location', () => {
    const path = 'topics/中文目录/讲解/中文长路径-分享-v5-20261019.md'
    const rows = [
      { text: 'Word (' + path.slice(0, 22), isWrapped: false },
      { text: path.slice(22) + ':513-515) · next.txt', isWrapped: true },
    ]
    for (const row of [0, 1]) {
      const [link] = findWrappedTerminalFileLinks(rows, row)
      expect(link).toMatchObject({ path, lineStart: 513, lineEnd: 515, row })
      expect(rows[row].text.slice(link.rawStart, link.rawStart + link.rawLength)).not.toContain('next.txt')
    }
  })

  it('preserves real spaces at the wrap boundary of a delimited filename', () => {
    const rows = [
      { text: 'open "docs/report ', isWrapped: false },
      { text: ' final.md" and next.txt', isWrapped: true },
    ]
    expect(findWrappedTerminalFileLinks(rows, 0)[0].path).toBe('docs/report  final.md')
    expect(findWrappedTerminalFileLinks(rows, 1).map((l) => l.path)).toEqual(['docs/report  final.md', 'next.txt'])
  })

  it('never concatenates independent hard-newline records', () => {
    const rows = [
      { text: 'docs/partial', isWrapped: false },
      { text: 'other.md', isWrapped: false },
    ]
    expect(findWrappedTerminalFileLinks(rows, 0)).toEqual([])
    expect(findWrappedTerminalFileLinks(rows, 1)[0].path).toBe('other.md')
  })

  it('removes only empty terminal padding, retaining explicitly printed spaces', () => {
    const line = (chars: string[], isWrapped: boolean) => ({
      isWrapped, length: chars.length,
      getCell: (i: number) => ({ getChars: () => chars[i], getWidth: () => 1 }),
    })
    const lines = [
      line(['(', 'a', '/', '中', '', ''], false),
      line(['文', '.', 'm', 'd', ')', ' ', ''], true),
    ]
    const group = wrappedTerminalRows({ length: 2, getLine: (i) => lines[i] }, 1)
    expect(group.rows.map((row) => row.text)).toEqual(['(a/中', '文.md) '])
    expect(findWrappedTerminalFileLinks(group.rows, group.currentRow)[0].path).toBe('a/中文.md')
  })

  it('does not turn a URL wrapped across rows into a filesystem link', () => {
    expect(findWrappedTerminalFileLinks([
      { text: 'https://example.com/', isWrapped: false },
      { text: 'report.md', isWrapped: true },
    ], 1)).toEqual([])
  })

  it('keeps quoted paths with spaces as a single target instead of partial filenames', () => {
    expect(findTerminalFileLinks('open "docs/my report.md" and (docs/中文 文件.docx)').map((l) => l.path))
      .toEqual(['docs/my report.md', 'docs/中文 文件.docx'])
  })
})

describe('Markdown targets inside document edit previews', () => {
  const buffer = (lines: string[]) => ({ length: lines.length, getLine: (row: number) => {
    const text = lines[row]
    return text === undefined ? undefined : {isWrapped: false, length: text.length,
      getCell: (col: number) => ({getChars: () => text[col], getWidth: () => 1})}
  } })

  it('uses the Added document directory for the screenshot shape, keeping the gutter on its source', () => {
    const doc = 'collab/work/whiteboard-ng自举-20261001/新版隔离重分析-20261019/audit/本轮结论.md'
    const target = '../workspace/output/UEBA数据库选型/UEBA基线设计与数据库选择依据-v2-20261019.md'
    const b = buffer([`• Added ${doc} (+28 -0)`, '1 +# 本轮结论', '2 +', `3 +交付是 [UEBA基线 v2](${target})，入口已同步。`])
    const links = terminalFileLinksAtRow(b, 3)
    expect(links.find(l => l.documentRelative)?.path).toBe('./collab/work/whiteboard-ng自举-20261001/新版隔离重分析-20261019/workspace/output/UEBA数据库选型/UEBA基线设计与数据库选择依据-v2-20261019.md')
    expect(links.find(l => l.referenceStart === -1)).toMatchObject({path: doc, lineStart: 3})
    const link = links.find(l => l.documentRelative)!
    expect(terminalFileLinkAtCell(b, 3, link.cellStart - 1)?.path).toBe(link.path)
    expect(b.getLine(3)!.length).toBeGreaterThan(link.cellEnd)
  })

  it('keeps a plain terminal ../ reference cwd-relative and only rebases explicit Markdown targets', () => {
    const b = buffer(['Added docs/audit/notes.md (+1 -0)', '1 +[linked](../guide.md) and ../plain.md'])
    const paths = terminalFileLinksAtRow(b, 1).map(l => l.path)
    expect(paths).toEqual(['./docs/guide.md', '../plain.md', 'docs/audit/notes.md'])
    expect(findTerminalFileLinks('../guide.md:20-25')[0]).toMatchObject({path: '../guide.md', lineStart: 20, lineEnd: 25})
    expect(terminalFileLinksAtRow(buffer(['[linked](../guide.md)']), 0)[0].path).toBe('../guide.md')
  })

  it('never borrows a stale, non-document or incomplete edit base', () => {
    for (const lines of [
      ['Added docs/audit/notes.md (+1 -0)', 'unrelated output', '1 +[linked](../guide.md)'],
      ['Added docs/example.ts (+1 -0)', '1 +[linked](../guide.md)'],
      ['Added docs/.../notes.md (+1 -0)', '1 +[linked](../guide.md)'],
      ['1 +[linked](../guide.md)'],
    ]) expect(terminalFileLinksAtRow(buffer(lines), lines.length - 1).filter(l => l.documentRelative)).toEqual([])
    const b = buffer(['Added old/notes.md (+1 -0)', '1 +previous', 'Added next/audit/notes.md (+1 -0)', '1 +[linked](../guide.md)'])
    expect(terminalFileLinksAtRow(b, 3).find(l => l.documentRelative)?.path).toBe('./next/guide.md')
  })
})
