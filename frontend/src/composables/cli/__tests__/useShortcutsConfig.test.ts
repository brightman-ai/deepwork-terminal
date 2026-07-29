import { describe, it, expect, mock } from 'bun:test'

const saveSpy = mock((_d: unknown) => Promise.resolve())
// Seeded with the LEGACY pre-SSOT shape so the migration path is exercised on first load.
mock.module('@terminal/api/store', () => ({
  fetchStore: () => Promise.resolve({ cliTabShortcuts: { switchModifier: 'Ctrl', closeTab: 'Ctrl+W' } }),
  saveStore: (d: Record<string, unknown>) => { saveSpy(d); return Promise.resolve() },
}))

const { useShortcutsConfig, bindingFor, isDerived, DEFAULT_SHORTCUTS_CONFIG } = await import('../useShortcutsConfig')

// NOTE: useShortcutsConfig sits on useServerStore, a MODULE-LEVEL singleton (same caveat as
// useServerStore.test.ts) — these share hydration and run in order.
describe('useShortcutsConfig', () => {
  it('migrates the legacy {switchModifier, per-action strings} shape to {prefix, overrides}', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    expect(cfg.config.value.prefix).toBe('Ctrl')      // the user's modifier is preserved
    expect(cfg.config.value.overrides).toEqual({})     // legacy per-action strings were just defaults
  })

  it('setPrefix moves every derived action at once', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    cfg.setPrefix('Alt')
    expect(bindingFor(cfg.config.value, 'closeTab')).toBe('Alt+KeyW')
    expect(bindingFor(cfg.config.value, 'newTab')).toBe('Alt+KeyN')
    expect(bindingFor(cfg.config.value, 'switchTab')).toBe('Alt')
  })

  it('setOverride pins ONE action and opts it out of later prefix changes', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    cfg.setPrefix('Alt')
    cfg.setOverride('closeTab', 'Ctrl+KeyQ')
    expect(isDerived(cfg.config.value, 'closeTab')).toBe(false)
    expect(isDerived(cfg.config.value, 'newTab')).toBe(true)

    cfg.setPrefix('Meta')
    expect(bindingFor(cfg.config.value, 'closeTab')).toBe('Ctrl+KeyQ') // untouched — personal choice wins
    expect(bindingFor(cfg.config.value, 'newTab')).toBe('Meta+KeyN')   // derived — follows
  })

  it('clearOverride hands an action back to the global prefix', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    cfg.setPrefix('Alt')
    cfg.setOverride('closeTab', 'Ctrl+KeyQ')
    cfg.clearOverride('closeTab')
    expect(bindingFor(cfg.config.value, 'closeTab')).toBe('Alt+KeyW')
  })

  it('resetToDefaults restores the prefix and drops every override', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    cfg.setOverride('renameTab', 'Ctrl+KeyZ')
    cfg.resetToDefaults()
    expect(cfg.config.value).toEqual(DEFAULT_SHORTCUTS_CONFIG)
    await new Promise((r) => setTimeout(r, 550)) // past useServerStore's 500ms debounce
    expect(saveSpy).toHaveBeenLastCalledWith({ cliTabShortcuts: DEFAULT_SHORTCUTS_CONFIG })
  })
})

describe('findInTerminal — NOT part of the tab-switch prefix family', () => {
  // bare `bun test` has no DOM, so `navigator` is undefined and the module's platform check falls
  // through to the non-Mac default — the same fallback a real Linux/Windows browser gets.
  it('defaults to Ctrl+Shift+KeyF (never bare Ctrl+F — that is readline/vim forward-char)', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    expect(bindingFor(cfg.config.value, 'findInTerminal')).toBe('Ctrl+Shift+KeyF')
  })

  it('does NOT move when the tab-switch prefix changes — it is not a derived action', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    const before = bindingFor(cfg.config.value, 'findInTerminal')
    cfg.setPrefix('Meta')
    expect(bindingFor(cfg.config.value, 'findInTerminal')).toBe(before)
  })

  it('can still be individually overridden and handed back, like any other action', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    expect(isDerived(cfg.config.value, 'findInTerminal')).toBe(true)
    cfg.setOverride('findInTerminal', 'Alt+KeyF')
    expect(isDerived(cfg.config.value, 'findInTerminal')).toBe(false)
    expect(bindingFor(cfg.config.value, 'findInTerminal')).toBe('Alt+KeyF')
    cfg.clearOverride('findInTerminal')
    expect(isDerived(cfg.config.value, 'findInTerminal')).toBe(true)
    expect(bindingFor(cfg.config.value, 'findInTerminal')).toBe('Ctrl+Shift+KeyF')
  })
})
