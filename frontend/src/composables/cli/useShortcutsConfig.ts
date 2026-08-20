/**
 * useShortcutsConfig — persistence for the CLI tab shortcuts (D2/D3).
 *
 * ── The prefix is the SSOT ────────────────────────────────────────────────────────────────────
 * A user thinks "my shortcuts are the Alt ones", not "six independent bindings that happen to
 * start with Alt". So ONE `prefix` drives every action, and each action's key is derived from it.
 * Changing the prefix moves all six at once — which is what "如果是SSOT的,按理是一起改的" asks for.
 *
 * An action the user has *individually* rebound is recorded in `overrides` and is NOT swept along
 * by a later prefix change: an explicit personal choice outranks a bulk default. Reset clears both.
 *
 * Backed by the existing generic per-key server KV (useServerStore → GET/PUT /api/store), which
 * already carries the hydration gate + server-side per-key merge that fixed the store-overwrite
 * bug — a new key here inherits that safety instead of re-risking it.
 */
import { computed, ref, type Ref } from 'vue'
import { useServerStore } from './useServerStore'

/** The six tab actions, plus `findInTerminal` (opens the in-terminal search bar). `switchTab` is
 *  the digit family (prefix + 1..9); the tab actions are prefix + a key. `findInTerminal` is NOT
 *  part of that prefix family — see its default binding below — but shares the same persistence/
 *  override machinery so it is configured and stored the same way. */
export type ShortcutAction =
  | 'switchTab' | 'prevTab' | 'nextTab' | 'newTab' | 'closeTab'
  | 'findInTerminal' | 'toggleComposeDesktop'

/**
 * A modifier (or modifier combo) that can serve as the global prefix.
 *
 * Combos are offered because a single modifier is not always available: browsers reserve some,
 * and third-party tools grab others system-wide (e.g. Contexts on macOS claims plain Alt). A
 * two-modifier prefix is almost never taken by either, so it is the reliable escape hatch.
 */
export type ShortcutPrefix = 'Alt' | 'Ctrl' | 'Meta' | 'Ctrl+Shift' | 'Alt+Shift'

/**
 * The key each action binds, expressed as a KeyboardEvent.code (NOT a printed character).
 *
 * `code` is physical-key identity: it stays "KeyN" whether the OS produces "n", "˜" (macOS
 * Option+n) or a non-Latin layout's character. Matching on the printed `key` is exactly why every
 * Alt binding silently did nothing on macOS — see useTabShortcuts.matchesBinding.
 */
export const ACTION_CODES: Record<Exclude<ShortcutAction, 'switchTab' | 'findInTerminal'>, string> = {
  prevTab: 'ArrowUp',
  nextTab: 'ArrowDown',
  newTab: 'KeyN',
  closeTab: 'KeyW',
  // Desktop-only (mobile reaches the compose bar through its bottom toolbar — no keyboard to
  // bind). Unlike findInTerminal, there is no OS/browser idiom to defer to here, so it just
  // follows the user's own prefix like the tab actions above instead of a special-cased default.
  toggleComposeDesktop: 'KeyI',
}

// Renaming a tab has NO binding, deliberately. It is a rare action, and any key it took would be
// taken from the shell underneath: with prefix=Ctrl the derived binding was Ctrl+R, which is
// readline's reverse-i-search — every user of it, every day, losing history search so that a
// once-a-week rename could have a shortcut. Rename stays where a rare action belongs: double-click
// the tab, or its context menu. The same trade decided findInTerminal's default below.

// findInTerminal's default is deliberately NOT `prefix + code`: it must match the OS/browser's
// idiomatic "find" gesture (Cmd+F on macOS) regardless of whatever prefix the user picked for tab
// switching — a user who set prefix=Ctrl still expects Cmd+F to open search on their Mac. Ctrl+F
// alone is reserved (readline/vim forward-char inside the shell), so the non-Mac default adds
// Shift so a bare Ctrl+F always reaches the PTY untouched.
const IS_MAC_PLATFORM = typeof navigator !== 'undefined'
  && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '')
export const DEFAULT_FIND_IN_TERMINAL_BINDING = IS_MAC_PLATFORM ? 'Meta+KeyF' : 'Ctrl+Shift+KeyF'

