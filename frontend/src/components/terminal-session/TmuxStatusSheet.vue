<template>
  <!-- WS8 — tmux status sheet. Opened by tapping the `tmux:` label in TmuxQuickBar.
       Pure read model over the PUSHED tmux_state (no roundtrip): walks sessions → windows
       → panes to render prefix, per-session topology and an agent rollup. Mobile renders as
       a bottom sheet; desktop as an anchored popover. Dismissible via tap-out / close. -->
  <Teleport to="body">
    <Transition name="tss-fade">
      <div
        v-if="open"
        class="tss-scrim"
        :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
        data-testid="tmux-status-sheet"
        @click.self="$emit('close')"
      >
        <div class="tss-panel" @mousedown.prevent>
          <div class="tss-header">
            <span class="tss-title">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#c080ff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="3" y="3" width="8" height="8" rx="1" /><rect x="13" y="3" width="8" height="8" rx="1" />
                <rect x="3" y="13" width="8" height="8" rx="1" /><rect x="13" y="13" width="8" height="8" rx="1" />
              </svg>
              tmux
            </span>
            <span class="tss-prefix mono">prefix <b>{{ prefixDisplay }}</b></span>
            <button class="tss-close" title="Close" @click="$emit('close')">&times;</button>
          </div>

          <div class="tss-body">
            <!-- Top stat strip -->
            <div class="tss-stats">
              <div class="tss-stat">
                <span class="tss-stat-num">{{ sessions.length }}</span>
                <span class="tss-stat-lbl">sessions</span>
              </div>
              <div class="tss-stat">
                <span class="tss-stat-num">{{ totalWindows }}</span>
                <span class="tss-stat-lbl">windows</span>
              </div>
              <div class="tss-stat">
                <span class="tss-stat-num">{{ totalPanes }}</span>
                <span class="tss-stat-lbl">panes</span>
              </div>
              <div class="tss-stat" :class="{ 'is-alert': waitingCount > 0 }">
                <span class="tss-stat-num">{{ waitingCount }}</span>
                <span class="tss-stat-lbl">waiting</span>
              </div>
            </div>

            <!-- Agent rollup -->
            <div v-if="agentRollup.length" class="tss-section">
              <div class="tss-section-lbl mono">agents</div>
              <div class="tss-chips">
                <span
                  v-for="a in agentRollup"
                  :key="a.tool"
                  class="tss-chip"
                  :class="{ 'is-waiting': a.waiting > 0 }"
                >
                  <span class="tss-chip-dot" :class="a.waiting > 0 ? 'wait' : 'busy'" />
                  <span class="tss-chip-name mono">{{ a.tool }}</span>
                  <span class="tss-chip-count">{{ a.count }}</span>
                  <span v-if="a.waiting > 0" class="tss-chip-wait mono">{{ a.waiting }} waiting</span>
                </span>
              </div>
            </div>

            <!-- Per-session topology -->
            <div class="tss-section">
              <div class="tss-section-lbl mono">topology</div>
              <div v-if="!sessions.length" class="tss-empty">no tmux server running</div>
              <div v-for="s in sessions" :key="s.name" class="tss-sess">
                <div class="tss-sess-head">
                  <span class="tss-sess-name mono">{{ s.name }}</span>
                  <span v-if="s.attached" class="tss-badge tss-badge--attached">attached</span>
                  <span class="tss-sess-meta mono">{{ s.windows.length }}w · {{ paneCount(s) }}p</span>
                </div>
                <div class="tss-wins">
                  <button
                    v-for="w in s.windows"
                    :key="w.index"
                    type="button"
                    class="tss-win"
                    :class="{ 'is-active': w.active }"
                    :title="`Switch to ${w.name} — ${w.panes.length} pane(s)`"
                    @click="selectWindow(w)"
                  >
                    <span class="tss-win-idx mono">{{ w.index }}</span>
                    <span class="tss-win-name">{{ w.name }}</span>
                    <span v-if="winDot(w)" class="tss-win-dot" :class="winDot(w)" />
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { TmuxSessionState, TmuxWindowState } from '@terminal/types/terminal'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'

const props = defineProps<{ sessionId: string; open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'sendKey', key: string): void }>()

