<template>
  <div class="connection-status" :class="statusClass" data-testid="cli-connection-status">
    <span class="status-indicator" />
    <span class="status-text">{{ statusText }}</span>
    <template v-if="status === 'connected' && safeRtt > 0">
      <span class="net-stat net-rtt" :class="rttClass">{{ safeRtt }}ms</span>
      <span class="net-stat net-bw" v-if="safeDownloadBps > 0 || safeUploadBps > 0">
        <span class="bw-down">{{ formatBps(safeDownloadBps) }}</span>
        <span class="bw-up">{{ formatBps(safeUploadBps) }}</span>
      </span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { WSConnectionStatus } from '@/types/terminal'

const props = defineProps<{
  status: WSConnectionStatus
  rtt?: number
  uploadBps?: number
  downloadBps?: number
}>()

const statusClass = computed(() => `status-${props.status}`)
const safeRtt = computed(() => props.rtt ?? 0)
const safeUploadBps = computed(() => props.uploadBps ?? 0)
const safeDownloadBps = computed(() => props.downloadBps ?? 0)
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

function formatBps(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)}K`
  return `${(bytes / 1048576).toFixed(1)}M`
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

@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }
</style>