/**
 * ── leader：第二条路，不是替代 ──────────────────────────────────────────────────────────────
 *
 * 上面那套是**单修饰键前缀**：一按就到（Alt+2）。它够快，但候选池只有 Alt / Ctrl / Meta 和两个
 * 组合 —— 浏览器和系统级工具就在这几个里抢，抢完了就没有退路。leader 是 tmux 那种**两段式**：
 * 先敲 leader，再敲一个普通字母/数字。因为第二段不再需要修饰键，键位空间一下子够用了。
 *
 * 两条路**同时有效**，映射同一张动作表。这不是重复，是互为退路 —— 而且退路是真的会用上的：
 * 在 attach 了 tmux 的标签里 leader 整个让位给 tmux（见 useTabShortcuts），那时 Alt+数字 就是
 * 你在那个标签里切 dw 标签的唯一手段。
 *
 * 默认 `Ctrl+B` 是使用者在知情下选的：它在 shell 里本来是 readline 的 backward-char（光标左移
 * 一格），dw 注册它就意味着在**非 tmux** 场景（也就是这条 leader 唯一生效的场景）里吃掉那个键。
 * 代价说清楚了，选择是使用者的。改成别的（`Ctrl+\`` 在 readline 里没有默认绑定）或留空关掉，
 * 都在设置页里。
 */
export const DEFAULT_LEADER = 'Ctrl+KeyB'

/** leader 能触发的动作。比 ShortcutAction 多两个：它们本来就没有单修饰键绑定可给。 */
export type LeaderAction = Exclude<ShortcutAction, 'findInTerminal'> | 'overview' | 'rename'

/**
 * leader 之后按哪个键。**照抄 tmux**，因为要的就是"和 tmux 一样的快捷键"，而且第二段已经不受
 * 浏览器保留键的限制，字母可以随便用：
 *   c = new-window  ·  n = next-window  ·  p = previous-window  ·  w = choose-window  ·  , = rename
 * 数字 1-9 单独处理（tmux 的 select-window）。
 *
 * 关闭用 `x`（tmux 的 kill-**pane**）而不是 tmux 关窗口的 `&`：`&` 要按 Shift+7，用物理键位表达
 * 就是 Shift+Digit7，在非美式布局上根本不是同一个键；`x` 无修饰、单手可达，而且 tmux 用户对
 * "x = 关掉当前这个" 本来就熟。
 *
 * **没有搜索**：终端内搜索的分发住在 CliTerminalSurface（它才碰得到 xterm），不在这张表能触及的
 * 范围里；而它已经有 Cmd+F 和状态行上那枚常驻按钮两个入口了。为一个键把跨组件的线拉过来不值。
 */
export const LEADER_BINDINGS: ReadonlyArray<{
  /** 物理键位（KeyboardEvent.code）—— 匹配一律走它，见 useTabShortcuts 头部那段教训。 */
  code: string
  action: LeaderAction
  /** 印给人看的那个字符。 */
  key: string
  /** 印给人看的那个动作名。 */
  hint: string
}> = [
  { code: 'Digit1', action: 'switchTab', key: '1-9', hint: '跳转' }, // 数字族：code 仅作占位，实际匹配 Digit1-9
  { code: 'KeyC', action: 'newTab', key: 'c', hint: '新建' },
  { code: 'KeyN', action: 'nextTab', key: 'n', hint: '下一个' },
  { code: 'KeyP', action: 'prevTab', key: 'p', hint: '上一个' },
  { code: 'KeyW', action: 'overview', key: 'w', hint: '概览' },
  { code: 'KeyX', action: 'closeTab', key: 'x', hint: '关闭' },
  { code: 'Comma', action: 'rename', key: ',', hint: '重命名' },
]

/** code → action 的查表。**从 LEADER_BINDINGS 派生**，不另手写一份。数字族单独处理，不进这张表。 */
export const LEADER_CODES: Record<string, LeaderAction> = Object.fromEntries(
  LEADER_BINDINGS.filter((b) => b.action !== 'switchTab').map((b) => [b.code, b.action]),
)

