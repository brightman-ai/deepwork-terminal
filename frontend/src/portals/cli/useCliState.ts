import { ref, computed, onMounted, reactive, nextTick, watch } from 'vue'
import { useRouter } from 'vue-router'
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
import { useTabShortcuts } from '@terminal/composables/cli/useTabShortcuts'
import { computeVisibleTabOrder } from '@terminal/composables/cli/useVisibleTabOrder'
import { displayTabName } from '@terminal/composables/cli/useTabDisplayName'
import { useSessionsOverview } from '@terminal/composables/cli/useSessionsOverview'
import { useOverviewUnits, type EffectiveStatus, type OverviewUnit } from '@terminal/composables/cli/useAgentOverview'
import { useTabContextMenu } from '@terminal/composables/cli/useTabContextMenu'
import { copyTextToClipboard } from '@ce/utils/clipboard'
import { type TabLiveness, type TabNotLive } from '@terminal/composables/cli/tabLiveness'
import { reconcileTabs } from '@terminal/composables/cli/reconcileTabs'
import { reopenDetachedTabs, type ReopenCandidate } from '@terminal/composables/cli/reopenDetached'
import { selectOverviewCard } from '@terminal/composables/cli/overviewSelection'
import { postReopenNotice, reopenNoticeOf, forgetReopenNotice } from '@terminal/composables/cli/reopenNotice'

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
    load, addTab, setTabCwd, removeTab, renameTab, setActiveTab,
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

  // ─── Per-tab liveness ─────────────────────────────────────────────────────────
  // 「这个标签背后还有没有一个活着的进程」——和 agentState（进程在干嘛）是两个轴，别混。
  // 刻意只活在内存里：写进 workbench 配置就会出现「上次记着 detached，这次其实活着」的陈旧真相；
  // 每次挂载由 reconcile 重算才是 SSOT。默认 live（新建出来的标签当然是活的）。
  const tabLiveness = reactive<Record<string, TabLiveness>>({})
  function setTabLiveness(tabId: string, liveness: TabLiveness): void { tabLiveness[tabId] = liveness }
  function livenessOf(tabId: string): TabLiveness { return tabLiveness[tabId] ?? 'live' }
  /** 给标签栏/总览用的只读投影（和 tabStatuses 同一形状，便于两个轴并排读）。 */
  const tabLivenessMap = computed(() => new Map<string, TabLiveness>(Object.entries(tabLiveness)))

  // ─── 「这个标签刚被自动重开过」──────────────────────────────────────────────
  // 真相按 session id 存在 reopenNotice.ts（那行字要由终端连接层写进 xterm，而连接层只认识
  // session id），这里只做「标签 → 它那条 session 还挂不挂着标记」的投影。不在这里再存一份
  // tabId 版本：两处各留一份，迟早出现「标签上还写着已重开、终端里早被用户敲过」的分叉。
  /** 标签栏读的只读投影：哪些标签还挂着「已重开」小标。用户在该终端首次输入后自动消失
   *  （撤标由 useWebSocketClient.sendBinary 那一处出口负责，见 reopenNotice 不变量 ③）。 */
  const reopenedTabIds = computed(
    () => new Set(allTabs.value.filter(t => !!reopenNoticeOf(t.sessionId)).map(t => t.id)),
  )
  /** 接上新 shell 并同时交付痕迹 —— reopenDetached 的 adopt 端口实现（见那个文件的 ③）。
   *  先投递 notice 再 bindSession：bind 一发生 surface 就会挂载并在 xterm ready 时取信，
   *  那时投递箱里必须已经有东西，否则第一帧就错过了写入时机（ready 只发生一次）。 */
  function adoptReopened(tabId: string, sessionId: string, notice: string): void {
    postReopenNotice(sessionId, notice)
    bindSession(tabId, sessionId)
    ensureRuntime(tabId)
    setTabLiveness(tabId, 'live')
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
        httpBase: conn.httpBase,
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

  // ─── D4/D1/D7: visible tab order (collapsed-group tabs are neither clickable nor numbered) ────
  const visibleTabIds = computed<string[]>(() =>
    groups.value.filter(g => !g.collapsed).flatMap(g => g.tabs.map(t => t.id)),
  )
  const tabOrder = computed(() => computeVisibleTabOrder(visibleTabIds.value))
  const tabPositions = computed(() => tabOrder.value.position)

  function tabDisplayName(tabId: string, name: string): string {
    return displayTabName(name, tabPositions.value.get(tabId))
  }

  // D1-D3: single-key-direct shortcuts (Alt+1-9 / next / prev / new / close / rename). Standalone
  // has one always-mounted CLI portal (no sibling portal competes for these Alt combos the way
  // pro's WindowDockOverlay does), so isActive is unconditionally true — the listener's own
  // onMounted/onBeforeUnmount lifecycle (tied to this component tree) is the only gate needed.
  useTabShortcuts({
    orderedTabIds: () => visibleTabIds.value,
    activeTabId: () => activeTab.value?.id,
    isActive: () => true,
    onSelect: switchTab,
    onNew: quickCreateTab,
    onClose: (tabId: string) => { void closeTab(tabId) },
    onRename: startRenameTab,
  })

  // D7: the SAME Agent Overview tmux users get — card grid with each terminal's live output —
  // fed by the pushed sessions_overview frame instead of tmux topology. Cards are keyed by PTY
  // session id; the visible tab order supplies the card number so it matches 终端N and Alt+N.
  const overviewOpen = ref(false)
  function toggleOverview(): void { overviewOpen.value = !overviewOpen.value }
  /** 关掉总览。Esc / 点遮罩 / 点中一张卡片走的都是它——「关闭总览」只有一处实现，
   *  模板里不再各写一份 `overviewOpen = false`。 */
  function closeOverview(): void { overviewOpen.value = false }
  /** 可见标签顺序 → 卡片编号的钥匙串。没有 session 的标签（进程已结束）占一个 `tab:` 占位号：
   *  卡片编号同时也是标签栏的「终端N」和 前缀+N 快捷键的目标，一旦跳号，三者就对不上了。占位 id
   *  不可能撞上真实 session id，useSessionsOverview 会把它当"没有对应 session"直接滤掉。 */
  const overviewIdOrder = computed<string[]>(() =>
    visibleTabIds.value.map((tabId) => allTabs.value.find(t => t.id === tabId)?.sessionId || `tab:${tabId}`),
  )
  const { entries: sessionEntries, units: liveOverviewUnits } = useSessionsOverview(
    () => activeTab.value?.sessionId,
    () => overviewIdOrder.value,
  )
  /** 把服务端上报的**活着的 cwd** 同步回标签并持久化。
   *
   *  标签里存的 cwd 本来是「创建时」的目录（UI 新建一律是 `~`），而人几乎一定会 cd 到别处去。
   *  服务一重启，活着的 cwd 就随会话一起消失了 —— 那一刻唯一还存在的副本，就是我们在它死之前
   *  存下来的这一份。没有这条同步，自动重开只能回到家目录，"回到原目录"就名不副实。
   *
   *  顺带修正了另外两处一直在用陈旧值的地方：右键「复制目录路径」与总览卡片标题（cwd basename）。
   *  写入本身在 setTabCwd 里做了「值没变就不写」的短路，所以稳态下不产生任何请求。 */
  watch(sessionEntries, (entries) => {
    for (const tab of allTabs.value) {
      if (!tab.sessionId) continue
      const cwd = entries.find(e => e.id === tab.sessionId)?.cwd
      if (cwd) setTabCwd(tab.id, cwd)
    }
  }, { deep: false })

  /** 总览 = 活着的 session 卡 + 没活着的标签卡。
   *
   *  后者必须在：总览的承诺是「所有终端一眼看全」，而进程已结束的标签恰恰是最需要被看见的那类
   *  （用户以为 agent 还在跑的就是它）。它们没有 session id，所以永远不会出现在服务端推送的
   *  sessions_overview 帧里——只能在这里按标签补齐，keyed 在同一个 `tab:` 占位 id 上。 */
  const overviewUnits = computed<OverviewUnit[]>(() => {
    const extra: OverviewUnit[] = []
    for (const tabId of visibleTabIds.value) {
      const tab = allTabs.value.find(t => t.id === tabId)
      if (!tab || tab.sessionId) continue
      const l = livenessOf(tab.id)
      extra.push({
        key: `tab:${tab.id}`,
        index: tabPositions.value.get(tab.id) ?? 0,
        title: tabDisplayName(tab.id, tab.name),
        active: activeTab.value?.id === tab.id,
        cwd: tab.cwd && tab.cwd !== '~' ? tab.cwd : '',
        tool: '',
        // 存活态不是 agent 状态：没有进程就谈不上 waiting/running，所以原始状态就是 idle，
        // 由 liveness 这个独立的轴去说明「它不是闲着，是不在了」。
        rawStatus: 'idle',
        awaiting: false,
        awaitingSince: '',
        signals: [],
        tail: [],
        liveness: l === 'live' ? 'detached' : l,
      })
    }
    return extra.length ? [...liveOverviewUnits.value, ...extra] : liveOverviewUnits.value
  })
  const overview = useOverviewUnits(overviewUnits, overviewOpen)

  /** tabId → seen-aware status, so the tab strip's dot is the SAME derivation the overview card
   *  uses — not a second one that happened to agree. The strip keys on TAB id while the overview
   *  keys on SESSION id, so this is the one place the two identities are joined; every consumer
   *  downstream just reads the map. */
  const tabStatuses = computed(() => {
    const bySession = new Map(overviewUnits.value.map(u => [u.key, overview.effectiveStatus(u)]))
    const out = new Map<string, EffectiveStatus>()
    for (const tab of allTabs.value) {
      const s = tab.sessionId ? bySession.get(tab.sessionId) : undefined
      if (s) out.set(tab.id, s)
    }
    return out
  })
  /** Overview card → 切过去**并关掉总览**。编号就是可见标签的位置（活着的和已结束的一视同仁），
   *  所以这里直接按位置取标签，不再绕 session id —— 已结束的卡片同样点得动。
   *  「切+关」这件事本身由跨壳 SSOT selectOverviewCard 决定，两个壳不各写一遍。 */
  function selectOverviewIndex(index: number): void {
    selectOverviewCard(index, visibleTabIds.value, { switchTo: switchTab, closeOverview })
  }

  // ─── Tab context menu ─────────────────────────────────────────────────────────
  /** The tab's REAL working directory. Prefers the live one the server reports for its session
   *  (the user has almost certainly cd'd since the tab was created); falls back to the configured
   *  one, except for the "~" placeholder — copying a literal tilde helps nobody. */
  function liveCwd(tabId: string): string {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab) return ''
    const entry = tab.sessionId ? sessionEntries.value.find(e => e.id === tab.sessionId) : undefined
    if (entry?.cwd) return entry.cwd
    return tab.cwd && tab.cwd !== '~' ? tab.cwd : ''
  }

  /** Closing N terminals at once kills N processes and cannot be undone — the one place in this
   *  menu that earns a confirm (single close doesn't: it's one tab, and it's what you clicked). */
  async function closeOtherTabs(keepId: string): Promise<void> {
    const others = visibleTabIds.value.filter(id => id !== keepId)
    if (!others.length) return
    if (!window.confirm(`关闭其他 ${others.length} 个终端？其中运行的进程会被结束。`)) return
    for (const id of others) await closeTab(id)
  }

  const tabMenu = useTabContextMenu({
    rename: startRenameTab,
    close: (id) => { void closeTab(id) },
    closeOthers: (id) => { void closeOtherTabs(id) },
    create: () => { void quickCreateTab() },
    copy: (text) => copyTextToClipboard(text),
    tabCount: () => visibleTabIds.value.length,
  })

  function openTabMenu(e: MouseEvent, tabId: string): void {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab) return
    tabMenu.openAt(e, { id: tabId, name: tabDisplayName(tabId, tab.name), cwd: liveCwd(tabId) })
  }

  // D6: guide banner's "去设置" deep-links to the shortcuts settings section.
  const router = useRouter()
  function openShortcutsSettings(): void {
    void router.push({ path: '/portal/settings', query: { section: 'shortcuts' } })
  }

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
    delete tabLiveness[tabId]
    forgetReopenNotice(tab?.sessionId)
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
  /**
   * 重载/重启后把标签和真实 session 对上账，**判定与动作分成两步**。
   *
   * 第一步 reconcileTabs：只判定，不重建。那个端口集合里根本没有能造出终端的口子，所以
   * 「孤儿标签就偷偷开个空 PTY 顶上去」这种事在判定阶段结构上就写不出来（见 reconcileTabs.ts）。
   * 这里只负责把 HTTP 细节（前缀/鉴权/超时/本机 vs peer）翻译成「拿到集合 / 没拿到」。
   *
   * 第二步 reopenDetachedTabs：本机标签确认结束（detached）→ **自动重开一个 shell，cd 回它
   * 原来的目录，并在终端里留下一行说明**。这一步和被删掉的静默重建的全部区别就是那行说明，
   * 所以它由端口签名强制（见 reopenDetached.ts）。远程标签与 unreachable 不走这条路，仍然由
   * DetachedTerminalCard 把话讲清楚、把选择交还用户——那种处境里对面的 agent 可能正跑着。
   */
  async function reconcileSessions() {
    if (allTabs.value.length === 0) return
    await reconcileTabs(
      allTabs.value.map((t: WorkbenchTab) => ({ id: t.id, sessionId: t.sessionId, remotePeerId: t.remotePeerId })),
      {
        listLocalSessions: () => fetchSessionIds(() => cliFetch(cliApi('/sessions'))),
        listPeerSessions: (peerId: string) => {
          // Peer 解析不出来（已删除 / 缺认证码 / scheme 不对）= 问不到，不是"它上面没有终端"。
          const conn = remotePeers.resolveTabConnection({ remotePeerId: peerId })
          if (conn.error || !conn.authToken) return Promise.resolve(null)
          return fetchSessionIds(() => tabFetch(conn, '/sessions'))
        },
        setLiveness: (tabId, liveness) => {
          setTabLiveness(tabId, liveness)
          // 还活着的标签要有 runtime 槽位（agentState/wsStatus 挂在这上面）；已结束的没有进程可挂。
          if (liveness === 'live') ensureRuntime(tabId)
        },
        unbindSession,
      },
    )
    await reopenDetachedTabs(reopenCandidates(), {
      createSession: (tab) => createSessionOn(tab, { name: tab.name, cwd: tab.cwd || '~' }),
      adopt: adoptReopened,
    })
  }

  /** 当前所有标签的「自动重开候选」视图。判定（谁该重开）在 reopenDetached.ts，这里只搬数据。 */
  function reopenCandidates(): ReopenCandidate[] {
    return allTabs.value.map((t: WorkbenchTab) => ({
      id: t.id,
      name: t.name,
      cwd: t.cwd,
      remotePeerId: t.remotePeerId,
      liveness: livenessOf(t.id),
    }))
  }

  /** POST /sessions 到这个标签该去的那台机器（本机 / 它的 peer），返回新 session id 或 null。
   *  自动重开与卡片上的手动新建共用它——同一件事只能有一处 HTTP 细节。 */
  async function createSessionOn(
    tab: { remotePeerId?: string },
    body: { name: string; cwd: string },
  ): Promise<string | null> {
    const conn = remotePeers.resolveTabConnection(tab)
    try {
      const resp = await tabFetch(conn, '/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!resp.ok) return null
      const data = await resp.json() as { id?: string; session_id?: string }
      return data.id || data.session_id || null
    } catch {
      return null
    }
  }

  /** 把一次 /sessions 请求翻译成「id 集合」或 null（没问到）。null ≠ 空集合，见 tabLiveness.ts。 */
  async function fetchSessionIds(req: () => Promise<Response>): Promise<Set<string> | null> {
    try {
      const resp = await req()
      if (!resp.ok) return null
      const list = await resp.json() as Array<{ id?: string; session_id?: string }>
      const out = new Set<string>()
      for (const s of list) { const id = s.id || s.session_id; if (id) out.add(id) }
      return out
    } catch {
      return null
    }
  }

  /**
   * 用户在 DetachedTerminalCard 上明确按下「在此目录新建终端」后，才为这个标签开一个新 PTY。
   *
   * 和被删掉的静默重建是同一个 HTTP 调用，区别是**谁决定的**：那时是程序替用户假装恢复，现在是
   * 用户看完「进程已结束」的说明后自己选的。沿用原标签的名字与目录（远程则开在原来那台机器上），
   * 所以位置、编号、快捷键都不动。
   */
  async function restoreTab(tabId: string): Promise<{ ok: boolean; error?: string }> {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab) return { ok: false, error: '这个标签已经不在了' }
    const conn = remotePeers.resolveTabConnection(tab)
    if (tab.remotePeerId) {
      if (conn.error) return { ok: false, error: conn.error }
      if (!conn.authToken) return { ok: false, error: '缺少该远程的认证码' }
    }
    try {
      const resp = await tabFetch(conn, '/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: tab.name, cwd: tab.cwd || '~' }),
      })
      if (!resp.ok) return { ok: false, error: `新建终端失败 (HTTP ${resp.status})` }
      const data = await resp.json() as { id?: string; session_id?: string }
      const sessionId = data.id || data.session_id
      if (!sessionId) return { ok: false, error: '服务端没有返回会话 id' }
      bindSession(tab.id, sessionId)
      ensureRuntime(tab.id)
      setTabLiveness(tab.id, 'live')
      return { ok: true }
    } catch {
      return { ok: false, error: tab.remotePeerId ? '连不上那台机器（网络 / CORS 被拦）' : '新建终端异常（网络错误）' }
    }
  }

  /** unreachable 标签的「重新检查一次」：再对一次账，不新建也不结束任何进程。 */
  async function recheckTab(tabId: string): Promise<{ ok: boolean; error?: string }> {
    await reconcileSessions()
    return livenessOf(tabId) === 'unreachable'
      ? { ok: false, error: '还是问不到，稍后再试' }
      : { ok: true }
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
      setTabLiveness(tab.id, 'live')
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
      setTabLiveness(tab.id, 'live')
    } catch { removeTab(tab.id) }
  }

  // ─── Detached surface (the honest replacement for the silent rebuild) ──────────
  /**
   * 当前标签没有活着的进程时，终端区域该显示的那张卡的全部输入；否则 null（照常渲染终端）。
   *
   * 判据是**结构事实**「这个标签没有绑着 session」，而不是 liveness 本身：liveness 只负责解释
   * 为什么（确认结束 / 问不到）。一个仍然绑着 session 的 unreachable 标签不走这条路——它的终端
   * 界面自己就有连接诊断（connError / diagnose），盖一张卡上去只会挡住已有的输出和重连逻辑。
   */
  const detachedCard = computed(() => {
    const tab = activeTab.value
    if (!tab || tab.sessionId) return null
    const l = livenessOf(tab.id)
    const liveness: TabNotLive = l === 'live' ? 'detached' : l
    const conn = tab.remotePeerId ? remotePeers.resolveTabConnection(tab) : null
    return {
      tabId: tab.id,
      liveness,
      name: tabDisplayName(tab.id, tab.name),
      cwd: tab.cwd,
      remote: !!tab.remotePeerId,
      machineLabel: conn?.machineLabel ?? '',
    }
  })

  /** 卡上主按钮真正要做的事：已确认结束 → 新建；问不到 → 再问一次。 */
  function detachedAction(): Promise<{ ok: boolean; error?: string }> {
    const card = detachedCard.value
    if (!card) return Promise.resolve({ ok: true })
    return card.liveness === 'unreachable' ? recheckTab(card.tabId) : restoreTab(card.tabId)
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
    // 存活态（进程还在不在）——和 tabStatuses（agent 在干嘛）是两个轴
    tabLivenessMap, detachedCard, detachedAction, restoreTab, recheckTab,
    // 自动重开留下的标记：哪些标签现在挂的是新 shell（用户在里面首次输入即消失）
    reopenedTabIds,
    // D1-D7: tab-shortcut numbering + overview + guide banner deep-link
    tabPositions, tabDisplayName, tabStatuses,
    overviewOpen, toggleOverview, closeOverview, overviewGroups: overview.groups, overviewRollup: overview.rollup,
    selectOverviewIndex,
    openShortcutsSettings,
    // tab context menu (right-click) — same action table as the shortcuts
    tabMenu, openTabMenu,
  }
}
