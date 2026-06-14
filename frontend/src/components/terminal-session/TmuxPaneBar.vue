<template>
  <!-- One compact line of tmux window tabs, mounted under the "终端 N" title row and
       above the xterm surface. Renders only when a tmux server is running so it stays
       invisible for plain shells. Topology is PUSHED (WS tmux_state) — each tap just
       sends prefix+digit (select-window) or prefix+c (new-window); no server roundtrip
       to learn the layout. -->
  <div v-if="serverRunning && windows.length" class="tmux-pane-bar" data-testid="tmux-pane-bar">
    <span class="tpb-label">tmux</span>
    <button
      v-for="w in windows"
      :key="w.index"
      class="tpb-win"
      :class="{ 'is-active': w.active }"
      :data-testid="`tmux-win-${w.index}`"
      @click="selectWindow(w.index)"
    >
      <span class="tpb-idx">{{ w.index }}</span>
      <span v-if="dotClass(w)" class="tpb-dot" :class="dotClass(w)" />
    </button>
    <button class="tpb-win tpb-add" data-testid="tmux-win-add" @click="newWindow">+</button>

    <!-- WS7 secondary entry — contextual notify bell. Pushed to the right; badged
         when notifications are off, and a one-time inline hint pops near it the first
         time any pane enters `waiting` while unsubscribed. Opens the shared guide. -->
    <div class="tpb-spacer" />
    <div class="tpb-bell-wrap">
      <Transition name="tpb-hint">
        <span v-if="showWaitingHint" class="tpb-hint" data-testid="tmux-notify-hint">
          有 agent 在等待 — 开启通知？
        </span>
      </Transition>
      <button
        class="tpb-bell"
        :class="{ 'is-nudge': bellNudge, 'is-alert': anyWaiting && !push.subscribed.value }"
        type="button"
        title="通知设置"
        aria-label="通知设置"
        data-testid="tmux-notify-bell"
        @click="onBellClick"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        <span v-if="bellNudge" class="tpb-bell-dot" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { TmuxWindowState } from '@terminal/types/terminal'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { usePushNotifications } from '@terminal/composables/cli/usePushNotifications'

const props = defineProps<{ sessionId: string }>()

const emit = defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'open-notify'): void
}>()

const tmux = useTmuxState(() => props.sessionId)
const serverRunning = tmux.serverRunning
const windows = tmux.windows
const push = usePushNotifications()

// Bell nudges (amber dot) whenever notifications aren't on and aren't hard-denied.
const bellNudge = computed(() =>
  push.permission.value !== 'denied' && !push.subscribed.value,
)

const anyWaiting = computed(() =>
  windows.value.some(w => (w.panes ?? []).some(p => p.agentStatus === 'waiting')),
)

// One-time inline hint: first time a pane enters `waiting` while unsubscribed, pop a
// hint near the bell for a few seconds. `hintShown` makes it fire at most once per tab.
const showWaitingHint = ref(false)
const hintShown = ref(false)
let hintTimer: ReturnType<typeof setTimeout> | null = null
watch(anyWaiting, (waiting) => {
  if (waiting && !hintShown.value && !push.subscribed.value && push.permission.value !== 'denied') {
    hintShown.value = true
    showWaitingHint.value = true
    if (hintTimer) clearTimeout(hintTimer)
    hintTimer = setTimeout(() => { showWaitingHint.value = false }, 6000)
  }
})

function onBellClick(): void {
  showWaitingHint.value = false
  emit('open-notify')
}

/** waiting in any pane → red; else running anywhere → dim; else no dot. */
function dotClass(w: TmuxWindowState): string {
  const panes = w.panes ?? []
  if (panes.some(p => p.agentStatus === 'waiting')) return 'tpb-dot--waiting'
  if (panes.some(p => p.agentStatus === 'running')) return 'tpb-dot--running'
  return ''
}

function selectWindow(index: number): void {
  // tmux: prefix + digit = select-window
  emit('sendKey', tmux.prefixSeq(String(index)))
}

function newWindow(): void {
  // tmux: prefix + c = new-window
  emit('sendKey', tmux.prefixSeq('c'))
}
</script>

<style scoped>
.tmux-pane-bar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  background: #16121f;
  border-bottom: 1px solid #2a1f3a;
  overflow-x: auto;
  overflow-y: hidden;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
  scrollbar-width: none;
  user-select: none;
  -webkit-user-select: none;
}
.tmux-pane-bar::-webkit-scrollbar { display: none; }

.tpb-label {
  flex-shrink: 0;
  font-size: 0.62rem;
  font-weight: 600;
  letter-spacing: 0.4px;
  color: #6f5a90;
  margin-right: 2px;
}

.tpb-win {
  flex-shrink: 0;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 30px;
  height: 26px;
  padding: 0 8px;
  background: #221636;
  color: #b08fd0;
  border: 1px solid #3a2860;
  border-radius: 5px;
  font-size: 0.78rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
  touch-action: manipulation;
  transition: background 0.08s, transform 0.08s;
}
.tpb-win:active {
  background: #301f4e;
  transform: translateY(1px) scale(0.96);
}
.tpb-win.is-active {
  background: #4a2a7a;
  border-color: #7a4ab0;
  color: #f0e0ff;
}

.tpb-idx { line-height: 1; }

.tpb-add {
  color: #7a6a9a;
  font-size: 0.95rem;
  font-weight: 500;
}

.tpb-dot {
  position: absolute;
  top: 3px;
  right: 3px;
  width: 5px;
  height: 5px;
  border-radius: 50%;
}
.tpb-dot--waiting { background: #ff5252; }
.tpb-dot--running { background: #6a6a7a; }

/* WS7 — contextual notify bell, pushed to the trailing edge. */
.tpb-spacer { flex: 1; min-width: 6px; }
.tpb-bell-wrap { position: relative; display: flex; align-items: center; flex-shrink: 0; }

.tpb-bell {
  position: relative;
  display: inline-grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 5px;
  background: transparent;
  border: 1px solid transparent;
  color: #6f5a90;
  cursor: pointer;
  touch-action: manipulation;
  transition: color 0.1s, background 0.1s;
}
.tpb-bell:active { transform: scale(0.92); }
.tpb-bell.is-nudge { color: #b08fd0; }
.tpb-bell.is-alert { color: #f08a3c; }
.tpb-bell-dot {
  position: absolute;
  top: 3px; right: 3px;
  width: 6px; height: 6px;
  border-radius: 50%;
  background: #f08a3c;
  box-shadow: 0 0 0 2px #16121f;
}

.tpb-hint {
  position: absolute;
  right: calc(100% + 6px);
  top: 50%;
  transform: translateY(-50%);
  white-space: nowrap;
  padding: 3px 8px;
  border-radius: 6px;
  background: #251a14;
  border: 1px solid #4a3320;
  color: #e0b08a;
  font-size: 0.62rem;
  font-weight: 500;
  pointer-events: none;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.4);
}
.tpb-hint-enter-active, .tpb-hint-leave-active { transition: opacity 0.2s ease, transform 0.2s ease; }
.tpb-hint-enter-from, .tpb-hint-leave-to { opacity: 0; transform: translateY(-50%) translateX(6px); }
</style>
