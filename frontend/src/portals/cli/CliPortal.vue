<template>
  <div
    class="cli-portal"
    :class="{ 'is-mobile': breakpoint.isMobile.value, 'is-non-terminal': workbench.activeSpaceKind.value !== 'task' || centerPreviewActive }"
    data-testid="cli-portal"
  >
    <!-- D6: first-run shortcuts guide, collapses into a small icon after acknowledgement. -->
    <ShortcutsGuideBanner
      v-show="!saveError && workbench.activeSpaceKind.value === 'task' && !centerPreviewActive && !workbench.newTaskModalOpen.value && !workbench.profileModalOpen.value && !dispatchModalOpen"
      :on-open-settings="openShortcutsSettings"
    />

    <div class="cli-portal__body">
      <!-- Mobile scrim backdrop behind slide-over TaskSidebar drawer -->
      <div
        v-if="breakpoint.isMobile.value && workbench.sidebarOpen.value"
        class="cli-portal__sidebar-scrim"
        data-testid="task-sidebar-scrim"
        @click="workbench.setSidebarOpen(false)"
      />

      <!-- Collapsible left TaskSidebar: Resident Bots + Git Worktrees -->
      <TaskSidebar
        :open="workbench.sidebarOpen.value"
        :bots="workbench.bots.value"
        :worktrees="workbench.worktrees.value"
        :repo-groups="workbench.repoGroups.value"
        :tabs="allTabs"
        :tab-statuses="sidebarTabStatuses"
        :tab-tools="sidebarTabTools"
        :active-space-kind="workbench.activeSpaceKind.value"
        :active-bot-id="workbench.activeBotId.value"
        :active-worktree-path="activeTab?.cwd || ''"
        :active-tab-id="activeTab?.id || ''"
        :host-label="activeHostLabel"
        :filter="workbench.sidebarFilter.value"
        :query="workbench.sidebarQuery.value"
        :is-mobile="breakpoint.isMobile.value"
        :profile-count="workbench.profiles.value.length"
        @update:filter="workbench.sidebarFilter.value = $event"
        @update:query="workbench.sidebarQuery.value = $event"
        @collapse="workbench.setSidebarOpen(false)"
        @new-task="openNewTask"
        @select-bot="onSelectBot"
        @select-worktree="onSelectWorktree"
        @select-tab="onSelectSidebarTab"
        @merge-worktree="onMergeWorktree"
        @remove-worktree="onRemoveWorktree"
        @open-profiles="openProfileCenter('list')"
      />

      <div class="cli-portal__main">
        <CliTabBar
          :groups="groups"
          :active-tab-id="activeTab?.id"
          :show-group-headers="showGroupHeaders"
          :renaming-tab-id="renamingTabId"
          :rename-value="renameValue"
          :tab-positions="tabPositions"
          :tab-statuses="tabStatuses"
          :tab-pane-statuses="tabPaneStatuses"
          :tab-liveness="tabLivenessMap"
          :reopened-tabs="reopenedTabIds"
          :rollup="overviewRollup"
          :reorder-request="tabReorderRequest"
          :quick-switch-label="quickSwitchLabel"
          :sidebar-open="workbench.sidebarOpen.value"
          :is-mobile="breakpoint.isMobile.value"
          :active-space-kind="workbench.activeSpaceKind.value"
          :center-preview-file="workbench.centerPreviewFile.value"
          :center-preview-active="centerPreviewActive"
          :view-mode="activeTabViewMode"
          :drawer-open="activeDrawerOpen"
          @toggle-sidebar="workbench.toggleSidebar()"
          @select-preview="onSelectCenterPreviewTab"
          @close-preview="onCloseCenterPreview"
          @update:view-mode="onChangeViewMode"
          @open-dispatch="openDispatchModal"
          @toggle-right-drawer="toggleActiveDrawer"
          @quick-switch="openQuickSwitcher"
          @switch="onTabBarSwitch"
          @close="closeTab"
          @add="quickCreateTab"
          @add-remote="openRemoteDialog"
          @rename-start="startRenameTab"
          @rename-input="renameValue = $event"
          @rename-commit="commitRename"
          @rename-cancel="cancelRename"
          @toggle-group="toggleGroupCollapsed"
          @toggle-overview="toggleOverview"
          @context-menu="openTabMenu"
          @reorder="reorderTabs"
          @reorder-notice="showNotice"
        >
          <!-- One shared usage component; the adapter owns its permanent toolbar
               space and requests a compact summary when the tab list is crowded. -->
          <template #tab-trailing="{ compact }">
            <UsageChip :compact="compact" />
          </template>
        </CliTabBar>

        <div v-if="saveError" class="cli-save-error" role="status" data-testid="cli-workbench-save-error">
          <span>标签顺序或设置未保存：{{ saveError }}</span>
          <button type="button" data-testid="cli-workbench-save-retry" @click="retrySave">重试保存</button>
        </div>

        <div
          v-if="workbench.activeSpaceKind.value === 'task' && reviewReceipts.length > 0"
          class="cli-portal__banner-wrap"
        >
          <CollectReviewBanner
            :reviewers="reviewReceipts"
            @collect="onCollectReviewReceipts"
            @retry="onRetryReviewReceipt"
          />
        </div>

        <div class="cli-portal__canvas">
          <div class="cli-portal__primary" :style="companionDrawerInsetStyle" data-testid="cli-portal-primary">
            <!-- CRITICAL v-show INVARIANT (REQ-TW-01):
                 <CliTerminalView> MUST remain mounted via v-show (NEVER v-if) when switching
                 to BotWorkspaceView, CenterFilePreview (italic Preview Tab), SessionTranscriptView
                 (💬 Chat), or DetachedTerminalCard so live xterm instances and WebSockets never disconnect. -->
            <CliTerminalView
              ref="terminalView"
              v-show="canvas.showTerminalView"
              :loading="loading"
              :error="error"
              :active-tab-id="activeTab?.id"
              :tabs-with-session="surfaceTabs"
              :dw-rollup="overviewRollup"
              :dw-overview-open="overviewOpen"
              :split-allowed="activeTabSplitAllowed"
              :session-hud="sessionHud"
              :prefer-center-preview="false"
              @register-surface="registerSurface"
              @agent-state="onTabAgentState"
              @agent-notifications="onTabAgentNotifications"
              @session-exit="onTabSessionExit"
              @connection-change="onTabConnectionChange"
              @visible-sessions="onVisibleSessions"
              @dw-toggle-overview="toggleOverview"
              @split-ratio="setSplitRatio"
              @split-orientation="setSplitOrientation"
              @toggle-zoom="toggleZoom"
              @close-split="closeSplitPane"
              @open-center-preview="onOpenCenterPreview"
              @split-file="onSplitFile"
            />

            <!-- 服务重启后原来那张「假装恢复成功」的空终端，换成如实说明 + 两个明确选择。 -->
            <DetachedTerminalCard
              v-if="canvas.showDetachedCard && detachedCard"
              :key="detachedCard.tabId"
              :liveness="detachedCard.liveness"
              :name="detachedCard.name"
              :cwd="detachedCard.cwd"
              :remote="detachedCard.remote"
              :machine-label="detachedCard.machineLabel"
              :action="detachedAction"
              @close="closeTab(detachedCard.tabId)"
            />

            <!-- 💬 Chat structured transcript companion view -->
            <SessionTranscriptView
              v-if="canvas.showTranscriptView && activeTab?.sessionId"
              :session-id="activeTab.sessionId"
              :tab-title="activeTab.name"
              :cwd="activeTab.cwd"
              :profile-label="activeTabProfileLabel"
              :http-base="activeSurfaceTab?.httpBase"
              :auth-token="activeSurfaceTab?.authToken"
              :is-remote="activeSurfaceTab?.isRemote"
              :active="canvas.showTranscriptView"
              @switch-to-cli="onChangeViewMode('terminal')"
              @open-file="({ path, line }) => onOpenCenterPreview({ path, line, sessionId: activeTab?.sessionId || '', cwd: activeTab?.cwd })"
              @open-center-preview="(path) => onOpenCenterPreview({ path, sessionId: activeTab?.sessionId || '', cwd: activeTab?.cwd })"
            />

            <!-- Single reused italic Preview Tab (full-width/full-height center reader) -->
            <CenterFilePreview
              v-if="canvas.showCenterPreview && workbench.centerPreviewFile.value"
              :path="workbench.centerPreviewFile.value.path"
              :line="workbench.centerPreviewFile.value.line"
              :session-id="workbench.centerPreviewFile.value.sessionId || activeTab?.sessionId || ''"
              :cwd="workbench.centerPreviewFile.value.cwd || activeTab?.cwd || ''"
              :mode="breakpoint.isMobile.value ? 'mobile-full' : 'preview-tab'"
              :target-sessions="previewTargetSessions"
              :default-target-session-id="activeTab?.sessionId"
              @close="onCloseCenterPreview"
              @split-file="(path) => onSplitFile({ path, sessionId: workbench.centerPreviewFile.value?.sessionId || activeTab?.sessionId || '', cwd: workbench.centerPreviewFile.value?.cwd || activeTab?.cwd })"
              @cite-file="onCitePreviewFile"
              @draft-annotation="onDraftPreviewAnnotation"
              @send-annotation="onSendPreviewAnnotation"
              @navigate="(absPath) => onOpenCenterPreview({ path: absPath, sessionId: workbench.centerPreviewFile.value?.sessionId || activeTab?.sessionId || '', cwd: workbench.centerPreviewFile.value?.cwd || activeTab?.cwd })"
            />

            <!-- First-class Resident Bot Workspace -->
            <BotWorkspaceView
              v-if="canvas.showBotWorkspace"
              :bot="workbench.activeBot.value"
              :profiles="workbench.profiles.value"
              :busy="botBusy"
              :spawning-item-id="workbench.spawningItemId.value"
              :error-message="botError"
              :is-mobile="breakpoint.isMobile.value"
              @send-chat="onBotSendChat"
              @save-docs="onBotSaveDocs"
              @run-check="onBotRunCheck"
              @spawn-worktree="onBotSpawnWorktree"
              @open-worktree="onBotOpenWorktree"
              @open-file="onBotOpenFile"
              @jump-session="onBotJumpSession"
              @dismiss-item="onBotDismissItem"
            />
          </div>

          <!-- PC side-by-side split-file comparison pane (auto-closes right drawer to keep ≤3 columns) -->
          <div
            v-if="canvas.showSplitPreview && splitPreviewFile"
            class="cli-portal__split-file"
            data-testid="cli-split-file-pane"
          >
            <CenterFilePreview
              :path="splitPreviewFile.path"
              :line="splitPreviewFile.line"
              :session-id="splitPreviewFile.sessionId || activeTab?.sessionId || ''"
              :cwd="splitPreviewFile.cwd || activeTab?.cwd || ''"
              mode="split-pane"
              :target-sessions="previewTargetSessions"
              :default-target-session-id="activeTab?.sessionId"
              @close="splitPreviewFile = null"
              @promote-full="(path) => onOpenCenterPreview({ path, sessionId: splitPreviewFile?.sessionId || activeTab?.sessionId || '', cwd: splitPreviewFile?.cwd || activeTab?.cwd })"
              @cite-file="onCitePreviewFile"
              @draft-annotation="onDraftPreviewAnnotation"
              @send-annotation="onSendPreviewAnnotation"
              @navigate="(absPath) => { if (splitPreviewFile) splitPreviewFile = { ...splitPreviewFile, path: absPath } }"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- Bottom 24px Desktop Telemetry Bar (hidden on mobile) -->
    <WorkbenchTelemetryBar
      v-if="!breakpoint.isMobile.value"
      :profiles="workbench.profiles.value"
      :default-profile-id="workbench.defaultProfileId.value"
      :bots="workbench.bots.value"
      :worktrees="telemetryWorktrees"
      :pane-count="totalPaneCount"
      :host-label="activeHostLabel"
      :latency-ms="activeRtt > 0 ? activeRtt : null"
      @open-profiles="onOpenProfilesFromTelemetry"
      @open-bots="onOpenBotsFromTelemetry"
      @open-worktrees="onOpenWorktreesFromTelemetry"
    />

    <!-- leader 等待提示 —— 和 pro 用同一个共享组件，措辞从键位表派生（见 leaderHintText）。 -->
    <LeaderHintBar :pending="leaderPending" :label="leaderLabel" />

    <!-- Tab right-click menu. Same action table as the keyboard shortcuts, plus the two things a
         shortcut can't express (关闭其他 / 复制目录路径). -->
    <CliTabContextMenu
      :open="tabMenu.open.value"
      :items="tabMenu.items.value"
      :position="tabMenu.position.value"
      :title="tabMenu.target.value?.name"
      @close="tabMenu.close"
    />

    <!-- Remote-terminal picker (mesh): add/select a peer, then open a tab connected to it. -->
    <RemoteTermDialog v-model:open="remoteDialogOpen" :on-connect="createRemoteTab" />
    <TabQuickSwitcher :view="switcherView" :units="overviewUnits" :pane-total="totalPaneCount" :is-mobile="breakpoint.isMobile.value" :open="quickSwitcher.open.value" :query="quickSwitcher.query.value" :items="quickSwitcher.results.value" :selected-id="quickSwitcher.selectedId.value" :active-id="activeTab?.id" :binding="quickSwitchBinding" :binding-label="quickSwitchLabel" :message="quickSwitcher.message.value" :total="quickSwitcher.total.value" :status-kinds="quickSwitcher.statusKinds.value" :filter="quickSwitcher.filter.value" :filters="quickSwitcher.filters.value" :filter-stale="quickSwitcher.filterStale.value" @filter="quickSwitcher.setFilter" @refresh-filter="quickSwitcher.refreshFilter" @search="quickSwitcher.search" @move="quickSwitcher.move" @select="selectQuickTab" @close="quickSwitcher.close" @view="switcherView = $event" @pane="selectQuickPane" />

    <!-- Task Workbench v11 Modals -->
    <NewTaskModal
      :open="workbench.newTaskModalOpen.value"
      :profiles="workbench.profiles.value"
      :default-profile-id="workbench.defaultProfileId.value"
      :busy="newTaskBusy"
      :error-message="newTaskError"
      @close="workbench.closeNewTaskModal()"
      @submit="onSubmitNewTask"
    />

    <ProfileCenterModal
      :open="workbench.profileModalOpen.value"
      :profiles="workbench.profiles.value"
      :default-profile-id="workbench.defaultProfileId.value"
      :loading="workbench.profilesLoading.value"
      :initial-tab="workbench.profileModalInitialTab.value"
      :test-result="profileTestResult"
      :scan-message="profileScanMessage"
      :error-message="profileError"
      @close="workbench.closeProfileModal()"
      @create-profile="onCreateProfile"
      @set-default="onSetDefaultProfile"
      @delete-profile="onDeleteProfile"
      @scan-local="onScanLocalProfiles"
      @test-gateway="onTestProfileGateway"
      @spawn-with-profile="onSpawnWithProfile"
    />

    <DispatchReviewModal
      :open="dispatchModalOpen"
      :profiles="workbench.profiles.value"
      :default-profile-id="workbench.defaultProfileId.value"
      :busy="dispatchBusy"
      :error-message="dispatchError"
      @close="dispatchModalOpen = false"
      @dispatch="onDispatchReview"
    />

    <!-- 一次性轻提示。用行内元素而不是 window.alert：原生弹窗在 headless 下阻塞 JS 线程，
         那条路就再也验收不了（见 useCliState 的 showNotice）。 -->
    <div v-if="notice" class="cli-notice" data-testid="cli-notice">{{ notice }}</div>
  </div>
