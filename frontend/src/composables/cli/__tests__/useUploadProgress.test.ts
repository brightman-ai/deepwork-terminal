import { describe, expect, test } from 'bun:test'

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

async function store() {
  return import('../useUploadProgress')
}

describe('useUploadProgress — delayed-reveal upload store', () => {
  test('a fast upload that completes within the grace window is never revealed', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    const id = s.register('clipboard.png', 1024)
    s.complete(id)
    // Even after the grace window elapses, nothing should ever have appeared.
    await sleep(340)
    expect(s.entries.value.length).toBe(0)
  })

  test('an upload still in flight past 300ms becomes visible', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    const id = s.register('big-screenshot.png', 5_000_000)
    expect(s.entries.value.length).toBe(0) // not revealed yet
    await sleep(340)
    expect(s.entries.value.length).toBe(1)
    expect(s.entries.value[0].id).toBe(id)
    expect(s.entries.value[0].status).toBe('uploading')
  })

  test('an error ALWAYS reveals immediately, even inside the 300ms grace window', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    const id = s.register('note.docx', 2048)
    s.fail(id, 'network error')
    expect(s.entries.value.length).toBe(1)
    expect(s.entries.value[0].status).toBe('error')
    expect(s.entries.value[0].error).toBe('network error')
  })

  test('progress() updates sent/total on the same visible entry', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    // revealImmediately=true — same path a user-triggered retry takes.
    const id = s.register('video.mp4', 10_000_000, undefined, true)
    s.progress(id, 2_500_000, 10_000_000)
    expect(s.entries.value[0].sent).toBe(2_500_000)
    s.progress(id, 9_000_000, 10_000_000)
    expect(s.entries.value[0].sent).toBe(9_000_000)
  })

  test('a revealed success flashes done then auto-dismisses', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    const id = s.register('a.png', 1024, undefined, true)
    s.complete(id)
    expect(s.entries.value.length).toBe(1)
    expect(s.entries.value[0].status).toBe('done')
    await sleep(1600)
    expect(s.entries.value.length).toBe(0)
  })

  test('retry() is carried on the entry so a caller can invoke it', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    let retried = false
    s.register('x.png', 10, () => { retried = true }, true)
    s.entries.value[0].retry?.()
    expect(retried).toBe(true)
  })

  test('multiple concurrent uploads are tracked independently', async () => {
    const { createUploadProgressStore } = await store()
    const s = createUploadProgressStore()
    const a = s.register('a.png', 10, undefined, true)
    const b = s.register('b.png', 20, undefined, true)
    expect(s.entries.value.length).toBe(2)
    s.complete(a)
    expect(s.entries.value.find(e => e.id === a)?.status).toBe('done')
    expect(s.entries.value.find(e => e.id === b)?.status).toBe('uploading')
  })
})
