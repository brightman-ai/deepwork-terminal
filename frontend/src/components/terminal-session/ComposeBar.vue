<template>
  <div class="compose-bar">
    <!-- Toolbar: cursor nav + DelAll + Snippets -->
    <div class="compose-toolbar" @mousedown.prevent>
      <button class="cb-tool cb-tool--esc" @click="$emit('close')" title="退出输入框">Esc</button>
      <div class="cb-tool-divider" />
      <button class="cb-tool" @click="moveCursor('up')" title="上移">↑</button>
      <button class="cb-tool" @click="moveCursor('down')" title="下移">↓</button>
      <button class="cb-tool" @click="moveCursor('left')" title="左移">←</button>
      <button class="cb-tool" @click="moveCursor('right')" title="右移">→</button>
      <button class="cb-tool" @click="moveCursor('home')" title="行首">Home</button>
      <button class="cb-tool" @click="moveCursor('end')" title="行尾">End</button>
      <div class="cb-tool-divider" />
      <button class="cb-tool cb-tool--copy" @click="copyAll" title="复制全部" data-testid="compose-copy-all">
        <Check v-if="copyFeedback" width="13" height="13" />
        <Copy v-else width="13" height="13" />
      </button>
      <button class="cb-tool cb-tool--danger" @click="clearAll" title="清空全部" data-testid="compose-clear-all">
        <Trash2 width="13" height="13" />
      </button>
      <button class="cb-tool cb-tool--snip" :class="{ 'cb-tool--active': showSnippets }" @click="toggleSnippets" title="快捷短语">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M16 4h2a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V6a2 2 0 012-2h2"/>
          <rect x="8" y="2" width="8" height="4" rx="1"/>
        </svg>
      </button>
      <button class="cb-tool cb-tool--hist" :class="{ 'cb-tool--active': showHistory }" @click="toggleHistory" title="发送历史">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <circle cx="12" cy="12" r="10"/>
          <polyline points="12,6 12,12 16,14"/>
        </svg>
      </button>
    </div>

    <!-- Snippet panel (toggle) -->
    <div v-if="showSnippets" class="snippet-panel">
      <div class="snippet-header">
        <span class="snippet-title">Snippets</span>
        <button class="snippet-save-btn" @click="saveSnippet" title="Save current text">+ Save</button>
      </div>
      <div v-if="snippets.length === 0" class="snippet-empty">No snippets saved</div>
      <div v-else class="snippet-list">
        <div v-for="(s, i) in snippets" :key="i" class="snippet-item" @click="insertSnippet(s)">
          <span class="snippet-text">{{ s.length > 40 ? s.slice(0, 40) + '...' : s }}</span>
          <button class="snippet-del" @click.stop="deleteSnippet(i)" title="Delete">x</button>
        </div>
      </div>
    </div>

    <!-- Undo-clear bar: one-shot affordance after 清空全部. Non-modal, own row (never
         overlaps the textarea or send button), auto-dismisses so it never lingers and
         blocks input. Once dismissed (timeout OR tapped) the cleared text is gone for good. -->
    <Transition name="cb-fade">
      <div v-if="showUndo" class="undo-bar" data-testid="compose-undo-clear-bar">
        <span class="undo-msg">已清空全部</span>
        <button class="undo-btn" type="button" @click="undoClear" data-testid="compose-undo-clear">
          <RotateCcw width="12" height="12" /> 撤销
        </button>
      </div>
    </Transition>

    <!-- History panel (auto-saved send history, separate from snippets) -->
    <div v-if="showHistory" class="history-panel">
      <div class="history-header">
        <span class="history-title">History</span>
        <button class="history-clear-btn" @click="clearHistory" title="Clear all history">Clear</button>
      </div>
      <div v-if="history.length === 0" class="history-empty">No history yet</div>
      <div v-else class="history-list">
        <div v-for="(h, i) in history" :key="i" class="history-item" @click="insertFromHistory(h)">
          <span class="history-text">{{ h.length > 50 ? h.slice(0, 50) + '...' : h }}</span>
        </div>
      </div>
    </div>

    <!-- Input row -->
    <div class="compose-input-row">
      <textarea
        ref="textareaRef"
        v-model="text"
        class="compose-input"
        placeholder="Enter = newline, tap send to submit"
        @input="onInput"
        @focus="onTextareaFocus"
      />
      <!-- Send: submit compose text to terminal -->
      <button class="btn-send" @click="send" @touchend.stop.prevent="send" title="Send text">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="22" y1="2" x2="11" y2="13"/><polygon points="22,2 15,22 11,13 2,9"/>
        </svg>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * ComposeBar — Native textarea for composing multi-line input on mobile.
 * Enter = newline. Send button = submit. Draft persisted to localStorage.
 * Snippets stored in localStorage for quick-recall.
 */
