import { describe, expect, it } from 'bun:test'
import { createForegroundFocusBoundary } from '../foregroundFocus'
import { focusCallerLocations, installPageFocusGuard } from '../pageFocus'

// Native calls are an oracle here: a background temporary input must never reach them.
// This does not emulate macOS activation; the real browser check is separate.
function harness() {
  const calls: unknown[][] = []
  const state = { focused: true, visible: true }
  const logs: { event: string; fields: Record<string, unknown> }[] = []
  class Element {
    tagName = 'DIV'
    classList = { contains: () => false }
    focus(...args: unknown[]) { calls.push(['focus', this, ...args]) }
  }
  class Input extends Element { select() { calls.push(['input-select', this]) } }
  class Textarea extends Element { select() { calls.push(['textarea-select', this]) } }
  const win = Object.assign(new EventTarget(), {
    HTMLElement: Element, HTMLInputElement: Input, HTMLTextAreaElement: Textarea,
    performance, focus() { calls.push(['window-focus']) },
  })
  const doc = Object.assign(new EventTarget(), { activeElement: null }) as unknown as Document
  const boundary = createForegroundFocusBoundary({ focused: () => state.focused, visible: () => state.visible,
    now: () => performance.now(), report: (event, fields) => logs.push({ event, fields }) })
  const guard = installPageFocusGuard(win as unknown as Window & typeof globalThis, doc, boundary)
  return { state, logs, calls, win, doc, boundary, guard, Element, Input, Textarea }
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
  it('keeps only bundle positions from stacks, dropping origins, query strings and function names', () => {
    expect(focusCallerLocations('Error\n at privateName (http://private:123/assets/index-Ab3.js:2:81?auth=secret)\n at http://host/src/composables/cli/pageFocus.ts:77:8')).toEqual([
      'index-Ab3.js:2:81', 'pageFocus.ts:77:8',
    ])
  })
})
