<template>
  <!-- Compact single-row assist bar (input-first redesign, coexists with the classic bars behind
       a toggle). One horizontally-scrollable HIGH-frequency row + a `⋯` overflow drawer for the
       long tail. Re-emits the SAME events as Toolbar so the parent's existing handlers are reused
       verbatim; only stickyCtrl/Shift/Alt + activePanel come in as props for active-state render. -->
  <div class="compact-assist">
    <div class="ca-hot" @mousedown.prevent>
      <button
        v-for="k in HOT_KEYS"
        :key="k.label"
        class="ca-key"
        :class="{ 'ca-key--active': isActive(k), 'ca-key--danger': k.danger }"
        @click="onKey(k)"
      >{{ k.label }}</button>
      <button
        class="ca-key ca-key--more"
        :class="{ 'ca-key--active': drawerOpen }"
        title="更多按键"
        @click="drawerOpen = !drawerOpen"
      >⋯</button>
    </div>

    <div v-if="drawerOpen" class="ca-drawer">
      <div v-for="g in DRAWER_GROUPS" :key="g.title" class="ca-group">
        <span class="ca-group-title">{{ g.title }}</span>
        <div class="ca-group-keys">
          <button
            v-for="k in g.keys"
            :key="k.label"
            class="ca-key"
            :class="{ 'ca-key--active': isActive(k) }"
            @click="onKey(k)"
          >{{ k.label }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

// A key is declared by WHAT it does; onKey() dispatches. Adding a key = one array entry (SSOT,
// no per-button template duplication). Payloads/emit names are byte-for-byte the same as Toolbar/
// TmuxQuickBar/KeyboardPanel so the parent handlers (onSendKey/onClipboard/onTogglePanel…) apply.
interface AKey {
  label: string
  send?: string // sendKey payload (control byte / CSI seq / dw:scroll sentinel)
  toggle?: 'shift' | 'ctrl' | 'alt' // sticky modifier — emits toggle*, NEVER a byte
  panel?: 'numpad' | 'compose' // mode toggle (tri-state via activePanel)
  action?: 'keyboard' | 'attach'
  clipboard?: string // browser Clipboard-API op
  danger?: boolean
}

const props = defineProps<{
  stickyShift?: boolean
  stickyCtrl?: boolean
  stickyAlt?: boolean
  activePanel?: 'none' | 'numpad' | 'compose'
  keyboardUp?: boolean
}>()

const emit = defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'clipboard', op: string): void
  (e: 'toggleShift'): void
  (e: 'toggleCtrl'): void
  (e: 'toggleAlt'): void
  (e: 'toggleNumpad'): void
  (e: 'toggleCompose'): void
  (e: 'toggleKeyboard'): void
  (e: 'attach'): void
}>()

const drawerOpen = ref(false)

// HIGH-freq single row — the edit/nav keys a mobile terminal user hits constantly.
const HOT_KEYS: AKey[] = [
  { label: 'Esc', send: '\x1b' },
  { label: 'Ctrl', toggle: 'ctrl' },
  { label: 'Tab', send: '\t' },
  { label: '↑', send: '\x1b[A' },
  { label: '↓', send: '\x1b[B' },
  { label: '←', send: '\x1b[D' },
  { label: '→', send: '\x1b[C' },
  { label: '^C', send: '\x03', danger: true },
  { label: '⌫', send: '\x7f' },
  { label: '⏎', send: '\r' },
]

// LOW-freq long tail, grouped, behind `⋯`.
const DRAWER_GROUPS: { title: string; keys: AKey[] }[] = [
  { title: '修饰', keys: [{ label: 'Shift', toggle: 'shift' }, { label: 'Alt', toggle: 'alt' }] },
  {
    title: '导航',
    keys: [
      { label: 'Home', send: '\x1b[H' },
      { label: 'End', send: '\x1b[F' },
      { label: 'PgU', send: '\x1b[5~' },
      { label: 'PgD', send: '\x1b[6~' },
      { label: 'Del', send: '\x1b[3~' },
    ],
  },
  { title: '滚动', keys: [{ label: '½↑', send: 'dw:scroll-half-up' }, { label: '½↓', send: 'dw:scroll-half-down' }] },
  {
    title: '面板',
    keys: [
      { label: '数字盘', panel: 'numpad' },
      { label: '输入框', panel: 'compose' },
      { label: '系统键盘', action: 'keyboard' },
    ],
  },
  { title: '其它', keys: [{ label: '粘贴', clipboard: 'paste' }, { label: '附件', action: 'attach' }] },
]

function isActive(k: AKey): boolean {
  if (k.toggle === 'ctrl') return !!props.stickyCtrl
  if (k.toggle === 'shift') return !!props.stickyShift
  if (k.toggle === 'alt') return !!props.stickyAlt
  if (k.panel) return props.activePanel === k.panel
  if (k.action === 'keyboard') return props.activePanel === 'none' && !!props.keyboardUp
  return false
}

function onKey(k: AKey): void {
  if (k.send !== undefined) return emit('sendKey', k.send)
  if (k.clipboard) return emit('clipboard', k.clipboard)
  if (k.toggle === 'shift') return emit('toggleShift')
  if (k.toggle === 'ctrl') return emit('toggleCtrl')
  if (k.toggle === 'alt') return emit('toggleAlt')
  if (k.panel === 'numpad') { emit('toggleNumpad'); drawerOpen.value = false; return }
  if (k.panel === 'compose') { emit('toggleCompose'); drawerOpen.value = false; return }
  if (k.action === 'keyboard') return emit('toggleKeyboard')
  if (k.action === 'attach') { emit('attach'); drawerOpen.value = false; return }
}
</script>

<style scoped>
.compact-assist {
  background: #1a1a2e;
  border-top: 1px solid #2a2a45;
}
.ca-hot {
  display: flex;
  gap: 5px;
  padding: 6px 8px;
  overflow-x: auto;
  scrollbar-width: none;
  -webkit-overflow-scrolling: touch;
}
.ca-hot::-webkit-scrollbar {
  display: none;
}
.ca-key {
  flex: 0 0 auto;
  min-width: 42px;
  height: 38px;
  padding: 0 10px;
  border: 1px solid #33335a;
  border-radius: 8px;
  background: #23233f;
  color: #d0d0e8;
  font-size: 0.9rem;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  display: flex;
  align-items: center;
  justify-content: center;
  touch-action: manipulation;
  transition: background 0.1s, border-color 0.1s;
}
.ca-key:active {
  background: #33335f;
}
.ca-key--active {
  background: #2f5fd0;
  border-color: #4a80d8;
  color: #fff;
}
.ca-key--danger {
  color: #ff8a8a;
  border-color: #5a2a3a;
}
.ca-key--more {
  min-width: 44px;
  font-weight: bold;
}
.ca-drawer {
  padding: 4px 8px 8px;
  border-top: 1px solid #2a2a45;
  max-height: 40vh;
  overflow-y: auto;
}
.ca-group {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 4px 0;
}
.ca-group-title {
  flex: 0 0 40px;
  font-size: 0.7rem;
  color: #8080a8;
  line-height: 34px;
}
.ca-group-keys {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}
.ca-drawer .ca-key {
  min-width: 44px;
  height: 34px;
  font-size: 0.82rem;
}
</style>