</template>

<script setup lang="ts">
import { usePortalRuntime } from '@ce/composables/layout/usePortalRuntime'
import { cliScenarios, cliBreakpointOverrides } from './cliScenarios'
import { cliLayoutPolicy } from './cliLayoutPolicy'
import { useCliState } from './useCliState'
import { resolveDeepLinkTabId, syncTabQuery, resolveDeepLinkPaneIndex, syncPaneQuery } from './tabDeepLink'
import { resolveCompanionDrawerInset, resolveWorkbenchCanvas } from './workbenchCanvasState'
import { computed, onMounted, ref, watch, nextTick } from 'vue'
import TabQuickSwitcher from '@terminal/components/cli/TabQuickSwitcher.vue'
import SessionTranscriptView from '@terminal/components/cli/SessionTranscriptView.vue'
import { useRoute, useRouter } from 'vue-router'
import { CliTabBar, CliTerminalView } from './adapters'
import RemoteTermDialog from '@terminal/components/terminal-session/RemoteTermDialog.vue'
import UsageChip from '@terminal/components/report/UsageChip.vue'
import ShortcutsGuideBanner from '@terminal/components/terminal-session/ShortcutsGuideBanner.vue'
import { overviewPaneTarget } from '@terminal/composables/cli/overviewSelection'
import LeaderHintBar from '@terminal/components/terminal-session/LeaderHintBar.vue'
import CliTabContextMenu from '@terminal/components/cli/CliTabContextMenu.vue'
import DetachedTerminalCard from '@terminal/components/cli/DetachedTerminalCard.vue'
import { useSessionHud } from '@terminal/composables/cli/useSessionHud'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import {
  useTaskWorkbench,
  type CenterPreviewFile,
  type ProfileModalTab,
  type TabViewMode,
} from '@terminal/composables/cli/useTaskWorkbench'
import {
  appendToSessionDraft,
} from '@terminal/composables/cli/composeLenses'
import TaskSidebar, { type TaskStatusKind } from '@terminal/components/workbench/TaskSidebar.vue'
import BotWorkspaceView from '@terminal/components/workbench/BotWorkspaceView.vue'
import WorkbenchTelemetryBar from '@terminal/components/workbench/WorkbenchTelemetryBar.vue'
import NewTaskModal, { type NewTaskSubmitPayload } from '@terminal/components/workbench/NewTaskModal.vue'
import ProfileCenterModal from '@terminal/components/workbench/ProfileCenterModal.vue'
import DispatchReviewModal, {
  type DispatchReviewMode,
  type DispatchReviewPayload,
} from '@terminal/components/workbench/DispatchReviewModal.vue'
import CollectReviewBanner, { type ReviewReceipt } from '@terminal/components/workbench/CollectReviewBanner.vue'
import CenterFilePreview, { type CenterPreviewTargetSession } from '@terminal/components/files/CenterFilePreview.vue'
import { WorktreeApiError, type WorktreeInfo } from '@terminal/api/worktrees'
import { BotApiError, type SimulateBotAlertPayload } from '@terminal/api/bots'
import type { CreateProfileRequest, ProfileItem, TestProfileResponse } from '@terminal/api/profiles'

