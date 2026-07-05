<template>
  <!-- One compact line of tmux window tabs that REPLACES the per-session status row
       (终端 N idle) whenever THIS session's shell is attached to tmux. It mounts in the
       header/status row — OUTSIDE the terminal body — so its taps never reach the
       terminal's copy-mode touch handlers or the floating touchball. Gated on `attached`
       (THIS shell is inside a tmux client), never on the machine-global serverRunning.
       Topology is PUSHED (WS tmux_state); each tap just sends prefix+digit (select-window)
       or prefix+c (new-window) — no server roundtrip to learn the layout. -->
  <div v-if="ready && attached && windows.length" class="tmux-pane-bar" data-testid="tmux-pane-bar">
    <!-- Agent Overview toggle — LEADING + sticky-left so it stays reachable on mobile without
         scrolling past the window list. Attention badge (red dot) when any pane needs you. -->
    <button
      class="tpb-overview tpb-overview--lead"
      :class="{ on: overviewOpen }"
      type="button"
      title="Agent 概览（同屏看全部）"
      aria-label="Agent 概览"
      data-testid="tmux-overview-toggle"
      @click="emit('toggle-overview')"
      @pointerup.stop
      @touchend.stop
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" />
      </svg>
      <!-- Merged global roll-up (方向 Y 统一状态区): the glance count lives IN the toggle capsule,
           pinned at the bar's left edge so it's always visible without scrolling past the window
           list. Hidden while the overview is open (the grid renders the full line then). -->
      <span v-if="rollupSegs.length && !overviewOpen" class="tpb-caps-rollup" data-testid="tmux-rollup">
        <span v-for="s in rollupSegs" :key="s.k" class="tpb-seg" :class="s.cls">{{ s.icon }}{{ s.n }}</span>
      </span>
      <!-- Fallback attention dot only when there's no roll-up data to render ◉N inline. -->
      <span v-if="anyWaiting && !overviewOpen && !rollupSegs.length" class="tpb-overview-badge" />
    </button>
    <button
      v-for="w in windows"
      :key="w.index"
      class="tpb-win"
      :class="{ 'is-active': w.active }"
      :data-testid="`tmux-win-${w.index}`"
      @click="onWinClick(w, $event)"
      @mouseenter="onWinHover(w, $event)"
      @mouseleave="hideTip"
      @touchstart.passive="touched = true"
      @pointerup.stop
      @touchend.stop
    >
      <span class="tpb-idx">{{ w.index }}</span>
      <span v-if="dotClass(w)" class="tpb-dot" :class="dotClass(w)" />
    </button>
    <button
      class="tpb-win tpb-add"
      data-testid="tmux-win-add"
      @click="newWindow"
      @pointerup.stop
      @touchend.stop
    >+</button>

    <!-- WS7 secondary entry — contextual notify bell. Pushed to the right; badged when
         notifications are off, and a one-time inline hint pops near it the first time any pane
         enters `waiting` while unsubscribed. Opens the shared guide. The connection heartbeat is
         NOT here — it is pinned (non-scrolling) in the surface status row so a long window list
         can't push it off-screen. -->
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
        @pointerup.stop
        @touchend.stop
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        <span v-if="bellNudge" class="tpb-bell-dot" />
      </button>
    </div>
  </div>

  <!-- cwd/status tip — teleported out of the clipping bar, fixed under the button. -->
  <Teleport to="body">
    <div
      v-if="tipWin !== null && tipCwd"
      class="tpb-tip"
      :style="{ left: tipPos.left + 'px', top: tipPos.top + 'px' }"
      data-testid="tmux-win-tip"
    >
      <div class="tpb-tip-row"><span class="tpb-tip-k">目录</span><span class="tpb-tip-v tpb-tip-cwd">{{ tipCwd }}</span></div>
      <div class="tpb-tip-row"><span class="tpb-tip-k">状态</span><span class="tpb-tip-v">{{ tipStatus }}</span></div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { TmuxWindowState } from '@terminal/types/terminal'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { usePushNotifications } from '@terminal/composables/cli/usePushNotifications'
