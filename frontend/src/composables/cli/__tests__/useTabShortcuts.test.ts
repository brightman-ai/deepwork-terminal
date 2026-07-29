import { describe, it, expect, mock } from 'bun:test'

// useTabShortcuts pulls in useShortcutsConfig -> useServerStore -> @terminal/api/store, whose
// import chain touches `window.location` at module scope (useCliAuth.ts) — dead in bare `bun
// test` (no DOM). Mock the store API before importing, same pattern as useServerStore.test.ts.
mock.module('@terminal/api/store', () => ({
  fetchStore: () => Promise.resolve({}),
  saveStore: () => Promise.resolve(),
}))

const { parseBinding, matchesBinding, matchesPrefixDigit, resolveShortcutAction } = await import('../useTabShortcuts')
const { DEFAULT_SHORTCUTS_CONFIG, bindingFor } = await import('../useShortcutsConfig')

/**
 * `code` is what the OS reports for the PHYSICAL key; `key` is the character produced. The two
 * diverge under macOS Option (a compose modifier) and under non-Latin layouts — which is exactly
 * how the first implementation shipped a keymap that did nothing on a Mac.
 */
function key(o: Partial<{ code: string; key: string; altKey: boolean; ctrlKey: boolean; shiftKey: boolean; metaKey: boolean }>): KeyboardEvent {
  return { code: '', key: '', altKey: false, ctrlKey: false, shiftKey: false, metaKey: false, ...o } as KeyboardEvent
}

describe('parseBinding', () => {
  it('splits modifiers from the physical code', () => {
    expect(parseBinding('Alt+KeyW')).toEqual({ alt: true, ctrl: false, shift: false, meta: false, code: 'KeyW' })
    expect(parseBinding('Ctrl+ArrowUp')).toEqual({ alt: false, ctrl: true, shift: false, meta: false, code: 'ArrowUp' })
  })
  it('parses a modifier-only binding (the digit family)', () => {
    expect(parseBinding('Alt').code).toBe('')
  })
})

describe('macOS Option regression — the bug that shipped', () => {
  // On macOS, Option+1 delivers key:"¡" and Option+W key:"∑". Matching `key` made every binding
  // dead on a Mac while CDP-synthesized events (key:"1") passed. These pin the fix.
  it('Option+1 resolves to tab 1 even though the character is "¡"', () => {
    const e = key({ code: 'Digit1', key: '¡', altKey: true })
    expect(matchesPrefixDigit(e, 'Alt')).toBe(1)
  })
  it('Option+W matches the close binding even though the character is "∑"', () => {
    const e = key({ code: 'KeyW', key: '∑', altKey: true })
    expect(matchesBinding(e, 'Alt+KeyW')).toBe(true)
  })
  it('a non-Latin layout (Cyrillic "ц" on the W key) still matches', () => {
    expect(matchesBinding(key({ code: 'KeyW', key: 'ц', altKey: true }), 'Alt+KeyW')).toBe(true)
  })
  it('end-to-end: Option+2 selects the 2nd visible tab', () => {
    const e = key({ code: 'Digit2', key: '™', altKey: true })
    expect(resolveShortcutAction(e, DEFAULT_SHORTCUTS_CONFIG, ['t1', 't2', 't3'], 't1'))
      .toEqual({ type: 'select', id: 't2' })
  })
})

describe('matchesPrefixDigit', () => {
  it('rejects Digit0 (there is no tab 0) and non-digits', () => {
    expect(matchesPrefixDigit(key({ code: 'Digit0', altKey: true }), 'Alt')).toBeUndefined()
    expect(matchesPrefixDigit(key({ code: 'KeyA', altKey: true }), 'Alt')).toBeUndefined()
  })
  it('requires the exact modifier set — a missing or extra modifier does not fire', () => {
    expect(matchesPrefixDigit(key({ code: 'Digit1' }), 'Alt')).toBeUndefined()
    expect(matchesPrefixDigit(key({ code: 'Digit1', altKey: true, shiftKey: true }), 'Alt')).toBeUndefined()
  })
})

