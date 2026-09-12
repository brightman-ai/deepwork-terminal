import { describe, it, expect } from 'bun:test'
import { installMissingApis, type ApiSurface } from './polyfills'

/** REQ-fp-a2 附带发现：vendor chunk（pdfjs/docx-preview）依赖的 API，在干净假靶子上补齐。 */

function fakeSurface(): ApiSurface {
  return { Promise: {}, Object: {}, Map: class FakeMap extends Map {} as unknown as ApiSurface['Map'], ArrayPrototype: {} }
}

describe('installMissingApis（只补缺失的）', () => {
  it('干净靶子：全部装上且行为正确', () => {
    const s = fakeSurface()
    installMissingApis(s)

    const P = (s.Promise as { withResolvers: () => { promise: Promise<string>; resolve: (v: string) => void } })
    const r = P.withResolvers()
    r.resolve('ok')
    expect(r.promise).resolves.toBe('ok')

    const O = s.Object as { groupBy: <T, K extends PropertyKey>(items: T[], cb: (x: T) => K) => Record<string, T[]> }
    const grouped = O.groupBy([1, 2, 3], (n) => (n % 2 === 0 ? 'even' : 'odd'))
    expect(grouped.odd).toEqual([1, 3])
    expect(grouped.even).toEqual([2])

    const M = s.Map as { groupBy: <T, K>(items: T[], cb: (x: T) => K) => Map<K, T[]> }
    const mg = M.groupBy(['a', 'ab', 'bc'], (w) => w.length)
    expect(mg.get(1)).toEqual(['a'])
    expect(mg.get(2)).toEqual(['ab', 'bc'])

    const Ap = s.ArrayPrototype as Record<string, (arr: number[], ...rest: unknown[]) => number[]>
    expect(Ap.toSorted([3, 1, 2])).toEqual([1, 2, 3])
    expect(Ap.toReversed([1, 2, 3])).toEqual([3, 2, 1])
    expect(Ap.toSpliced([1, 2, 3], 1, 1, 9)).toEqual([1, 9, 3])
  })

  it('toSorted/toReversed 不改原数组（MDN 语义）', () => {
    const s = fakeSurface()
    installMissingApis(s)
    const Ap = s.ArrayPrototype as Record<string, (arr: number[]) => number[]>
    const original = [3, 1, 2]
    void Ap.toSorted(original)
    void Ap.toReversed(original)
    expect(original).toEqual([3, 1, 2])
  })

  it('已有实现不被覆盖（真 globalThis 上跑一遍 = 生产路径冒烟）', () => {
    const s: ApiSurface = {
      Promise: globalThis.Promise as unknown as ApiSurface['Promise'],
      Object: globalThis.Object as unknown as Record<string, unknown>,
      Map: globalThis.Map as unknown as ApiSurface['Map'],
      ArrayPrototype: Array.prototype as unknown as Record<string, unknown>,
    }
    const before = (globalThis.Promise as unknown as { withResolvers?: unknown }).withResolvers
    installMissingApis(s)
    expect((globalThis.Promise as unknown as { withResolvers?: unknown }).withResolvers).toBe(before)
    // 真环境行为不变
    const r = Promise.withResolvers<number>()
    r.resolve(7)
    expect(r.promise).resolves.toBe(7)
  })
})
