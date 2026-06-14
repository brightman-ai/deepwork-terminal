<template>
  <div class="cli-workbench" :class="{ 'is-mobile': isMobile }" data-testid="cli-workbench-page">

    <!-- Tab Bar (含侧栏触发器 + 右侧状态工具栏) -->
    <div class="workbench-tab-bar dw-titlebar-blend" ref="tabBarRef" data-testid="workbench-tab-bar">
      <template v-for="group in groups" :key="group.id">

        <!-- Group Header (仅多 group 时显示) -->
        <div
          v-if="showGroupHeaders"
          class="tab-group-header"
          :style="group.color ? { '--group-color': group.color } : {}"
          :data-testid="`workbench-group-header-${group.id}`"
          @click="toggleGroupCollapsed(group.id)"
        >
          <span class="group-name">{{ group.name }}</span>
          <span class="group-chevron">{{ group.collapsed ? '▸' : '▾' }}</span>
        </div>

        <!-- Tabs -->
        <template v-if="!group.collapsed">
          <button
            v-for="tab in group.tabs"
            :key="tab.id"
            class="workbench-tab"
            :class="{ 'is-active': tab.id === activeTab?.id }"
            :data-testid="`workbench-tab-${tab.id}`"
            @click="switchTab(tab.id)"
            @dblclick.stop="startRenameTab(tab.id)"
          >
            <span class="tab-agent-dot" :class="tabAgentDotClass(tab.id)" />
            <input
              v-if="renamingTabId === tab.id"
              v-model="renameValue"
              class="tab-rename-input"
              :data-testid="`workbench-tab-rename-${tab.id}`"
              @blur="commitRename"
              @keyup.enter="commitRename"
              @keyup.escape="cancelRename"
              @keydown.stop
              @keypress.stop
              @click.stop
              @dblclick.stop
              @mousedown.stop
            />
            <span v-else class="tab-name">{{ tab.name }}</span>
            <span
              class="tab-close"
              :data-testid="`workbench-tab-close-${tab.id}`"
              @click.stop="closeTab(tab.id)"
            >&times;</span>
          </button>
        </template>
      </template>

      <!-- + 号创建新 tab (直接创建，无对话框) -->
      <button
        class="workbench-tab workbench-tab--add"
        data-testid="workbench-add-tab"
        @click="quickCreateTab"
      >+</button>

      <!-- 右侧状态栏 -->
      <div class="tab-bar-spacer" />
      <div class="tab-bar-status" data-testid="tab-bar-status">
        <ConnectionStatus
          v-if="activeTab?.sessionId"
          :status="activeWsStatus"
          :rtt="activeNetStats?.rtt ?? 0"
          :upload-bps="activeNetStats?.uploadBps ?? 0"
          :download-bps="activeNetStats?.downloadBps ?? 0"
          data-testid="workbench-connection-status"
        />
        <AgentStatusBadge
          v-if="activeAgentState || activeAgentNotifications.length > 0"
          :state="activeAgentState"
          :notifications="activeAgentNotifications"
          data-testid="workbench-agent-status"
        />
        <SetupWizardIcon :inline="true" />
      </div>
    </div>

    <!-- Terminal 区域 -->
    <div class="workbench-terminal" data-testid="workbench-terminal">
      <div v-if="loading" class="workbench-state-msg" data-testid="workbench-loading">加载中…</div>
      <div v-else-if="error" class="workbench-state-msg workbench-state-error" data-testid="workbench-error">{{ error }}</div>
      <div v-else-if="!activeTab" class="workbench-state-msg" data-testid="workbench-empty">
        <p>正在准备终端…</p>
      </div>

      <!-- 每个 tab 渲染一个 Surface，通过 v-show 控制可见性，但只有 active 的才建 WS -->
      <template v-else>
        <CliTerminalSurface
          v-for="tab in allTabsWithSession"
          :key="tab.sessionId"
          v-show="tab.id === activeTab?.id"
          :session-id="tab.sessionId!"
          :session-name="tab.name"
          :active="tab.id === activeTab?.id"
          :ref="(el) => registerSurface(tab.id, el)"
          :data-testid="`workbench-surface-${tab.id}`"
          @agent-state="(s) => onTabAgentState(tab.id, s)"
          @agent-notifications="(s) => onTabAgentNotifications(tab.id, s)"
          @session-exit="(code) => onTabSessionExit(tab.id, code)"
          @connection-change="(s) => onTabConnectionChange(tab.id, s)"
        />
      </template>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, reactive, nextTick } from 'vue'
