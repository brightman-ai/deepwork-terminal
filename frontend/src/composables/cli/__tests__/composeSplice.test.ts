import { describe, it, expect } from 'bun:test'
import { spliceAtSelection } from '../useComposeBox'

// 输入框里贴图：上传后的 `@./tmp/clip/x.png ` 插到用户贴的那个位置（2026-10-13 Human：所见即所得）。
describe('spliceAtSelection — 文件引用插进输入框', () => {
  const REF = '@./tmp/clip/a.png '

  it('插在两段中文之间：前后各隔一个空格，光标落在引用之后', () => {
    const v = '好看到一些？同时'
    const at = v.indexOf('同')
    const r = spliceAtSelection(v, at, at, REF)
    expect(r.value).toBe('好看到一些？ @./tmp/clip/a.png 同时')
    expect(r.value.slice(0, r.caret)).toBe('好看到一些？ @./tmp/clip/a.png ')
  })

  it('空输入框：只有引用和一个尾随空格（可以接着打字）', () => {
    expect(spliceAtSelection('', 0, 0, REF)).toEqual({ value: '@./tmp/clip/a.png ', caret: 18 })
  })

  it('两侧已有空白：不再补，不出现双空格', () => {
    const r = spliceAtSelection('看 这张', 2, 2, REF)
    expect(r.value).toBe('看 @./tmp/clip/a.png 这张')
    expect(r.value).not.toContain('  ')
  })

  it('有选区：引用替换掉选中的文字', () => {
    const r = spliceAtSelection('看[图]这张', 1, 4, REF)
    expect(r.value).toBe('看 @./tmp/clip/a.png 这张')
  })

  it('越界选区被夹住，不抛错', () => {
    expect(spliceAtSelection('ab', 10, 20, REF).value).toBe('ab @./tmp/clip/a.png ')
  })
})
