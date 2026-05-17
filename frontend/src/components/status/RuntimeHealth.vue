<script setup lang="ts">
import { computed } from 'vue'
import { Loader2, Wifi, WifiOff } from 'lucide-vue-next'

type RuntimeHealthState = 'connected' | 'ready' | 'running' | 'reconnecting' | 'pending' | 'disconnected' | 'failed'

const props = withDefaults(defineProps<{
  state?: RuntimeHealthState
  label?: string
  rtt?: number
}>(), {
  state: 'ready',
  label: '',
  rtt: undefined,
})

const icon = computed(() => {
  if (props.state === 'disconnected' || props.state === 'failed') return WifiOff
  if (props.state === 'running' || props.state === 'reconnecting' || props.state === 'pending') return Loader2
  return Wifi
})

const displayLabel = computed(() => props.label || ({
  connected: 'Connected',
  ready: 'Ready',
  running: 'Running',
  reconnecting: 'Reconnecting',
  pending: 'Pending',
  disconnected: 'Disconnected',
  failed: 'Failed',
}[props.state]))
</script>

<template>
  <span class="runtime-health" :class="`runtime-health--${state}`" data-testid="runtime-health">
    <component :is="icon" class="runtime-health__icon" :class="{ 'runtime-health__icon--spin': state === 'running' || state === 'reconnecting' || state === 'pending' }" />
    <span>{{ displayLabel }}</span>
    <span v-if="rtt" class="runtime-health__rtt">{{ rtt }}ms</span>
  </span>
</template>

<style scoped>
.runtime-health {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  white-space: nowrap;
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
}

.runtime-health__icon {
  width: 0.875rem;
  height: 0.875rem;
}

.runtime-health__icon--spin {
  animation: runtime-health-spin 1s linear infinite;
}

.runtime-health--connected,
.runtime-health--ready {
  color: rgb(22 163 74);
}

.runtime-health--running,
.runtime-health--pending,
.runtime-health--reconnecting {
  color: rgb(202 138 4);
}

.runtime-health--disconnected,
.runtime-health--failed {
  color: hsl(var(--destructive));
}

.runtime-health__rtt {
  color: hsl(var(--muted-foreground));
}

@keyframes runtime-health-spin {
  to { transform: rotate(360deg); }
}
</style>