const runtime = usePortalRuntime({
  portalId: 'cli',
  scenarios: cliScenarios,
  breakpointOverrides: cliBreakpointOverrides,
  layoutPolicy: cliLayoutPolicy,
})

const {
  breakpoint,
  loading, error, saveError, retrySave, reorderTabs, tabReorderRequest, showNotice, groups, activeTab, showGroupHeaders,
  toggleGroupCollapsed,
  registerSurface,
  surfaceTabs,
  onTabAgentState, onTabAgentNotifications, onTabSessionExit, onTabConnectionChange,
  switchTab, closeTab,
  renamingTabId, renameValue, startRenameTab, commitRename, cancelRename,
  quickCreateTab, createTabSilent,
  remoteDialogOpen, openRemoteDialog, createRemoteTab,
  tabPositions, tabStatuses, tabPaneStatuses, onVisibleSessions, tabLivenessMap, reopenedTabIds, detachedCard, detachedAction,
  overviewOpen, toggleOverview, overviewRollup, overviewUnits, switcherView,
  openShortcutsSettings,
  tabMenu, openTabMenu,
  notice,
  quickSwitcher, openQuickSwitcher, quickSwitchBinding, quickSwitchLabel,
  leaderPending, leaderLabel,
  allTabs,
  activeRtt,
  activeTmuxWindowIndex, selectActiveTabWindow,
  setSplitRatio, setSplitOrientation, toggleZoom, closeSplitPane, activeTabSplitAllowed,
  activeDrawerOpen, activeDrawerSqueezePx, activeDrawerDock, toggleActiveDrawer, setActiveDrawerOpen,
  activeAgentState,
} = useCliState(runtime)

