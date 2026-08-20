<template>
  <div class="compose-bar-desktop" data-testid="compose-bar-desktop">
    <!-- Toolbar: DelAll + Snippets + History. No cursor-nav buttons here — unlike mobile, a
         desktop user already has arrow keys / Home / End on the keyboard sitting under the
         textarea; on-screen duplicates would be pure clutter. See ComposeBar.vue (mobile). -->
    <div class="cbd-toolbar">
      <button class="cbd-tool cbd-tool--esc" type="button" title="退出输入条 (Esc)" @click="$emit('close')">Esc</button>
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
      <span class="cbd-send-hint">{{ sendHint }} 发送 · Shift+Enter 换行</span>
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
        placeholder="Shift+Enter 换行，Ctrl/Cmd+Enter 发送"
        @input="onInput"
        @focus="onTextareaFocus"
        @keydown="onKeydown"
      />
      <button class="cbd-btn-send" type="button" title="发送" @click="send">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="22" y1="2" x2="11" y2="13"/><polygon points="22,2 15,22 11,13 2,9"/>
        </svg>
        发送
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * ComposeBarDesktop — the desktop skin over useComposeBox (see ComposeBar.vue for the mobile
 * one). Differences from mobile, each tied to a real desktop trait, not just "smaller mobile UI":
 *  - No cursor-nav buttons: a hardware keyboard already has arrow keys / Home / End.
 *  - No soft-keyboard viewport dodging: desktop has no visual-viewport keyboard inset to correct.
 *  - Taller box (maxLines): desktop's vertical space isn't scarce the way a phone's is.
 *  - Ctrl/Cmd+Enter sends (Enter alone stays newline, matching mobile's convention) — the
 *    idiomatic desktop compose gesture (Slack/Discord/etc.), absent on mobile because there's no
 *    keyboard modifier to reach for one-handed.
 */
import { Copy, Check, Trash2, RotateCcw } from 'lucide-vue-next'
import { useComposeBox } from '@terminal/composables/cli/useComposeBox'

const props = defineProps<{ draft?: string }>()

const emit = defineEmits<{
  (e: 'send', text: string): void
  (e: 'close'): void
}>()

const IS_MAC = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '')
const sendHint = IS_MAC ? '⌘+Enter' : 'Ctrl+Enter'

const {
  text, textareaRef,
  showSnippets, showHistory, snippets, history,
  copyFeedback, showUndo,
  toggleSnippets, toggleHistory, saveSnippet, insertSnippet, deleteSnippet, clearHistory, insertFromHistory,
  onInput, onTextareaFocus,
  send, copyAll, clearAll, undoClear,
} = useComposeBox({
  maxLines: 13, // desktop screens have vertical room mobile's ~5-line cap was never about
  diagnosticSurface: 'compose-textarea-desktop',
  draft: () => props.draft,
  focusEl: (el) => el?.focus(),
  // No onFocusSideEffects — desktop has no soft-keyboard viewport to correct for.
  emit: { send: (t) => emit('send', t) },
})

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    send()
  } else if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  }
}
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
/* A real flex row, NOT an absolute overlay — same model as mobile's .bottom-bar (see
   CliTerminalSurface.vue): it takes real layout space and pushes .terminal-body up, so it never
   covers the terminal content the user might be looking at while composing. .terminal-body's
   existing ResizeObserver-driven xterm fit() picks up the resulting height change for free —
   this is exactly the geometry change class it already handles (drawer open/close, mobile
   bottom-bar visibility), not a new case. */
.compose-bar-desktop {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--cbd-bg);
  border-top: 1px solid var(--cbd-border);
  box-shadow: 0 -6px 24px rgba(0,0,0,0.35);
}

.cbd-toolbar {
  display: flex;
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
  padding: 8px 10px 10px;
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