import CliTerminalSurface from '@terminal/components/terminal-session/CliTerminalSurface.vue'
import ConnectionStatus from '@terminal/components/terminal-session/ConnectionStatus.vue'
import AgentStatusBadge from '@terminal/components/terminal-session/AgentStatusBadge.vue'
import SetupWizardIcon from '@ce/components/wizard/SetupWizardIcon.vue'
import { useWorkbench } from '@terminal/composables/cli/useWorkbench'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import type { AgentState, WSConnectionStatus } from '@terminal/types/terminal'
import type { NetStats } from '@terminal/composables/cli/useWebSocketClient'

const { isMobile } = useDeviceDetection()
const { cliFetch } = useCliAuth()

const {
  loading,
  error,
  groups,
  activeTab,
  allTabs,
  showGroupHeaders,
  load,
  addTab,
  removeTab,
  renameTab,
  setActiveTab,
  toggleGroupCollapsed,
  bindSession,
  unbindSession,
} = useWorkbench()

// ─── Per-tab runtime state ────────────────────────────────────────────────────

interface TabRuntime {
  agentState: AgentState | null
  agentNotifications: AgentState[]
  wsStatus: WSConnectionStatus
}

const tabRuntimes = reactive<Record<string, TabRuntime>>({})

function ensureRuntime(tabId: string): TabRuntime {
  if (!tabRuntimes[tabId]) {
    tabRuntimes[tabId] = { agentState: null, agentNotifications: [], wsStatus: 'disconnected' }
  }
  return tabRuntimes[tabId]
}

// Per-tab surface refs
const surfaceRefs = reactive<Record<string, InstanceType<typeof CliTerminalSurface> | null>>({})

function registerSurface(tabId: string, el: any) {
  surfaceRefs[tabId] = el
}

// ─── Active tab computed helpers ──────────────────────────────────────────────

const activeWsStatus = computed<WSConnectionStatus>(() => {
  if (!activeTab.value) return 'disconnected'
  return tabRuntimes[activeTab.value.id]?.wsStatus ?? 'disconnected'
})

const activeAgentState = computed<AgentState | null>(() => {
  if (!activeTab.value) return null
  return tabRuntimes[activeTab.value.id]?.agentState ?? null
})

const activeAgentNotifications = computed<AgentState[]>(() => {
  if (!activeTab.value) return []
  return tabRuntimes[activeTab.value.id]?.agentNotifications ?? []
})

const activeNetStats = computed<NetStats | null>(() => {
  if (!activeTab.value) return null
  const ref = surfaceRefs[activeTab.value.id]
  return ref?.netStats ?? null
})

// Tabs that have a session bound (safe to render CliTerminalSurface)
const allTabsWithSession = computed(() =>
  allTabs.value.filter(t => !!t.sessionId)
)

// ─── Tab agent dot class ──────────────────────────────────────────────────────

function tabAgentDotClass(tabId: string): string {
  const rt = tabRuntimes[tabId]
  if (!rt) return 'dot-idle'
  const status = rt.agentState?.status
  if (status === 'running') return 'dot-running'
  if (status === 'waiting') return 'dot-waiting'
  if (rt.wsStatus === 'connected') return 'dot-connected'
  return 'dot-idle'
}

// ─── Surface event handlers ───────────────────────────────────────────────────

function onTabAgentState(tabId: string, state: AgentState | null) {
  ensureRuntime(tabId).agentState = state
}

function onTabAgentNotifications(tabId: string, state: AgentState[]) {
  ensureRuntime(tabId).agentNotifications = state
}

function onTabSessionExit(tabId: string, _exitCode: number) {
  ensureRuntime(tabId).wsStatus = 'disconnected'
}

function onTabConnectionChange(tabId: string, status: WSConnectionStatus) {
  ensureRuntime(tabId).wsStatus = status
}

// ─── Tab bar actions ──────────────────────────────────────────────────────────

function switchTab(tabId: string) {
  setActiveTab(tabId)
}

async function closeTab(tabId: string) {
  const tab = allTabs.value.find(t => t.id === tabId)
  if (tab?.sessionId) {
    try {
      await cliFetch(`/api/sessions/${tab.sessionId}`, { method: 'DELETE' })
    } catch { /* ignore */ }
  }
  delete tabRuntimes[tabId]
  delete surfaceRefs[tabId]
  removeTab(tabId)
}