import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { Copy, Check, Trash2, RotateCcw } from 'lucide-vue-next'
import { focusWithoutViewportScroll, resetViewportScroll } from '@terminal/composables/cli/useVisualKeyboardInset'
import {
  attachCliInputDiagnostics,
  reportCliInputDiagnostic,
  summarizeText,
} from '@terminal/composables/cli/useCliInputDiagnostics'
import { useServerStore } from '@terminal/composables/cli/useServerStore'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const DRAFT_KEY = 'cli-compose-draft'
const HISTORY_MAX = 15
const serverStore = useServerStore()

/**
 * `draft` lets the host pre-fill the textarea for editing (e.g. ResourceDrawer's
 * 重发 inserts a past prompt here instead of sending it directly). It is applied
 * on mount and whenever it changes to a fresh value, REPLACING the current text so
 * the user lands in an editable compose with the inserted text ready to refine.
 */
const props = defineProps<{ draft?: string }>()

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'close'): void
}>()

const text = ref('')
const textareaRef = ref<HTMLTextAreaElement>()
const showSnippets = ref(false)
const showHistory = ref(false)
const snippets = ref<string[]>([])
const history = ref<string[]>([])
let cleanupInputDiagnostics: (() => void) | null = null

// --- Copy-all feedback (icon flips to a checkmark briefly) ---
const copyFeedback = ref(false)
let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null

// --- Undo-clear: 清空全部 is destructive on multi-line drafts (the exact complaint this
// ships to fix — "很难清除" cuts both ways once it's gone), so the wiped text is kept around
// for a short window as a one-shot restore, not a confirm-before-clear dialog (that trades
// one friction for another). clearedBackup holds the pre-clear text; showUndo drives the pill;
// the timer auto-dismisses so the pill never lingers and blocks the input.
const clearedBackup = ref<string | null>(null)
const showUndo = ref(false)
let undoTimer: ReturnType<typeof setTimeout> | null = null
const UNDO_VISIBLE_MS = 6000

// --- Draft persistence ---
function saveDraft() {
  try { localStorage.setItem(DRAFT_KEY, text.value) } catch {}
}
function loadDraft() {
  try { text.value = localStorage.getItem(DRAFT_KEY) || '' } catch {}
}

// --- Injected draft (host-supplied, e.g. ResourceDrawer 重发) ---
// Replace the textarea with the injected text and put the caret at the end so the
// user can immediately edit before sending. Resize + persist so it behaves like
// hand-typed content.
function applyDraft(draft: string) {
  if (draft == null) return
  text.value = draft
  saveDraft()
  nextTick(() => {
    autoResize()
    const ta = textareaRef.value
    if (ta) {
      const end = ta.value.length
      try { ta.setSelectionRange(end, end) } catch {}
    }
    focusWithoutViewportScroll(textareaRef.value)
  })
}
watch(() => props.draft, (d) => { if (d != null) applyDraft(d) })

