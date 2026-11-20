import { describe, expect, it } from 'bun:test'
import { Terminal } from '@xterm/xterm'
import { createForegroundFocusBoundary, guardElementFocus } from '../foregroundFocus'

function harness() {
  const state = { focused: true, visible: true, now: 0 }
  const logs: { event: string; fields: Record<string, unknown> }[] = []
  const gate = createForegroundFocusBoundary({ focused: () => state.focused, visible: () => state.visible,
    now: () => state.now, report: (event, fields) => logs.push({ event, fields }) })
  return { state, gate, logs }
}

describe('foreground input ownership', () => {
  it('a selected, visible tab cannot focus while another app owns the window', () => {
    const { state, gate, logs } = harness()
    state.focused = false
    let calls = 0
    expect(gate.run('compose-desktop', () => calls++)).toBe(false)
    expect(calls).toBe(0)
    expect(logs[0]?.fields).toMatchObject({ source: 'compose-desktop', reason: 'window-unfocused', focused: false, visible: true })
    state.focused = true
    expect(gate.run('compose-desktop', () => calls++)).toBe(true)
    expect(calls).toBe(1)
  })
  it('losing the window invalidates queued handoffs even after returning', () => {
    const { state, gate, logs } = harness()
    const token = gate.token()
    state.focused = false
    gate.blur()
    state.focused = true
    gate.focus()
    let calls = 0
    expect(gate.run('tab-input-handoff', () => calls++, token)).toBe(false)
    expect(calls).toBe(0)
    expect(logs.at(-1)?.fields.reason).toBe('stale-request')
    expect(gate.run('tab-input-handoff', () => calls++, gate.token())).toBe(true)
    expect(calls).toBe(1)
  })
  it('hiding the browser tab also invalidates pending work', () => {
    const { state, gate } = harness()
    const token = gate.token()
    state.visible = false
    gate.visibility()
    expect(gate.run('terminal-search', () => { throw new Error('hidden focus') })).toBe(false)
    state.visible = true
    expect(gate.run('terminal-search', () => { throw new Error('stale focus') }, token)).toBe(false)
  })
  it('guards internal/deferred element focus and restores the original method on disposal', () => {
    const { state, gate } = harness()
    const options: (FocusOptions | undefined)[] = []
    const element = Object.create({ focus(opts?: FocusOptions) { options.push(opts) } }) as HTMLElement
    const native = element.focus
    const dispose = guardElementFocus(element, gate, 'xterm-helper')
    state.focused = false
    element.focus()
    expect(options).toEqual([])
    state.focused = true
    element.focus({ preventScroll: true })
    expect(options).toEqual([{ preventScroll: true }])
    dispose()
    expect(element.focus).toBe(native)
  })
  it('intercepts actual xterm keyup refocusing while preserving foreground typing', () => {
    const { state, gate } = harness()
    const term = new Terminal({ allowProposedApi: true })
    const core = (term as unknown as { _core: { focus(): void; updateCursorStyle(): void; _keyUp(e: KeyboardEvent): void } })._core
    let calls = 0
    core.focus = () => { calls++ }
    core.updateCursorStyle = () => {}
    term.attachCustomKeyEventHandler(event => gate.keyEvent(event))
    const release = (key: string, keyCode: number, metaKey = false, altKey = false) =>
      core._keyUp({ type: 'keyup', key, keyCode, metaKey, altKey } as KeyboardEvent)
    try {
      release('Meta', 91)
      release('Alt', 18)
      release('Tab', 9, true)
      release('Tab', 9, false, true)
      expect(calls).toBe(0)
      state.focused = false
      release('a', 65)
      expect(calls).toBe(0)
      state.focused = true
      release('a', 65)
      expect(calls).toBe(1)
    } finally { term.dispose() }
  })
  it('bounds repetitive logs without losing the window ownership event', () => {
    const { state, gate, logs } = harness()
    state.focused = false
    for (let i = 0; i < 100; i++) gate.run('xterm-helper', () => {})
    expect(logs).toHaveLength(80)
    gate.blur()
    expect(logs).toHaveLength(81)
    expect(logs.at(-1)?.fields.suppressed).toBe(20)
    expect(JSON.stringify(logs)).not.toMatch(/"(?:value|text|url|key|sessionId)":/)
    state.now = 10_001
    gate.run('xterm-helper', () => {})
    expect(logs).toHaveLength(82)
  })
})
