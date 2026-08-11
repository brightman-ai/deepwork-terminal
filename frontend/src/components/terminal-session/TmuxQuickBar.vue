<template>
  <!-- WS4 — persistent tmux quick-action row. One compact, horizontally-scrollable line
       mounted directly ABOVE the main Toolbar. Every action just sends a key sequence over the
       existing @send-key → PTY path; topology is PUSHED (WS tmux_state) so there is no per-tap
       server roundtrip. The leading `tmux:` label is itself a tap target that opens the WS8
       status sheet.

       ── 门是 `attached`，不再是 `installed` ────────────────────────────────────────────────
       曾经的门是「这台机器装了 tmux」。那让一个从不敲 tmux 的人，屏幕最下面常年顶着
       vspl / hspl / zoom / sess / detach —— 对他每一个都是死键，占掉手机最输不起的一行。
       现在这一行是二选一：在 tmux 里就是这条，不在就是 DwQuickBar。
       因此**这条 bar 内部不再判 attached**：门已经保证了它为真。

       那个「未 attach 时置顶的醒目 attach 按钮」也随之删掉了 —— 门改成 attached 之后它永远
       不可能出现。它没有消失，是搬了家：搬到 DwQuickBar 的尾部当逃生口（见 dwQuickBar.ts）。
       没有那次搬家，装了 tmux 但此刻没 attach 的人就再也点不到「进 tmux」了。 -->
  <div v-if="ready && attached" class="tmux-quick-bar" data-testid="tmux-quick-bar" @mousedown.prevent>
    <button
      class="tqb-tag is-attached"
      data-testid="tmux-quick-tag"
      title="tmux status"
      @click="$emit('openSheet')"
    >
      <span class="tqb-tag-text">tmux:</span>
    </button>

    <button class="tqb-btn" data-testid="tmux-quick-cp" :title="`${pfxLabel} [ copy mode`" @click="send(tmux.prefixSeq('['))">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1" />
      </svg>
      <span class="tqb-cap">cp</span>
    </button>
    <button class="tqb-btn tqb-btn--scroll" data-testid="tmux-quick-pgup" title="Page Up" @click="send('\x1b[5~')"><span class="tqb-cap">PgUp</span></button>
    <button class="tqb-btn tqb-btn--scroll" data-testid="tmux-quick-pgdn" title="Page Down" @click="send('\x1b[6~')"><span class="tqb-cap">PgDn</span></button>
    <!-- Half-page up/down — a STABLE half-screen scroll, buffer-aware. Routes through the
         surface's onSendKey (the sentinel below): in a fullscreen TUI (alt screen, e.g. claude-code)
         it scrolls the app itself a fixed half-screen via forwarded mouse-wheel; in the normal
         buffer it runs tmux copy-mode half-page (server-side, reaching tmux's full scrollback). -->
    <button
      class="tqb-btn tqb-btn--scroll"
      data-testid="tmux-quick-halfpgup"
      title="Half Page Up"
      @click="send('dw:scroll-half-up')"
    ><span class="tqb-cap">½↑</span></button>
    <button
      class="tqb-btn tqb-btn--scroll"
      data-testid="tmux-quick-halfpgdn"
      title="Half Page Down"
      @click="send('dw:scroll-half-down')"
    ><span class="tqb-cap">½↓</span></button>
    <button class="tqb-btn tqb-btn--danger" data-testid="tmux-quick-ctrlc" title="Ctrl+C" @click="send('\x03')"><span class="tqb-cap">^C</span></button>
    <button class="tqb-btn" data-testid="tmux-quick-up" title="Arrow Up" @click="send('\x1b[A')"><span class="tqb-glyph">↑</span></button>
    <button class="tqb-btn" data-testid="tmux-quick-down" title="Arrow Down" @click="send('\x1b[B')"><span class="tqb-glyph">↓</span></button>
    <button class="tqb-btn tqb-btn--enter" data-testid="tmux-quick-enter" title="Enter" @click="send('\r')">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="9,10 4,15 9,20"/><path d="M20 4v7a4 4 0 01-4 4H4"/>
      </svg>
    </button>
    <button class="tqb-btn" data-testid="tmux-quick-space" title="Space (copy-mode select)" @click="send(' ')"><span class="tqb-cap">SpC</span></button>
    <button class="tqb-btn" data-testid="tmux-quick-bksp" title="Backspace" @click="send('\x7f')"><span class="tqb-glyph">⌫</span></button>

    <span class="tqb-sep" />

    <!-- vsplit: split left/right when single pane, else cycle panes -->
    <button class="tqb-btn" data-testid="tmux-quick-vsplit" :title="vsplitTitle" @click="send(tmux.prefixSeq(vsplitSuffix))">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="12" y1="3" x2="12" y2="21" />
      </svg>
      <span class="tqb-cap">{{ multiPane ? 'next' : 'vspl' }}</span>
    </button>
    <!-- hsplit: split top/bottom when single pane, else cycle panes -->
    <button class="tqb-btn" data-testid="tmux-quick-hsplit" :title="hsplitTitle" @click="send(tmux.prefixSeq(hsplitSuffix))">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="3" y1="12" x2="21" y2="12" />
      </svg>
      <span class="tqb-cap">{{ multiPane ? 'next' : 'hspl' }}</span>
    </button>
    <button class="tqb-btn" data-testid="tmux-quick-zoom" :title="`${pfxLabel} z zoom toggle`" @click="send(tmux.prefixSeq('z'))">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="15,3 21,3 21,9" /><polyline points="9,21 3,21 3,15" /><line x1="21" y1="3" x2="14" y2="10" /><line x1="3" y1="21" x2="10" y2="14" />
      </svg>
      <span class="tqb-cap">zoom</span>
    </button>
    <!-- New tmux session — create + switch the client onto it (server-side; keystroke
         new-session is unreliable and refuses to nest). Placed before the session switcher
         so "新会话 → sess" reads as create-then-pick. -->
    <button
      class="tqb-btn tqb-btn--new"
      data-testid="tmux-quick-newsession"
      title="新建 tmux 会话并切换"
      @click="tmux.newSession()"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="4" width="18" height="4" rx="1" /><rect x="3" y="10" width="11" height="4" rx="1" /><rect x="3" y="16" width="11" height="4" rx="1" /><line x1="19" y1="14" x2="19" y2="22" /><line x1="15" y1="18" x2="23" y2="18" />
      </svg>
      <span class="tqb-cap">新会话</span>
    </button>
    <button class="tqb-btn" data-testid="tmux-quick-sessions" :title="`${pfxLabel} s sessions`" @click="send(tmux.prefixSeq('s'))">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="4" width="18" height="4" rx="1" /><rect x="3" y="10" width="18" height="4" rx="1" /><rect x="3" y="16" width="18" height="4" rx="1" />
      </svg>
      <span class="tqb-cap">sess</span>
    </button>
    <button class="tqb-btn" data-testid="tmux-quick-detach" :title="`${pfxLabel} d detach`" @click="send(tmux.prefixSeq('d'))">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" /><polyline points="16,17 21,12 16,7" /><line x1="21" y1="12" x2="9" y2="12" />
      </svg>
      <span class="tqb-cap">detach</span>
    </button>

    <!-- attach 收尾（低强调的「重新 attach」意符）。这条 bar 只在已经 attach 时出现，所以它
         唯一的用处是 tmux 客户端掉了之后再连回去。 -->
    <button
      class="tqb-btn"
      data-testid="tmux-quick-attach"
      title="tmux attach"
      @click="send('tmux attach\r')"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M15 3h4a2 2 0 012 2v14a2 2 0 01-2 2h-4" /><polyline points="10,17 15,12 10,7" /><line x1="15" y1="12" x2="3" y2="12" />
      </svg>
      <span class="tqb-cap">attach</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'

const props = defineProps<{ sessionId: string }>()

const emit = defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'openSheet'): void
}>()

