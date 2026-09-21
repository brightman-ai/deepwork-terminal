import { describe, expect, it } from 'bun:test'
import { mergeSearchEntries, groupedSearchRows } from '../searchGroups'
import type { SearchEntry } from '@terminal/api/files'

const hit = (rel: string, isDir = false, mtimeMs = 0): SearchEntry => ({
  name: rel.split('/').pop()!, rel, isDir, mtimeMs, size: isDir ? 0 : 100,
})

describe('flat search directory groups', () => {
  it('shares a single compact path header, including when the directory itself matches', () => {
    const rows = groupedSearchRows([hit('docs/specs/one.md'), hit('docs/specs', true, 200), hit('docs/specs/two.md')])
    expect(rows.map(r => [r.label, r.grouped])).toEqual([
      ['docs/specs', false], ['one.md', true], ['two.md', true],
    ])
    expect(rows[0].mtimeMs).toBe(200)
    expect(groupedSearchRows([hit('docs/specs', true), hit('docs/specs/one.md')]).map(r => r.rel))
      .toEqual(['docs/specs', 'docs/specs/one.md'])
  })

  it('keeps differently located directories with the same name distinguishable', () => {
    const rows = groupedSearchRows([hit('app/test/a.md'), hit('lib/test/a.md')])
    expect(rows.filter(r => r.isDir).map(r => r.label)).toEqual(['app/test', 'lib/test'])
    expect(rows.filter(r => !r.isDir)).toHaveLength(2)
  })

  it('never adds ancestor rows or deeper indentation for deeply nested hits', () => {
    const deep = Array.from({ length: 50 }, (_, i) => `level-${i}`).join('/')
    const rows = groupedSearchRows([hit(`${deep}/one.md`), hit(`${deep}/two.md`)])
    expect(rows).toHaveLength(3)
    expect(rows[0].label).toBe(deep)
    expect(rows.slice(1).map(r => r.grouped)).toEqual([true, true])
  })

  it('displays scope-relative headings while retaining complete paths for actions', () => {
    const rows = groupedSearchRows([hit('docs/specs/a.md'), hit('docs/specs/nested/b.md'), hit('docs/specs-other/c.md')], 'docs/specs')
    expect(rows.map(r => [r.rel, r.label, r.grouped])).toEqual([
      ['docs/specs/a.md', 'a.md', false],
      ['docs/specs/nested', 'nested', false],
      ['docs/specs/nested/b.md', 'b.md', true],
    ])
  })

  it('deduplicates incoming responses and overlapping pages by full path', () => {
    const first = mergeSearchEntries([], [hit('docs/a.md'), hit('docs/a.md')])
    const merged = mergeSearchEntries(first, [hit('docs/a.md', false, 500), hit('docs/b.md'), hit('docs/b.md')])
    expect(merged).toHaveLength(2)
    expect(merged[0].mtimeMs).toBe(500)
    expect(groupedSearchRows(merged).map(r => r.rel)).toEqual(['docs', 'docs/a.md', 'docs/b.md'])
  })

  it('keeps supplied rank/time ordering within groups and orders named groups by path', () => {
    const hits = [hit('z/new.md'), hit('a/old.md'), hit('z/old.md')]
    expect(groupedSearchRows(hits, '', 'time').map(r => r.rel))
      .toEqual(['z', 'z/new.md', 'z/old.md', 'a', 'a/old.md'])
    expect(groupedSearchRows(hits, '', 'name').map(r => r.rel))
      .toEqual(['a', 'a/old.md', 'z', 'z/new.md', 'z/old.md'])
  })

  it('removes empty groups after filtering, with no heading for root-level files', () => {
    expect(groupedSearchRows([hit('root.md')]).map(r => [r.label, r.grouped])).toEqual([['root.md', false]])
    const hits = [hit('docs/a.md'), hit('images/a.png')]
    expect(groupedSearchRows(hits.filter(e => e.name.endsWith('.md'))).map(r => r.rel)).toEqual(['docs', 'docs/a.md'])
    expect(groupedSearchRows([])).toEqual([])
  })
})
