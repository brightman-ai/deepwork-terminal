import { ref, computed, onMounted, onBeforeUnmount, reactive, nextTick, watch } from 'vue'
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
import { postReopenNotice, reopenNoticeOf, forgetTerminalNotice } from '@terminal/composables/cli/terminalNotice'

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
    load, resync, addTab, setTabCwd, removeTab, renameTab, adoptRemoteTabName, setActiveTab,
    toggleGroupCollapsed, bindSession, unbindSession,
  } = useWorkbench()

  // Timestamp of this device's own last local rename, by sessionId — see commitRename and the
  // sessionEntries watch below (cross-device rename sync grace window).
  const lastLocalRenameAt = new Map<string, number>()
  const RENAME_GRACE_MS = 4000

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
  // 真相按 session id 存在 terminalNotice.ts（那行字要由终端连接层写进 xterm，而连接层只认识
  // session id），这里只做「标签 → 它那条 session 还挂不挂着标记」的投影。不在这里再存一份
  // tabId 版本：两处各留一份，迟早出现「标签上还写着已重开、终端里早被用户敲过」的分叉。
  /** 标签栏读的只读投影：哪些标签还挂着「已重开」小标。用户在该终端首次输入后自动消失
   *  （撤标由 useWebSocketClient.sendBinary 那一处出口负责，见 terminalNotice 不变量 ③）。 */
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

  // D1-D3: single-key-direct shortcuts (Alt+1-9 / next / prev / new / close). Standalone
  // has one always-mounted CLI portal (no sibling portal competes for these Alt combos the way
  // pro's WindowDockOverlay does), so isActive is unconditionally true — the listener's own
  // onMounted/onBeforeUnmount lifecycle (tied to this component tree) is the only gate needed.
  // leader（默认 Ctrl+B）走的是同一张动作表的第二条路。它在**当前标签 attach 了 tmux 时整个让位**：
  // 那个前缀就是 tmux 自己的前缀，抢它就是抢。attached 只有终端表面知道，所以经它已注册的实例读
  // ——和这里读 netStats / 调 onSendKey 是同一条既有通路，不新拉线。
  const { leaderPending, leaderLabel } = useTabShortcuts({
    orderedTabIds: () => visibleTabIds.value,
    activeTabId: () => activeTab.value?.id,
    isActive: () => true,
    onSelect: switchTab,
    onNew: quickCreateTab,
    onClose: (tabId: string) => { void closeTab(tabId) },
    leaderEnabled: () => !(activeTab.value ? surfaceRefs[activeTab.value.id]?.tmuxAttached : false),
    onOverview: toggleOverview,
    onRename: startRenameTab,
    // 前缀 + `[` = 进入只读回看，和 tmux 的 copy-mode 同一个键。动作住在终端表面上（只有它拿得到
    // 那个会话的历史），这里只是把 leader 转给当前标签——和 onSendKey / netStats 走的是同一条既有
    // 通路，不新拉线。
    onCopyMode: () => {
      const id = activeTab.value?.id
      if (!id) return
      // 表面返回拒绝理由（tmux 标签 / 远程标签），这一层负责说出来 —— 一个按下去毫无反应的
      // 快捷键，使用者只会以为是自己按错了。
      const why = surfaceRefs[id]?.openCopyMode?.()
      if (why) showNotice(why)
    },
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
      const entry = entries.find(e => e.id === tab.sessionId)
      if (!entry) continue
      if (entry.cwd) setTabCwd(tab.id, entry.cwd)
      // Cross-device rename sync (read side — see adoptRemoteTabName's doc comment for the write
      // side this closes). Grace-windowed, NOT string-matched against what we last pushed: a
      // stale overview frame can carry the PRE-rename title (POST hasn't landed server-side yet),
      // which differs from both tab.name and our pushed value — matching on value alone would
      // adopt that stale title and stomp the input the user just typed. A short window during
      // which this tab ignores the overview feed entirely, regardless of what value shows up in
      // it, is what actually closes that race.
      const pushedAt = lastLocalRenameAt.get(tab.sessionId)
      const withinGrace = pushedAt !== undefined && (Date.now() - pushedAt) < RENAME_GRACE_MS
      if (entry.title && entry.title !== tab.name && !withinGrace) {
        adoptRemoteTabName(tab.id, entry.title)
      }
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
  // 曾经这里有个 `dwStripTabs` —— 非 tmux 底栏编号列的数据投影。编号列 2026-08-11 退场
  // （切终端改走底栏总览胶囊 → 总览浮层），这份投影随之没有消费者，一并删掉。
  // 号码/状态的真相仍是 `tabPositions` / `tabStatuses`，它们还有标签栏、快捷键、总览三个消费者。

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
    forceKillForeground: (id) => { void forceKillForegroundTab(id) },
    applyEnvHere: (id) => { void applyEnvToTab(id) },
  })

  function openTabMenu(e: MouseEvent, tabId: string): void {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab) return
    const isTmux = tab.sessionId
      ? (sessionEntries.value.find(entry => entry.id === tab.sessionId)?.tmuxDetected ?? false)
      : false
    tabMenu.openAt(e, { id: tabId, name: tabDisplayName(tabId, tab.name), cwd: liveCwd(tabId), isTmux })
  }

  // D6: guide banner's "去设置" deep-links to the shortcuts settings section.
  const router = useRouter()
  function openShortcutsSettings(): void {
    void router.push({ path: '/portal/settings', query: { section: 'shortcuts' } })
  }

  // ─── Surface event handlers ───────────────────────────────────────────────────
  function onTabAgentState(tabId: string, state: AgentState | null) { ensureRuntime(tabId).agentState = state }
  function onTabAgentNotifications(tabId: string, states: AgentState[]) { ensureRuntime(tabId).agentNotifications = states }
  /**
   * 这个标签背后的 shell 刚刚退出了（人敲了 `exit`、程序跑完、崩溃、被杀）。
   *
   * 这里曾经只有一句 `wsStatus = 'disconnected'` —— **事件被显示了，却没有被处置**。标签仍然绑着
   * 那条已经死掉的 sessionId，而 detachedCard 的判据恰恰是「这个标签没有绑着 session」，所以那张
   * "进程已结束"的卡永远不出现：用户看到的是一个写着 `[进程已退出]` 的死终端，敲什么都没反应，
   * 一直卡到下次刷新页面（reconcileSessions 只在 onMounted 跑）。用户报的就是这个。
   *
   * 处置按**退出码**分两种（对标 tmux：`remain-on-exit` 默认 off，pane 直接消失）：
   *
   *  • **干净退出（0）→ 关掉这个标签。** 这就是 `exit` 本来的意思。留一个空壳标签在那儿、等人再点
   *    一次"关闭"，是纯粹多出来的一步。
   *  • **非零 → 留下标签、解绑、让卡出现。** 崩了或被杀时，退出码和它所在的目录正是用户要看的
   *    东西；把标签连同名字和目录一起吞掉，等于把出事现场清理干净了。
   *
   * 不必担心这条被历史事件误触发：WS 层对已经退出的 session **直接拒绝升级**（410 Gone，见
   * handleWebSocket），所以 `shell_exit` 只可能来自"在这条连接眼前刚死的"那一次，不存在重放。
   */
  async function onTabSessionExit(tabId: string, exitCode: number): Promise<void> {
    ensureRuntime(tabId).wsStatus = 'disconnected'
    await settleExitedTab(tabId, exitCode)
  }

  /**
   * 一个标签背后的 shell 已经结束了，把这个标签**处置掉**。
   *
   * 两条入口共用这一处，这是刻意的：
   *   • 实时 —— 有浏览器盯着时收到的 `shell_exit` 帧（onTabSessionExit）
   *   • 事后 —— 挂载/恢复时才发现它已经退了（reconcileSessions）
   *
   * 第二条入口原先根本不存在，于是"没人看着的时候退出"的标签会**永久卡成 disconnected**：
   * `/sessions` 把已退出的会话也一并返回，而对账把"在清单里"当成了"还活着"，所以既不解绑、也不
   * 出卡片，界面就一直连一个已经死掉的 session（WS 对它直接 410）。这和当初 `exit` 卡住是同一个
   * 缺陷的两半——**两条路径只修一条等于没修**，这个仓库为这句话付过学费（见 session_manager.go
   * 里 PTYFactory 那段注释）。
   *
   * 策略按退出码分，和 tmux `remain-on-exit off` 一致：
   *   • 0    —— 干净退出（人敲了 `exit` / 程序跑完）→ 关掉标签。
   *   • 非 0 —— 崩了 / 被杀 → 留下标签、解绑，让"进程已结束"的卡出现。退出码和目录正是用户此刻
   *            要看的东西，把标签吞掉等于把现场清理干净了。
   */
  async function settleExitedTab(tabId: string, exitCode: number): Promise<void> {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab) return

    if (exitCode === 0) {
      await closeTab(tabId)
      // 关掉最后一个标签会留下一个空门户 —— onMounted 里那条兜底只在挂载时跑一次。
      if (allTabs.value.length === 0) await createTabSilent({ name: nextTabName(), cwd: '~' })
      return
    }

    setTabLiveness(tabId, 'detached')
    if (tab.sessionId) {
      // 顺手把 daemon 里那条已死的 session 收掉，否则它会以 exited 状态一直留在清单里（既占
      // `dw-terminal muxd --status` 的版面，也让孤儿认领每轮都要跳过它）。
      const conn = remotePeers.resolveTabConnection(tab)
      try { await tabFetch(conn, `/sessions/${tab.sessionId}`, { method: 'DELETE' }) } catch { /* silent */ }
      forgetTerminalNotice(tab.sessionId)
      unbindSession(tabId)
    }
  }
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
    forgetTerminalNotice(tab?.sessionId)
    removeTab(tabId)
  }

  // ─── 轻提示（一次性、不阻塞）────────────────────────────────────────────────
  //
  // 刻意不用 window.alert/confirm：原生弹窗在 headless CDP 下会**阻塞整个 JS 线程**，于是这条路
  // 就变成了永远验收不了的路（cli-tabs 的 REQ-004 已经被 window.confirm 卡死过一次，那条验收至今
  // 是未完成状态）。一个能被截图看见的行内提示，既是给用户的反馈，也是给验收的证据。
  //
  // 也不写进 xterm：注入被拒的那一刻，终端里正跑着一个可能处于 alt-screen 的程序（那正是被拒的
  // 原因），往它的画面上写字会把那一屏搞乱。
  const notice = ref('')
  let noticeTimer: ReturnType<typeof setTimeout> | null = null
  function showNotice(text: string): void {
    notice.value = text
    if (noticeTimer) clearTimeout(noticeTimer)
    noticeTimer = setTimeout(() => { notice.value = '' }, 6000)
  }

  /**
   * 把设置页里那份「新终端的环境变量」应用到这个**已经在跑**的终端。
   *
   * 那份配置是 spawn 时套用的，所以只影响新建的终端 —— 和 tmux `set-environment` 一样（实测：改完
   * 之后同一个 pane 读到的还是旧值，只有新开的 window 是新值）。已在跑的 shell 的环境在它自己的
   * 内存里，外部改不了，唯一能改它的是 shell 自己执行一条命令。所以这里做的事就是把 `unset` /
   * `export` 敲进去，**让用户在终端里看得见**。
   *
   * 先 switchTab 再注入：这条命令的正当性完全来自"你看得见它被敲进去"。往一个看不见的标签里注入
   * 命令，就成了背着用户在他自己的 shell 里跑东西。
   *
   * 服务端会在前台有程序时拒绝（否则这几行字会被打进那个程序的 stdin —— 比如打进你正在对话的
   * claude 里），拒绝理由原样端到提示里。
   */
  async function applyEnvToTab(tabId: string): Promise<void> {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab?.sessionId) { showNotice('这个标签还没有连上终端'); return }
    switchTab(tabId)
    const conn = remotePeers.resolveTabConnection(tab)
    try {
      const resp = await tabFetch(conn, `/sessions/${tab.sessionId}/apply-env`, { method: 'POST' })
      const data = await resp.json().catch(() => ({})) as { applied?: string[]; note?: string; error?: string }
      if (!resp.ok) { showNotice(data.error || `应用失败（HTTP ${resp.status}）`); return }
      if (data.note) { showNotice(data.note); return }
      const n = data.applied?.length ?? 0
      showNotice(n ? `已把 ${n} 条命令敲进这个终端，看终端里的回显` : '没有可应用的环境变量')
    } catch {
      showNotice('应用失败（网络错误）')
    }
  }

  /** SIGKILL the tab's current PTY foreground process group — see useTabContextMenu's
   *  forceKillForeground doc. Best-effort like the rest of this menu's fire-and-forget actions:
   *  it's a recovery command reached for when something is already stuck, so a failed request has
   *  nothing better to fall back to client-side than leaving the tab as stuck as it already was. */
  async function forceKillForegroundTab(tabId: string): Promise<void> {
    const tab = allTabs.value.find(t => t.id === tabId)
    if (!tab?.sessionId) return
    const conn = remotePeers.resolveTabConnection(tab)
    try {
      await tabFetch(conn, `/sessions/${tab.sessionId}/force-kill-fg`, { method: 'POST' })
    } catch { /* silent */ }
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
      const trimmed = renameValue.value.trim()
      const tab = allTabs.value.find(t => t.id === renamingTabId.value)
      // Recorded BEFORE the backend POST resolves — the very next sessions_overview push can
      // still carry the pre-rename title (network/debounce), and without this the reconcile
      // watch below would read that stale value as "someone else renamed it back" and fight the
      // input the user just typed. See lastLocalRenameAt below (grace window, not a value match:
      // the stale frame's title is neither the new name NOR literally our old push, so comparing
      // against a remembered string can't catch it — only "ignore this tab's feed for a bit" can).
      if (tab?.sessionId) lastLocalRenameAt.set(tab.sessionId, Date.now())
      renameTab(renamingTabId.value, trimmed)
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
    // 本机清单只取一次，两个方向共用：标签→session 的对账（reconcileTabs），以及
    // session→标签 的认领（adoptOrphanSessions）。**必须在 allTabs 为空时也取**——标签列表整份
    // 丢掉恰恰是认领要救的那种情形，早退会让唯一的自救通道在最需要它的时候关着。
    const localSessions = await fetchLocalSessions()

    // 先结算「没人看着的时候就已经退出」的会话，再做对账。
    //
    // 顺序是承重的：结算走的是退出码策略（0 关标签 / 非 0 留卡片），而对账之后的
    // reopenDetachedTabs 会给任何 detached 的本机标签**自动开一个新 shell**。让它先跑，用户敲过
    // `exit` 的标签就会被"恢复"成一个新 shell，并配上一句"上一个进程已随服务重启结束"——服务根本
    // 没重启，那是句谎话。所以已结算的标签要从重开候选里摘掉。
    const settled = new Set<string>()
    if (localSessions) {
      const exited = new Map(localSessions.filter(s => s.exited).map(s => [s.id, s.exitCode]))
      // 快照迭代：settleExitedTab 在干净退出时会关掉标签，边遍历边改 allTabs 会漏项。
      for (const t of allTabs.value.slice()) {
        if (!t.sessionId || t.remotePeerId) continue
        const code = exited.get(t.sessionId)
        if (code === undefined) continue
        settled.add(t.id)
        await settleExitedTab(t.id, code)
      }
    }

    // 存活集合**只含真的还活着的**。此前是不分状态全收，于是一个已退出的 session 仍被判成 live
    // ——标签不解绑、卡片不出现、终端永远连不上（tab 10 的 disconnected 就是这么来的）。
    const localIds = localSessions ? new Set(localSessions.filter(s => !s.exited).map(s => s.id)) : null

    if (allTabs.value.length > 0) {
      await reconcileTabs(
        allTabs.value.map((t: WorkbenchTab) => ({ id: t.id, sessionId: t.sessionId, remotePeerId: t.remotePeerId })),
        {
          listLocalSessions: () => Promise.resolve(localIds),
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
      await reopenDetachedTabs(reopenCandidates(settled), {
        createSession: (tab) => createSessionOn(tab, { name: tab.name, cwd: tab.cwd || '~' }),
        adopt: adoptReopened,
      })
    }

    // 反方向，最后跑：上面那一步可能刚给 detached 标签接上新 shell，那些新 session 不在
    // localSessions 这份快照里，所以不会被误当成孤儿。
    adoptOrphanSessions(localSessions)
    ensureActiveTab()
  }

  /**
   * 保证「有标签就一定有一个是活跃的」。
   *
   * 这一轮里有两处会把 activeTabId 留空，而界面对空活跃标签是**整块空白**：终端区不渲染（没有
   * 当前标签），连"进程已结束"那张卡也不出现（detachedCard 的第一个判据就是 activeTab 存在）。
   *
   *   ① 孤儿认领用 `activate: false`（不该抢用户正在看的终端）—— 但当标签**全部**来自认领时，
   *     就没有任何人被激活过。刚好就是"标签记录整份丢掉后自救"那个场景，也就是最需要它好用的
   *     那一刻。
   *   ② settleExitedTab 关掉的恰好是当时的活跃标签。
   *
   * 由夹具实测抓到（`will-exit-0` 被关掉后整页空白），不是推演出来的。
   */
  function ensureActiveTab(): void {
    if (activeTab.value) return
    const first = visibleTabIds.value[0] ?? allTabs.value[0]?.id
    if (first) setActiveTab(first)
  }

  /** 当前所有标签的「自动重开候选」视图。判定（谁该重开）在 reopenDetached.ts，这里只搬数据。
   *
   *  `exclude` 是**本轮已按退出码结算过**的标签（见 settleExitedTab）。它们不该再被自动重开：
   *  自动重开交付的是一个新 shell 加一句"上一个进程已随服务重启结束"，而这些标签的进程是自己
   *  退出的、服务好好的——那句话会是谎话，而这个模块存在的全部理由就是不撒那种谎。 */
  function reopenCandidates(exclude: ReadonlySet<string> = new Set()): ReopenCandidate[] {
    return allTabs.value.filter((t: WorkbenchTab) => !exclude.has(t.id)).map((t: WorkbenchTab) => ({
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

  /** 本机一条 session 里，对账 + 认领需要用到的部分。 */
  interface LocalSessionBrief {
    id: string
    name: string
    cwd: string
    exited: boolean
    /** 只在 `exited` 时有意义。决定这个标签该被关掉（干净退出）还是留下来说明情况（崩了），
     *  和实时 `shell_exit` 帧走的是同一条策略 —— 见 settleExitedTab。 */
    exitCode: number
    /** 这条 session 属于本部署的标签列表吗。daemon 是 per-user 的，standalone(:18074) 与
     *  pro embed(:8087) 共用一个，各自却有各自的 workbench.json —— 没有这一位，"活着但没有标签"
     *  就分不清是「我的标签记录丢了」还是「那是另一个部署的终端」。见 Go 侧 sessionMeta.Origin。 */
    ownedHere: boolean
  }

  /** 本机 session 全量清单，或 null（没问到）。比 fetchSessionIds 多取 name/cwd/status/ownedHere
   *  —— 孤儿认领要靠它们把标签建得像原来那条，而不是一个叫"终端"的空壳。 */
  async function fetchLocalSessions(): Promise<LocalSessionBrief[] | null> {
    try {
      const resp = await cliFetch(cliApi('/sessions'))
      if (!resp.ok) return null
      const list = await resp.json() as Array<Record<string, unknown>>
      const out: LocalSessionBrief[] = []
      for (const s of list) {
        const id = (typeof s.id === 'string' && s.id) || (typeof s.session_id === 'string' && s.session_id)
        if (!id) continue
        out.push({
          id,
          name: typeof s.name === 'string' ? s.name : '',
          cwd: typeof s.cwd === 'string' ? s.cwd : '',
          exited: s.status === 'exited',
          // 旧服务端根本不发这个键 → 当作"是我的"。判据写成 `!== false` 而不是真值判断：
          // 服务端刻意不给这个布尔加 omitempty，false 是有意义的值，必须能过 wire。
          ownedHere: s.ownedHere !== false,
          // 缺这个键（旧服务端）时按**非零**处理：宁可留下标签让用户自己决定，也不要凭一个
          // 猜出来的 0 把标签连同它的名字和目录一起关掉。
          exitCode: typeof s.exitCode === 'number' ? s.exitCode : 1,
        })
      }
      return out
    } catch {
      return null
    }
  }

  /**
   * 认领「活着、属于本部署、但没有任何标签指着它」的 session。
   *
   * 这是标签对账的**反方向**，此前完全缺失。reconcileTabs 只问「我这条标签背后的进程还活着吗」；
   * 没人问过「这个还活着的进程还有标签指着它吗」。于是一旦标签记录丢了（见 useWorkbench 文件头
   * 那次真实事故：陈旧副本整份覆盖），那条 session 就永久隐身——尽管 daemon（"什么终端存在"的
   * 唯一真相）还好好地拿着它，进程还在跑。
   *
   * 它同时是那次数据丢失的**安全网**：即便 rev 并发控制哪天又被绕开，最坏结果也只是标签暂时消失，
   * 下次挂载自动回来，而不是一个正在对话的 agent 永久失联。
   *
   * 刻意的几个选择：
   *  • `null`（没问到）直接返回。请求失败不是"没有孤儿"的证据（同 tabLiveness 的 null ≠ 空集合）。
   *  • 跳过已退出的：那不是失联的终端，只是垃圾，认领它等于凭空造一个死标签。
   *  • `activate: false`：这是后台对账顺手做的事，不该把用户正在看的终端切走。
   *  • 不往终端里写任何"已恢复"的痕迹。reopenDetached 那条痕迹存在的理由是它交付的是一个**新的
   *    空 shell**（假装恢复是那次的原罪）；这里交付的是**原来那个真进程**，无需自证。
   */
  function adoptOrphanSessions(sessions: LocalSessionBrief[] | null): void {
    if (!sessions) return
    const bound = new Set<string>()
    for (const t of allTabs.value) if (t.sessionId) bound.add(t.sessionId)
    const orphans = sessions.filter(s => s.ownedHere && !s.exited && !bound.has(s.id))
    if (!orphans.length) return
    const gid = groups.value[0]?.id
    if (!gid) return
    for (const s of orphans) {
      const tab = addTab(gid, {
        name: s.name || nextTabName(),
        cwd: s.cwd || '~',
        activate: false,
      })
      bindSession(tab.id, s.id)
      ensureRuntime(tab.id)
      setTabLiveness(tab.id, 'live')
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
    document.addEventListener('visibilitychange', onPageVisible)
  })
  onBeforeUnmount(() => document.removeEventListener('visibilitychange', onPageVisible))

  /**
   * 页面重新可见时：先把服务端的标签列表合并进来，再对一次账。
   *
   * 补的是「这份共享文档一辈子只在挂载时读一次」这个洞。手机上一个后台挂了两小时的页面，本地那份
   * 列表已经陈旧得离谱，而它随时会因为一次**完全自动的** setTabCwd（sessions_overview 推送帧
   * 触发）把陈旧内容写回服务端 —— 那正是那次真实事故的最后一步。rev 并发控制会挡住覆盖，但挡住
   * 之后总得有人去合并；让合并发生在**用户看到界面之前**，好过让它发生在一次失败的写之后。
   *
   * 顺带对账：离开的这段时间里进程可能已经结束或被别处关了，回来第一眼该是真相，不是两小时前的
   * 快照。
   */
  let resuming = false
  async function onPageVisible(): Promise<void> {
    if (document.visibilityState !== 'visible') return
    // 切前后台可能连着来好几次（切 Space、锁屏、PWA 恢复）。一次跑完再接下一次：并发跑两遍只会
    // 让合并基准前后错位。
    if (resuming) return
    resuming = true
    try {
      await resync()
      await reconcileSessions()
    } finally {
      resuming = false
    }
  }

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
    tabPositions, tabDisplayName, tabStatuses, leaderPending, leaderLabel,
    overviewOpen, toggleOverview, closeOverview, overviewGroups: overview.groups, overviewRollup: overview.rollup,
    selectOverviewIndex,
    openShortcutsSettings,
    // tab context menu (right-click) — same action table as the shortcuts
    tabMenu, openTabMenu,
    // 一次性轻提示（环境变量注入的结果/拒绝理由）。不是 alert：见 showNotice。
    notice,
  }
}
