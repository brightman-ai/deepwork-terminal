/**
 * useWorkbench — Workbench 状态管理 composable.
 * 管理 group/tab 树结构，持久化到后端 /api/cli/workbench。
 * save() 有 500ms debounce，避免频繁写入。
 * [Ref: TH-0502-w6d Round 4-5]
 */
import { ref, computed } from 'vue'
import type { WorkbenchConfig, WorkbenchGroup, WorkbenchTab } from '@terminal/types/workbench'
import { createTab, createGroup, createDefaultWorkbenchConfig } from '@terminal/types/workbench'
import { fetchWorkbenchConfig, saveWorkbenchConfig } from '@terminal/api/workbench'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export function useWorkbench() {
  const config = ref<WorkbenchConfig | null>(null)
  const loading = ref(true)
  const error = ref<string | null>(null)

  // ─── debounce state ───────────────────────────────────────────────────────
  let saveTimer: ReturnType<typeof setTimeout> | null = null

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
      config.value = await fetchWorkbenchConfig()
    } catch (err) {
      error.value = err instanceof Error ? err.message : '加载失败'
      config.value = createDefaultWorkbenchConfig()
    } finally {
      loading.value = false
    }
  }

  async function save(): Promise<void> {
    if (saveTimer !== null) {
      clearTimeout(saveTimer)
    }
    saveTimer = setTimeout(async () => {
      if (!config.value) return
      config.value.lastSaved = new Date().toISOString()
      try {
        await saveWorkbenchConfig(config.value)
      } catch (err) {
        error.value = err instanceof Error ? err.message : '保存失败'
      }
    }, 500)
  }

  // ─── tab operations ───────────────────────────────────────────────────────

  function addTab(
    groupId: string,
    opts?: { name?: string; cwd?: string; engine?: string; remotePeerId?: string }
  ): WorkbenchTab {
    const cfg = ensureConfig()
    const group = cfg.groups.find(g => g.id === groupId)
    if (!group) throw new Error(`Group not found: ${groupId}`)
    const tab = createTab({ ...opts, groupId })
    group.tabs.push(tab)
    cfg.activeGroupId = groupId
    cfg.activeTabId = tab.id
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
    groups,
    activeGroup,
    activeTab,
    allTabs,
    showGroupHeaders,
    load,
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
