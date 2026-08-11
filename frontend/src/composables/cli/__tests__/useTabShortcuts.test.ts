import { describe, it, expect, mock } from 'bun:test'

// useTabShortcuts pulls in useShortcutsConfig -> useServerStore -> @terminal/api/store, whose
// import chain touches `window.location` at module scope (useCliAuth.ts) — dead in bare `bun
// test` (no DOM). Mock the store API before importing, same pattern as useServerStore.test.ts.
mock.module('@terminal/api/store', () => ({
  fetchStore: () => Promise.resolve({}),
  saveStore: () => Promise.resolve(),
}))

const { parseBinding, matchesBinding, matchesPrefixDigit, resolveShortcutAction, resolveLeaderKey } = await import('../useTabShortcuts')
const { DEFAULT_SHORTCUTS_CONFIG, DEFAULT_LEADER, bindingFor } = await import('../useShortcutsConfig')

type Cfg = typeof DEFAULT_SHORTCUTS_CONFIG
/** 手搓一个配置。leader 给默认值，这样这些用例断言的仍然只是「单修饰键前缀那条路」。 */
function cfgOf(p: Partial<Cfg>): Cfg {
  return { prefix: 'Alt', overrides: {}, leader: DEFAULT_LEADER, ...p }
}

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

  it('new/close resolve from their bindings', () => {
    expect(resolveShortcutAction(key({ code: 'KeyN', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'new' })
    expect(resolveShortcutAction(key({ code: 'KeyW', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'close' })
  })

  // Rename has no binding at all, so the key it used to hold reaches the terminal untouched. With
  // prefix=Ctrl that key was Ctrl+R — readline's reverse-i-search — spent on a once-a-week action.
  it('the key rename used to hold is left to the terminal', () => {
    for (const cfgUnder of [cfg, cfgOf({ prefix: 'Ctrl' })]) {
      expect(resolveShortcutAction(key({ code: 'KeyR', altKey: true }), cfgUnder, ids, 't1')).toBeNull()
      expect(resolveShortcutAction(key({ code: 'KeyR', ctrlKey: true }), cfgUnder, ids, 't1')).toBeNull()
    }
  })

  it('plain typing is never swallowed', () => {
    expect(resolveShortcutAction(key({ code: 'KeyW', key: 'w' }), cfg, ids, 't1')).toBeNull()
    expect(resolveShortcutAction(key({ code: 'Enter', key: 'Enter' }), cfg, ids, 't1')).toBeNull()
  })

  it('a digit beyond the tab count resolves to nothing', () => {
    expect(resolveShortcutAction(key({ code: 'Digit9', altKey: true }), cfg, ids, 't1')).toBeNull()
  })

  it('close with no active tab resolves to nothing', () => {
    expect(resolveShortcutAction(key({ code: 'KeyW', altKey: true }), cfg, ids, undefined)).toBeNull()
  })
})

describe('prefix is the SSOT', () => {
  it('changing the prefix moves EVERY derived action at once', () => {
    const ctrl = cfgOf({ prefix: 'Ctrl' })
    const ids = ['t1', 't2']
    expect(resolveShortcutAction(key({ code: 'Digit1', ctrlKey: true }), ctrl, ids, 't2'))
      .toEqual({ type: 'select', id: 't1' })
    expect(resolveShortcutAction(key({ code: 'KeyN', ctrlKey: true }), ctrl, ids, 't1')).toEqual({ type: 'new' })
    // The old prefix stops working — no stale Alt bindings left behind.
    expect(resolveShortcutAction(key({ code: 'KeyN', altKey: true }), ctrl, ids, 't1')).toBeNull()
  })

  it('an individually overridden action is NOT swept along by a prefix change', () => {
    const cfg = cfgOf({ prefix: 'Ctrl', overrides: { closeTab: 'Alt+KeyQ' } })
    const ids = ['t1']
    // close keeps the personal binding…
    expect(resolveShortcutAction(key({ code: 'KeyQ', altKey: true }), cfg, ids, 't1')).toEqual({ type: 'close' })
    // …and does NOT answer to the new global prefix.
    expect(resolveShortcutAction(key({ code: 'KeyW', ctrlKey: true }), cfg, ids, 't1')).toBeNull()
    // while a derived sibling does follow it.
    expect(resolveShortcutAction(key({ code: 'KeyN', ctrlKey: true }), cfg, ids, 't1')).toEqual({ type: 'new' })
  })

  it('bindingFor derives from the prefix and honors overrides', () => {
    expect(bindingFor(cfgOf({ prefix: 'Alt' }), 'closeTab')).toBe('Alt+KeyW')
    expect(bindingFor(cfgOf({ prefix: 'Ctrl' }), 'closeTab')).toBe('Ctrl+KeyW')
    expect(bindingFor(cfgOf({ prefix: 'Ctrl', overrides: { closeTab: 'Alt+KeyQ' } }), 'closeTab')).toBe('Alt+KeyQ')
  })
})

/**
 * leader —— 第二段。第一段（认出 leader 本身）走的是 matchesBinding，已被上面的用例覆盖；
 * 这里钉住的是「按下 leader 之后那一个键意味着什么」，以及**它什么时候必须放手**。
 */
describe('leader 第二段', () => {
  const L = DEFAULT_LEADER  // 'Ctrl+KeyB'
  /** 一个「什么都实现了」的宿主。差异化能力由本组最后两条单独覆盖。 */
  const ALL = new Set(['switchTab', 'nextTab', 'prevTab', 'newTab', 'closeTab', 'overview', 'rename'] as const)

  it('照抄 tmux 的键位：c 新建 / n 下一个 / p 上一个 / w 概览 / , 重命名', () => {
    expect(resolveLeaderKey(key({ code: 'KeyC', key: 'c' }), L, ALL)).toEqual({ type: 'action', action: 'newTab' })
    expect(resolveLeaderKey(key({ code: 'KeyN', key: 'n' }), L, ALL)).toEqual({ type: 'action', action: 'nextTab' })
    expect(resolveLeaderKey(key({ code: 'KeyP', key: 'p' }), L, ALL)).toEqual({ type: 'action', action: 'prevTab' })
    expect(resolveLeaderKey(key({ code: 'KeyW', key: 'w' }), L, ALL)).toEqual({ type: 'action', action: 'overview' })
    expect(resolveLeaderKey(key({ code: 'Comma', key: ',' }), L, ALL)).toEqual({ type: 'action', action: 'rename' })
  })

  it('关闭用 x（tmux 的 kill-pane），不是要按 Shift 的 &', () => {
    expect(resolveLeaderKey(key({ code: 'KeyX', key: 'x' }), L, ALL)).toEqual({ type: 'action', action: 'closeTab' })
  })

  it('数字 1-9 切标签；0 不是标签号', () => {
    expect(resolveLeaderKey(key({ code: 'Digit3', key: '3' }), L, ALL)).toEqual({ type: 'action', action: 'switchTab', digit: 3 })
    expect(resolveLeaderKey(key({ code: 'Digit0', key: '0' }), L, ALL)).toEqual({ type: 'passthrough' })
  })

  it('Esc 或再按一次 leader = 取消', () => {
    expect(resolveLeaderKey(key({ code: 'Escape', key: 'Escape' }), L, ALL)).toEqual({ type: 'cancel' })
    expect(resolveLeaderKey(key({ code: 'KeyB', key: 'b', ctrlKey: true }), L, ALL)).toEqual({ type: 'cancel' })
  })

  // 这一条是这组里最重要的：一个「进了模式就吞掉一切」的 leader，会在误触后静静吃掉你接下来敲的
  // 那个字符，而屏幕上什么都不会发生 —— 你只会觉得键盘坏了。没命中就放行，字符进终端，看得见。
  it('没命中的键放行，不吞', () => {
    expect(resolveLeaderKey(key({ code: 'KeyZ', key: 'z' }), L, ALL)).toEqual({ type: 'passthrough' })
    expect(resolveLeaderKey(key({ code: 'Enter', key: 'Enter' }), L, ALL)).toEqual({ type: 'passthrough' })
  })

  it('第二段带修饰键一律放行 —— leader 的意义就是第二段不需要修饰键', () => {
    // Ctrl+B 然后 Ctrl+C 必须是「取消 + 一个真正的 ^C」，不是某个隐藏绑定。
    expect(resolveLeaderKey(key({ code: 'KeyC', key: 'c', ctrlKey: true }), L, ALL)).toEqual({ type: 'passthrough' })
    expect(resolveLeaderKey(key({ code: 'KeyN', key: 'n', altKey: true }), L, ALL)).toEqual({ type: 'passthrough' })
  })

  it('修饰键本身不算一段（按住 Ctrl 时 keydown 会先为 Control 触发一次）', () => {
    expect(resolveLeaderKey(key({ code: 'ControlLeft', key: 'Control' }), L, ALL)).toEqual({ type: 'passthrough' })
    expect(resolveLeaderKey(key({ code: 'ShiftLeft', key: 'Shift' }), L, ALL)).toEqual({ type: 'passthrough' })
  })

  it('第二段按物理键位 —— macOS Option 或非拉丁布局下印出什么字符都不影响', () => {
    expect(resolveLeaderKey(key({ code: 'KeyC', key: 'ç' }), L, ALL)).toEqual({ type: 'action', action: 'newTab' })
    expect(resolveLeaderKey(key({ code: 'KeyW', key: 'ц' }), L, ALL)).toEqual({ type: 'action', action: 'overview' })
  })
})

/**
 * 宿主能力差异 —— 这一组盯的是一个真实的坑：
 *
 * `overview` / `rename` 是 adapter 的**可选**回调（有的壳没有这两个概念）。最初的实现是「先吞掉
 * 这个键，再去调 `adapter.onOverview?.()`」—— 于是在没实现它的壳里，按下去既没有动作、键也没进
 * 终端，屏幕上什么都不发生。使用者只会以为键盘坏了，而且没有任何东西会报错。
 *
 * 正确的做法是让能力差异在**解析阶段**就体现：没实现 = 这个键根本不属于 leader = 原样放行。
 */
describe('leader 只吞它真的能执行的键', () => {
  const L = DEFAULT_LEADER
  const MINIMAL = new Set(['switchTab', 'nextTab', 'prevTab', 'newTab', 'closeTab'] as const)

  it('宿主没实现 overview / rename → 那两个键放行，不吞', () => {
    expect(resolveLeaderKey(key({ code: 'KeyW', key: 'w' }), L, MINIMAL)).toEqual({ type: 'passthrough' })
    expect(resolveLeaderKey(key({ code: 'Comma', key: ',' }), L, MINIMAL)).toEqual({ type: 'passthrough' })
  })

  it('同一个壳里，它实现了的键照常工作', () => {
    expect(resolveLeaderKey(key({ code: 'KeyC', key: 'c' }), L, MINIMAL)).toEqual({ type: 'action', action: 'newTab' })
    expect(resolveLeaderKey(key({ code: 'Digit2', key: '2' }), L, MINIMAL)).toEqual({ type: 'action', action: 'switchTab', digit: 2 })
  })

  it('一个什么都没实现的壳，leader 第二段一个键都不吞', () => {
    const NONE = new Set([] as const)
    for (const code of ['KeyC', 'KeyN', 'KeyP', 'KeyX', 'KeyW', 'Comma', 'Digit1']) {
      expect(resolveLeaderKey(key({ code }), L, NONE)).toEqual({ type: 'passthrough' })
    }
  })

  // Shift 曾经漏在 modifier 守卫外面：Shift+3 打出的是 `#`，被当成「跳到第 3 个标签」，
  // 于是敲一个 `#` 就莫名其妙换了标签。
  it('第二段带 Shift 一律放行（Shift+3 是 # 不是「第 3 个」）', () => {
    expect(resolveLeaderKey(key({ code: 'Digit3', key: '#', shiftKey: true }), L, ALL_ACTIONS)).toEqual({ type: 'passthrough' })
    expect(resolveLeaderKey(key({ code: 'KeyC', key: 'C', shiftKey: true }), L, ALL_ACTIONS)).toEqual({ type: 'passthrough' })
  })
})

const ALL_ACTIONS = new Set(['switchTab', 'nextTab', 'prevTab', 'newTab', 'closeTab', 'overview', 'rename'] as const)
