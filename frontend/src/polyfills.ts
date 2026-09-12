/**
 * polyfills — 最小 API 兼容层（2026-09-11，REQ-fp-a2 附带发现）。
 *
 * 背景：pdfjs-dist@5 / docx-preview@0.4 的下发 chunk 用了 `Promise.withResolvers` 与
 * `Object.groupBy`（Safari/iOS ≥ 17.4 才有），Vite 只转译语法、不补 API——iOS ≤ 17.3 的
 * 设备上这两个渲染器必挂。本文件按 **特性检测** 补齐缺失 API：新浏览器上一行不改。
 *
 * 为什么必须在入口第一行 import：必须先于任何 vendor chunk（pdfjs / docx-preview /
 * vue-vendor）求值——API 调用发生在模块顶层时就已生效。
 *
 * 这里只写与 vendor chunk 相关、且实现代价小的几件；刻意不做通用 polyfill 库（core-js 全量
 * 是几十 KB，为一两个 API 不值）。实现按 MDN 语义的常用子集，够渲染器跑即可。
 *
 * 可测性：全部补丁装在传入的 surface 上（生产 = globalThis 各构造器/Array.prototype，
 * 测试 = 干净假对象），单测不碰真全局。
 */

type Resolvers<T> = {
  promise: Promise<T>
  resolve: (value: T | PromiseLike<T>) => void
  reject: (reason?: unknown) => void
}

function withResolvers<T>(): Resolvers<T> {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function groupBy<K extends PropertyKey, T>(items: Iterable<T>, cb: (item: T, index: number) => K): Record<string, T[]> {
  const map = new Map<K, T[]>()
  let i = 0
  for (const item of items) {
    const key = cb(item, i++)
    const bucket = map.get(key)
    if (bucket) bucket.push(item)
    else map.set(key, [item])
  }
  return Object.fromEntries(map) as Record<string, T[]>
}

function mapGroupBy<K, T>(items: Iterable<T>, cb: (item: T, index: number) => K): Map<K, T[]> {
  const map = new Map<K, T[]>()
  let i = 0
  for (const item of items) {
    const key = cb(item, i++)
    const bucket = map.get(key)
    if (bucket) bucket.push(item)
    else map.set(key, [item])
  }
  return map
}

function toSorted<T>(arr: Iterable<T>, cmp?: (a: T, b: T) => number): T[] {
  return [...arr].sort(cmp)
}
function toReversed<T>(arr: Iterable<T>): T[] {
  return [...arr].reverse()
}
function toSpliced<T>(arr: Iterable<T>, start: number, deleteCount?: number, ...items: unknown[]): T[] {
  const copy = [...arr] as T[] & { splice(s: number, d?: number, ...i: unknown[]): unknown[] }
  copy.splice(start, deleteCount ?? copy.length - start, ...items)
  return copy
}

export interface ApiSurface {
  Promise?: { withResolvers?: unknown }
  Object?: Record<string, unknown>
  Map?: { groupBy?: unknown } & MapConstructor
  ArrayPrototype?: Record<string, unknown>
}

/** 全部装到传入 surface 上，只补缺失的（特性检测——新浏览器零改动）。 */
export function installMissingApis(surface: ApiSurface): void {
  if (surface.Promise && typeof surface.Promise.withResolvers !== 'function') {
    surface.Promise.withResolvers = withResolvers
  }
  if (surface.Object && typeof surface.Object.groupBy !== 'function') {
    surface.Object.groupBy = groupBy
  }
  if (surface.Map && typeof surface.Map.groupBy !== 'function') {
    surface.Map.groupBy = mapGroupBy
  }
  if (surface.ArrayPrototype) {
    const P = surface.ArrayPrototype
    if (typeof P.toSorted !== 'function') P.toSorted = toSorted
    if (typeof P.toReversed !== 'function') P.toReversed = toReversed
    if (typeof P.toSpliced !== 'function') P.toSpliced = toSpliced
  }
}

// 生产入口：装到真实全局。这一行就是本文件的副作用，main.ts 必须【第一个】import 它。
installMissingApis({
  Promise: (globalThis as { Promise?: { withResolvers?: unknown } }).Promise,
  Object: globalThis.Object as unknown as Record<string, unknown>,
  Map: globalThis.Map as unknown as { groupBy?: unknown } & MapConstructor,
  ArrayPrototype: Array.prototype as unknown as Record<string, unknown>,
})
