import { ref, computed, onMounted, reactive, nextTick } from 'vue'
import { useWorkbench } from '@/composables/cli/useWorkbench'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { cliApi } from '@/composables/cli/useCliApiPrefix'
import type { AgentState, WSConnectionStatus } from '@/types/terminal'
import type { NetStats } from '@/composables/cli/useWebSocketClient'
import type { WorkbenchTab } from '@/types/workbench'
import CliTerminalSurface from '@/components/terminal-session/CliTerminalSurface.vue'
import type { PortalRuntimeResult } from '@ce/composables/layout/usePortalRuntime'

interface TabRuntime {
  agentState: AgentState | null
  agentNotifications: AgentState[]
  wsStatus: WSConnectionStatus
}

export function useCliState(runtime: PortalRuntimeResult) {
  const { scenario, breakpoint } = runtime
  const { cliFetch } = useCliAuth()

  const {
    loading, error, groups, activeTab, allTabs, showGroupHeaders,
    load, addTab, removeTab, renameTab, setActiveTab,
    toggleGroupCollapsed, bindSession, unbindSession,
  } = useWorkbench()

  // ─── Per-tab runtime state ────────────────────────────────────────────────────
  const tabRuntimes = reactive<Record<string, TabRuntime>>({})

  function ensureRuntime(tabId: string): TabRuntime {
    if (!tabRuntimes[tabId]) {
      tabRuntimes[tabId] = { agentState: null, agentNotifications: [], wsStatus: 'disconnected' }
    }
    return tabRuntimes[tabId]
  }

  const surfaceRefs = reactive<Record<string, InstanceType<typeof CliTerminalSurface> | null>>({})

  function registerSurface(tabId: string, el: InstanceType<typeof CliTerminalSurface> | null) {
    surfaceRefs[tabId] = el
  }

  // ─── Active tab derived state ─────────────────────────────────────────────────
  const activeWsStatus = computed<WSConnectionStatus>(() =>
    activeTab.value ? (tabRuntimes[activeTab.value.id]?.wsStatus ?? 'disconnected') : 'disconnected',
  )
  const activeAgentState = computed<AgentState | null>(() =>
    activeTab.value ? (tabRuntimes[activeTab.value.id]?.agentState ?? null) : null,
  )
  const activeAgentNotifications = computed<AgentState[]>(() =>
    activeTab.value ? (tabRuntimes[activeTab.value.id]?.agentNotifications ?? []) : [],
  )
  const activeNetStats = computed<NetStats | null>(() =>
    activeTab.value ? (surfaceRefs[activeTab.value.id]?.netStats ?? null) : null,
  )
  const activeRtt = computed<number>(() => activeNetStats.value?.rtt ?? 0)
  const allTabsWithSession = computed(() => allTabs.value.filter(t => !!t.sessionId))

  const stripTabs = computed(() =>
    allTabsWithSession.value.map(t => ({
      tabId: t.id,
      tabName: t.name,
      agentState: tabRuntimes[t.id]?.agentState ?? null,
      wsStatus: tabRuntimes[t.id]?.wsStatus ?? ('disconnected' as WSConnectionStatus),
    })),
  )

  // ─── Surface event handlers ───────────────────────────────────────────────────
  function onTabAgentState(tabId: string, state: AgentState | null) { ensureRuntime(tabId).agentState = state }
  function onTabAgentNotifications(tabId: string, states: AgentState[]) { ensureRuntime(tabId).agentNotifications = states }
  function onTabSessionExit(tabId: string, _exitCode: number) { ensureRuntime(tabId).wsStatus = 'disconnected' }
  function onTabConnectionChange(tabId: string, status: WSConnectionStatus) { ensureRuntime(tabId).wsStatus = status }

  // ─── Tab bar operations ───────────────────────────────────────────────────────
  function switchTab(tabId: string) { setActiveTab(tabId) }

  async function closeTab(tabId: string) {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (tab?.sessionId) {
      try { await cliFetch(cliApi(`/sessions/${tab.sessionId}`), { method: 'DELETE' }) } catch { /* silent */ }
    }
    delete tabRuntimes[tabId]
    delete surfaceRefs[tabId]
    removeTab(tabId)
  }

  // ─── Tab rename ───────────────────────────────────────────────────────────────
  const renamingTabId = ref<string | null>(null)
  const renameValue = ref('')

  function startRenameTab(tabId: string) {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab) return
    renamingTabId.value = tabId
    renameValue.value = tab.name
    nextTick(() => {
      const input = document.querySelector(`[data-testid="cli-portal-tab-rename-${tabId}"]`) as HTMLInputElement | null
      input?.select()
    })
  }

  function commitRename() {
    if (renamingTabId.value && renameValue.value.trim()) {
      renameTab(renamingTabId.value, renameValue.value.trim())
    }
    renamingTabId.value = null
  }

  function cancelRename() { renamingTabId.value = null }

  // ─── Tab creation ─────────────────────────────────────────────────────────────
  function nextTabName(): string {
    const existing = allTabs.value.map(t => t.name)
    for (let i = 1; ; i++) {
      const candidate = `终端 ${i}`
      if (!existing.includes(candidate)) return candidate
    }
  }

  async function quickCreateTab() { await createTabSilent({ name: nextTabName(), cwd: '~' }) }

  // ─── Session lifecycle ────────────────────────────────────────────────────────
  async function reconcileSessions() {
    if (allTabs.value.length === 0) return
    let liveSessions: Set<string> = new Set()
    try {
      const resp = await cliFetch(cliApi('/sessions'))
      if (resp.ok) {
        const list = await resp.json() as Array<{ id?: string; session_id?: string }>
        for (const s of list) {
          const id = s.id || s.session_id
          if (id) liveSessions.add(id)
        }
      }
    } catch { /* silent */ }

    for (const tab of allTabs.value) {
      if (tab.sessionId) {
        if (!liveSessions.has(tab.sessionId)) unbindSession(tab.id)
        else ensureRuntime(tab.id)
      }
    }

    const orphans = allTabs.value.filter((t: WorkbenchTab) => !t.sessionId)
    for (const tab of orphans) {
      try {
        const resp = await cliFetch(cliApi('/sessions'), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: tab.name, cwd: tab.cwd || '~' }),
        })
        if (resp.ok) {
          const data = await resp.json() as { id?: string; session_id?: string }
          const sessionId = data.id || data.session_id
          if (sessionId) { bindSession(tab.id, sessionId); ensureRuntime(tab.id); continue }
        }
      } catch { /* silent */ }
      removeTab(tab.id)
    }
  }

  async function createTabSilent(opts?: { name?: string; cwd?: string }) {
    const gid = groups.value[0]?.id
    if (!gid) return
    const tab = addTab(gid, { name: opts?.name, cwd: opts?.cwd || '~' })
    try {
      const resp = await cliFetch(cliApi('/sessions'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: tab.name, cwd: tab.cwd }),
      })
      if (!resp.ok) { removeTab(tab.id); return }
      const data = await resp.json() as { id?: string; session_id?: string }
      const sessionId = data.id || data.session_id
      if (!sessionId) { removeTab(tab.id); return }
      bindSession(tab.id, sessionId)
      ensureRuntime(tab.id)
    } catch { removeTab(tab.id) }
  }

  // ─── Mount initialization ─────────────────────────────────────────────────────
  onMounted(async () => {
    await load()
    await reconcileSessions()
    if (allTabs.value.length === 0) {
      await createTabSilent({ name: nextTabName(), cwd: '~' })
    }
    scenario.send('TABS_READY')
  })

  return {
    scenario, breakpoint,
    loading, error, groups, activeTab, allTabs, showGroupHeaders,
    toggleGroupCollapsed,
    tabRuntimes, registerSurface,
    activeWsStatus, activeAgentState, activeAgentNotifications, activeRtt, activeNetStats,
    allTabsWithSession, stripTabs,
    onTabAgentState, onTabAgentNotifications, onTabSessionExit, onTabConnectionChange,
    switchTab, closeTab,
    renamingTabId, renameValue, startRenameTab, commitRename, cancelRename,
    quickCreateTab,
  }
}