import { windowCwd, windowRawStatus, type EffectiveStatus } from '@terminal/composables/cli/useAgentOverview'

const props = defineProps<{
  sessionId: string
  /** Agent Overview is open — the toggle reflects it (the overview state itself is owned by the
   *  surface, the SSOT for both the bar's roll-up/badge and the overview grid). */
  overviewOpen?: boolean
  /** Global status counts from the shared useAgentOverview (single source; the bar renders a
   *  compact glance, the overview grid the full line). Omit → no roll-up shown. */
  rollup?: Record<EffectiveStatus, number>
  /** index→effectiveStatus from the shared useAgentOverview, so each window's dot is seen-aware
   *  (incl. done-unseen teal) from the SAME source as the overview. Omit → falls back to raw. */
  statusByIndex?: Record<number, EffectiveStatus>
}>()

const emit = defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'open-notify'): void
  (e: 'toggle-overview'): void
}>()

// Compact roll-up: only non-zero, urgency-ordered (idle omitted — not actionable at a glance).
const rollupSegs = computed(() => {
  const r = props.rollup
  if (!r) return []
  const defs: Array<{ k: EffectiveStatus; icon: string; cls: string }> = [
    { k: 'waiting', icon: '◉', cls: 'seg-waiting' },
    { k: 'running', icon: '●', cls: 'seg-running' },
    { k: 'done-unseen', icon: '✓', cls: 'seg-done' },
  ]
  return defs.filter((d) => (r[d.k] ?? 0) > 0).map((d) => ({ ...d, n: r[d.k] }))
})

const tmux = useTmuxState(() => props.sessionId)
const ready = tmux.ready
const attached = tmux.attached
const windows = tmux.windows
const push = usePushNotifications()

// Bell nudges (amber dot) whenever notifications aren't on and aren't hard-denied.
const bellNudge = computed(() =>
  push.permission.value !== 'denied' && !push.subscribed.value,
)

