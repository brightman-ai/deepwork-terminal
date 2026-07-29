<template>
  <!-- The per-surface agent chip: what the agent in THIS terminal is doing, in words.
       Shown only when this shell is NOT attached to tmux (tmux hands the row to TmuxPaneBar). -->
  <div v-if="chips.length > 0" class="agent-chips">
    <button
      v-for="c in chips"
      :key="c.id"
      type="button"
      class="agent-chip"
      :class="`s-${c.effective}`"
      :title="chipTooltip(c)"
      :data-testid="`agent-chip-${c.id}`"
      @click.stop="togglePopover(c, $event)"
    >
      <span v-if="c.topology" class="agent-chip-idx">{{ c.topology }}</span>
      <span class="agent-chip-dot" />
      <span class="agent-chip-text">{{ c.text }}</span>
    </button>

    <Teleport to="body">
      <div v-if="activePopover" class="agent-popover" :style="popoverStyle" @click.stop>
        <div class="popover-tool">{{ activePopover.text }}</div>
        <div v-if="activePopover.waitReason" class="popover-status">{{ activePopover.waitReason }}</div>
        <div v-if="activePopover.model" class="popover-model">{{ activePopover.model }}</div>
        <div v-if="activePopover.totalTokens" class="popover-tokens">{{ formatTokens(activePopover.totalTokens) }} tokens</div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
/**
 * ── Why this is text and not a letter ────────────────────────────────────────────────────────
 * This used to render a coloured circle containing a single character, and for a non-tmux
 * terminal that character was the literal `'D'` — the code's internal word for "Direct mode".
 * An implementation term leaked straight onto the screen: nothing in the UI ever defined it, and
 * it sat alone in an otherwise empty row, so it read as a stray glyph rather than as status.
 * A label that has to be decoded is not a label. It now says what it means ("Codex 运行中"),
 * reusing agentSignalText — the SAME phrasing the overview cards and the tmux pane attribution
 * use, so the three never diverge into three dialects.
 *
 * ── Why the colours come from STATUS_COLOR ───────────────────────────────────────────────────
 * It also carried a third, private palette (running = BLUE #4C8DFF, done = green, waiting =
 * orange) while every other status surface used STATUS_COLOR (running = green, waiting = red,
 * done-unseen = amber). Same concept, opposite colours, on screen at the same time. Now it binds
 * the shared constants like every other dot, including the motion contract.
 */
import { ref, computed, onUnmounted, nextTick, watch } from 'vue'
import type { AgentState } from '@terminal/types/terminal'
import {
  agentSignalText,
  STATUS_COLOR,
  STATUS_MOTION,
  type EffectiveStatus,
} from '@terminal/composables/cli/useAgentOverview'

interface ChipItem {
  id: string
  /** "3.1" for a tmux pane, '' for this session's own agent (no topology to disambiguate). */
  topology: string
  text: string
  effective: EffectiveStatus
  model: string
  totalTokens: number
  waitReason: string
}

const props = defineProps<{
  state: AgentState | null
  notifications?: AgentState[]
}>()

const activePopover = ref<ChipItem | null>(null)
const popoverStyle = ref<Record<string, string>>({})

/** AgentState → the same four-state vocabulary every other status surface renders.
 *  `awaitingUser` is what turns a finished turn into the amber "done-unseen" tier; without it a
 *  completed agent looked identical to one that never ran. */
function effectiveOf(s: AgentState): EffectiveStatus {
  if (s.status === 'waiting') return 'waiting'
  if (s.status === 'running') return 'running'
  return s.awaitingUser ? 'done-unseen' : 'idle'
}

function toChip(s: AgentState, id: string): ChipItem {
  return {
    id,
    topology: s.tmuxWindow != null ? `${s.tmuxWindow}${s.tmuxPane != null ? '.' + s.tmuxPane : ''}` : '',
    text: agentSignalText(s.tool, s.status, s.awaitingUser, s.endedOnQuestion) || '终端',
    effective: effectiveOf(s),
    model: s.model || '',
    totalTokens: s.totalTokens || 0,
    waitReason: s.waitReason || '',
  }
}

const chips = computed((): ChipItem[] => {
  const notifs = props.notifications ?? []
  if (notifs.length > 0) {
    return notifs.map((n, i) => toChip(n, `${n.tmuxWindow ?? 'direct'}-${n.tmuxPane ?? i}`))
  }
  return props.state ? [toChip(props.state, 'current')] : []
})

