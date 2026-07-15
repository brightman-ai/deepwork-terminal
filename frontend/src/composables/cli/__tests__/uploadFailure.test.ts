import { describe, expect, test } from 'bun:test'
import { classifyUploadFailure } from '../uploadFailure'
import { createUploadProgressStore } from '../useUploadProgress'

/**
 * The bug being pinned: a .drawio was rejected by a server-side MIME allowlist (400 —
 * deterministic), and the upload float offered 重试 anyway. The user pressed it. The whole
 * file uploaded a second time and was refused identically 7 seconds later.
 *
 * A retry button may only exist where a retry can actually succeed.
 */
describe('classifyUploadFailure — retry is a promise, not decoration', () => {
  test('413 is deterministic: no retry, and the limit is quoted from the server', () => {
    const r = classifyUploadFailure(413, { error: 'file exceeds the 10 MB limit', limit_mb: 10 })
    expect(r.retryable).toBe(false)
    expect(r.message).toBe('文件超过 10 MB 上限')
  })

  test('413 without limit_mb refuses to invent a number', () => {
    const r = classifyUploadFailure(413, null)
    expect(r.retryable).toBe(false)
    expect(r.message).not.toMatch(/\d/)
  })

  test('other 4xx = the server judging THESE bytes → no retry; its words survive for diagnosis', () => {
    const r = classifyUploadFailure(400, { error: 'invalid multipart form' })
    expect(r.retryable).toBe(false)
    expect(r.message).toContain('invalid multipart form')
  })

  test('401/429 stay retryable — the user can re-auth and the same bytes then go through', () => {
    expect(classifyUploadFailure(401, null).retryable).toBe(true)
    expect(classifyUploadFailure(429, null).retryable).toBe(true)
  })

  test('5xx is a condition of the moment → retry is honest', () => {
    expect(classifyUploadFailure(500, null).retryable).toBe(true)
    expect(classifyUploadFailure(502, null).retryable).toBe(true)
  })
})

describe('useUploadProgress — fail(retryable) governs the button', () => {
  test('a deterministic failure drops the retry action, so the float cannot render 重试', () => {
    const s = createUploadProgressStore()
    let retried = 0
    const id = s.register('arch.drawio', 1024, () => { retried++ }, true)

    s.fail(id, '文件超过 10 MB 上限', false)

    const entry = s.entries.value.find(e => e.id === id)
    expect(entry?.status).toBe('error')
    expect(entry?.retry).toBeUndefined()
    expect(retried).toBe(0)
  })

  test('a transient failure keeps the retry action', () => {
    const s = createUploadProgressStore()
    const id = s.register('arch.drawio', 1024, () => {}, true)

    s.fail(id, '服务端错误 (HTTP 500)', true)

    expect(typeof s.entries.value.find(e => e.id === id)?.retry).toBe('function')
  })

  test('fail() still defaults to retryable, so existing call sites keep their button', () => {
    const s = createUploadProgressStore()
    const id = s.register('x.png', 10, () => {}, true)
    s.fail(id, 'boom')
    expect(typeof s.entries.value.find(e => e.id === id)?.retry).toBe('function')
  })

  test('an error surfaces even inside the 300ms grace window', () => {
    const s = createUploadProgressStore()
    const id = s.register('arch.drawio', 1024, undefined, false)
    s.fail(id, 'boom', false)
    expect(s.entries.value.map(e => e.id)).toContain(id)
  })
})
