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
    opts?: { name?: string; cwd?: string; engine?: string }
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
    removeTab,
    renameTab,
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
