<template>
  <div
    class="cli-portal"
    :class="{ 'is-mobile': breakpoint.isMobile.value }"
    data-testid="cli-portal"
  >
    <!-- D6: first-run shortcuts guide, collapses into a small icon after acknowledgement. -->
    <ShortcutsGuideBanner :on-open-settings="openShortcutsSettings" />

    <CliTabBar
      :groups="groups"
      :active-tab-id="activeTab?.id"
      :show-group-headers="showGroupHeaders"
      :renaming-tab-id="renamingTabId"
      :rename-value="renameValue"
      :tab-positions="tabPositions"
      :tab-statuses="tabStatuses"
      :tab-liveness="tabLivenessMap"
      :reopened-tabs="reopenedTabIds"
      :rollup="overviewRollup"
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
    >
      <!-- Usage chip trails the tabs (contextual to the terminal session) — the SAME position
           pro-embed uses (CliV2 mounts it right after its TopTabBar), so the chip sits identically
           in both shells. It is the SAME shared @terminal/components/report/UsageChip.vue (one SSOT).
           Connection health + agent badge live in the per-surface status row inside CliTerminalSurface
           (not here) to avoid the old DUPLICATE heartbeat; the chip is a different concern (订阅额度 %). -->
      <template #tab-trailing>
        <UsageChip />
      </template>
    </CliTabBar>

    <!-- The per-surface status row (tmux pane bar ↔ "终端 N <status>" strip) now lives
         INSIDE CliTerminalSurface (the SSOT), so the host no longer mounts or re-wires it.
         CliPortal owns only the tab bar + connection/lifecycle.

         互斥点就在这里：当前标签背后没有活着的进程时，终端区域渲染 DetachedTerminalCard 而不是
         终端。CliTerminalView 本身**不能**被 v-if 掉——它把每个标签的 surface 都常驻挂载（WS +
         xterm 绑的是长命进程，不能随可见性生灭），所以这里用 v-show 只藏画面、不拆连接；已结束的
         标签本来就没有 surface，藏的只是一块空白。 -->
    <CliTerminalView
      v-show="!detachedCard"
      :loading="loading"
      :error="error"
      :active-tab-id="activeTab?.id"
      :tabs-with-session="surfaceTabs"
      @register-surface="registerSurface"
      @agent-state="onTabAgentState"
      @agent-notifications="onTabAgentNotifications"
      @session-exit="onTabSessionExit"
      @connection-change="onTabConnectionChange"
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

    <!-- D7: the SAME Agent Overview tmux users get — card grid with each terminal's live output.
         One component, two data sources (tmux topology / sessions_overview frame). -->
    <!-- 关闭只有一处实现（closeOverview）：遮罩、Esc、点中卡片走的是同一个动作，模板里不再各留
         一份 `overviewOpen = false`。点中卡片时的「切过去 + 关掉」由 selectOverviewIndex 一起做。 -->
    <div v-if="overviewOpen" class="cli-overview-scrim" data-testid="cli-overview" @click.self="closeOverview">
      <div class="cli-overview-panel">
        <AgentOverview
          :groups="overviewGroups"
          :rollup="overviewRollup"
          :is-mobile="breakpoint.isMobile.value"
          @select="selectOverviewIndex"
          @close="closeOverview"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { usePortalRuntime } from '@ce/composables/layout/usePortalRuntime'
import { cliScenarios, cliBreakpointOverrides } from './cliScenarios'
import { cliLayoutPolicy } from './cliLayoutPolicy'
import { useCliState } from './useCliState'
import { CliTabBar, CliTerminalView } from './adapters'
import RemoteTermDialog from '@terminal/components/terminal-session/RemoteTermDialog.vue'
import UsageChip from '@terminal/components/report/UsageChip.vue'
import ShortcutsGuideBanner from '@terminal/components/terminal-session/ShortcutsGuideBanner.vue'
import AgentOverview from '@terminal/components/terminal-session/AgentOverview.vue'
import CliTabContextMenu from '@terminal/components/cli/CliTabContextMenu.vue'
import DetachedTerminalCard from '@terminal/components/cli/DetachedTerminalCard.vue'

const runtime = usePortalRuntime({
  portalId: 'cli',
  scenarios: cliScenarios,
  breakpointOverrides: cliBreakpointOverrides,
  layoutPolicy: cliLayoutPolicy,
})

const {
  breakpoint,
  loading, error, groups, activeTab, showGroupHeaders,
  toggleGroupCollapsed,
  registerSurface,
  surfaceTabs,
  onTabAgentState, onTabAgentNotifications, onTabSessionExit, onTabConnectionChange,
  switchTab, closeTab,
  renamingTabId, renameValue, startRenameTab, commitRename, cancelRename,
  quickCreateTab,
  remoteDialogOpen, openRemoteDialog, createRemoteTab,
  tabPositions, tabStatuses, tabLivenessMap, reopenedTabIds, detachedCard, detachedAction,
  overviewOpen, toggleOverview, closeOverview, overviewGroups, overviewRollup,
  selectOverviewIndex, openShortcutsSettings,
  tabMenu, openTabMenu,
} = useCliState(runtime)
</script>

<style scoped>
/* Overview overlay — the card grid needs real estate (it shows live output), so it takes the
   surface rather than floating as a small popover. Scrim click closes.

   `align-items: stretch` + no max-height is load-bearing, not cosmetic. AgentOverview's `.is-fill`
   rule is `min-height: 100%`, which needs a parent with a DEFINITE height to resolve against. The
   panel used to be `align-items: flex-start` + `max-height: 100%`, i.e. height:auto — so
   `min-height:100%` had nothing to measure, the fill never engaged, and the cards fell back to
   their 300px floor. That is the whole of "非 tmux 的总览比 tmux 矮": the tmux one mounts in
   `.terminal-overview-overlay { position:absolute; inset:0 }`, which HAS a definite height, so the
   identical component stretched there and not here. Same component, different container, and the
   container was the bug. */
.cli-overview-scrim {
  position: fixed; inset: 0; z-index: 2600; background: rgba(0, 0, 0, 0.55);
  display: flex; align-items: stretch; justify-content: center; padding: 44px 16px 16px;
}
.cli-overview-panel {
  width: 100%; max-width: 1200px; overflow-y: auto;
  background: hsl(var(--card)); border: 1px solid hsl(var(--border));
  border-radius: 12px; box-shadow: 0 16px 48px rgba(0, 0, 0, 0.45);
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
</style>
