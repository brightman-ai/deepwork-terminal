<template>
  <aside
    v-show="open"
    class="tw-sidebar"
    :class="{ 'is-mobile': isMobile }"
    data-testid="task-sidebar"
  >
    <div class="tw-sidebar-head">
      <span class="tw-sidebar-title">工作空间</span>
      <div class="tw-sidebar-head-actions">
        <button
          type="button"
          class="tw-btn-accent"
          data-testid="btn-new-task"
          @click="emit('new-task')"
        >
          ＋ 新建任务
        </button>
        <button
          type="button"
          class="tw-icon-btn"
          data-testid="btn-collapse-sidebar"
          title="收起导航栏"
          @click="emit('collapse')"
        >
          ⇤
        </button>
      </div>
    </div>

    <div class="tw-sidebar-search">
      <span class="tw-search-ico" aria-hidden="true">🔍</span>
      <input
        :value="query"
        type="text"
        class="tw-search-input"
        data-testid="sidebar-filter-input"
        placeholder="过滤幕僚或工作树…"
        @input="onQueryInput"
      />
      <button
        v-if="query"
        type="button"
        class="tw-search-clear"
        data-testid="sidebar-filter-clear"
        title="清除过滤"
        @click="emit('update:query', '')"
      >
        ✕
      </button>
    </div>

    <div class="tw-sidebar-list">
      <!-- 1. 常驻幕僚 (BOTS) -->
      <div class="tw-section-head">
        <span>🤖 常驻幕僚 (BOTS · 7×24在线)</span>
      </div>

      <button
        v-for="(bot, bIdx) in visibleBots"
        :key="bot.id"
        type="button"
        class="tw-bot-row"
        :class="{ active: activeSpaceKind === 'bot' && activeBotId === bot.id }"
        :data-testid="`bot-nav-${bot.id}`"
        @click="emit('select-bot', bot.id)"
      >
        <div class="tw-bot-avatar" :class="{ purple: bIdx % 2 === 1 }">
          <span>{{ bot.emoji || '🤖' }}</span>
          <span
            class="tw-bot-avatar-dot"
            :style="{ background: botDotColor(bot) }"
          />
        </div>
        <div class="tw-bot-info">
          <div class="tw-bot-info-top">
            <strong class="tw-bot-name">{{ bot.name }}</strong>
            <span class="tw-bot-host">{{ hostLabel }}</span>
          </div>
          <div class="tw-bot-sub">
            <template v-if="botPendingNeedsYou(bot) > 0">
              <span class="tw-c-waiting">● {{ botPendingNeedsYou(bot) }} 待你拍板</span>
              <span v-if="botWorkingCount(bot) > 0">
                · ◐ {{ botWorkingCount(bot) }} 排查中
              </span>
            </template>
            <template v-else-if="botWorkingCount(bot) > 0">
              <span class="tw-c-running">◐ {{ botWorkingCount(bot) }} 排查中</span>
            </template>
            <template v-else>
              <span>静默值守中 · 0 空跑</span>
            </template>
          </div>
        </div>
      </button>

      <div v-if="!visibleBots.length" class="tw-empty-hint">
        暂无匹配的常驻幕僚
      </div>

      <!-- 2. 代码工作树 (WORKTREES) -->
      <div class="tw-section-head tw-section-head-mt">
        <span>⎇ 代码工作树 (WORKTREES)</span>
      </div>

      <div class="tw-filter-row" role="group" aria-label="工作树状态筛选">
        <button
          type="button"
          class="tw-filter-pill"
          :class="{ active: filter === 'all' }"
          data-testid="sidebar-filter-all"
          @click="emit('update:filter', 'all')"
        >
          全部
        </button>
        <button
          type="button"
          class="tw-filter-pill"
          :class="{ active: filter === 'waiting' }"
          data-testid="sidebar-filter-waiting"
          @click="emit('update:filter', 'waiting')"
        >
          🔴 待确认
        </button>
        <button
          type="button"
          class="tw-filter-pill"
          :class="{ active: filter === 'active' }"
          data-testid="sidebar-filter-active"
          @click="emit('update:filter', 'active')"
        >
          ◐ 活跃
        </button>
      </div>

      <template v-for="(grp, gIdx) in visibleRepoGroups" :key="grp.root || grp.name">
        <div
          v-if="grp.name"
          class="tw-repo-header"
          :data-testid="`repo-group-${gIdx}`"
        >
          <span class="tw-repo-name" :title="grp.root">📁 {{ grp.name }}</span>
          <span
            class="tw-repo-count"
            :data-testid="`repo-group-count-${gIdx}`"
            title="关联会话数"
          >
            {{ grp.displaySessionCount }}
          </span>
          <span class="tw-repo-host">🖥 {{ hostLabel }}</span>
        </div>

        <div
          v-for="entry in grp.entries"
          :key="entry.wt.path"
          class="tw-task-card"
          :class="{ active: isWorktreeActive(entry.wt) }"
          :data-testid="`worktree-card-${entry.globalIdx}`"
          role="button"
          tabindex="0"
          @click="emit('select-worktree', entry.wt)"
          @keydown.enter.prevent="emit('select-worktree', entry.wt)"
          @keydown.space.prevent="emit('select-worktree', entry.wt)"
        >
          <div class="tw-task-card-top">
            <span class="tw-task-num">{{ entry.globalIdx + 1 }}</span>
            <span v-if="entry.status === 'running'" class="tw-spin-ring" />
            <span v-else class="tw-status-dot" :class="entry.status" />
            <span class="tw-task-title" :title="entry.wt.path">
              {{ worktreeLabel(entry.wt) }}
            </span>
            <span class="tw-badge-tiny" :class="worktreeBadgeClass(entry.wt)">
              {{ worktreeBadgeText(entry.wt) }}
            </span>
          </div>

          <div class="tw-task-meta-line">
            <span class="tw-task-branch">{{ entry.wt.branch || '(detached)' }}</span>
            <span v-if="entry.wt.dirty" class="tw-dirty-dot" title="有未提交改动">● 脏</span>
            <span v-if="!entry.wt.isPrimary" class="tw-wt-actions" @click.stop>
              <button
                type="button"
                class="tw-mini-act"
                :data-testid="`worktree-merge-${entry.globalIdx}`"
                title="合入主分支"
                @click="emit('merge-worktree', entry.wt)"
              >
                合入
              </button>
              <button
                type="button"
                class="tw-mini-act danger"
                :data-testid="`worktree-remove-${entry.globalIdx}`"
                title="移除工作树"
                @click="emit('remove-worktree', entry.wt)"
              >
                ✕
              </button>
            </span>
          </div>

          <div v-if="entry.tabs.length > 0" class="tw-sub-agents">
            <button
              v-for="tb in displayedTabsFor(entry.wt.path, entry.tabs)"
              :key="tb.id"
              type="button"
              class="tw-sub-agent-row"
              :class="{ current: tb.id === activeTabId && activeSpaceKind === 'task' }"
              :data-testid="`worktree-tab-${tb.id}`"
              @click.stop="emit('select-tab', tb.id, entry.wt)"
            >
              <span
                v-if="tabStatusOf(tb.id) === 'running'"
                class="tw-spin-ring small"
              />
              <span
                v-else
                class="tw-status-dot small"
                :class="tabStatusOf(tb.id)"
              />
              <span class="tw-sub-title">{{ tb.name }}</span>
              <span class="tw-sub-model" :class="tabToolClass(tb)">{{ tabToolLabel(tb) }}</span>
            </button>
            <button
              v-if="entry.tabs.length > MAX_COLLAPSED_TABS"
              type="button"
              class="tw-expand-tabs-btn"
              :data-testid="`worktree-expand-${entry.globalIdx}`"
              @click.stop="toggleExpandWorktree(entry.wt.path)"
            >
              {{
                isWorktreeExpanded(entry.wt.path)
                  ? '收起会话列表'
                  : `+ 展开其余 ${entry.tabs.length - MAX_COLLAPSED_TABS} 个会话`
              }}
            </button>
          </div>
        </div>
      </template>

      <!-- 未归属任何 Git 工作树的其他目录会话 -->
      <div
        v-if="visibleOtherTabs.length > 0"
        class="tw-task-card tw-other-dirs-card"
        data-testid="other-dirs-card"
      >
        <div class="tw-task-card-top">
          <span class="tw-task-title">📂 其他目录会话</span>
          <span class="tw-badge-tiny">{{ visibleOtherTabs.length }}</span>
        </div>
        <div class="tw-sub-agents">
          <button
            v-for="tb in displayedTabsFor(OTHER_DIRS_KEY, visibleOtherTabs)"
            :key="tb.id"
            type="button"
            class="tw-sub-agent-row"
            :class="{ current: tb.id === activeTabId && activeSpaceKind === 'task' }"
            :data-testid="`worktree-tab-${tb.id}`"
            @click.stop="emit('select-tab', tb.id, fallbackWorktreeForTab(tb))"
          >
            <span
              v-if="tabStatusOf(tb.id) === 'running'"
              class="tw-spin-ring small"
            />
            <span
              v-else
              class="tw-status-dot small"
              :class="tabStatusOf(tb.id)"
            />
            <span class="tw-sub-title" :title="tb.cwd || ''">{{ tb.name }}</span>
            <span class="tw-sub-model" :class="tabToolClass(tb)">{{ tabToolLabel(tb) }}</span>
          </button>
          <button
            v-if="visibleOtherTabs.length > MAX_COLLAPSED_TABS"
            type="button"
            class="tw-expand-tabs-btn"
            data-testid="other-tabs-expand"
            @click.stop="toggleExpandWorktree(OTHER_DIRS_KEY)"
          >
            {{
              isWorktreeExpanded(OTHER_DIRS_KEY)
                ? '收起会话列表'
                : `+ 展开其余 ${visibleOtherTabs.length - MAX_COLLAPSED_TABS} 个会话`
            }}
          </button>
        </div>
      </div>

      <div v-if="!visibleRepoGroups.length && !visibleOtherTabs.length" class="tw-empty-hint">
        暂无匹配的代码工作树
      </div>

      <div v-if="isMobile" class="tw-mobile-prof-wrap">
        <button
          type="button"
          class="tw-btn-ghost-full"
          data-testid="btn-mobile-profiles"
          @click="emit('open-profiles')"
        >
          👤 账号与 API 网关配置 ({{ profileCount }})
        </button>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { BotDetail } from '@terminal/api/bots'
