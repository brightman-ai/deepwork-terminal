<template>
  <!-- 悬浮在终端之上（absolute，不占布局，所以出现 / 收起 / 拖动都不会让终端重排行列数）。
       位置规则见 composePlacement.ts：拖过就用拖过的（按 tab 记住），否则出现在光标所在行。 -->
  <div
    ref="rootRef"
    class="compose-bar-desktop"
    :class="{ 'is-dragging': dragging }"
    :style="{ left: `${pos.x}px`, top: `${pos.y}px` }"
    data-testid="compose-bar-desktop"
  >
    <!-- 拖动柄：按住移动整个输入框，双击复位到默认位置。 -->
    <div
      class="cbd-drag-handle"
      data-testid="compose-desktop-drag-handle"
      title="拖动移动输入框 · 双击复位"
      @pointerdown="onDragStart"
      @dblclick="resetPlacement"
    ><div class="cbd-drag-grip" /></div>
    <!-- Toolbar: DelAll + Pinned Lenses + Phrase Arsenal + History. -->
    <div class="cbd-toolbar">
      <button class="cbd-tool cbd-tool--esc" type="button" title="收起输入条，保留草稿" @click="$emit('close')">Esc</button>
      <div class="cbd-tool-divider" />
      <button class="cbd-tool" type="button" title="复制全部" data-testid="compose-desktop-copy-all" @click="copyAll">
        <Check v-if="copyFeedback" :size="13" />
        <Copy v-else :size="13" />
      </button>
      <button class="cbd-tool cbd-tool--danger" type="button" title="清空全部（6秒内可撤销）" data-testid="compose-desktop-clear-all" @click="handleClearAll">
        <Trash2 :size="13" />
      </button>
      <button
        class="cbd-tool cbd-tool--snip"
        type="button"
        :class="{ 'cbd-tool--active': showSnippets }"
        data-testid="compose-desktop-arsenal-btn"
        title="短语武器库（叠加透镜 / 动作模板 / 自定义短语，支持输入 / 唤起）"
        @click="toggleSnippets"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M16 4h2a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V6a2 2 0 012-2h2"/>
          <rect x="8" y="2" width="8" height="4" rx="1"/>
        </svg>
        快捷短语
      </button>
      <button
        class="cbd-tool cbd-tool--hist"
        type="button"
        :class="{ 'cbd-tool--active': showHistory }"
        title="发送历史"
        @click="toggleHistory"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <circle cx="12" cy="12" r="10"/><polyline points="12,6 12,12 16,14"/>
        </svg>
        发送历史
      </button>
      <div class="cbd-tool-divider" />
      <!-- Pinned non-destructive thinking lenses -->
      <div class="cbd-pinned-lenses" data-testid="compose-desktop-pinned-lenses">
        <button
          v-for="lens in lenses.pinnedLenses.value"
          :key="lens.id"
          type="button"
          class="cbd-lens-pill"
          :class="{ 'is-on': lenses.isLensActive(lens.id) }"
          :title="`${lens.title}（挂载不污染正文草稿）`"
          :data-testid="`compose-lens-pill-${lens.id}`"
          @click="onToggleLens(lens.id)"
        >
          {{ lenses.isLensActive(lens.id) ? '✓ ' : '+ ' }}{{ lens.short }}
        </button>
      </div>
      <div class="cbd-toolbar-spacer" />
      <span class="cbd-send-hint">Enter 发送 · / 短语库</span>
    </div>

    <!-- Phrase Arsenal: 🟣 叠加透镜 | 🟢 动作模板 + 自定义短语 -->
    <div v-if="showSnippets" class="cbd-panel cbd-panel--arsenal" data-testid="compose-phrase-arsenal">
      <div class="cbd-pa-head">
        <div class="cbd-pa-cats">
          <button
            type="button"
            class="cbd-pa-cat"
            :class="{ 'is-active': lenses.phraseFilter.value === 'all' }"
            data-testid="phrase-cat-all"
            @click="lenses.phraseFilter.value = 'all'"
          >全部</button>
          <button
            type="button"
            class="cbd-pa-cat cbd-pa-cat--lens"
            :class="{ 'is-active': lenses.phraseFilter.value === 'lens' }"
            data-testid="phrase-cat-lens"
            @click="lenses.phraseFilter.value = 'lens'"
          >🟣 叠加透镜</button>
          <button
            type="button"
            class="cbd-pa-cat cbd-pa-cat--tpl"
            :class="{ 'is-active': lenses.phraseFilter.value === 'tpl' }"
            data-testid="phrase-cat-tpl"
            @click="lenses.phraseFilter.value = 'tpl'"
          >🟢 动作模板</button>
        </div>
        <input
          v-model="lenses.phraseQuery.value"
          type="text"
          class="cbd-pa-search"
          data-testid="phrase-search-input"
          placeholder="搜短语…"
        />
        <button class="cbd-panel-save" type="button" title="将当前输入存为自定义短语" @click="saveSnippet">+ 存草稿</button>
      </div>

      <div class="cbd-pa-grid">
        <div
          v-for="p in lenses.filteredPhrases.value"
          :key="p.id"
          class="cbd-pa-card"
          :class="{ 'is-on': p.kind === 'lens' && lenses.isLensActive(p.id) }"
          :data-testid="`phrase-card-${p.id}`"
        >
          <div class="cbd-pa-card-top">
            <span class="cbd-pa-short">{{ p.short }}</span>
            <span class="cbd-pa-badge" :class="`is-${p.kind}`">{{ p.kind === 'lens' ? '透镜' : '模板' }}</span>
          </div>
          <div class="cbd-pa-desc">{{ p.desc }}</div>
          <div class="cbd-pa-actions">
            <button
              v-if="p.kind === 'lens'"
              type="button"
              class="cbd-pa-btn"
              :class="{ 'is-on': lenses.isLensActive(p.id) }"
              :data-testid="`phrase-toggle-lens-${p.id}`"
              @click="onToggleLens(p.id)"
            >{{ lenses.isLensActive(p.id) ? '✓ 已挂载' : '＋ 附加' }}</button>
            <button
              v-else
              type="button"
              class="cbd-pa-btn is-tpl"
              :data-testid="`phrase-insert-tpl-${p.id}`"
              @click="onInsertTemplate(p)"
            >↳ 插入光标</button>
          </div>
        </div>
      </div>

      <!-- User-saved custom snippets -->
      <div v-if="snippets.length > 0" class="cbd-custom-snippets">
        <div class="cbd-panel-header">
          <span class="cbd-panel-title">自定义短语</span>
        </div>
        <div class="cbd-panel-list">
          <div v-for="(s, i) in snippets" :key="i" class="cbd-panel-item" @click="onInsertCustomSnippet(s)">
            <span class="cbd-panel-text">{{ s.length > 60 ? s.slice(0, 60) + '...' : s }}</span>
            <button class="cbd-panel-del" type="button" title="删除" @click.stop="deleteSnippet(i)">x</button>
          </div>
        </div>
      </div>
    </div>

    <Transition name="cbd-fade">
      <div v-if="showUndo || lenses.hasUndoBackup.value" class="cbd-undo-bar" data-testid="compose-desktop-undo-clear-bar">
        <span class="cbd-undo-msg">已清空草稿与挂载项（6秒内可恢复）</span>
        <button class="cbd-undo-btn" type="button" data-testid="compose-desktop-undo-clear" @click="handleUndoClear">
          <RotateCcw :size="12" /> 撤销恢复
        </button>
      </div>
    </Transition>

    <div v-if="showHistory" class="cbd-panel cbd-panel--history">
      <div class="cbd-panel-header">
        <span class="cbd-panel-title">History</span>
        <button class="cbd-panel-clear" type="button" @click="clearHistory">Clear</button>
      </div>
      <div v-if="history.length === 0" class="cbd-panel-empty">No history yet</div>
      <div v-else class="cbd-panel-list">
        <div v-for="(h, i) in history" :key="i" class="cbd-panel-item cbd-panel-item--history" @click="insertFromHistory(h)">
          <span class="cbd-panel-text cbd-panel-text--mono">{{ h.length > 80 ? h.slice(0, 80) + '...' : h }}</span>
        </div>
      </div>
    </div>

    <!-- Active @file:line reference chips & stacked lens chips -->
    <div
      v-if="lenses.hasContextChips.value"
      class="cbd-chips-row"
      data-testid="compose-desktop-active-chips"
    >
      <span
        v-for="r in lenses.refChips.value"
        :key="`ref-${r}`"
        class="cbd-chip cbd-chip--ref"
        :data-testid="`compose-ref-chip-${r}`"
      >
        <span>📄 @{{ r }}</span>
        <button
          type="button"
          class="cbd-chip-x"
          :data-testid="`compose-remove-ref-${r}`"
          title="移除文件引用"
          @click="lenses.removeRefChip(r)"
        >✕</button>
      </span>
      <span
        v-for="p in lenses.activeLensObjects.value"
        :key="`lens-${p.id}`"
        class="cbd-chip cbd-chip--lens"
        :data-testid="`compose-active-lens-${p.id}`"
      >
        <span>{{ p.short }}</span>
        <button
          type="button"
          class="cbd-chip-x"
          :data-testid="`compose-remove-lens-${p.id}`"
          title="卸载透镜"
          @click="onToggleLens(p.id)"
        >✕</button>
      </span>
    </div>

    <div class="cbd-input-row">
      <textarea
        ref="textareaRef"
        v-model="text"
        class="cbd-input"
        data-compose-input
        placeholder="Enter 发送 · Shift+Enter 换行 · 输入 / 唤起叠加透镜与动作模板"
        @input="handleTextareaInput"
        @focus="onTextareaFocus"
        @keydown="onKeydown"
        @compositionstart="composing = true"
        @compositionend="onCompositionEnd"
      />
      <button class="cbd-btn-send" type="button" title="发送" data-testid="compose-desktop-send" @click="handleSend">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="22" y1="2" x2="11" y2="13"/><polygon points="22,2 15,22 11,13 2,9"/>
        </svg>
        发送
      </button>
    </div>
    <!-- 调整输入区高度：抓手在下沿，往下拖变高（和原先停靠式相反，因为框的上沿是锚点）。 -->
    <div
      class="cbd-resize-handle"
      data-testid="compose-desktop-resize-handle"
      title="拖动调整高度"
      @pointerdown="onResizeStart($event, 'below')"
    ><div class="cbd-resize-grip" /></div>
  </div>
