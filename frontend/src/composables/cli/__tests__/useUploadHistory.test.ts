import { describe, expect, test } from 'bun:test'
import { effectScope, nextTick, ref } from 'vue'
import { useUploadHistory } from '../useUploadHistory'
import type { UploadsResponse, UploadItem } from '../../../api/uploads'

function page(ids: string[], cursor?: string): UploadsResponse {
  return { items: ids.map(id => ({ id, name: id, kind: 'file' } as UploadItem)),
    total: 3, counts: { files: 3, images: 0 }, sessions: ['design'], nextCursor: cursor }
}
function options() {
  return { active: ref(true), kind: ref<'file' | 'image'>('file'), search: ref(''), session: ref(''), newest: ref(true) }
}
async function settle() { await nextTick(); await Promise.resolve(); await nextTick() }

describe('history pages survive interruption', () => {
  test('a failed next page keeps old results; retry appends once and cached reopening preserves progress', async () => {
    const scope = effectScope(), o = options()
    let failNext = true
    const history = scope.run(() => useUploadHistory(o, async q => {
      if (!q.cursor) return page(['recent'], 'older')
      if (failNext) throw Error('offline')
      return page(['recent', 'middle', 'old'])
    }))!
    try {
      await settle(); expect(history.page.value.items.map(i => i.id)).toEqual(['recent'])
      await history.loadMore(); expect(history.error.value).toContain('重试')
      expect(history.page.value.items.map(i => i.id)).toEqual(['recent'])
      failNext = false; await history.retry()
      expect(history.page.value.items.map(i => i.id)).toEqual(['recent', 'middle', 'old'])
      expect(history.error.value).toBe('')
      o.active.value = false; await settle(); o.active.value = true; await settle()
      expect(history.page.value.items.map(i => i.id)).toEqual(['recent', 'middle', 'old'])
    } finally { scope.stop() }
  })
  test('a late response from the previous category cannot overwrite the current category', async () => {
    const scope = effectScope(), o = options()
    let release!: (p: UploadsResponse) => void
    const history = scope.run(() => useUploadHistory(o, q => q.kind === 'file'
      ? new Promise(resolve => { release = resolve }) : Promise.resolve(page(['current-image']))))!
    try {
      o.kind.value = 'image'; await settle()
      expect(history.page.value.items[0].id).toBe('current-image')
      release(page(['stale-file'])); await settle()
      expect(history.page.value.items[0].id).toBe('current-image')
      expect(history.loading.value).toBe(false)
    } finally { scope.stop() }
  })
  test('a hidden panel cancels its request without publishing stale data or an error', async () => {
    const scope = effectScope(), o = options()
    let release!: (p: UploadsResponse) => void
    let signal: AbortSignal | undefined
    const history = scope.run(() => useUploadHistory(o, (_, s) => {
      signal = s; return new Promise(resolve => { release = resolve })
    }))!
    try {
      o.active.value = false; await settle(); expect(signal?.aborted).toBe(true)
      release(page(['stale'])); await settle()
      expect(history.page.value.items).toEqual([])
      expect(history.error.value).toBe('')
      expect(history.loading.value).toBe(false)
    } finally { scope.stop() }
  })
})
