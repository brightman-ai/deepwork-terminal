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
  | 'switchTab' | 'prevTab' | 'nextTab' | 'newTab' | 'closeTab' | 'renameTab'
  | 'findInTerminal'

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
  renameTab: 'KeyR',
}

// findInTerminal's default is deliberately NOT `prefix + code`: it must match the OS/browser's
// idiomatic "find" gesture (Cmd+F on macOS) regardless of whatever prefix the user picked for tab
// switching — a user who set prefix=Ctrl still expects Cmd+F to open search on their Mac. Ctrl+F
// alone is reserved (readline/vim forward-char inside the shell), so the non-Mac default adds
// Shift so a bare Ctrl+F always reaches the PTY untouched.
const IS_MAC_PLATFORM = typeof navigator !== 'undefined'
  && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '')
const DEFAULT_FIND_IN_TERMINAL_BINDING = IS_MAC_PLATFORM ? 'Meta+KeyF' : 'Ctrl+Shift+KeyF'

export interface ShortcutsConfig {
  /** The one modifier every non-overridden action uses. */
  prefix: ShortcutPrefix
  /** Per-action explicit bindings ("Ctrl+KeyQ"). Absent = derived from `prefix`. */
  overrides: Partial<Record<ShortcutAction, string>>
}

const STORE_KEY = 'cliTabShortcuts'

// Alt is the default because Ctrl/Cmd + digit / W / T are RESERVED by every major browser
// (switch / close / open browser tab) and a page cannot intercept them.
export const DEFAULT_SHORTCUTS_CONFIG: ShortcutsConfig = { prefix: 'Alt', overrides: {} }

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

/** Migrates the pre-SSOT shape ({switchModifier, nextTab: 'Alt+ArrowDown', …}) to {prefix, overrides}. */
function normalize(raw: unknown): ShortcutsConfig {
  if (!raw || typeof raw !== 'object') return { ...DEFAULT_SHORTCUTS_CONFIG }
  const r = raw as Record<string, unknown>
  if (typeof r.prefix === 'string') {
    return {
      prefix: r.prefix as ShortcutPrefix,
      overrides: (r.overrides && typeof r.overrides === 'object' ? r.overrides : {}) as ShortcutsConfig['overrides'],
    }
  }
  // Legacy: keep the old modifier, drop the old per-action strings (they were all just the
  // modifier + the same default keys, so nothing personal is lost).
  const legacy = typeof r.switchModifier === 'string' ? (r.switchModifier as ShortcutPrefix) : 'Alt'
  return { prefix: legacy, overrides: {} }
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
    resetToDefaults,
  }
}
