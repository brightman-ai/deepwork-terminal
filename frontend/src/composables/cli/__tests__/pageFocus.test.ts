import { describe, expect, it } from 'bun:test'
import { createForegroundFocusBoundary } from '../foregroundFocus'
import { FOCUS_RESTORE_SETTLE_MS, focusCallerLocations, installPageFocusGuard } from '../pageFocus'

// Native calls are an oracle here: a background temporary input must never reach them.
// This does not emulate macOS activation; the real browser check is separate.
function harness() {
  const calls: unknown[][] = []
  const state = { focused: true, visible: true }
  const logs: { event: string; fields: Record<string, unknown> }[] = []
  const timers = new Map<number, { at: number; run: () => void }>()
  let clock = 0
  let nextTimerId = 1

  const body = { tagName: 'BODY', classList: { contains: () => false } }
  const docState: { activeElement: unknown; body: unknown; documentElement: unknown } = {
    activeElement: body,
    body,
    documentElement: { tagName: 'HTML', classList: { contains: () => false } },
  }

  class Element {
    tagName = 'DIV'
    isConnected = true
    disabled = false
    private helper = false
    classList = { contains: (cls: string) => cls === 'xterm-helper-textarea' && this.helper }
    rects = 1
    markXtermHelper() { this.tagName = 'TEXTAREA'; this.helper = true; return this }
    getClientRects() { return Array.from({ length: this.rects }, () => ({})) }
    focus(...args: unknown[]) {
      calls.push(['focus', this, ...args])
      docState.activeElement = this
    }
    blur() {
      calls.push(['blur', this])
      if (docState.activeElement === this) docState.activeElement = body
    }
    scrollIntoView(...args: unknown[]) {
      calls.push(['scrollIntoView', this, ...args])
    }
  }
  class Input extends Element { tagName = 'INPUT'; select() { calls.push(['input-select', this]) } }
  class Textarea extends Element { tagName = 'TEXTAREA'; select() { calls.push(['textarea-select', this]) } }
  class Button extends Element { tagName = 'BUTTON' }

  const win = Object.assign(new EventTarget(), {
    HTMLElement: Element, HTMLInputElement: Input, HTMLTextAreaElement: Textarea,
    performance: { now: () => clock },
    setTimeout(fn: () => void, ms = 0) {
      const id = nextTimerId++
      timers.set(id, { at: clock + ms, run: fn })
      return id
    },
    clearTimeout(id: number) { timers.delete(id) },
    history: {
      replaceState(...args: unknown[]) { calls.push(['replaceState', ...args]) },
      pushState(...args: unknown[]) { calls.push(['pushState', ...args]) },
    },
    focus() { calls.push(['window-focus']) },
  })
  const doc = Object.defineProperties(new EventTarget(), {
    activeElement: { get: () => docState.activeElement, configurable: true },
    body: { get: () => docState.body, configurable: true },
    documentElement: { get: () => docState.documentElement, configurable: true },
  }) as unknown as Document
  const boundary = createForegroundFocusBoundary({
    focused: () => state.focused,
    visible: () => state.visible,
    now: () => clock,
    report: (event, fields) => logs.push({ event, fields }),
  })
  const guard = installPageFocusGuard(
    win as unknown as Window & typeof globalThis,
    doc,
    boundary,
    event => event.isTrusted || Boolean((event as { trustedForTest?: boolean }).trustedForTest),
  )
  function advance(ms: number) {
    clock += ms
    for (const [id, entry] of [...timers.entries()]) {
      if (entry.at <= clock) {
        timers.delete(id)
        entry.run()
      }
    }
  }
  return { state, logs, calls, win, doc, docState, body, boundary, guard, advance, Element, Input, Textarea, Button }
}