import type { RepoWorktreeGroup, WorktreeInfo } from '@terminal/api/worktrees'
import type { WorkbenchTab } from '@terminal/types/workbench'
import type { SidebarFilter, SpaceKind } from '@terminal/composables/cli/useTaskWorkbench'

export type TaskStatusKind = 'waiting' | 'running' | 'done' | 'idle'

const MAX_COLLAPSED_TABS = 6
const OTHER_DIRS_KEY = '__other_dirs__'

const props = withDefaults(
  defineProps<{
    open: boolean
    bots: BotDetail[]
    worktrees: WorktreeInfo[]
    repoGroups?: RepoWorktreeGroup[]
    tabs?: WorkbenchTab[]
    tabStatuses?: Record<string, TaskStatusKind>
    tabTools?: Record<string, string>
    activeSpaceKind: SpaceKind
    activeBotId: string
    activeWorktreePath?: string
    activeTabId?: string
    repoName?: string
    hostLabel?: string
    filter?: SidebarFilter
    query?: string
    isMobile?: boolean
    profileCount?: number
  }>(),
  {
    repoGroups: () => [],
    tabs: () => [],
    tabStatuses: () => ({}),
    tabTools: () => ({}),
    activeWorktreePath: '',
    activeTabId: '',
    repoName: '',
    hostLabel: 'local',
    filter: 'all',
    query: '',
    isMobile: false,
    profileCount: 0,
  },
)

