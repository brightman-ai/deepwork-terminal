import { afterAll, describe, it, expect, mock } from 'bun:test'
import type { TaskWorkbenchDeps } from '@terminal/composables/cli/useTaskWorkbench'
import type { RepoWorktreeGroup, WorktreeInfo } from '@terminal/api/worktrees'
import type { ProfileItem } from '@terminal/api/profiles'
import type { BotDetail } from '@terminal/api/bots'

// useCliAuth.ts reads window.location / localStorage at module load time.
// Follow repo convention (useTmuxServerVanished.test.ts) with a minimal scoped shim + dynamic import.
const storageMap = new Map<string, string>()
const localStorageStub = {
  getItem: (k: string) => storageMap.get(k) ?? null,
  setItem: (k: string, v: string) => { storageMap.set(k, v) },
  removeItem: (k: string) => { storageMap.delete(k) },
}
const originalWindow = (globalThis as Record<string, unknown>).window
const originalLocalStorage = (globalThis as Record<string, unknown>).localStorage

Object.defineProperty(globalThis, 'localStorage', { value: localStorageStub, configurable: true })
Object.defineProperty(globalThis, 'window', {
  value: {
    innerWidth: 1280,
    location: { search: '', pathname: '/', hash: '' },
    history: { replaceState: () => {} },
    localStorage: localStorageStub,
  },
  configurable: true,
})

afterAll(() => {
  delete (globalThis as Record<string, unknown>).window
  delete (globalThis as Record<string, unknown>).localStorage
  if (originalWindow !== undefined) (globalThis as Record<string, unknown>).window = originalWindow
  if (originalLocalStorage !== undefined) {
    (globalThis as Record<string, unknown>).localStorage = originalLocalStorage
  }
})

const { createTaskWorkbench } = await import('@terminal/composables/cli/useTaskWorkbench')

function makeWorktree(overrides: Partial<WorktreeInfo> = {}): WorktreeInfo {
  return {
    path: '/repo/main',
    branch: 'main',
    head: 'abc1234',
    isPrimary: true,
    dirty: false,
    merged: false,
    cacheMB: 12,
    ...overrides,
  }
}

function makeProfile(overrides: Partial<ProfileItem> = {}): ProfileItem {
  return {
    id: 'prof-claude-1',
    name: 'Claude 官方 #1',
    engine: 'claude',
    kind: 'oauth',
    model: 'claude-opus-4-6',
    hasApiKey: false,
    configDir: '/home/u/.dw-terminal/profiles/prof-claude-1/claude',
    isDefault: true,
    badge: '本机 ~/.claude',
    ...overrides,
  }
}

function makeBot(overrides: Partial<BotDetail> = {}): BotDetail {
  return {
    id: 'bot-scout',
    slug: 'scout',
    name: 'Scout',
    emoji: '🤖',
    roleTitle: '线上告警与回归哨兵',
    profileId: 'prof-claude-1',
    repoCwd: '/repo/main',
    needsYouCount: 1,
    workingCount: 0,
    soulMd: '# Scout SOUL\n只读巡检',
    memoryMd: '# Scout MEMORY\n',
    triggers: [
      {
        id: 'trg-1',
        name: 'CI 失败预检',
        precheck: 'git status --short',
        mode: 'zero-token-precheck',
        stats: '今日拦截 12 次',
        skippedZeroToken: 12,
      },
    ],
    items: [
      {
        id: 'item-1',
        num: 42,
        status: 'needs_you',
        origin: 'Sentry',
        title: '弱网上传分片重试偶发超时',
        question: '是否派生独立工作树修复 uploadTransport.ts 并补单测？',
        targetFile: 'frontend/src/composables/cli/uploadTransport.ts:88',
        branchSlug: 'fix/upload-retry-42',
        createdAt: '2026-11-21T05:00:00Z',
      },
    ],
    threads: [
      {
        id: 'home',
        title: '🏠 主对话室',
        kind: 'home',
        messages: [
          {
            id: 'm1',
            role: 'bot',
            meta: 'Scout',
            text: '已完成只读巡检，发现 1 项待确认事项。',
            createdAt: '2026-11-21T05:00:00Z',
          },
        ],
      },
    ],
    ...overrides,
  }
}

