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
          <!-- Settings (gear) — the canonical standalone entry to the settings portal,
               where the built-in Cloudflare-tunnel / HTTPS control lives. Lives in the top
               tab-bar status slot (always visible, right-aligned, above the bottom
               tmux/compose toolbars so it is never occluded). Restores the entry the
               deleted TerminalWorkbenchPage used to carry. -->
          <button
            class="cli-portal__settings-btn"
            type="button"
            title="设置 (Cloudflare 隧道 / HTTPS)"
            aria-label="设置"
            data-testid="cli-portal-settings-btn"
            @click="openSettings"
          >
            <Settings :size="18" />
          </button>
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
import { useRouter } from 'vue-router'
import { Settings } from 'lucide-vue-next'
import { usePortalRuntime } from '@ce/composables/layout/usePortalRuntime'
import { cliScenarios, cliBreakpointOverrides } from './cliScenarios'
import { cliLayoutPolicy } from './cliLayoutPolicy'
import { useCliState } from './useCliState'
import ConnectionStatus from '@terminal/components/terminal-session/ConnectionStatus.vue'
import AgentStatusBadge from '@terminal/components/terminal-session/AgentStatusBadge.vue'
import { CliTabBar, CliTerminalView } from './adapters'

// Cross-portal navigation is plain router push — the settings portal route
// (/portal/settings) is registered via the portal registry (see router/index.ts).
const router = useRouter()
function openSettings() {
  router.push('/portal/settings')
}

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
/* Settings gear — always-visible, tappable hit-target in the top tab bar. Kept
   outside the .is-mobile :deep() hides below so it never collapses on mobile. */
.cli-portal__settings-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--workbench-text-muted, hsl(var(--muted-foreground, 240 4% 60%)));
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
}
.cli-portal__settings-btn:hover {
  background: hsl(var(--accent, 240 4% 22%));
  color: var(--workbench-text, hsl(var(--foreground, 0 0% 90%)));
}
.cli-portal__settings-btn:active { background: hsl(var(--accent, 240 4% 22%)); }
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
