import { describe, expect, test } from 'bun:test'
import { terminalTextFromKeyboardEvent, useXtermKeyboardFallback } from '../../../composables/cli/useXtermKeyboardFallback'

function keyEvent(input: Partial<Parameters<typeof terminalTextFromKeyboardEvent>[0]> & { key: string }) {
  return {
    key: input.key,
    code: input.code ?? '',
    isComposing: input.isComposing ?? false,
    metaKey: input.metaKey ?? false,
    altKey: input.altKey ?? false,
    ctrlKey: input.ctrlKey ?? false,
  }
}

function installDomShims() {
  const originalWindow = (globalThis as any).window
  const originalHTMLElement = (globalThis as any).HTMLElement

  class FakeHTMLElement {
    tagName = 'TEXTAREA'
    className = 'xterm-helper-textarea'
    classList = {
      contains: (name: string) => name === 'xterm-helper-textarea',
    }
  }

  ;(globalThis as any).HTMLElement = FakeHTMLElement
  ;(globalThis as any).window = {
    setTimeout: globalThis.setTimeout,
    clearTimeout: globalThis.clearTimeout,
    location: { search: '' },
    localStorage: {
      getItem: () => null,
      setItem: () => {},
      removeItem: () => {},
    },
  }

  return {
    FakeHTMLElement,
    restore: () => {
      ;(globalThis as any).window = originalWindow
      ;(globalThis as any).HTMLElement = originalHTMLElement
    },
  }
}

describe('terminalTextFromKeyboardEvent', () => {
  test('passes committed printable text including CJK strings', () => {
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'a', code: 'KeyA' }))).toBe('a')
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: '百度', code: 'Unidentified' }))).toBe('百度')
  })

  test('maps terminal control keys', () => {
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'Enter' }))).toBe('\r')
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'Backspace' }))).toBe('\x7f')
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'Tab' }))).toBe('\t')
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'Escape' }))).toBe('\x1b')
  })

  test('ignores composing and modifier chords', () => {
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'x', isComposing: true }))).toBeNull()
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'c', metaKey: true }))).toBeNull()
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'c', ctrlKey: true }))).toBeNull()
  })

  test('ignores non-terminal named keys', () => {
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'Shift' }))).toBeNull()
    expect(terminalTextFromKeyboardEvent(keyEvent({ key: 'Unidentified' }))).toBeNull()
  })
})

describe('useXtermKeyboardFallback', () => {
  test('does not fallback when xterm already emitted the same key data first', async () => {
    const { FakeHTMLElement, restore } = installDomShims()
    const sent: string[] = []
    const encoder = new TextEncoder()
    const decoder = new TextDecoder()

    try {
      const fallback = useXtermKeyboardFallback({
        surface: 'test',
        enabled: () => true,
        send: data => sent.push(decoder.decode(data)),
        delayMs: 1,
        recentTerminalDataMs: 1000,
      })
      fallback.notifyTerminalData(encoder.encode('s'))

      const handled = fallback.handleKeydown({
        currentTarget: new FakeHTMLElement(),
        target: new FakeHTMLElement(),
        key: 's',
        code: 'KeyS',
        isComposing: false,
        metaKey: false,
        altKey: false,
        ctrlKey: false,
      } as unknown as KeyboardEvent)

      expect(handled).toBe(false)
      await new Promise(resolve => setTimeout(resolve, 5))
      expect(sent).toEqual([])
    } finally {
      restore()
    }
  })

  test('falls back for committed IME text when xterm emits no data', async () => {
    const { FakeHTMLElement, restore } = installDomShims()
    const sent: string[] = []
    const decoder = new TextDecoder()

    try {
      const fallback = useXtermKeyboardFallback({
        surface: 'test',
        enabled: () => true,
        send: data => sent.push(decoder.decode(data)),
        delayMs: 1,
      })

      const handled = fallback.handleKeydown({
        currentTarget: new FakeHTMLElement(),
        target: new FakeHTMLElement(),
        key: '百度',
        code: 'Unidentified',
        isComposing: false,
        metaKey: false,
        altKey: false,
        ctrlKey: false,
      } as unknown as KeyboardEvent)

      expect(handled).toBe(true)
      await new Promise(resolve => setTimeout(resolve, 5))
      expect(sent).toEqual(['百度'])
    } finally {
      restore()
    }
  })
})