/**
 * 「leader 之后能按什么」这句提示 —— 同样**从 LEADER_BINDINGS 派生**。
 *
 * 它此前是手写的字符串，而且写了两份（终端里的等待提示、设置页的说明）。手写的第二份注定和键位
 * 表分家：改了键位忘了改文案，屏幕上就开始教人按一个不存在的键。
 *
 * @param available 宿主真的实现了的动作。没实现的不印 —— 提示里列一个按下去没反应的键，
 *   比不列更伤：使用者会以为是自己按错了。不传 = 全列（设置页那种"这个功能长什么样"的场合）。
 */
export function leaderHintText(available?: ReadonlySet<LeaderAction>): string {
  const parts = LEADER_BINDINGS
    .filter((b) => !available || available.has(b.action))
    .map((b) => `${b.key} ${b.hint}`)
  return [...parts, 'Esc 取消'].join(' · ')
}

/**
 * 这个 leader 在 shell 里原本是什么键。
 *
 * 注册一个 leader 就是从 shell 手里拿走那个键 —— 而 leader 唯一生效的场景（非 tmux）正是 shell
 * 场景。所以设置页必须当场说出代价，而不是等用户某天发现光标左移不好使了、再自己去猜是谁干的。
 *
 * 只收**真的会被拿走且真的有人天天用**的那些（readline 默认键位，bash/zsh/emacs 模式通用）。
 * 查不到 → 空串：那意味着这个键本来就没人用，是个好选择，没什么可警告的。
 */
const READLINE_KEYS: Record<string, { what: string; severe?: boolean }> = {
  KeyA: { what: '跳到行首' },
  KeyB: { what: '光标左移一格' },
  KeyC: { what: '中断当前命令（SIGINT）', severe: true },
  KeyD: { what: '发送 EOF / 退出 shell', severe: true },
  KeyE: { what: '跳到行尾' },
  KeyF: { what: '光标右移一格' },
  KeyG: { what: '取消当前操作' },
  KeyK: { what: '删到行尾' },
  KeyL: { what: '清屏' },
  KeyR: { what: '反向搜索历史' },
  KeyU: { what: '删到行首' },
  KeyW: { what: '删除前一个词' },
  KeyY: { what: '粘回刚删掉的内容' },
  KeyZ: { what: '挂起当前进程', severe: true },
}

/** 一句人话：这个 leader 会从 shell 手里拿走什么。没有可拿的就返回空串。 */
export function leaderCostNote(binding: string): { text: string; severe: boolean } {
  if (!binding) return { text: '', severe: false }
  const parts = binding.split('+')
  const code = parts[parts.length - 1]
  const onlyCtrl = parts.length === 2 && parts[0] === 'Ctrl'
  // 只有裸 Ctrl+X 才会撞 readline；Ctrl+Shift+X / Alt+X 之类不在它的键位表里。
  if (!onlyCtrl) return { text: '', severe: false }
  const hit = READLINE_KEYS[code]
  if (!hit) return { text: '', severe: false }
  return { text: hit.what, severe: !!hit.severe }
}

export interface ShortcutsConfig {
  /** The one modifier every non-overridden action uses. */
  prefix: ShortcutPrefix
  /** Per-action explicit bindings ("Ctrl+KeyQ"). Absent = derived from `prefix`. */
  overrides: Partial<Record<ShortcutAction, string>>
  /** 两段式 leader（"Ctrl+KeyB"）。空串 = 关掉这条路，只留单修饰键前缀。 */
  leader: string
}

const STORE_KEY = 'cliTabShortcuts'

// Alt is the default because Ctrl/Cmd + digit / W / T are RESERVED by every major browser
// (switch / close / open browser tab) and a page cannot intercept them.
export const DEFAULT_SHORTCUTS_CONFIG: ShortcutsConfig = {
  prefix: 'Alt',
  overrides: {},
  leader: DEFAULT_LEADER,
}

/** The effective binding for an action: its override, else prefix + the action's default code. */
export function bindingFor(cfg: ShortcutsConfig, action: ShortcutAction): string {
  const override = cfg.overrides?.[action]
  if (override) return override
  if (action === 'switchTab') return cfg.prefix // digits are implicit (1..9)
  if (action === 'findInTerminal') return DEFAULT_FIND_IN_TERMINAL_BINDING
  return `${cfg.prefix}+${ACTION_CODES[action]}`
}

