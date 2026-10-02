import { describe, expect, test } from 'bun:test'
import { createTerminalConnectionGate } from '../terminalConnectionGate'

describe('lazy terminal startup', () => {
  test('the initial full replay is delivered only after the receiver exists', () => {
    let receiver: ((s: string) => void) | undefined
    const frames: string[] = []
    let connections = 0
    const gate = createTerminalConnectionGate(() => {
      connections++
      receiver?.('full-screen: title\nbody\nstatus')
    })
    gate.request() // tab activates; child is still in its 50ms initialization gap
    gate.request() // a concurrent mount/activation must not start a second socket
    expect(connections).toBe(0)
    receiver = s => frames.push(s)
    gate.receiverAttached()
    expect(connections).toBe(1)
    expect(frames).toEqual(['full-screen: title\nbody\nstatus'])
    receiver('timer: 7')
    expect(frames[0]).toContain('body')
  })
  test('mounting a hidden terminal alone does not attach a session', () => {
    let connections = 0
    const gate = createTerminalConnectionGate(() => connections++)
    gate.receiverAttached()
    expect(connections).toBe(0)
    gate.request()
    expect(connections).toBe(1)
  })
  test('a destroyed surface cannot attach when its delayed initialization finishes', () => {
    let connections = 0
    const gate = createTerminalConnectionGate(() => connections++)
    gate.request()
    gate.dispose()
    gate.receiverAttached()
    gate.request()
    expect(connections).toBe(0)
  })
})