function chipTooltip(c: ChipItem): string {
  const parts = [c.topology ? `tmux ${c.topology}` : '本终端', c.text]
  if (c.waitReason) parts.push(c.waitReason)
  if (c.model) parts.push(c.model)
  if (c.totalTokens) parts.push(formatTokens(c.totalTokens) + ' tokens')
  return parts.join(' · ')
}

const POPOVER_WIDTH = 200
const EDGE = 8

function togglePopover(c: ChipItem, event: MouseEvent) {
  if (activePopover.value?.id === c.id) {
    activePopover.value = null
    return
  }
  activePopover.value = c
  nextTick(() => {
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
    // Left-aligned to the chip and CLAMPED to the viewport. It used to pin the popover's RIGHT
    // edge to the chip's right edge, which pushed it off-screen to the left for any chip near the
    // left edge — i.e. always, since this row starts at the left. That is the "显示不正确" in the
    // report: the panel rendered underneath the nav rail, clipped.
    const left = Math.min(
      Math.max(EDGE, rect.left),
      Math.max(EDGE, window.innerWidth - POPOVER_WIDTH - EDGE),
    )
    popoverStyle.value = { top: `${rect.bottom + 6}px`, left: `${left}px` }
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
/* The ONE place these colours/rhythms enter this file — bound live from the same constants the
   pane bar, the status sheet and the overview grid bind. See useAgentOverview.ts. */
.agent-chips {
  --status-waiting: v-bind('STATUS_COLOR.waiting');
  --status-running: v-bind('STATUS_COLOR.running');
  --status-done: v-bind("STATUS_COLOR['done-unseen']");
  --dot-waiting-duration: v-bind('STATUS_MOTION.waiting.duration');
  --dot-waiting-easing: v-bind('STATUS_MOTION.waiting.easing');
  --dot-waiting-min: v-bind('STATUS_MOTION.waiting.minOpacity');
  --dot-running-duration: v-bind('STATUS_MOTION.running.duration');
  --dot-running-easing: v-bind('STATUS_MOTION.running.easing');
  --dot-running-min: v-bind('STATUS_MOTION.running.minOpacity');
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.agent-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 9px;
  border-radius: 999px;
  border: 1px solid hsl(var(--border, 240 4% 24%));
  background: transparent;
  color: hsl(var(--muted-foreground, 240 4% 64%));
  font-size: 0.72rem;
  line-height: 1;
  white-space: nowrap;
  cursor: pointer;
  flex-shrink: 0;
}
.agent-chip:hover { background: hsl(var(--accent, 240 4% 20%)); }

.agent-chip-idx {
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.66rem;
  font-weight: 700;
  opacity: 0.7;
}

.agent-chip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
  background: #6f5a90;
}
/* Same three-state motion contract as every other dot: waiting = slow/big, running = quick/small,
   done-unseen = static. One shared @keyframes, each rule only remaps --dot-min-opacity. */
.agent-chip.s-waiting { border-color: color-mix(in srgb, var(--status-waiting) 45%, transparent); color: var(--status-waiting); }
.agent-chip.s-waiting .agent-chip-dot {
  background: var(--status-waiting);
  --dot-min-opacity: var(--dot-waiting-min);
  animation: status-dot-pulse var(--dot-waiting-duration) var(--dot-waiting-easing) infinite;
}
.agent-chip.s-running .agent-chip-dot {
  background: var(--status-running);
  --dot-min-opacity: var(--dot-running-min);
  animation: status-dot-pulse var(--dot-running-duration) var(--dot-running-easing) infinite;
}
.agent-chip.s-done-unseen { color: var(--status-done); }
.agent-chip.s-done-unseen .agent-chip-dot { background: var(--status-done); }  /* STATIC by contract */

@keyframes status-dot-pulse {
  0%, 100% { opacity: var(--dot-min-opacity); }
  50% { opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .agent-chip-dot { animation: none !important; opacity: 1; }
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
  width: 200px;
  box-sizing: border-box;
  line-height: 1.6;
}
.agent-popover .popover-tool { color: #fff; font-weight: 600; }
.agent-popover .popover-model { color: #8a7aa8; font-size: 0.72rem; }
.agent-popover .popover-tokens { color: #8a7aa8; font-size: 0.72rem; font-variant-numeric: tabular-nums; }
</style>
