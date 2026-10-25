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
    <!-- Toolbar: DelAll + Snippets + History. No cursor-nav buttons here — unlike mobile, a
         desktop user already has arrow keys / Home / End on the keyboard sitting under the
         textarea; on-screen duplicates would be pure clutter. See ComposeBar.vue (mobile). -->
    <div class="cbd-toolbar">
      <button class="cbd-tool cbd-tool--esc" type="button" title="收起输入条，保留草稿" @click="$emit('close')">Esc</button>
      <div class="cbd-tool-divider" />
      <button class="cbd-tool" type="button" title="复制全部" data-testid="compose-desktop-copy-all" @click="copyAll">
        <Check v-if="copyFeedback" :size="13" />
        <Copy v-else :size="13" />
      </button>
      <button class="cbd-tool cbd-tool--danger" type="button" title="清空全部" data-testid="compose-desktop-clear-all" @click="clearAll">
        <Trash2 :size="13" />
      </button>
      <button
        class="cbd-tool cbd-tool--snip" type="button" :class="{ 'cbd-tool--active': showSnippets }"
        title="快捷短语" @click="toggleSnippets"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M16 4h2a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V6a2 2 0 012-2h2"/>
          <rect x="8" y="2" width="8" height="4" rx="1"/>
        </svg>
        快捷短语
      </button>
      <button
        class="cbd-tool cbd-tool--hist" type="button" :class="{ 'cbd-tool--active': showHistory }"
        title="发送历史" @click="toggleHistory"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <circle cx="12" cy="12" r="10"/><polyline points="12,6 12,12 16,14"/>
        </svg>
        发送历史
      </button>
      <div class="cbd-toolbar-spacer" />
      <span class="cbd-send-hint">Enter 发送 · Shift+Enter 换行</span>
    </div>

    <div v-if="showSnippets" class="cbd-panel">
      <div class="cbd-panel-header">
        <span class="cbd-panel-title">Snippets</span>
        <button class="cbd-panel-save" type="button" @click="saveSnippet">+ Save</button>
      </div>
      <div v-if="snippets.length === 0" class="cbd-panel-empty">No snippets saved</div>
      <div v-else class="cbd-panel-list">
        <div v-for="(s, i) in snippets" :key="i" class="cbd-panel-item" @click="insertSnippet(s)">
          <span class="cbd-panel-text">{{ s.length > 60 ? s.slice(0, 60) + '...' : s }}</span>
          <button class="cbd-panel-del" type="button" title="Delete" @click.stop="deleteSnippet(i)">x</button>
        </div>
      </div>
    </div>

    <Transition name="cbd-fade">
      <div v-if="showUndo" class="cbd-undo-bar" data-testid="compose-desktop-undo-clear-bar">
        <span class="cbd-undo-msg">已清空全部</span>
        <button class="cbd-undo-btn" type="button" data-testid="compose-desktop-undo-clear" @click="undoClear">
          <RotateCcw :size="12" /> 撤销
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

    <div class="cbd-input-row">
      <textarea
        ref="textareaRef"
        v-model="text"
        class="cbd-input"
        data-compose-input
        placeholder="Enter 发送 · Shift+Enter 换行"
        @input="onInput"
        @focus="onTextareaFocus"
        @keydown="onKeydown"
        @compositionstart="composing = true"
        @compositionend="onCompositionEnd"
      />
      <button class="cbd-btn-send" type="button" title="发送" @click="send">
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
 * ComposeBarDesktop — the desktop skin over useComposeBox (see ComposeBar.vue for the mobile
 * one). Since terminal-input (2026-10-14) it is a FLOATING box over the terminal, not a docked row:
 *  - absolute, so opening / closing / dragging it never changes the terminal's cols×rows;
 *  - opened by typing (type-to-compose, see typeToCompose.ts), by the FAB, or by the shortcut — all
 *    three land in THIS component: one draft per tab, one set of keys, one placement rule;
 *  - Enter sends AND submits (Shift+Enter is the newline); an empty box is transparent to
 *    Esc / Ctrl-C / arrows / Tab / Enter — see composeKeys.ts for the whole table.
 * Differences from mobile, each tied to a real desktop trait:
 *  - No cursor-nav buttons: a hardware keyboard already has arrow keys / Home / End.
 *  - No soft-keyboard viewport dodging: desktop has no visual-viewport keyboard inset to correct.
 *  - Taller box (maxLines): desktop's vertical space isn't scarce the way a phone's is.
 */
import { ref, onMounted, onUnmounted } from 'vue'
import { Copy, Check, Trash2, RotateCcw } from 'lucide-vue-next'
import { useComposeBox } from '@terminal/composables/cli/useComposeBox'
import { composerKeyAction } from '@terminal/composables/cli/composeKeys'
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

