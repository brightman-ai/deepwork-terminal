import { describe, expect, test } from 'bun:test'
import { xhrUploadFormData } from '../uploadHttp'

class NativeXhrBoundary {
  static last: NativeXhrBoundary
  timeout = 0
  responseText = ''
  status = 0
  upload: { onprogress?: (event: ProgressEvent) => void } = {}
  onload?: () => void
  onerror?: () => void
  ontimeout?: () => void
  onabort?: () => void
  constructor() { NativeXhrBoundary.last = this }
  open() {}
  setRequestHeader() {}
  send() {}
  abort() { this.onabort?.() }
}

async function usingNativeBoundary(check: () => Promise<void>) {
  const original = globalThis.XMLHttpRequest
  globalThis.XMLHttpRequest = NativeXhrBoundary as unknown as typeof XMLHttpRequest
  try { await check() }
  finally { globalThis.XMLHttpRequest = original }
}

describe('upload native request lifecycle', () => {
  test('a stalled request has a finite native deadline and rejects on expiry', async () => {
    await usingNativeBoundary(async () => {
      const result = xhrUploadFormData('/upload', new Blob(['payload']), {})
      expect(NativeXhrBoundary.last.timeout).toBe(90000)
      NativeXhrBoundary.last.ontimeout?.()
      await expect(result).rejects.toThrow('upload stalled')
    })
  })
  test('native cancellation rejects instead of stranding the shared sender', async () => {
    await usingNativeBoundary(async () => {
      const result = xhrUploadFormData('/upload', new Blob(['payload']), {})
      NativeXhrBoundary.last.abort()
      await expect(result).rejects.toThrow('upload aborted')
    })
  })
  test('network rejection and a successful response both settle the request', async () => {
    await usingNativeBoundary(async () => {
      const failed = xhrUploadFormData('/upload', new Blob(['payload']), {})
      NativeXhrBoundary.last.onerror?.()
      await expect(failed).rejects.toThrow('network error')
      const success = xhrUploadFormData('/upload', new Blob(['payload']), {})
      NativeXhrBoundary.last.status = 200
      NativeXhrBoundary.last.responseText = '{"size":7}'
      NativeXhrBoundary.last.onload?.()
      expect(await success).toEqual({ status: 200, json: { size: 7 } })
    })
  })
})