</template>

<script setup lang="ts">
/**
 * ComposeBarDesktop — the desktop skin over useComposeBox + useComposeLenses (see ComposeBar.vue
 * for the mobile one). FLOATING box over the terminal:
 *  - absolute, so opening / closing / dragging it never changes the terminal's cols×rows;
 *  - supports non-destructive stackable `🟣 叠加透镜`, cursor-range `🟢 动作模板`, `@file:line`
 *    reference chips, `/` slash arsenal trigger, and 6000ms undo-clear backup;
 *  - Enter sends AND submits (Shift+Enter is the newline); an empty box is transparent to
 *    Esc / Ctrl-C / arrows / Tab / Enter — see composeKeys.ts.
 */
import { ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { Copy, Check, Trash2, RotateCcw } from 'lucide-vue-next'
import { useComposeBox } from '@terminal/composables/cli/useComposeBox'
import {
  useComposeLenses,
  insertAtCursorRange,
  type ComposePhrase,
} from '@terminal/composables/cli/composeLenses'
import { composerKeyAction } from '@terminal/composables/cli/composeKeys'
import { guardTerminalElementFocus } from '@terminal/composables/cli/terminalFocus'
import {
  clampPlacement,
  defaultPlacement,
  parsePlacement,
  placementStorageKey,
  serializePlacement,
  type Point,
} from '@terminal/composables/cli/composePlacement'

const props = defineProps<{
  draft?: string
  sessionId?: string
  /** 光标所在行的上沿（相对终端容器，像素）；null / 缺省 = 光标不在可见区，默认贴底。 */
  anchorTop?: number | null
  /** 终端此刻是不是「应用光标键」模式——空框直通方向键时要发 SS3 还是 CSI。 */
  applicationCursorKeys?: () => boolean
}>()

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'close'): void
  /** 框是空的：这个键的字节原样发给终端，框随之收起。 */
  (e: 'passthrough', data: string): void
  /** 框里只有触发它的那次输入、没动过就按了 Esc：那次输入原样补发给终端，框收起。 */
  (e: 'cancel-typed', text: string): void
}>()

