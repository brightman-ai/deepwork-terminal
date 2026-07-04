<template>
  <!-- Compact inline: just the dot + RTT + live ↓↑ rate. Everything else (uptime,
       cumulative traffic) moves into a tap-to-open popover so the bar stays small. -->
  <div
    class="connection-status" :class="[statusClass, { 'is-clickable': status === 'connected' }]"
    data-testid="cli-connection-status"
    :title="targetLabel ? `连接目标: ${targetLabel}` : undefined"
    @click="status === 'connected' && (popOpen = !popOpen)"
  >
    <span class="status-indicator" />
    <span v-if="status !== 'connected'" class="status-text">{{ statusText }}</span>
    <!-- Non-intrusive diagnostic for a failing (usually remote) connection: an ⓘ that
         opens the classified reason (auth code? IP/port unreachable?) so a stuck
         "Connecting…" isn't a dead end. Only shown when we actually have a reason. -->
    <button
      v-if="diagnostic && status !== 'connected'"
      class="status-diag" type="button" data-testid="cli-conn-diagnostic"
      :title="diagnostic" @click.stop="popOpen = !popOpen"
    >ⓘ</button>
    <template v-if="status === 'connected'">
      <span v-if="safeRtt > 0" class="net-stat net-rtt" :class="rttClass">{{ safeRtt }}ms</span>
      <span class="net-stat net-speed" v-if="safeDownBps > 0 || safeUpBps > 0">
        <span class="bw-down">{{ formatRate(safeDownBps) }}</span>
        <span class="bw-up">{{ formatRate(safeUpBps) }}</span>
      </span>
    </template>

    <!-- Detail popover (tap the chip). Backdrop closes on outside tap. -->
    <Teleport to="body">
      <div v-if="popOpen" class="net-pop-backdrop" @click="popOpen = false" />
    </Teleport>
    <div v-if="popOpen && diagnostic && status !== 'connected'" class="net-pop net-pop--diag" data-testid="cli-connection-diag-pop" @click.stop>
      <div class="net-pop-diag-title">连不上？可能原因</div>
      <div class="net-pop-diag-reason">{{ diagnostic }}</div>
      <div v-if="targetLabel" class="net-pop-row"><span>目标</span><span>{{ targetLabel }}</span></div>
    </div>
    <div v-else-if="popOpen" class="net-pop" data-testid="cli-connection-pop" @click.stop>
      <div class="net-pop-row"><span>状态</span><span class="np-ok">已连接</span></div>
      <div v-if="targetLabel" class="net-pop-row"><span>目标</span><span>{{ targetLabel }}</span></div>
      <div class="net-pop-row"><span>延迟 RTT</span><span :class="rttClass">{{ safeRtt > 0 ? safeRtt + ' ms' : '—' }}</span></div>
      <div class="net-pop-row"><span>连接时长</span><span>{{ safeUptime > 0 ? formatDuration(safeUptime) : '—' }}</span></div>
      <div class="net-pop-sep" />
      <div class="net-pop-row"><span>实时 ↓ / ↑</span><span>{{ formatRate(safeDownBps) }} / {{ formatRate(safeUpBps) }}</span></div>
      <div class="net-pop-row"><span>累计 ↓ 下行</span><span>{{ formatBytes(safeRxTotal) }}</span></div>
      <div class="net-pop-row"><span>累计 ↑ 上行</span><span>{{ formatBytes(safeTxTotal) }}</span></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { WSConnectionStatus } from '@terminal/types/terminal'

// Tap-to-open detail popover (uptime + cumulative traffic). Kept out of the inline bar.
const popOpen = ref(false)

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
  /** Human-readable endpoint label for this exact WS connection. */
  targetLabel?: string
  /** Classified failure reason for a stuck/failed connection (remote peers). Shown via the ⓘ. */
  diagnostic?: string
}>()

const statusClass = computed(() => `status-${props.status}`)
const safeRtt = computed(() => props.rtt ?? 0)
const safeDownBps = computed(() => props.downloadBps ?? 0)
const safeUpBps = computed(() => props.uploadBps ?? 0)
const safeTxTotal = computed(() => props.txTotal ?? 0)
const safeRxTotal = computed(() => props.rxTotal ?? 0)
const safeUptime = computed(() => props.uptimeSec ?? 0)
// Close the CONNECTED-stats popover when the connection drops — but keep the popover usable
// for the diagnostic case (a failing connection with a reason still wants its ⓘ popover).
watch(() => props.status, (s) => { if (s !== 'connected' && !props.diagnostic) popOpen.value = false })
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

/** Live throughput as a compact per-second rate, e.g. 1.2K/s. */
function formatRate(bps: number): string {
  return `${formatBytes(bps)}/s`
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
  position: relative;
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
.connection-status.is-clickable { cursor: pointer; }
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

.net-speed {
  display: inline-flex;
  gap: 3px;
  opacity: 0.9;
}
.bw-down::before { content: "\2193"; font-size: 0.58rem; }
.bw-up::before { content: "\2191"; font-size: 0.58rem; }

/* Detail popover — small card anchored under the chip (right-aligned to stay on screen). */
.net-pop-backdrop {
  position: fixed; inset: 0; z-index: 60;
}
.net-pop {
  position: absolute; top: calc(100% + 6px); right: 0; z-index: 61;
  min-width: 168px;
  padding: 8px 10px;
  background: #1a1230;
  border: 1px solid #3a2860;
  border-radius: 10px;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.5);
  color: #d8c4f0;
  cursor: default;
  white-space: nowrap;
}
.net-pop-row {
  display: flex; align-items: center; justify-content: space-between; gap: 14px;
  padding: 2px 0;
  font-size: 0.66rem;
}
.net-pop-row > span:first-child { color: #8a76aa; }
.net-pop-row > span:last-child { font-weight: 600; font-variant-numeric: tabular-nums; }
.net-pop-row .np-ok { color: #4caf50; }
.net-pop-sep { height: 1px; background: #2e2050; margin: 5px 0; }

.status-diag {
  background: transparent; border: none; padding: 0 2px; cursor: pointer;
  color: currentColor; opacity: 0.75; font-size: 0.72rem; line-height: 1;
}
.status-diag:hover { opacity: 1; }
.net-pop--diag { white-space: normal; max-width: 240px; }
.net-pop-diag-title { color: #f4b7b7; font-size: 0.66rem; font-weight: 600; margin-bottom: 4px; }
.net-pop-diag-reason { color: #e9d8f4; font-size: 0.7rem; line-height: 1.5; margin-bottom: 6px; }

@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }
</style>
