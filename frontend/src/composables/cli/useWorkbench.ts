/**
 * useWorkbench — Workbench 状态管理 composable.
 * 管理 group/tab 树结构，持久化到后端 /api/cli/workbench。
 * save() 有 500ms debounce，避免频繁写入。
 * [Ref: TH-0502-w6d Round 4-5]
 *
 * # 这份文档是共享的，而这个 composable 曾经假装它不是
 *
 * 标签列表存在服务端（一份 JSON），但**每台设备只在页面加载时读一次**，之后整份躺在内存里，任何
 * 改动都把整份 PUT 回去。两台设备同开 → 两份各自演化的副本 → 后写的静默抹掉前一方。
 *
 * 真实事故（2026-09-03）：手机上一个从 21:29 之前就开着的页面，内存里那份没有 PC 后来新建的标签。
 * 断电后手机侧只要发生**任何一次** save —— 点一下标签（setActiveTab）、甚至完全不用人动手
 * （setTabCwd 由 sessions_overview 推送帧自动触发）——就把那条标签连同它绑着的 session 一起从
 * 服务端抹掉了。那个 session（一个正在对话的 claude）其实一直活着，只是再也没有标签指着它。
 *
 * 三处结构性缺陷，逐条对应下面的三处修复：
 *
 *  ① **陈旧副本整份覆盖** → `rev` 乐观并发：PUT 带上 load 到的版本号，服务端发现基准已移动就回
 *    409 + 当前文档，客户端拿 `baseline` 做三方合并后重试（mergeWorkbench）。只有客户端还留着
 *    "我当初读到的那一份"，所以只有客户端分辨得出"这个标签是我关的"还是"我从没见过它"。
 *
 *  ② **load 失败仍照常存盘** → `hydrated` 闸门：没成功读到服务端状态时，`config` 只是让界面能
 *    渲染的兜底默认值，此时 save() 一律 no-op。原来 load 失败会静默装上一份空配置，下一次改动
 *    就把用户所有标签清空 PUT 上去（和 store.json 那次"两个 key 轮流消失"同一个根）。
 *
 *  ③ **一辈子不再读第二次** → `resync()`：页面重新可见时合并一次服务端最新状态。手机上一个后台
 *    挂了两小时的页面，此前会一直显示两小时前的标签列表。
 */
import { ref, computed } from 'vue'
import type { WorkbenchConfig, WorkbenchGroup, WorkbenchTab } from '@terminal/types/workbench'
import { createTab, createGroup, createDefaultWorkbenchConfig } from '@terminal/types/workbench'
import { fetchWorkbenchConfig, saveWorkbenchConfig } from '@terminal/api/workbench'
import { mergeWorkbench } from '@terminal/composables/cli/workbenchMerge'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

/** 冲突重试上限。收敛通常发生在第一次重试；给个上限是为了让"两台设备高频对写"这种病态情形
 *  以一条 error 结束，而不是变成无限 PUT 循环。 */
const MAX_CONFLICT_RETRIES = 3

/** 纯 JSON 深拷贝。config 全是 plain data（无 Date/Map/循环引用），够用且确定。 */
function snapshot(cfg: WorkbenchConfig): WorkbenchConfig {
  return JSON.parse(JSON.stringify(cfg)) as WorkbenchConfig
}