const lenses = useComposeLenses(() => props.sessionId)
let pendingAssembledSend: string | null = null

const {
  text, textareaRef, autoResize,
  showSnippets, showHistory, snippets, history,
  copyFeedback, showUndo,
  toggleSnippets, toggleHistory, saveSnippet, deleteSnippet, clearHistory, insertFromHistory,
  onInput, onTextareaFocus, onResizeStart,
  send, copyAll, clearAll, undoClear, insertAtCursor,
  beginTyped, beginImeTyped, adoptCommitted, noteCompositionEnd, consumePristineTrigger,
} = useComposeBox({
  maxLines: 13,
  diagnosticSurface: 'compose-textarea-desktop',
  draft: () => props.draft,
  draftScope: () => props.sessionId,
  focusEl: (el) => el?.focus(),
  focusOnMount: false,
  emit: {
    send: (rawText) => {
      const out = pendingAssembledSend ?? lenses.assemblePayload(rawText)
      pendingAssembledSend = null
      emit('send', out)
    },
  },
})

// Sync draft bidirectionally with shared per-session store (for Chat ↔ CLI continuity)
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

function handleTextareaInput(e: Event): void {
  onInput(e)
  if (!composing.value && lenses.checkSlashTrigger(text.value)) {
    showSnippets.value = true
    showHistory.value = false
  }
}

