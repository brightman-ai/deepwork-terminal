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
import { fetchStore, saveStore } from '@/api/store'

// 模块级单例 — 所有组件实例共享同一份数据，避免重复请求
const data = ref<Record<string, unknown>>({})
let loadPromise: Promise<void> | null = null
let saveTimer: ReturnType<typeof setTimeout> | null = null

export function useServerStore() {
  function load(): Promise<void> {
    if (!loadPromise) {
      loadPromise = fetchStore()
        .then(d => { data.value = d })
        .catch(() => { /* 网络失败时保持空对象，不阻塞 UI */ })
    }
    return loadPromise
  }

  function get<T>(key: string, defaultVal: T): T {
    const v = data.value[key]
    return v !== undefined ? (v as T) : defaultVal
  }

  function set(key: string, value: unknown): void {
    data.value[key] = value
    if (saveTimer !== null) clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      saveStore(data.value).catch(() => {})
    }, 500)
  }

  return { load, get, set }
}
