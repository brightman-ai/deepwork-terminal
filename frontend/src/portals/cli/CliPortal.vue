<template>
  <div
    class="cli-portal"
    :class="{ 'is-mobile': breakpoint.isMobile.value }"
    data-testid="cli-portal"
  >
    <!-- D6: first-run shortcuts guide, collapses into a small icon after acknowledgement. -->
    <ShortcutsGuideBanner v-show="!saveError" :on-open-settings="openShortcutsSettings" />

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
      @quick-switch="openQuickSwitcher"
      @switch="switchTab"
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

    <!-- The per-surface status row (tmux pane bar ↔ "终端 N <status>" strip) now lives
         INSIDE CliTerminalSurface (the SSOT), so the host no longer mounts or re-wires it.
         CliPortal owns only the tab bar + connection/lifecycle.

         互斥点就在这里：当前标签背后没有活着的进程时，终端区域渲染 DetachedTerminalCard 而不是
         终端。CliTerminalView 本身**不能**被 v-if 掉——它把每个标签的 surface 都常驻挂载（WS +
         xterm 绑的是长命进程，不能随可见性生灭），所以这里用 v-show 只藏画面、不拆连接；已结束的
         标签本来就没有 surface，藏的只是一块空白。 -->
    <CliTerminalView
      ref="terminalView"
      v-show="!detachedCard"
      :loading="loading"
      :error="error"
      :active-tab-id="activeTab?.id"
      :tabs-with-session="surfaceTabs"
      :dw-rollup="overviewRollup"
      :dw-overview-open="overviewOpen"
      :split-allowed="activeTabSplitAllowed"
      :session-hud="sessionHud"
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
    />

    <!-- 服务重启后原来那张「假装恢复成功」的空终端，换成如实说明 + 两个明确选择。 -->
    <DetachedTerminalCard
      v-if="detachedCard"
      :key="detachedCard.tabId"
      :liveness="detachedCard.liveness"
      :name="detachedCard.name"
      :cwd="detachedCard.cwd"
      :remote="detachedCard.remote"
      :machine-label="detachedCard.machineLabel"
      :action="detachedAction"
      @close="closeTab(detachedCard.tabId)"
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
    <TabQuickSwitcher :view="switcherView" :units="overviewUnits" :pane-total="allTabs.reduce((sum, tab) => sum + (tab.split ? 2 : 1), 0)" :is-mobile="breakpoint.isMobile.value" :open="quickSwitcher.open.value" :query="quickSwitcher.query.value" :items="quickSwitcher.results.value" :selected-id="quickSwitcher.selectedId.value" :active-id="activeTab?.id" :binding="quickSwitchBinding" :binding-label="quickSwitchLabel" :message="quickSwitcher.message.value" :total="quickSwitcher.total.value" :status-kinds="quickSwitcher.statusKinds.value" :filter="quickSwitcher.filter.value" :filters="quickSwitcher.filters.value" :filter-stale="quickSwitcher.filterStale.value" @filter="quickSwitcher.setFilter" @refresh-filter="quickSwitcher.refreshFilter" @search="quickSwitcher.search" @move="quickSwitcher.move" @select="selectQuickTab" @close="quickSwitcher.close" @view="switcherView = $event" @pane="selectQuickPane" />

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
import { ref, watch, nextTick } from 'vue'
import TabQuickSwitcher from '@terminal/components/cli/TabQuickSwitcher.vue'
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
  quickCreateTab,
  remoteDialogOpen, openRemoteDialog, createRemoteTab,
  tabPositions, tabStatuses, tabPaneStatuses, onVisibleSessions, tabLivenessMap, reopenedTabIds, detachedCard, detachedAction,
  overviewOpen, toggleOverview, overviewRollup, overviewUnits, switcherView,
  openShortcutsSettings,
  tabMenu, openTabMenu,
  notice,
  quickSwitcher, openQuickSwitcher, quickSwitchBinding, quickSwitchLabel,
  // leader 等待态 + 它的键名。键名走 bindingLabel（绑定措辞的 SSOT），所以提示条和设置页
  // 显示的是同一个结果，不会写成两种样子。
  leaderPending, leaderLabel,
  allTabs,
  activeTmuxWindowIndex, selectActiveTabWindow,
  setSplitRatio, setSplitOrientation, toggleZoom, closeSplitPane, activeTabSplitAllowed,
  activeAgentState,
} = useCliState(runtime)

const terminalView = ref<InstanceType<typeof CliTerminalView>>()
async function selectQuickPane(id: string, sessionId: string): Promise<void> {
  const target = overviewPaneTarget(sessionId, allTabs.value)
  if (!target || target.tabId !== id) {
    quickSwitcher.message.value = '这个窗格已变更，请重新选择'
    return
  }
  if (!quickSwitcher.select(id)) return
  await nextTick()
  terminalView.value?.revealPane(target.tabId, target.pane)
}
async function selectQuickTab(id: string): Promise<void> {
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
// 2026-10-11 推翻 tabDeepLink.ts 头注里 2026-10-07 的"v1 不做"决定（机制与取舍见该文件头注 +
// docs/.tg/work/20261011-230813-cli-tabs-term/request.md）。与 tab 的"只读一次"不同，这里是真
// 双向：读（URL→切 window）持续监听（不是 immediate 应用一次就收手），因为 Human 明确要的是前进/
// 后退、手改地址栏也能把视图拽到那个 pane；写（切 window→URL）复用同一条 diff-suppression
// 防自激环路（syncPaneQuery 返回 undefined 即不动）。
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
  background: var(--workbench-bg, #1e1e1e);
  color: var(--workbench-text, #e0e0e0);
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
  bottom: 18px;
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
