<template>
  <div class="cli-tab-bar dw-titlebar-blend" data-testid="cli-portal-tab-bar">
    <!-- Groups + tabs -->
    <template v-for="group in groups" :key="group.id">
      <!-- Group header (only when multiple groups) -->
      <div
        v-if="showGroupHeaders"
        class="cli-tab-bar__group-header"
        :style="group.color ? { '--group-color': group.color } : {}"
        :data-testid="`cli-portal-group-header-${group.id}`"
        @click="emit('toggle-group', group.id)"
      >
        <span class="group-name">{{ group.name }}</span>
        <span class="group-chevron">{{ group.collapsed ? '▸' : '▾' }}</span>
      </div>

      <template v-if="!group.collapsed">
        <button
          v-for="tab in group.tabs"
          :key="tab.id"
          class="cli-tab-bar__tab"
          :class="{
            'is-active': tab.id === activeTabId,
            'needs-input': tabNeedsInput(tab.id),
          }"
          :data-testid="`cli-portal-tab-${tab.id}`"
          @click="emit('switch', tab.id)"
          @dblclick.stop="emit('rename-start', tab.id)"
        >
          <!-- Agent status dot -->
          <span class="tab-agent-dot" :class="tabDotClass(tab.id)" />

          <!-- Rename input -->
          <input
            v-if="renamingTabId === tab.id"
            :value="renameValue"
            class="tab-rename-input"
            :data-testid="`cli-portal-tab-rename-${tab.id}`"
            @input="emit('rename-input', ($event.target as HTMLInputElement).value)"
            @blur="emit('rename-commit')"
            @keyup.enter="emit('rename-commit')"
            @keyup.escape="emit('rename-cancel')"
            @keydown.stop
            @keypress.stop
            @click.stop
            @dblclick.stop
            @mousedown.stop
          />
          <span v-else class="tab-name">{{ tab.name }}</span>

          <!-- Close -->
          <span
            class="tab-close"
            :data-testid="`cli-portal-tab-close-${tab.id}`"
            @click.stop="emit('close', tab.id)"
          >&times;</span>
        </button>
      </template>
    </template>

    <!-- Add tab -->
    <button
      class="cli-tab-bar__tab cli-tab-bar__tab--add"
      data-testid="cli-portal-add-tab"
      @click="emit('add')"
    >+</button>

    <!-- Spacer + right-side status (settings icon) -->
    <div class="cli-tab-bar__spacer" />
    <slot name="status" />
  </div>
</template>

<script setup lang="ts">
import type { WorkbenchGroup } from '@/types/workbench'

interface TabRuntime {
  agentState: { status?: string } | null
  wsStatus: string
}

const props = defineProps<{
  groups: WorkbenchGroup[]
  activeTabId: string | undefined
  showGroupHeaders: boolean
  renamingTabId: string | null
  renameValue: string
  tabRuntimes: Record<string, TabRuntime>
}>()

const emit = defineEmits<{
  (e: 'switch', tabId: string): void
  (e: 'close', tabId: string): void
  (e: 'add'): void
  (e: 'rename-start', tabId: string): void
  (e: 'rename-input', value: string): void
  (e: 'rename-commit'): void
  (e: 'rename-cancel'): void
  (e: 'toggle-group', groupId: string): void
}>()

function tabDotClass(tabId: string): string {
  const rt = props.tabRuntimes[tabId]
  if (!rt) return 'dot-idle'
  const status = rt.agentState?.status
  if (status === 'running') return 'dot-running'
  if (status === 'waiting') return 'dot-waiting'
  if (rt.wsStatus === 'connected') return 'dot-connected'
  return 'dot-idle'
}

function tabNeedsInput(tabId: string): boolean {
  const rt = props.tabRuntimes[tabId]
  return rt?.agentState?.status === 'waiting'
}
</script>

<style scoped>
.cli-tab-bar {
  display: flex;
  align-items: stretch;
  height: 36px;
  background: hsl(var(--card));
  border-bottom: 1px solid hsl(var(--border));
  overflow-x: auto;
  overflow-y: hidden;
  flex-shrink: 0;
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.cli-tab-bar::-webkit-scrollbar { display: none; }

/* Group header */
.cli-tab-bar__group-header {
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
.cli-tab-bar__group-header:hover {
  background: hsl(var(--accent));
}
.group-chevron { font-size: 0.7rem; }

/* Tab */
.cli-tab-bar__tab {
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
}
.cli-tab-bar__tab:hover {
  background: hsl(var(--accent));
  color: hsl(var(--foreground));
}
.cli-tab-bar__tab.is-active {
  border-bottom-color: #4a9eff;
  color: hsl(var(--foreground));
  background: hsl(var(--accent));
}
/* Pulse border on agent needs-input */
.cli-tab-bar__tab.needs-input {
  border-bottom-color: #ff9800;
  animation: tab-needs-input 1.5s infinite;
}
@keyframes tab-needs-input {
  0%, 100% { border-bottom-color: #ff9800; }
  50%       { border-bottom-color: rgba(255,152,0,0.3); }
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

/* Close (visible on hover/active) */
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
.cli-tab-bar__tab:hover .tab-close,
.cli-tab-bar__tab.is-active .tab-close { opacity: 0.6; }
.tab-close:hover { opacity: 1 !important; color: #ff6b6b; background: rgba(255,255,255,0.12); }

/* Agent dot */
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

/* Add button */
.cli-tab-bar__tab--add {
  min-width: 36px;
  max-width: 36px;
  font-size: 1.1rem;
  color: hsl(var(--muted-foreground));
  flex-shrink: 0;
  padding: 0;
  justify-content: center;
}
.cli-tab-bar__tab--add:hover { color: rgba(255,255,255,0.8); }

/* Spacer */
.cli-tab-bar__spacer { flex: 1; min-width: 8px; }
</style>
