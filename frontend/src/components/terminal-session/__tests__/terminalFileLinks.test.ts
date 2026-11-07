// Synthetic width-preserving grammar fixtures; names and prose are invented.
import { describe, expect, it } from 'bun:test'
import { terminalPathScenarios } from './fixtures/terminalPathScenarios'
import { diffLineNumber, editedDiffLineLink, editedDiffLineLinkFromBuffer, editedFileHeader, findTerminalFileLinks, findWrappedTerminalFileLinks, wrappedTerminalRows, parseFileLocationSuffix, terminalCellRange, terminalFileLinksAtRow, terminalFileLinkAtCell, terminalFileSelectionAtCell } from '../terminalFileLinks'

describe('findTerminalFileLinks', () => {
  it('ends a parenthesized file before sentence punctuation and keeps later references independent', () => {
    const path = '~/docs/example/设计方案.md'
    for (const [open, close] of [['（', '）'], ['(', ')'], ['【', '】'], ['《', '》']]) {
      for (const separator of ['，', ',', '；', ';', '。', '！', '？', '、']) {
        const text = `● 已保存${open}${path}${separator}这里只更新方案，另见 README.md 的说明${close}。`
        const links = findTerminalFileLinks(text)
        expect(links.map(l => l.path)).toEqual([path, 'README.md'])
        expect(text.slice(links[0].start, links[0].end + 1)).toBe(path)
        expect(text.slice(links[1].start, links[1].end + 1)).toBe('README.md')
      }
    }
  })

  it('separates adjacent ASCII file-list punctuation without dropping parenthesized filename spaces or locations', () => {
    expect(findTerminalFileLinks('(docs/my report.md，说明)').map(l => l.path)).toEqual(['docs/my report.md'])
    expect(findTerminalFileLinks('(docs/app.ts:121，说明)')[0]).toMatchObject({path: 'docs/app.ts', lineStart: 121})
    for (const text of ['(docs/a.md,docs/b.md)', 'docs/a.md,docs/b.md', '(docs/a.md;docs/b.md)']) {
      const links = findTerminalFileLinks(text)
      expect(links.map(l => l.path)).toEqual(['docs/a.md', 'docs/b.md'])
      for (const link of links) expect(text.slice(link.start, link.end + 1)).toBe(link.path)
    }
    expect(findTerminalFileLinks('~/docs/a.md,这里只更新方案').map(l => l.path)).toEqual(['~/docs/a.md'])
  })

  it('preserves literal comma names in explicit targets and commas before a filename extension', () => {
    for (const path of ['docs/report,final.md', 'docs/报告，终稿.md', 'docs/report.md，final copy.md']) {
      for (const text of [`"${path}"`, `\`${path}\``, `[文档](<${path}>)`, `Read(${path})`, `● Write(${path})`, `  ⎿  Wrote 1 line to ${path}`]) {
        expect(findTerminalFileLinks(text).map(l => l.path)).toEqual([path])
      }
    }
    for (const path of ['docs/report,final.md', 'docs/报告，终稿.md', 'docs/report(final, copy).md']) {
      expect(findTerminalFileLinks(`(${path})`).map(l => l.path)).toEqual([path])
    }
    expect(findTerminalFileLinks('docs/report,final.md').map(l => l.path)).toEqual(['docs/report,final.md'])
    expect(findTerminalFileLinks('(docs/app.html?tags=one,two)').map(l => l.path)).toEqual(['docs/app.html?tags=one,two'])
    expect(findTerminalFileLinks('https://example.com/a.md,docs/b.md')).toEqual([])
  })

  it('keeps the comma and prose outside both file selections at every soft-wrap cut', () => {
    const path = '~/docs/example/中文方案.md'
    const text = `● 已保存（${path}，这里只更新方案，另见 README.md 的说明）。`
    for (let cut = 1; cut < text.length; cut++) {
      const rows = [{text: text.slice(0, cut), isWrapped: false}, {text: text.slice(cut), isWrapped: true}]
      const links = rows.flatMap((_, row) => findWrappedTerminalFileLinks(rows, row))
      for (const target of [path, 'README.md']) {
        const fragments = links.filter(link => link.path === target)
        expect(fragments.length).toBeGreaterThan(0)
        expect(fragments.map(link => rows[link.row].text.slice(link.rawStart, link.rawStart + link.rawLength)).join('')).toBe(target)
      }
    }
  })

  it('links the whole review directory in the synthetic Chinese sentence', () => {
    const path = 'docs/.tg/work/20300000-asc-0.9.7-103/'
    const text = `⏺ 文件文文文文已文草文文文文文文，文文文文(${path}):`
    expect(findTerminalFileLinks(text)).toEqual([{ path, start: text.indexOf(path), end: text.indexOf(path) + path.length - 1 }])
    expect(findTerminalFileLinks(`文文文文记录(docs/记录.md)已保存。`).map(l => l.path)).toEqual(['docs/记录.md'])
  })

  it('keeps directory boundaries, wrappers, hidden names and literal spaces', () => {
    for (const path of ['docs/.tg/work/review-v0.9.7/', './docs/', '../docs/', '/work/中文目录/', '~/docs/', 'docs/config.d/', './docs', '/work/review']) {
      for (const text of [path, `目录：${path}`, `(${path})`, `【${path}】`, `文文（${path}）:`, `文文(${path}):`, `\`${path}\``, `[目录](${path})`]) {
        expect(findTerminalFileLinks(text).map(l => l.path)).toEqual([path])
      }
    }
    expect(findTerminalFileLinks('"docs/中文 目录/"').map(l => l.path)).toEqual(['docs/中文 目录/'])
    expect(findTerminalFileLinks('目录:docs/review/，文件:docs/a.md。').map(l => l.path)).toEqual(['docs/review/', 'docs/a.md'])
    expect(findTerminalFileLinks('docs/review/（文文文）').map(l => l.path)).toEqual(['docs/review/'])
  })

  it('does not expose directory tails of addresses, expressions, globs or numeric ratios', () => {
    for (const text of ['https://example.com/docs/', 'ssh:docs/review/', 'C:/work/', '${ROOT}/docs/', '$(pwd)/docs/', 'docs/*/', '3/5/', '3/5', 'a/b', '/', '//example.com/docs/', '/browser|tool_search/i.test(x)']) {
      expect(findTerminalFileLinks(text)).toEqual([])
    }
  })

  it('maps soft-wrapped directory fragments to the same whole target', () => {
    const path = 'docs/.tg/work/20300000-asc-0.9.7-103/'
    const rows = [{ text: '文文文文(' + path.slice(0, 22), isWrapped: false }, { text: path.slice(22) + '):', isWrapped: true }]
    for (let row = 0; row < 2; row++) {
      expect(findWrappedTerminalFileLinks(rows, row)).toHaveLength(1)
      expect(findWrappedTerminalFileLinks(rows, row)[0].path).toBe(path)
    }
  })

  it('ends a complete file reference before an adjacent parenthetical prose note', () => {
    const path = 'topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文文文与KA报文文/S1文文文文与KA报文文-文文文文文文文文文文文-v4-20300103.md'
    for (const note of ['（v1 到 v3 在同一目录）', '(v1 到 v3 在同一目录)', '（说明）', '(note)', '【说明】', '[note]', '《说明》']) {
      const text = `文件：${path}${note}`
      expect(findTerminalFileLinks(text)).toEqual([{path, start: 3, end: path.length + 2}])
    }
    expect(findTerminalFileLinks('文件：docs/report.md:121（对应源码）')[0]).toMatchObject({path: 'docs/report.md', lineStart: 121})
  })

  it('keeps parentheses inside complete filenames and directories when trimming prose notes', () => {
    for (const path of ['docs/report(final).md', 'docs/report.md(final).txt', 'docs/报告（终稿）.md', 'docs/a.md（目录）/b.md', 'docs/[draft].md', 'docs/(draft).md']) {
      expect(findTerminalFileLinks(path)[0]?.path).toBe(path)
      expect(findTerminalFileLinks(path + ':121')[0]).toMatchObject({path, lineStart: 121})
    }
  })

  it('does not mistake the extension inside a clipped prose note for the file type', () => {
    const path = '文文/文文文文文文文文文文文/文文文文文文文文文文文-文文文文原文-文文文文文与PD文文-v2-20300104.md'
    const text = `稿是原文文的 v2，文另文 v3（同一文文版文文）：${path}（.docx 重出），643`
    expect(findTerminalFileLinks(text)).toEqual([{path, start: text.indexOf(path), end: text.indexOf(path) + path.length - 1}])
    for (const file of ['docs/report.md', '/work/report.md', 'docs/report.md(final).txt', 'docs/报告（终稿）.md', 'docs/文文.out']) {
      for (const [open, close] of [['（', '）'], ['(', ')'], ['【', '】'], ['[', ']'], ['《', '》']]) {
        for (const ext of ['.docx', '.pdf', '.html', '.json']) {
          const sentence = `${file}${open}${ext} 重出${close}，文文已文新。`
          expect(findTerminalFileLinks(sentence).map(l => l.path)).toEqual([file])
          expect(sentence.slice(findTerminalFileLinks(sentence)[0]!.start, findTerminalFileLinks(sentence)[0]!.end + 1)).toBe(file)
        }
      }
    }
  })

  it('retains internal filename brackets and explicit literal targets beside format-like notes', () => {
    for (const file of ['docs/report.md(final).txt', 'docs/report.md(final)-old.txt', 'docs/a.md（目录）/b.md', 'docs/report.md（.docx）.txt']) {
      expect(findTerminalFileLinks(file).map(l => l.path)).toEqual([file])
      expect(findTerminalFileLinks(file + '（.docx 重出）').map(l => l.path)).toEqual([file])
      expect(findTerminalFileLinks(file + '（普通说明）').map(l => l.path)).toEqual([file])
    }
    const literal = 'docs/report.md（.docx'
    for (const text of [`"${literal}"`, `\`${literal}\``, `[原稿](<${literal}>)`]) {
      expect(findTerminalFileLinks(text).map(l => l.path)).toEqual([literal])
    }
    expect(findTerminalFileLinks('docs/report.md（另文a.docx（重出））').map(l => l.path)).toEqual(['docs/report.md'])
    expect(findTerminalFileLinks('docs/report.md:121（.docx 重出）')[0]).toMatchObject({path: 'docs/report.md', lineStart: 121})
  })

  it('cuts ASCII and fullwidth label colons with or without spaces before a path', () => {
    const path = 'topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/报告-v3.md'
    for (const label of ['v3', 'v3.1', '第文场纪要', '文件', 'output-v3']) {
      for (const colon of [':', '：']) {
        for (const gap of ['', ' ']) {
          const line = `  - ${label}${colon}${gap}${path}`
          expect(findTerminalFileLinks(line)).toEqual([{path, start: line.indexOf(path), end: line.indexOf(path) + path.length - 1}])
        }
      }
    }
    expect(findTerminalFileLinks('输出:文件:./docs/app.ts:121')[0]).toMatchObject({path: './docs/app.ts', lineStart: 121})
  })

  it('distinguishes label colons from file locations, URIs, drive letters and literal filename punctuation', () => {
    for (const value of ['docs/app.ts:121', 'docs/app.ts:121:7', 'docs/app.ts:121-125']) {
      expect(findTerminalFileLinks(value)[0]).toMatchObject({path: 'docs/app.ts', lineStart: 121})
    }
    for (const value of ['https://example.com/app.ts', 'https:docs/app.ts', 'ssh:docs/app.ts', 'mailto:docs/app.ts', 'data:text/app.ts', 'vscode:docs/app.ts', 'C:/docs/app.ts', 'C:docs/app.ts', '${LABEL}:docs/app.ts']) {
      expect(findTerminalFileLinks(value)).toEqual([])
    }
    expect(findTerminalFileLinks('输出:https://example.com/app.ts')).toEqual([])
    expect(findTerminalFileLinks('文件:file:///work/docs/app.ts:121')[0]).toMatchObject({path: '/work/docs/app.ts', lineStart: 121})
    for (const value of ['docs/版本：报告.md', 'docs/文文：2026.md', '"版本：报告.md"']) {
      expect(findTerminalFileLinks(value)[0]?.path).toBe(value.replaceAll('"', ''))
    }
  })

  it('finds both quoted paths inside a shell-tool call instead of letting its parentheses swallow them', () => {
    const path = 'docs/meetings/20300103-S1文文文文-文路径定位文文3/asr/meeting.qwen.txt'
    const line = `Bash(until [ -s "${path}" ]; do sleep 5; done; cat "${path}")`
    expect(findTerminalFileLinks(line)).toEqual([
      {path, start: line.indexOf(path), end: line.indexOf(path) + path.length - 1},
      {path, start: line.lastIndexOf(path), end: line.lastIndexOf(path) + path.length - 1},
    ])
  })

  it('keeps independent quoted and bare command operands without linking command syntax as part of a path', () => {
    for (const tool of ['Bash', 'Shell', 'RunCommand', 'UnfamiliarTool']) {
      const line = `${tool}(cat "docs/中文 report.md" && cat 'docs/second.txt')`
      expect(findTerminalFileLinks(line).map(l => l.path)).toEqual(['docs/中文 report.md', 'docs/second.txt'])
      expect(findTerminalFileLinks(`${tool}(cat docs/a.md;cat docs/b.txt)`).map(l => l.path)).toEqual(['docs/a.md', 'docs/b.txt'])
    }
    expect(findTerminalFileLinks('Bash(cat <docs/in.txt >docs/out.txt)').map(l => l.path)).toEqual(['docs/in.txt', 'docs/out.txt'])
    expect(findTerminalFileLinks('Bash((cat docs/a.md);cat docs/b.txt)').map(l => l.path)).toEqual(['docs/a.md', 'docs/b.txt'])
    expect(findTerminalFileLinks('Bash(test -s "docs/report(final).md"; cat "docs/report(final).md")').map(l => l.path))
      .toEqual(['docs/report(final).md', 'docs/report(final).md'])
    expect(findTerminalFileLinks('docs/r&d.md docs/a;b.txt').map(l => l.path)).toEqual(['docs/r&d.md', 'docs/a;b.txt'])
    expect(findTerminalFileLinks('(docs/中文 report.md)')[0]?.path).toBe('docs/中文 report.md')
    expect(findTerminalFileLinks('Bash(curl "https://example.com/report.md")')).toEqual([])
    expect(findTerminalFileLinks('Bash(cat "${ROOT}/report.md")')).toEqual([])
  })

  it('separates prose punctuation from whole paths without changing filename parentheses', () => {
    const path = 'collab/work/sampletool-ng文文-20300101/文文文文文文-20300103/文文文文的文文与文文.md'
    for (const [open, close] of [['(', ')'], ['（', '）'], ['[', ']'], ['【', '】']]) {
      const line = `文文结文 ${open}${path}${close}已文文。`
      expect(findTerminalFileLinks(line)).toEqual([{path, start: 5 + open.length, end: 5 + open.length + path.length - 1}])
    }
    for (const prefix of ['文件：', '文件： ', '输出，', '说明；']) {
      const value = '.../DEMO文文文文文文/DEMO基线-v2-20300103.md'
      expect(findTerminalFileLinks(prefix + value)).toEqual([{path: value, start: prefix.length, end: prefix.length + value.length - 1}])
    }
    expect(findTerminalFileLinks('docs/报告（终稿）.md，docs/report(final).md。').map(l => l.path))
      .toEqual(['docs/报告（终稿）.md', 'docs/report(final).md'])
    expect(findTerminalFileLinks('docs/file[1].md)已文文')[0]?.path).toBe('docs/file[1].md')
    expect(findTerminalFileLinks('The result (docs/report.md).')[0]?.path).toBe('docs/report.md')
  })

  it('associates comma-separated Codex stats and Claude Update records with their source rows', () => {
    for (const header of ['  • Edited docs/report.md (+6, -4)', 'Edited docs/report.md (+6 −4)', '⏺ Update(docs/report.md)']) {
      expect(editedFileHeader(header)).toBe('docs/report.md')
    }
    expect(editedDiffLineLink('      121 +新增内容', ['      120  原有内容', '  ⎿  Added 6 lines, removed 4 lines', '⏺ Update(docs/report.md)']))
      .toMatchObject({path: 'docs/report.md', lineStart: 121})
    expect(editedDiffLineLink('121 +new', ['unrelated text', '⏺ Update(docs/old.md)'])).toBeUndefined()
    expect(editedFileHeader('Read(docs/report.md)')).toBeUndefined()
  })

  it('finds supported relative paths in prose and parentheses', () => {
    const line = 'prototype/vocabulary/index.html (docs/product/voicemore-reading/prototype/vocabulary/index.html)'
    expect(findTerminalFileLinks(line).map((link) => link.path)).toEqual([
      'prototype/vocabulary/index.html',
      'docs/product/voicemore-reading/prototype/vocabulary/index.html',
    ])
  })

  it('keeps dot-prefixed directory names intact', () => {
    const path = 'docs/.tg/work/20300000-000000-sample-interface-check/implementation/DEMO-NEXT/QA-REVIEW/HANDOFF-20300107.md'
    expect(findTerminalFileLinks(`(${path})`).map((link) => link.path)).toEqual([path])
  })

  it('keeps Unicode directory and filename segments intact', () => {
    const path = 'collab/work/sampletool-ng文文-2030000/文文-2030000-文文文文重文/reviews/demo_sample_review_note_20300000.md'
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
      'topics/EX-0001-…/文文/基线.md', 'topics/EX-0001-.../文文/基线.md',
      'docs/📚文记.md', 'docs/cafe\u0301.md', 'docs/report(final).md',
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

  it('separates Read tool line annotations from the synthetic absolute filename', () => {
    const path = '/home/tester/work/example-workspace/docs/meetings/20300103-AA+BBB文文文文-1文/20300103-AA+BBB文文文文-1文.md'
    const text = `● Read(${path} · lines 224-423)`
    expect(findTerminalFileLinks(text)).toEqual([
      { path, start: 7, end: 7 + path.length - 1, lineStart: 224, lineEnd: 423 },
    ])
  })

  it('handles single-line, quoted and parenthesized tool paths without making notes clickable', () => {
    for (const tool of ['Read', 'Update', 'Write', 'Edit', 'Edited']) {
      for (const [target, path, annotation, lineEnd] of [
        ['docs/report(final).md', 'docs/report(final).md', ' · lines 224-423', 423],
        ['"docs/my report.md"', 'docs/my report.md', ' · line 224', 224],
        ["'docs/中文 report.md'", 'docs/中文 report.md', ' · lines 224–423', 423],
      ] as const) {
        const text = `${tool}(${target}${annotation})`, link = findTerminalFileLinks(text)[0]!
        expect(link).toMatchObject({ path, lineStart: 224, lineEnd })
        expect(text.slice(link.start, link.end + 1)).toBe(path)
        expect((link.displayLength ?? path.length)).toBe(path.length)
      }
    }
  })

  it('preserves literal middle dots and quoted annotation-looking filenames outside tool metadata', () => {
    for (const text of [
      'Read("/work/report · lines 224-423")',
      'Read("docs/my report · lines 224-423.md")',
      'Read(docs/report · lines 224-423.md)',
      '"/work/report · lines 224-423"',
      '[报告](/work/report · lines 224-423)',
    ]) {
      const link = findTerminalFileLinks(text)[0]!
      expect(link.path).toContain(' · lines 224-423')
      expect(link.lineStart).toBeUndefined()
    }
    for (const annotation of [' · lines 0-423', ' · lines 423-224', ' · line 9007199254740992']) {
      expect(findTerminalFileLinks(`Read(/work/report.md${annotation})`)).toEqual([
        { path: '/work/report.md', start: 5, end: 19 },
      ])
    }
  })

  it('does not attach unrelated numbered output or a statistics sentence to an old Edited header', () => {
    expect(editedFileHeader('cost docs/report.md (+2 -2)')).toBeUndefined()
    expect(editedDiffLineLink('22 | other', ['unrelated output', 'Edited docs/old.md (+2 -2)'])).toBeUndefined()
    expect(editedDiffLineLink('22 | other', ['Read docs/new.md', 'Edited docs/old.md (+2 -2)'])).toBeUndefined()
  })

  it('maps every soft-wrap split of combined references back to the identical target', () => {
    for (const [token, path, lineStart, lineEnd] of [
      ['topics/EX-0001-…/文文/📚基线.md:513-515', 'topics/EX-0001-…/文文/📚基线.md', 513, 515],
      ['"docs/中文 report(final).md"', 'docs/中文 report(final).md', undefined, undefined],
      ['docs/cafe\u0301.md#L20-L25', 'docs/cafe\u0301.md', 20, 25],
      ['Read(/work/中文+AES/report(final).md · lines 224-423)', '/work/中文+AES/report(final).md', 224, 423],
      ['Read("docs/中文 report.md" · line 224)', 'docs/中文 report.md', 224, 224],
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
    const claude = buffer([
      {text: '⏺ Update(docs/report.md)', isWrapped: false},
      {text: '  ⎿  Added 6 lines, removed 4 lines', isWrapped: false},
      {text: '      120  context', isWrapped: false},
      {text: '      121 +new', isWrapped: false},
    ])
    expect(terminalFileLinksAtRow(claude, 0)[0]).toMatchObject({path: 'docs/report.md', lineStart: 121})
    expect(editedDiffLineLinkFromBuffer(claude, 3)).toMatchObject({path: 'docs/report.md', lineStart: 121})
    expect(editedDiffLineLinkFromBuffer(claude, 2)).toMatchObject({path: 'docs/report.md', lineStart: 120})
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
    const path = 'topics/中文目录/文文/中文文路径-文文-v5-20300103.md'
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

describe('explicit file references in renderer-wrapped prose', () => {
  const buffer = (lines: string[]) => ({ length: lines.length, getLine: (row: number) => {
    const text = lines[row]
    return text === undefined ? undefined : {isWrapped: false, length: text.length,
      getCell: (col: number) => ({getChars: () => text[col], getWidth: () => 1})}
  } })

  it('recovers both fragments of the synthetic multi-row paragraph', () => {
    const prefix = 'topics/EX-0002-demo-文文路径文文文文文文与文果文文文文/文文文文文文/文文'
    const suffix = '文文文文-文文文文-v5-20300103.md'
    const lines = [
      '  文已文读文文文文的文文与文文文文、文版文文、文文线原文文文文 CEO 文文。文文结果保存文《文文文文文文·文文文文 v5》 (' + prefix,
      '  ' + suffix + ')，文文原文文文定位和文文文文。',
      '',
      '  文一文中的 unrelated.md',
    ]
    const b = buffer(lines)
    for (const row of [0, 1]) {
      const links = terminalFileLinksAtRow(b, row)
      expect(links).toHaveLength(1)
      expect(links[0].path).toBe(prefix + suffix)
      for (let col = links[0].cellStart - 1; col < links[0].cellEnd; col++) {
        expect(terminalFileLinkAtCell(b, row, col)?.path).toBe(prefix + suffix)
      }
      expect(terminalFileSelectionAtCell(b, row, links[0].cellStart - 1, 200)).toMatchObject({startRow: 0, endRow: 1, path: prefix + suffix})
    }
    expect(terminalFileLinkAtCell(b, 1, 1)).toBeUndefined()
    expect(terminalFileLinkAtCell(b, 1, 2 + suffix.length)).toBeUndefined()
    expect(terminalFileLinksAtRow(b, 3)[0]?.path).toBe('unrelated.md')
  })

  it('recovers multiple hard continuations inside parentheses, quotes, backticks and Markdown targets', () => {
    for (const [open, close] of [['(', ')'], ['（', '）'], ['"', '"'], ["'", "'"], ['`', '`'], ['[报告](', ')'], ['[报告](<', '>)'], ['`[Ref: ', ']`'], ['`cat ', '`'], ['Read(', ')']]) {
      for (const head of ['  文文记录 ', '• 文文记录 ', '⏺ 文文记录 ']) {
        const b = buffer([head + open + 'docs/文', '  文文文/报', '  告.md' + close + '已保存。'])
        for (const row of [0, 1, 2]) expect(terminalFileLinksAtRow(b, row)[0]?.path).toBe('docs/文文文文/报告.md')
      }
    }
    const tool = buffer(['⏺ Update(docs/文', '  文文文/报告.md)', '      12 +new'])
    expect(terminalFileLinksAtRow(tool, 1)[0]).toMatchObject({path: 'docs/文文文文/报告.md', lineStart: 12})
  })

  it('removes the tool-title hanging gutter from every fragment of the synthetic Update path', () => {
    const path = '/home/tester/work/sample-assets/skills/sampletool-tg2/design/sampletool-t3文文文文文的场文文文-文文文文文文与文文-v4附一-20300105.md'
    const lines = ['● Update(/home/tester/work/sample-assets/skills/sampletoo',
      '        l-tg2/design/sampletool-t3文文文文文的场文文文-文',
      '        文文文文文与文文-v4附一-20300105.md)',
      '  ⎿  Added 1 line, removed 1 line', '      12 +new']
    const b = buffer(lines)
    for (const row of [0, 1, 2]) {
      const links = terminalFileLinksAtRow(b, row)
      expect(links).toHaveLength(1)
      expect(links[0]).toMatchObject({path, lineStart: 12})
      if (row > 0) expect(links[0].rawStart).toBe(8)
      for (let col = links[0].cellStart - 1; col < links[0].cellEnd; col++) {
        expect(terminalFileLinkAtCell(b, row, col)?.path).toBe(path)
      }
      expect(terminalFileSelectionAtCell(b, row, links[0].cellStart - 1, 80))
        .toMatchObject({path, startRow: 0, endRow: 2})
    }
    expect(terminalFileLinkAtCell(b, 1, 7)).toBeUndefined()
    expect(editedDiffLineLinkFromBuffer(b, 4)).toMatchObject({path, lineStart: 12})
  })

  it('uses each tool title opening-parenthesis column for aligned continuations', () => {
    for (const [tool, column] of [['Read', 4], ['Update', 6], ['Write', 5], ['Edit', 4], ['Edited', 6]] as const) {
      for (const bullet of ['', '● ', '⏺ ', '• ']) {
        for (const outer of ['', '  ']) {
          const b = buffer([`${outer}${bullet}${tool}(docs/my report/文`,
            `${' '.repeat(outer.length + column + (bullet ? 2 : 0))}文文.md${tool === 'Read' ? ' · lines 12-20' : ''})`])
          for (const row of [0, 1]) {
            expect(terminalFileLinksAtRow(b, row)[0])
              .toMatchObject({path: 'docs/my report/文文文.md', ...(tool === 'Read' ? {lineStart: 12, lineEnd: 20} : {})})
          }
        }
      }
    }
  })

  it('keeps filename spaces beyond a shorter tool title alignment', () => {
    const cases = [
      {rows: ['● Read("docs/my', '        report.md")'], path: 'docs/my  report.md'},
      {rows: ['● Write("docs/my', '        report.md")'], path: 'docs/my report.md'},
    ]
    for (const {rows, path} of cases) for (const row of [0, 1]) {
      expect(terminalFileLinksAtRow(buffer(rows), row)[0]?.path).toBe(path)
    }
  })

  it('preserves literal filename spaces around a tool-title hard boundary', () => {
    for (const gutter of ['  ', '        ']) {
      for (const [open, close] of [['', ''], ['"', '"'], ["'", "'"]]) {
        const b = buffer([`● Update(${open}docs/my `, `${gutter} report  final.md${close})`])
        for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row)[0]?.path).toBe('docs/my  report  final.md')
      }
    }
    // An ordinary paragraph has no tool-title gutter; its extra spaces are content.
    const b = buffer(['● 文文 (docs/my', '        report.md)'])
    expect(terminalFileLinksAtRow(b, 1)[0]?.path).toBe('docs/my      report.md')
  })

  it('does not extend a tool title across a blank, new tool, finished title or another record', () => {
    for (const lines of [
      ['● Update(docs/partial', '', '        report.md)'],
      ['● Update(docs/partial', '        Read(other.md)'],
      ['● Update(docs/done.md)', '        report.md)'],
      ['● Update(docs/partial', 'report.md)'],
      ['● Update(docs/partial', '        more/', '  report.md)'],
    ]) {
      const b = buffer(lines)
      for (let row = 0; row < lines.length; row++) {
        expect(terminalFileLinksAtRow(b, row).some(link => link.path === 'docs/partialreport.md' || link.path === 'docs/partialmore/report.md')).toBe(false)
      }
    }
  })

  it('retains real spaces in quoted filenames at a prose hard-wrap boundary', () => {
    const b = buffer(['  记录 "docs/my ', '   report.md" 已保存。'])
    expect(terminalFileLinksAtRow(b, 0)[0]?.path).toBe('docs/my  report.md')
    expect(terminalFileLinksAtRow(b, 1)[0]?.path).toBe('docs/my  report.md')
    const english = buffer(["  I've saved (docs/long-", '  report.md).'])
    expect(terminalFileLinksAtRow(english, 1)[0]?.path).toBe('docs/long-report.md')
  })

  it('does not combine bare paths, independent records, blank paragraphs or a new bullet', () => {
    for (const lines of [
      ['  docs/partial', '  report.md'],
      ['  输出 (docs/partial', '', '  report.md)'],
      ['  输出 (docs/partial', '• report.md)'],
      ['  输出 (docs/partial', '    report.md)'],
      ['输出 (docs/partial', 'report.md)'],
    ]) {
      const b = buffer(lines)
      for (let row = 0; row < lines.length; row++) expect(terminalFileLinksAtRow(b, row).some(l => l.path === 'docs/partialreport.md')).toBe(false)
    }
  })

  it('keeps a hard-wrapped quoted URL or variable opaque instead of exposing its local-looking tail', () => {
    for (const prefix of ['https://example.com/', '${ROOT}/', 'docs/<name>/']) {
      const b = buffer(['  输出 "' + prefix, '  report.md"'])
      for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row)).toEqual([])
    }
  })
})

describe('rendered tool records from the synthetic Write/Wrote output', () => {
  const buffer = (lines: string[]) => ({length: lines.length, getLine: (row: number) => {
    const text = lines[row]
    return text === undefined ? undefined : {isWrapped: false, length: text.length,
      getCell: (col: number) => ({getChars: () => text[col], getWidth: () => 1})}
  }})
  const path = '/home/tester/work/sample-assets/skills/sampletool-t3/design/design.md'
  const lines = ['● Write(/home/tester/work/sample-assets/skills/sampletool',
    '       -t3/design/design.md)',
    '  ⎿ \u00a0Wrote 89 lines to /home/tester/work/sample-asse',
    '     ts/skills/sampletool-t3/design/design.md']
  it('keeps the original absolute target in both Write title fragments', () => {
    const b = buffer(lines)
    for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row).map(l => l.path)).toEqual([path])
  })
  it('keeps the original absolute target in both Wrote receipt fragments', () => {
    const b = buffer(lines)
    for (const row of [2, 3]) {
      const links = terminalFileLinksAtRow(b, row)
      expect(links.map(l => l.path)).toEqual([path])
      for (let col = links[0].cellStart - 1; col < links[0].cellEnd; col++) {
        expect(terminalFileLinkAtCell(b, row, col)?.path).toBe(path)
      }
      expect(terminalFileSelectionAtCell(b, row, links[0].cellStart - 1, 80))
        .toMatchObject({path, startRow: 2, endRow: 3})
    }
    expect(terminalFileLinkAtCell(b, 2, 7)).toBeUndefined()
    expect(terminalFileLinkAtCell(b, 3, 4)).toBeUndefined()
  })
})

describe('tool record boundary matrices', () => {
  const buffer = (rows: string[]) => ({length: rows.length, getLine: (i: number) => rows[i] === undefined ? undefined : {
    isWrapped: false, length: rows[i].length, getCell: (j: number) => ({getChars: () => rows[i][j], getWidth: () => 1}),
  }})
  it('preserves tool paths and Read locations at every hard split of the argument or metadata', () => {
    for (const [title, gutter] of [['● Read(', 6], ['● Update(', 8], ['● Write(', 7], ['● Edit(', 6]] as const) {
      for (const path of ['/work/sampletool/report.md', '~/docs/中文报告.md', './docs/my report(final).md']) {
        const tail = path + (title.includes('Read') ? ' · lines 12-20)' : ')')
        for (let cut = 0; cut < tail.length; cut++) {
          const b = buffer([title + tail.slice(0, cut), ' '.repeat(gutter) + tail.slice(cut)])
          const links = [0, 1].flatMap(row => terminalFileLinksAtRow(b, row))
          expect(links.length).toBeGreaterThan(0)
          for (const link of links) expect(link).toMatchObject({path, ...(title.includes('Read') ? {lineStart: 12, lineEnd: 20} : {})})
        }
      }
    }
  })
  it('recovers receipt metadata and path cuts without borrowing another record', () => {
    for (const [prefix, gutter] of [['  ⎿ \u00a0Wrote 89 lines to ', 5], ['  └  Wrote 1 line to ', 5], ['└ Wrote 1 line to ', 2], ['    ⎿  Wrote 1 line to ', 7]] as const) {
      for (const path of ['/work/sampletool/report.md', '~/docs/中文报告.md', 'docs/my  report.md']) {
        const full = prefix + path
        for (let cut = prefix.indexOf('Wrote') + 5; cut < full.length; cut++) {
          // A new root after an existing partial target remains an independent record.
          if (cut > prefix.length && full[cut] === '/') continue
          const b = buffer([full.slice(0, cut), ' '.repeat(gutter) + full.slice(cut)])
          const links = [0, 1].flatMap(row => terminalFileLinksAtRow(b, row))
          expect(links.length).toBeGreaterThan(0)
          for (const link of links) expect(link.path).toBe(path)
        }
      }
    }
    for (const tail of ['     /other.md', '     ● other.md', '  ⎿  Wrote 1 line to other.md', '', '    report.md']) {
      const b = buffer(['  ⎿  Wrote 1 line to /work/long', tail])
      for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row).some(l => l.path === '/work/longreport.md' || l.path === '/work/long/other.md')).toBe(false)
    }
    const b = buffer(['  ⎿  Wrote 2 lines to ', '     https://example.com/report.md'])
    for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row)).toEqual([])
  })
})

