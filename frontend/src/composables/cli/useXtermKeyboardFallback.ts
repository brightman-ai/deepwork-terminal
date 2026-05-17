import { reportCliInputDiagnostic, summarizeBytes, summarizeText } from '@/composables/cli/useCliInputDiagnostics'

type Timer = number

export interface XtermKeyboardFallbackOptions {
  surface: string
  enabled: () => boolean
  send: (data: Uint8Array) => void
  delayMs?: number
  compositionGuardMs?: number
  recentTerminalDataMs?: number
}

interface KeyEventLike {
  key: string
  code: string
  isComposing: boolean
  metaKey: boolean
  altKey: boolean
  ctrlKey: boolean
}

interface PendingFallback {
  timer: Timer
  bytes: Uint8Array
  key: string
  code: string
  sequence: number
}

const encoder = new TextEncoder()
const decoder = new TextDecoder()
const DEFAULT_DELAY_MS = 120
const DEFAULT_COMPOSITION_GUARD_MS = 600
const DEFAULT_RECENT_TERMINAL_DATA_MS = 50
const ignoredNamedKeys = new Set([
  'Shift',
  'Control',
  'Alt',
  'Meta',
  'CapsLock',
  'Fn',
  'FnLock',
  'Symbol',
  'SymbolLock',
  'Dead',
  'Process',
  'Unidentified',
])

export function terminalTextFromKeyboardEvent(event: KeyEventLike): string | null {
  if (event.isComposing || event.metaKey || event.altKey || event.ctrlKey) return null
  if (!event.key || ignoredNamedKeys.has(event.key)) return null

  switch (event.key) {
    case 'Enter':
      return '\r'
    case 'Backspace':
      return '\x7f'
    case 'Tab':
      return '\t'
    case 'Escape':
      return '\x1b'
    default:
      return event.key.length >= 1 ? event.key : null
  }
}

function eventTargetIsXtermHelper(event: KeyboardEvent): boolean {
  const target = event.currentTarget || event.target || document.activeElement
  return target instanceof HTMLElement && target.classList.contains('xterm-helper-textarea')
}

export function useXtermKeyboardFallback(options: XtermKeyboardFallbackOptions) {
  let pending: PendingFallback[] = []
  let sequence = 0
  let compositionGuardUntil = 0
  let lastTerminalDataAt = 0
  let lastTerminalDataText = ''

  function guardComposition(reason: string): void {
    compositionGuardUntil = Math.max(
      compositionGuardUntil,
      performance.now() + (options.compositionGuardMs ?? DEFAULT_COMPOSITION_GUARD_MS),
    )
    clearPending(reason)
    reportCliInputDiagnostic('xterm-keydown-fallback.composition-guard', {
      surface: options.surface,
      reason,
      untilMs: Math.round(compositionGuardUntil),
    })
  }

  function clearPending(reason: string, data?: Uint8Array): void {
    if (pending.length === 0) return
    for (const item of pending) window.clearTimeout(item.timer)
    reportCliInputDiagnostic('xterm-keydown-fallback.cancel', {
      surface: options.surface,
      reason,
      count: pending.length,
      key: summarizeText(pending[pending.length - 1]?.key),
      code: pending[pending.length - 1]?.code,
      data: data ? summarizeBytes(data) : undefined,
    })
    pending = []
  }

  function handleKeydown(event: KeyboardEvent): boolean {
    if (!options.enabled()) return false
    if (!eventTargetIsXtermHelper(event)) return false

    if (event.isComposing) {
      guardComposition('composing-keydown')
      return false
    }

    if (performance.now() < compositionGuardUntil) {
      reportCliInputDiagnostic('xterm-keydown-fallback.suppress', {
        surface: options.surface,
        reason: 'composition-guard',
        key: summarizeText(event.key),
        code: event.code,
      })
      return false
    }

    const text = terminalTextFromKeyboardEvent(event)
    if (!text) return false
    if (
      text === lastTerminalDataText
      && performance.now() - lastTerminalDataAt < (options.recentTerminalDataMs ?? DEFAULT_RECENT_TERMINAL_DATA_MS)
    ) {
      reportCliInputDiagnostic('xterm-keydown-fallback.suppress', {
        surface: options.surface,
        reason: 'already-handled-by-xterm',
        key: summarizeText(event.key),
        code: event.code,
      })
      return false
    }

    const bytes = encoder.encode(text)
    const current = ++sequence
    const item: PendingFallback = {
      timer: window.setTimeout(() => {
        const index = pending.findIndex(candidate => candidate.sequence === current)
        if (index < 0) return
        const [fallback] = pending.splice(index, 1)
        if (!fallback) return
        reportCliInputDiagnostic('xterm-keydown-fallback.emit', {
          surface: options.surface,
          key: summarizeText(fallback.key),
          code: fallback.code,
          data: summarizeBytes(fallback.bytes),
        })
        options.send(fallback.bytes)
      }, options.delayMs ?? DEFAULT_DELAY_MS),
      bytes,
      key: event.key,
      code: event.code,
      sequence: current,
    }
    pending.push(item)
    if (pending.length > 32) {
      const overflow = pending.splice(0, pending.length - 32)
      for (const item of overflow) window.clearTimeout(item.timer)
    }
    reportCliInputDiagnostic('xterm-keydown-fallback.schedule', {
      surface: options.surface,
      key: summarizeText(event.key),
      code: event.code,
      data: summarizeBytes(bytes),
    })
    return true
  }

  function notifyTerminalData(data: Uint8Array): void {
    try {
      lastTerminalDataText = decoder.decode(data)
      lastTerminalDataAt = performance.now()
    } catch {
      lastTerminalDataText = ''
      lastTerminalDataAt = performance.now()
    }
    clearPending('xterm-data', data)
  }

  function notifyComposition(reason: string): void {
    if (!options.enabled()) return
    guardComposition(reason)
  }

  function dispose(): void {
    clearPending('dispose')
  }

  return { handleKeydown, notifyTerminalData, notifyComposition, dispose }
}
