import { describe, it, expect, afterEach } from 'bun:test'
import { probePeer } from '../peerProbe'

/**
 * probePeer resilience (RT-2/RT-4): a DERP-relayed tailscale peer blips briefly, so a single
 * transient probe failure must NOT hard-block a peer that is actually reachable — the transient
 * class (thrown fetch / timeout) retries; a wrong code (401) fails fast. Deterministic via a
 * mocked global fetch + delayMs:0.
 */
const realFetch = globalThis.fetch
afterEach(() => { globalThis.fetch = realFetch })

const ok200 = { status: 200, ok: true } as Response
const r401 = { status: 401, ok: false } as Response
const netThrow = () => { throw new TypeError('Failed to fetch') }

/** Install a fetch that yields responses[i] per call (last one sticks); a function entry is called. */
function seqFetch(responses: Array<Response | (() => Response)>): () => number {
  let calls = 0
  globalThis.fetch = (async () => {
    const r = responses[Math.min(calls, responses.length - 1)]
    calls++
    return typeof r === 'function' ? r() : r
  }) as unknown as typeof fetch
  return () => calls
}

describe('probePeer — transient-blip resilience', () => {
  it('retries a transient network throw, then connects when the peer answers (the reported bug)', async () => {
    const calls = seqFetch([netThrow, ok200]) // blip once, then reachable
    const r = await probePeer('http://stmac:8087', 'CODE', { delayMs: 0 })
    expect(r.ok).toBe(true)
    expect(calls()).toBe(2)
  })

  it('does NOT retry a wrong code (401) — fails fast, one attempt', async () => {
    const calls = seqFetch([r401, ok200]) // even if a later attempt would succeed, 401 stops now
    const r = await probePeer('http://stmac:8087', 'BAD', { delayMs: 0 })
    expect(r.ok).toBe(false)
    expect(r.error).toBe('认证码错误')
    expect(calls()).toBe(1)
  })

  it('a reachable peer connects on the first try (no retry)', async () => {
    const calls = seqFetch([ok200])
    const r = await probePeer('http://stmac:8087', 'CODE', { delayMs: 0 })
    expect(r.ok).toBe(true)
    expect(calls()).toBe(1)
  })

  it('gives up after all attempts fail, surfacing an unreachable error', async () => {
    const calls = seqFetch([netThrow])
    const r = await probePeer('http://stmac:8087', 'CODE', { attempts: 3, delayMs: 0 })
    expect(r.ok).toBe(false)
    expect(r.error).toContain('地址不可达')
    expect(calls()).toBe(3)
  })
})