const { cliFetch } = useCliAuth()
const workbench = useTaskWorkbench()

const terminalView = ref<InstanceType<typeof CliTerminalView>>()

// ── Task Workbench v11 UI state ───────────────────────────────────────────────
const centerPreviewActive = ref(false)
const splitPreviewFile = ref<CenterPreviewFile | null>(null)

watch(
  () => workbench.centerPreviewFile.value,
  (next) => {
    centerPreviewActive.value = Boolean(next)
  },
)

const activeTabViewMode = computed<TabViewMode>(() => {
  const id = activeTab.value?.id
  return id ? workbench.getTabViewMode(id) : 'terminal'
})

const canvas = computed(() =>
  resolveWorkbenchCanvas({
    activeSpaceKind: workbench.activeSpaceKind.value,
    hasDetachedCard: Boolean(detachedCard.value),
    hasCenterPreview: Boolean(workbench.centerPreviewFile.value),
    centerPreviewActive: centerPreviewActive.value,
    hasSplitPreview: Boolean(splitPreviewFile.value),
    isMobile: breakpoint.isMobile.value,
    activeTabViewMode: activeTabViewMode.value,
    hasActiveSession: Boolean(activeTab.value?.sessionId),
  }),
)

// R2-01: When the Teleported docked ('split') ResourceDrawer is open while CenterFilePreview or
// SessionTranscriptView is shown (CliTerminalView is hidden via v-show), apply the active surface's
// live drawerSqueezePx inset so toolbar actions and design-pick-popover never sit behind the drawer.
const companionDrawerInsetStyle = computed<Record<string, string>>(() =>
  resolveCompanionDrawerInset(canvas.value, activeDrawerSqueezePx.value, activeDrawerDock.value),
)

const totalPaneCount = computed(() =>
  allTabs.value.reduce((sum, tab) => sum + (tab.split ? 2 : 1), 0),
)

const activeSurfaceTab = computed(() => {
  const tab = activeTab.value
  if (!tab) return undefined
  return surfaceTabs.value.find((s) => s.id === tab.id)
})

const activeHostLabel = computed(() => activeSurfaceTab.value?.machineLabel || 'local')

const activeTabProfileLabel = computed(() => {
  const pid = activeTab.value?.profileId
  if (!pid) return ''
  const prof = workbench.profiles.value.find((p) => p.id === pid)
  return prof ? `${prof.name}${prof.model ? ` · ${prof.model}` : ''}` : ''
})

const sidebarTabStatuses = computed<Record<string, TaskStatusKind>>(() => {
  const out: Record<string, TaskStatusKind> = {}
  for (const [id, st] of tabStatuses.value) {
    if (st === 'waiting' || st === 'running') out[id] = st
    else if (st === 'done-unseen') out[id] = 'done'
    else out[id] = 'idle'
  }
  return out
})

const sidebarTabTools = computed<Record<string, string>>(() => {
  const byKey = new Map<string, string>()
  for (const u of overviewUnits.value) {
    const tool = (u.tool || '').trim()
    if (tool && tool.toLowerCase() !== 'none') {
      byKey.set(u.key, tool)
    }
  }
  const out: Record<string, string> = {}
  for (const tab of allTabs.value) {
    const liveTool =
      (tab.sessionId ? byKey.get(tab.sessionId) : undefined) ||
      (tab.split?.secondSessionId ? byKey.get(tab.split.secondSessionId) : undefined) ||
      (tab.id === activeTab.value?.id ? activeAgentState.value?.tool?.trim() : undefined)
    if (liveTool && liveTool.toLowerCase() !== 'none') {
      out[tab.id] = liveTool
    }
  }
  return out
})

const previewTargetSessions = computed<CenterPreviewTargetSession[]>(() =>
  surfaceTabs.value
    .filter((s) => Boolean(s.sessionId) && (!s.isRemote || Boolean(s.httpBase)))
    .map((s, idx) => ({
      id: s.id,
      sessionId: s.sessionId!,
      title: s.isRemote && s.machineLabel ? `${s.name} (${s.machineLabel})` : s.name,
      isPrimary: s.id === activeTab.value?.id || idx === 0,
      httpBase: s.httpBase,
      authToken: s.authToken,
      isRemote: s.isRemote,
    })),
)

const telemetryWorktrees = computed<WorktreeInfo[]>(() =>
  workbench.repoGroups.value.length > 0
    ? workbench.repoGroups.value.flatMap((g) => g.worktrees)
    : workbench.worktrees.value,
)

const PROFILES_AUTOSCAN_KEY = 'dw_profiles_autoscanned_v1'

// Initial workbench hydration (worktrees, profiles, resident bots)
onMounted(() => {
  void workbench
    .loadAll({
      session: activeTab.value?.sessionId,
      cwd: activeTab.value?.cwd,
    })
    .then(() => {
      try {
        if (window.localStorage?.getItem(PROFILES_AUTOSCAN_KEY) === '1') return
        if (
          workbench.profiles.value.length === 1 &&
          workbench.profiles.value[0]?.id === 'prof-default'
        ) {
          window.localStorage?.setItem(PROFILES_AUTOSCAN_KEY, '1')
          void workbench.scanProfiles().catch(() => {})
        }
      } catch {
        // Ignore storage failures
      }
    })
})

