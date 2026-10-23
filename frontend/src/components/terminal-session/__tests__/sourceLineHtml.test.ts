import { describe, expect, it } from 'bun:test'
import { sourceLineHtml } from '../sourceLineHtml'

describe('source line layout', () => {
  it('marks only the requested source range without adding line numbers to copied text', () => {
    const html = sourceLineHtml('first\nsecond\nthird\nfourth', {start: 2, end: 3})
    expect([...html.matchAll(/class="fp-source-line is-target" data-source-line="(\d+)"/g)].map(m => m[1])).toEqual(['2', '3'])
    expect(html.replace(/<[^>]*>/g, '')).toBe('first\nsecond\nthird\nfourth')
  })
  it('keeps source text and its blank/trailing line identities', () => {
    const html = sourceLineHtml('first\n\nlast\n')
    expect(html.replace(/<[^>]*>/g, '')).toBe('first\n\nlast\n')
    expect([...html.matchAll(/data-source-line="(\d+)"/g)].map(m => m[1])).toEqual(['1', '2', '3', '4'])
  })
  it('preserves syntax context when a highlighted token crosses a newline', () => {
    expect(sourceLineHtml('<span class="hljs-comment">one\ntwo</span>')).toBe(
      '<span class="fp-source-line" data-source-line="1"><span class="fp-source-text"><span class="hljs-comment">one\n</span></span></span>' +
      '<span class="fp-source-line" data-source-line="2"><span class="fp-source-text"><span class="hljs-comment">two</span></span></span>',
    )
  })
  it('keeps escaped source and nested token spans intact without injecting number text', () => {
    const source = '<span class="outer">a<span class="inner">&lt;tag&gt;\n&amp;</span>b</span>'
    const html = sourceLineHtml(source)
    expect(html.replace(/<[^>]*>/g, '')).toBe('a&lt;tag&gt;\n&amp;b')
    expect((html.match(/<span\b/g) || []).length).toBe((html.match(/<\/span>/g) || []).length)
  })
})