const expandedWorktrees = ref<Record<string, boolean>>({})

function isWorktreeExpanded(key: string): boolean {
  return Boolean(expandedWorktrees.value[key])
}

function toggleExpandWorktree(key: string): void {
  expandedWorktrees.value = {
    ...expandedWorktrees.value,
    [key]: !expandedWorktrees.value[key],
  }
}

const emit = defineEmits<{
  (e: 'update:filter', value: SidebarFilter): void
  (e: 'update:query', value: string): void
  (e: 'collapse'): void
  (e: 'new-task'): void
  (e: 'select-bot', botId: string): void
  (e: 'select-worktree', wt: WorktreeInfo): void
  (e: 'select-tab', tabId: string, wt: WorktreeInfo): void
  (e: 'merge-worktree', wt: WorktreeInfo): void
  (e: 'remove-worktree', wt: WorktreeInfo): void
  (e: 'open-profiles'): void
}>()

function onQueryInput(ev: Event): void {
  const target = ev.target as HTMLInputElement | null
  emit('update:query', target?.value ?? '')
}

function botPendingNeedsYou(bot: BotDetail): number {
  const fromItems = bot.items.filter(
    (it) => it.status === 'needs_you' && !it.spawnedWorktreePath,
  ).length
  return fromItems || bot.needsYouCount || 0
}

