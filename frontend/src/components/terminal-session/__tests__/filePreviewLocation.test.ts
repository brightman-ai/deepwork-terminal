import { describe, expect, it } from 'bun:test'
import { filePreviewLocation, treeRelativePath } from '../filePreviewLocation'

describe('automatic tree location keeps its current namespace; explicit root changes are server resolved', () => {
  it('maps relative and absolute directory links, including the project root', () => {
    expect(treeRelativePath('/work', 'docs/.tg/work/review/')).toBe('docs/.tg/work/review')
    expect(treeRelativePath('/work', '/work/docs/中文 目录/')).toBe('docs/中文 目录')
    expect(treeRelativePath('/work', './docs/old/../review/')).toBe('docs/review')
    expect(treeRelativePath('/work', '/work/')).toBe('')
    expect(treeRelativePath('/work', './')).toBe('')
    expect(treeRelativePath('/', '/docs/review/')).toBe('docs/review')
  })
  it('does not reinterpret outside, prefix-collision or unresolved home paths as children', () => {
    for (const path of ['/work-other/docs/', '/work/../outside/', '../docs/', '~/docs/']) {
      expect(treeRelativePath('/work', path)).toBeNull()
    }
    expect(treeRelativePath('', '/work/docs/')).toBeNull()
  })
})

describe('document location inside its unchanged permission root', () => {
  it('locates absolute, relative, root-level and Unicode files by the same identity', () => {
    expect(filePreviewLocation('/work', '/work/docs/交流/提纲.md', 'docs/交流/提纲.md')).toEqual({ rel: 'docs/交流/提纲.md', parent: 'docs/交流' })
    expect(filePreviewLocation('/work/', './docs/old/../next.md', './docs/old/../next.md')).toEqual({ rel: 'docs/next.md', parent: 'docs' })
    expect(filePreviewLocation('/work', '/work/README.md', 'README.md')).toEqual({ rel: 'README.md', parent: '' })
    expect(filePreviewLocation('/', '/docs/intro.md', '/docs/intro.md')).toEqual({ rel: 'docs/intro.md', parent: 'docs' })
  })
  it('does not turn an outside or unknown absolute location into an in-scope basename', () => {
    expect(filePreviewLocation('/work', '/work-other/docs/a.md', 'a.md')).toBeNull()
    expect(filePreviewLocation('/work', '../other/a.md', '../other/a.md')).toBeNull()
    expect(filePreviewLocation('~', '/home/user/docs/a.md', 'a.md')).toBeNull()
  })
})
