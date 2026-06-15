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
      @rename-start="startRenameTab"
      @rename-input="renameValue = $event"
      @rename-commit="commitRename"
      @rename-cancel="cancelRename"
      @toggle-group="toggleGroupCollapsed"
    >
      <template #status>
        <div class="cli-portal__tab-status" :class="{ 'is-mobile': breakpoint.isMobile.value }">
          <ConnectionStatus
            v-if="activeTab?.sessionId"
            :status="activeWsStatus"
            :rtt="activeRtt"
            :upload-bps="activeNetStats?.uploadBps ?? 0"
            :download-bps="activeNetStats?.downloadBps ?? 0"
            data-testid="cli-portal-connection-status"
          />
          <AgentStatusBadge
            v-if="activeAgentState || activeAgentNotifications.length > 0"
            :state="activeAgentState"
            :notifications="activeAgentNotifications"
            data-testid="cli-portal-agent-status"
          />
          <!-- Settings now lives in the portal NAV sidebar (@ce NavigationSidebar),
               a single SSOT entry reachable via the draggable left-edge nav trigger —
               no top-right gear here (it was easy to miss / could be occluded). -->
        </div>
      </template>
    </CliTabBar>

    <!-- The per-surface status row (tmux pane bar ↔ "终端 N <status>" strip) now lives
         INSIDE CliTerminalSurface (the SSOT), so the host no longer mounts or re-wires it.
         CliPortal owns only the tab bar + connection/lifecycle. -->
    <CliTerminalView
      :loading="loading"
      :error="error"
      :active-tab-id="activeTab?.id"
      :tabs-with-session="allTabsWithSession"
      @register-surface="registerSurface"
      @agent-state="onTabAgentState"
      @agent-notifications="onTabAgentNotifications"
      @session-exit="onTabSessionExit"
      @connection-change="onTabConnectionChange"
    />
  </div>
</template>

<script setup lang="ts">
import { usePortalRuntime } from '@ce/composables/layout/usePortalRuntime'
import { cliScenarios, cliBreakpointOverrides } from './cliScenarios'
import { cliLayoutPolicy } from './cliLayoutPolicy'
import { useCliState } from './useCliState'
import ConnectionStatus from '@terminal/components/terminal-session/ConnectionStatus.vue'
import AgentStatusBadge from '@terminal/components/terminal-session/AgentStatusBadge.vue'
import { CliTabBar, CliTerminalView } from './adapters'

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
  activeWsStatus, activeAgentState, activeAgentNotifications, activeRtt, activeNetStats,
  allTabsWithSession,
  onTabAgentState, onTabAgentNotifications, onTabSessionExit, onTabConnectionChange,
  switchTab, closeTab,
  renamingTabId, renameValue, startRenameTab, commitRename, cancelRename,
  quickCreateTab,
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
.cli-portal__tab-status {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 8px;
  flex-shrink: 0;
}
.cli-portal__tab-status.is-mobile {
  position: sticky;
  right: 0;
  z-index: 3;
  gap: 4px;
  padding: 0 6px;
  max-width: 54vw;
  background: var(--workbench-tabbar-bg, #252526);
  box-shadow: -10px 0 14px rgba(0, 0, 0, 0.22);
}
.cli-portal__tab-status.is-mobile :deep(.connection-status) { padding: 2px 5px; }
.cli-portal__tab-status.is-mobile :deep(.status-text),
.cli-portal__tab-status.is-mobile :deep(.net-bw) { display: none; }
.cli-portal__tab-status.is-mobile :deep(.agent-circles) {
  max-width: 76px;
  overflow-x: auto;
  scrollbar-width: none;
}
.cli-portal__tab-status.is-mobile :deep(.agent-circles::-webkit-scrollbar) { display: none; }
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