// --- Snippets (server-side, survives trycloudflare domain changes) ---
function loadSnippets() {
  snippets.value = serverStore.get<string[]>('snippets', [])
}
function saveSnippetsToStorage() {
  serverStore.set('snippets', snippets.value)
}
function saveSnippet() {
  const t = text.value.trim()
  if (!t) return
  if (!snippets.value.includes(t)) {
    snippets.value.unshift(t)
    if (snippets.value.length > 20) snippets.value.pop()
    saveSnippetsToStorage()
  }
}
function insertSnippet(s: string) {
  text.value = s
  showSnippets.value = false
  nextTick(() => {
    autoResize()
    focusWithoutViewportScroll(textareaRef.value)
  })
}
function deleteSnippet(i: number) {
  snippets.value.splice(i, 1)
  saveSnippetsToStorage()
}
function toggleSnippets() {
  showSnippets.value = !showSnippets.value
  if (showSnippets.value) showHistory.value = false
}

// --- Send History (server-side, survives trycloudflare domain changes) ---
function loadHistory() {
  history.value = serverStore.get<string[]>('history', [])
}
function saveHistoryToStorage() {
  serverStore.set('history', history.value)
}
function pushHistory(t: string) {
  const trimmed = t.trim()
  if (!trimmed) return
  // Dedup: remove existing identical entry
  const idx = history.value.indexOf(trimmed)
  if (idx !== -1) history.value.splice(idx, 1)
  // Prepend (newest first)
  history.value.unshift(trimmed)
  if (history.value.length > HISTORY_MAX) history.value.pop()
  saveHistoryToStorage()
}
function insertFromHistory(h: string) {
  text.value = h
  showHistory.value = false
  nextTick(() => {
    autoResize()
    focusWithoutViewportScroll(textareaRef.value)
  })
}
function clearHistory() {
  history.value = []
  serverStore.set('history', [])
}
function toggleHistory() {
  showHistory.value = !showHistory.value
  if (showHistory.value) showSnippets.value = false
}

// --- Auto-resize + input handler ---
// Grow with content up to MAX_INPUT_LINES, then scroll internally. Capping at a few lines
// (not ~50vh) is the core of the mobile input fix: a long paste can no longer swallow the
// screen or push the caret line behind the keyboard. Native caret-follow keeps the caret
// visible as the box scrolls. Resetting to 'auto' first lets the box shrink back when cleared.
const MAX_INPUT_LINES = 5
function autoResize() {
  const ta = textareaRef.value
  if (!ta) return
  ta.style.height = 'auto'
  const cs = getComputedStyle(ta)
  const lineHeight = parseFloat(cs.lineHeight) || 21
  const vPad = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom) + 2 // padding + 1px borders
  const cap = lineHeight * MAX_INPUT_LINES + vPad
  ta.style.height = Math.min(ta.scrollHeight, cap) + 'px'
}
function onInput(e: Event) {
  const ie = e as InputEvent
  reportCliInputDiagnostic('compose.input', {
    isComposing: ie.isComposing,
    inputType: ie.inputType,
    eventData: summarizeText(ie.data),
    modelValue: summarizeText(text.value),
  })
  autoResize()
  saveDraft()
}

// --- Focus handler: keep viewport scroll pinned when textarea regains focus ---
function onTextareaFocus() {
  // iOS may keep a stale page scroll after the IME checkmark dismisses the
  // keyboard. The app shell owns visual viewport height; compose only keeps the
  // root scroll pinned so the textarea never creates its own spacer.
  reportCliInputDiagnostic('compose.focus', { textLen: text.value.length })
  resetViewportScroll()
  setTimeout(resetViewportScroll, 150)
  setTimeout(resetViewportScroll, 400)
}

// --- Send ---
function send() {
  const val = text.value
  if (!val) return
  reportCliInputDiagnostic('compose.send', { value: summarizeText(val) })
  pushHistory(val)
  emit('send', val)
  text.value = ''
  try { localStorage.removeItem(DRAFT_KEY) } catch {}
  nextTick(autoResize)
}

