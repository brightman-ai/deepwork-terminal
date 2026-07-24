/**
 * useKeyCastrHud — KeyCastr-style keystroke visualization.
 *
 * Projection criterion (SSOT, request.md §8.3): a key projects into the HUD only if "the
 * terminal alone doesn't show you pressed it" — see `shouldProjectKeystroke`. Printable chars
 * (incl. IME commit), Space, Enter and Backspace all have an immediate visible terminal trace, so
 * they are never projected (that used to be the whole point of the "merge into a word" buffer —
 * removed, since there is nothing left to merge once printable keys are filtered out upstream).
 *
 * Design:
 * - Modifier combos shown with Apple symbols: ⌘⇧⌥⌃
 * - Non-printable keys that DO project are shown as symbols: ⇥ ⎋ ⌦ ← → ↑ ↓ ⇱ ⇲ ⇞ ⇟
 * - Each entry lives 2s then fades out (CSS transition handles the visual)
 * - Max 5 visible entries; oldest evicted when limit reached
 */
import { ref, readonly } from 'vue'

export interface KeyCastrEntry {
  id: number
  display: string
  createdAt: number
}

/** Minimal duck-typed shape `shouldProjectKeystroke` needs — a real KeyboardEvent satisfies it. */
export interface KeystrokeLike {
  key: string
  ctrlKey?: boolean
  altKey?: boolean
  metaKey?: boolean
  shiftKey?: boolean
  isComposing?: boolean
}

const ENTRY_LIFETIME_MS = 2000
const MAX_ENTRIES = 5

// Keys with an immediate, visible terminal trace (char appears / cursor moves / line breaks /
// char erased) — projecting them would be pure redundancy. Printable single chars (incl. IME
// commit results) are handled separately below, by `key.length === 1`.
const NON_PROJECTING_NAMED_KEYS = new Set(['Enter', 'Backspace', ' '])

// Keys with NO visible terminal trace of their own — these are exactly what KeyCastr is for.
const PROJECTING_NAMED_KEYS = new Set([
  'Escape', 'Tab',
  'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight',
  'Home', 'End', 'PageUp', 'PageDown',
  'Insert', 'Delete',
  'F1', 'F2', 'F3', 'F4', 'F5', 'F6', 'F7', 'F8', 'F9', 'F10', 'F11', 'F12',
])

/**
 * Projection judgment, pure + unit-testable (request.md §8.3 table). Order matters:
 * 1. A lone modifier press (key itself IS the modifier) — nothing happened yet, don't project.
 * 2. IME composition in progress, or its commit event — the composed text lands in the terminal
 *    either way, so there is nothing invisible about it.
 * 2b. A key the ENGINE could not name (`Unidentified`, or an empty `key`). This is the same
 *    family as rule 2 — the browser is telling us it does not know what was pressed — and it is
 *    common on Android soft keyboards / IME paths, which fire it while ordinary typing. The rule-6
 *    fallback would render it as the literal pill `UNIDENTIFIED`, which names no key and so tells
 *    the user strictly nothing: it fails the SSOT criterion in the other direction (the criterion
 *    is "project what you cannot SEE", not "project what we cannot IDENTIFY"). Rejected BEFORE the
 *    chord rule on purpose: `⌃UNIDENTIFIED` is no more informative than `UNIDENTIFIED`, and on the
 *    soft-keyboard paths that emit it, letting a chord through would re-open exactly the mobile
 *    spam this whole filter exists to kill.
 * 3. Any modifier chord (Ctrl/Alt/Meta + X, incl. tmux prefix chords like Ctrl-B) — always
 *    projects, regardless of which key it's chorded with (a chord's effect is rarely visible).
 * 4. Named keys explicitly known to be visible (Enter/Backspace/Space) or invisible (Esc, Tab,
 *    arrows, Home/End/PgUp/PgDn, Insert/Delete, F1–F12) — table lookup.
 * 5. A bare printable character (`key.length === 1`, covers letters/digits/symbols and IME commit
 *    results) — visible in the terminal immediately, don't project.
 * 6. Anything else unmapped (CapsLock, ContextMenu, Pause, ...) — no known visible trace, so err
 *    toward showing it rather than silently swallowing an unrecognized key.
 */
export function shouldProjectKeystroke(e: KeystrokeLike): boolean {
  if (['Shift', 'Control', 'Alt', 'Meta'].includes(e.key)) return false
  if (e.isComposing || e.key === 'Process' || e.key === 'Dead') return false
  if (!e.key || e.key === 'Unidentified') return false
  if (e.ctrlKey || e.altKey || e.metaKey) return true
  if (NON_PROJECTING_NAMED_KEYS.has(e.key)) return false
  if (PROJECTING_NAMED_KEYS.has(e.key)) return true
  if (e.key.length === 1) return false
  return true
}

// Maps KeyboardEvent.key → display symbol for keys that DO project (see PROJECTING_NAMED_KEYS).
const SPECIAL_KEY_MAP: Record<string, string> = {
  Tab: '⇥',
  Escape: '⎋',
  Delete: '⌦',
  ArrowUp: '↑',
  ArrowDown: '↓',
  ArrowLeft: '←',
  ArrowRight: '→',
  Home: '⇱',
  End: '⇲',
  PageUp: '⇞',
  PageDown: '⇟',
}

// Modifier symbols in Apple HIG order: Control, Option, Shift, Command.
function modifierPrefix(e: KeyboardEvent): string {
  let prefix = ''
  if (e.ctrlKey) prefix += '⌃'
  if (e.altKey) prefix += '⌥'
  if (e.shiftKey) prefix += '⇧'
  if (e.metaKey) prefix += '⌘'
  return prefix
}

export function useKeyCastrHud() {
  const entries = ref<KeyCastrEntry[]>([])
  const enabled = ref(true)
  let nextId = 0

  function pushEntry(display: string) {
    const entry: KeyCastrEntry = { id: ++nextId, display, createdAt: Date.now() }
    entries.value = [...entries.value, entry]
    if (entries.value.length > MAX_ENTRIES) {
      entries.value = entries.value.slice(-MAX_ENTRIES)
    }
    // Schedule auto-removal.
    const entryId = entry.id
    setTimeout(() => {
      entries.value = entries.value.filter(e => e.id !== entryId)
    }, ENTRY_LIFETIME_MS)
  }

  function feed(e: KeyboardEvent) {
    if (!enabled.value) return
    // Ignore keystrokes in non-terminal input fields (auth dialog, tab rename, etc.).
    // But NOT the xterm helper textarea (class xterm-helper-textarea) which is the terminal input proxy.
    // The CLI compose textarea is also a terminal input path, so it should still drive the HUD.
    const el = e.target as HTMLElement
    if (el?.tagName === 'INPUT' || el?.isContentEditable) return
    if (el?.tagName === 'TEXTAREA'
      && !el.classList?.contains('xterm-helper-textarea')
      && !el.classList?.contains('compose-input')) return

    if (!shouldProjectKeystroke(e)) return

    const keyDisplay = SPECIAL_KEY_MAP[e.key] ?? e.key.toUpperCase()
    pushEntry(modifierPrefix(e) + keyDisplay)
  }

  function clear() {
    entries.value = []
  }

  return {
    entries: readonly(entries),
    enabled,
    feed,
    clear,
  }
}