export function useWorkbench() {
  const config = ref<WorkbenchConfig | null>(null)
  const loading = ref(true)
  const error = ref<string | null>(null)

  /** 「`config` 真的代表过服务端状态」。false = 只读到了兜底默认值 → 禁止存盘（缺陷②）。 */
  const hydrated = ref(false)

  /** 本地这份 config 是从服务端哪一版长出来的。三方合并的 base；`null` = 不知道。
   *  刻意不是 ref：它是合并算法的输入，不参与渲染，暴露成响应式只会诱使别处去读它。 */
  let baseline: WorkbenchConfig | null = null

  // ─── debounce state ───────────────────────────────────────────────────────
  let saveTimer: ReturnType<typeof setTimeout> | null = null
  let flushing = false
  let pending = false

  // ─── computed ─────────────────────────────────────────────────────────────

  const groups = computed<WorkbenchGroup[]>(() => config.value?.groups ?? [])

  const activeGroup = computed<WorkbenchGroup | undefined>(() =>
    groups.value.find(g => g.id === config.value?.activeGroupId)
  )

  const activeTab = computed<WorkbenchTab | undefined>(() => {
    const tabId = config.value?.activeTabId
    if (!tabId) return undefined
    for (const g of groups.value) {
      const t = g.tabs.find(t => t.id === tabId)
      if (t) return t
    }
    return undefined
  })

  const allTabs = computed<WorkbenchTab[]>(() =>
    groups.value.flatMap(g => g.tabs)
  )

  const showGroupHeaders = computed<boolean>(() => groups.value.length > 1)

  // ─── helpers ──────────────────────────────────────────────────────────────

  function ensureConfig(): WorkbenchConfig {
    if (!config.value) {
      config.value = createDefaultWorkbenchConfig()
    }
    return config.value
  }

  function findGroupForTab(tabId: string): WorkbenchGroup | undefined {
    return groups.value.find(g => g.tabs.some(t => t.id === tabId))
  }

  // ─── persistence ──────────────────────────────────────────────────────────

  async function load(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const got = await fetchWorkbenchConfig()
      config.value = got.config
      hydrated.value = got.hydrated
      // 只有真的读到了服务端状态，才有资格当合并基准。没读到就留 null：mergeWorkbench 会退化成
      // 并集（宁可多留一个该关的标签，也不再丢一个还活着的 session）。
      baseline = got.hydrated ? snapshot(got.config) : null
    } catch (err) {
      error.value = err instanceof Error ? err.message : '加载失败'
      // 兜底配置只为了让界面能渲染；hydrated=false 会让 save() 全程 no-op，所以它不可能被
      // PUT 上去覆盖用户真实的标签（缺陷②）。
      config.value = createDefaultWorkbenchConfig()
      hydrated.value = false
      baseline = null
    } finally {
      loading.value = false
    }
  }

  /**
   * 重新读一次服务端并合并进本地（缺陷③）。
   *
   * 给"页面重新可见"用：手机上一个后台挂了两小时的页面，本地那份文档已经陈旧得离谱，而它随时会
   * 因为一次自动的 setTabCwd 把陈旧内容写回去。**先合并再有机会写**，是这里的全部意义。
   *
   * 合并而不是直接替换：本地可能有还没存上去的改动（刚新建的标签、刚改的名字），替换会把它们丢掉。
   * 读不到就保持现状——读失败不是"服务端空了"的证据（同 tabLiveness 的 null ≠ 空集合）。
   */
  async function resync(): Promise<void> {
    if (!config.value) return
    try {
      const got = await fetchWorkbenchConfig()
      if (!got.hydrated) return
      config.value = mergeWorkbench(baseline, config.value, got.config)
      // 基准前移到"服务端那一版"：合并结果正是建立在它之上的，本地独有的部分相对它仍然算
      // "本地新增"（因为确实还没上去），这正是下一次冲突合并需要的语义。
      baseline = snapshot(got.config)
      hydrated.value = true
    } catch {
      /* 读不到就保持本地现状，不动 baseline、不动 hydrated */
    }
  }

  /**
   * 真正写一次盘，并在基准过期时三方合并后重试（缺陷①）。
   *
   * 冲突不是错误：服务端只是在说"你编辑的那一版已经不是当前版了，这是当前版"。会丢数据的做法是
   * 重新拉一份然后把本地整份盖上去——那和原来的 bug 一模一样。必须拿 `baseline` 三方合并。
   */
  async function flush(attempt = 0): Promise<void> {
    if (!config.value) return
    if (!hydrated.value) return // 闸门：没读到服务端状态，绝不写
    config.value.lastSaved = new Date().toISOString()
    const outgoing = snapshot(config.value)
    const outcome = await saveWorkbenchConfig(outgoing)
    switch (outcome.kind) {
      case 'saved':
        if (outcome.rev !== undefined) {
          outgoing.rev = outcome.rev
          if (config.value) config.value.rev = outcome.rev
        }
        baseline = outgoing
        error.value = null
        return
      case 'conflict': {
        if (attempt >= MAX_CONFLICT_RETRIES) {
          error.value = '标签列表保存冲突未能收敛，已保留本地状态'
          return
        }
        config.value = mergeWorkbench(baseline, config.value, outcome.current)
        // 合并结果建立在服务端那一版之上，所以基准也跟着走到那一版，否则下一轮又是陈旧的。
        baseline = snapshot(outcome.current)
        return flush(attempt + 1)
      }
      case 'skipped':
        return // 401/429 —— 认证弹窗会接手，不覆盖也不报错
      case 'failed':
        error.value = outcome.message
        return
    }
  }

  /** 串行化 flush：冲突重试是异步的，期间又来的改动不能并发发起第二条 PUT（两条并发必然互相
   *  409，还可能让 baseline 前后错位）。合并成"跑完再跑一轮"。 */
  async function kick(): Promise<void> {
    if (flushing) { pending = true; return }
    flushing = true
    try {
      do {
        pending = false
        await flush()
      } while (pending)
    } finally {
      flushing = false
    }
  }

  async function save(): Promise<void> {
    if (saveTimer !== null) {
      clearTimeout(saveTimer)
    }
    saveTimer = setTimeout(() => { void kick() }, 500)
  }

  // ─── tab operations ───────────────────────────────────────────────────────

  function addTab(
    groupId: string,
    opts?: {
      name?: string; cwd?: string; engine?: string; remotePeerId?: string
      /** 新标签是否同时抢占焦点。默认 true —— 人点"新建终端"就是想去那儿。
       *
       *  唯一传 false 的是**孤儿认领**（见 useCliState adoptOrphanSessions）：那是后台对账顺手
       *  补上一条别处创建的标签，把用户正在看的终端切走属于抢夺，而且对账每轮都跑。 */
      activate?: boolean
    }
  ): WorkbenchTab {
    const cfg = ensureConfig()
    const group = cfg.groups.find(g => g.id === groupId)
    if (!group) throw new Error(`Group not found: ${groupId}`)
    const { activate = true, ...tabOpts } = opts ?? {}
    const tab = createTab({ ...tabOpts, groupId })
    group.tabs.push(tab)
    if (activate) {
      cfg.activeGroupId = groupId
      cfg.activeTabId = tab.id
    }
    save()
    return tab
  }

  function removeTab(tabId: string): void {
    const cfg = ensureConfig()
    const group = findGroupForTab(tabId)
    if (!group) return
    const idx = group.tabs.findIndex(t => t.id === tabId)
    group.tabs.splice(idx, 1)
    // 若删的是当前活跃 tab，切到同 group 中相邻 tab 或清空
    if (cfg.activeTabId === tabId) {
      const next = group.tabs[Math.max(0, idx - 1)]
      cfg.activeTabId = next?.id ?? ''
    }
    save()
  }

  function renameTab(tabId: string, name: string): void {
    const group = findGroupForTab(tabId)
    const tab = group?.tabs.find(t => t.id === tabId)
    if (!tab) return
    tab.name = name
    save()
    syncSessionName(tab, name)
  }

  /** Tells the backend session its new name too — otherwise this was purely a local
   *  `tab.name` mutation, and every backend consumer of "what is this session called"
   *  (the notifier's identityTag/notification title, GET /sessions) kept showing the
   *  pre-rename name forever, no matter how many times the tab got renamed here.
   *  Local sessions only: a remote tab's session lives on its peer's own backend
   *  (different host, different auth code) — out of scope for this sync. Best-effort:
   *  a failed sync just leaves the old name standing until the next successful rename
   *  or session recreate, not worth surfacing as an error for a cosmetic label. */
  function syncSessionName(tab: WorkbenchTab, name: string): void {
    if (!tab.sessionId || tab.remotePeerId) return
    const { cliFetch } = useCliAuth()
    cliFetch(cliApi(`/sessions/${tab.sessionId}/rename`), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    }).catch(() => {})
  }

  /** Records where the terminal ACTUALLY is now, so the stored cwd stops being "where this tab was
   *  created" and becomes "where its work lives".
   *
   *  This is what makes 自动重开 restore the working state instead of just a shell: after a server
   *  restart the session list is empty, so the live cwd is unrecoverable — the only copy left is
   *  whatever we persisted before it died. Without this, a tab created at "~" and then `cd`'d into
   *  a project reopens in the home directory, and the trace line honestly but uselessly reports
   *  "已回到主目录 ~".
   *
   *  No-ops when unchanged: save() is debounced but still a network write, and a terminal sitting
   *  in one directory must not generate a request per poll. */
  function setTabCwd(tabId: string, cwd: string): void {
    if (!cwd) return
    const group = findGroupForTab(tabId)
    const tab = group?.tabs.find(t => t.id === tabId)
    if (!tab || tab.cwd === cwd) return
    tab.cwd = cwd
    save()
  }

  /** Adopts a name that arrived FROM the backend (another device's rename) into this device's
   *  own tab list — the read side `syncSessionName` never had. A rename only ever wrote
   *  `tab.name` on the device that typed it (`renameTab` above); every other device keeps
   *  showing whatever name its own tab list already had, forever, because nothing ever told it
   *  the backend's title had moved. This is that missing read side, one device's version of the
   *  same gap `syncSessionName`'s doc comment already named for the write side.
   *
   *  Deliberately does NOT call `syncSessionName`: this name came FROM the backend, echoing it
   *  back would just be a redundant no-op write, not a new fact. */
  function adoptRemoteTabName(tabId: string, name: string): void {
    if (!name) return
    const group = findGroupForTab(tabId)
    const tab = group?.tabs.find(t => t.id === tabId)
    if (!tab || tab.name === name) return
    tab.name = name
    save()
  }

  function setActiveTab(tabId: string): void {
    const cfg = ensureConfig()
    const group = findGroupForTab(tabId)
    if (!group) return
    cfg.activeTabId = tabId
    cfg.activeGroupId = group.id
    save()
  }

  // ─── group operations ─────────────────────────────────────────────────────

  function addGroup(name: string): WorkbenchGroup {
    const cfg = ensureConfig()
    const group = createGroup(name)
    cfg.groups.push(group)
    cfg.activeGroupId = group.id
    save()
    return group
  }

  function removeGroup(groupId: string): void {
    const cfg = ensureConfig()
    const idx = cfg.groups.findIndex(g => g.id === groupId)
    if (idx === -1) return
    const [removed] = cfg.groups.splice(idx, 1)
    // 将被删 group 中的 tabs 移入第一个剩余 group（若存在）
    if (cfg.groups.length > 0 && removed.tabs.length > 0) {
      cfg.groups[0].tabs.push(...removed.tabs.map(t => ({ ...t, groupId: cfg.groups[0].id })))
    }
    // 修正 activeGroupId
    if (cfg.activeGroupId === groupId) {
      cfg.activeGroupId = cfg.groups[0]?.id ?? ''
    }
    save()
  }

  function renameGroup(groupId: string, name: string): void {
    const group = groups.value.find(g => g.id === groupId)
    if (!group) return
    group.name = name
    save()
  }

  function setActiveGroup(groupId: string): void {
    const cfg = ensureConfig()
    if (!cfg.groups.find(g => g.id === groupId)) return
    cfg.activeGroupId = groupId
    save()
  }

  function toggleGroupCollapsed(groupId: string): void {
    const group = groups.value.find(g => g.id === groupId)
    if (!group) return
    group.collapsed = !group.collapsed
    save()
  }

  // ─── session binding ──────────────────────────────────────────────────────

  function bindSession(tabId: string, sessionId: string): void {
    const group = findGroupForTab(tabId)
    const tab = group?.tabs.find(t => t.id === tabId)
    if (!tab) return
    tab.sessionId = sessionId
    save()
  }

  function unbindSession(tabId: string): void {
    const group = findGroupForTab(tabId)
    const tab = group?.tabs.find(t => t.id === tabId)
    if (!tab) return
    tab.sessionId = undefined
    save()
  }

  // ─── public API ───────────────────────────────────────────────────────────

  return {
    config,
    loading,
    error,
    /** 读到过服务端状态吗。false 时 save() 全程 no-op —— 消费方若要在界面上说明"当前未同步"
     *  可以读它，但**不要**用它去绕过闸门。 */
    hydrated,
    groups,
    activeGroup,
    activeTab,
    allTabs,
    showGroupHeaders,
    load,
    /** 重新读服务端并合并进本地。挂在"页面重新可见"上，见 useCliState 的 onMounted。 */
    resync,
    save,
    addTab,
    setTabCwd,
    removeTab,
    renameTab,
    adoptRemoteTabName,
    setActiveTab,
    addGroup,
    removeGroup,
    renameGroup,
    setActiveGroup,
    toggleGroupCollapsed,
    bindSession,
    unbindSession,
  }
}
