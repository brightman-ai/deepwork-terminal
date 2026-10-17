import { describe, it, expect } from 'bun:test'
import { sliceForWrite, WRITE_SLICE_BYTES } from '../writeSlices'

describe('sliceForWrite', () => {
  it('leaves an ordinary frame as one write', () => {
    const frame = new Uint8Array(4096)
    const parts = sliceForWrite(frame)
    expect(parts).toHaveLength(1)
    expect(parts[0]).toBe(frame)
  })

  // The 520KB replay that held the main thread: it must reach xterm as pieces its WriteBuffer can
  // yield between, in order, with nothing lost or duplicated.
  it('splits a large binary frame into ordered pieces that add back up to the frame', () => {
    const frame = new Uint8Array(520_016)
    for (let i = 0; i < frame.length; i++) frame[i] = i % 251
    const parts = sliceForWrite(frame) as Uint8Array[]
    expect(parts.length).toBe(Math.ceil(frame.length / WRITE_SLICE_BYTES))
    expect(parts.every((p) => p.byteLength <= WRITE_SLICE_BYTES)).toBe(true)
    const joined = new Uint8Array(parts.reduce((n, p) => n + p.byteLength, 0))
    let at = 0
    for (const p of parts) { joined.set(p, at); at += p.byteLength }
    expect(joined).toEqual(frame)
  })

  // Views, not copies: slicing a replay must not double its memory.
  it('binary pieces are views onto the frame, not copies', () => {
    const frame = new Uint8Array(3 * WRITE_SLICE_BYTES)
    const parts = sliceForWrite(frame) as Uint8Array[]
    expect(parts.every((p) => p.buffer === frame.buffer)).toBe(true)
  })

  it('splits strings the same way', () => {
    const s = 'ab'.repeat(WRITE_SLICE_BYTES)
    const parts = sliceForWrite(s) as string[]
    expect(parts).toHaveLength(2)
    expect(parts.join('')).toBe(s)
  })
})
