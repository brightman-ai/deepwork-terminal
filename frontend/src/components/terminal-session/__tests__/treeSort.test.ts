import { describe, it, expect } from 'bun:test'
import { compareTreeEntries, sortTreeLevel, nextTreeSort, TREE_SORT_CYCLE, type TreeSortEntry } from '../treeSort'

/**
 * REQ-fp-tree-sort（2026-09-12）：目录树按时间/大小排序。这个比较器坏掉的方式不报错：
 * 目录混进文件中间、时间序来回跳——扫读一乱，排序反而比不排序更糟。所以每条语义都钉死。
 */

const e = (name: string, o: Partial<TreeSortEntry> = {}): TreeSortEntry => ({
  name, isDir: false, size: 100, mtimeMs: 1000, ...o,
})

describe('compareTreeEntries（目录恒在前，组内按键）', () => {
  const dirA = e('zzz-dir', { isDir: true })
  const dirB = e('aaa-dir', { isDir: true })
  const old = e('aaa.md', { mtimeMs: 100, size: 1 })
  const newFile = e('zzz.md', { mtimeMs: 900, size: 9999 })

  it('time 模式：目录组在前（组内名字升序），文件按时间新→旧', () => {
    const out = sortTreeLevel([newFile, dirA, old, dirB], 'time')
    expect(out.map((x) => x.name)).toEqual(['aaa-dir', 'zzz-dir', 'zzz.md', 'aaa.md'])
  })

  it('size 模式：文件按大小大→小；目录不受 size 影响（目录组内名字序）', () => {
    const out = sortTreeLevel([old, dirA, newFile, dirB], 'size')
    expect(out.map((x) => x.name)).toEqual(['aaa-dir', 'zzz-dir', 'zzz.md', 'aaa.md'])
  })

  it('name 模式：等价于名字升序（目录在前）', () => {
    const out = sortTreeLevel([newFile, dirA, old, dirB], 'name')
    expect(out.map((x) => x.name)).toEqual(['aaa-dir', 'zzz-dir', 'aaa.md', 'zzz.md'])
  })

  it('目录组内恒为名字升序（目录 mtime 随内容增删跳动，按它排会来回抖）', () => {
    const d1 = e('b-dir', { isDir: true, mtimeMs: 500 })
    const d2 = e('a-dir', { isDir: true, mtimeMs: 900 })
    const out = sortTreeLevel([d1, d2], 'time')
    expect(out.map((x) => x.name)).toEqual(['a-dir', 'b-dir'])
  })

  it('同键同值 → 名字升序兜底（稳定不抖动）', () => {
    const f1 = e('b.txt', { mtimeMs: 500 })
    const f2 = e('a.txt', { mtimeMs: 500 })
    const out = sortTreeLevel([f1, f2], 'time')
    expect(out.map((x) => x.name)).toEqual(['a.txt', 'b.txt'])
  })

  it('compareTreeEntries 直接可用（flatten 每层排序用同一把尺）', () => {
    expect(compareTreeEntries(old, newFile, 'time')).toBeGreaterThan(0)
    expect(compareTreeEntries(dirA, old, 'time')).toBeLessThan(0)
  })

  it('循环顺序与 nextTreeSort', () => {
    expect(TREE_SORT_CYCLE).toEqual(['name', 'time', 'size'])
    expect(nextTreeSort('name')).toBe('time')
    expect(nextTreeSort('time')).toBe('size')
    expect(nextTreeSort('size')).toBe('name')
  })
})
