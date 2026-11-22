/**
 * useTaskWorkbench — Reactive state & actions for Task Workbench v11:
 *  - Collapsible task sidebar (`sidebarOpen`, `sidebarFilter`, `sidebarQuery`)
 *  - First-class spaces (`activeSpaceKind: 'task' | 'bot'`, `activeBotId`)
 *  - Single reused center italic Preview Tab (`centerPreviewFile`)
 *  - Per-tab dual view mode (`tabViewModes: Record<string, 'terminal' | 'transcript'>`)
 *  - Git CoW worktrees, isolated CLI profiles, and resident bots CRUD + CAS spawn
 */
import { computed, ref } from 'vue'
import {
  fetchWorktrees,
  fetchAllWorktrees as apiFetchAllWorktrees,
  createWorktree as apiCreateWorktree,
  mergeWorktree as apiMergeWorktree,
  removeWorktree as apiRemoveWorktree,
  type WorktreeInfo,
  type RepoWorktreeGroup,
  type WorktreeListResponse,
  type CreateWorktreeRequest,
  type CreateWorktreeResponse,
  type MergeWorktreeRequest,
  type MergeWorktreeResponse,
  type RemoveWorktreeRequest,
  type RemoveWorktreeResponse,
} from '@terminal/api/worktrees'
import {
  fetchProfiles,
  createProfile as apiCreateProfile,
  updateProfile as apiUpdateProfile,
  deleteProfile as apiDeleteProfile,
  scanLocalProfiles as apiScanLocalProfiles,
  testProfileGateway as apiTestProfileGateway,
  type ProfileItem,
  type ProfileListResponse,
  type CreateProfileRequest,
  type UpdateProfileRequest,
  type ScanProfilesResponse,
  type TestProfileRequest,
  type TestProfileResponse,
} from '@terminal/api/profiles'
import {
  fetchBots,
  updateBotDocs as apiUpdateBotDocs,
  sendBotChat as apiSendBotChat,
  runBotCheck as apiRunBotCheck,
  patchBotItem as apiPatchBotItem,
  type BotDetail,
  type BotItem,
  type UpdateBotDocsRequest,
  type SendBotChatRequest,
  type SendBotChatResponse,
  type RunBotCheckRequest,
  type RunBotCheckResponse,
  type PatchBotItemRequest,
  type PatchBotItemResponse,
} from '@terminal/api/bots'

export type SpaceKind = 'task' | 'bot'
export type TabViewMode = 'terminal' | 'transcript'
export type SidebarFilter = 'all' | 'waiting' | 'active'
export type ProfileModalTab = 'list' | 'oauth' | 'gateway'

export interface CenterPreviewFile {
  path: string
  line?: number
  sessionId?: string
  cwd?: string
}

export interface SpawnFromBotOptions {
  session?: string
  cwd?: string
  baseRef?: string
  createTabForWorktree?: (info: {
    path: string
    branch: string
    bot: BotDetail
    item: BotItem
  }) => Promise<string | undefined> | string | undefined
}

export interface SpawnFromBotResult {
  alreadySpawned: boolean
  worktreePath: string
  branch: string
  tabId?: string
  item: BotItem
  bot: BotDetail
}

