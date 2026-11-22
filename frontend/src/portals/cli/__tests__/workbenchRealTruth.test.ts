/**
 * Real-truth Resident Bots & Multi-Repo Sidebar component tests:
 *  - BotWorkspaceView: removes fake simulate-alert, wires real patrol button,
 *    repoCwd pills + open-file cwd, jump-session, dismiss-item, conditional spawn.
 *  - TaskSidebar: multi-repo grouping, backward-compat worktrees fallback,
 *    tab priority & >6 collapse/expand, and unmatched "其他目录会话" bucket.
 */
import { afterEach, beforeAll, describe, expect, it } from 'bun:test'
import { nextTick, type Component } from 'vue'
import { installVueSfcLoader } from './support/sfcLoader'
import { byTestId, findAll, fire, mountView, type TNode } from './support/testRenderer'
import type { BotDetail } from '../../../api/bots'
import type { ProfileItem } from '../../../api/profiles'
import type { RepoWorktreeGroup, WorktreeInfo } from '../../../api/worktrees'
import type { WorkbenchTab } from '../../../types/workbench'

installVueSfcLoader()

let BotWorkspaceView: Component
let TaskSidebar: Component
let WorkbenchTelemetryBar: Component
let NewTaskModal: Component
let DispatchReviewModal: Component
let CollectReviewBanner: Component
let cleanups: Array<() => void> = []

beforeAll(async () => {
  BotWorkspaceView = (await import('../../../components/workbench/BotWorkspaceView.vue')).default
  TaskSidebar = (await import('../../../components/workbench/TaskSidebar.vue')).default
  WorkbenchTelemetryBar = (await import('../../../components/workbench/WorkbenchTelemetryBar.vue')).default
  NewTaskModal = (await import('../../../components/workbench/NewTaskModal.vue')).default
  DispatchReviewModal = (await import('../../../components/workbench/DispatchReviewModal.vue')).default
  CollectReviewBanner = (await import('../../../components/workbench/CollectReviewBanner.vue')).default
})

afterEach(() => {
  cleanups.forEach((fn) => fn())
  cleanups = []
})

function textOf(node: TNode): string {
  if (node.kind === 'text') return node.text
  return node.children.map(textOf).join('')
}

function makeBot(): BotDetail {
  return {
    id: 'bot-scout',
    slug: 'scout',
    name: 'Scout',
    emoji: '🤖',
    roleTitle: '真实运行态巡检幕僚',
    profileId: 'prof-default',
    repoCwd: '',
    needsYouCount: 2,
    workingCount: 0,
    soulMd: '# SOUL',
    memoryMd: '# MEMORY',
    triggers: [],
    items: [
      {
        id: 'item-repo',
        num: 1,
        status: 'needs_you',
        origin: 'Git 巡检',
        title: 'beta 仓库存在未提交改动',
        question: '是否查看改动或派生独立工作树接管？',
        targetFile: 'src/index.ts:42',
        repoCwd: '/home/dev/code/beta-service',
        sessionId: 'sess-beta-1',
        live: true,
        createdAt: '2026-11-21T10:00:00Z',
      },
      {
        id: 'item-norepo',
        num: 2,
        status: 'needs_you',
        origin: '会话巡检',
        title: '家目录会话等待确认',
        question: '是否直达该终端会话继续处理？',
        sessionId: 'sess-home-1',
        live: true,
        createdAt: '2026-11-21T10:01:00Z',
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
            text: '已完成真实运行态巡检。',
            createdAt: '2026-11-21T10:00:00Z',
          },
        ],
      },
    ],
  }
}

function makeWt(path: string, branch: string, isPrimary = true): WorktreeInfo {
  return {
    path,
    branch,
    head: 'abc1234',
    isPrimary,
    dirty: false,
    merged: false,
    cacheMB: 0,
  }
}

function makeTab(id: string, name: string, cwd: string, engine = 'claude'): WorkbenchTab {
  return {
    id,
    groupId: 'g1',
    name,
    cwd,
    engine,
    sessionId: `sess-${id}`,
  }
}

