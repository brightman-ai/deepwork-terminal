import type { createForegroundFocusBoundary } from './foregroundFocus'

type Boundary = ReturnType<typeof createForegroundFocusBoundary>
type PageWindow = Window & typeof globalThis

/** Only structural categories: never IDs, labels, values, text, paths or attributes supplied by users. */
export function focusTargetKind(target: EventTarget | null): string {
  if (!target || !('tagName' in target)) return 'window'
  const element = target as Element
  const tag = element.tagName.toLowerCase()
  if (element.classList.contains('xterm-helper-textarea')) return 'xterm-helper'
  return ['input', 'textarea', 'button', 'a', 'div', 'section', 'body', 'iframe'].includes(tag) ? tag : 'element'
}

/** Bundle coordinates let us locate the caller without logging a URL (which may hold credentials). */
export function focusCallerLocations(stack: string | undefined): string[] {
  return [...(stack ?? '').matchAll(/\/(?:assets|src(?:\/[^\s():]+)*)\/([a-zA-Z0-9_.-]+\.(?:js|ts|vue)):(\d+):(\d+)/g)]
    .slice(0, 6).map(match => `${match[1]}:${match[2]}:${match[3]}`)
}

/**
 * Install before Vue mounts, including third-party dialogs and temporary clipboard inputs.
 * Native clicks/Tab and OS activation do not call these JS methods and are left to the browser.
 * No retry/queued focus is retained when ownership is lost.
 */
export function installPageFocusGuard(win: PageWindow, doc: Document, boundary: Boundary) {
  const releases: (() => void)[] = []
  const nativeWindowFocus = win.focus
  let lastGesture = { kind: 'none', at: -Infinity }

  const facts = (event: Event) => ({
    trusted: event.isTrusted,
    target: focusTargetKind(event.target),
    activeTarget: focusTargetKind(doc.activeElement),
    gesture: lastGesture.kind,
    sinceGestureMs: Number.isFinite(lastGesture.at) ? Math.round(win.performance.now() - lastGesture.at) : null,
  })
  const listen = (target: EventTarget, name: string, handler: EventListener) => {
    target.addEventListener(name, handler, true)
    releases.push(() => target.removeEventListener(name, handler, true))
  }
  listen(doc, 'pointerdown', event => {
    if (event.isTrusted) lastGesture = { kind: 'pointer', at: win.performance.now() }
  })
  listen(doc, 'keydown', event => {
    if (!event.isTrusted) return
    const key = (event as KeyboardEvent).key
    lastGesture = { kind: ['Meta', 'Alt', 'Control', 'Shift', 'Tab', 'Escape'].includes(key) ? key : 'key', at: win.performance.now() }
  })
  listen(win, 'blur', event => {
    if (event.target === win) boundary.blur(facts(event))
  })
  listen(win, 'focus', event => {
    if (event.target === win) boundary.focus(facts(event))
  })
  listen(doc, 'visibilitychange', event => boundary.visibility(facts(event)))
  for (const name of ['focusin', 'focusout']) {
    listen(doc, name, event => boundary.observe('cli.focus.dom', { ...facts(event), event: name }))
  }

  function guard(owner: object, key: string, source: string) {
    const descriptor = Object.getOwnPropertyDescriptor(owner, key)
    const original = Reflect.get(owner, key) as (...args: unknown[]) => unknown
    if (typeof original !== 'function') return
    const guarded = function (this: EventTarget, ...args: unknown[]) {
      let result: unknown
      boundary.run(source, () => { result = Reflect.apply(original, this, args) }, undefined, () => ({
        target: focusTargetKind(this), callers: focusCallerLocations(new Error().stack),
      }))
      return result
    }
    Object.defineProperty(owner, key, { configurable: true, writable: true, value: guarded })
    releases.push(() => {
      if (Reflect.get(owner, key) !== guarded) return
      if (descriptor) Object.defineProperty(owner, key, descriptor)
      else Reflect.deleteProperty(owner, key)
    })
  }
  guard(win.HTMLElement.prototype, 'focus', 'page-element-focus')
  guard(win.HTMLInputElement.prototype, 'select', 'page-input-select')
  guard(win.HTMLTextAreaElement.prototype, 'select', 'page-textarea-select')
  guard(win, 'focus', 'page-window-focus')

  return {
    /** Clicking an OS notification explicitly requests activation, even while the page is hidden. */
    activateFromNotification(event: Event): boolean {
      if (!event.isTrusted || event.type !== 'click') return false
      boundary.observe('cli.focus.activation', { source: 'notification-click', trusted: true })
      nativeWindowFocus.call(win)
      return true
    },
    dispose() { for (const release of releases.reverse()) release() },
  }
}