watch(
  () => [activeTab.value?.sessionId, activeTab.value?.cwd] as const,
  ([sid, cwd], [prevSid, prevCwd]) => {
    if (!sid || (sid === prevSid && cwd === prevCwd)) return
    void workbench.loadWorktrees({ session: sid, cwd }).catch(() => {})
    void workbench.loadAllWorktrees(cwd ? { cwd } : undefined).catch(() => {})
  },
)

// ── Topbar & Preview handlers ─────────────────────────────────────────────────
function onTabBarSwitch(tabId: string): void {
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  switchTab(tabId)
}

function onSelectCenterPreviewTab(): void {
  if (!workbench.centerPreviewFile.value) return
  workbench.selectTaskSpace()
  centerPreviewActive.value = true
}

function onCloseCenterPreview(): void {
  workbench.closeCenterPreview()
  centerPreviewActive.value = false
}

function onChangeViewMode(mode: TabViewMode): void {
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  const id = activeTab.value?.id
  if (id) workbench.setTabViewMode(id, mode)
}

function onOpenCenterPreview(payload: { path: string; line?: number; sessionId?: string; cwd?: string }): void {
  if (!payload.path) return
  // Auto-collapse right drawer when it would occlude CenterFilePreview (overlay/floating/mobile) (REQ-TW-05).
  if (activeDrawerOpen.value && (activeDrawerSqueezePx.value === 0 || breakpoint.isMobile.value)) {
    setActiveDrawerOpen(false)
  }
  splitPreviewFile.value = null
  workbench.openCenterPreview(payload.path, {
    line: payload.line,
    sessionId: payload.sessionId || activeTab.value?.sessionId,
    cwd: payload.cwd || activeTab.value?.cwd,
  })
  centerPreviewActive.value = true
}

function onSplitFile(payload: { path: string; line?: number; sessionId?: string; cwd?: string }): void {
  if (!payload.path) return
  if (breakpoint.isMobile.value) {
    onOpenCenterPreview(payload)
    return
  }
  // Auto-close right drawer on PC to prevent 4-column squeeze (REQ-TW-05).
  setActiveDrawerOpen(false)
  workbench.closeCenterPreview()
  centerPreviewActive.value = false
  workbench.selectTaskSpace()
  splitPreviewFile.value = {
    path: payload.path,
    line: payload.line,
    sessionId: payload.sessionId || activeTab.value?.sessionId,
    cwd: payload.cwd || activeTab.value?.cwd,
  }
}

// F1: CenterFilePreview already persists the refChip / draftText once via appendToSessionDraft.
// Host only displays the lightweight notice so text is never appended twice.
function onCitePreviewFile(payload: { path: string; line: number; refChip: string; targetSessionId: string }): void {
  showNotice(`已引用 @${payload.refChip} 到输入框`)
}

function onDraftPreviewAnnotation(payload: { targetSessionId: string; draftText: string; refChip: string }): void {
  showNotice(payload.refChip ? `已填入输入框 (@${payload.refChip})` : '已填入输入框')
}

function onSendPreviewAnnotation(): void {
  showNotice('已发送批注到目标会话')
}

// ── Sidebar handlers (Bots + Worktrees) ───────────────────────────────────────
function onSelectBot(botId: string): void {
  setActiveDrawerOpen(false)
  workbench.selectBot(botId)
  if (breakpoint.isMobile.value) workbench.setSidebarOpen(false)
}

async function onSelectWorktree(wt: WorktreeInfo): Promise<void> {
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  if (breakpoint.isMobile.value) workbench.setSidebarOpen(false)
  const normWt = wt.path.replace(/\/+$/, '')
  const existing = allTabs.value.find((t) => {
    const cwd = (t.cwd || '').replace(/\/+$/, '')
    return cwd === normWt || cwd.startsWith(`${normWt}/`)
  })
  if (existing) {
    switchTab(existing.id)
    return
  }
  const created = await createTabSilent({
    name: wt.branch || wt.path.split('/').pop() || 'worktree',
    cwd: wt.path,
    profileId: workbench.defaultProfileId.value || undefined,
  })
  if (created) switchTab(created.id)
}

function onSelectSidebarTab(tabId: string): void {
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  switchTab(tabId)
  if (breakpoint.isMobile.value) workbench.setSidebarOpen(false)
}

async function onMergeWorktree(wt: WorktreeInfo): Promise<void> {
  try {
    const res = await workbench.mergeWorktree({ cwd: wt.path })
    showNotice(`已将 ${res.mergedBranch} 合入 ${res.targetBranch}`)
  } catch (err) {
    showNotice(err instanceof Error ? err.message : '合入工作树失败')
  }
}

async function onRemoveWorktree(wt: WorktreeInfo): Promise<void> {
  try {
    await workbench.removeWorktree({ path: wt.path })
    showNotice(`已移除工作树 ${wt.branch || wt.path}`)
  } catch (err) {
    if (err instanceof WorktreeApiError && err.code === 'dirty_worktree') {
      showNotice('工作树含未提交改动，已由安全护栏拦截删除')
      return
    }
    showNotice(err instanceof Error ? err.message : '移除工作树失败')
  }
}

// ── Bot Workspace handlers ────────────────────────────────────────────────────
const botBusy = ref(false)
const botError = ref<string | null>(null)

async function onBotSendChat(payload: { botId: string; threadId: string; message: string }): Promise<void> {
  botBusy.value = true
  botError.value = null
  try {
    await workbench.sendBotMessage(payload.botId, payload.message, payload.threadId)
  } catch (err) {
    botError.value = err instanceof Error ? err.message : '发送幕僚消息失败'
  } finally {
    botBusy.value = false
  }
}

async function onBotSaveDocs(payload: { botId: string; soulMd: string; memoryMd: string }): Promise<void> {
  botBusy.value = true
  botError.value = null
  try {
    await workbench.saveBotDocs(payload.botId, {
      soulMd: payload.soulMd,
      memoryMd: payload.memoryMd,
    })
    showNotice('已保存幕僚 SOUL.md / MEMORY.md')
  } catch (err) {
    botError.value = err instanceof Error ? err.message : '保存幕僚记忆失败'
  } finally {
    botBusy.value = false
  }
}