// --- Copy all: writes the FULL current draft to the OS clipboard (same SSOT idiom as
// every other 复制 affordance in the terminal — copyTextToClipboard handles the
// insecure-HTTP / iOS execCommand fallback). Briefly flips the icon to a checkmark so a
// tap gets visible confirmation without a toast stealing space from the compact toolbar.
async function copyAll() {
  const ok = await copyTextToClipboard(text.value)
  reportCliInputDiagnostic('compose.copy-all', { ok, len: text.value.length })
  if (!ok) return
  copyFeedback.value = true
  if (copyFeedbackTimer) clearTimeout(copyFeedbackTimer)
  copyFeedbackTimer = setTimeout(() => { copyFeedback.value = false }, 1400)
}

// --- Clear all (undoable) ---
// Wipes the draft but keeps the pre-clear text in `clearedBackup` for UNDO_VISIBLE_MS so an
// accidental tap on a long multi-line draft isn't unrecoverable. An empty textarea has
// nothing worth restoring, so clearing it shows no undo pill (matches the "clear an
// already-empty box" fixture — a no-op, not a false affordance).
function clearAll() {
  const prev = text.value
  text.value = ''
  try { localStorage.removeItem(DRAFT_KEY) } catch {}
  nextTick(() => {
    autoResize()
    focusWithoutViewportScroll(textareaRef.value)
  })
  if (undoTimer) { clearTimeout(undoTimer); undoTimer = null }
  if (!prev) {
    showUndo.value = false
    clearedBackup.value = null
    return
  }
  clearedBackup.value = prev
  showUndo.value = true
  undoTimer = setTimeout(() => {
    showUndo.value = false
    clearedBackup.value = null
    undoTimer = null
  }, UNDO_VISIBLE_MS)
}

// --- Undo the last clear: restores the FULL pre-clear text (not appended — a straight
// replace, since the textarea is guaranteed empty right after clearAll). One-shot: once used
// (or once the pill times out) clearedBackup is gone, so a second tap does nothing.
function undoClear() {
  if (clearedBackup.value == null) return
  text.value = clearedBackup.value
  saveDraft()
  clearedBackup.value = null
  showUndo.value = false
  if (undoTimer) { clearTimeout(undoTimer); undoTimer = null }
  nextTick(() => {
    autoResize()
    const ta = textareaRef.value
    if (ta) {
      const end = ta.value.length
      try { ta.setSelectionRange(end, end) } catch {}
    }
    focusWithoutViewportScroll(textareaRef.value)
  })
}

// --- Cursor movement ---
function moveCursor(dir: 'up' | 'down' | 'left' | 'right' | 'home' | 'end') {
  const ta = textareaRef.value
  if (!ta) return
  const pos = ta.selectionStart
  const val = ta.value
  switch (dir) {
    case 'left':
      ta.selectionStart = ta.selectionEnd = Math.max(0, pos - 1); break
    case 'right':
      ta.selectionStart = ta.selectionEnd = Math.min(val.length, pos + 1); break
    case 'home': {
      const ls = val.lastIndexOf('\n', pos - 1) + 1
      ta.selectionStart = ta.selectionEnd = ls; break
    }
    case 'end': {
      let le = val.indexOf('\n', pos)
      if (le === -1) le = val.length
      ta.selectionStart = ta.selectionEnd = le; break
    }
    case 'up': {
      const cls = val.lastIndexOf('\n', pos - 1) + 1
      const col = pos - cls
      const pls = val.lastIndexOf('\n', cls - 2) + 1
      ta.selectionStart = ta.selectionEnd = Math.min(pls + col, Math.max(0, cls - 1)); break
    }
    case 'down': {
      const csl = val.lastIndexOf('\n', pos - 1) + 1
      const cp = pos - csl
      let nls = val.indexOf('\n', pos)
      if (nls === -1) break
      nls += 1
      let nle = val.indexOf('\n', nls)
      if (nle === -1) nle = val.length
      ta.selectionStart = ta.selectionEnd = Math.min(nls + cp, nle); break
    }
  }
  focusWithoutViewportScroll(ta)
}

