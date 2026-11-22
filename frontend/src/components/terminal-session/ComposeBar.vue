<template>
  <div class="compose-bar">
    <!-- Toolbar: cursor nav + DelAll + Phrase Arsenal + History -->
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
      <button class="cb-tool cb-tool--danger" @click="handleClearAll" title="清空全部（6秒可撤销）" data-testid="compose-clear-all">
        <Trash2 width="13" height="13" />
      </button>
      <button
        class="cb-tool cb-tool--snip"
        :class="{ 'cb-tool--active': showSnippets }"
        data-testid="compose-mobile-arsenal-btn"
        @click="toggleSnippets"
        title="短语武器库（透镜 / 模板）"
      >
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

    <!-- Scrollable pinned lens pills -->
    <div class="cb-lens-strip" data-testid="compose-mobile-pinned-lenses" @mousedown.prevent>
      <button
        v-for="lens in lenses.pinnedLenses.value"
        :key="lens.id"
        type="button"
        class="cb-lens-pill"
        :class="{ 'is-on': lenses.isLensActive(lens.id) }"
        :data-testid="`compose-mobile-lens-pill-${lens.id}`"
        @click="onToggleLens(lens.id)"
      >{{ lenses.isLensActive(lens.id) ? '✓ ' : '+ ' }}{{ lens.short }}</button>
    </div>

    <!-- Phrase Arsenal panel (🟣 叠加透镜 | 🟢 动作模板 + 自定义短语) -->
    <div v-if="showSnippets" class="snippet-panel" data-testid="compose-mobile-phrase-arsenal">
      <div class="snippet-header">
        <div class="cb-pa-cats">
          <button
            type="button"
            class="cb-pa-cat"
            :class="{ 'is-active': lenses.phraseFilter.value === 'all' }"
            data-testid="mobile-phrase-cat-all"
            @click="lenses.phraseFilter.value = 'all'"
          >全部</button>
          <button
            type="button"
            class="cb-pa-cat"
            :class="{ 'is-active': lenses.phraseFilter.value === 'lens' }"
            data-testid="mobile-phrase-cat-lens"
            @click="lenses.phraseFilter.value = 'lens'"
          >🟣 叠加透镜</button>
          <button
            type="button"
            class="cb-pa-cat"
            :class="{ 'is-active': lenses.phraseFilter.value === 'tpl' }"
            data-testid="mobile-phrase-cat-tpl"
            @click="lenses.phraseFilter.value = 'tpl'"
          >🟢 动作模板</button>
        </div>
        <button class="snippet-save-btn" @click="saveSnippet" title="Save current text">+ 存草稿</button>
      </div>
      <div class="snippet-list">
        <div
          v-for="p in lenses.filteredPhrases.value"
          :key="p.id"
          class="snippet-item"
          :class="{ 'is-lens-on': p.kind === 'lens' && lenses.isLensActive(p.id) }"
          @click="p.kind === 'lens' ? onToggleLens(p.id) : onInsertTemplate(p)"
        >
          <div class="cb-pa-info">
            <span class="cb-pa-short">{{ p.short }}</span>
            <span class="cb-pa-desc">{{ p.desc }}</span>
          </div>
          <span class="cb-pa-act">{{ p.kind === 'lens' ? (lenses.isLensActive(p.id) ? '✓ 已挂载' : '＋ 附加') : '↳ 插入' }}</span>
        </div>
        <div v-for="(s, i) in snippets" :key="`custom-${i}`" class="snippet-item" @click="onInsertCustomSnippet(s)">
          <span class="snippet-text">{{ s.length > 40 ? s.slice(0, 40) + '...' : s }}</span>
          <button class="snippet-del" @click.stop="deleteSnippet(i)" title="Delete">x</button>
        </div>
      </div>
    </div>

    <!-- Undo-clear bar: 6-second restore of draft + lenses + @file chips -->
    <Transition name="cb-fade">
      <div v-if="showUndo || lenses.hasUndoBackup.value" class="undo-bar" data-testid="compose-undo-clear-bar">
        <span class="undo-msg">已清空草稿与挂载项（6秒内可恢复）</span>
        <button class="undo-btn" type="button" @click="handleUndoClear" data-testid="compose-undo-clear">
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

    <!-- Active @file:line reference chips & stacked lens chips -->
    <div
      v-if="lenses.hasContextChips.value"
      class="cb-chips-row"
      data-testid="compose-mobile-active-chips"
    >
      <span v-for="r in lenses.refChips.value" :key="`ref-${r}`" class="cb-chip cb-chip--ref">
        <span>📄 @{{ r }}</span>
        <button type="button" class="cb-chip-x" @click="lenses.removeRefChip(r)">✕</button>
      </span>
      <span v-for="p in lenses.activeLensObjects.value" :key="`lens-${p.id}`" class="cb-chip cb-chip--lens">
        <span>{{ p.short }}</span>
        <button type="button" class="cb-chip-x" @click="onToggleLens(p.id)">✕</button>
      </span>
    </div>

    <!-- Input row -->
    <div class="compose-input-row">
      <textarea
        ref="textareaRef"
        v-model="text"
        class="compose-input"
        data-compose-input
        placeholder="Enter 换行 · 点发送提交 · 输入 / 唤起短语透镜"
        @input="handleTextareaInput"
        @focus="onTextareaFocus"
      />
      <!-- Send: submit compose text to terminal -->
      <button class="btn-send" @click="handleSend" @touchend.stop.prevent="handleSend" title="Send text">
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
 * Enter = newline. Send button = submit. Draft persisted to localStorage and shared with
 * `useComposeLenses` (`🟣 叠加透镜` / `🟢 动作模板` / `@file:line` chips / 6s undo).
 */
