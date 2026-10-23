import { describe, expect, it } from 'bun:test'
import { filePathCandidates, normalizeTerminalPath, resolveTerminalPath, resolveDocumentPath, terminalPathSearchQuery } from '../terminalPathResolver'

describe('terminalPathResolver', () => {
  it('normalizes the visible Codex @ marker before resolving', () => {
    expect(normalizeTerminalPath('@./src/app.ts')).toBe('./src/app.ts')
    expect(normalizeTerminalPath('@/src/app.ts')).toBe('/src/app.ts')
  })

  it('searches basenames instead of guessing the current directory', () => {
    expect(resolveTerminalPath('implementation-notes.md')).toEqual({ kind: 'search', query: 'implementation-notes.md' })
    expect(resolveTerminalPath('case-产研组织.md')).toEqual({ kind: 'search', query: 'case-产研组织.md' })
  })

  it('opens explicit and absolute paths directly', () => {
    expect(resolveTerminalPath('./src/app.ts')).toEqual({ kind: 'direct', path: './src/app.ts' })
    expect(resolveTerminalPath('/work/src/app.ts')).toEqual({ kind: 'direct', path: '/work/src/app.ts' })
  })

  it('keeps a suffix path query for a 404 fallback', () => {
    expect(resolveTerminalPath('src/app.ts')).toEqual({ kind: 'direct', path: 'src/app.ts', fallbackQuery: 'src/app.ts' })
  })

  it('searches the complete suffix without trying cwd when a document base is unknown', () => {
    expect(resolveTerminalPath('revisions/buddy-wireframe-v1.html', true)).toEqual({ kind: 'search', query: 'revisions/buddy-wireframe-v1.html' })
    expect(resolveTerminalPath('./revisions/buddy-wireframe-v1.html', true)).toEqual({ kind: 'search', query: './revisions/buddy-wireframe-v1.html' })
    const candidates = filePathCandidates([
      { name: 'buddy-wireframe-v1.html', rel: 'revisions/buddy-wireframe-v1.html', isDir: false },
      { name: 'buddy-wireframe-v1.html', rel: 'docs/buddy/revisions/buddy-wireframe-v1.html', isDir: false },
      { name: 'buddy-wireframe-v1.html', rel: 'other/buddy-wireframe-v1.html', isDir: false },
    ], '/project', 'revisions/buddy-wireframe-v1.html')
    expect(candidates.map(c => c.rel)).toEqual(['revisions/buddy-wireframe-v1.html', 'docs/buddy/revisions/buddy-wireframe-v1.html'])
    expect(candidates.every(c => c.exact)).toBe(true)
  })

  it('filters directories and anchors candidates under cwd', () => {
    expect(filePathCandidates([
      { name: 'app.ts', rel: 'src/app.ts', isDir: false },
      { name: 'src', rel: 'src', isDir: true },
    ], '/work/project/', 'app.ts')).toEqual([{
      name: 'app.ts', rel: 'src/app.ts', absPath: '/work/project/src/app.ts', exact: true, match: 'suffix',
    }])
  })

  it('prefers exact suffix matches and marks fallback matches as approximate', () => {
    const entries = [
      { name: 'app.ts', rel: 'src/app.ts', isDir: false },
      { name: 'app.ts', rel: 'vendor/app.ts', isDir: false },
      { name: 'application.ts', rel: 'src/application.ts', isDir: false },
    ]
    expect(filePathCandidates(entries, '/work', 'src/./app.ts').map((e) => [e.rel, e.exact])).toEqual([['src/app.ts', true]])
    expect(filePathCandidates(entries, '/work', 'missing.ts').map((e) => [e.rel, e.exact])).toEqual([
      ['src/app.ts', false], ['vendor/app.ts', false], ['src/application.ts', false],
    ])
  })

  const entry = (rel: string) => ({ name: rel.split('/').at(-1)!, rel, isDir: false })

  it('recovers the reported middle elision without discarding known path constraints', () => {
    const name = 'UEBA基线-数据库选型-StarRocks还是Cassandra-v1-20261019.md'
    const reference = `topics/TH-0628-.../讲解/UEBA工程架构评审/${name}`
    const target = `topics/TH-0628-安全分析/讲解/UEBA工程架构评审/${name}`
    expect(terminalPathSearchQuery(reference)).toBe(`topics/TH-0628- 讲解/UEBA工程架构评审/${name}`)
    expect(filePathCandidates([
      entry(target), entry(`topics/other/讲解/UEBA工程架构评审/${name}`),
      entry(`topics/TH-0628-安全分析/其他/UEBA工程架构评审/${name}`),
    ], '/work', reference).map(c => [c.rel, c.match])).toEqual([[target, 'elided']])
  })

  it('uses the same policy for ASCII, Unicode, and whole-directory omissions', () => {
    for (const gap of ['...', '…', '⋯']) {
      expect(filePathCandidates([entry('a/sub/deep/b.md')], '/work', `a/${gap}/b.md`)[0]?.match).toBe('elided')
      expect(filePathCandidates([entry('a/b.md')], '/work', `a/${gap}/b.md`)[0]?.match).toBe('elided')
      expect(filePathCandidates([entry('a/TH-0628-安全/b.md')], '/work', `a/TH-0628-${gap}/b.md`)[0]?.match).toBe('elided')
    }
  })

  it('retains ambiguity and rejects wrong order, directory boundaries, or filename suffix', () => {
    const wanted = 'topics/TH-.../讲解/report.md'
    const entries = [
      'topics/TH-one/讲解/report.md', 'topics/TH-two/讲解/report.md',
      'topics/TH-one/other/讲解/report.md', 'topics/TH-one/讲解/report.md.bak',
      '讲解/topics/TH-one/report.md', 'topics/TH-one/讲解/myreport.md',
    ].map(entry)
    expect(filePathCandidates(entries, '/work', wanted).map(c => c.rel)).toEqual([
      'topics/TH-one/讲解/report.md', 'topics/TH-two/讲解/report.md',
    ])
    expect(filePathCandidates([entry('a/other.md')], '/work', 'a/.../report.md')).toEqual([])
  })

  it('preserves literal dotted files and basename search instead of treating every dot as a wildcard', () => {
    expect(resolveTerminalPath('./docs/release...notes.md')).toEqual({
      kind: 'direct', path: './docs/release...notes.md', fallbackQuery: './docs/release...notes.md',
    })
    expect(resolveTerminalPath('release...notes.md')).toEqual({ kind: 'search', query: 'release...notes.md' })
    expect(filePathCandidates([entry('docs/release...notes.md'), entry('docs/release-final-notes.md')], '/work', 'docs/release...notes.md')
      .map(c => c.match)).toEqual(['literal'])
  })

  it('verifies absolute references against absolute candidates while keeping retrieval scoped', () => {
    expect(terminalPathSearchQuery('/work/a/.../b.md', '/work')).toBe('a b.md')
    expect(filePathCandidates([entry('a/sub/b.md')], '/work', '/work/a/.../b.md')[0]?.match).toBe('elided')
    expect(filePathCandidates([entry('a/sub/b.md')], '/other', '/work/a/.../b.md')).toEqual([])
    expect(filePathCandidates([entry('src/App.ts')], '/work', 'src/app.ts')[0]?.exact).toBe(false)
  })

  it('bounds adversarial omissions without regex backtracking or changing literal characters', () => {
    expect(filePathCandidates([entry('a/'.repeat(300) + 'b.md')], '/work', 'a/.../b.md')).toEqual([])
    expect(filePathCandidates([entry('a/[v1]+report.md')], '/work', 'a/...report.md')[0]?.match).toBe('elided')
    expect(filePathCandidates([entry('a/file.md')], '/work', 'a/xx...xx.md')).toEqual([])
  })
})