export interface TaskWorkbenchDeps {
  fetchWorktrees: (opts?: { session?: string; cwd?: string }) => Promise<WorktreeListResponse>
  fetchAllWorktrees?: (opts?: { cwd?: string }) => Promise<{ repos: RepoWorktreeGroup[] }>
  createWorktree: (req: CreateWorktreeRequest) => Promise<CreateWorktreeResponse>
  mergeWorktree: (req: MergeWorktreeRequest) => Promise<MergeWorktreeResponse>
  removeWorktree: (req: RemoveWorktreeRequest) => Promise<RemoveWorktreeResponse>
  fetchProfiles: () => Promise<ProfileListResponse>
  createProfile: (req: CreateProfileRequest) => Promise<ProfileItem>
  updateProfile: (id: string, req: UpdateProfileRequest) => Promise<ProfileItem>
  deleteProfile: (id: string) => Promise<void>
  scanLocalProfiles: () => Promise<ScanProfilesResponse>
  testProfileGateway: (req: TestProfileRequest) => Promise<TestProfileResponse>
  fetchBots: () => Promise<BotDetail[]>
  updateBotDocs: (id: string, req: UpdateBotDocsRequest) => Promise<BotDetail>
  sendBotChat: (id: string, req: SendBotChatRequest) => Promise<SendBotChatResponse>
  runBotCheck: (id: string, req: RunBotCheckRequest) => Promise<RunBotCheckResponse>
  patchBotItem: (id: string, itemId: string, req: PatchBotItemRequest) => Promise<PatchBotItemResponse>
}

const SIDEBAR_STORAGE_KEY = 'dw_task_sidebar_open'

function readInitialSidebarOpen(): boolean {
  try {
    if (typeof window === 'undefined') return true
    const saved = window.localStorage?.getItem(SIDEBAR_STORAGE_KEY)
    if (saved === '0') return false
    if (saved === '1') return true
    return window.innerWidth >= 769
  } catch {
    return true
  }
}

function persistSidebarOpen(open: boolean): void {
  try {
    if (typeof window !== 'undefined') {
      window.localStorage?.setItem(SIDEBAR_STORAGE_KEY, open ? '1' : '0')
    }
  } catch {
    // Ignore storage failures (e.g. private browsing / test sandbox)
  }
}

function defaultDeps(): TaskWorkbenchDeps {
  return {
    fetchWorktrees,
    fetchAllWorktrees: apiFetchAllWorktrees,
    createWorktree: apiCreateWorktree,
    mergeWorktree: apiMergeWorktree,
    removeWorktree: apiRemoveWorktree,
    fetchProfiles,
    createProfile: apiCreateProfile,
    updateProfile: apiUpdateProfile,
    deleteProfile: apiDeleteProfile,
    scanLocalProfiles: apiScanLocalProfiles,
    testProfileGateway: apiTestProfileGateway,
    fetchBots,
    updateBotDocs: apiUpdateBotDocs,
    sendBotChat: apiSendBotChat,
    runBotCheck: apiRunBotCheck,
    patchBotItem: apiPatchBotItem,
  }
}

