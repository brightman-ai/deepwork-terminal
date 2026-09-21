import { describe, expect, it } from 'bun:test'
import { decodeTerminalClipboard, MAX_TERMINAL_CLIPBOARD_BYTES } from '../terminalClipboard'

describe('OSC 52 terminal clipboard writes', () => {
  it('preserves UTF-8, whitespace, emoji and trailing newlines exactly', () => {
    const text = '中文🙂\tcopy\n\nsecond line\n'
    expect(decodeTerminalClipboard(`c;${Buffer.from(text).toString('base64')}`)).toEqual({ text })
  })
  it('supports default and primary selections, including empty writes', () => {
    expect(decodeTerminalClipboard(';aGk=')).toEqual({ text: 'hi' })
    expect(decodeTerminalClipboard('pc;aGk=')).toEqual({ text: 'hi' })
    expect(decodeTerminalClipboard('c;')).toEqual({ text: '' })
  })
  it('never services clipboard read requests or invalid targets', () => {
    expect(decodeTerminalClipboard('c;?')).toBeNull()
    expect(decodeTerminalClipboard(';?')).toBeNull()
    expect(decodeTerminalClipboard('c')).toBeNull()
    expect(decodeTerminalClipboard('unknown;aGk=')).toBeNull()
  })
  it('reports corrupt or non-text data and bounds allocation', () => {
    expect(decodeTerminalClipboard('c;%%%%')).toHaveProperty('error')
    expect(decodeTerminalClipboard('c;/w==')).toHaveProperty('error')
    expect(decodeTerminalClipboard(`c;${'a'.repeat(Math.ceil(MAX_TERMINAL_CLIPBOARD_BYTES / 3) * 4 + 1)}`)).toHaveProperty('error')
  })
})