const anyWaiting = computed(() =>
  windows.value.some(w => windowRawStatus(w) === 'waiting'),
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

/** Seen-aware dot: waiting→red, running→green, done-unseen→teal, idle→none. Uses the shared
 *  effectiveStatus (SSOT) when provided, else falls back to raw pane status. */
function dotClass(w: TmuxWindowState): string {
  const s = props.statusByIndex?.[w.index] ?? windowRawStatus(w)
  if (s === 'waiting') return 'tpb-dot--waiting'
  if (s === 'running') return 'tpb-dot--running'
  if (s === 'done-unseen') return 'tpb-dot--done'
  return ''
}

function selectWindow(index: number): void {
  emit('sendKey', tmux.selectWindowSeq(index)) // this-client-scoped, valid for any index
}

function newWindow(): void {
  // tmux: prefix + c = new-window
  emit('sendKey', tmux.prefixSeq('c'))
}

// ── Per-window cwd/status tip (mirrors the terminal tab tip) ──────────────────
// The bar clips (overflow: auto), so the tip is teleported to <body> and fixed-positioned
// under the button. Data is already pushed (tmux_state): the window's cwd is its ACTIVE
// pane's pane_current_path. SWITCHING stays the primary action — the tip never blocks it:
// desktop hovers it; a touch tap switches AND flashes it for ~2s (auto-dismiss).
// Tip content reuses the overview's SSOT helpers (windowCwd/windowRawStatus) — no second
// cwd/status derivation lives here.
function winStatusLabel(w: TmuxWindowState): string {
  const raw = windowRawStatus(w)
  return raw === 'waiting' ? '等待输入' : raw === 'running' ? '运行中' : '空闲'
}

const tipWin = ref<number | null>(null)
const tipPos = ref({ left: 0, top: 0 })
let touched = false // set on touchstart so we can tell a tap (auto-dismiss) from a hover
let tipTimer: ReturnType<typeof setTimeout> | null = null

const tipWindow = computed(() => windows.value.find(w => w.index === tipWin.value) ?? null)
const tipCwd = computed(() => (tipWindow.value ? windowCwd(tipWindow.value) : ''))
const tipStatus = computed(() => (tipWindow.value ? winStatusLabel(tipWindow.value) : ''))

function showTip(w: TmuxWindowState, el: HTMLElement): void {
  if (!windowCwd(w)) return // no cwd yet → don't pop an empty tip
  const r = el.getBoundingClientRect()
  const width = 240
  tipPos.value = { left: Math.max(8, Math.min(r.left, window.innerWidth - width - 8)), top: r.bottom + 6 }
  tipWin.value = w.index
}
function hideTip(): void {
  tipWin.value = null
  if (tipTimer) { clearTimeout(tipTimer); tipTimer = null }
}
function onWinHover(w: TmuxWindowState, e: MouseEvent): void {
  if (touched) return // a touch tap already handles its own transient tip
  showTip(w, e.currentTarget as HTMLElement)
}
function onWinClick(w: TmuxWindowState, e: MouseEvent): void {
  selectWindow(w.index) // PRIMARY — switch, always, first
  if (touched) {
    showTip(w, e.currentTarget as HTMLElement) // touch: flash the tip, then auto-dismiss
    if (tipTimer) clearTimeout(tipTimer)
    tipTimer = setTimeout(hideTip, 2000)
    touched = false
  }
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
.tpb-dot--waiting { background: #ff5252; }   /* red — needs your input */
.tpb-dot--running { background: #3fb950; }   /* green — agent actively running (incl. thinking) */
.tpb-dot--done { background: #2dd4bf; }       /* teal — finished while you were elsewhere (unseen) */

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

/* Global roll-up (compact glance) merged INTO the leading Agent Overview capsule (方向 Y). */
.tpb-caps-rollup { display: inline-flex; align-items: center; gap: 5px; font-size: 0.62rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.tpb-seg { color: #6f5a90; }
.tpb-seg.seg-waiting { color: #ff5252; }
.tpb-seg.seg-running { color: #3fb950; }
.tpb-seg.seg-done { color: #2dd4bf; }
.tpb-overview {
  position: relative; flex-shrink: 0;
  display: inline-grid; place-items: center;
  width: 26px; height: 26px; border-radius: 5px;
  background: transparent; border: 1px solid transparent; color: #6f5a90;
  cursor: pointer; touch-action: manipulation;
  transition: color 0.1s, background 0.1s, border-color 0.1s;
}
.tpb-overview:hover { color: #b08fd0; }
.tpb-overview:active { transform: scale(0.92); }
.tpb-overview.on { color: #f0e0ff; background: #4a2a7a; border-color: #7a4ab0; }
/* Leading status capsule (方向 Y): overview toggle + roll-up count, one unit pinned at the left
   edge while the window strip scrolls (mobile reach). Auto-width so the ◉N●N✓N segs fit inline;
   collapses back to a 26px square when there's nothing to count. */
.tpb-overview--lead {
  position: sticky; left: 0; z-index: 2; margin-right: 2px; background: #16121f;
  display: inline-flex; align-items: center; gap: 5px; width: auto; min-width: 26px; padding: 0 7px;
}
.tpb-overview--lead.on { background: #4a2a7a; }
.tpb-overview-badge {
  position: absolute; top: 3px; right: 3px;
  width: 6px; height: 6px; border-radius: 50%;
  background: #ff5252; box-shadow: 0 0 0 2px #16121f;
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

/* Per-window cwd/status tip. Teleported to <body> to escape the bar's overflow clip;
   Vue keeps the scoped data-attr on teleported nodes, so these scoped rules still apply. */
.tpb-tip {
  position: fixed;
  z-index: 4000;
  max-width: 240px;
  padding: 7px 10px;
  border-radius: 8px;
  background: #1a1526;
  border: 1px solid #3a2860;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  font-size: 0.66rem;
  line-height: 1.5;
  pointer-events: none;
}
.tpb-tip-row { display: flex; gap: 8px; }
.tpb-tip-k { flex-shrink: 0; color: #6f5a90; }
.tpb-tip-v { color: #d8c8ee; min-width: 0; }
.tpb-tip-cwd {
  font-family: var(--dw-mono, ui-monospace, monospace);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
</style>