function botWorkingCount(bot: BotDetail): number {
  const fromItems = bot.items.filter((it) => it.status === 'working').length
  return fromItems || bot.workingCount || 0
}

function botDotColor(bot: BotDetail): string {
  if (botPendingNeedsYou(bot) > 0) return 'var(--waiting, #ff6b6b)'
  if (botWorkingCount(bot) > 0) return 'var(--running, #3dd68c)'
  return '#596273'
}

const normalizedQuery = computed(() => props.query.trim().toLowerCase())

const visibleBots = computed(() => {
  const q = normalizedQuery.value
  if (!q) return props.bots
  return props.bots.filter((b) =>
    `${b.name} ${b.roleTitle} ${b.slug}`.toLowerCase().includes(q),
  )
})

function tabStatusOf(tabId: string): TaskStatusKind {
  return props.tabStatuses[tabId] ?? 'idle'
}

function tabToolLabel(tb: WorkbenchTab): string {
  return props.tabTools[tb.id] || tb.engine || 'shell'
}

function tabToolClass(tb: WorkbenchTab): string {
  const tool = tabToolLabel(tb).toLowerCase()
  if (tool === 'claude' || tool === 'codex' || tool === 'agy') {
    return `is-agent is-${tool}`
  }
  return ''
}

function normalizePath(p?: string): string {
  return (p || '').replace(/\/+$/, '')
}

function tabPriority(tb: WorkbenchTab): number {
  const st = tabStatusOf(tb.id)
  if (st === 'waiting') return 0
  if (st === 'running') return 1
  if (st === 'done') return 2
  if (tb.id === props.activeTabId) return 3
  return 4
}

function prioritizeTabs(list: WorkbenchTab[]): WorkbenchTab[] {
  return list.slice().sort((a, b) => tabPriority(a) - tabPriority(b))
}

function displayedTabsFor(key: string, tabs: WorkbenchTab[]): WorkbenchTab[] {
  const sorted = prioritizeTabs(tabs)
  if (sorted.length <= MAX_COLLAPSED_TABS || isWorktreeExpanded(key)) {
    return sorted
  }
  return sorted.slice(0, MAX_COLLAPSED_TABS)
}

function worktreeStatus(wt: WorktreeInfo, wtTabs: WorkbenchTab[]): TaskStatusKind {
  const statuses = wtTabs.map((t) => tabStatusOf(t.id))
  if (statuses.includes('waiting')) return 'waiting'
  if (statuses.includes('running')) return 'running'
  if (statuses.includes('done') || wt.dirty) return 'done'
  return 'idle'
}

const repoDisplayName = computed(() => {
  if (props.repoName) return props.repoName
  const primary = props.worktrees.find((w) => w.isPrimary) ?? props.worktrees[0]
  if (!primary?.path) return ''
  const parts = normalizePath(primary.path).split('/')
  return parts[parts.length - 1] || primary.path
})