async function onBotRunCheck(payload: {
  botId: string
  triggerId?: string
  precheckCmd?: string
  patrol?: boolean
  simulateAlert?: SimulateBotAlertPayload
}): Promise<void> {
  botBusy.value = true
  botError.value = null
  try {
    const res = await workbench.triggerBotCheck(payload.botId, {
      triggerId: payload.triggerId,
      precheckCmd: payload.precheckCmd,
      patrol: payload.patrol,
      simulateAlert: payload.simulateAlert,
    })
    if (payload.patrol) {
      showNotice('已完成全机真实会话与仓库只读巡检')
    } else if (res.skippedZeroToken) {
      showNotice('零 Token 预检未发现异常，已跳过模型唤醒')
    }
  } catch (err) {
    if (err instanceof BotApiError && err.blocked) {
      botError.value = `只读护栏已拦截：${err.message}`
    } else {
      botError.value = err instanceof Error ? err.message : '执行只读巡检失败'
    }
  } finally {
    botBusy.value = false
  }
}

async function onBotSpawnWorktree(payload: { botId: string; itemId: string }): Promise<void> {
  botError.value = null
  try {
    const result = await workbench.spawnWorktreeFromBotItem(payload.botId, payload.itemId, {
      session: activeTab.value?.sessionId,
      cwd: activeTab.value?.cwd,
      createTabForWorktree: async ({ path, branch, bot }) => {
        const created = await createTabSilent({
          name: branch,
          cwd: path,
          profileId: bot.profileId || workbench.defaultProfileId.value || undefined,
        })
        return created?.id
      },
    })
    centerPreviewActive.value = false
    if (result.tabId && allTabs.value.some((t) => t.id === result.tabId)) {
      switchTab(result.tabId)
    } else {
      await onBotOpenWorktree({ path: result.worktreePath })
    }
    showNotice(result.alreadySpawned ? `已跳转分支目录 ${result.branch}` : `已新建分支目录 ${result.branch}`)
  } catch (err) {
    botError.value = err instanceof Error ? err.message : '新建分支目录失败'
  }
}

async function onBotOpenWorktree(payload: { path?: string; tabId?: string }): Promise<void> {
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  if (payload.tabId && allTabs.value.some((t) => t.id === payload.tabId)) {
    switchTab(payload.tabId)
    return
  }
  if (payload.path) {
    const norm = payload.path.replace(/\/+$/, '')
    const match = allTabs.value.find((t) => {
      const cwd = (t.cwd || '').replace(/\/+$/, '')
      return cwd === norm || cwd.startsWith(`${norm}/`)
    })
    if (match) {
      switchTab(match.id)
      return
    }
    const created = await createTabSilent({
      name: payload.path.split('/').pop() || 'worktree',
      cwd: payload.path,
      profileId: workbench.defaultProfileId.value || undefined,
    })
    if (created) switchTab(created.id)
  }
}

function onBotOpenFile(payload: { path: string; line?: number; cwd?: string }): void {
  onOpenCenterPreview({
    path: payload.path,
    line: payload.line,
    sessionId: activeTab.value?.sessionId || '',
    cwd: payload.cwd || workbench.activeBot.value?.repoCwd || activeTab.value?.cwd || '',
  })
}

function onBotJumpSession(payload: { sessionId: string; cwd?: string }): void {
  const normCwd = (payload.cwd || '').replace(/\/+$/, '')
  const target =
    (payload.sessionId ? allTabs.value.find((t) => t.sessionId === payload.sessionId) : undefined) ||
    (normCwd
      ? allTabs.value.find((t) => {
          const tc = (t.cwd || '').replace(/\/+$/, '')
          return tc === normCwd || tc.startsWith(`${normCwd}/`)
        })
      : undefined)
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  if (target) {
    switchTab(target.id)
  } else if (normCwd) {
    void onBotOpenWorktree({ path: payload.cwd })
  } else {
    showNotice('未找到对应的打开标签页')
  }
}

async function onBotDismissItem(payload: { botId: string; itemId: string }): Promise<void> {
  botBusy.value = true
  botError.value = null
  try {
    await workbench.dismissBotItem(payload.botId, payload.itemId)
    showNotice('已归档该分诊事项')
  } catch (err) {
    botError.value = err instanceof Error ? err.message : '归档分诊事项失败'
  } finally {
    botBusy.value = false
  }
}

// ── New Task Modal handlers ───────────────────────────────────────────────────
const newTaskBusy = ref(false)
const newTaskError = ref<string | null>(null)

function openNewTask(): void {
  newTaskError.value = null
  workbench.openNewTaskModal()
}

async function sendPromptToSession(sessionId: string, text: string): Promise<void> {
  if (!sessionId || !text.trim()) return
  try {
    await cliFetch(cliApi(`/sessions/${encodeURIComponent(sessionId)}/input`), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ data: `${text.trim()}\n` }),
    })
  } catch {
    // Best effort; fallback to compose draft
    appendToSessionDraft(sessionId, text)
  }
}

async function onSubmitNewTask(payload: NewTaskSubmitPayload): Promise<void> {
  newTaskBusy.value = true
  newTaskError.value = null
  try {
    const ids = payload.profileIds.length > 0 ? payload.profileIds : ['']
    let firstCreatedId: string | undefined
    for (let i = 0; i < ids.length; i++) {
      const pid = ids[i]
      const prof = workbench.profiles.value.find((p) => p.id === pid)
      let targetCwd = activeTab.value?.cwd || '~'
      let tabName = payload.branch

      if (payload.mode === 'arena') {
        const branchName = i === 0 ? payload.branch : `${payload.branch}-${i + 1}`
        const wt = await workbench.createCoWWorktree({
          session: activeTab.value?.sessionId,
          cwd: activeTab.value?.cwd,
          branch: branchName,
          baseRef: payload.baseRef,
        })
        targetCwd = wt.path
        tabName = wt.branch
      } else {
        const baseLabel = payload.prompt.slice(0, 16) || payload.branch
        tabName = prof ? `${baseLabel} · ${prof.name}` : baseLabel
      }

      const created = await createTabSilent({
        name: tabName,
        cwd: targetCwd,
        engine: prof?.engine || 'shell',
        profileId: prof?.id || undefined,
        activate: i === 0,
      })
      if (created && !firstCreatedId) firstCreatedId = created.id
      if (created?.sessionId && payload.prompt) {
        appendToSessionDraft(created.sessionId, payload.prompt)
      }
    }
    workbench.selectTaskSpace()
    centerPreviewActive.value = false
    if (firstCreatedId) switchTab(firstCreatedId)
    workbench.closeNewTaskModal()
    showNotice(payload.mode === 'arena' ? `已新建分支目录任务 ${payload.branch}` : '已在当前目录新开终端会话')
  } catch (err) {
    newTaskError.value = err instanceof Error ? err.message : '创建任务失败'
  } finally {
    newTaskBusy.value = false
  }
}