export function createTaskWorkbench(overrides?: Partial<TaskWorkbenchDeps>) {
  const deps: TaskWorkbenchDeps = { ...defaultDeps(), ...overrides }

  // ── UI navigation & view state ──
  const sidebarOpen = ref<boolean>(readInitialSidebarOpen())
  const sidebarFilter = ref<SidebarFilter>('all')
  const sidebarQuery = ref<string>('')
  const activeSpaceKind = ref<SpaceKind>('task')
  const activeBotId = ref<string>('')
  const centerPreviewFile = ref<CenterPreviewFile | null>(null)
  const tabViewModes = ref<Record<string, TabViewMode>>({})

  // ── Modals ──
  const profileModalOpen = ref<boolean>(false)
  const profileModalInitialTab = ref<ProfileModalTab>('list')
  const newTaskModalOpen = ref<boolean>(false)

  // ── Domain collections ──
  const worktreeRoot = ref<string>('')
  const worktrees = ref<WorktreeInfo[]>([])
  const repoGroups = ref<RepoWorktreeGroup[]>([])
  const worktreesNotGit = ref<boolean>(false)
  const worktreesLoading = ref<boolean>(false)

  const profiles = ref<ProfileItem[]>([])
  const defaultProfileId = ref<string>('')
  const profilesLoading = ref<boolean>(false)

  const bots = ref<BotDetail[]>([])
  const botsLoading = ref<boolean>(false)
  const spawningItemId = ref<string | null>(null)
  const lastError = ref<string | null>(null)

  const lastWorktreeQuery = ref<{ session?: string; cwd?: string }>({})
  const inFlightSpawns = new Map<string, Promise<SpawnFromBotResult>>()

  // ── Derived state ──
  const activeBot = computed<BotDetail | null>(() => {
    if (!bots.value.length) return null
    return bots.value.find((b) => b.id === activeBotId.value) ?? bots.value[0] ?? null
  })

  const totalBotNeedsYou = computed<number>(() =>
    bots.value.reduce(
      (sum, b) =>
        sum +
        b.items.filter((it) => it.status === 'needs_you' && !it.spawnedWorktreePath).length,
      0,
    ),
  )

  const managedWorktreeCount = computed<number>(() => {
    if (repoGroups.value.length > 0) {
      return repoGroups.value.reduce(
        (sum, repo) => sum + repo.worktrees.filter((w) => !w.isPrimary).length,
        0,
      )
    }
    return worktrees.value.filter((w) => !w.isPrimary).length
  })

  // ── Helpers ──
  function replaceBot(updated: BotDetail): void {
    const idx = bots.value.findIndex((b) => b.id === updated.id)
    if (idx >= 0) {
      const next = bots.value.slice()
      next[idx] = updated
      bots.value = next
    } else {
      bots.value = [...bots.value, updated]
    }
  }

  function setSidebarOpen(open: boolean): void {
    sidebarOpen.value = open
    persistSidebarOpen(open)
  }

  function toggleSidebar(): void {
    setSidebarOpen(!sidebarOpen.value)
  }

  function selectBot(botId: string): void {
    activeBotId.value = botId
    activeSpaceKind.value = 'bot'
  }

  function selectTaskSpace(): void {
    activeSpaceKind.value = 'task'
  }

  function openCenterPreview(
    path: string,
    opts?: { line?: number; sessionId?: string; cwd?: string },
  ): void {
    centerPreviewFile.value = {
      path,
      line: opts?.line,
      sessionId: opts?.sessionId,
      cwd: opts?.cwd,
    }
    activeSpaceKind.value = 'task'
  }

  function closeCenterPreview(): void {
    centerPreviewFile.value = null
  }

  function getTabViewMode(tabId: string): TabViewMode {
    return tabViewModes.value[tabId] ?? 'terminal'
  }

  function setTabViewMode(tabId: string, mode: TabViewMode): void {
    tabViewModes.value = { ...tabViewModes.value, [tabId]: mode }
  }

  function openProfileModal(tab: ProfileModalTab = 'list'): void {
    profileModalInitialTab.value = tab
    profileModalOpen.value = true
  }

  function closeProfileModal(): void {
    profileModalOpen.value = false
  }

  function openNewTaskModal(): void {
    newTaskModalOpen.value = true
  }

  function closeNewTaskModal(): void {
    newTaskModalOpen.value = false
  }

  // ── Worktrees ──
  async function loadWorktrees(opts?: {
    session?: string
    cwd?: string
  }): Promise<WorktreeListResponse> {
    worktreesLoading.value = true
    lastError.value = null
    if (opts) lastWorktreeQuery.value = { ...opts }
    try {
      const res = await deps.fetchWorktrees(opts ?? lastWorktreeQuery.value)
      worktreeRoot.value = res.root
      worktrees.value = res.worktrees
      worktreesNotGit.value = Boolean(res.notGit)
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      worktreesLoading.value = false
    }
  }

  async function loadAllWorktrees(opts?: {
    cwd?: string
  }): Promise<{ repos: RepoWorktreeGroup[] }> {
    const fetcher = deps.fetchAllWorktrees
    if (!fetcher) return { repos: [] }
    try {
      const cwd = opts?.cwd ?? lastWorktreeQuery.value.cwd ?? (worktreeRoot.value || undefined)
      const res = await fetcher(cwd ? { cwd } : undefined)
      repoGroups.value = Array.isArray(res?.repos) ? res.repos : []
      return { repos: repoGroups.value }
    } catch {
      return { repos: repoGroups.value }
    }
  }

  async function createCoWWorktree(
    req: CreateWorktreeRequest,
  ): Promise<CreateWorktreeResponse> {
    lastError.value = null
    try {
      const created = await deps.createWorktree(req)
      const refreshCwd = req.cwd ?? lastWorktreeQuery.value.cwd ?? worktreeRoot.value
      await Promise.allSettled([
        loadWorktrees({
          session: req.session ?? lastWorktreeQuery.value.session,
          cwd: refreshCwd,
        }).catch(() => {
          // Optimistically insert if list refresh fails transiently
          if (!worktrees.value.some((w) => w.path === created.path)) {
            worktrees.value = [
              ...worktrees.value,
              {
                path: created.path,
                branch: created.branch,
                head: created.baseRef || '',
                isPrimary: false,
                dirty: false,
                merged: false,
                cacheMB: 0,
              },
            ]
          }
        }),
        loadAllWorktrees({ cwd: refreshCwd }),
      ])
      return created
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function mergeWorktree(
    req: MergeWorktreeRequest,
  ): Promise<MergeWorktreeResponse> {
    lastError.value = null
    try {
      const res = await deps.mergeWorktree(req)
      await Promise.allSettled([
        loadWorktrees().catch(() => {}),
        loadAllWorktrees(),
      ])
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function removeWorktree(
    req: RemoveWorktreeRequest,
  ): Promise<RemoveWorktreeResponse> {
    lastError.value = null
    try {
      const res = await deps.removeWorktree(req)
      worktrees.value = worktrees.value.filter((w) => w.path !== req.path)
      if (repoGroups.value.length > 0) {
        repoGroups.value = repoGroups.value.map((repo) => ({
          ...repo,
          worktrees: repo.worktrees.filter((w) => w.path !== req.path),
        }))
      }
      await Promise.allSettled([
        loadWorktrees().catch(() => {}),
        loadAllWorktrees(),
      ])
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  // ── Profiles ──
  async function loadProfiles(): Promise<ProfileListResponse> {
    profilesLoading.value = true
    lastError.value = null
    try {
      const res = await deps.fetchProfiles()
      profiles.value = res.profiles
      defaultProfileId.value =
        res.defaultProfileId ||
        res.profiles.find((p) => p.isDefault)?.id ||
        res.profiles[0]?.id ||
        ''
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      profilesLoading.value = false
    }
  }

  async function addProfile(req: CreateProfileRequest): Promise<ProfileItem> {
    lastError.value = null
    try {
      const created = await deps.createProfile(req)
      const next = created.isDefault
        ? profiles.value.map((p) => ({ ...p, isDefault: false }))
        : profiles.value.slice()
      next.push(created)
      profiles.value = next
      if (created.isDefault || !defaultProfileId.value) {
        defaultProfileId.value = created.id
      }
      return created
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function editProfile(
    id: string,
    req: UpdateProfileRequest,
  ): Promise<ProfileItem> {
    lastError.value = null
    try {
      const updated = await deps.updateProfile(id, req)
      profiles.value = profiles.value.map((p) => {
        if (p.id === id) return updated
        if (updated.isDefault) return { ...p, isDefault: false }
        return p
      })
      if (updated.isDefault) defaultProfileId.value = updated.id
      return updated
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function setDefaultProfile(id: string): Promise<ProfileItem> {
    return editProfile(id, { isDefault: true })
  }

  async function removeProfile(id: string): Promise<void> {
    lastError.value = null
    try {
      await deps.deleteProfile(id)
      profiles.value = profiles.value.filter((p) => p.id !== id)
      if (defaultProfileId.value === id) {
        defaultProfileId.value =
          profiles.value.find((p) => p.isDefault)?.id ?? profiles.value[0]?.id ?? ''
      }
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function scanProfiles(): Promise<ScanProfilesResponse> {
    lastError.value = null
    try {
      const res = await deps.scanLocalProfiles()
      if (res.profiles.length > 0) {
        profiles.value = res.profiles
        if (
          !defaultProfileId.value ||
          !res.profiles.some((p) => p.id === defaultProfileId.value)
        ) {
          defaultProfileId.value =
            res.profiles.find((p) => p.isDefault)?.id ?? res.profiles[0]?.id ?? ''
        }
      }
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function testGateway(req: TestProfileRequest): Promise<TestProfileResponse> {
    lastError.value = null
    try {
      return await deps.testProfileGateway(req)
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  // ── Resident Bots ──
  async function loadBots(): Promise<BotDetail[]> {
    botsLoading.value = true
    lastError.value = null
    try {
      const list = await deps.fetchBots()
      bots.value = list
      if (!activeBotId.value || !list.some((b) => b.id === activeBotId.value)) {
        activeBotId.value = list[0]?.id ?? ''
      }
      return list
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      botsLoading.value = false
    }
  }

  async function saveBotDocs(
    botId: string,
    docs: UpdateBotDocsRequest,
  ): Promise<BotDetail> {
    lastError.value = null
    try {
      const updated = await deps.updateBotDocs(botId, docs)
      replaceBot(updated)
      return updated
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function sendBotMessage(
    botId: string,
    message: string,
    threadId?: string,
  ): Promise<BotDetail> {
    lastError.value = null
    try {
      const res = await deps.sendBotChat(botId, { threadId, message })
      replaceBot(res.bot)
      return res.bot
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function triggerBotCheck(
    botId: string,
    req: RunBotCheckRequest,
  ): Promise<RunBotCheckResponse> {
    lastError.value = null
    try {
      const res = await deps.runBotCheck(botId, req)
      replaceBot(res.bot)
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  async function dismissBotItem(
    botId: string,
    itemId: string,
  ): Promise<PatchBotItemResponse> {
    lastError.value = null
    try {
      const res = await deps.patchBotItem(botId, itemId, { status: 'done' })
      replaceBot(res.bot)
      return res
    } catch (err) {
      lastError.value = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  /**
   * Idempotent CAS spawn of a Git CoW worktree from a Bot `needs_you` triage item.
   *
   * 1. If the item already has `spawnedWorktreePath`, jumps straight to task space
   *    without creating another worktree.
   * 2. Deduplicates concurrent in-flight clicks for the same `(botId, itemId)`.
   * 3. Calls `POST /git/worktrees` then CAS-binds via `PATCH /bots/{id}/items/{itemId}`.
   *    If the server reports `alreadySpawned: true` (race won by another client),
   *    honors the server's canonical `spawnedWorktreePath` / `spawnedTabId`.
   */
  async function spawnWorktreeFromBotItem(
    botId: string,
    itemId: string,
    opts?: SpawnFromBotOptions,
  ): Promise<SpawnFromBotResult> {
    const bot = bots.value.find((b) => b.id === botId)
    if (!bot) {
      throw new Error(`未找到常驻幕僚: ${botId}`)
    }
    const item = bot.items.find((it) => it.id === itemId)
    if (!item) {
      throw new Error(`未找到分诊事项: ${itemId}`)
    }

    // Local idempotent fast-path: already bound to a worktree.
    if (item.spawnedWorktreePath) {
      activeSpaceKind.value = 'task'
      return {
        alreadySpawned: true,
        worktreePath: item.spawnedWorktreePath,
        branch: item.branchSlug || `bot/${bot.slug}-item-${item.num}`,
        tabId: item.spawnedTabId,
        item,
        bot,
      }
    }

    const key = `${botId}:${itemId}`
    const existing = inFlightSpawns.get(key)
    if (existing) return existing

    const run = (async (): Promise<SpawnFromBotResult> => {
      spawningItemId.value = itemId
      lastError.value = null
      try {
        const branch =
          (item.branchSlug && item.branchSlug.trim()) ||
          `bot/${bot.slug || bot.id}-item-${item.num}`
        const cwd = item.repoCwd || opts?.cwd || bot.repoCwd || worktreeRoot.value || undefined

        const created = await deps.createWorktree({
          session: opts?.session,
          cwd,
          branch,
          baseRef: opts?.baseRef,
        })

        let spawnedTabId: string | undefined
        if (opts?.createTabForWorktree) {
          spawnedTabId = await opts.createTabForWorktree({
            path: created.path,
            branch: created.branch,
            bot,
            item,
          })
        }

        const patched = await deps.patchBotItem(botId, itemId, {
          status: 'working',
          spawnedWorktreePath: created.path,
          spawnedTabId,
        })

        replaceBot(patched.bot)
        const canonicalPath = patched.item.spawnedWorktreePath || created.path
        const canonicalTabId = patched.item.spawnedTabId || spawnedTabId

        await Promise.allSettled([
          loadWorktrees({
            session: opts?.session ?? lastWorktreeQuery.value.session,
            cwd: cwd ?? lastWorktreeQuery.value.cwd,
          }).catch(() => {}),
          loadAllWorktrees({ cwd: cwd ?? lastWorktreeQuery.value.cwd }),
        ])

        activeSpaceKind.value = 'task'

        return {
          alreadySpawned: patched.alreadySpawned,
          worktreePath: canonicalPath,
          branch: created.branch,
          tabId: canonicalTabId,
          item: patched.item,
          bot: patched.bot,
        }
      } catch (err) {
        lastError.value = err instanceof Error ? err.message : String(err)
        throw err
      } finally {
        inFlightSpawns.delete(key)
        if (spawningItemId.value === itemId) {
          spawningItemId.value = null
        }
      }
    })()

    inFlightSpawns.set(key, run)
    return run
  }

  async function loadAll(opts?: { session?: string; cwd?: string }): Promise<void> {
    await Promise.allSettled([
      loadWorktrees(opts),
      loadAllWorktrees(opts?.cwd ? { cwd: opts.cwd } : undefined),
      loadProfiles(),
      loadBots(),
    ])
  }

  return {
    // UI state
    sidebarOpen,
    sidebarFilter,
    sidebarQuery,
    activeSpaceKind,
    activeBotId,
    activeBot,
    centerPreviewFile,
    tabViewModes,
    profileModalOpen,
    profileModalInitialTab,
    newTaskModalOpen,

    // Domain collections
    worktreeRoot,
    worktrees,
    repoGroups,
    worktreesNotGit,
    worktreesLoading,
    managedWorktreeCount,
    profiles,
    defaultProfileId,
    profilesLoading,
    bots,
    botsLoading,
    totalBotNeedsYou,
    spawningItemId,
    lastError,

    // UI actions
    setSidebarOpen,
    toggleSidebar,
    selectBot,
    selectTaskSpace,
    openCenterPreview,
    closeCenterPreview,
    getTabViewMode,
    setTabViewMode,
    openProfileModal,
    closeProfileModal,
    openNewTaskModal,
    closeNewTaskModal,

    // Data actions
    loadAll,
    loadWorktrees,
    loadAllWorktrees,
    createCoWWorktree,
    mergeWorktree,
    removeWorktree,
    loadProfiles,
    addProfile,
    editProfile,
    setDefaultProfile,
    removeProfile,
    scanProfiles,
    testGateway,
    loadBots,
    saveBotDocs,
    sendBotMessage,
    triggerBotCheck,
    dismissBotItem,
    spawnWorktreeFromBotItem,
  }
}

export type TaskWorkbenchStore = ReturnType<typeof createTaskWorkbench>

let _singleton: TaskWorkbenchStore | null = null

/** Shared singleton composable for Task Workbench v11 UI components. */
export function useTaskWorkbench(): TaskWorkbenchStore {
  if (!_singleton) {
    _singleton = createTaskWorkbench()
  }
  return _singleton
}
