import { mock } from 'bun:test'
import { strict as assert } from 'node:assert'
import { effectScope } from 'vue'
import type { ClipboardEntry } from '../clipboardHistory'

Object.defineProperty(globalThis, 'localStorage', { value: { getItem: () => null, setItem: () => {} } })
Object.defineProperty(globalThis, 'document', { value: { visibilityState: 'visible' } })
const urls: string[] = []
let serverEpoch = 'epoch-a'
let serverCursor = 0
let serverEntries: ClipboardEntry[] = []
mock.module('@terminal/composables/cli/useCliAuth', () => ({
  useCliAuth: () => ({
    getAuthCode: () => 'fixture-account',
    cliFetch: async (url: string, init: RequestInit) => {
      if (init.signal?.aborted) throw new DOMException('Aborted', 'AbortError')
      urls.push(url)
      return Response.json({ epoch: serverEpoch, cursor: serverCursor, entries: serverEntries })
    },
  }),
}))
mock.module('../clipboardPersistence', () => ({
  readClipboardHistory: async () => undefined, saveClipboardHistory: async () => {},
}))
mock.module('../terminalClipboard', () => ({ copyTerminalTextOnClick: () => false }))

const { useRemoteClipboard } = await import('../useRemoteClipboard')
const scope = effectScope()
const client = scope.run(() => useRemoteClipboard({ sessionId: () => 'fixture', active: () => true }))!
client.settings.autoCopy = false
const release = client.claimControlPlane()
const settle = async () => { for (let i = 0; i < 60; i++) await Promise.resolve() }
const entry = (n: number): ClipboardEntry => ({
  id: 'item-' + n, sequence: n, sessionId: 'fixture', source: 'fixture',
  direction: 'remote', text: 'record-' + n, at: new Date().toISOString(),
})
await settle()
assert.equal(urls.length, 1, 'first WS owner needs one HTTP snapshot')
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 1, entries: [entry(1)] })
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 1, entries: [entry(1)] })
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 2, entries: [entry(2)] })
assert.deepEqual(client.entries.value.map(e => e.id), ['item-2', 'item-1'])

serverCursor = 4
serverEntries = [entry(3), entry(4)]
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 4 }) // metadata after another socket attaches
await settle()
assert.equal(urls.length, 2, 'metadata gap recovers once, without a second redundant snapshot')
assert.deepEqual(client.entries.value.map(e => e.id), ['item-4', 'item-3', 'item-2', 'item-1'])
serverCursor = 6
serverEntries = [entry(5), entry(6)]
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 6, entries: [entry(6)] })
await settle()
assert.equal(urls.length, 3, 'dropped event gap recovers once')
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 1, entries: [entry(1)] })
await client.poll(true)
assert.equal(new URL(urls.at(-1)!, 'http://fixture').searchParams.get('since'), '6', 'late duplicate must not rewind the cursor')
client.applyClipboardTargets({ revision: 50, host: 'old-app', targets: [] })
serverEpoch = 'epoch-b'
serverCursor = 0
serverEntries = []
client.applyClipboardDelta({ epoch: serverEpoch, cursor: 0 })
await settle()
client.applyClipboardTargets({ revision: 1, host: 'new-app', targets: [] })
assert.equal(client.host.value, 'new-app', 'app-only restart must accept the new target revision')
scope.stop()
release()
console.log('clipboard control oracle passed')