// ── Profile Center Modal & Telemetry Bar handlers ─────────────────────────────
const profileTestResult = ref<TestProfileResponse | null>(null)
const profileScanMessage = ref<string | null>(null)
const profileError = ref<string | null>(null)

function openProfileCenter(tab: ProfileModalTab = 'list'): void {
  profileTestResult.value = null
  profileScanMessage.value = null
  profileError.value = null
  workbench.openProfileModal(tab)
}

function onOpenProfilesFromTelemetry(payload?: { tab?: ProfileModalTab }): void {
  openProfileCenter(payload?.tab ?? 'list')
}

function onOpenBotsFromTelemetry(): void {
  const firstBot = workbench.bots.value[0]
  if (firstBot) {
    onSelectBot(workbench.activeBotId.value || firstBot.id)
  } else {
    workbench.setSidebarOpen(true)
  }
}

function onOpenWorktreesFromTelemetry(): void {
  workbench.setSidebarOpen(true)
  workbench.selectTaskSpace()
}

async function onCreateProfile(req: CreateProfileRequest): Promise<void> {
  profileError.value = null
  try {
    await workbench.addProfile(req)
    showNotice(`已保存配置 ${req.name}`)
  } catch (err) {
    profileError.value = err instanceof Error ? err.message : '创建配置失败'
  }
}

async function onSetDefaultProfile(id: string): Promise<void> {
  profileError.value = null
  try {
    await workbench.setDefaultProfile(id)
    showNotice('已切换默认配置')
  } catch (err) {
    profileError.value = err instanceof Error ? err.message : '设置默认配置失败'
  }
}

async function onDeleteProfile(id: string): Promise<void> {
  profileError.value = null
  try {
    await workbench.removeProfile(id)
    showNotice('已删除配置')
  } catch (err) {
    profileError.value = err instanceof Error ? err.message : '删除配置失败'
  }
}

async function onScanLocalProfiles(): Promise<void> {
  profileError.value = null
  profileScanMessage.value = null
  try {
    const res = await workbench.scanProfiles()
    profileScanMessage.value = `扫描完成：发现 ${res.profiles.length} 套配置（新增导入 ${res.imported} 套）`
  } catch (err) {
    profileError.value = err instanceof Error ? err.message : '扫描本机配置失败'
  }
}

async function onTestProfileGateway(req: { baseUrl: string; apiKey?: string }): Promise<void> {
  profileError.value = null
  profileTestResult.value = null
  try {
    profileTestResult.value = await workbench.testGateway(req)
  } catch (err) {
    profileError.value = err instanceof Error ? err.message : '网关连通性自检失败'
  }
}

async function onSpawnWithProfile(prof: ProfileItem): Promise<void> {
  const created = await createTabSilent({
    name: prof.name,
    cwd: activeTab.value?.cwd || '~',
    engine: prof.engine,
    profileId: prof.id,
  })
  workbench.closeProfileModal()
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  if (created) {
    switchTab(created.id)
    showNotice(`已使用配置「${prof.name}」新开终端`)
  }
}

// ── Dispatch Review Modal & Collect Banner handlers ───────────────────────────
const dispatchModalOpen = ref(false)
const dispatchBusy = ref(false)
const dispatchError = ref<string | null>(null)
const reviewReceipts = ref<ReviewReceipt[]>([])
const lastDispatchContext = ref('')
const lastDispatchMode = ref<DispatchReviewMode>('blind')

function openDispatchModal(): void {
  dispatchError.value = null
  dispatchModalOpen.value = true
}

async function onDispatchReview(payload: DispatchReviewPayload): Promise<void> {
  dispatchBusy.value = true
  dispatchError.value = null
  lastDispatchContext.value = payload.context
  lastDispatchMode.value = payload.mode
  try {
    const cwd = activeTab.value?.cwd || '~'
    const nextReceipts: ReviewReceipt[] = []
    for (const pid of payload.profileIds) {
      const prof = workbench.profiles.value.find((p) => p.id === pid)
      const label = prof?.name || pid
      const created = await createTabSilent({
        name: `审·${label}`,
        cwd,
        engine: prof?.engine || 'shell',
        profileId: pid,
        activate: false,
      })
      if (created?.sessionId) {
        await sendPromptToSession(created.sessionId, payload.context)
        nextReceipts.push({
          id: created.id,
          title: label,
          status: 'done',
          summary: `${payload.mode}: ${payload.context.slice(0, 120)}`,
        })
      } else {
        nextReceipts.push({
          id: `timeout-${pid}`,
          title: label,
          status: 'timeout',
          summary: '会话创建失败或超时',
        })
      }
    }
    reviewReceipts.value = [...reviewReceipts.value, ...nextReceipts]
    dispatchModalOpen.value = false
    showNotice(`已派发 ${nextReceipts.length} 路并发评审`)
  } catch (err) {
    dispatchError.value = err instanceof Error ? err.message : '派发评审失败'
  } finally {
    dispatchBusy.value = false
  }
}

function onCollectReviewReceipts(receipts: ReviewReceipt[]): void {
  const sid = activeTab.value?.sessionId
  const summaryLines = [
    `[会审汇总 · ${lastDispatchMode.value}]`,
    ...receipts.map((r) => `- ${r.title}: ${r.summary || '已完成评审'}`),
  ]
  appendToSessionDraft(sid, summaryLines.join('\n'))
  const collectedIds = new Set(receipts.map((r) => r.id))
  reviewReceipts.value = reviewReceipts.value.filter((r) => !collectedIds.has(r.id))
  showNotice(`已将 ${receipts.length} 条会审结论汇总填入输入框`)
}

async function onRetryReviewReceipt(reviewerId: string): Promise<void> {
  const target = allTabs.value.find((t) => t.id === reviewerId)
  if (target?.sessionId && lastDispatchContext.value) {
    await sendPromptToSession(target.sessionId, lastDispatchContext.value)
  }
  reviewReceipts.value = reviewReceipts.value.map((r) =>
    r.id === reviewerId ? { ...r, status: 'done' as const } : r,
  )
  showNotice('已重新派发该评审')
}

