<template>
  <div class="cli-terminal-view" data-testid="cli-portal-terminal-view">
    <!-- Loading / error / empty states -->
    <div v-if="loading" class="terminal-state-msg" data-testid="cli-portal-loading">
      加载中…
    </div>
    <div
      v-else-if="error"
      class="terminal-state-msg terminal-state-msg--error"
      data-testid="cli-portal-error"
    >{{ error }}</div>
    <div
      v-else-if="!activeTabId"
      class="terminal-state-msg"
      data-testid="cli-portal-empty"
    >
      <p>正在准备终端…</p>
    </div>

    <!-- One CliTerminalSurface per tab — v-show for tab switching, keep DOM alive -->
    <template v-else>
      <CliTerminalSurface
        v-for="tab in tabsWithSession"
        :key="tab.sessionId"
        v-show="tab.id === activeTabId"
        :session-id="tab.sessionId!"
        :session-name="tab.name"
        :active="tab.id === activeTabId"
        :http-base="tab.httpBase"
        :ws-base="tab.wsBase"
        :auth-token="tab.authToken"
        :machine-label="tab.machineLabel"
        :is-remote="tab.isRemote"
        :conn-error="tab.connError"
        :diagnose="tab.diagnose"
        :dw-rollup="dwRollup"
        :dw-overview-open="dwOverviewOpen"
        :ref="(el) => onSurfaceRef(tab.id, el)"
        :data-testid="`cli-portal-surface-${tab.id}`"
        @agent-state="(s) => emit('agent-state', tab.id, s)"
        @agent-notifications="(s) => emit('agent-notifications', tab.id, s)"
        @session-exit="(code) => emit('session-exit', tab.id, code)"
        @connection-change="(s) => emit('connection-change', tab.id, s)"
        @dw-toggle-overview="emit('dw-toggle-overview')"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import CliTerminalSurface from '@terminal/components/terminal-session/CliTerminalSurface.vue'
import type { EffectiveStatus } from '@terminal/composables/cli/useAgentOverview'
import type { AgentState, WSConnectionStatus } from '@terminal/types/terminal'

interface TabWithSession {
  id: string
  name: string
  sessionId?: string
  // Remote-tab connection (mesh), resolved upstream by resolveTabConnection. Local tabs leave
  // httpBase/wsBase/authToken empty → CliTerminalSurface falls back to same-origin behavior.
  httpBase?: string
  wsBase?: string
  authToken?: string
  machineLabel?: string
  isRemote?: boolean
  connError?: string
  diagnose?: () => Promise<{ ok: boolean; error?: string }>
}

defineProps<{
  loading: boolean
  error: string | null
  activeTabId: string | undefined
  tabsWithSession: TabWithSession[]
  /** 非 tmux 底栏的状态卷起与总览开关。这一层只**转发** —— 标签的身份/顺序/状态归 portal
   *  （useCliState），终端表面不自己推导（否则就有第二个真相源）。 */
  dwRollup?: Record<EffectiveStatus, number>
  dwOverviewOpen?: boolean
}>()

const emit = defineEmits<{
  (e: 'register-surface', tabId: string, el: InstanceType<typeof CliTerminalSurface> | null): void
  (e: 'agent-state', tabId: string, state: AgentState | null): void
  (e: 'agent-notifications', tabId: string, states: AgentState[]): void
  (e: 'session-exit', tabId: string, exitCode: number): void
  (e: 'connection-change', tabId: string, status: WSConnectionStatus): void
  /** 底栏的总览胶囊，原样上交给拥有标签的那一层执行。 */
  (e: 'dw-toggle-overview'): void
}>()

function onSurfaceRef(tabId: string, el: unknown) {
  emit('register-surface', tabId, el as InstanceType<typeof CliTerminalSurface> | null)
}
</script>

<style scoped>
.cli-terminal-view {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  position: relative;
  display: flex;
  flex-direction: column;
}

.terminal-state-msg {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 48px;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}
.terminal-state-msg--error { color: #f87171; }

/* Surface fills remaining vertical space */
.cli-terminal-view :deep(.cli-terminal-surface) {
  flex: 1;
  min-height: 0;
}
</style>