function onToggleLens(lensId: string): void {
  lenses.toggleLens(lensId)
  void nextTick(() => textareaRef.value?.focus({ preventScroll: true }))
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
    el.focus({ preventScroll: true })
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
    el.focus({ preventScroll: true })
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
    void nextTick(() => {
      autoResize()
      const ta = textareaRef.value
      if (ta) {
        const end = ta.value.length
        try { ta.setSelectionRange(end, end) } catch {}
        ta.focus({ preventScroll: true })
      }
    })
  }
}

// ─── 键位（REQ-TI-04 / REQ-TI-05）：表在 composeKeys.ts，这里只执行 ───────────────────────────
const composing = ref(false)

function onCompositionEnd(): void {
  composing.value = false
  noteCompositionEnd()
  if (lenses.checkSlashTrigger(text.value)) {
    showSnippets.value = true
    showHistory.value = false
  }
}

function insertTab(): void {
  const ta = textareaRef.value
  if (!ta) return
  ta.setRangeText('\t', ta.selectionStart, ta.selectionEnd, 'end')
  ta.dispatchEvent(new Event('input', { bubbles: true }))
}

function onKeydown(e: KeyboardEvent): void {
  const effectiveText = text.value || (lenses.hasContextChips.value ? ' ' : '')
  const action = composerKeyAction(e, {
    text: effectiveText,
    composing: composing.value,
    applicationCursorKeys: props.applicationCursorKeys?.() ?? false,
  })
  switch (action.kind) {
    case 'ignore':
      return
    case 'send':
      e.preventDefault()
      handleSend()
      return
    case 'close-keep': {
      e.preventDefault()
      if (showSnippets.value) {
        showSnippets.value = false
        return
      }
      // 框里只有触发它的那次输入且没动过 → 那次输入还给终端（误拦截一键撤回，一个键都不丢）。
      const typed = consumePristineTrigger()
      if (typed && !lenses.hasContextChips.value) emit('cancel-typed', typed)
      else emit('close')
      return
    }
    case 'insert-tab':
      e.preventDefault()
      insertTab()
      return
    case 'swallow':
      e.preventDefault()
      return
    case 'passthrough':
      e.preventDefault()
      emit('passthrough', action.data)
      return
  }
}

// ─── 位置（REQ-TI-06）：算术在 composePlacement.ts ───────────────────────────────────────────
const rootRef = ref<HTMLElement>()
const pos = ref<Point>({ x: 8, y: 8 })
const dragging = ref(false)
let saved: Point | null = null
let resizeObserver: ResizeObserver | null = null

function readSaved(): Point | null {
  try { return parsePlacement(localStorage.getItem(placementStorageKey(props.sessionId))) } catch { return null }
}
function writeSaved(p: Point | null): void {
  try {
    const key = placementStorageKey(props.sessionId)
    if (p) localStorage.setItem(key, serializePlacement(p))
    else localStorage.removeItem(key)
  } catch { /* 记不住位置不该拦住输入框 */ }
}