describe('document-relative references', () => {
  it('keeps terminal-relative dot paths on the direct route', () => {
    for (const path of ['../workspace/output/报告.md', '../../other/report.md', './docs/report.md']) {
      expect(resolveTerminalPath(path)).toEqual({kind: 'direct', path})
    }
  })

  it('anchors Markdown targets to their document and preserves the path namespace', () => {
    expect(resolveDocumentPath('collab/review/audit/本轮结论.md', '../workspace/output/报告.md'))
      .toBe('./collab/review/workspace/output/报告.md')
    expect(resolveDocumentPath('/work/docs/audit/notes.md', '../guide.md')).toBe('/work/docs/guide.md')
    expect(resolveDocumentPath('../docs/notes.md', '../../guide.md')).toBe('../../guide.md')
    expect(resolveDocumentPath('README.md', 'guide.md')).toBe('./guide.md')
    expect(resolveDocumentPath('docs/notes.md', '/other/guide.md')).toBe('/other/guide.md')
    expect(resolveDocumentPath('~/docs/notes.md', '../../guide.md')).toBe('~/../guide.md')
    expect(resolveDocumentPath('docs/release...notes.md', 'assets/image.png')).toBe('./docs/assets/image.png')
    expect(resolveTerminalPath(resolveDocumentPath('README.md', 'guide.md')!)).toEqual({kind: 'direct', path: './guide.md'})
  })

  it('does not invent a document base from missing context or resolve foreign URLs as files', () => {
    expect(resolveDocumentPath('', '../guide.md')).toBeUndefined()
    expect(resolveDocumentPath('docs/notes.md', 'https://example.com/guide.md')).toBeUndefined()
  })
})
