// Separate runtime for auth/transport stubs; the actual overview module is the object under test.
import { mock } from 'bun:test'
import { strict as assert } from 'node:assert'

let auth = 'account-a'
const requests: Array<{ resolve: (r: Response) => void; signal: AbortSignal }> = []
mock.module('@terminal/composables/cli/useCliAuth', () => ({
  useCliAuth: () => ({
    getAuthCode: () => auth,
    cliFetch: (_url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ resolve, signal: init.signal! })),
  }),
}))
const { sessionOverview } = await import('../overview')
const settle = async () => { for (let i = 0; i < 12; i++) await Promise.resolve() }
const answer = (index: number, id: string) => requests[index].resolve(Response.json({ detail: { model_id: id } }))

const owner = new AbortController()
const first = sessionOverview('shared', '', 'codex', owner.signal)
const second = sessionOverview('shared', '', 'codex')
owner.abort()
assert.equal((await first).detail, null)
assert.equal(requests[0].signal.aborted, false, 'one departing subscriber must not cancel another')
answer(0, 'shared-result')
assert.equal((await second).detail?.model_id, 'shared-result')
assert.equal(requests.length, 1)

const oldOwner = new AbortController()
const old = sessionOverview('race', '', '', oldOwner.signal)
oldOwner.abort()
assert.equal((await old).detail, null)
assert.equal(requests[1].signal.aborted, true)
const replacement = sessionOverview('race')
answer(1, 'stale-result')
await settle()
const follower = sessionOverview('race')
assert.equal(requests.length, 3, 'late old cleanup must not remove the replacement request')
answer(2, 'new-result')
assert.equal((await replacement).detail?.model_id, 'new-result')
assert.equal((await follower).detail?.model_id, 'new-result')

const failed = sessionOverview('retry')
requests[3].resolve(new Response('', { status: 503 }))
assert.equal((await failed).detail, null)
const retried = sessionOverview('retry')
assert.equal(requests.length, 5, 'failed responses must not be cached as successful emptiness')
answer(4, 'recovered')
assert.equal((await retried).detail?.model_id, 'recovered')

auth = 'account-b'
const rotated = sessionOverview('shared', '', 'codex')
assert.equal(requests.length, 6, 'new authentication must not receive a previous account snapshot')
answer(5, 'account-b-result')
assert.equal((await rotated).detail?.model_id, 'account-b-result')
console.log('overview lifecycle oracle passed')
