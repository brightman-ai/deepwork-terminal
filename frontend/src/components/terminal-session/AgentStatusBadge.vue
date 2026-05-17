<template>
  <div v-if="circles.length > 0" class="agent-circles">
    <div
      v-for="c in circles"
      :key="c.id"
      class="agent-circle"
      :class="circleClass(c)"
      :title="circleTooltip(c)"
      @click.stop="togglePopover(c, $event)"
    >
      {{ c.label }}
    </div>

    <Teleport to="body">
      <div v-if="activePopover" class="agent-popover" :style="popoverStyle" @click.stop>
        <div class="popover-tool">{{ activePopover.tool }}</div>
        <div class="popover-status">{{ activePopover.status }}{{ activePopover.waitReason ? ' · ' + activePopover.waitReason : '' }}</div>
        <div v-if="activePopover.model" class="popover-model">{{ activePopover.model }}</div>
        <div v-if="activePopover.totalTokens" class="popover-tokens">{{ formatTokens(activePopover.totalTokens) }} tokens</div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onUnmounted, nextTick, watch } from 'vue'
import type { AgentState } from '@/types/terminal'

interface CircleItem {
  id: string
  label: string
  status: string
  tool: string
  model: string
  totalTokens: number
  waitReason: string
  tmuxWindow: number | null
  tmuxPane: number | null
}

const props = defineProps<{
  state: AgentState | null
  notifications?: AgentState[]
}>()

const activePopover = ref<CircleItem | null>(null)
const popoverStyle = ref<Record<string, string>>({})

// Track previous status per circle id for completion detection.
const previousStatus = ref<Record<string, string>>({})
const completedIds = ref<Set<string>>(new Set())

// Build circle list from notifications array.
// All notifications are shown — status=none panes get an empty circle.
const circles = computed((): CircleItem[] => {
  const notifs = props.notifications ?? []

  if (notifs.length > 0) {
    return notifs.map((n) => {
      return {
        id: `${n.tmuxWindow ?? 'direct'}-${n.tmuxPane ?? 0}`,
        label: stateLabel(n),
        status: n.status,
        tool: n.tool || (n.tmuxWindow != null ? 'tmux' : 'terminal'),
        model: n.model || '',
        totalTokens: n.totalTokens || 0,
        waitReason: n.waitReason || '',
        tmuxWindow: n.tmuxWindow ?? null,
        tmuxPane: n.tmuxPane ?? null,
      }
    })
  }

  // Fallback: show current state as a single circle (direct mode or no notifications yet)
  if (props.state) {
    const s = props.state
    return [
      {
        id: 'current',
        label: stateLabel(s),
        status: s.status,
        tool: s.tool || (s.tmuxWindow != null ? 'tmux' : 'terminal'),
        model: s.model || '',
        totalTokens: s.totalTokens || 0,
        waitReason: s.waitReason || '',
        tmuxWindow: s.tmuxWindow ?? null,
        tmuxPane: s.tmuxPane ?? null,
      },
    ]
  }

  return []
})

function stateLabel(state: AgentState): string {
  if (state.tmuxWindow == null) return 'D'
  if (state.tmuxPane != null) return `${state.tmuxWindow}.${state.tmuxPane}`
  return String(state.tmuxWindow)
}

// Detect transitions from running → idle/none and mark as completed.
watch(circles, (newCircles) => {
  const newCompleted = new Set(completedIds.value)
  const newPrev: Record<string, string> = {}

  for (const c of newCircles) {
    const prev = previousStatus.value[c.id]
    if (prev === 'running' && (c.status === 'idle' || c.status === 'none')) {
      newCompleted.add(c.id)
      setTimeout(() => {
        completedIds.value.delete(c.id)
      }, 30000)
    }
    newPrev[c.id] = c.status
  }

  completedIds.value = newCompleted
  previousStatus.value = newPrev
})

function circleClass(c: CircleItem): string {
  if (completedIds.value.has(c.id)) return 'circle-completed'
  const s = c.status
  if (s === 'running') return 'circle-running'
  if (s === 'waiting') return 'circle-waiting'
  if (s === 'done') return 'circle-done'
  if (s === 'idle' && c.tool) return 'circle-idle'
  return 'circle-empty'
}

function circleTooltip(c: CircleItem): string {
  const topology = c.tmuxWindow != null
    ? `tmux ${c.tmuxWindow}.${c.tmuxPane ?? 0}`
    : 'direct terminal'
  const parts = [topology, c.tool, c.status].filter(Boolean)
  if (c.waitReason) parts.push(c.waitReason)
  if (c.totalTokens) parts.push(formatTokens(c.totalTokens) + ' tokens')
  return parts.join(' · ')
}

function togglePopover(c: CircleItem, event: MouseEvent) {
  if (activePopover.value?.id === c.id) {
    activePopover.value = null
    return
  }
  activePopover.value = c
  nextTick(() => {
    const el = event.target as HTMLElement
    const rect = el.getBoundingClientRect()
    popoverStyle.value = {
      top: `${rect.bottom + 6}px`,
      right: `${window.innerWidth - rect.right}px`,
    }
  })
}

function onClickOutside() {
  activePopover.value = null
}

watch(activePopover, (val) => {
  if (val) {
    nextTick(() => document.addEventListener('click', onClickOutside))
  } else {
    document.removeEventListener('click', onClickOutside)
  }
})
onUnmounted(() => document.removeEventListener('click', onClickOutside))

function formatTokens(n: number): string {
  if (!n) return '0'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return `${n}`
}
</script>

<style scoped>
.agent-circles {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  margin-right: 4px;
}

.agent-circle {
  min-width: 20px;
  height: 20px;
  padding: 0 5px;
  border-radius: 999px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  color: white;
  cursor: pointer;
  flex-shrink: 0;
  transition: transform 0.15s;
  user-select: none;
}

.agent-circle:hover {
  transform: scale(1.2);
}

.circle-running { background: #4C8DFF; }
.circle-waiting { background: #FF9800; animation: pulse-circle 1.5s infinite; }
.circle-idle { background: #555; }
.circle-done { background: #4CAF50; }
.circle-empty {
  background: transparent;
  border: 1.5px solid #444;
  color: #666;
}
.circle-completed {
  background: #4CAF50;
  animation: pulse-complete 2s ease-out 3;
}

@keyframes pulse-circle {
  0%, 100% { opacity: 1; box-shadow: 0 0 0 0 rgba(255, 152, 0, 0.4); }
  50% { opacity: 0.85; box-shadow: 0 0 0 4px rgba(255, 152, 0, 0); }
}

@keyframes pulse-complete {
  0%, 100% { box-shadow: 0 0 0 0 rgba(76, 175, 80, 0.4); }
  50% { box-shadow: 0 0 0 5px rgba(76, 175, 80, 0); }
}
</style>

<!-- Non-scoped styles for Teleported popover -->
<style>
.agent-popover {
  position: fixed;
  background: #1e1e2e;
  border: 1px solid #444;
  border-radius: 8px;
  padding: 8px 12px;
  z-index: 9999;
  font-size: 0.78rem;
  color: #ccc;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  min-width: 140px;
  line-height: 1.6;
}
.agent-popover .popover-tool { font-weight: 600; color: #e0e0e0; text-transform: capitalize; }
.agent-popover .popover-status { color: #aaa; }
.agent-popover .popover-model { color: #666; font-size: 0.72rem; }
.agent-popover .popover-tokens { color: #4C8DFF; font-variant-numeric: tabular-nums; }
</style>