onMounted(async () => {
  loadDraft()
  // A host-supplied draft (ResourceDrawer 重发) takes precedence over the persisted
  // local draft so the inserted prompt is what the user sees.
  if (props.draft != null) text.value = props.draft
  // 先加载服务端数据，再填充 snippets/history
  await serverStore.load()
  loadSnippets()
  loadHistory()
  await nextTick()
  focusWithoutViewportScroll(textareaRef.value)
  autoResize()
  cleanupInputDiagnostics = attachCliInputDiagnostics(textareaRef.value, 'compose-textarea')
  resetViewportScroll()
})

onUnmounted(() => {
  // Save draft when component unmounts (switching panels)
  saveDraft()
  cleanupInputDiagnostics?.()
  cleanupInputDiagnostics = null
  if (copyFeedbackTimer) clearTimeout(copyFeedbackTimer)
  if (undoTimer) clearTimeout(undoTimer)
  // Reset scroll on unmount to prevent residual gap
  resetViewportScroll()
})
</script>

<style scoped>
.compose-bar {
  --cb-bg: #14151f;
  --cb-border: #383850;
  --cb-toolbar-bg: #1a1b28;
  --cb-input-bg: #22233a;
  --cb-input-color: #e8e8f8;
  --cb-input-border: #4a4a6a;
  --cb-placeholder: #666680;
}
@media (prefers-color-scheme: light) {
  .compose-bar {
    --cb-bg: #f0f0f8;
    --cb-border: #c0c0d8;
    --cb-toolbar-bg: #e4e4f0;
    --cb-input-bg: #ffffff;
    --cb-input-color: #1a1a2e;
    --cb-input-border: #a0a0c0;
    --cb-placeholder: #8888aa;
  }
}
.compose-bar {
  position: relative;
  width: 100%;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--cb-bg);
  border-top: 1px solid var(--cb-border);
  z-index: 150;
  box-shadow: 0 -4px 20px rgba(0,0,0,0.4);
}

.compose-toolbar {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 4px 8px 3px;
  background: var(--cb-toolbar-bg);
  border-bottom: 1px solid var(--cb-border);
  overflow-x: auto;
  overflow-y: hidden;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
  scrollbar-width: none;
}
.compose-toolbar::-webkit-scrollbar { display: none; }

