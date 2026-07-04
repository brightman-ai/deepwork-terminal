/**
 * useTuiAdvisory — drives the "Claude is in fullscreen mode" advisory.
 *
 * Claude Code's `tui: fullscreen` renders to the terminal's ALTERNATE screen buffer, which breaks
 * tmux copy-mode, scroll-to-copy, and native web-terminal text selection (the content never lands in
 * a selectable/scrollback buffer). The root fix is to flip Claude to `classic`/`inline` (normal
 * buffer) — done live via the `/tui default` keystroke, optionally persisted to settings.json.
 *
 * This composable is a tiny state machine fed by the terminal's buffer-type changes:
 *   hidden    — not in fullscreen (or already resolved)
 *   prompt    — fullscreen detected, show the advisory sheet
 *   collapsed — user deferred; a small entry sits in the toolbar cluster, re-openable
 *
 * Deferring or resolving MUTES auto-prompting PERSISTENTLY (localStorage): once the user has dealt
 * with this — deferred it or switched to classic — it must NOT re-nag on every revisit (the old
 * sessionStorage scope re-prompted each new tab/session, which read as a bug). Still discoverable:
 * while in fullscreen a muted user sees the small collapsed toolbar entry, reopenable on demand.
 */
import { ref } from 'vue'

export type TuiAdvisoryState = 'hidden' | 'prompt' | 'collapsed'

const MUTE_KEY = 'tui_advisory_muted'

export function useTuiAdvisory() {
  const state = ref<TuiAdvisoryState>('hidden')

  function muted(): boolean {
    try { return localStorage.getItem(MUTE_KEY) === '1' } catch { return false }
  }
  function mute(): void {
    try { localStorage.setItem(MUTE_KEY, '1') } catch { /* private mode: best-effort */ }
  }

  /** Called on every terminal buffer-type change. alt = Claude entered fullscreen. */
  function noteFullscreen(isAlternate: boolean): void {
    if (isAlternate) {
      if (state.value === 'hidden') state.value = muted() ? 'collapsed' : 'prompt'
    } else {
      // Back to the normal buffer → Claude left fullscreen (switched to classic / exited). Resolved.
      state.value = 'hidden'
    }
  }

  /** Reopen the sheet from the collapsed toolbar entry. */
  function reopen(): void {
    if (state.value === 'collapsed') state.value = 'prompt'
  }

  /** User chose "later": collapse to the toolbar entry and stop auto-prompting this session. */
  function defer(): void {
    state.value = 'collapsed'
    mute()
  }

  /** The advisory was acted on (switched to classic): hide it and stop nagging. */
  function resolved(): void {
    state.value = 'hidden'
    mute()
  }

  return { state, noteFullscreen, reopen, defer, resolved }
}
