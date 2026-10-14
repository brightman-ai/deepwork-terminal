/**
 * typeToComposeSetting — 「打字即成框」总开关的逻辑本体（terminal-input REQ-TI-08）。默认开。
 *
 * 只依赖 vue 和一个注入进来的 store 接口，不 import useServerStore：后者的依赖链在加载时就读
 * `window.location`（useCliAuth），单独跑这个模块的测试会因为 `window is not defined` 在 import 阶段
 * 崩掉。把"逻辑"和"绑定到真 store"拆成两个文件，测试才能自洽，也不用去碰 useServerStore 这个进程级
 * 单例（碰它会污染别的测试对"最后一次落盘"的断言）。绑定在 useTypeToComposeSetting.ts。
 */
import { computed, ref } from 'vue'

export const STORE_KEY = 'typeToCompose'

/** 没人动过就是开。读到的值不是 boolean（坏数据 / 缺失）一律按默认，不按"关"。 */
export const DEFAULT_TYPE_TO_COMPOSE = true

export function normalizeTypeToCompose(raw: unknown): boolean {
  return typeof raw === 'boolean' ? raw : DEFAULT_TYPE_TO_COMPOSE
}

/** useServerStore 的、这里用到的那一小块。 */
export interface SettingStore {
  load(): Promise<void>
  get<T>(key: string, defaultVal: T): T
  set(key: string, value: unknown): void
  isHydrated(): boolean
}

export function createTypeToComposeSetting(store: SettingStore) {
  const enabled = ref(DEFAULT_TYPE_TO_COMPOSE)

  async function load(): Promise<void> {
    await store.load().catch(() => {})
    enabled.value = normalizeTypeToCompose(store.get<unknown>(STORE_KEY, undefined))
  }

  function setEnabled(v: boolean): void {
    enabled.value = v
    store.set(STORE_KEY, v)
  }

  return {
    enabled: computed(() => enabled.value),
    load,
    setEnabled,
    /** 服务端的值读到了没有。没读到时不该允许改——useServerStore 此时不会落盘，改动会在读到的那一刻被覆盖。 */
    isHydrated: () => store.isHydrated(),
  }
}