const tmux = useTmuxState(() => props.sessionId)
const ready = tmux.ready
// `installed`（这台机器装没装 tmux）刻意不再读：它曾经是这条 bar 的门，而那正是「不用 tmux 的人
// 也常年顶着一排 tmux 死键」的根。现在唯一还需要它的是 DwQuickBar 的 attach 逃生口。
const attached = tmux.attached

/** Caption matching the live tmux prefix: "^B" for C-b, "^A" for C-a, else display. */
const pfxLabel = computed(() => {
  const d = tmux.prefixDisplay.value
  if (d === 'C-b') return '^B'
  if (d === 'C-a') return '^A'
  return d
})

/** Active window pane count drives the split/cycle state machine. */
const paneCount = computed(() => {
  const ws = tmux.windows.value
  const w = ws.find(x => x.active) ?? ws[0]
  return w?.panes?.length ?? 0
})
const multiPane = computed(() => paneCount.value >= 2)

// vsplit: single pane → split L/R ('%'); already split → cycle panes ('o').
const vsplitSuffix = computed(() => (multiPane.value ? 'o' : '%'))
// hsplit: single pane → split T/B ('"'); already split → cycle panes ('o').
const hsplitSuffix = computed(() => (multiPane.value ? 'o' : '"'))
const vsplitTitle = computed(() => (multiPane.value ? `${pfxLabel.value} o next pane` : `${pfxLabel.value} % split L/R`))
const hsplitTitle = computed(() => (multiPane.value ? `${pfxLabel.value} o next pane` : `${pfxLabel.value} " split T/B`))