function measure(): { box: { w: number; h: number }; bounds: { w: number; h: number } } | null {
  const el = rootRef.value
  const parent = el?.offsetParent as HTMLElement | null | undefined
  if (!el || !parent) return null
  return {
    box: { w: el.offsetWidth, h: el.offsetHeight },
    bounds: { w: parent.clientWidth, h: parent.clientHeight },
  }
}

function layout(): void {
  if (dragging.value) return
  const m = measure()
  if (!m) return
  pos.value = saved
    ? clampPlacement(saved, m.box, m.bounds)
    : defaultPlacement(props.anchorTop ?? null, m.box, m.bounds)
}

let dragOrigin: { px: number; py: number; x: number; y: number } | null = null

function onDragStart(e: PointerEvent): void {
  if (e.button !== 0) return
  const handle = e.currentTarget as HTMLElement
  e.preventDefault()
  dragOrigin = { px: e.clientX, py: e.clientY, x: pos.value.x, y: pos.value.y }
  dragging.value = true
  try { handle.setPointerCapture(e.pointerId) } catch { /* 合成事件没有活动指针 */ }
  handle.addEventListener('pointermove', onDragMove)
  handle.addEventListener('pointerup', onDragEnd)
  handle.addEventListener('pointercancel', onDragEnd)
}
function onDragMove(e: PointerEvent): void {
  const m = measure()
  if (!dragOrigin || !m) return
  pos.value = clampPlacement(
    { x: dragOrigin.x + e.clientX - dragOrigin.px, y: dragOrigin.y + e.clientY - dragOrigin.py },
    m.box,
    m.bounds,
  )
}
function onDragEnd(e: PointerEvent): void {
  const handle = e.currentTarget as HTMLElement
  handle.removeEventListener('pointermove', onDragMove)
  handle.removeEventListener('pointerup', onDragEnd)
  handle.removeEventListener('pointercancel', onDragEnd)
  try { handle.releasePointerCapture(e.pointerId) } catch { /* 已经释放 */ }
  dragging.value = false
  if (!dragOrigin) return
  const moved = pos.value.x !== dragOrigin.x || pos.value.y !== dragOrigin.y
  dragOrigin = null
  if (moved) {
    saved = { ...pos.value }
    writeSaved(saved)
  }
}
function resetPlacement(): void {
  saved = null
  writeSaved(null)
  layout()
}

let releaseFocusGuard: (() => void) | undefined
onMounted(() => {
  releaseFocusGuard = guardTerminalElementFocus(textareaRef.value, 'compose-desktop')
  if (!text.value && lenses.sharedDraft.value) {
    text.value = lenses.sharedDraft.value
  } else if (text.value) {
    lenses.sharedDraft.value = text.value
  }
  saved = readSaved()
  layout()
  const el = rootRef.value
  if (el && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(layout)
    resizeObserver.observe(el)
    if (el.offsetParent) resizeObserver.observe(el.offsetParent)
  }
})
onUnmounted(() => {
  releaseFocusGuard?.()
  resizeObserver?.disconnect()
  resizeObserver = null
})

defineExpose({
  insertAtCursor,
  beginTyped,
  beginImeTyped,
  adoptCommitted,
  addRefChip: lenses.addRefChip,
  toggleLens: lenses.toggleLens,
  focusInput: () => {
    autoResize()
    layout()
    const ta = textareaRef.value
    if (!ta) return
    const wasFocused = document.activeElement === ta
    ta.focus({ preventScroll: true })
    if (!wasFocused) {
      const end = ta.value.length
      try { ta.setSelectionRange(end, end) } catch { /* textarea may be unavailable in tests */ }
    }
  },
})
</script>