import { watch, nextTick, onMounted } from 'vue'
import { focusWithoutViewportScroll, resetViewportScroll } from '@terminal/composables/cli/useVisualKeyboardInset'
import { useComposeBox } from '@terminal/composables/cli/useComposeBox'
import {
  useComposeLenses,
  insertAtCursorRange,
  type ComposePhrase,
} from '@terminal/composables/cli/composeLenses'
import { Copy, Check, Trash2, RotateCcw } from 'lucide-vue-next'

const props = defineProps<{ draft?: string; sessionId?: string }>()

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'close'): void
}>()

const lenses = useComposeLenses(() => props.sessionId)
let pendingAssembledSend: string | null = null

const {
  text, textareaRef, autoResize,
  showSnippets, showHistory, snippets, history,
  copyFeedback, showUndo,
  toggleSnippets, toggleHistory, saveSnippet, deleteSnippet, clearHistory, insertFromHistory,
  onInput, onTextareaFocus,
  send, copyAll, clearAll, undoClear, insertAtCursor, moveCursor,
} = useComposeBox({
  maxLines: 5,
  diagnosticSurface: 'compose-textarea-mobile',
  draft: () => props.draft,
  draftScope: () => props.sessionId,
  focusEl: focusWithoutViewportScroll,
  onFocusSideEffects: () => {
    resetViewportScroll()
    setTimeout(resetViewportScroll, 150)
    setTimeout(resetViewportScroll, 400)
  },
  emit: {
    send: (rawText) => {
      const out = pendingAssembledSend ?? lenses.assemblePayload(rawText)
      pendingAssembledSend = null
      emit('send', out)
    },
  },
})

let syncingDraft = false
watch(text, (val) => {
  if (syncingDraft) return
  syncingDraft = true
  lenses.sharedDraft.value = val
  syncingDraft = false
})
watch(
  () => lenses.sharedDraft.value,
  (val) => {
    if (syncingDraft || val === text.value) return
    syncingDraft = true
    text.value = val
    syncingDraft = false
    void nextTick(autoResize)
  },
)

onMounted(() => {
  if (!text.value && lenses.sharedDraft.value) {
    text.value = lenses.sharedDraft.value
  } else if (text.value) {
    lenses.sharedDraft.value = text.value
  }
})

function handleTextareaInput(e: Event): void {
  onInput(e)
  if (lenses.checkSlashTrigger(text.value)) {
    showSnippets.value = true
    showHistory.value = false
  }
}

function onToggleLens(lensId: string): void {
  lenses.toggleLens(lensId)
}

function onInsertTemplate(phrase: ComposePhrase): void {
  const ta = textareaRef.value
  const start = ta ? ta.selectionStart : text.value.length
  const end = ta ? ta.selectionEnd : start
  const inserted = lenses.insertTemplateAtRange(text.value, start, end, phrase)
  text.value = inserted.text
  showSnippets.value = false
  void nextTick(() => {
    autoResize()
    const el = textareaRef.value
    if (!el) return
    focusWithoutViewportScroll(el)
    try { el.setSelectionRange(inserted.cursor, inserted.cursor) } catch {}
  })
}

function onInsertCustomSnippet(snippet: string): void {
  const ta = textareaRef.value
  const start = ta ? ta.selectionStart : text.value.length
  const end = ta ? ta.selectionEnd : start
  const res = insertAtCursorRange(text.value, start, end, snippet)
  text.value = res.text
  lenses.sharedDraft.value = res.text
  showSnippets.value = false
  void nextTick(() => {
    autoResize()
    const el = textareaRef.value
    if (!el) return
    focusWithoutViewportScroll(el)
    try { el.setSelectionRange(res.cursor, res.cursor) } catch {}
  })
}

function handleSend(): void {
  const assembled = lenses.assemblePayload(text.value)
  if (!assembled) return
  if (text.value) {
    pendingAssembledSend = assembled
    send()
  } else {
    emit('send', assembled)
  }
  lenses.clearAfterSend()
  showSnippets.value = false
}

function handleClearAll(): void {
  lenses.clearWithUndo(text.value)
  clearAll()
}

