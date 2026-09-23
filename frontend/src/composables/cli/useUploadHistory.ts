import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import type { fetchUploadsPage, UploadsResponse } from '../../api/uploads'

const emptyPage = (): UploadsResponse => ({ items: [], total: 0, counts: { images: 0, files: 0 }, sessions: [] })

/** Only the visible category fetches. Cached pages survive a drawer close/tab switch. */
export function useUploadHistory(options: {
  active: Readonly<Ref<boolean>>
  kind: Readonly<Ref<'image' | 'file'>>
  search: Readonly<Ref<string>>
  session: Readonly<Ref<string>>
  newest: Readonly<Ref<boolean>>
}, fetchPage: typeof fetchUploadsPage) {
  const page = ref<UploadsResponse>(emptyPage())
  const loading = ref(false)
  const error = ref('')
  const queryText = ref(options.search.value)
  const inventory = ref<Pick<UploadsResponse, 'counts' | 'sessions'>>(emptyPage())
  const cache = new Map<string, { page: UploadsResponse; at: number }>()
  const query = computed(() => ({ kind: options.kind.value, q: queryText.value.trim(),
    session: options.session.value, order: options.newest.value ? 'newest' as const : 'oldest' as const }))
  const key = computed(() => JSON.stringify(query.value))
  let request: AbortController | null = null
  let searchTimer: ReturnType<typeof setTimeout> | undefined
  let retryAppend = false

  watch(options.search, (value) => {
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => { queryText.value = value }, 220)
  })

  async function load(append = false, force = false): Promise<void> {
    request?.abort()
    request = null
    loading.value = false
    if (!options.active.value) return
    error.value = ''
    const requestKey = key.value
    if (!append) {
      const cached = cache.get(requestKey)
      page.value = cached?.page ?? emptyPage()
      if (cached && !force && Date.now() - cached.at < 30_000) return
    } else if (!page.value.nextCursor) return
    const controller = new AbortController()
    request = controller
    loading.value = true
    retryAppend = append
    let timedOut = false
    const timeout = setTimeout(() => { timedOut = true; controller.abort() }, 10_000)
    try {
      const result = await fetchPage({ ...query.value, cursor: append ? page.value.nextCursor : undefined }, controller.signal)
      if (request !== controller) return
      if (append) {
        const seen = new Set(page.value.items.map(item => item.id))
        result.items = [...page.value.items, ...result.items.filter(item => !seen.has(item.id))]
      }
      page.value = result
      inventory.value = { counts: result.counts, sessions: result.sessions }
      cache.delete(requestKey)
      cache.set(requestKey, { page: result, at: Date.now() })
      if (cache.size > 12) cache.delete(cache.keys().next().value!)
    } catch {
      if (request === controller) error.value = timedOut ? '加载超时，请重试' : '加载失败，请重试'
    } finally {
      clearTimeout(timeout)
      if (request === controller) { request = null; loading.value = false }
    }
  }
  watch([options.active, key], () => { void load() }, { immediate: true })
  onScopeDispose(() => { request?.abort(); request = null; clearTimeout(searchTimer) })
  function invalidate(): void { cache.clear(); void load(false, true) }
  return { page, inventory, loading, error, invalidate,
    loadMore: () => load(true), retry: () => load(retryAppend, true), refresh: () => load(false, true) }
}
