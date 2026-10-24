<script setup lang="ts">
import { CheckCircle2, Unplug } from 'lucide-vue-next'
import { PANE_STATUS_LABEL, type PaneStatus } from '../../composables/cli/tabPanePresentation'
import { STATUS_COLOR, STATUS_MOTION } from '../../composables/cli/useAgentOverview'
import { LIVENESS_COLOR } from '../../composables/cli/tabLiveness'

defineProps<{ status: PaneStatus; label?: string; compact?: boolean }>()
</script>

<template>
  <span class="pane-status" :class="{'is-compact': compact}" :data-status="status" :role="compact ? 'img' : undefined" :aria-label="`${label ? label + '窗格：' : ''}${PANE_STATUS_LABEL[status]}`" :title="`${label ? label + '窗格：' : ''}${PANE_STATUS_LABEL[status]}`">
    <span v-if="label && !compact" class="pane-status__place">{{ label }}</span>
    <CheckCircle2 v-if="!compact && status === 'done-unseen'" :size="13" aria-hidden="true" />
    <Unplug v-else-if="!compact && status === 'disconnected'" :size="13" aria-hidden="true" />
    <span v-else class="pane-status__dot" aria-hidden="true" />
    <span v-if="!compact">{{ PANE_STATUS_LABEL[status] }}</span>
  </span>
</template>

<style scoped>
.pane-status { --state-color: hsl(var(--muted-foreground)); --waiting-color: v-bind('STATUS_COLOR.waiting'); --running-color: v-bind('STATUS_COLOR.running'); --done-color: v-bind("STATUS_COLOR['done-unseen']"); --disconnected-color: v-bind('LIVENESS_COLOR.detached'); display: inline-flex; align-items: center; gap: 5px; padding: 3px 7px; border: 1px solid color-mix(in srgb, var(--state-color), transparent 65%); border-radius: 5px; background: color-mix(in srgb, var(--state-color), transparent 92%); color: var(--state-color); font-size: 12px; font-weight: 500; line-height: 16px; white-space: nowrap; }
.pane-status[data-status=waiting] { --state-color: var(--waiting-color); }
.pane-status[data-status=running] { --state-color: var(--running-color); }
.pane-status[data-status=done-unseen] { --state-color: var(--done-color); }
.pane-status[data-status=disconnected] { --state-color: var(--disconnected-color); }
.pane-status__place { color: hsl(var(--muted-foreground)); font-size: 10px; }
.pane-status svg { flex-shrink: 0; }
.pane-status__dot { position: relative; flex-shrink: 0; width: 8px; height: 8px; border: 1px solid var(--state-color); border-radius: 50%; background: var(--state-color); }
.pane-status[data-status=unknown] .pane-status__dot { background: transparent; border-style: dashed; }
.pane-status[data-status=disconnected] .pane-status__dot { background: transparent; }
.pane-status[data-status=disconnected] .pane-status__dot::after { content: ''; position: absolute; top: 2px; left: -1px; width: 6px; height: 1px; background: var(--state-color); transform: rotate(-45deg); }
.pane-status[data-status=waiting] .pane-status__dot { --dot-min-opacity: v-bind('STATUS_MOTION.waiting.minOpacity'); animation: status-dot-pulse v-bind('STATUS_MOTION.waiting.duration') v-bind('STATUS_MOTION.waiting.easing') infinite; }
.pane-status[data-status=running] .pane-status__dot { --dot-min-opacity: v-bind('STATUS_MOTION.running.minOpacity'); animation: status-dot-pulse v-bind('STATUS_MOTION.running.duration') v-bind('STATUS_MOTION.running.easing') infinite; }
.is-compact { padding: 0; gap: 0; border: 0; border-radius: 0; background: transparent; line-height: 0; }
.is-compact .pane-status__dot { width: 6px; height: 6px; }
@keyframes status-dot-pulse { 0%, 100% { opacity: var(--dot-min-opacity); } 50% { opacity: 1; } }
@media (prefers-reduced-motion: reduce) { .pane-status__dot { animation: none !important; opacity: 1; } }
</style>