async function selectQuickPane(id: string, sessionId: string): Promise<void> {
  const target = overviewPaneTarget(sessionId, allTabs.value)
  if (!target || target.tabId !== id) {
    quickSwitcher.message.value = '这个窗格已变更，请重新选择'
    return
  }
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  if (!quickSwitcher.select(id)) return
  await nextTick()
  terminalView.value?.revealPane(target.tabId, target.pane)
}
async function selectQuickTab(id: string): Promise<void> {
  workbench.selectTaskSpace()
  centerPreviewActive.value = false
  if (!quickSwitcher.select(id)) return
  await nextTick()
  if (!breakpoint.isMobile.value) terminalView.value?.focusActiveTerminal()
}

// One visible-session snapshot only. The old top-bar number came from the UsageChip's selected
// fleet window and could silently combine several Codex sessions. This HUD follows the active tab
// and is passed to its surface status row; UsageChip remains the separate account/usage view.
const sessionHud = useSessionHud(() => {
  const tab = activeTab.value
  if (!tab?.sessionId || tab.remotePeerId) return null
  // The workbench tab's engine can remain `shell` while a Codex/Claude process starts inside it.
  // The live agent_state frame is the authority for the current runtime; using the stale tab
  // creation engine here is how a Codex session previously picked up Claude's model transcript.
  const tool = activeAgentState.value?.tool
  if (!tool) return null
  return { sessionId: tab.sessionId, cwd: tab.cwd, tool, remote: false }
})

// ── tab 深链：/portal/cli?t=<tabId>（机制与取舍见 tabDeepLink.ts 头注）──────────────────
// 读：首次 tabs 就绪时命中 ?t= 则切一次（无效/已关 → 静默落服务端兜底）；写：activeTab 变化即
// replace 进 URL（不 push——切 tab 不是导航），刷新/分享/手机书签因此回到同一 tab。
const route = useRoute()
const router = useRouter()
const deepLinkApplied = ref(false)
watch(
  [groups, () => route.query.t],
  () => {
    if (deepLinkApplied.value || !groups.value.length) return
    const target = resolveDeepLinkTabId(route.query.t, allTabs.value)
    deepLinkApplied.value = true
    if (target && target !== activeTab.value?.id) switchTab(target, { restoring: true })
  },
  { immediate: true },
)
watch(
  () => activeTab.value?.id,
  (id) => {
    const t = syncTabQuery(id, route.query.t)
    if (t !== undefined) void router.replace({ query: { ...route.query, t } })
  },
  { immediate: true },
)

// ── pane（tmux window）深链：/portal/cli?t=<tabId>&p=<windowIndex>，双向 ──────────────────
watch(
  [() => route.query.p, activeTab],
  () => {
    const target = resolveDeepLinkPaneIndex(route.query.p)
    if (target !== undefined && target !== activeTmuxWindowIndex.value) {
      selectActiveTabWindow(target)
    }
  },
  { immediate: true },
)
watch(
  activeTmuxWindowIndex,
  () => {
    const p = syncPaneQuery(activeTmuxWindowIndex.value, route.query.p)
    if (p === undefined) return
    const nextQuery = { ...route.query }
    if (p === null) delete nextQuery.p
    else nextQuery.p = p
    void router.replace({ query: nextQuery })
  },
  { immediate: true },
)
</script>

<style scoped>
.cli-save-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-shrink: 0;
  padding: 5px 10px;
  background: hsl(var(--card));
  border-bottom: 1px solid hsl(var(--border));
  color: hsl(var(--foreground));
  font-size: 12px;
}
.cli-save-error button {
  flex-shrink: 0;
  border: 1px solid hsl(var(--border));
  border-radius: 4px;
  padding: 3px 8px;
  background: hsl(var(--accent));
  color: inherit;
  cursor: pointer;
}
.cli-portal {
  display: flex;
  flex-direction: column;
  height: 100%;
  position: relative;
  background: var(--workbench-bg, #1e1e1e);
  color: var(--workbench-text, #e0e0e0);
  overflow: hidden;
}
.cli-portal__body {
  display: flex;
  flex: 1;
  min-height: 0;
  min-width: 0;
  position: relative;
  overflow: hidden;
}
.cli-portal__sidebar-scrim {
  position: absolute;
  inset: 0;
  z-index: 30;
  background: rgba(5, 7, 12, 0.56);
}
.cli-portal__main {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.cli-portal__banner-wrap {
  padding: 5px 10px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  background: #10131a;
}
.cli-portal__canvas {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  position: relative;
  overflow: hidden;
}
.cli-portal__primary {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
  box-sizing: border-box;
  overflow: hidden;
}
.cli-portal__split-file {
  flex: 0 0 46%;
  min-width: 320px;
  max-width: 65%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--line-strong, rgba(255, 255, 255, 0.14));
  overflow: hidden;
}
@media (prefers-color-scheme: light) {
  .cli-portal {
    --workbench-bg: #f5f5f5;
    --workbench-text: #222;
    --workbench-text-muted: #666;
    --workbench-tabbar-bg: #e8e8e8;
    --workbench-border: #d0d0d0;
    --workbench-tab-hover: #ddd;
    --workbench-tab-active-bg: #f5f5f5;
    --workbench-strip-bg: #ededed;
  }
}

/* 一次性轻提示：环境变量注入的结果 / 被拒的理由。悬浮在底部中间，6s 自动消失，不拦交互。 */
.cli-notice {
  position: absolute;
  left: 50%;
  bottom: 32px;
  transform: translateX(-50%);
  max-width: min(92vw, 560px);
  z-index: 60;
  padding: 8px 14px;
  border-radius: 8px;
  background: rgba(20, 20, 20, 0.94);
  color: #f2f2f2;
  border: 1px solid rgba(255, 255, 255, 0.14);
  font-size: 12px;
  line-height: 1.5;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.35);
  /* 提示是"读的"，不是"点的"：挡住下面终端的点击会让它从帮助变成障碍。 */
  pointer-events: none;
}
</style>

<style>
body:has(.cli-portal.is-non-terminal) .help-hint,
body:has([data-testid='new-task-modal']) .help-hint,
body:has([data-testid='dispatch-modal']) .help-hint,
body:has([data-testid='profile-modal']) .help-hint {
  display: none !important;
}
</style>