function makeDeps(overrides: Partial<TaskWorkbenchDeps> = {}): TaskWorkbenchDeps {
  const defaultBot = makeBot()
  const defaultProfile = makeProfile()
  const defaultWt = makeWorktree()

  return {
    fetchWorktrees: mock(async () => ({
      root: '/repo/main',
      worktrees: [defaultWt],
    })),
    fetchAllWorktrees: mock(async () => ({
      repos: [] as RepoWorktreeGroup[],
    })),
    createWorktree: mock(async (req) => ({
      path: `/data/worktrees/repo/${req.branch.replace(/\//g, '-')}`,
      branch: req.branch,
      baseRef: req.baseRef || 'main',
      isPrimary: false,
    })),
    mergeWorktree: mock(async (req) => ({
      ok: true as const,
      mergedBranch: 'feat/demo',
      targetBranch: req.targetBranch || 'main',
    })),
    removeWorktree: mock(async () => ({ ok: true as const })),
    fetchProfiles: mock(async () => ({
      profiles: [defaultProfile],
      defaultProfileId: defaultProfile.id,
    })),
    createProfile: mock(async (req) =>
      makeProfile({
        id: 'prof-new',
        name: req.name,
        engine: req.engine,
        kind: req.kind,
        model: req.model || 'glm-5.1-coding',
        baseUrl: req.baseUrl,
        apiKeyMasked: req.apiKey ? 'sk-****1234' : undefined,
        hasApiKey: Boolean(req.apiKey),
        isDefault: Boolean(req.isDefault),
        badge: '独立网关',
      }),
    ),
    updateProfile: mock(async (id, req) =>
      makeProfile({
        id,
        name: req.name || 'Updated Profile',
        isDefault: Boolean(req.isDefault),
      }),
    ),
    deleteProfile: mock(async () => undefined),
    scanLocalProfiles: mock(async () => ({
      imported: 1,
      profiles: [
        defaultProfile,
        makeProfile({ id: 'prof-codex-1', name: 'Codex Pro', engine: 'codex', isDefault: false }),
      ],
    })),
    testProfileGateway: mock(async () => ({
      ok: true,
      latencyMs: 36,
      message: 'OK',
    })),
    fetchBots: mock(async () => [defaultBot]),
    updateBotDocs: mock(async (id, req) =>
      makeBot({ id, soulMd: req.soulMd, memoryMd: req.memoryMd }),
    ),
    sendBotChat: mock(async (id, req) => ({
      bot: makeBot({
        id,
        threads: [
          {
            id: req.threadId || 'home',
            title: '🏠 主对话室',
            kind: 'home',
            messages: [
              {
                id: 'u1',
                role: 'user',
                text: req.message,
                createdAt: '2026-11-21T05:01:00Z',
              },
              {
                id: 'b1',
                role: 'bot',
                text: '收到，已完成只读核查。',
                createdAt: '2026-11-21T05:01:01Z',
              },
            ],
          },
        ],
      }),
    })),
    runBotCheck: mock(async (id, req) => ({
      skippedZeroToken: !req.simulateAlert,
      bot: makeBot({ id }),
    })),
    patchBotItem: mock(async (id, itemId, req) => {
      const base = makeBot({ id })
      const updatedItems = base.items.map((it) =>
        it.id === itemId
          ? {
              ...it,
              status: req.status ?? it.status,
              spawnedWorktreePath: req.spawnedWorktreePath ?? it.spawnedWorktreePath,
              spawnedTabId: req.spawnedTabId ?? it.spawnedTabId,
            }
          : it,
      )
      const updatedItem = updatedItems.find((it) => it.id === itemId)!
      return {
        item: updatedItem,
        alreadySpawned: false,
        bot: { ...base, items: updatedItems, needsYouCount: 0, workingCount: 1 },
      }
    }),
    ...overrides,
  }
}

