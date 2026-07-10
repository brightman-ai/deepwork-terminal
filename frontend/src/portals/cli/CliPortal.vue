<template>
  <div
    class="cli-portal"
    :class="{ 'is-mobile': breakpoint.isMobile.value }"
    data-testid="cli-portal"
  >
    <CliTabBar
      :groups="groups"
      :active-tab-id="activeTab?.id"
      :show-group-headers="showGroupHeaders"
      :renaming-tab-id="renamingTabId"
      :rename-value="renameValue"
      :tab-runtimes="tabRuntimes"
      @switch="switchTab"
      @close="closeTab"
      @add="quickCreateTab"
      @add-remote="openRemoteDialog"
      @rename-start="startRenameTab"
      @rename-input="renameValue = $event"
      @rename-commit="commitRename"
      @rename-cancel="cancelRename"
      @toggle-group="toggleGroupCollapsed"
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
         CliPortal owns only the tab bar + connection/lifecycle. -->
    <CliTerminalView
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

    <!-- Remote-terminal picker (mesh): add/select a peer, then open a tab connected to it. -->
    <RemoteTermDialog v-model:open="remoteDialogOpen" :on-connect="createRemoteTab" />
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
  tabRuntimes, registerSurface,
  surfaceTabs,
  onTabAgentState, onTabAgentNotifications, onTabSessionExit, onTabConnectionChange,
  switchTab, closeTab,
  renamingTabId, renameValue, startRenameTab, commitRename, cancelRename,
  quickCreateTab,
  remoteDialogOpen, openRemoteDialog, createRemoteTab,
} = useCliState(runtime)
</script>

<style scoped>
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