function handleUndoClear(): void {
  const restored = lenses.undoClear()
  undoClear()
  if (restored && text.value !== restored.draft) {
    text.value = restored.draft
    void nextTick(autoResize)
  }
}

// 宿主（CliTerminalSurface）在输入框里贴图时，把上传后的文件引用插到光标处。
defineExpose({ insertAtCursor, addRefChip: lenses.addRefChip, toggleLens: lenses.toggleLens })
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
  --cb-lens-bg: rgba(132, 90, 220, 0.18);
  --cb-lens-border: rgba(168, 128, 255, 0.52);
  --cb-lens-color: #e9dcff;
  --cb-lens-on-bg: #6b3fd9;
  --cb-lens-on-border: #c4a3ff;
  --cb-lens-on-color: #ffffff;
  --cb-ref-bg: rgba(68, 132, 244, 0.16);
  --cb-ref-border: rgba(98, 156, 255, 0.42);
  --cb-ref-color: #bcd6ff;
  --cb-chip-lens-bg: rgba(145, 96, 245, 0.22);
  --cb-chip-lens-border: rgba(180, 140, 255, 0.5);
  --cb-chip-lens-color: #eadbff;
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
    --cb-lens-bg: #ece4ff;
    --cb-lens-border: #8b5cf6;
    --cb-lens-color: #3b1775;
    --cb-lens-on-bg: #6534c9;
    --cb-lens-on-border: #4c1d95;
    --cb-lens-on-color: #ffffff;
    --cb-ref-bg: #dbeafe;
    --cb-ref-border: #3b82f6;
    --cb-ref-color: #1e3a8a;
    --cb-chip-lens-bg: #ece4ff;
    --cb-chip-lens-border: #7c3aed;
    --cb-chip-lens-color: #3b1775;
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

.cb-lens-strip {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 8px;
  overflow-x: auto;
  border-bottom: 1px solid var(--cb-border);
  scrollbar-width: none;
}
.cb-lens-strip::-webkit-scrollbar { display: none; }
.cb-lens-pill {
  flex-shrink: 0;
  height: 24px;
  padding: 0 8px;
  border-radius: 999px;
  border: 1px solid var(--cb-lens-border);
  background: var(--cb-lens-bg);
  color: var(--cb-lens-color);
  font-size: 0.7rem;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
}
.cb-lens-pill.is-on {
  background: var(--cb-lens-on-bg);
  border-color: var(--cb-lens-on-border);
  color: var(--cb-lens-on-color);
}

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

/* Snippet / Phrase Arsenal panel */
.snippet-panel {
  max-height: 180px;
  overflow-y: auto;
  background: #12131e;
  border-bottom: 1px solid var(--cb-border);
  padding: 6px 10px;
}
.snippet-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  margin-bottom: 5px;
}
.cb-pa-cats {
  display: inline-flex;
  gap: 4px;
  overflow-x: auto;
}
.cb-pa-cat {
  padding: 2px 7px;
  border-radius: 5px;
  border: 1px solid #353754;
  background: #1a1b2c;
  color: #a8acc8;
  font-size: 0.68rem;
  white-space: nowrap;
}
.cb-pa-cat.is-active {
  background: #283252;
  border-color: #5b82d6;
  color: #eef4ff;
}
.snippet-save-btn {
  flex-shrink: 0;
  background: #1a3a5a;
  color: #80c8ff;
  border: 1px solid #2a5a8a;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 0.68rem;
  cursor: pointer;
}
.snippet-save-btn:active { background: #2a4a6a; }
.snippet-list { display: flex; flex-direction: column; gap: 4px; }
.snippet-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 6px 8px;
  background: #1a1b2e;
  border: 1px solid #333350;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.1s;
}
.snippet-item.is-lens-on {
  border-color: rgba(176, 132, 255, 0.7);
  background: rgba(112, 74, 196, 0.16);
}
.snippet-item:active { background: #252640; }
.cb-pa-info {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.cb-pa-short {
  font-size: 0.75rem;
  font-weight: 600;
  color: #eceeff;
}
.cb-pa-desc {
  font-size: 0.66rem;
  color: #969ab8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cb-pa-act {
  flex-shrink: 0;
  font-size: 0.68rem;
  color: #9ec5ff;
}
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

.cb-chips-row {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 5px 10px 0;
}
.cb-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border-radius: 999px;
  font-size: 0.69rem;
}
.cb-chip--ref {
  background: var(--cb-ref-bg);
  border: 1px solid var(--cb-ref-border);
  color: var(--cb-ref-color);
}
.cb-chip--lens {
  background: var(--cb-chip-lens-bg);
  border: 1px solid var(--cb-chip-lens-border);
  color: var(--cb-chip-lens-color);
  font-weight: 500;
}
.cb-chip-x {
  background: none;
  border: none;
  color: inherit;
  font-size: 0.66rem;
  padding: 0 1px;
}

/* Undo-clear bar */
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

/* History panel */
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