describe('BotWorkspaceView · real-truth patrol & multi-repo actions', () => {
  it('replaces fake simulate-alert with btn-patrol-bot and wires repoCwd, open-file, jump-session, dismiss, and conditional spawn', async () => {
    const events: Array<{ name: string; payload: unknown }> = []
    const m = mountView(BotWorkspaceView, {
      bot: makeBot(),
      busy: false,
      onRunCheck: (p: unknown) => events.push({ name: 'run-check', payload: p }),
      onOpenFile: (p: unknown) => events.push({ name: 'open-file', payload: p }),
      onJumpSession: (p: unknown) => events.push({ name: 'jump-session', payload: p }),
      onDismissItem: (p: unknown) => events.push({ name: 'dismiss-item', payload: p }),
      onSpawnWorktree: (p: unknown) => events.push({ name: 'spawn-worktree', payload: p }),
    })
    cleanups.push(m.unmount)

    // No fake simulate-alert button or fake copy
    expect(byTestId(m.host, 'btn-sim-bot-event')).toHaveLength(0)
    expect(textOf(m.host)).not.toContain('模拟新告警')

    // Patrol button emits { botId, patrol: true }
    const patrolBtns = byTestId(m.host, 'btn-patrol-bot')
    expect(patrolBtns).toHaveLength(1)
    expect(textOf(patrolBtns[0]!)).toContain('立即巡检真实状态')
    fire(patrolBtns[0]!, 'onClick')
    expect(events[0]).toEqual({
      name: 'run-check',
      payload: { botId: 'bot-scout', patrol: true },
    })

    // Repo pill on item card & banner
    expect(textOf(byTestId(m.host, 'bot-item-repo-item-repo')[0]!)).toContain('beta-service')
    expect(textOf(byTestId(m.host, 'bot-banner-repo-item-repo')[0]!)).toContain('beta-service')

    // Clicking targetFile emits open-file with item.repoCwd
    fire(byTestId(m.host, 'bot-item-file-item-repo')[0]!, 'onClick')
    expect(events[1]).toEqual({
      name: 'open-file',
      payload: { path: 'src/index.ts', line: 42, cwd: '/home/dev/code/beta-service' },
    })

    // Jump session button emits jump-session with sessionId and repoCwd
    fire(byTestId(m.host, 'bot-jump-session-item-repo')[0]!, 'onClick')
    expect(events[2]).toEqual({
      name: 'jump-session',
      payload: { sessionId: 'sess-beta-1', cwd: '/home/dev/code/beta-service' },
    })

    // Dismiss button emits dismiss-item
    fire(byTestId(m.host, 'bot-dismiss-item-item-repo')[0]!, 'onClick')
    expect(events[3]).toEqual({
      name: 'dismiss-item',
      payload: { botId: 'bot-scout', itemId: 'item-repo' },
    })

    // Spawn worktree button shown for item-repo (has repoCwd), hidden for item-norepo (no item.repoCwd and no bot.repoCwd)
    expect(byTestId(m.host, 'bot-spawn-worktree-item-repo')).toHaveLength(1)
    expect(byTestId(m.host, 'bot-spawn-worktree-item-norepo')).toHaveLength(0)
    // Dismiss + jump-session still available for item-norepo
    expect(byTestId(m.host, 'bot-dismiss-item-item-norepo')).toHaveLength(1)
    expect(byTestId(m.host, 'bot-jump-session-item-norepo')).toHaveLength(1)
  })

  it('uses jargon-free labels, smart dirty-repo actions, file-ref stripping/clipping, and 1-click quick chips', async () => {
    const events: Array<{ name: string; payload: unknown }> = []
    const longRelPath = 'docs/meetings/2026-11-21-architecture-review/very-long-summary-report-final.json'
    const bot = makeBot()
    bot.items = [
      {
        id: 'live-dirty-alpha',
        num: 1,
        status: 'needs_you',
        origin: 'Git 巡检',
        title: 'alpha 有 3 个未提交文件',
        question: '可先进入终端查看或另开干净分支处理。',
        targetFile: `/home/dev/code/alpha/${longRelPath}`,
        repoCwd: '/home/dev/code/alpha',
        createdAt: '2026-11-21T10:00:00Z',
      },
      {
        id: 'item-session-wait',
        num: 2,
        status: 'needs_you',
        origin: '会话巡检',
        title: 'Claude 等待确认',
        question: '是否回到会话继续？',
        targetFile: '/home/dev/code/beta/src/app.ts:12',
        repoCwd: '/home/dev/code/beta',
        sessionId: 'sess-wait-2',
        createdAt: '2026-11-21T10:01:00Z',
      },
      {
        id: 'item-spawned',
        num: 3,
        status: 'needs_you',
        origin: 'Git 巡检',
        title: '已新建分支目录任务',
        question: '可直接进入分支目录继续。',
        repoCwd: '/home/dev/code/gamma',
        spawnedWorktreePath: '/data/wt/gamma-fix',
        spawnedTabId: 'tab-wt-1',
        createdAt: '2026-11-21T10:02:00Z',
      },
    ]

    const m = mountView(BotWorkspaceView, {
      bot,
      busy: false,
      onSendChat: (p: unknown) => events.push({ name: 'send-chat', payload: p }),
      onRunCheck: (p: unknown) => events.push({ name: 'run-check', payload: p }),
      onOpenFile: (p: unknown) => events.push({ name: 'open-file', payload: p }),
      onJumpSession: (p: unknown) => events.push({ name: 'jump-session', payload: p }),
    })
    cleanups.push(m.unmount)

    const fullText = textOf(m.host)
    expect(fullText).toContain('只读巡检不改代码，发现问题可一键直达终端或另开独立分支处理')
    expect(fullText).toContain('⚡ 自动巡检')
    expect(fullText).not.toContain('CoW')
    expect(fullText).not.toContain('巡检门控')

    // Dirty repo item without sessionId still renders primary terminal jump + secondary clean branch spawn
    const dirtyJump = byTestId(m.host, 'bot-jump-session-live-dirty-alpha')[0]!
    expect(textOf(dirtyJump)).toContain('↗ 进入该仓终端')
    fire(dirtyJump, 'onClick')
    expect(events[0]).toEqual({
      name: 'jump-session',
      payload: { sessionId: '', cwd: '/home/dev/code/alpha' },
    })
    expect(textOf(byTestId(m.host, 'bot-dismiss-item-live-dirty-alpha')[0]!)).toContain('✓ 已知晓')
    const dirtySpawn = byTestId(m.host, 'bot-spawn-worktree-live-dirty-alpha')[0]!
    expect(textOf(dirtySpawn)).toContain('🌿 另开干净分支 ↗')
    expect(String(dirtySpawn.props.title)).toContain('不会搬移当前未提交文件')

    // Normal session item uses '↗ 前往会话处理' & '🌿 新建分支目录 ↗'
    expect(textOf(byTestId(m.host, 'bot-jump-session-item-session-wait')[0]!)).toContain('↗ 前往会话处理')
    expect(textOf(byTestId(m.host, 'bot-spawn-worktree-item-session-wait')[0]!)).toContain('🌿 新建分支目录 ↗')

    // Spawned item renders '进入分支目录 ↗'
    expect(textOf(byTestId(m.host, 'bot-open-worktree-item-spawned')[0]!)).toContain('进入分支目录 ↗')

    // File pills strip repoCwd prefix and clip long paths >52 chars while preserving full path in title & click
    const shortPill = byTestId(m.host, 'bot-item-file-item-session-wait')[0]!
    expect(textOf(shortPill)).toContain('src/app.ts:12')
    expect(textOf(shortPill)).not.toContain('/home/dev/code/beta/')
    expect(shortPill.props.title).toBe('/home/dev/code/beta/src/app.ts:12')

    const longPill = byTestId(m.host, 'bot-item-file-live-dirty-alpha')[0]!
    expect(textOf(longPill)).not.toContain('/home/dev/code/alpha/')
    expect(textOf(longPill)).toContain('…')
    expect(longPill.props.title).toBe(`/home/dev/code/alpha/${longRelPath}`)
    fire(longPill, 'onClick')
    expect(events[1]).toEqual({
      name: 'open-file',
      payload: { path: `/home/dev/code/alpha/${longRelPath}`, cwd: '/home/dev/code/alpha' },
    })

    // Quick-prompt chips emit send-chat / run-check in 1 click
    fire(byTestId(m.host, 'bot-quick-chip-sessions')[0]!, 'onClick')
    fire(byTestId(m.host, 'bot-quick-chip-dirty')[0]!, 'onClick')
    fire(byTestId(m.host, 'bot-quick-chip-status')[0]!, 'onClick')
    expect(events[2]).toEqual({
      name: 'send-chat',
      payload: { botId: 'bot-scout', threadId: 'home', message: '汇总待处理会话' },
    })
    expect(events[3]).toEqual({
      name: 'send-chat',
      payload: { botId: 'bot-scout', threadId: 'home', message: '列出未提交变更仓库' },
    })
    expect(events[4]).toEqual({
      name: 'run-check',
      payload: { botId: 'bot-scout', precheckCmd: 'git status --short' },
    })

    // Switch to triggers tab to verify updated read-only guard banner copy
    fire(byTestId(m.host, 'bot-rtab-triggers')[0]!, 'onClick')
    await nextTick()
    expect(textOf(m.host)).toContain('只读安全护栏已开启：巡检命令仅允许读取状态与日志，自动拦截写文件、删除与代码提交。')
  })
})