describe('page focus ownership', () => {
  it('covers newly created preview/clipboard targets and window activation without per-element registration', () => {
    const h = harness()
    try {
      h.state.focused = false
      new h.Element().focus()
      new h.Input().select()
      new h.Textarea().focus()
      new h.Textarea().select()
      h.win.focus()
      expect(h.calls).toEqual([])
      expect(h.logs.map(log => log.fields.source)).toEqual([
        'page-element-focus', 'page-input-select', 'page-element-focus', 'page-textarea-select', 'page-window-focus',
      ])
      expect(h.logs.every(log => log.fields.reason === 'window-unfocused')).toBe(true)
      h.state.focused = true
      h.state.visible = false
      new h.Element().focus()
      expect(h.calls).toEqual([])
      expect(h.logs.at(-1)?.fields.reason).toBe('hidden')
    } finally { h.guard.dispose() }
  })
  it('preserves foreground receivers/options, drops blocked calls and restores native methods on cleanup', () => {
    const h = harness()
    const el = new h.Element()
    const options = { preventScroll: true }
    try {
      el.focus(options)
      expect(h.calls).toEqual([['focus', el, options]])
      h.state.focused = false
      el.focus()
      h.state.focused = true
      expect(h.calls).toHaveLength(1) // returning does not replay the denied call
      new h.Input().select()
      new h.Textarea().select()
      expect(h.calls.map(call => call[0])).toEqual(['focus', 'input-select', 'textarea-select'])
    } finally { h.guard.dispose() }
    h.state.focused = false
    el.focus()
    expect(h.calls.at(-1)?.[0]).toBe('focus')
  })
  it('distinguishes window blur from element blur and reserves evidence after heavy foreground typing', () => {
    const h = harness()
    try {
      const token = h.boundary.token()
      h.doc.dispatchEvent(new Event('blur'))
      expect(h.boundary.token()).toBe(token)
      for (let i = 0; i < 100; i++) new h.Element().focus()
      h.state.focused = false
      h.win.dispatchEvent(new Event('blur'))
      expect(h.boundary.token()).toBe(token + 1)
      new h.Element().focus()
      expect(h.logs.at(-1)).toMatchObject({ event: 'cli.focus.blocked', fields: { source: 'page-element-focus' } })
      expect(h.guard.activateFromNotification(new Event('click'))).toBe(false)
      expect(h.calls.filter(call => call[0] === 'window-focus')).toEqual([])
      expect(h.guard.activateFromNotification({ type: 'click', isTrusted: true } as Event)).toBe(true)
      expect(h.calls.at(-1)).toEqual(['window-focus'])
      expect(h.logs.at(-1)).toMatchObject({ event: 'cli.focus.activation', fields: { source: 'notification-click' } })
    } finally { h.guard.dispose() }
  })
  it('parks activeElement on window blur and rejects macOS Contexts hidden transient focus pulses', () => {
    const h = harness()
    const xterm = new h.Textarea().markXtermHelper()
    try {
      xterm.focus()
      expect(h.doc.activeElement).toBe(xterm)
      h.calls.length = 0

      // Step 1: Contexts panel opens on Space 1 -> window blur while page still visible.
      h.state.focused = false
      h.win.dispatchEvent(new Event('blur'))
      expect(h.doc.activeElement).toBe(h.body)
      expect(h.calls).toEqual([['blur', xterm]])
      expect(h.logs.some(l => l.event === 'cli.focus.parked'
        && l.fields.target === 'xterm-helper'
        && l.fields.reason === 'window-blur'
        && l.fields.activeAfter === 'body')).toBe(true)

      // Step 2: Space transitions away -> visibility hidden.
      h.advance(750)
      h.state.visible = false
      h.doc.dispatchEvent(new Event('visibilitychange'))
      expect(h.doc.activeElement).toBe(h.body)

      // Step 3: Spurious native window.focus + focusin while hidden (focused=true, visible=false).
      h.advance(30)
      h.state.focused = true
      h.win.dispatchEvent(new Event('focus'))
      h.docState.activeElement = xterm
      const spuriousFocusIn = new Event('focusin')
      Object.defineProperty(spuriousFocusIn, 'target', { value: xterm })
      h.doc.dispatchEvent(spuriousFocusIn)
      expect(h.doc.activeElement).toBe(h.body)
      expect(h.logs.some(l => l.event === 'cli.focus.blocked'
        && l.fields.source === 'dom-focusin'
        && l.fields.reason === 'hidden'
        && l.fields.target === 'xterm-helper')).toBe(true)

      // Waiting past settle window while hidden must NOT restore focus.
      h.advance(FOCUS_RESTORE_SETTLE_MS * 2)
      expect(h.doc.activeElement).toBe(h.body)
      expect(h.calls.filter(c => c[0] === 'focus')).toEqual([])

      // Subsequent blur settles cleanly on target Space.
      h.state.focused = false
      h.win.dispatchEvent(new Event('blur'))
      expect(h.doc.activeElement).toBe(h.body)

      // Genuine return to foreground later restores xterm-helper after settle window.
      h.advance(5_000)
      h.state.visible = true
      h.doc.dispatchEvent(new Event('visibilitychange'))
      h.state.focused = true
      h.win.dispatchEvent(new Event('focus'))
      expect(h.doc.activeElement).toBe(h.body)
      h.advance(FOCUS_RESTORE_SETTLE_MS)
      expect(h.doc.activeElement).toBe(xterm)
      expect(h.calls.at(-1)).toEqual(['focus', xterm, { preventScroll: true }])
      expect(h.logs.some(l => l.event === 'cli.focus.request'
        && l.fields.source === 'parked-focus-restore'
        && l.fields.target === 'xterm-helper'
        && l.fields.trigger === 'settle')).toBe(true)
    } finally { h.guard.dispose() }
  })
  it('cancels parked restoration during brief same-Space handoff gaps, pointer clicks, or hidden targets, and restores immediately on foreground typing', () => {
    const h = harness()
    const xterm = new h.Textarea().markXtermHelper()
    const button = new h.Button()
    try {
      // 1. Brief same-Space Contexts handoff (<80ms transient focus -> blur) never refocuses xterm.
      xterm.focus()
      h.calls.length = 0
      h.state.focused = false
      h.win.dispatchEvent(new Event('blur'))
      expect(h.doc.activeElement).toBe(h.body)
      h.advance(20)
      h.state.focused = true
      h.win.dispatchEvent(new Event('focus'))
      h.advance(25) // < FOCUS_RESTORE_SETTLE_MS
      h.state.focused = false
      h.win.dispatchEvent(new Event('blur'))
      h.advance(FOCUS_RESTORE_SETTLE_MS * 2)
      expect(h.calls.filter(c => c[0] === 'focus')).toEqual([])

      // 2. Fast foreground typing (<80ms) restores immediately, while Alt/Meta chords do not.
      h.state.focused = true
      h.win.dispatchEvent(new Event('focus'))
      const altChord = Object.assign(new Event('keydown'), { key: 'Tab', altKey: true, metaKey: false, trustedForTest: true })
      h.doc.dispatchEvent(altChord)
      expect(h.doc.activeElement).toBe(h.body)

      const typedKey = Object.assign(new Event('keydown'), { key: 'l', altKey: false, metaKey: false, trustedForTest: true })
      h.doc.dispatchEvent(typedKey)
      expect(h.doc.activeElement).toBe(xterm)

      // 3. Returning via pointer click cancels parked restore so clicked control keeps focus.
      h.state.focused = false
      h.win.dispatchEvent(new Event('blur'))
      expect(h.doc.activeElement).toBe(h.body)
      h.state.focused = true
      h.win.dispatchEvent(new Event('focus'))
      h.advance(14)
      const pointerDown = Object.assign(new Event('pointerdown'), { trustedForTest: true })
      h.doc.dispatchEvent(pointerDown)
      button.focus()
      h.advance(FOCUS_RESTORE_SETTLE_MS * 2)
      expect(h.doc.activeElement).toBe(button)
      expect(JSON.stringify(h.logs)).not.toMatch(/"(?:value|text|url|key|sessionId)":/)
    } finally { h.guard.dispose() }
  })
  it('suppresses URL-less hidden history.replaceState while allowing explicit URL replacements without leaking URLs', () => {
    const h = harness()
    try {
      // Foreground URL-less and URL-bearing calls execute normally
      h.win.history.replaceState({ scroll: { left: 0, top: 0 } }, '')
      h.win.history.replaceState({}, '', '/portal/cli?t=secret-token')
      expect(h.calls.filter(c => c[0] === 'replaceState')).toHaveLength(2)
      h.calls.length = 0

      // Hidden URL-less history.replaceState(state, '') (vue-router's visibilitychange scroll
      // write on our overflow:hidden SPA) is suppressed so Chromium commits zero same-doc navs mid-Space-switch.
      h.state.visible = false
      h.win.history.replaceState({ scroll: { left: 0, top: 0 } }, '')
      expect(h.calls.filter(c => c[0] === 'replaceState')).toEqual([])
      expect(h.logs.some(l => l.event === 'cli.focus.blocked'
        && l.fields.source === 'page-history-replaceState'
        && l.fields.reason === 'hidden')).toBe(true)

      // Explicit URL replacements still execute and never leak URL/query contents into logs
      h.win.history.replaceState({}, '', '/portal/cli?t=secret-token')
      expect(h.calls.filter(c => c[0] === 'replaceState')).toHaveLength(1)
      expect(JSON.stringify(h.logs)).not.toContain('secret-token')
    } finally { h.guard.dispose() }
    // Restores native replaceState after dispose
    h.calls.length = 0
    h.state.visible = false
    h.win.history.replaceState({ scroll: { left: 0, top: 0 } }, '')
    expect(h.calls.filter(c => c[0] === 'replaceState')).toHaveLength(1)
  })
  it('keeps only bundle positions from stacks, dropping origins, query strings and function names', () => {
    expect(focusCallerLocations('Error\n at exampleCaller (http://localhost/assets/index-Ab3.js:2:81?example=synthetic-demo)\n at http://localhost/src/composables/cli/pageFocus.ts:77:8')).toEqual([
      'index-Ab3.js:2:81', 'pageFocus.ts:77:8',
    ])
  })
})
