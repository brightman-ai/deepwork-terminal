import { describe, expect, test } from 'bun:test'
import { createUploadScheduler, noteUploadRtt, uploadAttachment, uploadChunkSize, type UploadRequest } from '../uploadTransport'

describe('upload transport: terminal bandwidth remains available', () => {
  test('concurrent callers share one sender and a failed request releases it', async () => {
    const lane = createUploadScheduler(async () => {})
    let active = 0
    let maximum = 0
    const events: number[] = []
    const send = (n: number) => lane(async () => {
      active++
      maximum = Math.max(maximum, active)
      await new Promise(r => setTimeout(r, 2))
      active--
      events.push(n)
      if (n === 1) throw new Error('offline')
      return n
    })
    const results = await Promise.allSettled([send(1), send(2), send(3)])
    expect(maximum).toBe(1)
    expect(events).toEqual([1, 2, 3])
    expect(results.map(r => r.status)).toEqual(['rejected', 'fulfilled', 'fulfilled'])
  })

  test('body budget adapts from slow uplinks to LAN and stays bounded', () => {
    expect(uploadChunkSize(125_000)).toBe(8192) // 1 Mbit/s, <=100ms of payload
    expect(uploadChunkSize(1_250_000)).toBe(122880)
    expect(uploadChunkSize(1_000_000_000)).toBe(262144)
    expect(uploadChunkSize(0)).toBe(8192)
  })

  test('the shared two-request window bounds concurrent bulk sends', async () => {
    const lane = createUploadScheduler(async () => {}, 2)
    let active = 0
    let maximum = 0
    await Promise.all(Array.from({ length: 12 }, () => lane(async () => {
      active++
      maximum = Math.max(maximum, active)
      await new Promise(r => setTimeout(r, 2))
      active--
    })))
    expect(maximum).toBe(2)
  })

  test('real payload slices reassemble exactly; embedded URL and acknowledgement progress survive', async () => {
    const url = 'https://upload.example/api/cli/sessions/demo/paste-upload'
    noteUploadRtt(url, 50)
    const original = new Uint8Array(29_000).map((_, i) => i % 251)
    const chunks = new Map<number, Blob>()
    const progress: number[] = []
    let requestedChunkSize = 0
    const request: UploadRequest = async (target, body) => {
      if (target.endsWith('probe=1')) return { status: 200, json: { probe: true } }
      if (target.endsWith('/init')) {
        requestedChunkSize = Number((body as FormData).get('chunkSize'))
        return { status: 200, json: { uploadId: 'example', chunkSize: 8192, totalChunks: 4 } }
      }
      if (target.includes('/chunk?')) {
        expect(target.startsWith('https://upload.example/api/cli/files/upload/chunk?')).toBe(true)
        expect(target.endsWith('ack=index')).toBe(true)
        expect(body instanceof Blob).toBe(true)
        chunks.set(Number(new URL(target).searchParams.get('index')), body as Blob)
        return { status: 200, json: {} }
      }
      return { status: 200, json: { path: '/work/tmp/files/sample.bin' } }
    }
    const metadata = new FormData()
    metadata.set('name', 'sample.bin')
    const response = await uploadAttachment(url, new Blob([original]), metadata, request, sent => progress.push(sent))
    expect(requestedChunkSize).toBeGreaterThanOrEqual(8192)
    expect(progress.at(-1)).toBe(29000)
    expect(progress.every((n, i) => i === 0 || n > progress[i - 1])).toBe(true)
    expect(new Uint8Array(await new Blob([0, 1, 2, 3].map(i => chunks.get(i)!)).arrayBuffer())).toEqual(original)
    expect(response.json).toEqual({ path: '/work/tmp/files/sample.bin' })
  })

  test('a rejecting peer never receives file bytes; an older peer falls back with payload-only progress', async () => {
    const metadata = new FormData()
    metadata.set('name', 'sample.bin')
    const calls: string[] = []
    const reject: UploadRequest = async target => { calls.push(target); return { status: 413, json: { limit_mb: 1 } } }
    const blob = new Blob([new Uint8Array(20_000)])
    const result = await uploadAttachment('https://reject.example/api/sessions/demo/paste-upload', blob, metadata, reject, () => {})
    expect(result.status).toBe(413)
    expect(calls).toHaveLength(1)
    const progress: number[] = []
    const old: UploadRequest = async (target, body, onProgress) => {
      if (target.includes('/init')) return { status: 404, json: null }
      expect((body as FormData).get('file')).toBeInstanceOf(Blob)
      onProgress?.(20_500, 20_500)
      return { status: 200, json: {} }
    }
    await uploadAttachment('https://older.example/api/sessions/demo/paste-upload', blob, metadata, old, n => progress.push(n))
    expect(progress).toEqual([20_000])
  })
})
