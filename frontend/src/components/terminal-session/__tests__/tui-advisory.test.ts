import { afterAll, describe, expect, test, beforeEach } from 'bun:test'

// In-memory localStorage stub (jsdom-free) so the mute/remember logic is exercised
// deterministically. Mute persists in localStorage so it survives revisits (no re-nag).
const store = new Map<string, string>()

// Snapshot whatever this process had before we stub globals here, so we can put things
// back exactly as we found them once this file's tests are done. Without this,
// `Object.defineProperty` (unlike a plain assignment) defaults to `writable: false`, so
// any later test file in the same bun process that does `globalThis.localStorage = ...`
// (a plain assignment) throws "Attempted to assign to readonly property" — this file's
// stub otherwise leaks across test files.
const originalLocalStorage = (globalThis as any).localStorage

Object.defineProperty(globalThis, 'localStorage', {
  value: {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v) },
    removeItem: (k: string) => { store.delete(k) },
  },
  configurable: true,
  writable: true,
})

afterAll(() => {
  // `delete` (not reassignment) is required to drop the non-writable property
  // descriptor installed above before restoring the prior value.
  delete (globalThis as any).localStorage
  if (originalLocalStorage !== undefined) (globalThis as any).localStorage = originalLocalStorage
})

import { useTuiAdvisory } from '../../../composables/cli/useTuiAdvisory'

describe('useTuiAdvisory', () => {
  beforeEach(() => store.clear())

  test('fullscreen → prompt; back to normal → hidden', () => {
    const a = useTuiAdvisory()
    expect(a.state.value).toBe('hidden')
    a.noteFullscreen(true)
    expect(a.state.value).toBe('prompt')
    a.noteFullscreen(false)
    expect(a.state.value).toBe('hidden')
  })

  test('defer collapses + mutes; re-entering fullscreen stays collapsed (no re-nag)', () => {
    const a = useTuiAdvisory()
    a.noteFullscreen(true)
    a.defer()
    expect(a.state.value).toBe('collapsed')
    a.noteFullscreen(false)
    a.noteFullscreen(true) // muted → must NOT pop the prompt again
    expect(a.state.value).toBe('collapsed')
  })

  test('reopen brings the sheet back from the collapsed entry', () => {
    const a = useTuiAdvisory()
    a.noteFullscreen(true)
    a.defer()
    a.reopen()
    expect(a.state.value).toBe('prompt')
  })

  test('resolved hides + mutes so it will not auto-prompt again', () => {
    const a = useTuiAdvisory()
    a.noteFullscreen(true)
    a.resolved()
    expect(a.state.value).toBe('hidden')
    a.noteFullscreen(false)
    a.noteFullscreen(true)
    expect(a.state.value).toBe('collapsed') // muted, available via entry but not auto-popped
  })

  test('mute PERSISTS across a fresh instance (revisit) → no re-nag', () => {
    // First visit: user defers.
    useTuiAdvisory().defer()
    // Revisit: a brand-new composable instance (store NOT cleared) must stay muted →
    // entering fullscreen collapses to the entry instead of re-popping the prompt.
    const revisit = useTuiAdvisory()
    revisit.noteFullscreen(true)
    expect(revisit.state.value).toBe('collapsed')
  })
})
