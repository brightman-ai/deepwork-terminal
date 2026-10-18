import { describe, expect, it } from 'bun:test'
import { filePathCandidates, normalizeTerminalPath, resolveTerminalPath } from '../terminalPathResolver'

describe('terminalPathResolver', () => {
  it('normalizes the visible Codex @ marker before resolving', () => {
    expect(normalizeTerminalPath('@./src/app.ts')).toBe('./src/app.ts')
    expect(normalizeTerminalPath('@/src/app.ts')).toBe('/src/app.ts')
  })

  it('searches basenames instead of guessing the current directory', () => {
    expect(resolveTerminalPath('implementation-notes.md')).toEqual({ kind: 'search', query: 'implementation-notes.md' })
  })

  it('opens explicit and absolute paths directly', () => {
    expect(resolveTerminalPath('./src/app.ts')).toEqual({ kind: 'direct', path: './src/app.ts' })
    expect(resolveTerminalPath('/work/src/app.ts')).toEqual({ kind: 'direct', path: '/work/src/app.ts' })
  })

  it('keeps a suffix path query for a 404 fallback', () => {
    expect(resolveTerminalPath('src/app.ts')).toEqual({ kind: 'direct', path: 'src/app.ts', fallbackQuery: 'src/app.ts' })
  })

  it('filters directories and anchors candidates under cwd', () => {
    expect(filePathCandidates([
      { name: 'app.ts', rel: 'src/app.ts', isDir: false },
      { name: 'src', rel: 'src', isDir: true },
    ], '/work/project/', 'app.ts')).toEqual([{
      name: 'app.ts', rel: 'src/app.ts', absPath: '/work/project/src/app.ts', exact: true,
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
})