describe('resolveShortcutAction', () => {
  const ids = ['t1', 't2', 't3']
  const cfg = DEFAULT_SHORTCUTS_CONFIG

  it('next/prev move relative to the active tab and wrap', () => {
    expect(resolveShortcutAction(key({ code: 'ArrowDown', altKey: true }), cfg, ids, 't3'))
      .toEqual({ type: 'select', id: 't1' })
    expect(resolveShortcutAction(key({ code: 'ArrowUp', altKey: true }), cfg, ids, 't1'))
      .toEqual({ type: 'select', id: 't3' })
  })

  it('new/close/rename resolve from their bindings', () => {
    expect(resolveShortcutAction(key({ code: 'KeyN', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'new' })
    expect(resolveShortcutAction(key({ code: 'KeyW', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'close' })
    expect(resolveShortcutAction(key({ code: 'KeyR', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'rename' })
  })

  it('plain typing is never swallowed', () => {
    expect(resolveShortcutAction(key({ code: 'KeyW', key: 'w' }), cfg, ids, 't1')).toBeNull()
    expect(resolveShortcutAction(key({ code: 'Enter', key: 'Enter' }), cfg, ids, 't1')).toBeNull()
  })

  it('a digit beyond the tab count resolves to nothing', () => {
    expect(resolveShortcutAction(key({ code: 'Digit9', altKey: true }), cfg, ids, 't1')).toBeNull()
  })

  it('close/rename with no active tab resolve to nothing', () => {
    expect(resolveShortcutAction(key({ code: 'KeyW', altKey: true }), cfg, ids, undefined)).toBeNull()
    expect(resolveShortcutAction(key({ code: 'KeyR', altKey: true }), cfg, ids, undefined)).toBeNull()
  })
})

describe('prefix is the SSOT', () => {
  it('changing the prefix moves EVERY derived action at once', () => {
    const ctrl = { prefix: 'Ctrl' as const, overrides: {} }
    const ids = ['t1', 't2']
    expect(resolveShortcutAction(key({ code: 'Digit1', ctrlKey: true }), ctrl, ids, 't2'))
      .toEqual({ type: 'select', id: 't1' })
    expect(resolveShortcutAction(key({ code: 'KeyN', ctrlKey: true }), ctrl, ids, 't1')).toEqual({ type: 'new' })
    // The old prefix stops working — no stale Alt bindings left behind.
    expect(resolveShortcutAction(key({ code: 'KeyN', altKey: true }), ctrl, ids, 't1')).toBeNull()
  })

  it('an individually overridden action is NOT swept along by a prefix change', () => {
    const cfg = { prefix: 'Ctrl' as const, overrides: { closeTab: 'Alt+KeyQ' } }
    const ids = ['t1']
    // close keeps the personal binding…
    expect(resolveShortcutAction(key({ code: 'KeyQ', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'close' })
    // …and does NOT answer to the new global prefix.
    expect(resolveShortcutAction(key({ code: 'KeyW', ctrlKey: true }), cfg, ids, 't1')).toBeNull()
    // while a derived sibling does follow it.
    expect(resolveShortcutAction(key({ code: 'KeyN', ctrlKey: true }), cfg, ids, 't1')).toEqual({ type: 'new' })
  })

  it('bindingFor derives from the prefix and honors overrides', () => {
    expect(bindingFor({ prefix: 'Alt', overrides: {} }, 'closeTab')).toBe('Alt+KeyW')
    expect(bindingFor({ prefix: 'Ctrl', overrides: {} }, 'closeTab')).toBe('Ctrl+KeyW')
    expect(bindingFor({ prefix: 'Ctrl', overrides: { closeTab: 'Alt+KeyQ' } }, 'closeTab')).toBe('Alt+KeyQ')
  })
})
