/**
 * useKeyCastrHud — KeyCastr-style keystroke visualization.
 *
 * Design:
 * - Consecutive printable ASCII chars merge into one "word" entry (500ms window)
 * - Modifier combos shown with Apple symbols: ⌘⇧⌥⌃
 * - Special keys shown as symbols: ⏎ ⇥ ⎋ ⌫ ← → ↑ ↓ ␣
 * - Each entry lives 2s then fades out (CSS transition handles the visual)
 * - Max 5 visible entries; oldest evicted when limit reached
 */
import { ref, readonly } from 'vue'

export interface KeyCastrEntry {
  id: number
  display: string
  createdAt: number
}

const MERGE_WINDOW_MS = 500
const ENTRY_LIFETIME_MS = 2000
const MAX_ENTRIES = 5

// Maps KeyboardEvent.key → display symbol for special keys.
const SPECIAL_KEY_MAP: Record<string, string> = {
  Enter: '⏎',
  Tab: '⇥',
  Escape: '⎋',
  Backspace: '⌫',
  Delete: '⌦',
  ArrowUp: '↑',
  ArrowDown: '↓',
  ArrowLeft: '←',
  ArrowRight: '→',
  ' ': '␣',
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
  let mergeBuffer = ''
  let mergeTimerId: ReturnType<typeof setTimeout> | null = null
  let lastKeyTime = 0

  function flushMergeBuffer() {
    if (mergeBuffer === '') return
    if (mergeTimerId !== null) { clearTimeout(mergeTimerId); mergeTimerId = null }
    pushEntry(mergeBuffer)
    mergeBuffer = ''
  }

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
    // Ignore pure modifier presses and IME composition.
    if (['Shift', 'Control', 'Alt', 'Meta'].includes(e.key)) return
    if (e.isComposing || e.key === 'Process' || e.key === 'Dead') return
    // Ignore keystrokes in non-terminal input fields (auth dialog, tab rename, etc.).
    // But NOT the xterm helper textarea (class xterm-helper-textarea) which is the terminal input proxy.
    // The CLI compose textarea is also a terminal input path, so it should still drive the HUD.
    const el = e.target as HTMLElement
    if (el?.tagName === 'INPUT' || el?.isContentEditable) return
    if (el?.tagName === 'TEXTAREA'
      && !el.classList?.contains('xterm-helper-textarea')
      && !el.classList?.contains('compose-input')) return

    const now = performance.now()
    const hasModifier = e.ctrlKey || e.altKey || e.metaKey
    const isSpecial = e.key.length > 1 || e.key === ' '
    const prefix = modifierPrefix(e)

    if (hasModifier || isSpecial) {
      // Flush any pending text buffer first.
      flushMergeBuffer()
      // Build display: modifiers + key symbol.
      const keyDisplay = SPECIAL_KEY_MAP[e.key] ?? e.key.toUpperCase()
      pushEntry(prefix + keyDisplay)
    } else {
      // Printable single char — merge into buffer.
      const gap = now - lastKeyTime
      if (gap > MERGE_WINDOW_MS && mergeBuffer !== '') {
        flushMergeBuffer()
      }
      mergeBuffer += e.key
      lastKeyTime = now

      // Reset merge timer.
      if (mergeTimerId !== null) clearTimeout(mergeTimerId)
      mergeTimerId = setTimeout(flushMergeBuffer, MERGE_WINDOW_MS)
    }
  }

  function clear() {
    entries.value = []
    mergeBuffer = ''
    if (mergeTimerId !== null) { clearTimeout(mergeTimerId); mergeTimerId = null }
  }

  return {
    entries: readonly(entries),
    enabled,
    feed,
    clear,
  }
}