describe('bare paths with CLI hard continuations', () => {
  const buffer = (lines: string[]) => ({ length: lines.length, getLine: (row: number) => {
    const text = lines[row]
    return text === undefined ? undefined : { isWrapped: false, length: text.length,
      getCell: (col: number) => ({ getChars: () => text[col], getWidth: () => 1 }) }
  } })

  it('recovers the synthetic absolute path including the separated extension, without the label or note', () => {
    const lines = ['文件：/home/tester/work/example-workspace/collab/work/s',
      '  ampletool-ng文文-20300101/文文-20300104-DemoX文文重文/',
      '  文文重文-sampletool-tg文文文文与文文-DemoX-v1-20300104.',
      '  md（393 行，第一文文文是全文结文，文文行文在附录 A）。']
    const path = '/home/tester/work/example-workspace/collab/work/sampletool-ng文文-20300101/文文-20300104-DemoX文文重文/文文重文-sampletool-tg文文文文与文文-DemoX-v1-20300104.md'
    for (const head of [lines[0], '  ' + lines[0], '⏺ ' + lines[0]]) {
      const b = buffer([head, ...lines.slice(1)])
      for (let row = 0; row < lines.length; row++) {
        const links = terminalFileLinksAtRow(b, row)
        expect(links.map(l => l.path)).toEqual([path])
        for (let col = links[0]!.cellStart - 1; col < links[0]!.cellEnd; col++) {
          expect(terminalFileLinkAtCell(b, row, col)?.path).toBe(path)
        }
      }
      expect(terminalFileLinkAtCell(b, 0, head.indexOf('文件'))).toBeUndefined()
      expect(terminalFileLinkAtCell(b, 3, lines[3].indexOf('393'))).toBeUndefined()
    }
  })

  it('recovers the synthetic bare path that starts on a continuation of an assistant paragraph', () => {
    const path = '~/code/sample-assets/skills/sampletool-tg2/design/sampletool-t3文文文文文-结文文结与文文文文-v1-20300105.md'
    const lines = ['● 文文，文文一文文文文文文文文文；文文文的结文文结一页在 ',
      '  ~/code/sample-assets/skills/sampletool-tg2/design/sampl',
      '  etool-t3文文文文文-结文文结与文文文文-v1-20300105.md。',
      '  文和 Codex 说的']
    const b = buffer(lines)
    for (const row of [1, 2]) {
      const links = terminalFileLinksAtRow(b, row)
      expect(links.map(l => l.path)).toEqual([path])
      for (let col = links[0].cellStart - 1; col < links[0].cellEnd; col++) {
        expect(terminalFileLinkAtCell(b, row, col)?.path).toBe(path)
      }
      expect(terminalFileSelectionAtCell(b, row, links[0].cellStart - 1, 80))
        .toMatchObject({path, startRow: 1, endRow: 2})
    }
    for (const row of [0, 3]) expect(terminalFileLinksAtRow(b, row)).toEqual([])
    expect(terminalFileLinkAtCell(b, 2, 0)).toBeUndefined()
    expect(terminalFileLinkAtCell(b, 2, lines[2].length - 1)).toBeUndefined()
  })

  it('recovers labelled path token continuations, including a recognized extension prefix', () => {
    for (const path of ['docs/long-report.md', './docs/report.html', '~/docs/report.docx', '/work/result.json', '/work/readme.markdown']) {
      for (let cut = 1; cut < path.length; cut++) {
        // A filesystem/name anchor has to occur before the first hard boundary.
        if (!path.slice(0, cut).includes('/') || path[cut] === '/') continue
        const b = buffer(['  结果文件: ' + path.slice(0, cut), '  ' + path.slice(cut) + '（已保存）'])
        for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row).map(l => l.path)).toEqual([path])
      }
    }
  })

  it('uses one matrix for path prefixes, paragraph positions, bullets, gutters and every eligible hard split', () => {
    const paths = ['~/docs/sampletool.md', '/work/docs/sampletool.md', './docs/sampletool.html',
      '../docs/sampletool.docx', 'docs/sampletool.md', 'topics/.tg/中文目录/文文报告.markdown']
    for (const path of paths) for (const bullet of ['● ', '⏺ ', '• ', '- ']) {
      for (const outer of ['', '  ']) for (const placement of ['inline', 'next', 'later']) {
        const gutter = ' '.repeat(outer.length + bullet.length)
        for (let cut = 1; cut < path.length; cut++) {
          if (!path.slice(0, cut).includes('/') || path[cut] === '/') continue
          const prefix = outer + bullet + '已保存文文'
          const lines = placement === 'inline'
            ? [prefix + ' ' + path.slice(0, cut), gutter + path.slice(cut) + '。']
            : [prefix, ...(placement === 'later' ? [gutter + '文文是文文记录'] : []),
              gutter + path.slice(0, cut), gutter + path.slice(cut) + '。']
          const b = buffer(lines), first = lines.length - 2
          for (const row of [first, first + 1]) {
            const links = terminalFileLinksAtRow(b, row)
            expect(links.map(link => link.path)).toEqual([path])
            expect(terminalFileLinkAtCell(b, row, links[0].cellStart - 1)?.path).toBe(path)
            expect(terminalFileSelectionAtCell(b, row, links[0].cellStart - 1, 120)?.path).toBe(path)
          }
        }
      }
    }
  })

  it('keeps paragraph-owned bare paths separate from new files, records, paragraphs and literal spaces', () => {
    for (const tail of ['/other.md', '~/other.md', '../other.md', './other.md',
      '文件：other.md', '● other.md', '  other.md', '', '● Read(other.md)', '12 +other.md']) {
      const b = buffer(['● 文件已保存', '  ~/docs/long', '  ' + tail])
      for (const row of [1, 2]) expect(terminalFileLinksAtRow(b, row).some(link => link.path === '~/docs/longother.md')).toBe(false)
    }
    for (const lines of [
      ['● ~/docs/done.md', '  other.md'],
      ['● 记录', '', '  ~/docs/long', '  report.md'],
      ['● 记录', '    ~/docs/long', '    report.md'],
      ['● 记录', '  ~/docs/long ', '  report.md'],
    ]) {
      const b = buffer(lines)
      for (let row = 0; row < lines.length; row++) expect(terminalFileLinksAtRow(b, row).some(link => link.path === '~/docs/longreport.md' || link.path === '~/docs/done.mdother.md')).toBe(false)
    }
    const spaces = buffer(['● "~/docs/my ', '   report.md"'])
    for (const row of [0, 1]) expect(terminalFileLinksAtRow(spaces, row)[0]?.path).toBe('~/docs/my  report.md')
  })

  it('keeps paragraph-owned foreign or dynamic references opaque across bare hard continuations', () => {
    for (const prefix of ['https://example.com/', '${ROOT}/', 'docs/<name>/', 'C:/work/']) {
      const b = buffer(['● 保存到 ' + prefix + 'long-', '  report.md。'])
      for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row)).toEqual([])
    }
  })

  it('does not splice complete files, new roots, new labels, records, blank rows or different gutters', () => {
    for (const lines of [
      ['  文件：/tmp/report.md', '  other.md'],
      ['  文件：/tmp/report', '  /other.md'],
      ['  文件：/tmp/report', '  文件：other.md'],
      ['  文件：/tmp/report', '  ⏺ other.md'],
      ['  文件：/tmp/report', '', '  other.md'],
      ['  文件：/tmp/report', '    other.md'],
      ['  文件：/tmp/report ', '  other.md'],
      ['  /tmp/report', '  other.md'],
    ]) {
      const b = buffer(lines)
      for (let row = 0; row < lines.length; row++) {
        expect(wrappedTerminalRows(b, row).rows).toHaveLength(1)
        expect(terminalFileLinksAtRow(b, row).some(l => l.path === '/tmp/reportother.md' || l.path === '/tmp/report.mdother.md')).toBe(false)
      }
    }
  })

  it('keeps labelled foreign addresses and dynamic targets opaque across their continuation', () => {
    for (const prefix of ['https://example.com/', '${ROOT}/', 'docs/<name>/', 'C:/work/']) {
      const b = buffer(['  文件：' + prefix + 'long-', '  report.md（已保存）'])
      for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row)).toEqual([])
    }
  })
})