describe('useTaskWorkbench', () => {
  it('manages sidebar, space switching, center preview tab, and per-tab view modes', () => {
    const wb = createTaskWorkbench(makeDeps())

    expect(wb.activeSpaceKind.value).toBe('task')
    expect(wb.centerPreviewFile.value).toBeNull()
    expect(wb.getTabViewMode('tab-1')).toBe('terminal')

    wb.setSidebarOpen(false)
    expect(wb.sidebarOpen.value).toBe(false)
    wb.toggleSidebar()
    expect(wb.sidebarOpen.value).toBe(true)

    wb.selectBot('bot-scout')
    expect(wb.activeSpaceKind.value).toBe('bot')
    expect(wb.activeBotId.value).toBe('bot-scout')

    // Opening center file preview switches back to task space and sets single preview slot
    wb.openCenterPreview('frontend/src/App.vue', { line: 42, sessionId: 's1' })
    expect(wb.activeSpaceKind.value).toBe('task')
    expect(wb.centerPreviewFile.value).toEqual({
      path: 'frontend/src/App.vue',
      line: 42,
      sessionId: 's1',
      cwd: undefined,
    })

    wb.closeCenterPreview()
    expect(wb.centerPreviewFile.value).toBeNull()

    // Per-tab view modes are isolated
    wb.setTabViewMode('tab-1', 'transcript')
    expect(wb.getTabViewMode('tab-1')).toBe('transcript')
    expect(wb.getTabViewMode('tab-2')).toBe('terminal')
  })

  it('loads, creates, merges, and removes CoW worktrees', async () => {
    const worktreesList: WorktreeInfo[] = [makeWorktree()]
    const deps = makeDeps({
      fetchWorktrees: mock(async () => ({
        root: '/repo/main',
        worktrees: worktreesList.slice(),
      })),
      createWorktree: mock(async (req) => {
        const created = {
          path: `/data/worktrees/repo/${req.branch}`,
          branch: req.branch,
          baseRef: req.baseRef || 'main',
          isPrimary: false,
        }
        worktreesList.push(
          makeWorktree({
            path: created.path,
            branch: created.branch,
            isPrimary: false,
          }),
        )
        return created
      }),
      removeWorktree: mock(async (req) => {
        const idx = worktreesList.findIndex((w) => w.path === req.path)
        if (idx >= 0) worktreesList.splice(idx, 1)
        return { ok: true as const }
      }),
    })

    const wb = createTaskWorkbench(deps)
    await wb.loadWorktrees({ session: 'sess-1' })
    expect(wb.worktreeRoot.value).toBe('/repo/main')
    expect(wb.worktrees.value).toHaveLength(1)
    expect(wb.managedWorktreeCount.value).toBe(0)

    const created = await wb.createCoWWorktree({ branch: 'feat-x', cwd: '/repo/main' })
    expect(created.path).toBe('/data/worktrees/repo/feat-x')
    expect(wb.worktrees.value).toHaveLength(2)
    expect(wb.managedWorktreeCount.value).toBe(1)

    const merged = await wb.mergeWorktree({ cwd: created.path })
    expect(merged.ok).toBe(true)

    await wb.removeWorktree({ path: created.path })
    expect(wb.worktrees.value).toHaveLength(1)
    expect(wb.managedWorktreeCount.value).toBe(0)
  })

  it('loads, adds, updates default, scans, tests, and deletes isolated profiles', async () => {
    const deps = makeDeps()
    const wb = createTaskWorkbench(deps)

    await wb.loadProfiles()
    expect(wb.profiles.value).toHaveLength(1)
    expect(wb.defaultProfileId.value).toBe('prof-claude-1')

    const gw = await wb.addProfile({
      name: 'GLM 网关',
      engine: 'claude',
      kind: 'gateway',
      model: 'glm-5.1-coding',
      baseUrl: 'https://open.bigmodel.cn/api/anthropic',
      apiKey: 'sk-secret-1234',
      isDefault: true,
    })
    expect(gw.apiKeyMasked).toBe('sk-****1234')
    expect(wb.defaultProfileId.value).toBe('prof-new')
    expect(wb.profiles.value.find((p) => p.id === 'prof-claude-1')?.isDefault).toBe(false)

    const testRes = await wb.testGateway({ baseUrl: 'https://open.bigmodel.cn/api/anthropic' })
    expect(testRes.ok).toBe(true)
    expect(testRes.latencyMs).toBe(36)

    const scanRes = await wb.scanProfiles()
    expect(scanRes.imported).toBe(1)
    expect(wb.profiles.value).toHaveLength(2)

    await wb.removeProfile('prof-claude-1')
    expect(wb.profiles.value).toHaveLength(1)
    expect(wb.defaultProfileId.value).toBe('prof-codex-1')
  })

  it('loads bots, updates SOUL/MEMORY docs, chats, and triggers read-only checks', async () => {
    const deps = makeDeps()
    const wb = createTaskWorkbench(deps)

    await wb.loadBots()
    expect(wb.bots.value).toHaveLength(1)
    expect(wb.activeBotId.value).toBe('bot-scout')
    expect(wb.totalBotNeedsYou.value).toBe(1)

    await wb.saveBotDocs('bot-scout', {
      soulMd: '# Updated SOUL',
      memoryMd: '# Updated MEMORY',
    })
    expect(wb.activeBot.value?.soulMd).toBe('# Updated SOUL')

    await wb.sendBotMessage('bot-scout', '/check git status --short', 'home')
    expect(wb.activeBot.value?.threads[0]?.messages).toHaveLength(2)

    const checkRes = await wb.triggerBotCheck('bot-scout', {
      triggerId: 'trg-1',
      precheckCmd: 'git status --short',
    })
    expect(checkRes.skippedZeroToken).toBe(true)
  })

  it('CAS-spawns a CoW worktree from a Bot needs_you item and is idempotent on repeat clicks', async () => {
    const deps = makeDeps()
    const wb = createTaskWorkbench(deps)
    await wb.loadBots()
    wb.selectBot('bot-scout')
    expect(wb.activeSpaceKind.value).toBe('bot')

    const tabCreator = mock(async () => 'tab-spawned-1')

    const first = await wb.spawnWorktreeFromBotItem('bot-scout', 'item-1', {
      createTabForWorktree: tabCreator,
    })

    expect(first.alreadySpawned).toBe(false)
    expect(first.worktreePath).toBe('/data/worktrees/repo/fix-upload-retry-42')
    expect(first.tabId).toBe('tab-spawned-1')
    expect(wb.activeSpaceKind.value).toBe('task')
    expect(wb.totalBotNeedsYou.value).toBe(0)
    expect(deps.createWorktree).toHaveBeenCalledTimes(1)
    expect(deps.patchBotItem).toHaveBeenCalledTimes(1)
    expect(tabCreator).toHaveBeenCalledTimes(1)

    // Repeat click on already-spawned item must short-circuit without creating a second worktree
    wb.selectBot('bot-scout')
    const second = await wb.spawnWorktreeFromBotItem('bot-scout', 'item-1', {
      createTabForWorktree: tabCreator,
    })

    expect(second.alreadySpawned).toBe(true)
    expect(second.worktreePath).toBe('/data/worktrees/repo/fix-upload-retry-42')
    expect(second.tabId).toBe('tab-spawned-1')
    expect(wb.activeSpaceKind.value).toBe('task')
    expect(deps.createWorktree).toHaveBeenCalledTimes(1)
    expect(deps.patchBotItem).toHaveBeenCalledTimes(1)
  })

  it('deduplicates concurrent double-clicks on CAS spawn', async () => {
    let resolveCreate!: (val: { path: string; branch: string; baseRef: string; isPrimary: boolean }) => void
    const deps = makeDeps({
      createWorktree: mock(
        () =>
          new Promise<{ path: string; branch: string; baseRef: string; isPrimary: boolean }>((r) => {
            resolveCreate = r
          }),
      ),
    })

    const wb = createTaskWorkbench(deps)
    await wb.loadBots()

    const p1 = wb.spawnWorktreeFromBotItem('bot-scout', 'item-1')
    const p2 = wb.spawnWorktreeFromBotItem('bot-scout', 'item-1')

    resolveCreate({
      path: '/data/worktrees/repo/fix-upload-retry-42',
      branch: 'fix/upload-retry-42',
      baseRef: 'main',
      isPrimary: false,
    })

    const [r1, r2] = await Promise.all([p1, p2])
    expect(r1.worktreePath).toBe('/data/worktrees/repo/fix-upload-retry-42')
    expect(r2.worktreePath).toBe('/data/worktrees/repo/fix-upload-retry-42')
    expect(deps.createWorktree).toHaveBeenCalledTimes(1)
    expect(deps.patchBotItem).toHaveBeenCalledTimes(1)
  })

  it('honors server-side CAS winner when another client spawned the item first', async () => {
    const deps = makeDeps({
      patchBotItem: mock(async (id, itemId) => {
        const base = makeBot({ id })
        const winnerItem = {
          ...base.items[0]!,
          id: itemId,
          status: 'working' as const,
          spawnedWorktreePath: '/data/worktrees/repo/winner-path',
          spawnedTabId: 'tab-winner',
        }
        return {
          item: winnerItem,
          alreadySpawned: true,
          bot: { ...base, items: [winnerItem], needsYouCount: 0, workingCount: 1 },
        }
      }),
    })

    const wb = createTaskWorkbench(deps)
    await wb.loadBots()

    const res = await wb.spawnWorktreeFromBotItem('bot-scout', 'item-1')
    expect(res.alreadySpawned).toBe(true)
    expect(res.worktreePath).toBe('/data/worktrees/repo/winner-path')
    expect(res.tabId).toBe('tab-winner')
    expect(wb.activeBot.value?.items[0]?.spawnedWorktreePath).toBe(
      '/data/worktrees/repo/winner-path',
    )
  })

  it('loads multi-repo worktrees via loadAllWorktrees, counts managed worktrees across repos, and refreshes on mutations', async () => {
    const reposState: RepoWorktreeGroup[] = [
      {
        root: '/repos/alpha',
        name: 'alpha',
        sessionCount: 3,
        worktrees: [
          makeWorktree({ path: '/repos/alpha', branch: 'main', isPrimary: true }),
          makeWorktree({ path: '/data/worktrees/alpha/feat-1', branch: 'feat-1', isPrimary: false }),
        ],
      },
      {
        root: '/repos/beta',
        name: 'beta',
        sessionCount: 2,
        worktrees: [
          makeWorktree({ path: '/repos/beta', branch: 'main', isPrimary: true }),
          makeWorktree({ path: '/data/worktrees/beta/fix-2', branch: 'fix-2', isPrimary: false }),
        ],
      },
    ]

    const fetchAllMock = mock(async () => ({
      repos: reposState.map((r) => ({ ...r, worktrees: r.worktrees.slice() })),
    }))
    const deps = makeDeps({
      fetchAllWorktrees: fetchAllMock,
      removeWorktree: mock(async (req) => {
        for (const r of reposState) {
          r.worktrees = r.worktrees.filter((w) => w.path !== req.path)
        }
        return { ok: true as const }
      }),
    })

    const wb = createTaskWorkbench(deps)
    await wb.loadAll({ session: 's1', cwd: '/repos/alpha' })

    expect(fetchAllMock).toHaveBeenCalledTimes(1)
    expect(wb.repoGroups.value).toHaveLength(2)
    expect(wb.managedWorktreeCount.value).toBe(2)

    await wb.createCoWWorktree({ branch: 'feat-new', cwd: '/repos/alpha' })
    expect(fetchAllMock).toHaveBeenCalledTimes(2)

    await wb.mergeWorktree({ cwd: '/data/worktrees/alpha/feat-1' })
    expect(fetchAllMock).toHaveBeenCalledTimes(3)

    await wb.removeWorktree({ path: '/data/worktrees/beta/fix-2' })
    expect(fetchAllMock).toHaveBeenCalledTimes(4)
    expect(wb.managedWorktreeCount.value).toBe(1)
  })

  it('dismissBotItem patches status to done and updates bot state', async () => {
    const patchMock = mock(async (id: string, itemId: string, req: { status?: 'needs_you' | 'working' | 'done' }) => {
      const base = makeBot({ id })
      const updatedItems = base.items.map((it) =>
        it.id === itemId ? { ...it, status: req.status ?? it.status } : it,
      )
      return {
        item: updatedItems.find((it) => it.id === itemId)!,
        alreadySpawned: false,
        bot: { ...base, items: updatedItems, needsYouCount: 0 },
      }
    })
    const deps = makeDeps({ patchBotItem: patchMock })
    const wb = createTaskWorkbench(deps)
    await wb.loadBots()
    expect(wb.totalBotNeedsYou.value).toBe(1)

    await wb.dismissBotItem('bot-scout', 'item-1')
    expect(patchMock).toHaveBeenCalledWith('bot-scout', 'item-1', { status: 'done' })
    expect(wb.totalBotNeedsYou.value).toBe(0)
    expect(wb.activeBot.value?.items[0]?.status).toBe('done')
  })

  it('spawnWorktreeFromBotItem prefers item.repoCwd over opts.cwd and bot.repoCwd', async () => {
    const createMock = mock(async (req: { cwd?: string; branch: string; baseRef?: string }) => ({
      path: `/data/worktrees/beta/${req.branch.replace(/\//g, '-')}`,
      branch: req.branch,
      baseRef: req.baseRef || 'main',
      isPrimary: false,
    }))
    const multiRepoBot = makeBot({
      id: 'bot-review',
      slug: 'review-buddy',
      repoCwd: '/repos/fallback-main',
      items: [
        {
          id: 'live-dirty-beta',
          num: 7,
          status: 'needs_you',
          origin: 'Git 巡检',
          title: 'beta 仓库存在未提交改动',
          question: '是否派生工作树接管？',
          repoCwd: '/repos/beta',
          branchSlug: 'fix/beta-dirty',
          live: true,
          createdAt: '2026-11-21T06:00:00Z',
        },
      ],
    })

    const deps = makeDeps({
      fetchBots: mock(async () => [multiRepoBot]),
      createWorktree: createMock,
      patchBotItem: mock(async (_id, itemId, req) => {
        const updatedItem = {
          ...multiRepoBot.items[0]!,
          id: itemId,
          status: req.status ?? 'working',
          spawnedWorktreePath: req.spawnedWorktreePath,
          spawnedTabId: req.spawnedTabId,
        }
        return {
          item: updatedItem,
          alreadySpawned: false,
          bot: { ...multiRepoBot, items: [updatedItem], needsYouCount: 0, workingCount: 1 },
        }
      }),
    })

    const wb = createTaskWorkbench(deps)
    await wb.loadBots()
    await wb.spawnWorktreeFromBotItem('bot-review', 'live-dirty-beta', {
      session: 'sess-alpha',
      cwd: '/repos/alpha-active-tab',
    })

    expect(createMock).toHaveBeenCalledTimes(1)
    expect(createMock.mock.calls[0]?.[0]?.cwd).toBe('/repos/beta')
  })
})
