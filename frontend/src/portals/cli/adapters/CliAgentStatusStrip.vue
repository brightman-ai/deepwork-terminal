<template>
  <div
    v-if="hasContent"
    class="cli-agent-status-strip"
    data-testid="cli-agent-status-strip"
  >
    <!-- Per-tab agent entries -->
    <template v-for="entry in stripEntries" :key="entry.tabId">
      <div
        class="strip-entry"
        :class="entryClass(entry)"
        :title="entryTooltip(entry)"
      >
        <span class="strip-dot" :class="dotClass(entry)" />
        <span class="strip-name">{{ entry.tabName }}</span>
        <span class="strip-status">{{ statusLabel(entry.status) }}</span>
        <span v-if="entry.waitReason" class="strip-reason">· {{ entry.waitReason }}</span>
      </div>
      <span class="strip-sep" aria-hidden="true" />
    </template>

    <!-- RTT from active tab -->
    <div v-if="rtt > 0" class="strip-rtt" :class="rttClass">
      <span class="strip-dot strip-dot--rtt" />
      {{ rtt }}ms
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { AgentState, WSConnectionStatus } from '@/types/terminal'

interface TabEntry {
  tabId: string
  tabName: string
  agentState: AgentState | null
  wsStatus: WSConnectionStatus
}

interface StripEntry {
  tabId: string
  tabName: string
  status: string
  waitReason: string
  wsStatus: WSConnectionStatus
}

const props = defineProps<{
  tabs: TabEntry[]
  rtt: number
}>()

const stripEntries = computed<StripEntry[]>(() =>
  props.tabs
    .filter(t => t.agentState !== null || t.wsStatus === 'connected')
    .map(t => ({
      tabId: t.tabId,
      tabName: t.tabName,
      status: t.agentState?.status ?? (t.wsStatus === 'connected' ? 'idle' : 'disconnected'),
      waitReason: t.agentState?.waitReason ?? '',
      wsStatus: t.wsStatus,
    })),
)

const hasContent = computed(() => stripEntries.value.length > 0 || props.rtt > 0)

function statusLabel(status: string): string {
  switch (status) {
    case 'running':      return 'running'
    case 'waiting':      return 'waiting'
    case 'idle':         return 'idle'
    case 'done':         return 'done'
    case 'disconnected': return 'offline'
    default:             return status
  }
}

function entryClass(e: StripEntry): string {
  if (e.status === 'running') return 'entry--running'
  if (e.status === 'waiting') return 'entry--waiting'
  return ''
}

function dotClass(e: StripEntry): string {
  if (e.status === 'running') return 'strip-dot--running'
  if (e.status === 'waiting') return 'strip-dot--waiting'
  if (e.wsStatus === 'connected') return 'strip-dot--connected'
  return 'strip-dot--idle'
}

function entryTooltip(e: StripEntry): string {
  const parts = [e.tabName, e.status]
  if (e.waitReason) parts.push(e.waitReason)
  return parts.join(' · ')
}

const rttClass = computed(() => {
  if (props.rtt < 50)  return 'rtt-good'
  if (props.rtt < 150) return 'rtt-ok'
  return 'rtt-bad'
})
</script>

<style scoped>
.cli-agent-status-strip {
  display: flex;
  align-items: center;
  height: 24px;
  background: hsl(var(--muted));
  border-bottom: 1px solid hsl(var(--border));
  padding: 0 10px;
  gap: 0;
  font-size: 0.68rem;
  color: hsl(var(--muted-foreground));
  overflow-x: auto;
  overflow-y: hidden;
  flex-shrink: 0;
  scrollbar-width: none;
  white-space: nowrap;
}
.cli-agent-status-strip::-webkit-scrollbar { display: none; }

.strip-entry {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 6px;
  height: 100%;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.entry--running { color: #4caf50; }
.entry--waiting { color: #ff9800; }

.strip-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  flex-shrink: 0;
  display: inline-block;
}
.strip-dot--running   { background: #4caf50; }
.strip-dot--waiting   { background: #ff9800; animation: strip-pulse 1.5s infinite; }
.strip-dot--connected { background: #4caf50; }
.strip-dot--idle      { background: #555; }
.strip-dot--rtt       { background: currentColor; }

@keyframes strip-pulse {
  0%, 100% { opacity: 1; }
  50%       { opacity: 0.3; }
}

.strip-name {
  font-weight: 500;
  max-width: 72px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.strip-status { opacity: 0.75; }
.strip-reason { opacity: 0.6; font-style: italic; }

.strip-sep {
  width: 1px;
  height: 12px;
  background: hsl(var(--border));
  flex-shrink: 0;
  align-self: center;
  margin: 0 2px;
}
.strip-sep:last-of-type { display: none; }

/* RTT pill */
.strip-rtt {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  margin-left: auto;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
  font-size: 0.65rem;
  font-weight: 600;
}
.rtt-good { color: #4caf50; }
.rtt-ok   { color: #ffc107; }
.rtt-bad  { color: #f44336; }
</style>