describe('TaskSidebar · multi-repo groups, >6 tab collapse, and other-dirs bucket', () => {
  it('renders multiple repoGroups with sequential globalIdx, prioritizes & collapses >6 tabs, and shows unmatched tabs in 其他目录会话', async () => {
    const repoGroups: RepoWorktreeGroup[] = [
      {
        root: '/repos/alpha',
        name: 'alpha',
        sessionCount: 8,
        worktrees: [
          makeWt('/repos/alpha', 'main', true),
          makeWt('/data/worktrees/alpha/feat-1', 'feat-1', false),
        ],
      },
      {
        root: '/repos/beta',
        name: 'beta',
        sessionCount: 1,
        worktrees: [makeWt('/repos/beta', 'develop', true)],
      },
    ]

    const tabs: WorkbenchTab[] = [
      makeTab('t-idle-1', 'idle-1', '/repos/alpha'),
      makeTab('t-idle-2', 'idle-2', '/repos/alpha'),
      makeTab('t-idle-3', 'idle-3', '/repos/alpha'),
      makeTab('t-idle-4', 'idle-4', '/repos/alpha'),
      makeTab('t-active', 'active-tab', '/repos/alpha'),
      makeTab('t-done', 'done-tab', '/repos/alpha'),
      makeTab('t-run', 'run-tab', '/repos/alpha'),
      makeTab('t-wait', 'wait-tab', '/repos/alpha'),
      makeTab('t-beta', 'beta-tab', '/repos/beta/sub'),
      makeTab('t-home', 'home-ops', '/home/dev'),
    ]

    const tabStatuses = {
      't-wait': 'waiting' as const,
      't-run': 'running' as const,
      't-done': 'done' as const,
    }

    const selectedTabs: string[] = []
    const m = mountView(TaskSidebar, {
      open: true,
      bots: [],
      worktrees: [],
      repoGroups,
      tabs,
      tabStatuses,
      activeSpaceKind: 'task',
      activeBotId: '',
      activeTabId: 't-active',
      onSelectTab: (tabId: string) => selectedTabs.push(tabId),
    })
    cleanups.push(m.unmount)

    // Both repo headers render with name & session count
    expect(textOf(byTestId(m.host, 'repo-group-0')[0]!)).toContain('alpha')
    expect(textOf(byTestId(m.host, 'repo-group-count-0')[0]!).trim()).toBe('8')
    expect(textOf(byTestId(m.host, 'repo-group-1')[0]!)).toContain('beta')
    expect(textOf(byTestId(m.host, 'repo-group-count-1')[0]!).trim()).toBe('1')

    // Sequential globalIdx worktree cards across repos: 0, 1, 2
    expect(byTestId(m.host, 'worktree-card-0')).toHaveLength(1)
    expect(byTestId(m.host, 'worktree-card-1')).toHaveLength(1)
    expect(byTestId(m.host, 'worktree-card-2')).toHaveLength(1)

    // Alpha primary worktree has 8 tabs -> collapsed to 6 by default, prioritizing waiting/running/done/activeTabId
    const card0 = byTestId(m.host, 'worktree-card-0')[0]!
    const card0TabNodes = findAll(card0, (n) => typeof n.props['data-testid'] === 'string' && String(n.props['data-testid']).startsWith('worktree-tab-'))
    expect(card0TabNodes).toHaveLength(6)
    const renderedIds = card0TabNodes.map((n) => String(n.props['data-testid']).replace('worktree-tab-', ''))
    expect(renderedIds.slice(0, 4)).toEqual(['t-wait', 't-run', 't-done', 't-active'])

    // Toggle expand shows all 8 tabs and switches button label
    const expandBtn = byTestId(m.host, 'worktree-expand-0')[0]!
    expect(textOf(expandBtn)).toContain('+ 展开其余 2 个会话')
    fire(expandBtn, 'onClick', { stopPropagation() {} })
    await nextTick()

    const expandedTabNodes = findAll(byTestId(m.host, 'worktree-card-0')[0]!, (n) => typeof n.props['data-testid'] === 'string' && String(n.props['data-testid']).startsWith('worktree-tab-'))
    expect(expandedTabNodes).toHaveLength(8)
    expect(textOf(byTestId(m.host, 'worktree-expand-0')[0]!)).toContain('收起会话列表')

    // Beta sub-directory tab matched under beta repo
    expect(byTestId(byTestId(m.host, 'worktree-card-2')[0]!, 'worktree-tab-t-beta')).toHaveLength(1)

    // Unmatched non-git tab renders in 📂 其他目录会话 card
    const otherCard = byTestId(m.host, 'other-dirs-card')
    expect(otherCard).toHaveLength(1)
    expect(textOf(otherCard[0]!)).toContain('其他目录会话')
    const homeTabBtn = byTestId(otherCard[0]!, 'worktree-tab-t-home')[0]!
    fire(homeTabBtn, 'onClick', { stopPropagation() {} })
    expect(selectedTabs).toEqual(['t-home'])
  })

  it('falls back to worktrees prop when repoGroups is empty', () => {
    const m = mountView(TaskSidebar, {
      open: true,
      bots: [],
      worktrees: [makeWt('/repos/legacy', 'main', true)],
      repoGroups: [],
      tabs: [makeTab('t1', 'shell-1', '/repos/legacy')],
      activeSpaceKind: 'task',
      activeBotId: '',
    })
    cleanups.push(m.unmount)

    expect(byTestId(m.host, 'worktree-card-0')).toHaveLength(1)
    expect(textOf(byTestId(m.host, 'repo-group-0')[0]!)).toContain('legacy')
    expect(byTestId(m.host, 'worktree-tab-t1')).toHaveLength(1)
  })

  it('uses 主目录/分支目录 badges (zero CoW/主检出) and displays live tabTools with agent highlight classes', () => {
    const m = mountView(TaskSidebar, {
      open: true,
      bots: [],
      worktrees: [
        makeWt('/repos/app', 'main', true),
        makeWt('/data/wt/app/feat-ux', 'feat-ux', false),
      ],
      repoGroups: [],
      tabs: [
        makeTab('t-claude', 'ui-work', '/repos/app', 'shell'),
        makeTab('t-codex', 'api-work', '/data/wt/app/feat-ux', 'shell'),
        makeTab('t-shell', 'plain', '/home/dev', 'shell'),
      ],
      tabTools: {
        't-claude': 'claude',
        't-codex': 'codex',
      },
      activeSpaceKind: 'task',
      activeBotId: '',
    })
    cleanups.push(m.unmount)

    const sidebarText = textOf(m.host)
    expect(sidebarText).toContain('主目录')
    expect(sidebarText).toContain('分支目录')
    expect(sidebarText).not.toContain('主检出')
    expect(sidebarText).not.toContain('CoW')

    const claudeTab = byTestId(m.host, 'worktree-tab-t-claude')[0]!
    expect(textOf(claudeTab)).toContain('claude')
    expect(textOf(claudeTab)).not.toContain('shell')
    const claudeBadge = findAll(claudeTab, (n) => String(n.props.class || '').includes('tw-sub-model'))[0]!
    expect(String(claudeBadge.props.class)).toContain('is-agent')
    expect(String(claudeBadge.props.class)).toContain('is-claude')

    const codexTab = byTestId(m.host, 'worktree-tab-t-codex')[0]!
    expect(textOf(codexTab)).toContain('codex')
    const codexBadge = findAll(codexTab, (n) => String(n.props.class || '').includes('tw-sub-model'))[0]!
    expect(String(codexBadge.props.class)).toContain('is-codex')

    const shellTab = byTestId(m.host, 'worktree-tab-t-shell')[0]!
    expect(textOf(shellTab)).toContain('shell')
  })
})

