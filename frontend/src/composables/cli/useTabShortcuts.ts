/**
 * useTabShortcuts — single-key-direct (NOT tmux prefix+key) shortcut dispatch for the CLI tab
 * strip, shared between standalone (deepwork-terminal) and pro (deepwork-pro via @terminal).
 *
 * ── Why every binding matches on `code`, never `key` ──────────────────────────────────────────
 * The first cut compared `e.key`, and the whole keymap silently did nothing on macOS: Option is a
 * COMPOSE modifier there, so Option+1 delivers `key:"¡"`, Option+N `"˜"`, Option+W `"∑"`,
 * Option+R `"®"` — none of which match "1"/"n"/"w"/"r". Non-Latin layouts break it the same way.
 * `code` is physical-key identity ("Digit1", "KeyN") and is immune to both.
 *
 * This also invalidated an earlier "verified in a browser" claim: CDP/synthetic events carry
 * `key:"1"` + `altKey:true`, so automation passes while a real keyboard fails. Synthetic events
 * can verify the DISPATCH LOGIC below; only a human on a real keyboard can verify the binding.
 *
 * Registers a CAPTURE-phase document keydown listener, gated by `adapter.isActive()` — that is
 * what lets pro scope it to "only while the CLI portal route is active", so it wins over pro's own
 * global Alt+1/2/3 binding (WindowDockOverlay, bubble phase) instead of racing it.
 */
import { onBeforeUnmount, onMounted } from 'vue'
import { computeVisibleTabOrder } from './useVisibleTabOrder'
import {
  useShortcutsConfig,
  bindingFor,
  type ShortcutsConfig,
  type ShortcutAction,
} from './useShortcutsConfig'

export interface TabShortcutsAdapter {
  /** Visible tab order, already flattened by the caller (groups respected on the standalone side). */
  orderedTabIds: () => string[]
  activeTabId: () => string | undefined
  /** Only dispatch while true — e.g. this CLI surface is the one currently mounted/focused. */
  isActive: () => boolean
  onSelect: (id: string) => void
  onNew: () => void
  onClose: (id: string) => void
  onRename: (id: string) => void
}

interface ParsedBinding {
  alt: boolean
  ctrl: boolean
  shift: boolean
  meta: boolean
  /** KeyboardEvent.code, e.g. "KeyN" / "ArrowUp". '' for a modifier-only binding (the digit family). */
  code: string
}

/** Parses "Alt+KeyW" / "Ctrl+ArrowUp" / "Alt" into modifier flags + a physical code. */
export function parseBinding(binding: string): ParsedBinding {
  const parsed: ParsedBinding = { alt: false, ctrl: false, shift: false, meta: false, code: '' }
  for (const part of binding.split('+').map((p) => p.trim())) {
    const lower = part.toLowerCase()
    if (lower === 'alt') parsed.alt = true
    else if (lower === 'ctrl' || lower === 'control') parsed.ctrl = true
    else if (lower === 'shift') parsed.shift = true
    else if (lower === 'meta' || lower === 'cmd') parsed.meta = true
    else parsed.code = part
  }
  return parsed
}

/** Every modifier flag matches exactly (so Alt+Ctrl+W never fires a plain Alt+W binding). */
function modifiersMatch(e: KeyboardEvent, p: ParsedBinding): boolean {
  return e.altKey === p.alt && e.ctrlKey === p.ctrl && e.shiftKey === p.shift && e.metaKey === p.meta
}

/** Whether a keydown matches a full binding ("Alt+KeyW"), comparing PHYSICAL code. */
export function matchesBinding(e: KeyboardEvent, binding: string): boolean {
  const p = parseBinding(binding)
  return modifiersMatch(e, p) && !!p.code && e.code === p.code
}

/**
 * Whether a keydown is <prefix> + a digit 1-9, returning the digit.
 *
 * Reads `e.code` ("Digit1".."Digit9"), so macOS Option+1 (which prints "¡") still resolves to 1.
 * Digit0 is deliberately excluded — there is no "tab 0".
 */
export function matchesPrefixDigit(e: KeyboardEvent, prefix: string): number | undefined {
  const p = parseBinding(prefix)
  if (!modifiersMatch(e, p)) return undefined
  const m = /^Digit([1-9])$/.exec(e.code)
  return m ? Number(m[1]) : undefined
}

/** Pure dispatch: given a config + a snapshot of tab state, what action does this keydown mean? */
export function resolveShortcutAction(
  e: KeyboardEvent,
  cfg: ShortcutsConfig,
  orderedIds: string[],
  activeId: string | undefined,
): { type: 'select'; id: string } | { type: 'new' | 'close' | 'rename' } | null {
  const order = computeVisibleTabOrder(orderedIds)
  const b = (action: ShortcutAction): string => bindingFor(cfg, action)

  const digit = matchesPrefixDigit(e, b('switchTab'))
  if (digit !== undefined) {
    const id = order.idAtPosition(digit)
    return id ? { type: 'select', id } : null
  }
  if (matchesBinding(e, b('nextTab'))) {
    const id = activeId ? order.nextId(activeId) : orderedIds[0]
    return id ? { type: 'select', id } : null
  }
  if (matchesBinding(e, b('prevTab'))) {
    const id = activeId ? order.prevId(activeId) : orderedIds[orderedIds.length - 1]
    return id ? { type: 'select', id } : null
  }
  if (matchesBinding(e, b('newTab'))) return { type: 'new' }
  if (matchesBinding(e, b('closeTab'))) return activeId ? { type: 'close' } : null
  if (matchesBinding(e, b('renameTab'))) return activeId ? { type: 'rename' } : null
  return null
}

export function useTabShortcuts(adapter: TabShortcutsAdapter): void {
  const { config, load } = useShortcutsConfig()

  function handleKeydown(e: KeyboardEvent): void {
    if (!adapter.isActive()) return
    const orderedIds = adapter.orderedTabIds()
    if (orderedIds.length === 0) return
    const action = resolveShortcutAction(e, config.value, orderedIds, adapter.activeTabId())
    if (!action) return

    e.preventDefault()
    e.stopImmediatePropagation()

    switch (action.type) {
      case 'select': adapter.onSelect(action.id); break
      case 'new': adapter.onNew(); break
      case 'close': { const id = adapter.activeTabId(); if (id) adapter.onClose(id); break }
      case 'rename': { const id = adapter.activeTabId(); if (id) adapter.onRename(id); break }
    }
  }

  onMounted(() => {
    void load()
    document.addEventListener('keydown', handleKeydown, { capture: true })
  })
  onBeforeUnmount(() => {
    document.removeEventListener('keydown', handleKeydown, { capture: true })
  })
}