describe('Markdown targets inside document edit previews', () => {
  const buffer = (lines: string[]) => ({ length: lines.length, getLine: (row: number) => {
    const text = lines[row]
    return text === undefined ? undefined : {isWrapped: false, length: text.length,
      getCell: (col: number) => ({getChars: () => text[col], getWidth: () => 1})}
  } })

  it('keeps both synthetic HTML targets searchable when the numbered edit source is missing', () => {
    const text = '     5 +**当前文文文文：[文文文与文文文文文文](panel-main-preview.html)**。同一个文文中的「文文文 / 文文文文」文文文文 chat、call 和文文文文；文版文文线文文保文在文文，文文时的原稿文文文 [v1](revisions/panel-wireframe-v1.html)。本稿文文文文 API/文文文，文有文文内容文文文文本；文是安文版文新。'
    for (const prior of [[], ['Edited stale/design.md (+1 -0)', 'unrelated output'], ['Edited docs/source.ts (+1 -0)'], ['Edited docs/.../design.md (+1 -0)']]) {
      const b = buffer([...prior, text])
      const links = terminalFileLinksAtRow(b, prior.length).filter(l => l.referenceStart >= 0)
      expect(links.map(l => [l.path, l.searchOnly])).toEqual([['panel-main-preview.html', true], ['revisions/panel-wireframe-v1.html', true]])
      for (const link of links) {
        expect(terminalFileLinkAtCell(b, prior.length, link.cellStart - 1)).toMatchObject({ path: link.path, searchOnly: true })
      }
    }
    const known = terminalFileLinksAtRow(buffer(['Edited docs/panel/plan.md (+1 -0)', text]), 1).filter(l => l.documentRelative)
    expect(known.map(l => [l.path, l.searchOnly])).toEqual([['./docs/panel/panel-main-preview.html', undefined], ['./docs/panel/revisions/panel-wireframe-v1.html', undefined]])
  })

  it('reassembles Codex hard-wrapped diff bodies without their hanging gutter or right padding', () => {
    const b = buffer([
      '• Added docs/audit/notes.md (+65 -0)',
      '    44 +',
      '    45 +FAQ 文文文 [第文文文文](demo-case/workspace/docum ',
      '        ent-3.md)。文文行文文文文版 FAQ 文文文文、文文： ',
      '        文文件文在来源／文文保文；',
      '    46 +',
    ])
    for (const row of [2, 3]) {
      const link = terminalFileLinksAtRow(b, row).find(l => l.documentRelative)
      expect(link?.path).toBe('./docs/audit/demo-case/workspace/document-3.md')
      for (let col = link!.cellStart - 1; col < link!.cellEnd; col++) {
        expect(terminalFileLinkAtCell(b, row, col)?.path).toBe(link!.path)
        const selection = terminalFileSelectionAtCell(b, row, col, 80)!
        expect(selection).toMatchObject({startRow: 2, endRow: 3, endCol: 15})
      }
    }
    expect(terminalFileLinkAtCell(b, 3, 7)).toBeUndefined()
    expect(terminalFileLinksAtRow(b, 2).find(l => l.referenceStart === -1)).toMatchObject({path: 'docs/audit/notes.md', lineStart: 45})
    expect(terminalFileLinksAtRow(b, 0)[0]).toMatchObject({path: 'docs/audit/notes.md', lineStart: 44})
  })

  it('recognizes a per-file tree header in a multi-file edit and traverses preceding hanging bodies', () => {
    const b = buffer([
      '• Edited 2 files (+3 -1)',
      '  └ docs/first.md (+1 -0)',
      '    121 +prior',
      '         continuation of prior',
      '    122 +[link](case-',
      '         文文文文.md)',
      '  └ docs/second.md (+2 -1)',
      '    122 +[link](case-second.md)',
    ])
    expect(terminalFileLinksAtRow(b, 5).find(l => l.documentRelative)?.path).toBe('./docs/case-文文文文.md')
    expect(terminalFileLinksAtRow(b, 4).find(l => l.referenceStart === -1)).toMatchObject({path: 'docs/first.md', lineStart: 122})
    expect(terminalFileLinksAtRow(b, 7).find(l => l.referenceStart === -1)?.path).toBe('docs/second.md')
  })

  it('recovers hard-wrapped edit headers as well as their body, without borrowing a finished header', () => {
    for (const header of [
      ['• Added docs/audit/long-', 'notes.md (+65 -0)'],
      ['  └ docs/audit/long- ', '  notes.md (+65 -0)'],
    ]) {
      const b = buffer([...header, '    45 +FAQ [文文](demo-case/workspace/docum', '        ent-3.md)'])
      expect(terminalFileLinksAtRow(b, 3).find(l => l.documentRelative)?.path).toBe('./docs/audit/demo-case/workspace/document-3.md')
      for (const row of [0, 1]) expect(terminalFileLinksAtRow(b, row).find(l => l.path === 'docs/audit/long-notes.md'))
        .toMatchObject({path: 'docs/audit/long-notes.md', lineStart: 45})
    }
    const b = buffer(['Added docs/stale.md (+2 -0)', 'other-', 'notes.md (+2 -0)', '    45 +[link](child/report.md)'])
    expect(terminalFileLinksAtRow(b, 3).find(l => l.documentRelative)).toMatchObject({ path: 'child/report.md', searchOnly: true })
  })

  it('does not join independent hard records or erase source indentation beyond the gutter', () => {
    const b = buffer([
      'Added docs/notes.md (+2 -0)',
      '    1 +[link](docs/docum',
      'unrelated output',
      '       nse.md)',
      'Added docs/notes.md (+2 -0)',
      '    2 +[link](docs/with',
      '         two spaces.md)',
      '    3 +[next](separate.md)',
    ])
    expect(terminalFileLinksAtRow(b, 1).filter(l => l.documentRelative)).toEqual([])
    expect(terminalFileLinksAtRow(b, 6).find(l => l.documentRelative)?.path).toBe('./docs/docs/with  two spaces.md')
    expect(terminalFileLinksAtRow(b, 7).find(l => l.documentRelative)?.path).toBe('./docs/separate.md')
  })

  it('keeps a basename searchable when numbered Markdown has no reliable document base', () => {
    const text = '3 +[文文文文的原文对文对文](case-文文文文.md)'
    for (const lines of [
      [text],
      ['Added docs/report.md (+1 -0)', 'unrelated output', text],
      ['Added docs/example.ts (+1 -0)', text],
      ['Added docs/.../report.md (+1 -0)', text],
    ]) {
      const b = buffer(lines)
      const row = lines.length - 1
      const link = terminalFileLinksAtRow(b, row).find(l => l.path === 'case-文文文文.md')
      expect(link).toBeDefined()
      expect(link?.lineStart).toBeUndefined()
      const start = text.indexOf('case-文文文文.md')
      for (let col = start; col < start + 'case-文文文文.md'.length; col++) {
        expect(terminalFileLinkAtCell(b, row, col)?.path).toBe('case-文文文文.md')
      }
    }
  })

  it('resolves a basename against a reliable current Markdown edit instead of searching unrelated directories', () => {
    const b = buffer(['Added docs/design/report.md (+1 -0)', '3 +[文文文文的原文对文对文](case-文文文文.md)'])
    expect(terminalFileLinksAtRow(b, 1).find(l => l.documentRelative)?.path).toBe('./docs/design/case-文文文文.md')
  })

  it('uses the Added document directory for the screenshot shape, keeping the gutter on its source', () => {
    const doc = 'collab/work/sampletool-ng文文-20300101/新版文文重文文-20300103/audit/本文结文.md'
    const target = '../workspace/output/DEMO文文文文文/DEMO基线文文与文文文文文文文-v2-20300103.md'
    const b = buffer([`• Added ${doc} (+28 -0)`, '1 +# 本文结文', '2 +', `3 +文文是 [DEMO基线 v2](${target})，文文已同文。`])
    const links = terminalFileLinksAtRow(b, 3)
    expect(links.find(l => l.documentRelative)?.path).toBe('./collab/work/sampletool-ng文文-20300101/新版文文重文文-20300103/workspace/output/DEMO文文文文文/DEMO基线文文与文文文文文文文-v2-20300103.md')
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


describe('shared path scenario catalog: independent identities across record families', () => {
  for (const scenario of terminalPathScenarios) it(`${scenario.family}: ${scenario.id}`, () => {
    const buffer = {length: scenario.rows.length, getLine: (row: number) => {
      const text = scenario.rows[row]
      return text === undefined ? undefined : {isWrapped: false, length: text.length,
        getCell: (col: number) => ({getChars: () => text[col], getWidth: () => 1})}
    }}
    const seen = new Set<string>()
    for (let row = 0; row < buffer.length; row++) for (const link of terminalFileLinksAtRow(buffer, row)) {
      expect(scenario.paths).toContain(link.path)
      seen.add(link.path)
      for (let col = link.cellStart - 1; col < link.cellEnd; col++) {
        expect(terminalFileLinkAtCell(buffer, row, col)?.path).toBe(link.path)
      }
      expect(terminalFileSelectionAtCell(buffer, row, link.cellStart - 1, 120)?.path).toBe(link.path)
    }
    expect([...seen].sort()).toEqual([...scenario.paths].sort())
  })
})