const { isMobile } = useDeviceDetection()
const tmux = useTmuxState(() => props.sessionId)

const prefixDisplay = tmux.prefixDisplay

/** Tap a window chip → switch to it via the server-side select-window (POST /tmux/select-window),
 *  the same SSOT TmuxPaneBar + overview use — robust for index ≥10, no leaked keystrokes. Close
 *  the sheet for one-handed flow. */
function selectWindow(w: TmuxWindowState): void {
  void tmux.selectWindow(w.index)
  emit('close')
}
const sessions = computed<TmuxSessionState[]>(() => tmux.state.value?.sessions ?? [])

function paneCount(s: TmuxSessionState): number {
  return s.windows.reduce((acc, w) => acc + (w.panes?.length ?? 0), 0)
}

const totalWindows = computed(() => sessions.value.reduce((acc, s) => acc + s.windows.length, 0))
const totalPanes = computed(() => sessions.value.reduce((acc, s) => acc + paneCount(s), 0))

/** All panes flattened — single walk feeds both rollup and waiting count. */
const allPanes = computed(() =>
  sessions.value.flatMap(s => s.windows.flatMap(w => w.panes ?? [])),
)
const waitingCount = computed(() => allPanes.value.filter(p => p.agentStatus === 'waiting').length)

/** Per-tool rollup: count of panes running each agent tool, + how many are waiting.
 *  Unknown / future tools render generically (we never hardcode the tool list). */
const agentRollup = computed(() => {
  const map = new Map<string, { tool: string; count: number; waiting: number }>()
  for (const p of allPanes.value) {
    const tool = p.agentTool
    if (!tool) continue
    const e = map.get(tool) ?? { tool, count: 0, waiting: 0 }
    e.count++
    if (p.agentStatus === 'waiting') e.waiting++
    map.set(tool, e)
  }
  // waiting tools first, then by count desc
  return [...map.values()].sort((a, b) => (b.waiting - a.waiting) || (b.count - a.count))
})

/** Window status dot: any waiting pane → red; else any running → dim; else none. */
function winDot(w: TmuxWindowState): string {
  const panes = w.panes ?? []
  if (panes.some(p => p.agentStatus === 'waiting')) return 'wait'
  if (panes.some(p => p.agentStatus === 'running')) return 'busy'
  return ''
}
</script>

<style scoped>
.tss-scrim {
  position: fixed;
  inset: 0;
  z-index: 300;
  background: rgba(8, 6, 14, 0.55);
  display: flex;
}
.tss-scrim.is-mobile { align-items: flex-end; justify-content: stretch; }
.tss-scrim.is-desktop { align-items: flex-end; justify-content: flex-start; }

.tss-panel {
  display: flex;
  flex-direction: column;
  background: #160f22;
  border: 1px solid #3a2860;
  color: #e0d4f0;
  font-size: 0.78rem;
  box-shadow: 0 -8px 40px rgba(0, 0, 0, 0.6);
  overflow: hidden;
  user-select: none;
  -webkit-user-select: none;
}
.is-mobile .tss-panel {
  width: 100%;
  max-height: 72vh;
  border-radius: 14px 14px 0 0;
  border-bottom: none;
  padding-bottom: env(safe-area-inset-bottom, 0px);
}
.is-desktop .tss-panel {
  width: 360px;
  max-height: 70vh;
  margin: 0 0 56px 12px;
  border-radius: 12px;
}

