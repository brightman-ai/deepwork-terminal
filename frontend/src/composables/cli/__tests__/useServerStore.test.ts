import { describe, it, expect, mock } from 'bun:test'

// Mock the store API so we can simulate a server-down GET (the restart race) and spy on saves.
const saveSpy = mock((_d: unknown) => Promise.resolve())
let failFetch = true
mock.module('@terminal/api/store', () => ({
  fetchStore: () => (failFetch ? Promise.reject(new Error('server down')) : Promise.resolve({ history: ['a'] })),
  saveStore: (d: unknown) => saveSpy(d),
}))

const { useServerStore } = await import('@terminal/composables/cli/useServerStore')

// NOTE: useServerStore is a module-level singleton, so these two tests share state and run in
// order — test 1 leaves it un-hydrated (load kept failing), test 2 flips the server up and hydrates.
describe('useServerStore — hydration gate (restart data-loss guard)', () => {
  it('un-hydrated (load failed) → set() does NOT persist, so a partial data can never overwrite the server', async () => {
    const s = useServerStore()
    await s.load().catch(() => {}) // fails → stays un-hydrated
    s.set('history', ['new'])
    expect(saveSpy).not.toHaveBeenCalled()
  })

  it('after a successful load → set() persists normally', async () => {
    failFetch = false
    const s = useServerStore()
    await s.load() // succeeds → hydrated
    s.set('history', ['a', 'b'])
    await new Promise((r) => setTimeout(r, 550)) // past the 500ms debounce
    expect(saveSpy).toHaveBeenCalled()
  })
})