.cb-tool {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-width: 36px;
  height: 32px;
  padding: 0 7px;
  background: #2a2b3e;
  color: #b0b0c8;
  border: 1px solid #484868;
  border-radius: 6px;
  font-size: 0.76rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  user-select: none;
  -webkit-user-select: none;
  white-space: nowrap;
  transition: background 0.1s;
}
.cb-tool:active { background: #383954; }

.cb-tool--esc {
  color: #ff8080;
  border-color: #5a2020;
  background: #2a1010;
  font-weight: 600;
  font-size: 0.68rem;
}
.cb-tool--esc:active { background: #3a1818; }

.cb-tool--danger {
  color: #ff8080;
  border-color: #5a2020;
  background: #2a1010;
}
.cb-tool--danger:active { background: #3a1818; }

.cb-tool--copy {
  color: #80e0b0;
  border-color: #2a5a44;
  background: #0e2418;
}
.cb-tool--copy:active { background: #163a28; }

.cb-tool--snip {
  color: #80c8ff;
  border-color: #2a4a6a;
  background: #0e2030;
}
.cb-tool--snip:active { background: #162a40; }
.cb-tool--active { background: #1a3a5a; border-color: #3a6a9a; }

.cb-tool-divider {
  flex-shrink: 0;
  width: 1px;
  height: 20px;
  background: #484868;
  margin: 0 1px;
}

/* Snippet panel */
.snippet-panel {
  max-height: 140px;
  overflow-y: auto;
  background: #12131e;
  border-bottom: 1px solid var(--cb-border);
  padding: 6px 10px;
}
.snippet-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}
.snippet-title {
  font-size: 0.68rem;
  color: #666;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.snippet-save-btn {
  background: #1a3a5a;
  color: #80c8ff;
  border: 1px solid #2a5a8a;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 0.68rem;
  cursor: pointer;
}
.snippet-save-btn:active { background: #2a4a6a; }
.snippet-empty {
  color: #555;
  font-size: 0.72rem;
  text-align: center;
  padding: 8px;
}
.snippet-list { display: flex; flex-direction: column; gap: 3px; }
.snippet-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  background: #1a1b2e;
  border: 1px solid #333350;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.1s;
}
.snippet-item:active { background: #252640; }
.snippet-text {
  flex: 1;
  font-size: 0.78rem;
  color: #c0c0d8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.snippet-del {
  background: none;
  border: none;
  color: #666;
  font-size: 0.8rem;
  cursor: pointer;
  padding: 0 4px;
}
.snippet-del:active { color: #ff6060; }

/* Undo-clear bar: its OWN row (never overlaps the textarea/send button below it), compact,
   non-modal — a one-shot pill you can tap or let time out. */
.undo-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 5px 10px;
  background: #241a10;
  border-bottom: 1px solid #5a3a20;
}
.undo-msg {
  font-size: 0.72rem;
  color: #d0a878;
}
.undo-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  background: #4a3018;
  color: #ffc880;
  border: 1px solid #7a5028;
  border-radius: 6px;
  padding: 3px 10px;
  font-size: 0.74rem;
  font-weight: 600;
  cursor: pointer;
  touch-action: manipulation;
}
.undo-btn:active { background: #5a3c20; }

.cb-fade-enter-active, .cb-fade-leave-active { transition: opacity 0.15s ease; }
.cb-fade-enter-from, .cb-fade-leave-to { opacity: 0; }

/* History panel (auto-saved, visually distinct from snippets) */
.history-panel {
  max-height: 160px;
  overflow-y: auto;
  background: #0e1018;
  border-bottom: 1px solid var(--cb-border);
  padding: 6px 10px;
}
.history-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}
.history-title {
  font-size: 0.68rem;
  color: #8888aa;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.history-clear-btn {
  background: #2a1010;
  color: #ff8080;
  border: 1px solid #4a2020;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 0.68rem;
  cursor: pointer;
}
.history-clear-btn:active { background: #3a1818; }
.history-empty {
  color: #555;
  font-size: 0.72rem;
  text-align: center;
  padding: 8px;
}
.history-list { display: flex; flex-direction: column; gap: 2px; }
.history-item {
  display: flex;
  align-items: center;
  padding: 5px 8px;
  background: #181928;
  border: 1px solid #2a2a44;
  border-left: 3px solid #4a4a8a;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.1s;
}
.history-item:active { background: #222340; }
.history-text {
  flex: 1;
  font-size: 0.76rem;
  color: #a8a8c8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
}

.cb-tool--hist {
  color: #a0a0e0;
  border-color: #3a3a6a;
  background: #1a1a30;
}
.cb-tool--hist:active { background: #252550; }

/* Input row */
.compose-input-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  padding: 6px 10px 8px;
}
.compose-input {
  flex: 1;
  background: var(--cb-input-bg);
  color: var(--cb-input-color);
  border: 1px solid var(--cb-input-border);
  border-radius: 10px;
  padding: 8px 12px;
  font-size: 0.95rem;
  font-family: inherit;
  resize: none;
  outline: none;
  line-height: 1.4;
  min-height: 38px;
  /* Height cap (~5 lines) is applied precisely by autoResize() in JS; this CSS bound is a
     fallback so the box can never swallow the screen even before JS runs. Overflow scrolls,
     with the native caret-follow keeping the caret visible. */
  max-height: calc(1.4em * 5 + 18px);
  overflow-y: auto;
  transition: border-color 0.15s;
}
.compose-input::placeholder { color: var(--cb-placeholder); }
.compose-input:focus {
  border-color: #4a80d8;
  box-shadow: 0 0 0 2px rgba(74,128,216,0.2);
}

.btn-send {
  width: 38px;
  min-height: 38px;
  align-self: stretch;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #1a4fd8;
  color: white;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  touch-action: manipulation;
  transition: background 0.1s;
}
.btn-send:active { background: #1240b8; }
</style>
