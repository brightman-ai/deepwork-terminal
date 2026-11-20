import type { createForegroundFocusBoundary } from './foregroundFocus'

type Boundary = ReturnType<typeof createForegroundFocusBoundary>
type PageWindow = Window & typeof globalThis
type ParkedBox = { deref(): HTMLElement | undefined }

export const FOCUS_RESTORE_SETTLE_MS = 80

const WeakRefCtor = (globalThis as unknown as {
  WeakRef?: new <T extends object>(value: T) => { deref(): T | undefined }
}).WeakRef

function boxParked(element: HTMLElement): ParkedBox {
  return WeakRefCtor ? new WeakRefCtor(element) : { deref: () => element }
}

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
 * Install before Vue mounts so every focus/navigation side-effect on the page obeys foreground ownership:
 *
 * 1. Programmatic & parked DOM focus:
 *    Blocks background `.focus()` / `.select()` / `window.focus()`, and parks `document.activeElement`
 *    to `document.body` on `window.blur` or `visibilitychange -> hidden`. Parked focus is restored only
 *    after `FOCUS_RESTORE_SETTLE_MS` of uninterrupted `focused && visible` ownership (or immediate typing).
 *
 * 2. Hidden macOS Space / Contexts transition quiescence (`history.replaceState`):
 *    When switching macOS Spaces via Contexts (`Option+Tab` held open >= ~0.5s), Contexts's panel first
 *    takes key focus (`window.blur` while `visible: true`), and confirming a target on another Space
 *    transitions this tab to `visibilityState === 'hidden'`. Within 1-19ms of `hidden`, `vue-router`
 *    (`createWebHistory`) calls `history.replaceState({ ...state, scroll }, '')` (empty URL) on this
 *    `overflow: hidden` SPA (`history.scrollRestoration = 'manual'`). In Chromium/Edge on macOS, that
 *    active-tab same-document navigation commit (`DidCommitSameDocumentNavigation`) during Contexts's
 *    panel teardown caused Edge's `NSWindow` to reclaim key focus (`window.focus` at +65-305ms while
 *    hidden, 6/6 reproductions), reversing Mission Control's Space transition back to the browser.
 *    Suppressing URL-less `replaceState` while hidden (paired with deferring hidden WS/HTTP flushes)
 *    eliminated 100% of hidden `window.focus` pulses and Space snap-backs (0/29 across 76 transitions),
 *    while still permitting explicit URL replacements.
 */
