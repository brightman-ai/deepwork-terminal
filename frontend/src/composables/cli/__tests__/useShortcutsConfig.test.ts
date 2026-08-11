import { describe, it, expect, mock } from 'bun:test'

const saveSpy = mock((_d: unknown) => Promise.resolve())
// Seeded with the LEGACY pre-SSOT shape so the migration path is exercised on first load.
mock.module('@terminal/api/store', () => ({
  fetchStore: () => Promise.resolve({ cliTabShortcuts: { switchModifier: 'Ctrl', closeTab: 'Ctrl+W' } }),
  saveStore: (d: Record<string, unknown>) => { saveSpy(d); return Promise.resolve() },
}))

const { useShortcutsConfig, bindingFor, isDerived, leaderCostNote, DEFAULT_SHORTCUTS_CONFIG, DEFAULT_FIND_IN_TERMINAL_BINDING, DEFAULT_LEADER } = await import('../useShortcutsConfig')

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
    cfg.setOverride('newTab', 'Ctrl+KeyZ')
    cfg.resetToDefaults()
    expect(cfg.config.value).toEqual(DEFAULT_SHORTCUTS_CONFIG)
    await new Promise((r) => setTimeout(r, 550)) // past useServerStore's 500ms debounce
    expect(saveSpy).toHaveBeenLastCalledWith({ cliTabShortcuts: DEFAULT_SHORTCUTS_CONFIG })
  })
})

describe('findInTerminal — NOT part of the tab-switch prefix family', () => {
  // The default is per-platform, so assert against DEFAULT_FIND_IN_TERMINAL_BINDING rather than
  // against one platform's answer. This used to hardcode the non-Mac string on the assumption
  // that `bun test` has no `navigator` — an assumption the runtime quietly stopped honouring,
  // leaving two permanent failures that said nothing about the code.
  it('is never bare Ctrl+F — that is readline/vim forward-char inside the shell', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    expect(bindingFor(cfg.config.value, 'findInTerminal')).toBe(DEFAULT_FIND_IN_TERMINAL_BINDING)
    expect(DEFAULT_FIND_IN_TERMINAL_BINDING).not.toBe('Ctrl+KeyF')
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
    expect(bindingFor(cfg.config.value, 'findInTerminal')).toBe(DEFAULT_FIND_IN_TERMINAL_BINDING)
  })
})

/**
 * leader —— 第二条路的配置。
 *
 * 这一组里最要紧的是 `leaderCostNote`：注册一个 leader 就是从 shell 手里拿走那个键，而 leader
 * 唯一生效的场景（不在 tmux 里）**正是** shell 场景。设置页必须当场把代价说出来，而不是等人某天
 * 发现光标左移不好使了、再自己去猜是谁干的。
 */
describe('leader', () => {
  it('旧数据里没有 leader → 给默认值（这是新功能上线，不是用户关过它）', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()   // 种子是 legacy 形状，没有 leader 字段
    expect(cfg.config.value.leader).toBe(DEFAULT_LEADER)
  })

  it('空串 = 用户明确关掉，必须原样留住（?? 默认值 会把它悄悄打开）', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    cfg.setLeader('')
    expect(cfg.config.value.leader).toBe('')
  })

  it('换 leader 不动单修饰键前缀 —— 两条路本来就独立', async () => {
    const cfg = useShortcutsConfig()
    await cfg.load()
    const prefixBefore = cfg.config.value.prefix
    cfg.setLeader('Ctrl+Backquote')
    expect(cfg.config.value.leader).toBe('Ctrl+Backquote')
    expect(cfg.config.value.prefix).toBe(prefixBefore)
  })

  it('默认 Ctrl+B 会拿走 readline 的光标左移 —— 说出来，别让人自己去猜', () => {
    const note = leaderCostNote('Ctrl+KeyB')
    expect(note.text).toContain('左移')
    expect(note.severe).toBe(false)
  })

  // 这三个不是"少个快捷键"，是一按就中断/退出/挂起 —— 必须升级成红色警告。
  it('^C / ^D / ^Z 标为严重', () => {
    expect(leaderCostNote('Ctrl+KeyC').severe).toBe(true)
    expect(leaderCostNote('Ctrl+KeyD').severe).toBe(true)
    expect(leaderCostNote('Ctrl+KeyZ').severe).toBe(true)
  })

  it('shell 里本来没人用的键 → 无话可说（那正是个好选择）', () => {
    expect(leaderCostNote('Ctrl+Backquote').text).toBe('')
    expect(leaderCostNote('').text).toBe('')
  })

  // readline 的键位表只对**裸 Ctrl+字母**成立；Ctrl+Shift+B / Alt+B 不在它手里，警告就是误报。
  it('带额外修饰键的组合不套用 readline 键位表', () => {
    expect(leaderCostNote('Ctrl+Shift+KeyB').text).toBe('')
    expect(leaderCostNote('Alt+KeyB').text).toBe('')
  })
})
