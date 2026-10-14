import { afterEach, describe, expect, it } from 'bun:test'
import { _flushNowForTest, reportServerEvent } from '../useCliInputDiagnostics'

const realFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = realFetch
})

describe('diagnostics transport', () => {
  it('keeps the events of a failed flush and reports the failure once a flush gets through', async () => {
    globalThis.fetch = (async () => { throw new TypeError('network down') }) as unknown as typeof fetch
    reportServerEvent('probe.event', { n: 1 })
    const afterFailure = await _flushNowForTest()
    expect(afterFailure.some(e => e.msg === 'probe.event')).toBe(true)
    expect(afterFailure.some(e => e.msg === 'cli.diag.transport' && e.outcome === 'network')).toBe(true)

    const sent: unknown[] = []
    globalThis.fetch = (async (_url: string, init?: RequestInit) => {
      sent.push(...JSON.parse(String(init?.body)).events)
      return new Response(null, { status: 204 })
    }) as unknown as typeof fetch
    const afterSuccess = await _flushNowForTest()
    expect(afterSuccess.filter(e => e.msg === 'probe.event')).toHaveLength(0)
    expect(sent.some(e => (e as { msg: string }).msg === 'probe.event')).toBe(true)
  })

  it('treats a non-2xx answer as a failure, not a delivery', async () => {
    globalThis.fetch = (async () => new Response(null, { status: 401 })) as unknown as typeof fetch
    reportServerEvent('probe.unauthorized', {})
    const queued = await _flushNowForTest()
    expect(queued.some(e => e.msg === 'probe.unauthorized')).toBe(true)
    expect(queued.some(e => e.msg === 'cli.diag.transport' && e.outcome === 'http_401')).toBe(true)
    globalThis.fetch = (async () => new Response(null, { status: 204 })) as unknown as typeof fetch
    await _flushNowForTest()
  })
})
