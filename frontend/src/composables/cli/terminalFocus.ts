import { createForegroundFocusBoundary, guardElementFocus } from './foregroundFocus'
import { reportServerEvent, flushServerEvents } from './useCliInputDiagnostics'
import { installPageFocusGuard } from './pageFocus'

let boundary: ReturnType<typeof createForegroundFocusBoundary> | undefined
let boundaryWindow: Window | undefined
let pageGuard: ReturnType<typeof installPageFocusGuard> | undefined

export function terminalFocusBoundary() {
  const currentWindow = typeof window === 'undefined' ? undefined : window
  if (boundary && boundaryWindow === currentWindow) return boundary
  pageGuard?.dispose()
  pageGuard = undefined
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
    pageGuard = installPageFocusGuard(window, document, current)
    let blurFlushTimer: ReturnType<typeof setTimeout> | null = null
    window.addEventListener('blur', () => {
      if (blurFlushTimer !== null) clearTimeout(blurFlushTimer)
      blurFlushTimer = setTimeout(() => { blurFlushTimer = null; flushServerEvents() }, 1200)
    })
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') {
        if (blurFlushTimer !== null) { clearTimeout(blurFlushTimer); blurFlushTimer = null }
        setTimeout(() => { flushServerEvents() }, 350)
      }
    })
    reportServerEvent('cli.focus.boundary', { page, coverage: 'page-v5', platform: navigator.platform, userAgent: navigator.userAgent })
  }
  return boundary
}

/** Early installation also covers autofocus performed by third-party components at mount. */
export function installTerminalFocusGuard(): void { terminalFocusBoundary() }

export function focusFromNotification(event: Event): void {
  terminalFocusBoundary()
  pageGuard?.activateFromNotification(event)
}

export function guardTerminalElementFocus(element: HTMLElement | null | undefined, source: string): () => void {
  return element ? guardElementFocus(element, terminalFocusBoundary(), source) : () => {}
}

export function focusInForeground(element: HTMLElement | null | undefined, source: string, options?: FocusOptions): void {
  if (element) terminalFocusBoundary().run(source, () => element.focus(options))
}
