<template>
  <div class="connection-status" :class="statusClass" data-testid="cli-connection-status">
    <span class="status-indicator" />
    <span class="status-text">{{ statusText }}</span>
    <template v-if="status === 'connected'">
      <span v-if="safeRtt > 0" class="net-stat net-rtt" :class="rttClass">{{ safeRtt }}ms</span>
      <span class="net-stat net-bw" v-if="safeRxTotal > 0 || safeTxTotal > 0">
        <span class="bw-down">{{ formatBytes(safeRxTotal) }}</span>
        <span class="bw-up">{{ formatBytes(safeTxTotal) }}</span>
      </span>
      <span class="net-stat net-uptime" v-if="safeUptime > 0">{{ formatDuration(safeUptime) }}</span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { WSConnectionStatus } from '@terminal/types/terminal'

const props = defineProps<{
  status: WSConnectionStatus
  rtt?: number
  uploadBps?: number
  downloadBps?: number
  /** cumulative bytes sent / received on the current connection */
  txTotal?: number
  rxTotal?: number
  /** seconds since the current connection opened */
  uptimeSec?: number
}>()

const statusClass = computed(() => `status-${props.status}`)
const safeRtt = computed(() => props.rtt ?? 0)
const safeTxTotal = computed(() => props.txTotal ?? 0)
const safeRxTotal = computed(() => props.rxTotal ?? 0)
const safeUptime = computed(() => props.uptimeSec ?? 0)
const statusText = computed(() => {
  switch (props.status) {
    case 'connecting': return 'Connecting...'
    case 'connected': return 'OK'
    case 'disconnected': return 'Disconnected'
    case 'reconnecting': return 'Reconnecting...'
    case 'preempted': return 'Taken over'
    default: return props.status
  }
})

const rttClass = computed(() => {
  const r = safeRtt.value
  if (r < 50) return 'rtt-good'
  if (r < 150) return 'rtt-ok'
  return 'rtt-bad'
})

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)}K`
  if (bytes < 1073741824) return `${(bytes / 1048576).toFixed(1)}M`
  return `${(bytes / 1073741824).toFixed(2)}G`
}

/** Compact elapsed: 45s · 5m · 1h20m. */
function formatDuration(sec: number): string {
  if (sec < 60) return `${sec}s`
  if (sec < 3600) return `${Math.floor(sec / 60)}m`
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return m > 0 ? `${h}h${m}m` : `${h}h`
}
</script>

<style scoped>
.connection-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  border-radius: 10px;
  font-size: 0.68rem;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  flex-shrink: 0;
}
.status-indicator {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.status-connected .status-indicator { background: #4caf50; }
.status-connecting .status-indicator { background: #ffc107; animation: pulse 1s infinite; }
.status-reconnecting .status-indicator { background: #ff9800; animation: pulse 1s infinite; }
.status-disconnected .status-indicator { background: #f44336; }
.status-preempted .status-indicator { background: #ff5722; animation: pulse 1s infinite; }
.status-connected { background: rgba(76,175,80,0.08); color: #4caf50; }
.status-connecting { background: rgba(255,193,7,0.08); color: #ffc107; }
.status-reconnecting { background: rgba(255,152,0,0.08); color: #ff9800; }
.status-disconnected { background: rgba(244,67,54,0.08); color: #f44336; }
.status-preempted { background: rgba(255,87,34,0.08); color: #ff5722; }

.net-stat {
  font-size: 0.62rem;
  opacity: 0.8;
}
.net-rtt {
  font-weight: 600;
}
.rtt-good { color: #4caf50; }
.rtt-ok { color: #ffc107; }
.rtt-bad { color: #f44336; }

.net-bw {
  display: inline-flex;
  gap: 3px;
}
.bw-down::before { content: "\2193"; font-size: 0.58rem; }
.bw-up::before { content: "\2191"; font-size: 0.58rem; }

.net-uptime {
  opacity: 0.7;
  font-weight: 600;
}
.net-uptime::before { content: "\25F7\2009"; font-size: 0.62rem; opacity: 0.85; }

@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }
</style>