const {
  text, textareaRef, autoResize,
  showSnippets, showHistory, snippets, history,
  copyFeedback, showUndo,
  toggleSnippets, toggleHistory, saveSnippet, insertSnippet, deleteSnippet, clearHistory, insertFromHistory,
  onInput, onTextareaFocus, onResizeStart,
  send, copyAll, clearAll, undoClear, insertAtCursor,
  beginTyped, beginImeTyped, adoptCommitted, noteCompositionEnd, consumePristineTrigger,
} = useComposeBox({
  maxLines: 13, // desktop screens have vertical room mobile's ~5-line cap was never about
  diagnosticSurface: 'compose-textarea-desktop',
  draft: () => props.draft,
  draftScope: () => props.sessionId,
  focusEl: (el) => el?.focus(),
  focusOnMount: false,
  // No onFocusSideEffects — desktop has no soft-keyboard viewport to correct for.
  emit: { send: (t) => emit('send', t) },
})

// ─── 键位（REQ-TI-04 / REQ-TI-05）：表在 composeKeys.ts，这里只执行 ───────────────────────────
const composing = ref(false)

function onCompositionEnd(): void {
  composing.value = false
  noteCompositionEnd()
}

function insertTab(): void {
  const ta = textareaRef.value
  if (!ta) return
  ta.setRangeText('\t', ta.selectionStart, ta.selectionEnd, 'end')
  // setRangeText 不触发 input：补一个，v-model 和草稿保存才跟得上。
  ta.dispatchEvent(new Event('input', { bubbles: true }))
}

function onKeydown(e: KeyboardEvent): void {
  const action = composerKeyAction(e, {
    text: text.value,
    composing: composing.value,
    applicationCursorKeys: props.applicationCursorKeys?.() ?? false,
  })
  switch (action.kind) {
    case 'ignore':
      return
    case 'send':
      e.preventDefault()
      send()
      return
    case 'close-keep': {
      e.preventDefault()
      // 框里只有触发它的那次输入且没动过 → 那次输入还给终端（误拦截一键撤回，一个键都不丢）。
      const typed = consumePristineTrigger()
      if (typed) emit('cancel-typed', typed)
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
  // 没动就不写：单击拖动柄不该把"默认位置"固化成"用户选的位置"。
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

onMounted(() => {
  saved = readSaved()
  layout() // 同步：在第一次绘制之前就位，不闪一下
  const el = rootRef.value
  if (el && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(layout)
    resizeObserver.observe(el)
    if (el.offsetParent) resizeObserver.observe(el.offsetParent)
  }
})
onUnmounted(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
})

// 宿主（CliTerminalSurface）：贴图时把上传后的文件引用插到光标处；打字唤起时把那次输入放进来。
defineExpose({
  insertAtCursor,
  beginTyped,
  beginImeTyped,
  adoptCommitted,
  focusInput: () => {
    autoResize()
    layout()
    // openDesktopCompose already awaited the mount tick. Focus synchronously here: delaying
    // this another tick lets a fast IME commit land before the native caret exists, after which
    // the next syllables can be inserted at position 0. If another input already owns focus,
    // preserve its selection; a newly focused box starts at the end of its draft.
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
  }
}
/* 浮层：absolute 贴在 .terminal-body 里（它是 position:relative），不进 flex 流，所以终端的
   行列数不受影响（REQ-TI-06）。left/top 由脚本按 composePlacement 的规则写入。
   z-index 12：在 xterm 和"正在恢复"提示(10)之上，在总览浮层(15)、回看视口(30)、搜索条(60)之下 ——
   它们打开时本来就是接管整个终端的模式，不该被一个输入框盖住。 */
.compose-bar-desktop {
  position: absolute;
  z-index: 12;
  width: min(820px, calc(100% - 16px));
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
  font-size: 0.72rem;
  color: #7a7a96;
  white-space: nowrap;
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
  max-height: 200px;
  overflow-y: auto;
  background: #12131e;
  border-bottom: 1px solid var(--cbd-border);
  padding: 6px 10px;
}
.cbd-panel-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.cbd-panel-title { font-size: 0.68rem; color: #666; text-transform: uppercase; letter-spacing: 0.5px; }
.cbd-panel-save {
  background: #1a3a5a; color: #80c8ff; border: 1px solid #2a5a8a;
  border-radius: 4px; padding: 2px 8px; font-size: 0.68rem; cursor: pointer;
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
  /* JS autoResize() applies the precise cap (maxLines=13); this is just a fallback ceiling. */
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