function makeProfile(id: string, name: string, engine: ProfileItem['engine'] = 'claude', isDefault = false): ProfileItem {
  return {
    id,
    name,
    engine,
    kind: 'gateway',
    model: `${engine}-model`,
    hasApiKey: true,
    configDir: `/home/dev/.${engine}`,
    isDefault,
    badge: `~/.${engine}`,
  }
}

describe('Holistic UX & zero-jargon polish across TelemetryBar, NewTaskModal, DispatchReviewModal, and CollectReviewBanner', () => {
  const BANNED_JARGON = ['CoW', 'M1a', 'M1b', 'M2', 'M3', 'M4', '0-Worktree', '★主卷', '主检出', '托管树', '＋配号']

  it('WorkbenchTelemetryBar caps visible profiles to 3 (default first), shows overflow pill, and uses human copy', () => {
    const opened: unknown[] = []
    const profiles = [
      makeProfile('p1', 'Profile 1', 'claude'),
      makeProfile('p2', 'Profile 2', 'codex'),
      makeProfile('p3', 'Profile 3', 'claude'),
      makeProfile('p4', 'Profile 4', 'codex'),
      makeProfile('p5', 'Profile 5', 'shell'),
      makeProfile('p6', 'Default Pro', 'claude', true),
    ]
    const worktrees: WorktreeInfo[] = [
      makeWt('/repos/app', 'main', true),
      { ...makeWt('/data/wt/feat', 'feat', false), cacheMB: 42 },
    ]

    const m = mountView(WorkbenchTelemetryBar, {
      profiles,
      defaultProfileId: 'p6',
      bots: [],
      worktrees,
      paneCount: 3,
      onOpenProfiles: (p: unknown) => opened.push(p),
    })
    cleanups.push(m.unmount)

    const profileBtns = findAll(
      m.host,
      (n) => typeof n.props['data-testid'] === 'string' && String(n.props['data-testid']).startsWith('telemetry-profile-'),
    )
    expect(profileBtns).toHaveLength(3)
    expect(profileBtns[0]!.props['data-testid']).toBe('telemetry-profile-p6')

    const allPill = byTestId(m.host, 'telemetry-all-profiles')
    expect(allPill).toHaveLength(1)
    expect(textOf(allPill[0]!)).toContain('📚 全部 6 套配置')
    fire(allPill[0]!, 'onClick')
    expect(opened[0]).toEqual({ tab: 'list' })

    const addBtn = byTestId(m.host, 'telemetry-add-profile')[0]!
    expect(textOf(addBtn)).toContain('＋新增配置')

    const text = textOf(m.host)
    expect(text).toContain('1 分支目录')
    expect(text).toContain('缓存 42MB')
    for (const term of BANNED_JARGON) {
      expect(text).not.toContain(term)
    }
  })

  it('NewTaskModal uses human-friendly mode cards, hides branch row in brainstorm mode, and has zero jargon', async () => {
    const m = mountView(NewTaskModal, {
      open: true,
      profiles: [makeProfile('p1', 'Claude Work', 'claude', true)],
      defaultProfileId: 'p1',
    })
    cleanups.push(m.unmount)

    const arenaText = textOf(m.host)
    expect(arenaText).toContain('＋ 新建任务')
    expect(arenaText).toContain('🌿 新建独立分支目录')
    expect(arenaText).toContain('💬 在当前目录直接开聊')
    expect(arenaText).toContain('将新建 1 个独立分支目录与终端')
    expect(byTestId(m.host, 'nt-branch-input')).toHaveLength(1)
    expect(byTestId(m.host, 'nt-baseref-input')).toHaveLength(1)
    for (const term of BANNED_JARGON) {
      expect(arenaText).not.toContain(term)
    }

    // Switching to brainstorm hides branch/baseRef inputs and updates budget hint
    fire(byTestId(m.host, 'nt-mode-brainstorm')[0]!, 'onClick')
    await nextTick()
    expect(byTestId(m.host, 'nt-branch-input')).toHaveLength(0)
    expect(byTestId(m.host, 'nt-baseref-input')).toHaveLength(0)
    expect(textOf(byTestId(m.host, 'nt-budget-hint')[0]!)).toContain('将在当前目录新开 1 个终端会话')
  })

  it('DispatchReviewModal and CollectReviewBanner have zero internal jargon', async () => {
    const dm = mountView(DispatchReviewModal, {
      open: true,
      profiles: [makeProfile('p1', 'Claude Work', 'claude', true)],
      defaultProfileId: 'p1',
    })
    cleanups.push(dm.unmount)

    const dmText = textOf(dm.host)
    expect(dmText).toContain('⚡ 多模型并发会审')
    expect(dmText).toContain('共享当前只读目录 · 不新建分支目录')
    expect(dmText).toContain('🙈 盲审求方案')
    expect(dmText).toContain('🔍 带方案挑刺')
    expect(dmText).toContain('🧬 审查当前改动 (Diff)')
    expect(dmText).toContain('结论以回执条呈现，可一键汇总填入当前会话输入框')
    for (const term of BANNED_JARGON) {
      expect(dmText).not.toContain(term)
    }

    const cb = mountView(CollectReviewBanner, {
      reviewers: [{ id: 'r1', title: 'Codex', status: 'done', summary: 'LGTM' }],
    })
    cleanups.push(cb.unmount)

    const cbText = textOf(cb.host)
    expect(cbText).toContain('📥 汇总填入当前会话输入框')
    for (const term of BANNED_JARGON) {
      expect(cbText).not.toContain(term)
    }
  })
})
