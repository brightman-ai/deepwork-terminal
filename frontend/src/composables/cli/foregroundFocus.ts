export interface FocusEnvironment {
  focused(): boolean
  visible(): boolean
  now(): number
  report(event: string, fields: Record<string, unknown>): void
}

/** Window ownership, rather than the selected terminal tab, decides who may focus. */
export function createForegroundFocusBoundary(env: FocusEnvironment) {
  let epoch = 0
  let blurredAt: number | undefined
  let windowStart = -Infinity
  let reported = 0
  let suppressed = 0

  function report(event: string, fields: Record<string, unknown>, details?: () => Record<string, unknown>) {
    const now = env.now()
    if (now - windowStart >= 10_000) { windowStart = now; reported = 0 }
    if (event !== 'cli.focus.window' && event !== 'cli.focus.activation' && reported++ >= 80) { suppressed++; return }
    env.report(event, { ...fields, ...details?.(), perfMs: Math.round(now * 10) / 10, epoch, focused: env.focused(), visible: env.visible(),
      ...(blurredAt === undefined ? {} : { sinceBlurMs: Math.round(now - blurredAt) }), suppressed })
    suppressed = 0
  }

  function permit(source: string, requestedEpoch = epoch, details?: () => Record<string, unknown>): boolean {
    const reason = requestedEpoch !== epoch ? 'stale-request'
      : !env.visible() ? 'hidden' : !env.focused() ? 'window-unfocused' : ''
    if (!reason) return true
    report('cli.focus.blocked', { source, reason }, details)
    return false
  }

  return {
    token: () => epoch,
    focused: () => env.focused(),
    visible: () => env.visible(),
    blur(fields: Record<string, unknown> = {}) {
      epoch++; blurredAt = env.now()
      // Foreground typing must not consume the evidence budget for the first background call.
      windowStart = env.now(); reported = 0
      report('cli.focus.window', { ...fields, event: 'blur' })
    },
    focus(fields: Record<string, unknown> = {}) { report('cli.focus.window', { ...fields, event: 'focus' }) },
    visibility(fields: Record<string, unknown> = {}) { if (!env.visible()) epoch++; report('cli.focus.window', { ...fields, event: 'visibility' }) },
    observe(event: string, fields: Record<string, unknown>) { report(event, fields) },
    run(source: string, action: () => void, requestedEpoch = epoch, details?: () => Record<string, unknown>): boolean {
      if (!permit(source, requestedEpoch, details)) return false
      report('cli.focus.request', { source }, details)
      action()
      return true
    },
    keyEvent(event: Pick<KeyboardEvent, 'type' | 'key' | 'metaKey' | 'altKey'>): boolean {
      // xterm refocuses on keyup, including Meta and Alt (e.g. macOS Contexts Option+Tab).
      // A system app-switch release is not a request to reclaim input. Never preventDefault here.
      if (event.type === 'keyup' && (event.key === 'Meta' || event.key === 'Alt' || event.key === 'Tab' && (event.metaKey || event.altKey))) {
        report('cli.focus.blocked', { source: 'xterm-keyup', reason: 'system-key-release' })
        return false
      }
      return permit('xterm-key-event')
    },
  }
}

/** Only this owned element is guarded; native user clicks and other apps are untouched. */
export function guardElementFocus(
  element: HTMLElement,
  boundary: ReturnType<typeof createForegroundFocusBoundary>,
  source: string,
): () => void {
  const own = Object.getOwnPropertyDescriptor(element, 'focus')
  const nativeFocus = element.focus
  const guarded = function (this: HTMLElement, options?: FocusOptions): void {
    boundary.run(source, () => nativeFocus.call(this, options))
  }
  Object.defineProperty(element, 'focus', { configurable: true, writable: true, value: guarded })
  return () => {
    if (element.focus !== guarded) return
    if (own) Object.defineProperty(element, 'focus', own)
    else delete (element as unknown as { focus?: unknown }).focus
  }
}
