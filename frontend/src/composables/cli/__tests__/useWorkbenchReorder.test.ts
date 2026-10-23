import { afterEach, describe, expect, it } from 'bun:test'
import type { WorkbenchConfig } from '../../../types/workbench'

// Auth bootstraps at import time. Like useWorkbenchRename.test.ts, provide only
// its browser storage boundary; all persistence below uses the real API adapter.
const realFetch = globalThis.fetch
const store = new Map<string, string>()
;(globalThis as any).localStorage ??= {
  getItem: (key: string) => store.get(key) ?? null,
  setItem: (key: string, value: string) => { store.set(key, value) },
  removeItem: (key: string) => { store.delete(key) },
}
;(globalThis as any).window ??= { location: { search: '', pathname: '/', hash: '' }, history: { replaceState() {} } }
const { useWorkbench } = await import('../useWorkbench')
afterEach(() => { globalThis.fetch = realFetch })

const clone = <T>(data: T): T => JSON.parse(JSON.stringify(data)) as T
function config(ids = ['a', 'b', 'c'], rev = 1): WorkbenchConfig {
  return { groups: [{ id: 'g', name: 'group', collapsed: false,
    tabs: ids.map(id => ({ id, name: id, cwd: '/', engine: 'shell', groupId: 'g', sessionId: `S-${id}` })) }],
    activeTabId: 'b', activeGroupId: 'g', lastSaved: '', rev }
}
const ids = (cfg: WorkbenchConfig | null) => cfg?.groups.flatMap(g => g.tabs.map(t => t.id))
const front = { groupId: 'g', tabId: 'c', beforeTabId: 'a' }
const response = (status: number, body: unknown) => ({ ok: status >= 200 && status < 300, status, json: async () => clone(body) }) as Response

function backend() {
  const state = { config: config(), puts: [] as WorkbenchConfig[], fail: false,
    intercept: null as null | ((init: RequestInit) => Promise<Response> | undefined) }
  globalThis.fetch = (async (_input: unknown, init?: RequestInit) => {
    if (init?.method !== 'PUT') return response(200, state.config)
    const next = JSON.parse(String(init.body)) as WorkbenchConfig
    state.puts.push(next)
    const override = state.intercept?.(init)
    if (override) return override
    if (state.fail) throw new Error('offline')
    if (next.rev !== state.config.rev) return response(409, state.config)
    state.config = { ...clone(next), rev: (state.config.rev ?? 0) + 1 }
    return response(200, { rev: state.config.rev })
  }) as typeof fetch
  return state
}

describe('useWorkbench reorder persistence and revision races', () => {
  it('saves/reloads a drop without changing focus, binding, or terminal objects', async () => {
    const server = backend()
    const wb = useWorkbench()
    await wb.load()
    const original = wb.groups.value[0]!.tabs[2]
    wb.reorderTabs([front])
    expect(wb.groups.value[0]!.tabs[0]).toBe(original)
    expect(wb.activeTab.value?.id).toBe('b')
    expect(server.puts).toHaveLength(0)
    await wb.retrySave()
    expect(ids(server.config)).toEqual(['c', 'a', 'b'])
    expect(server.puts).toHaveLength(1)
    const reloaded = useWorkbench()
    await reloaded.load()
    expect(ids(reloaded.config.value)).toEqual(['c', 'a', 'b'])
    expect(reloaded.allTabs.value.map(t => t.sessionId)).toEqual(['S-c', 'S-a', 'S-b'])
  })
  it('rebases over remote additions/rename and an independent browser keeps the saved order', async () => {
    const server = backend()
    const wb = useWorkbench(), stale = useWorkbench()
    await wb.load()
    await stale.load()
    wb.reorderTabs([front])
    server.config = config(['a', 'new', 'b', 'c'], 2)
    server.config.groups[0]!.tabs[0]!.name = 'remote rename'
    await wb.retrySave()
    expect(ids(server.config)).toEqual(['c', 'a', 'new', 'b'])
    expect(wb.allTabs.value.find(t => t.id === 'a')?.name).toBe('remote rename')
    stale.setTabCwd('b', '/new-cwd')
    await stale.retrySave()
    expect(ids(server.config)).toEqual(['c', 'a', 'new', 'b'])
    expect(server.config.groups[0]!.tabs.find(t => t.id === 'b')?.cwd).toBe('/new-cwd')
    await wb.resync()
    expect(ids(wb.config.value)).toEqual(ids(server.config))
  })
  it('keeps pending intent through resync and never resurrects a closed source', async () => {
    const server = backend()
    const wb = useWorkbench()
    await wb.load()
    wb.reorderTabs([front])
    server.config = config(['a', 'new', 'b', 'c'], 2)
    await wb.resync()
    expect(ids(wb.config.value)).toEqual(['c', 'a', 'new', 'b'])
    server.config = config(['a', 'new', 'b'], 3)
    await wb.retrySave()
    expect(ids(server.config)).toEqual(['a', 'new', 'b'])
  })
  it('failed writes keep the live UI and intent, and retry applies the move over newer data', async () => {
    const server = backend()
    const wb = useWorkbench()
    await wb.load()
    wb.reorderTabs([front])
    server.fail = true
    await wb.retrySave()
    expect(wb.error.value).toBeNull()
    expect(wb.saveError.value).toBe('offline')
    expect(ids(wb.config.value)).toEqual(['c', 'a', 'b'])
    server.config = config(['a', 'new', 'b', 'c'], 2)
    server.fail = false
    await wb.retrySave()
    expect(wb.saveError.value).toBeNull()
    expect(ids(server.config)).toEqual(['c', 'a', 'new', 'b'])
  })
  it('acknowledges only the outgoing moves when another drop occurs during the write', async () => {
    const server = backend()
    const wb = useWorkbench()
    await wb.load()
    let release!: (value: Response) => void
    server.intercept = () => new Promise<Response>(resolve => { release = resolve })
    wb.reorderTabs([front])
    const saving = wb.retrySave()
    wb.reorderTabs([{ groupId: 'g', tabId: 'a', beforeTabId: null }])
    await wb.retrySave() // queue behind the first write
    server.intercept = null
    server.config = { ...server.puts[0]!, rev: 2 }
    release(response(200, { rev: 2 }))
    await saving
    expect(ids(server.config)).toEqual(['c', 'b', 'a'])
    expect(server.puts).toHaveLength(2)
    // A later remote move must not be overwritten by the already acknowledged first drop.
    server.config = config(['a', 'c', 'b'], 4)
    await wb.resync()
    expect(ids(wb.config.value)).toEqual(['a', 'c', 'b'])
  })
})
