import { createForegroundFocusBoundary, guardElementFocus } from './foregroundFocus'
import { reportServerEvent, flushServerEvents } from './useCliInputDiagnostics'

let boundary: ReturnType<typeof createForegroundFocusBoundary> | undefined
let boundaryWindow: Window | undefined

export function terminalFocusBoundary() {
  const currentWindow = typeof window === 'undefined' ? undefined : window
  if (boundary && boundaryWindow === currentWindow) return boundary
  boundaryWindow = currentWindow
  const page = Math.random().toString(36).slice(2, 10)
  boundary = createForegroundFocusBoundary({
    focused: () => typeof document !== 'undefined' && document.hasFocus(),
    visible: () => typeof document !== 'undefined' && document.visibilityState === 'visible',
    now: () => performance.now(),
    // Deliberately no input values, key text, URLs, paths or session credentials.
    report: (event, fields) => reportServerEvent(event, { page, ...fields }),
  })
  if (typeof window !== 'undefined' && typeof document !== 'undefined') {
    const current = boundary
    window.addEventListener('blur', () => { current.blur(); flushServerEvents() })
    window.addEventListener('focus', () => current.focus())
    document.addEventListener('visibilitychange', () => current.visibility())
    reportServerEvent('cli.focus.boundary', { page, platform: navigator.platform, userAgent: navigator.userAgent })
  }
  return boundary
}

export function guardTerminalElementFocus(element: HTMLElement | null | undefined, source: string): () => void {
  return element ? guardElementFocus(element, terminalFocusBoundary(), source) : () => {}
}

export function focusInForeground(element: HTMLElement | null | undefined, source: string, options?: FocusOptions): void {
  if (element) terminalFocusBoundary().run(source, () => element.focus(options))
}