<style scoped>
.compose-bar-desktop {
  --cbd-bg: #14151f;
  --cbd-border: #383850;
  --cbd-toolbar-bg: #1a1b28;
  --cbd-input-bg: #22233a;
  --cbd-input-color: #e8e8f8;
  --cbd-input-border: #4a4a6a;
  --cbd-placeholder: #666680;
  --cbd-lens-bg: rgba(132, 90, 220, 0.18);
  --cbd-lens-border: rgba(168, 128, 255, 0.52);
  --cbd-lens-color: #e9dcff;
  --cbd-lens-hover-bg: rgba(132, 90, 220, 0.30);
  --cbd-lens-on-bg: #6b3fd9;
  --cbd-lens-on-border: #c4a3ff;
  --cbd-lens-on-color: #ffffff;
  --cbd-ref-bg: rgba(68, 132, 244, 0.16);
  --cbd-ref-border: rgba(98, 156, 255, 0.42);
  --cbd-ref-color: #bcd6ff;
  --cbd-chip-lens-bg: rgba(145, 96, 245, 0.22);
  --cbd-chip-lens-border: rgba(180, 140, 255, 0.5);
  --cbd-chip-lens-color: #eadbff;
}
@media (prefers-color-scheme: light) {
  .compose-bar-desktop {
    --cbd-bg: #f0f0f8;
    --cbd-border: #c0c0d8;
    --cbd-toolbar-bg: #e4e4f0;
    --cbd-input-bg: #ffffff;
    --cbd-input-color: #1a1a2e;
    --cbd-input-border: #a0a0c0;
    --cbd-placeholder: #8888aa;
    --cbd-lens-bg: #ece4ff;
    --cbd-lens-border: #8b5cf6;
    --cbd-lens-color: #3b1775;
    --cbd-lens-hover-bg: #dfd1ff;
    --cbd-lens-on-bg: #6534c9;
    --cbd-lens-on-border: #4c1d95;
    --cbd-lens-on-color: #ffffff;
    --cbd-ref-bg: #dbeafe;
    --cbd-ref-border: #3b82f6;
    --cbd-ref-color: #1e3a8a;
    --cbd-chip-lens-bg: #ece4ff;
    --cbd-chip-lens-border: #7c3aed;
    --cbd-chip-lens-color: #3b1775;
  }
}
.compose-bar-desktop {
  position: absolute;
  z-index: 12;
  width: min(860px, calc(100% - 16px));
  display: flex;
  flex-direction: column;
  background: var(--cbd-bg);
  border: 1px solid var(--cbd-border);
  border-radius: 10px;
  box-shadow: 0 10px 36px rgba(0, 0, 0, 0.55);
  overflow: hidden;
}
.compose-bar-desktop.is-dragging { box-shadow: 0 14px 44px rgba(0, 0, 0, 0.7); user-select: none; }

