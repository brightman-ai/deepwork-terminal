import { describe, it, expect } from 'bun:test'
import {
  DEFAULT_TYPE_TO_COMPOSE,
  createTypeToComposeSetting,
  normalizeTypeToCompose,
  type SettingStore,
} from '../typeToComposeSetting'

/** 一个独立的假服务端 KV：不碰 useServerStore 的进程级单例（那会污染别的测试对"最后一次落盘"的断言）。 */
function fakeStore(initial: Record<string, unknown>, opts: { failLoad?: boolean } = {}) {
  const saved: Array<Record<string, unknown>> = []
  let data: Record<string, unknown> = {}
  let hydrated = false
  const store: SettingStore = {
    async load() {
      if (opts.failLoad) throw new Error('offline')
      data = { ...initial }
      hydrated = true
    },
    get: <T>(k: string, d: T) => (data[k] !== undefined ? (data[k] as T) : d),
    set(k, v) { data[k] = v; saved.push({ ...data }) },
    isHydrated: () => hydrated,
  }
  return { store, saved }
}

describe('REQ-TI-08 总开关', () => {
  it('默认开', () => {
    expect(DEFAULT_TYPE_TO_COMPOSE).toBe(true)
    // 还没读到服务端之前
    expect(createTypeToComposeSetting(fakeStore({}).store).enabled.value).toBe(true)
  })

  it('缺失 / 坏数据一律按默认（开），不会因为读到垃圾就悄悄关掉', () => {
    for (const raw of [undefined, null, 'false', 0, 1, {}, []]) expect(normalizeTypeToCompose(raw)).toBe(true)
    expect(normalizeTypeToCompose(false)).toBe(false)
    expect(normalizeTypeToCompose(true)).toBe(true)
  })

  it('服务端没存过 → load 之后仍是开', async () => {
    const s = createTypeToComposeSetting(fakeStore({}).store)
    await s.load()
    expect(s.enabled.value).toBe(true)
  })

  it('服务端存的是关 → load 之后读到关（刷新后保持，换 trycloudflare 域名也保持）', async () => {
    const s = createTypeToComposeSetting(fakeStore({ typeToCompose: false }).store)
    await s.load()
    expect(s.isHydrated()).toBe(true)
    expect(s.enabled.value).toBe(false)
  })

  it('读服务端失败：保持默认（开），并且如实报告"没读到"，设置页据此禁止修改', async () => {
    const s = createTypeToComposeSetting(fakeStore({ typeToCompose: false }, { failLoad: true }).store)
    await s.load()
    expect(s.isHydrated()).toBe(false)
    expect(s.enabled.value).toBe(true)
  })

  it('改动立刻生效，并写进服务端 KV', async () => {
    const { store, saved } = fakeStore({ typeToCompose: true })
    const s = createTypeToComposeSetting(store)
    await s.load()
    s.setEnabled(false)
    expect(s.enabled.value).toBe(false)
    expect(saved.at(-1)).toEqual({ typeToCompose: false })
    s.setEnabled(true)
    expect(saved.at(-1)).toEqual({ typeToCompose: true })
  })
})