.mono { font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', monospace; }

.tss-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: rgba(192, 128, 255, 0.06);
  border-bottom: 1px solid #2a1f3a;
  flex-shrink: 0;
}
.tss-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: #c080ff;
  font-size: 0.82rem;
  letter-spacing: 0.4px;
}
.tss-prefix { flex: 1; color: #7a6a9a; font-size: 0.68rem; }
.tss-prefix b { color: #b08fd0; font-weight: 600; }
.tss-close {
  background: none; border: none; color: #6f5a90; cursor: pointer;
  font-size: 1.3rem; line-height: 1; padding: 0 2px;
}
.tss-close:active { color: #ff8080; }

.tss-body {
  overflow-y: auto;
  padding: 10px 12px 14px;
  scrollbar-width: thin;
  scrollbar-color: #3a2860 transparent;
}

/* Stat strip */
.tss-stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 6px;
  margin-bottom: 12px;
}
.tss-stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1px;
  padding: 8px 4px;
  background: #1c1430;
  border: 1px solid #2e2050;
  border-radius: 8px;
}
.tss-stat.is-alert { background: #2a1018; border-color: #5a2030; }
.tss-stat-num { font-size: 1.05rem; font-weight: 700; color: #d8c4f0; font-variant-numeric: tabular-nums; }
.tss-stat.is-alert .tss-stat-num { color: #ff7070; }
.tss-stat-lbl { font-size: 0.58rem; text-transform: uppercase; letter-spacing: 0.5px; color: #6f5a90; }

/* Sections */
.tss-section { margin-bottom: 12px; }
.tss-section:last-child { margin-bottom: 0; }
.tss-section-lbl {
  font-size: 0.6rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: #6f5a90;
  margin-bottom: 6px;
}

/* Agent chips */
.tss-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.tss-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 9px;
  background: #1c1430;
  border: 1px solid #2e2050;
  border-radius: 999px;
  font-size: 0.7rem;
}
.tss-chip.is-waiting { background: #2a1018; border-color: #5a2030; }
.tss-chip-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
.tss-chip-dot.busy { background: #6a8a6a; }
.tss-chip-dot.wait { background: #ff5252; animation: tss-pulse 1.3s infinite; }
.tss-chip-name { color: #c8a0e8; font-weight: 600; }
.tss-chip-count {
  color: #9a86ba; font-variant-numeric: tabular-nums;
  background: #2a1c48; border-radius: 5px; padding: 0 5px; font-size: 0.64rem;
}
.tss-chip-wait { color: #ff8080; font-size: 0.62rem; }

/* Session topology */
.tss-sess {
  padding: 8px 9px;
  background: #1a1228;
  border: 1px solid #2a1f3a;
  border-radius: 8px;
  margin-bottom: 6px;
}
.tss-sess:last-child { margin-bottom: 0; }
.tss-sess-head { display: flex; align-items: center; gap: 7px; margin-bottom: 6px; }
.tss-sess-name { color: #d8c4f0; font-weight: 600; font-size: 0.74rem; }
.tss-sess-meta { margin-left: auto; color: #6f5a90; font-size: 0.62rem; }
.tss-badge {
  font-size: 0.56rem; text-transform: uppercase; letter-spacing: 0.4px;
  padding: 1px 6px; border-radius: 999px;
}
.tss-badge--attached { background: #1a3a28; color: #60d890; border: 1px solid #1f5238; }

.tss-wins { display: flex; flex-wrap: wrap; gap: 4px; }
.tss-win {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 9px;
  background: #221636;
  border: 1px solid #3a2860;
  border-bottom: 2px solid #1f1040;
  border-radius: 5px;
  font-size: 0.68rem;
  font-family: inherit;
  color: #b08fd0;
  cursor: pointer;
  touch-action: manipulation;
  transition: background 0.08s, transform 0.08s;
}
.tss-win:active { background: #30205a; transform: translateY(1px) scale(0.97); border-bottom-width: 1px; }
.tss-win.is-active { background: #4a2a7a; border-color: #7a4ab0; color: #f0e0ff; }
.tss-win-idx { font-weight: 700; font-size: 0.62rem; }
.tss-win-name { max-width: 92px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tss-win-dot { width: 5px; height: 5px; border-radius: 50%; flex-shrink: 0; }
.tss-win-dot.wait { background: #ff5252; }
.tss-win-dot.busy { background: #6a6a7a; }

.tss-empty { color: #5a4a78; font-style: italic; font-size: 0.7rem; padding: 4px 0; }

@keyframes tss-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }

/* Sheet enter/leave */
.tss-fade-enter-active, .tss-fade-leave-active { transition: opacity 0.18s ease; }
.tss-fade-enter-from, .tss-fade-leave-to { opacity: 0; }
.tss-fade-enter-active .tss-panel, .tss-fade-leave-active .tss-panel { transition: transform 0.2s ease; }
.tss-fade-enter-from .tss-panel, .tss-fade-leave-to .tss-panel { transform: translateY(16px); }
</style>
