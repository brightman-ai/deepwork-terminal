import { describe, it, expect, afterEach } from 'bun:test'

/**
 * renameTab used to be a purely local `tab.name = x` mutation — nothing told the
 * backend session its new name, so every backend consumer (the notifier's
 * identityTag/notification title, GET /sessions) kept showing the pre-rename name
 * forever. These lock in the sync call's conditional shape: fires for a connected
 * LOCAL tab, skipped for a remote tab (its session lives on a different host/auth)
 * or a not-yet-connected tab (no sessionId to target).
 *
 * useCliAuth.ts reads `window.location`/`localStorage` at MODULE LOAD TIME (its
 * ?auth= bootstrap), so this project has never had DOM-free unit coverage for
 * anything importing it. A dynamic import() after a minimal global shim (instead
 * of a static import, which the JS spec hoists above any code that could set the
 * shim first) keeps that scoped to this one file rather than adding a project-wide
 * DOM test dependency for one small addition.
 */
const realFetch = globalThis.fetch

function installDomShim(): void {
  const store = new Map<string, string>()
  ;(globalThis as any).localStorage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  }
  ;(globalThis as any).window = {
    location: { search: '', pathname: '/', hash: '' },
    history: { replaceState: () => {} },
  }
}

function captureFetch(): { calls: Array<{ url: string; body: unknown }> } {
  const state = { calls: [] as Array<{ url: string; body: unknown }> }
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    state.calls.push({ url: String(input), body: init?.body ? JSON.parse(String(init.body)) : undefined })
    return { ok: true, status: 204, json: async () => ({}) } as Response
  }) as unknown as typeof fetch
  return state
}

afterEach(() => {
  globalThis.fetch = realFetch
})

installDomShim()
const { useWorkbench } = await import('../useWorkbench')

describe('useWorkbench renameTab — backend sync', () => {
  it('POSTs the new name to /sessions/{id}/rename for a connected local tab', () => {
    const fetches = captureFetch()
    const wb = useWorkbench()
    const group = wb.addGroup('g')
    const tab = wb.addTab(group.id, { name: 'stwork' })
    wb.bindSession(tab.id, 'sess-123')

    wb.renameTab(tab.id, 'agent-memory')

    expect(tab.name).toBe('agent-memory')
    expect(fetches.calls.length).toBe(1)
    expect(fetches.calls[0].url).toContain('/sessions/sess-123/rename')
    expect(fetches.calls[0].body).toEqual({ name: 'agent-memory' })
  })

  it('does NOT sync a remote tab — its session lives on a different host', () => {
    const fetches = captureFetch()
    const wb = useWorkbench()
    const group = wb.addGroup('g')
    const tab = wb.addTab(group.id, { name: 'remote-1', remotePeerId: 'peer-stmac' })
    wb.bindSession(tab.id, 'sess-456')

    wb.renameTab(tab.id, 'renamed')

    expect(tab.name).toBe('renamed') // local label still updates
    expect(fetches.calls.length).toBe(0) // but nothing fired at any backend
  })

  it('does NOT sync a not-yet-connected tab — no sessionId to target', () => {
    const fetches = captureFetch()
    const wb = useWorkbench()
    const group = wb.addGroup('g')
    const tab = wb.addTab(group.id, { name: 'fresh' })
    // tab.sessionId left unset — matches a brand-new tab before its PTY connects

    wb.renameTab(tab.id, 'renamed')

    expect(tab.name).toBe('renamed')
    expect(fetches.calls.length).toBe(0)
  })
})