const effectiveRepoGroups = computed<RepoWorktreeGroup[]>(() => {
  if (props.repoGroups && props.repoGroups.length > 0) {
    return props.repoGroups
  }
  if (!props.worktrees.length) return []
  const primary = props.worktrees.find((w) => w.isPrimary) ?? props.worktrees[0]
  return [
    {
      root: primary?.path || '',
      name: repoDisplayName.value || '当前仓库',
      worktrees: props.worktrees,
      sessionCount: 0,
    },
  ]
})

const tabAssignments = computed(() => {
  const allWorktrees: { path: string; norm: string }[] = []
  for (const grp of effectiveRepoGroups.value) {
    for (const wt of grp.worktrees) {
      const norm = normalizePath(wt.path)
      if (norm) allWorktrees.push({ path: wt.path, norm })
    }
  }

  const byWorktreeNorm = new Map<string, WorkbenchTab[]>()
  const unmatched: WorkbenchTab[] = []

  for (const tb of props.tabs) {
    const cwd = normalizePath(tb.cwd)
    let bestNorm = ''
    if (cwd) {
      for (const wt of allWorktrees) {
        if (cwd === wt.norm || cwd.startsWith(`${wt.norm}/`)) {
          if (wt.norm.length > bestNorm.length) {
            bestNorm = wt.norm
          }
        }
      }
    }
    if (bestNorm) {
      const arr = byWorktreeNorm.get(bestNorm)
      if (arr) arr.push(tb)
      else byWorktreeNorm.set(bestNorm, [tb])
    } else {
      unmatched.push(tb)
    }
  }

  return { byWorktreeNorm, unmatched }
})

interface SidebarWorktreeEntry {
  wt: WorktreeInfo
  tabs: WorkbenchTab[]
  status: TaskStatusKind
  globalIdx: number
}

interface SidebarRepoGroupView {
  root: string
  name: string
  displaySessionCount: number
  entries: SidebarWorktreeEntry[]
}

const visibleRepoGroups = computed<SidebarRepoGroupView[]>(() => {
  const q = normalizedQuery.value
  const { byWorktreeNorm } = tabAssignments.value
  let globalIdx = 0
  const out: SidebarRepoGroupView[] = []

  for (const grp of effectiveRepoGroups.value) {
    const entries: SidebarWorktreeEntry[] = []
    let matchedInRepo = 0

    for (const wt of grp.worktrees) {
      const wtTabs = byWorktreeNorm.get(normalizePath(wt.path)) ?? []
      matchedInRepo += wtTabs.length
      const status = worktreeStatus(wt, wtTabs)

      if (props.filter === 'waiting' && status !== 'waiting') continue
      if (props.filter === 'active' && status !== 'running' && status !== 'done') {
        continue
      }
      if (q) {
        const hay = `${grp.name} ${wt.branch} ${wt.path} ${wtTabs.map((t) => `${t.name} ${t.cwd || ''}`).join(' ')}`.toLowerCase()
        if (!hay.includes(q)) continue
      }

      entries.push({ wt, tabs: wtTabs, status, globalIdx })
      globalIdx += 1
    }

    if (entries.length > 0) {
      out.push({
        root: grp.root,
        name: grp.name,
        displaySessionCount: grp.sessionCount > 0 ? grp.sessionCount : matchedInRepo,
        entries,
      })
    }
  }

  return out
})

const visibleOtherTabs = computed<WorkbenchTab[]>(() => {
  const q = normalizedQuery.value
  return tabAssignments.value.unmatched.filter((tb) => {
    const st = tabStatusOf(tb.id)
    if (props.filter === 'waiting' && st !== 'waiting') return false
    if (props.filter === 'active' && st !== 'running' && st !== 'done') return false
    if (q && !`${tb.name} ${tb.cwd || ''} ${tb.engine || ''}`.toLowerCase().includes(q)) {
      return false
    }
    return true
  })
})

function fallbackWorktreeForTab(tb: WorkbenchTab): WorktreeInfo {
  return {
    path: tb.cwd || '',
    branch: '',
    head: '',
    isPrimary: false,
    dirty: false,
    merged: false,
    cacheMB: 0,
  }
}

function worktreeLabel(wt: WorktreeInfo): string {
  if (wt.isPrimary) return wt.branch || '主目录'
  const base = wt.path.replace(/\/+$/, '').split('/').pop()
  return wt.branch || base || wt.path
}

