/**
 * useServerStore — 服务端持久化 KV 存储。
 *
 * 数据存在服务器 ~/.dw-terminal/store.json，不受浏览器 origin 影响。
 * 适用于 snippets、history 等跨 trycloudflare 域名需要保留的用户数据。
 *
 * 使用方式:
 *   const store = useServerStore()
 *   await store.load()
 *   const snippets = store.get<string[]>('snippets', [])
 *   store.set('snippets', newValue)   // 500ms debounce 写入服务端
 */
import { ref } from 'vue'
import { fetchStore, saveStore } from '@terminal/api/store'

// 模块级单例 — 所有组件实例共享同一份数据，避免重复请求
const data = ref<Record<string, unknown>>({})
let loadPromise: Promise<void> | null = null
let hydrated = false // true only after a SUCCESSFUL load — the gate that prevents overwriting the server with partial data
let saveTimer: ReturnType<typeof setTimeout> | null = null

export function useServerStore() {
  function load(): Promise<void> {
    if (hydrated) return Promise.resolve()
    if (!loadPromise) {
      loadPromise = fetchStore()
        .then(d => {
          data.value = d
          hydrated = true
        })
        .catch(() => {
          // Load failed (e.g. the server is restarting) — do NOT pretend the store is empty.
          // Clear the promise so a later call retries, and stay un-hydrated so set() refuses to
          // persist: a partial/empty `data` must never overwrite the server's real data.
          loadPromise = null
        })
    }
    return loadPromise
  }

  function get<T>(key: string, defaultVal: T): T {
    const v = data.value[key]
    return v !== undefined ? (v as T) : defaultVal
  }

  function set(key: string, value: unknown): void {
    data.value[key] = value
    // Never persist before a successful hydrate: our `data` may be missing keys, and saving it
    // would wipe them server-side (the restart-time data-loss bug). Kick a (re)load instead; once
    // it succeeds, this and subsequent sets persist normally. The backend also merges per-key as a
    // second line of defence, but this stops us from clobbering a key's OWN value with a stale one.
    if (!hydrated) {
      void load()
      return
    }
    if (saveTimer !== null) clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      saveStore(data.value).catch(() => {})
    }, 500)
  }

  // isHydrated reports whether a load() actually succeeded (vs failed/never-run).
  // Consumers must distinguish "store loaded and this key is genuinely absent" from
  // "store never loaded" — e.g. useRemotePeers must not render a tab's peer as DELETED
  // just because a failed GET left the registry empty.
  return { load, get, set, isHydrated: () => hydrated }
}
