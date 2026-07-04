import { ref, computed, onMounted, reactive, nextTick } from 'vue'
import { useWorkbench } from '@terminal/composables/cli/useWorkbench'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import type { AgentState, WSConnectionStatus } from '@terminal/types/terminal'
import type { NetStats } from '@terminal/composables/cli/useWebSocketClient'
import type { WorkbenchTab } from '@terminal/types/workbench'
import { useRemotePeers } from '@terminal/composables/cli/useRemotePeers'
import type { TabConnection } from '@terminal/composables/cli/useRemotePeers'
import CliTerminalSurface from '@terminal/components/terminal-session/CliTerminalSurface.vue'
import type { PortalRuntimeResult } from '@ce/composables/layout/usePortalRuntime'

interface TabRuntime {
  agentState: AgentState | null
  agentNotifications: AgentState[]
  wsStatus: WSConnectionStatus
}

export function useCliState(runtime: PortalRuntimeResult) {
  const { scenario, breakpoint } = runtime
  const { cliFetch } = useCliAuth()
  const remotePeers = useRemotePeers()

  // ── Remote-terminal dialog (本机/远程 + peer 选择/新增) open state ──
  const remoteDialogOpen = ref(false)
  function openRemoteDialog() { remoteDialogOpen.value = true }

  // tabFetch — the ONE place that turns a resolved TabConnection into an HTTP call. Local tabs
  // keep the existing cliFetch (same-origin + auth-dialog on 401); remote tabs hit the peer's
  // absolute base with the peer's code as an explicit header (no cookie → CORS-safe, no CSRF).
  function tabFetch(conn: TabConnection, path: string, init?: RequestInit): Promise<Response> {
    if (!conn.isRemote) return cliFetch(cliApi(path), init)
    // Remote with no usable base (peer deleted / unreachable from this page) → reject; NEVER let
    // an empty base fall through to a same-origin request (that would e.g. DELETE a LOCAL session
    // when closing an orphaned remote tab). Callers wrap in try/catch.
    if (!conn.httpBase) return Promise.reject(new Error('remote peer unreachable'))
    const headers = new Headers(init?.headers)
    if (conn.authToken) headers.set('X-CLI-Auth', conn.authToken)
    // Timeout every remote call: an OFFLINE peer must not hang reconcile (on reload) / create /
    // close for the OS TCP timeout (~90s). 8s is far above a reachable peer's <1s. Callers catch.
    const ctrl = new AbortController()
    const timer = setTimeout(() => ctrl.abort(), 8000)
    return fetch(conn.httpBase + cliApi(path), { ...init, headers, signal: ctrl.signal })
      .finally(() => clearTimeout(timer))
  }

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
  const activeSessionId = computed<string | undefined>(() => activeTab.value?.sessionId)
  const allTabsWithSession = computed(() => allTabs.value.filter(t => !!t.sessionId))

  // Per-surface connection props, derived from the SINGLE source (resolveTabConnection). The
  // terminal view renders one surface per entry and passes wsBase/authToken (→ WS) +
  // machineLabel/isRemote (→ chip) straight through. Recomputes if peers/codes change.
  const surfaceTabs = computed(() =>
    allTabsWithSession.value.map((t) => {
      const conn = remotePeers.resolveTabConnection(t)
      return {
        id: t.id,
        name: t.name,
        sessionId: t.sessionId,
        wsBase: conn.wsBase,
        authToken: conn.authToken,
        machineLabel: conn.machineLabel,
        isRemote: conn.isRemote,
        connError: conn.error,
        // Classify a live connection failure (auth code? IP/port unreachable? HTTPS→HTTP block?)
        // by hitting the SAME REST the WS relies on. Reuses probePeer (SSOT) — no second classifier.
        diagnose: () => remotePeers.probePeer(conn.httpBase, conn.authToken),
      }
    }),
  )

  // Route the relocated tmux pane bar's keystrokes to the active surface, which
  // owns the WS / xterm and the tmux-aware key handling (PgUp/PgDn, nav, sticky).
  function activeSendKey(key: string) {
    if (activeTab.value) surfaceRefs[activeTab.value.id]?.onSendKey?.(key)
  }

  // Pane bar's notify bell opens the active surface's install/notify guide sheet.
  function activeOpenInstallGuide() {
    if (activeTab.value) surfaceRefs[activeTab.value.id]?.openInstallGuide?.()
  }

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
      // Delete the session on whichever host owns it — peer for a remote tab, local otherwise.
      const conn = remotePeers.resolveTabConnection(tab)
      try { await tabFetch(conn, `/sessions/${tab.sessionId}`, { method: 'DELETE' }) } catch { /* silent */ }
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
    // Remote tabs MUST be excluded from the local reconcile: their sessionId lives on a peer,
    // so it would never appear in the local /sessions list (→ every remote tab wrongly unbound),
    // and an orphan would get a LOCAL session instead of a peer one. Handle them separately.
    const localTabs = allTabs.value.filter((t: WorkbenchTab) => !t.remotePeerId)
    const remoteTabs = allTabs.value.filter((t: WorkbenchTab) => !!t.remotePeerId)

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

    for (const tab of localTabs) {
      if (tab.sessionId) {
        if (!liveSessions.has(tab.sessionId)) unbindSession(tab.id)
        else ensureRuntime(tab.id)
      }
    }

    const orphans = localTabs.filter((t: WorkbenchTab) => !t.sessionId)
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

    await reconcileRemoteTabs(remoteTabs)
  }

  // Per-peer reconcile for remote tabs (mesh; a peer may be offline). Groups tabs by peer so
  // each peer's session list is fetched once. A reachable peer that no longer has the tab's
  // session → rebuild a fresh shell (RT-7: "丢失重建", never a blank tab). An UNREACHABLE peer
  // (network error / bad scheme / missing code) → leave the tab untouched so the user can fix
  // the peer; never churn sessions on a transient failure.
  async function reconcileRemoteTabs(remoteTabs: WorkbenchTab[]) {
    const byPeer = new Map<string, WorkbenchTab[]>()
    for (const t of remoteTabs) {
      if (!t.remotePeerId) continue
      const arr = byPeer.get(t.remotePeerId) ?? []
      arr.push(t)
      byPeer.set(t.remotePeerId, arr)
    }
    for (const [peerId, tabs] of byPeer) {
      const conn = remotePeers.resolveTabConnection({ remotePeerId: peerId })
      if (conn.error || !conn.authToken) { for (const t of tabs) ensureRuntime(t.id); continue }
      let live: Set<string> | null = null
      try {
        const resp = await tabFetch(conn, '/sessions')
        if (resp.ok) {
          const list = await resp.json() as Array<{ id?: string; session_id?: string }>
          live = new Set()
          for (const s of list) { const id = s.id || s.session_id; if (id) live.add(id) }
        }
      } catch { live = null }
      if (live === null) { for (const t of tabs) ensureRuntime(t.id); continue } // peer offline
      for (const tab of tabs) {
        if (tab.sessionId && live.has(tab.sessionId)) { ensureRuntime(tab.id); continue }
        try {
          const resp = await tabFetch(conn, '/sessions', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: tab.name, cwd: '~' }),
          })
          if (resp.ok) {
            const data = await resp.json() as { id?: string; session_id?: string }
            const sid = data.id || data.session_id
            if (sid) { bindSession(tab.id, sid); ensureRuntime(tab.id); continue }
          }
        } catch { /* fall through to unbind */ }
        unbindSession(tab.id)
      }
    }
  }

  // Open a REMOTE terminal tab against a registered peer: create a session ON THE PEER, then
  // bind it to a new tab marked with remotePeerId. Returns a result so the dialog can surface a
  // precise error (bad scheme / missing code / unreachable) instead of a silent failure.
  async function createRemoteTab(peerId: string): Promise<{ ok: boolean; error?: string }> {
    const conn = remotePeers.resolveTabConnection({ remotePeerId: peerId })
    if (conn.error) return { ok: false, error: conn.error }
    if (!conn.authToken) return { ok: false, error: '缺少该远程的认证码' }
    const gid = groups.value[0]?.id
    if (!gid) return { ok: false, error: '无可用分组' }
    const name = nextTabName()
    // Create WITH remotePeerId so the very first (debounced) persist already marks it remote — a
    // crash/reload before bindSession must not leave a remote tab looking local (→ local session).
    const tab = addTab(gid, { name, cwd: '~', remotePeerId: peerId })
    try {
      const resp = await tabFetch(conn, '/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, cwd: '~' }),
      })
      if (!resp.ok) { removeTab(tab.id); return { ok: false, error: `远程建会话失败 (HTTP ${resp.status})` } }
      const data = await resp.json() as { id?: string; session_id?: string }
      const sessionId = data.id || data.session_id
      if (!sessionId) { removeTab(tab.id); return { ok: false, error: '远程未返回会话 id' } }
      bindSession(tab.id, sessionId)
      ensureRuntime(tab.id)
      return { ok: true }
    } catch {
      removeTab(tab.id)
      return { ok: false, error: '远程建会话异常（网络 / CORS 被拦）' }
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
    await remotePeers.loadPeers() // hydrate peer registry before reconcile resolves remote tabs
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
    activeSessionId, activeSendKey, activeOpenInstallGuide,
    allTabsWithSession, stripTabs, surfaceTabs,
    onTabAgentState, onTabAgentNotifications, onTabSessionExit, onTabConnectionChange,
    switchTab, closeTab,
    renamingTabId, renameValue, startRenameTab, commitRename, cancelRename,
    quickCreateTab,
    // remote-terminal (mesh)
    remoteDialogOpen, openRemoteDialog, createRemoteTab,
  }
}