/** KeyboardEvent.code 的箭头键没有可读的印刷字符，用箭头字形代替。 */
const ARROW_GLYPH: Record<string, string> = {
  ArrowUp: '↑', ArrowDown: '↓', ArrowLeft: '←', ArrowRight: '→',
}

/**
 * "Ctrl+Shift+KeyF" → "Ctrl + Shift + F"。
 *
 * `code` 是实现细节（见 useTabShortcuts 头部：所有匹配都走物理键位），从不直接给用户看。设置页和
 * 终端里那枚搜索按钮的 tooltip 共用这一个函数，否则同一个快捷键会在两个地方被写成两种样子。
 */
export function bindingLabel(binding: string): string {
  return binding
    .split('+')
    .map((seg) => ARROW_GLYPH[seg] ?? seg.replace(/^Key/, '').replace(/^Digit/, ''))
    .join(' + ')
}

/** Whether this action still follows the global prefix (false = the user pinned it themselves). */
export function isDerived(cfg: ShortcutsConfig, action: ShortcutAction): boolean {
  return !cfg.overrides?.[action]
}

/**
 * Migrates the pre-SSOT shape ({switchModifier, nextTab: 'Alt+ArrowDown', …}) to {prefix, overrides}.
 *
 * `leader` 在旧数据里不存在。缺失 → 给默认值（这是**新功能上线**，不是用户关掉过它）；而空串是
 * 用户明确关掉的意思，必须原样留住 —— `?? DEFAULT` 会把它悄悄打开，所以这里只在「不是字符串」
 * 时才回落。
 */
function normalize(raw: unknown): ShortcutsConfig {
  if (!raw || typeof raw !== 'object') return { ...DEFAULT_SHORTCUTS_CONFIG }
  const r = raw as Record<string, unknown>
  const leader = typeof r.leader === 'string' ? r.leader : DEFAULT_LEADER
  if (typeof r.prefix === 'string') {
    return {
      prefix: r.prefix as ShortcutPrefix,
      overrides: (r.overrides && typeof r.overrides === 'object' ? r.overrides : {}) as ShortcutsConfig['overrides'],
      leader,
    }
  }
  // Legacy: keep the old modifier, drop the old per-action strings (they were all just the
  // modifier + the same default keys, so nothing personal is lost).
  const legacy = typeof r.switchModifier === 'string' ? (r.switchModifier as ShortcutPrefix) : 'Alt'
  return { prefix: legacy, overrides: {}, leader }
}

export function useShortcutsConfig() {
  const store = useServerStore()
  const config: Ref<ShortcutsConfig> = ref({ ...DEFAULT_SHORTCUTS_CONFIG })
  const loading = ref(true)

  async function load(): Promise<void> {
    loading.value = true
    await store.load().catch(() => {})
    config.value = normalize(store.get<unknown>(STORE_KEY, null))
    loading.value = false
  }

  function persist(): void {
    store.set(STORE_KEY, config.value)
  }

  /** Change the global prefix. Derived actions all follow; overridden ones stay put. */
  function setPrefix(prefix: ShortcutPrefix): void {
    config.value = { ...config.value, prefix }
    persist()
  }

  /** Pin ONE action to an explicit binding (opts it out of future prefix changes). */
  function setOverride(action: ShortcutAction, binding: string): void {
    config.value = { ...config.value, overrides: { ...config.value.overrides, [action]: binding } }
    persist()
  }

  /** Hand an action back to the global prefix. */
  function clearOverride(action: ShortcutAction): void {
    const next = { ...config.value.overrides }
    delete next[action]
    config.value = { ...config.value, overrides: next }
    persist()
  }

  /** 换 leader 键；传空串 = 关掉这条路（单修饰键前缀不受影响，两者本就独立）。 */
  function setLeader(binding: string): void {
    config.value = { ...config.value, leader: binding }
    persist()
  }

  function resetToDefaults(): void {
    config.value = { ...DEFAULT_SHORTCUTS_CONFIG, overrides: {} }
    persist()
  }

  return {
    config: computed(() => config.value),
    loading,
    load,
    setPrefix,
    setOverride,
    clearOverride,
    setLeader,
    resetToDefaults,
  }
}