.cbd-drag-handle {
  flex-shrink: 0;
  height: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: grab;
  touch-action: none;
}
.is-dragging .cbd-drag-handle { cursor: grabbing; }
.cbd-drag-handle:hover .cbd-drag-grip,
.is-dragging .cbd-drag-grip { background: #6a80d8; }
.cbd-drag-grip {
  width: 36px;
  height: 3px;
  border-radius: 2px;
  background: #484868;
  transition: background 0.1s;
}

.cbd-resize-handle {
  flex-shrink: 0;
  height: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: ns-resize;
  touch-action: none;
}
.cbd-resize-handle:hover .cbd-resize-grip,
.cbd-resize-handle:active .cbd-resize-grip { background: #6a80d8; }
.cbd-resize-grip {
  width: 28px;
  height: 2px;
  border-radius: 2px;
  background: #3a3a58;
  transition: background 0.1s;
}

.cbd-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  padding: 5px 10px;
  background: var(--cbd-toolbar-bg);
  border-bottom: 1px solid var(--cbd-border);
}
.cbd-toolbar-spacer { flex: 1; }
.cbd-send-hint {
  font-size: 0.7rem;
  color: #7a7a96;
  white-space: nowrap;
}

.cbd-pinned-lenses {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
}
.cbd-lens-pill {
  display: inline-flex;
  align-items: center;
  height: 24px;
  padding: 0 8px;
  border-radius: 999px;
  border: 1px solid var(--cbd-lens-border);
  background: var(--cbd-lens-bg);
  color: var(--cbd-lens-color);
  font-size: 0.71rem;
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
  transition: all 0.12s ease;
}
.cbd-lens-pill:hover {
  background: var(--cbd-lens-hover-bg);
  border-color: var(--cbd-lens-on-border);
}
.cbd-lens-pill.is-on {
  background: var(--cbd-lens-on-bg);
  border-color: var(--cbd-lens-on-border);
  color: var(--cbd-lens-on-color);
  box-shadow: 0 0 0 1px rgba(139, 92, 246, 0.25);
}

.cbd-tool {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 9px;
  background: #2a2b3e;
  color: #b0b0c8;
  border: 1px solid #484868;
  border-radius: 6px;
  font-size: 0.76rem;
  cursor: pointer;
  transition: background 0.1s;
}
.cbd-tool:hover { background: #383954; }
.cbd-tool--esc { color: #ff8080; border-color: #5a2020; background: #2a1010; font-weight: 600; }
.cbd-tool--esc:hover { background: #3a1818; }
.cbd-tool--danger { color: #ff8080; border-color: #5a2020; background: #2a1010; }
.cbd-tool--danger:hover { background: #3a1818; }
.cbd-tool--snip { color: #80c8ff; border-color: #2a4a6a; background: #0e2030; }
.cbd-tool--hist { color: #a0a0e0; border-color: #3a3a6a; background: #1a1a30; }
.cbd-tool--active { background: #1a3a5a; border-color: #3a6a9a; }
.cbd-tool-divider { width: 1px; height: 18px; background: #484868; margin: 0 2px; }

.cbd-panel {
  max-height: 240px;
  overflow-y: auto;
  background: #12131e;
  border-bottom: 1px solid var(--cbd-border);
  padding: 8px 10px;
}
.cbd-pa-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
}
.cbd-pa-cats {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.cbd-pa-cat {
  height: 24px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid #353754;
  background: #1a1b2c;
  color: #a8acc8;
  font-size: 0.72rem;
  cursor: pointer;
}
.cbd-pa-cat.is-active {
  background: #283252;
  border-color: #5b82d6;
  color: #eef4ff;
  font-weight: 600;
}
.cbd-pa-search {
  flex: 1;
  min-width: 120px;
  height: 24px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid #383a58;
  background: #191a2a;
  color: #e4e6f6;
  font-size: 0.74rem;
  outline: none;
}
.cbd-pa-search:focus { border-color: #5b82d6; }
.cbd-pa-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: 6px;
}
.cbd-pa-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 7px 9px;
  border-radius: 7px;
  border: 1px solid #2e304c;
  background: #181a2c;
}
.cbd-pa-card.is-on {
  border-color: rgba(176, 132, 255, 0.7);
  background: rgba(112, 74, 196, 0.16);
}
.cbd-pa-card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}
.cbd-pa-short {
  font-size: 0.76rem;
  font-weight: 600;
  color: #eef0ff;
}
.cbd-pa-badge {
  font-size: 0.64rem;
  padding: 1px 6px;
  border-radius: 999px;
}
.cbd-pa-badge.is-lens {
  background: rgba(154, 106, 255, 0.2);
  color: #d9bfff;
  border: 1px solid rgba(154, 106, 255, 0.4);
}
.cbd-pa-badge.is-tpl {
  background: rgba(61, 214, 140, 0.16);
  color: #96f2c2;
  border: 1px solid rgba(61, 214, 140, 0.38);
}
.cbd-pa-desc {
  font-size: 0.69rem;
  color: #9ca0be;
  line-height: 1.35;
}
.cbd-pa-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
  padding-top: 2px;
}
.cbd-pa-btn {
  height: 22px;
  padding: 0 8px;
  border-radius: 5px;
  border: 1px solid #485688;
  background: #212c4d;
  color: #cde0ff;
  font-size: 0.69rem;
  cursor: pointer;
}
.cbd-pa-btn:hover { background: #2c3b66; }
.cbd-pa-btn.is-on {
  background: rgba(145, 96, 245, 0.32);
  border-color: #b48cff;
  color: #f6eeff;
}
.cbd-pa-btn.is-tpl {
  border-color: rgba(61, 214, 140, 0.45);
  background: rgba(34, 120, 82, 0.28);
  color: #adf5ce;
}

.cbd-custom-snippets {
  margin-top: 8px;
  padding-top: 6px;
  border-top: 1px dashed #2e304c;
}
.cbd-panel-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.cbd-panel-title { font-size: 0.68rem; color: #7a7e9a; text-transform: uppercase; letter-spacing: 0.5px; }
.cbd-panel-save {
  background: #1a3a5a; color: #80c8ff; border: 1px solid #2a5a8a;
  border-radius: 5px; padding: 2px 8px; font-size: 0.68rem; cursor: pointer;
}
.cbd-panel-clear {
  background: #2a1010; color: #ff8080; border: 1px solid #4a2020;
  border-radius: 4px; padding: 2px 8px; font-size: 0.68rem; cursor: pointer;
}
.cbd-panel-empty { color: #555; font-size: 0.72rem; text-align: center; padding: 8px; }
.cbd-panel-list { display: flex; flex-direction: column; gap: 3px; }
.cbd-panel-item {
  display: flex; align-items: center; gap: 6px; padding: 5px 8px;
  background: #1a1b2e; border: 1px solid #333350; border-radius: 6px;
  cursor: pointer; transition: background 0.1s;
}
.cbd-panel-item:hover { background: #252640; }
.cbd-panel-item--history { border-left: 3px solid #4a4a8a; }
.cbd-panel-text { flex: 1; font-size: 0.8rem; color: #c0c0d8; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cbd-panel-text--mono { font-family: 'SF Mono', 'Menlo', 'Consolas', monospace; }
.cbd-panel-del { background: none; border: none; color: #666; font-size: 0.85rem; cursor: pointer; padding: 0 4px; }
.cbd-panel-del:hover { color: #ff6060; }

.cbd-chips-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
  padding: 5px 10px 0;
}
.cbd-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 7px;
  border-radius: 999px;
  font-size: 0.71rem;
  line-height: 1.3;
}
.cbd-chip--ref {
  background: var(--cbd-ref-bg);
  border: 1px solid var(--cbd-ref-border);
  color: var(--cbd-ref-color);
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
}
.cbd-chip--lens {
  background: var(--cbd-chip-lens-bg);
  border: 1px solid var(--cbd-chip-lens-border);
  color: var(--cbd-chip-lens-color);
  font-weight: 500;
}
.cbd-chip-x {
  background: none;
  border: none;
  color: inherit;
  opacity: 0.75;
  cursor: pointer;
  padding: 0 1px;
  font-size: 0.68rem;
}
.cbd-chip-x:hover { opacity: 1; color: #ff9c9c; }

.cbd-undo-bar {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  padding: 5px 10px; background: #241a10; border-bottom: 1px solid #5a3a20;
}
.cbd-undo-msg { font-size: 0.74rem; color: #d0a878; }
.cbd-undo-btn {
  display: inline-flex; align-items: center; gap: 4px;
  background: #4a3018; color: #ffc880; border: 1px solid #7a5028;
  border-radius: 6px; padding: 3px 10px; font-size: 0.76rem; font-weight: 600; cursor: pointer;
}
.cbd-undo-btn:hover { background: #5a3c20; }
.cbd-fade-enter-active, .cbd-fade-leave-active { transition: opacity 0.15s ease; }
.cbd-fade-enter-from, .cbd-fade-leave-to { opacity: 0; }

.cbd-input-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  padding: 8px 10px 4px;
}
.cbd-input {
  flex: 1;
  background: var(--cbd-input-bg);
  color: var(--cbd-input-color);
  border: 1px solid var(--cbd-input-border);
  border-radius: 8px;
  padding: 9px 12px;
  font-size: 0.9rem;
  font-family: inherit;
  resize: none;
  outline: none;
  line-height: 1.45;
  min-height: 40px;
  max-height: calc(1.45em * 13 + 20px);
  overflow-y: auto;
  transition: border-color 0.15s;
}
.cbd-input::placeholder { color: var(--cbd-placeholder); }
.cbd-input:focus { border-color: #4a80d8; box-shadow: 0 0 0 2px rgba(74,128,216,0.2); }

.cbd-btn-send {
  display: flex;
  align-items: center;
  gap: 6px;
  align-self: flex-end;
  height: 40px;
  padding: 0 16px;
  background: #1a4fd8;
  color: white;
  border: none;
  border-radius: 8px;
  font-size: 0.85rem;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.1s;
}
.cbd-btn-send:hover { background: #2a5fe8; }
.cbd-btn-send:active { background: #1240b8; }
</style>