export function installPageFocusGuard(
  win: PageWindow,
  doc: Document,
  boundary: Boundary,
  isTrusted: (event: Event) => boolean = event => event.isTrusted,
) {
  const releases: (() => void)[] = []
  const nativeWindowFocus = win.focus
  const nativeElementFocus = win.HTMLElement?.prototype?.focus as ((options?: FocusOptions) => void) | undefined
  const nativeElementBlur = win.HTMLElement?.prototype?.blur as (() => void) | undefined
  const setTimer = typeof win.setTimeout === 'function' ? win.setTimeout.bind(win) : setTimeout
  const clearTimer = typeof win.clearTimeout === 'function' ? win.clearTimeout.bind(win) : clearTimeout
  let lastGesture = { kind: 'none', at: -Infinity }
  let parked: ParkedBox | null = null
  let restoreTimer: ReturnType<typeof setTimeout> | null = null

  function isParkableTarget(target: unknown): target is HTMLElement {
    if (!target || typeof target !== 'object' || !('tagName' in target)) return false
    if (target === doc.body || target === doc.documentElement) return false
    const tag = String((target as Element).tagName).toLowerCase()
    return tag !== 'body' && tag !== 'html'
  }

  function canRestoreTarget(target: HTMLElement | undefined): target is HTMLElement {
    if (!target) return false
    if ('isConnected' in target && target.isConnected === false) return false
    if ('disabled' in target && Boolean((target as HTMLButtonElement | HTMLInputElement | HTMLTextAreaElement).disabled)) return false
    if (typeof target.getClientRects === 'function' && target.getClientRects().length === 0) return false
    return true
  }

  function blurElement(target: HTMLElement): void {
    if (typeof nativeElementBlur === 'function') nativeElementBlur.call(target)
    else if (typeof target.blur === 'function') target.blur()
  }

  function cancelRestore(): void {
    if (restoreTimer !== null) {
      clearTimer(restoreTimer)
      restoreTimer = null
    }
  }

  function clearParked(): void {
    cancelRestore()
    parked = null
  }

  function parkActiveElement(reason: 'window-blur' | 'visibility-hidden'): void {
    cancelRestore()
    const active = doc.activeElement
    if (!isParkableTarget(active)) return
    if (!parked) parked = boxParked(active)
    const target = focusTargetKind(active)
    blurElement(active)
    boundary.observe('cli.focus.parked', { target, reason, activeAfter: focusTargetKind(doc.activeElement) })
  }

  function restoreParkedFocus(trigger: 'settle' | 'keydown', scheduledEpoch = boundary.token()): void {
    cancelRestore()
    if (!parked || !boundary.focused() || !boundary.visible()) return
    if (isParkableTarget(doc.activeElement)) {
      parked = null
      return
    }
    const target = parked.deref()
    parked = null
    if (!canRestoreTarget(target) || typeof nativeElementFocus !== 'function') return
    boundary.run('parked-focus-restore', () => {
      nativeElementFocus.call(target, { preventScroll: true })
    }, scheduledEpoch, () => ({ target: focusTargetKind(target), trigger }))
  }

  function scheduleRestore(): void {
    cancelRestore()
    if (!parked || !boundary.focused() || !boundary.visible()) return
    if (isParkableTarget(doc.activeElement)) {
      parked = null
      return
    }
    const scheduledEpoch = boundary.token()
    restoreTimer = setTimer(() => {
      restoreTimer = null
      restoreParkedFocus('settle', scheduledEpoch)
    }, FOCUS_RESTORE_SETTLE_MS)
  }

  const facts = (event: Event) => ({
    trusted: isTrusted(event),
    target: focusTargetKind(event.target),
    activeTarget: focusTargetKind(doc.activeElement),
    visState: typeof doc.visibilityState === 'string' ? doc.visibilityState : undefined,
    gesture: lastGesture.kind,
    sinceGestureMs: Number.isFinite(lastGesture.at) ? Math.round(win.performance.now() - lastGesture.at) : null,
  })
  const listen = (target: EventTarget, name: string, handler: EventListener) => {
    target.addEventListener(name, handler, true)
    releases.push(() => target.removeEventListener(name, handler, true))
  }
  releases.push(() => clearParked())

  listen(doc, 'pointerdown', event => {
    if (!isTrusted(event)) return
    lastGesture = { kind: 'pointer', at: win.performance.now() }
    clearParked()
  })
  listen(doc, 'keydown', event => {
    if (!isTrusted(event)) return
    const keyboard = event as KeyboardEvent
    const key = keyboard.key
    const keyKind = ['Meta', 'Alt', 'Control', 'Shift', 'Tab', 'Escape'].includes(key) ? key : 'key'
    lastGesture = { kind: keyKind, at: win.performance.now() }
    if (parked && boundary.focused() && boundary.visible()
      && !['Meta', 'Alt', 'Control', 'Shift'].includes(key)
      && !keyboard.metaKey && !keyboard.altKey) {
      restoreParkedFocus('keydown')
    }
  })
  listen(win, 'blur', event => {
    if (event.target !== win) return
    boundary.blur(facts(event))
    if (!boundary.focused()) parkActiveElement('window-blur')
  })
  listen(win, 'focus', event => {
    if (event.target !== win) return
    boundary.focus(facts(event))
    if (boundary.focused() && boundary.visible()) scheduleRestore()
    else cancelRestore()
  })
  listen(doc, 'visibilitychange', event => {
    boundary.visibility(facts(event))
    if (!boundary.visible()) parkActiveElement('visibility-hidden')
    else if (boundary.focused()) scheduleRestore()
  })
  listen(doc, 'focusin', event => {
    boundary.observe('cli.focus.dom', { ...facts(event), event: 'focusin' })
    if (!boundary.focused() || !boundary.visible()) {
      if (isParkableTarget(event.target)) {
        const reason = !boundary.visible() ? 'hidden' : 'window-unfocused'
        blurElement(event.target)
        boundary.observe('cli.focus.blocked', { source: 'dom-focusin', reason, target: focusTargetKind(event.target) })
      }
      return
    }
    if (isParkableTarget(event.target)) clearParked()
  })
  listen(doc, 'focusout', event => {
    boundary.observe('cli.focus.dom', { ...facts(event), event: 'focusout' })
  })

  function guard(owner: object | undefined, key: string, source: string) {
    if (!owner) return
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

  guard(win.HTMLElement?.prototype, 'focus', 'page-element-focus')
  guard(win.HTMLInputElement?.prototype, 'select', 'page-input-select')
  guard(win.HTMLTextAreaElement?.prototype, 'select', 'page-textarea-select')
  guard(win, 'focus', 'page-window-focus')

  if (win.history) {
    const repDesc = Object.getOwnPropertyDescriptor(win.history, 'replaceState')
    const origReplace = Reflect.get(win.history, 'replaceState') as ((...args: unknown[]) => unknown) | undefined
    if ((!repDesc || repDesc.configurable) && typeof origReplace === 'function') {
      const guardedReplace = function (this: History, ...args: unknown[]) {
        const urlArg = args[2]
        const isUrlLess = args.length < 3 || urlArg === undefined || urlArg === null || urlArg === ''
        if (!boundary.visible() && isUrlLess) {
          boundary.observe('cli.focus.blocked', {
            source: 'page-history-replaceState',
            reason: 'hidden',
            target: 'window',
            callers: focusCallerLocations(new Error().stack),
          })
          return undefined
        }
        return Reflect.apply(origReplace, this, args)
      }
      try {
        Object.defineProperty(win.history, 'replaceState', { configurable: true, writable: true, value: guardedReplace })
        releases.push(() => {
          if (Reflect.get(win.history, 'replaceState') !== guardedReplace) return
          if (repDesc) Object.defineProperty(win.history, 'replaceState', repDesc)
          else Reflect.deleteProperty(win.history, 'replaceState')
        })
      } catch { /* non-writable host property */ }
    }
  }

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