function worktreeBadgeClass(wt: WorktreeInfo): string {
  if (wt.isPrimary) return 'primary'
  if (wt.merged) return 'bot'
  if (wt.cacheMB >= 200) return 'warn'
  return ''
}

function worktreeBadgeText(wt: WorktreeInfo): string {
  if (wt.isPrimary) return '主目录'
  if (wt.merged) return '已合入'
  if (wt.cacheMB > 0) return `${wt.cacheMB}M`
  return '分支目录'
}

function isWorktreeActive(wt: WorktreeInfo): boolean {
  if (props.activeSpaceKind !== 'task') return false
  if (!props.activeWorktreePath) return wt.isPrimary
  const a = props.activeWorktreePath.replace(/\/+$/, '')
  const b = wt.path.replace(/\/+$/, '')
  return a === b || a.startsWith(`${b}/`)
}
</script>

<style scoped>
.tw-sidebar {
  width: 250px;
  height: 100%;
  background: var(--bg-sidebar, #11141a);
  color: var(--text, #eef1f6);
  border-right: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  user-select: none;
  z-index: 35;
  font-size: 13px;
}
.tw-sidebar.is-mobile {
  position: absolute;
  inset: 0 auto 0 0;
  width: min(286px, 86vw);
  box-shadow: 16px 0 48px rgba(0, 0, 0, 0.75);
}
.tw-sidebar-head {
  height: 38px;
  padding: 0 8px 0 12px;
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.tw-sidebar-title {
  font-weight: 600;
  font-size: 12px;
  color: var(--text, #eef1f6);
}
.tw-sidebar-head-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 4px;
}
.tw-btn-accent {
  height: 25px;
  padding: 0 8px;
  border-radius: 5px;
  border: 1px solid rgba(110, 168, 254, 0.42);
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #d6e6ff;
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
}
.tw-btn-accent:hover {
  background: rgba(110, 168, 254, 0.24);
}
.tw-icon-btn {
  min-width: 26px;
  height: 26px;
  padding: 0 6px;
  border-radius: 5px;
  color: var(--text-secondary, #a2acc0);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 11.5px;
  background: transparent;
  border: 0;
  cursor: pointer;
}
.tw-icon-btn:hover {
  background: var(--bg-hover, #222937);
  color: var(--text, #eef1f6);
}
.tw-sidebar-search {
  margin: 7px 8px 6px;
  height: 29px;
  padding: 0 8px;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.035);
  border: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.tw-sidebar-search:focus-within {
  border-color: #6ea8fe;
}
.tw-search-ico {
  font-size: 11px;
  color: var(--text-muted, #6c7688);
}
.tw-search-input {
  flex: 1;
  min-width: 0;
  border: 0;
  background: transparent;
  font-size: 11.5px;
  color: var(--text, #eef1f6);
  outline: none;
}
.tw-search-clear {
  background: transparent;
  border: 0;
  color: var(--text-muted, #6c7688);
  cursor: pointer;
  font-size: 10px;
  padding: 2px 4px;
}
.tw-search-clear:hover {
  color: #fff;
}
.tw-sidebar-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 2px 8px 12px;
  scrollbar-width: thin;
}
.tw-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 5px 3px;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.04em;
  color: var(--text-muted, #6c7688);
}
.tw-section-head-mt {
  margin-top: 6px;
}
.tw-bot-row {
  width: 100%;
  text-align: left;
  padding: 7px 8px;
  border-radius: 8px;
  border: 1px solid transparent;
  margin-bottom: 3px;
  display: flex;
  align-items: center;
  gap: 8px;
  background: transparent;
  cursor: pointer;
}
.tw-bot-row:hover {
  background: var(--bg-hover, #222937);
}
.tw-bot-row.active {
  background: linear-gradient(90deg, rgba(61, 214, 140, 0.14), rgba(37, 48, 68, 0.85));
  border-color: rgba(61, 214, 140, 0.45);
}
.tw-bot-avatar {
  width: 28px;
  height: 28px;
  border-radius: 7px;
  background: #1b2824;
  border: 1px solid rgba(61, 214, 140, 0.4);
  display: grid;
  place-items: center;
  font-size: 14px;
  flex-shrink: 0;
  position: relative;
}
.tw-bot-avatar.purple {
  background: #231d33;
  border-color: rgba(180, 142, 255, 0.4);
}
.tw-bot-avatar-dot {
  position: absolute;
  right: -2px;
  bottom: -2px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  border: 1.5px solid var(--bg-sidebar, #11141a);
}
.tw-bot-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.tw-bot-info-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
}
.tw-bot-name {
  font-size: 12.5px;
  color: var(--text, #eef1f6);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tw-bot-host {
  font-size: 10px;
  color: var(--text-muted, #6c7688);
  font-family: var(--font-mono, ui-monospace, monospace);
}
.tw-bot-sub {
  font-size: 10.5px;
  color: var(--text-secondary, #a2acc0);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tw-c-waiting {
  color: var(--waiting, #ff6b6b);
}
.tw-c-running {
  color: var(--running, #3dd68c);
}
.tw-filter-row {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 2px 4px 4px;
}
.tw-filter-pill {
  font-size: 10.5px;
  padding: 1px 7px;
  border-radius: 99px;
  color: var(--text-muted, #6c7688);
  background: transparent;
  border: 1px solid transparent;
  cursor: pointer;
}
.tw-filter-pill.active {
  color: var(--text, #eef1f6);
  background: rgba(255, 255, 255, 0.06);
  border-color: var(--line-strong, rgba(255, 255, 255, 0.145));
  font-weight: 500;
}
.tw-repo-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 5px 2px;
  color: var(--text-secondary, #a2acc0);
  font-size: 11px;
  font-weight: 600;
}
.tw-repo-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tw-repo-count {
  font-size: 10px;
  font-weight: 500;
  color: #b8ccf0;
  background: rgba(110, 168, 254, 0.14);
  border: 1px solid rgba(110, 168, 254, 0.28);
  padding: 0 5px;
  border-radius: 99px;
  font-family: var(--font-mono, ui-monospace, monospace);
  flex-shrink: 0;
}
.tw-repo-host {
  margin-left: auto;
  font-weight: 400;
  font-size: 10px;
  color: var(--text-muted, #6c7688);
  background: rgba(255, 255, 255, 0.045);
  padding: 1px 5px;
  border-radius: 4px;
  flex-shrink: 0;
}
.tw-expand-tabs-btn {
  margin-top: 2px;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px dashed var(--line-strong, rgba(255, 255, 255, 0.145));
  background: rgba(255, 255, 255, 0.03);
  color: var(--text-muted, #8a95aa);
  font-size: 10.5px;
  text-align: left;
  cursor: pointer;
}
.tw-expand-tabs-btn:hover {
  background: rgba(255, 255, 255, 0.07);
  color: #fff;
}
.tw-other-dirs-card {
  margin-top: 8px;
  cursor: default;
  border-color: var(--line, rgba(255, 255, 255, 0.075));
  background: rgba(255, 255, 255, 0.02);
}
.tw-task-card {
  width: 100%;
  text-align: left;
  padding: 7px 9px;
  border-radius: 8px;
  border: 1px solid transparent;
  margin-top: 3px;
  display: flex;
  flex-direction: column;
  gap: 3px;
  cursor: pointer;
}
.tw-task-card:hover {
  background: var(--bg-hover, #222937);
}
.tw-task-card.active {
  background: var(--bg-active, #253044);
  border-color: rgba(110, 168, 254, 0.38);
}
.tw-task-card-top {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.tw-task-num {
  font-size: 10px;
  color: var(--text-muted, #6c7688);
  width: 11px;
  flex-shrink: 0;
  font-family: var(--font-mono, ui-monospace, monospace);
}
.tw-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  display: inline-block;
}
.tw-status-dot.small {
  width: 6px;
  height: 6px;
}
.tw-status-dot.waiting {
  background: var(--waiting, #ff6b6b);
  box-shadow: 0 0 0 3px var(--waiting-bg, rgba(255, 107, 107, 0.14));
}
.tw-status-dot.running {
  background: var(--running, #3dd68c);
}
.tw-status-dot.done {
  background: var(--done, #f3b855);
}
.tw-status-dot.idle {
  background: #596273;
}
@keyframes twSpin {
  to {
    transform: rotate(360deg);
  }
}
.tw-spin-ring {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  border: 1.8px solid rgba(243, 184, 85, 0.28);
  border-top-color: #f3b855;
  display: inline-block;
  flex-shrink: 0;
  animation: twSpin 0.9s linear infinite;
}
.tw-spin-ring.small {
  width: 7px;
  height: 7px;
}
.tw-task-title {
  font-size: 12.5px;
  color: var(--text, #eef1f6);
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
}
.tw-badge-tiny {
  font-size: 9.5px;
  padding: 0 4px;
  border-radius: 3px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  color: var(--text-secondary, #a2acc0);
  white-space: nowrap;
}
.tw-badge-tiny.primary {
  border-color: #4b5c78;
  color: #b8ccf0;
}
.tw-badge-tiny.bot {
  border-color: rgba(61, 214, 140, 0.45);
  color: #8ce8b5;
  background: rgba(61, 214, 140, 0.12);
}
.tw-badge-tiny.warn {
  border-color: rgba(243, 184, 85, 0.45);
  color: #f5ca7b;
}
.tw-task-meta-line {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: 17px;
  font-size: 10.5px;
  color: var(--text-muted, #6c7688);
  font-family: var(--font-mono, ui-monospace, monospace);
}
.tw-task-branch {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.tw-dirty-dot {
  color: var(--done, #f3b855);
  font-size: 10px;
  flex-shrink: 0;
}
.tw-wt-actions {
  display: inline-flex;
  gap: 3px;
  opacity: 0;
  transition: opacity 0.12s ease;
}
.tw-task-card:hover .tw-wt-actions,
.tw-task-card:focus-within .tw-wt-actions {
  opacity: 1;
}
.tw-mini-act {
  font-size: 10px;
  padding: 0 4px;
  border-radius: 3px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-surface, #161a22);
  color: var(--text-secondary, #a2acc0);
  cursor: pointer;
}
.tw-mini-act:hover {
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #fff;
}
.tw-mini-act.danger:hover {
  background: rgba(255, 107, 107, 0.2);
  border-color: rgba(255, 107, 107, 0.5);
  color: #ffb8b8;
}
.tw-sub-agents {
  padding-left: 16px;
  margin-top: 2px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.tw-sub-agent-row {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
  padding: 2px 4px;
  border-radius: 4px;
  background: transparent;
  border: 0;
  cursor: pointer;
  width: 100%;
  text-align: left;
}
.tw-sub-agent-row:hover,
.tw-sub-agent-row.current {
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
}
.tw-sub-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.tw-sub-model {
  margin-left: auto;
  font-size: 10px;
  color: var(--text-muted, #6c7688);
  font-family: var(--font-mono, ui-monospace, monospace);
}
.tw-sub-model.is-agent {
  padding: 0 5px;
  border-radius: 3px;
  background: rgba(110, 168, 254, 0.14);
  color: #b7d4ff;
  font-weight: 500;
}
.tw-sub-model.is-claude {
  background: rgba(229, 142, 115, 0.15);
  color: #f0b29e;
}
.tw-sub-model.is-codex {
  background: rgba(125, 211, 252, 0.15);
  color: #9be0ff;
}
.tw-sub-model.is-agy {
  background: rgba(180, 142, 255, 0.16);
  color: #d6c2ff;
}
.tw-empty-hint {
  padding: 10px 8px;
  font-size: 11.5px;
  color: var(--text-muted, #6c7688);
  text-align: center;
}
.tw-mobile-prof-wrap {
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px solid var(--line, rgba(255, 255, 255, 0.075));
}
.tw-btn-ghost-full {
  width: 100%;
  padding: 6px 10px;
  border-radius: 6px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-surface, #161a22);
  color: var(--text-secondary, #a2acc0);
  font-size: 11.5px;
  cursor: pointer;
}
.tw-btn-ghost-full:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
</style>