// ─── Quick create (no dialog, like iTerm2 / cmux) ────────────────────────────

function nextTabName(): string {
  const existing = allTabs.value.map(t => t.name)
  for (let i = 1; ; i++) {
    const candidate = `终端 ${i}`
    if (!existing.includes(candidate)) return candidate
  }
}

async function quickCreateTab() {
  await createTabSilent({ name: nextTabName(), cwd: '~' })
}

// ─── Tab rename (double-click) ───────────────────────────────────────────────

const renamingTabId = ref<string | null>(null)
const renameValue = ref('')

function startRenameTab(tabId: string) {
  const tab = allTabs.value.find(t => t.id === tabId)
  if (!tab) return
  renamingTabId.value = tabId
  renameValue.value = tab.name
  nextTick(() => {
    const input = document.querySelector(`[data-testid="workbench-tab-rename-${tabId}"]`) as HTMLInputElement | null
    input?.select()
  })
}

function commitRename() {
  if (renamingTabId.value && renameValue.value.trim()) {
    renameTab(renamingTabId.value, renameValue.value.trim())
  }
  renamingTabId.value = null
}

function cancelRename() {
  renamingTabId.value = null
}

// ─── 初始化: 加载 workbench 配置 + 验证/恢复存量 sessions ───────────────────

async function reconcileSessions() {
  if (allTabs.value.length === 0) return

  // 拉取当前所有存活 sessions
  let liveSessions: Set<string> = new Set()
  try {
    const resp = await cliFetch('/api/sessions')
    if (resp.ok) {
      const list = await resp.json() as Array<{ id?: string; session_id?: string }>
      for (const s of list) {
        const id = s.id || s.session_id
        if (id) liveSessions.add(id)
      }
    }
  } catch { /* ignore */ }

  // 对每个已有 sessionId 的 tab：验证 session 是否存活
  for (const tab of allTabs.value) {
    if (tab.sessionId) {
      if (!liveSessions.has(tab.sessionId)) {
        // session 已消失，解绑 → 变成 orphan tab
        unbindSession(tab.id)
      } else {
        // session 存活，初始化 runtime
        ensureRuntime(tab.id)
      }
    }
  }

  // 第二轮：尝试为 orphan tab（无 sessionId）重建 session，失败则删除
  const orphans = allTabs.value.filter(t => !t.sessionId)
  for (const tab of orphans) {
    try {
      const resp = await cliFetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: tab.name, cwd: tab.cwd || '~' }),
      })
      if (resp.ok) {
        const data = await resp.json() as { id?: string; session_id?: string }
        const sessionId = data.id || data.session_id
        if (sessionId) {
          bindSession(tab.id, sessionId)
          ensureRuntime(tab.id)
          continue
        }
      }
    } catch { /* ignore */ }
    // 重建失败，删除该 orphan tab
    removeTab(tab.id)
  }

  // 清理后若全部 tab 已被删除，由 onMounted 后续逻辑创建默认 tab
}

