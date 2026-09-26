import { describe, expect, test } from 'bun:test'
import { effectScope } from 'vue'
import { useWebSocketClient } from '../useWebSocketClient'

// Only the browser transport and clock are replaced. Exercise the real client,
// including delayed callbacks from sockets that have already been replaced.
class Socket {
  static OPEN = 1
  static CONNECTING = 0
  static all: Socket[] = []
  readyState = 0
  binaryType = ''
  onopen: (() => void) | null = null
  onclose: ((event: { code: number; reason: string }) => void) | null = null
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  onerror: (() => void) | null = null
  sent: unknown[] = []
  constructor(public url: string) { Socket.all.push(this) }
  open() { this.readyState = 1; this.onopen?.() }
  message(data: string) { this.onmessage?.({ data }) }
  fail(code = 1006, reason = '') { this.readyState = 3; this.onclose?.({ code, reason }) }
  close() { this.readyState = 2 }
  send(data: unknown) { this.sent.push(data) }
}
function fixture(run: (h: {
  client: ReturnType<typeof useWebSocketClient>
  next: () => number | undefined
  advance: (ms: number) => void
  document: EventTarget
  dispose: () => void
}) => void) {
  const saved = { WebSocket: globalThis.WebSocket, setTimeout, clearTimeout, setInterval, clearInterval, now: Date.now,
    document: Object.getOwnPropertyDescriptor(globalThis, 'document') }
  const tasks = new Map<number, { fn: () => void; due: number }>()
  let now = 100, seq = 0
  const doc = Object.assign(new EventTarget(), { hidden: false })
  Object.defineProperty(globalThis, 'document', { configurable: true, value: doc })
  globalThis.WebSocket = Socket as unknown as typeof WebSocket
  globalThis.setTimeout = ((fn: () => void, ms: number) => { tasks.set(++seq, { fn, due: now + ms }); return seq }) as unknown as typeof setTimeout
  globalThis.clearTimeout = ((id: number) => { tasks.delete(id) }) as unknown as typeof clearTimeout
  globalThis.setInterval = (() => ++seq) as unknown as typeof setInterval
  globalThis.clearInterval = (() => {}) as typeof clearInterval
  Date.now = () => now
  Socket.all = []
  const scope = effectScope()
  try {
    const client = scope.run(() => useWebSocketClient(() => 'session', { wsBase: 'ws://fixture', maxReconnectAttempts: 3 }))!
    client.connect()
    run({ client, document: doc, dispose: () => scope.stop(), next: () => tasks.size ? Math.min(...[...tasks.values()].map(t => t.due - now)) : undefined,
      advance: ms => { now += ms; for (const [id, t] of [...tasks]) if (t.due <= now) { tasks.delete(id); t.fn() } } })
  } finally {
    scope.stop()
    globalThis.WebSocket = saved.WebSocket
    globalThis.setTimeout = saved.setTimeout; globalThis.clearTimeout = saved.clearTimeout
    globalThis.setInterval = saved.setInterval; globalThis.clearInterval = saved.clearInterval
    Date.now = saved.now
    if (saved.document) Object.defineProperty(globalThis, 'document', saved.document)
    else Reflect.deleteProperty(globalThis, 'document')
  }
}

describe('terminal connection recovery', () => {
  test('open-then-immediate-fail backs off and stops instead of replaying forever each second', () => fixture(({ next, advance }) => {
    for (const delay of [1000, 2000, 4000]) {
      Socket.all.at(-1)!.open(); Socket.all.at(-1)!.fail()
      expect(next()).toBe(delay); advance(delay)
    }
    Socket.all.at(-1)!.open(); Socket.all.at(-1)!.fail()
    expect(next()).toBeUndefined(); expect(Socket.all).toHaveLength(4)
  }))
  test('late close and output from the retired socket cannot corrupt a manual reconnection', () => fixture(({ client, next }) => {
    const old = Socket.all[0]; old.open()
    const lateClose = old.onclose!, lateMessage = old.onmessage!, lateOpen = old.onopen!
    let delivered = 0; client.onMessage(() => {}, () => { delivered++ })
    client.reconnect(); const current = Socket.all[1]; current.open()
    lateClose({ code: 1006, reason: '' }); lateMessage({ data: '{"type":"preempted"}' }); lateOpen()
    expect(client.status.value).toBe('connected'); expect(delivered).toBe(0); expect(next()).toBeUndefined()
    client.sendBinary(new Uint8Array([65])); expect(current.sent.at(-1)).toEqual(new Uint8Array([65]))
    current.fail(); expect(client.status.value).toBe('disconnected'); expect(next()).toBe(1000)
  }))
  test('manual reconnect cancels an old retry and a stable working connection resets the budget', () => fixture(({ client, next, advance }) => {
    Socket.all[0].open(); Socket.all[0].fail(); expect(next()).toBe(1000)
    client.reconnect(); expect(next()).toBeUndefined()
    Socket.all[1].open(); Socket.all[1].fail(); advance(1000)
    Socket.all[2].open(); Socket.all[2].fail(); expect(next()).toBe(2000); advance(2000)
    Socket.all[3].open(); advance(10001); Socket.all[3].message('{"type":"heartbeat_ack"}')
    Socket.all[3].fail(); expect(next()).toBe(1000)
  }))
  test('takeover does not fight the other device even when only the close reason arrives', () => fixture(({ client, next, document }) => {
    Socket.all[0].open(); Socket.all[0].fail(1008, 'preempted by new connection')
    expect(client.status.value).toBe('preempted'); expect(next()).toBeUndefined()
    document.dispatchEvent(new Event('visibilitychange')); expect(Socket.all).toHaveLength(1)
    client.reconnect(); expect(Socket.all).toHaveLength(2)
  }))
  test('returning to foreground wakes an exhausted connection without duplicate retries', () => fixture(({ client, next, advance, document }) => {
    for (const delay of [1000, 2000, 4000]) { Socket.all.at(-1)!.fail(); advance(delay) }
    Socket.all.at(-1)!.fail(); expect(next()).toBeUndefined()
    document.dispatchEvent(new Event('visibilitychange'))
    expect(Socket.all).toHaveLength(5); expect(client.status.value).toBe('connecting')
    document.dispatchEvent(new Event('visibilitychange')); expect(Socket.all).toHaveLength(5)
    Socket.all[4].open(); expect(client.status.value).toBe('connected'); expect(next()).toBeUndefined()
  }))
  test('disposed client cannot reconnect on a late close or return to foreground', () => fixture(({ client, next, document, dispose }) => {
    const old = Socket.all[0]; old.open(); const lateClose = old.onclose!
    dispose(); lateClose({ code: 1006, reason: '' }); document.dispatchEvent(new Event('visibilitychange')); client.connect()
    expect(next()).toBeUndefined(); expect(Socket.all).toHaveLength(1)
  }))
})
