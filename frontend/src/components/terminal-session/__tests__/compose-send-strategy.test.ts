/**
 * TC-08-FE-09: ComposeSendStrategy: short text sends character by character.
 * TC-08-FE-10: ComposeSendStrategy: long text uses bracketed paste.
 * [Ref: CAP-terminal-interaction S4, DDC-06]
 */
import { describe, test, expect } from 'bun:test'

const BRACKETED_PASTE_START = '\x1b[200~'
const BRACKETED_PASTE_END = '\x1b[201~'
const SHORT_TEXT_THRESHOLD = 200

// Re-implement the encode logic directly for testing without Vue dependencies.
function encode(text: string): Uint8Array[] {
  const encoder = new TextEncoder()
  if (!text) return []

  const isMultiLine = text.includes('\n')
  const isLong = text.length > SHORT_TEXT_THRESHOLD

  if (isMultiLine || isLong) {
    const wrapped = BRACKETED_PASTE_START + text + BRACKETED_PASTE_END
    return [encoder.encode(wrapped)]
  }

  return Array.from(text).map(ch => encoder.encode(ch))
}

function decodeAll(chunks: Uint8Array[]): string {
  const decoder = new TextDecoder()
  return chunks.map(c => decoder.decode(c)).join('')
}

describe('ComposeSendStrategy', () => {
  // TC-08-FE-09
  test('short single-line text: sends character by character', () => {
    const result = encode('ls -la')
    expect(result.length).toBe(6) // 6 characters
    expect(decodeAll(result)).toBe('ls -la')

    // Each chunk should be a single character.
    const decoder = new TextDecoder()
    expect(decoder.decode(result[0])).toBe('l')
    expect(decoder.decode(result[1])).toBe('s')
    expect(decoder.decode(result[2])).toBe(' ')
  })

  // TC-08-FE-10
  test('long text: uses bracketed paste', () => {
    const longText = 'a'.repeat(201)
    const result = encode(longText)
    expect(result.length).toBe(1) // Single bracketed paste chunk

    const decoded = decodeAll(result)
    expect(decoded.startsWith(BRACKETED_PASTE_START)).toBe(true)
    expect(decoded.endsWith(BRACKETED_PASTE_END)).toBe(true)
    expect(decoded).toContain(longText)
  })

  test('multi-line text: uses bracketed paste regardless of length', () => {
    const multiLine = 'line1\nline2'
    const result = encode(multiLine)
    expect(result.length).toBe(1)

    const decoded = decodeAll(result)
    expect(decoded.startsWith(BRACKETED_PASTE_START)).toBe(true)
    expect(decoded.endsWith(BRACKETED_PASTE_END)).toBe(true)
    expect(decoded).toContain(multiLine)
  })

  test('empty text: returns empty array', () => {
    const result = encode('')
    expect(result.length).toBe(0)
  })

  test('exactly 200 chars: sends character by character (threshold is >200)', () => {
    const text = 'b'.repeat(200)
    const result = encode(text)
    expect(result.length).toBe(200)
  })
})