/** 静默创建 tab — 不弹对话框，用于自动创建首个 tab 或恢复场景 */
async function createTabSilent(opts?: { name?: string; cwd?: string }) {
  const gid = groups.value[0]?.id
  if (!gid) return

  const tab = addTab(gid, {
    name: opts?.name,
    cwd: opts?.cwd || '~',
  })

  try {
    const resp = await cliFetch('/api/sessions', {
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
  } catch {
    removeTab(tab.id)
  }
}

onMounted(async () => {
  await load()
  await reconcileSessions()

  // 首次打开或所有 tab 已清空 → 自动创建默认 tab (~ + shell)
  if (allTabs.value.length === 0) {
    await createTabSilent({ name: nextTabName(), cwd: '~' })
  }
})
</script>

<style scoped>
/* ─── 整体布局 ─────────────────────────────────────────────────────────────── */
.cli-workbench {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  overflow: hidden;
}

/* ─── Tab Bar ─────────────────────────────────────────────────────────────── */
.workbench-tab-bar {
  display: flex;
  align-items: stretch;
  height: 36px;
  background: hsl(var(--card));
  border-bottom: 1px solid hsl(var(--border));
  overflow-x: auto;
  overflow-y: hidden;
  flex-shrink: 0;
  position: sticky;
  top: 0;
  z-index: 10;
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.workbench-tab-bar::-webkit-scrollbar {
  display: none;
}
/* ─── Tab Bar 右侧状态区 ───────────────────────────────────────────────────── */
.tab-bar-spacer {
  flex: 1;
  min-width: 8px;
}
.tab-bar-status {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 8px;
  flex-shrink: 0;
}

.cli-workbench.is-mobile .tab-bar-status {
  position: sticky;
  right: 0;
  z-index: 3;
  gap: 4px;
  padding: 0 6px;
  max-width: 54vw;
  background: hsl(var(--card));
  box-shadow: -10px 0 14px rgba(0, 0, 0, 0.22);
}
.cli-workbench.is-mobile .tab-bar-status :deep(.connection-status) {
  padding: 2px 5px;
}
.cli-workbench.is-mobile .tab-bar-status :deep(.status-text),
.cli-workbench.is-mobile .tab-bar-status :deep(.net-bw) {
  display: none;
}
.cli-workbench.is-mobile .tab-bar-status :deep(.agent-circles) {
  max-width: 76px;
  overflow-x: auto;
  scrollbar-width: none;
}
.cli-workbench.is-mobile .tab-bar-status :deep(.agent-circles::-webkit-scrollbar) {
  display: none;
}

/* ─── Group Header ────────────────────────────────────────────────────────── */
.tab-group-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 10px;
  font-size: 0.7rem;
  color: hsl(var(--muted-foreground));
  border-left: 3px solid var(--group-color, #4a9eff);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  user-select: none;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.tab-group-header:hover {
  background: hsl(var(--accent));
  color: hsl(var(--accent-foreground));
}
.group-chevron { font-size: 0.7rem; }

/* ─── Tab ─────────────────────────────────────────────────────────────────── */
.workbench-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 12px;
  min-width: 80px;
  max-width: 200px;
  height: 36px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: hsl(var(--muted-foreground));
  font-size: 0.8125rem;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  flex-shrink: 0;
  transition: background 0.12s, color 0.12s;
  outline: none;
  position: relative;
}
.workbench-tab:hover {
  background: hsl(var(--accent));
  color: hsl(var(--foreground));
}
.workbench-tab.is-active {
  border-bottom-color: #4a9eff;
  color: hsl(var(--foreground));
  background: hsl(var(--accent));
}

.tab-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tab-rename-input {
  flex: 1;
  min-width: 40px;
  max-width: 120px;
  height: 22px;
  border: 1px solid hsl(var(--border));
  border-radius: 3px;
  background: hsl(var(--background));
  color: inherit;
  font: inherit;
  padding: 0 4px;
  outline: none;
}

/* Close button: 只在 hover 或 active 时显示 */
.tab-close {
  font-size: 1rem;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
  border-radius: 3px;
  opacity: 0;
  margin-left: auto;
  color: hsl(var(--muted-foreground));
  transition: opacity 0.1s;
}
.workbench-tab:hover .tab-close,
.workbench-tab.is-active .tab-close {
  opacity: 0.6;
}
.tab-close:hover {
  opacity: 1 !important;
  color: #ff6b6b;
  background: rgba(255, 255, 255, 0.12);
}

/* ─── Agent Dot ───────────────────────────────────────────────────────────── */
.tab-agent-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}
.dot-idle      { background: #444; }
.dot-connected { background: #4caf50; }
.dot-running   { background: #4caf50; }
.dot-waiting   { background: #ff9800; }

/* ─── Add Tab Button ──────────────────────────────────────────────────────── */
.workbench-tab--add {
  min-width: 36px;
  max-width: 36px;
  font-size: 1.1rem;
  color: hsl(var(--muted-foreground));
  margin-left: auto;
  flex-shrink: 0;
  padding: 0;
  justify-content: center;
}
.workbench-tab--add:hover {
  color: rgba(255, 255, 255, 0.8);
}

/* ─── Terminal Surface Area ───────────────────────────────────────────────── */
.workbench-terminal {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  position: relative;
  display: flex;
  flex-direction: column;
}

.workbench-state-msg {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 48px;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}
.workbench-state-error {
  color: #f87171;
}

/* Surface 占满剩余空间 (flex, 不用 absolute — iOS Safari 下 absolute 会截断 toolbar) */
.workbench-terminal :deep(.cli-terminal-surface) {
  flex: 1;
  min-height: 0;
}

/* ─── Light mode dot overrides (semantic dot colors) ─────────────────────── */
@media (prefers-color-scheme: light) {
  .dot-idle      { background: #bbb; }
  .dot-connected { background: #2e7d32; }
  .dot-running   { background: #2e7d32; }
  .dot-waiting   { background: #e65100; }
}
</style>