function send(key: string): void {
  emit('sendKey', key)
}
</script>

<style scoped>
.tmux-quick-bar {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 3px 6px;
  background: #150f20;
  border-top: 1px solid #2a1f3a;
  overflow-x: auto;
  overflow-y: hidden;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
  scrollbar-width: none;
  user-select: none;
  -webkit-user-select: none;
}
.tmux-quick-bar::-webkit-scrollbar { display: none; }

/* Leading tap target → opens the WS8 status sheet. */
.tqb-tag {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding: 0 9px;
  background: #221636;
  color: #c8a0e8;
  border: 1px solid #4a2a7a;
  border-bottom: 2px solid #2a1050;
  border-radius: 6px;
  font-size: 0.7rem;
  font-weight: 600;
  cursor: pointer;
  touch-action: manipulation;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
}
.tqb-tag:active { background: #30205a; transform: translateY(1px) scale(0.97); border-bottom-width: 1px; }
.tqb-tag.is-attached { border-color: #7a4ab0; background: #2c1c48; color: #f0e0ff; }
.tqb-tag-text { letter-spacing: 0.3px; }

/* Action buttons — compact icon + micro-caption hybrid. */
.tqb-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-width: 36px;
  height: 30px;
  padding: 0 8px;
  background: #1c1430;
  color: #b08fd0;
  border: 1px solid #3a2860;
  border-bottom: 2px solid #1f1040;
  border-radius: 6px;
  font-size: 0.66rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
}
.tqb-btn:active { background: #3a2860; transform: translateY(1px) scale(0.93); border-bottom-width: 1px; }
.tqb-cap { font-size: 0.62rem; line-height: 1; }
.tqb-glyph { font-size: 0.85rem; line-height: 1; }

/* Scroll buttons (cp / PgUp / PgDn / ½↑ / ½↓) — the most-used one-handed targets. Wider hit area,
   brighter resting tint and a stronger press feedback than the dense action row (REQ-SCROLL-04). */
.tqb-btn--scroll {
  min-width: 50px;
  padding: 0 12px;
  color: #d6b8f4;
  border-color: #4c3676;
}
.tqb-btn--scroll .tqb-cap { font-size: 0.7rem; font-weight: 600; }
.tqb-btn--scroll:active { background: #4a3284; transform: translateY(1px) scale(0.9); }

/* Ctrl-C accent — red, matching the existing toolbar danger idiom. */
.tqb-btn--danger {
  color: #ff8080;
  border-color: #5a2030;
  border-bottom-color: #2a1020;
  background: #2a1020;
}
.tqb-btn--danger:active { background: #3a1828; }

/* Enter — same green accent as the main Toolbar's Enter (.tb-btn--enter), so the two bars'
   Enter keys read as one consistent affordance. */
.tqb-btn--enter {
  color: #80c880;
  border-color: #2a4a2a;
  border-bottom-color: #142a14;
  background: #0e1e0e;
}
.tqb-btn--enter:active { background: #16321a; }

/* attach prominent (green) when detached. */
.tqb-btn--attach {
  color: #60d890;
  border-color: #104828;
  border-bottom-color: #082012;
  background: #0a2418;
}
.tqb-btn--attach:active { background: #0e2e20; }

/* New-session: a calm purple accent (a create action, distinct from attach-green). */
.tqb-btn--new {
  color: #c080ff;
  border-color: #3a2860;
  border-bottom-color: #241640;
  background: #1a1030;
}
.tqb-btn--new:active { background: #241640; }

.tqb-sep {
  flex-shrink: 0;
  width: 1px;
  height: 18px;
  background: #3a2860;
  margin: 0 2px;
  opacity: 0.6;
}

/* Right-edge scroll fade hint. */
.tmux-quick-bar { position: relative; }
.tmux-quick-bar::after {
  content: '';
  position: sticky;
  right: 0;
  flex-shrink: 0;
  width: 16px;
  height: 100%;
  background: linear-gradient(to right, transparent, #150f20);
  pointer-events: none;
}
</style>
