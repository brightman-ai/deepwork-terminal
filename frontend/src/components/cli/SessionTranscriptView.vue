<script setup lang="ts">
/**
 * SessionTranscriptView — Structured `💬 Chat` companion view for a terminal session tab
 * (`api-contracts.md` §3, §4.2; `REQ-TW-01`, `REQ-TW-06`).
 *
 * Consumes `GET /sessions/{id}/transcript`, renders structured user/assistant turns with
 * clickable `📄 file:line` pills, and mounts a docked composer sharing `useComposeLenses`
 * (`sharedDraft`, `activeLenses`, `refChips`, 6s `undoClear`) with `>_ CLI` mode while
 * submitting directly to `POST /sessions/{id}/input`.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RefreshCw, RotateCcw, Trash2, Terminal } from 'lucide-vue-next'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import {
  useComposeLenses,
  type ComposePhrase,
} from '@terminal/composables/cli/composeLenses'
import { renderMarkdown } from '@terminal/utils/markdown'
import { findTerminalFileLinks } from '@terminal/components/terminal-session/terminalFileLinks'

export interface TranscriptTurn {
  id: string
  role: 'user' | 'assistant'
  text: string
  timestamp?: string
  files?: string[]
  optimistic?: boolean
}

export interface SessionTranscriptResponse {
  sessionId: string
  cwd: string
  agentTool: string
  turns: TranscriptTurn[]
}

const props = withDefaults(
  defineProps<{
    sessionId: string
    tabTitle?: string
    cwd?: string
    profileLabel?: string
    httpBase?: string
    authToken?: string
    isRemote?: boolean
    active?: boolean
    pollIntervalMs?: number
    autoPostInput?: boolean
  }>(),
  {
    tabTitle: '',
    cwd: '',
    profileLabel: '',
    httpBase: '',
    authToken: '',
    isRemote: false,
    active: true,
    pollIntervalMs: 3500,
    autoPostInput: true,
  },
)

const emit = defineEmits<{
  (e: 'open-file', payload: { path: string; line: number }): void
  (e: 'open-center-preview', path: string): void
  (e: 'switch-to-cli'): void
  (e: 'send', text: string): void
}>()

const lenses = useComposeLenses(() => props.sessionId)

const loading = ref(false)
const sending = ref(false)
const errorMsg = ref('')
const resolvedCwd = ref('')
const agentTool = ref('')
const serverTurns = ref<TranscriptTurn[]>([])
const optimisticTurns = ref<TranscriptTurn[]>([])

const scrollEl = ref<HTMLElement | null>(null)
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const composing = ref(false)

let fetchAbort: AbortController | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null
let followupTimer: ReturnType<typeof setTimeout> | null = null

const displayTurns = computed<TranscriptTurn[]>(() => {
  const base = serverTurns.value
  const pending = optimisticTurns.value.filter(
    (opt) => !base.some((t) => t.role === 'user' && t.text.trim() === opt.text.trim()),
  )
  return [...base, ...pending]
})

function escapeHtml(raw: string): string {
  return (raw || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

function formatAssistantHtml(text: string): string {
  if (!text) return ''
  try {
    if (typeof window !== 'undefined' && typeof document !== 'undefined') {
      const rendered = renderMarkdown(text)
      if (rendered.html) return rendered.html
    }
  } catch {
    // Fallback to safe escaped preformatted text
  }
  return `<div class="stv-plain">${escapeHtml(text)}</div>`
}

function collectTurnFiles(turn: TranscriptTurn): Array<{ raw: string; path: string; line: number }> {
  const seen = new Set<string>()
  const out: Array<{ raw: string; path: string; line: number }> = []

  const pushRef = (token: string, explicitLine?: number) => {
    const clean = token.trim().replace(/^@+/, '')
    if (!clean) return
    const colonMatch = /^(.*?):(\d+)(?::\d+)?$/.exec(clean)
    const path = colonMatch ? colonMatch[1] : clean
    const line = explicitLine ?? (colonMatch ? Number(colonMatch[2]) || 1 : 1)
    const key = `${path}:${line}`
    if (!path || seen.has(key)) return
    seen.add(key)
    out.push({ raw: line > 1 ? `${path}:${line}` : path, path, line })
  }

  for (const f of turn.files ?? []) {
    pushRef(f)
  }

  if (turn.role === 'assistant' && turn.text && out.length < 8) {
    for (const link of findTerminalFileLinks(turn.text)) {
      if (out.length >= 8) break
      pushRef(link.path, link.lineStart)
    }
  }

  return out
}

function scrollToBottom(force = false): void {
  const el = scrollEl.value
  if (!el) return
  const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 140
  if (force || nearBottom) {
    el.scrollTop = el.scrollHeight
  }
}

function sessionFetch(path: string, init?: RequestInit): Promise<Response> {
  if (props.isRemote) {
    if (!props.httpBase) {
      return Promise.reject(new Error('远程终端暂不可达，请切回 >_ CLI 终端查看'))
    }
    const headers = new Headers(init?.headers)
    if (props.authToken) headers.set('X-CLI-Auth', props.authToken)
    return fetch(props.httpBase + cliApi(path), { ...init, headers })
  }
  const { cliFetch } = useCliAuth()
  return cliFetch(cliApi(path), init)
}

async function fetchTranscript(silent = false): Promise<void> {
  if (!props.sessionId) return
  fetchAbort?.abort()
  const controller = new AbortController()
  fetchAbort = controller
  if (!silent) loading.value = true
  errorMsg.value = ''

  try {
    const resp = await sessionFetch(
      `/sessions/${encodeURIComponent(props.sessionId)}/transcript`,
      { signal: controller.signal, cache: 'no-store' },
    )
    if (controller.signal.aborted || fetchAbort !== controller) return
    if (!resp.ok) {
      errorMsg.value = `无法加载对话记录（HTTP ${resp.status}）`
      return
    }
    const data = (await resp.json()) as Partial<SessionTranscriptResponse>
    const prevCount = displayTurns.value.length
    resolvedCwd.value = data.cwd || props.cwd || ''
    agentTool.value = data.agentTool || ''
    serverTurns.value = Array.isArray(data.turns) ? data.turns : []

    // Prune optimistic turns already echoed by server transcript
    optimisticTurns.value = optimisticTurns.value.filter(
      (opt) =>
        !serverTurns.value.some((t) => t.role === 'user' && t.text.trim() === opt.text.trim()),
    )

    if (displayTurns.value.length > prevCount) {
      void nextTick(() => scrollToBottom(prevCount === 0))
    }
  } catch (err) {
    if (controller.signal.aborted) return
    errorMsg.value = err instanceof Error ? err.message : '读取会话记录失败'
  } finally {
    if (fetchAbort === controller) {
      fetchAbort = null
      loading.value = false
    }
  }
}

function startPolling(): void {
  stopPolling()
  if (!props.active || !props.sessionId || props.pollIntervalMs <= 0) return
  pollTimer = setInterval(() => {
    if (props.active && props.sessionId) {
      void fetchTranscript(true)
    }
  }, props.pollIntervalMs)
}

function stopPolling(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(
  () => props.sessionId,
  () => {
    serverTurns.value = []
    optimisticTurns.value = []
    errorMsg.value = ''
    if (props.active && props.sessionId) {
      void fetchTranscript(false)
      startPolling()
    }
  },
  { immediate: true },
)

watch(
  () => props.active,
  (isActive) => {
    if (isActive) {
      void fetchTranscript(serverTurns.value.length > 0)
      startPolling()
    } else {
      stopPolling()
      fetchAbort?.abort()
    }
  },
)

onMounted(() => {
  if (props.active && props.sessionId) {
    startPolling()
  }
})

onBeforeUnmount(() => {
  stopPolling()
  fetchAbort?.abort()
  if (followupTimer) clearTimeout(followupTimer)
})

function autoResizeTextarea(): void {
  const ta = textareaRef.value
  if (!ta) return
  ta.style.height = 'auto'
  ta.style.height = `${Math.min(Math.max(ta.scrollHeight, 40), 180)}px`
}

function onDraftInput(ev: Event): void {
  const val = (ev.target as HTMLTextAreaElement).value
  lenses.sharedDraft.value = val
  autoResizeTextarea()
  if (!composing.value) {
    lenses.checkSlashTrigger(val)
  }
}

watch(
  () => lenses.sharedDraft.value,
  () => {
    void nextTick(autoResizeTextarea)
  },
)

function onInsertTemplate(phrase: ComposePhrase): void {
  const ta = textareaRef.value
  const cur = lenses.sharedDraft.value
  const start = ta ? ta.selectionStart : cur.length
  const end = ta ? ta.selectionEnd : start
  const inserted = lenses.insertTemplateAtRange(cur, start, end, phrase)
  void nextTick(() => {
    autoResizeTextarea()
    const el = textareaRef.value
    if (!el) return
    el.focus()
    try {
      el.setSelectionRange(inserted.cursor, inserted.cursor)
    } catch {}
  })
}

function onComposerKeydown(e: KeyboardEvent): void {
  if (e.isComposing || composing.value || (e as KeyboardEvent & { keyCode?: number }).keyCode === 229) {
    return
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void handleSend()
  } else if (e.key === 'Escape' && lenses.arsenalOpen.value) {
    e.preventDefault()
    lenses.closeArsenal()
  }
}

async function postToSessionInput(text: string): Promise<boolean> {
  if (!props.sessionId || !text) return false
  try {
    const pasteBody = text.includes('\n') ? `\x1b[200~${text}\x1b[201~` : text
    const r1 = await sessionFetch(
      `/sessions/${encodeURIComponent(props.sessionId)}/input`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        body: pasteBody,
      },
    )
    if (!r1.ok) return false
    const r2 = await sessionFetch(
      `/sessions/${encodeURIComponent(props.sessionId)}/input`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        body: '\r',
      },
    )
    return r2.ok
  } catch {
    return false
  }
}

async function handleSend(): Promise<void> {
  if (sending.value) return
  const payload = lenses.assemblePayload()
  if (!payload) return

  const prevDraft = lenses.sharedDraft.value
  const prevLenses = lenses.activeLenses.value.slice()
  const prevRefs = lenses.refChips.value.slice()
  const optId = `opt-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`

  sending.value = true
  errorMsg.value = ''
  optimisticTurns.value.push({
    id: optId,
    role: 'user',
    text: payload,
    timestamp: '刚刚',
    optimistic: true,
  })
  lenses.clearAfterSend()
  void nextTick(() => {
    autoResizeTextarea()
    scrollToBottom(true)
  })

  try {
    if (props.autoPostInput && props.sessionId) {
      const ok = await postToSessionInput(payload)
      if (!ok) {
        // F2: Never leave a phantom sent turn on failure — roll back optimistic turn & restore draft.
        optimisticTurns.value = optimisticTurns.value.filter((t) => t.id !== optId)
        lenses.sharedDraft.value = prevDraft
        for (const id of prevLenses) {
          if (!lenses.isLensActive(id)) lenses.toggleLens(id)
        }
        for (const chip of prevRefs) {
          lenses.addRefChip(chip)
        }
        errorMsg.value = '发送失败：无法投递到终端会话，请检查连接后重试'
        void nextTick(autoResizeTextarea)
        return
      }
      if (followupTimer) clearTimeout(followupTimer)
      followupTimer = setTimeout(() => {
        void fetchTranscript(true)
      }, 900)
    }
    emit('send', payload)
  } finally {
    sending.value = false
  }
}

function handleClear(): void {
  lenses.clearWithUndo()
  void nextTick(autoResizeTextarea)
}

function handleUndo(): void {
  lenses.undoClear()
  void nextTick(() => {
    autoResizeTextarea()
    textareaRef.value?.focus?.()
  })
}

function onFilePillClick(path: string, line: number): void {
  emit('open-file', { path, line })
  emit('open-center-preview', path)
}

defineExpose({
  refresh: () => fetchTranscript(false),
})
</script>

<template>
  <div class="stv-root" data-testid="session-transcript-view">
    <!-- Top companion header -->
    <div class="stv-header">
      <div class="stv-meta">
        <span class="stv-mode-badge">💬 Chat 结构化视图</span>
        <strong v-if="tabTitle" class="stv-title">{{ tabTitle }}</strong>
        <span v-if="agentTool" class="stv-tool-badge" data-testid="transcript-agent-tool">
          {{ agentTool }}
        </span>
        <span v-if="profileLabel" class="stv-sub">{{ profileLabel }}</span>
        <span v-if="resolvedCwd || cwd" class="stv-cwd" :title="resolvedCwd || cwd">
          {{ resolvedCwd || cwd }}
        </span>
      </div>

      <div class="stv-header-actions">
        <button
          type="button"
          class="stv-btn"
          title="切换到 >_ CLI 实时终端视图（同一会话不断连）"
          data-testid="transcript-switch-cli"
          @click="$emit('switch-to-cli')"
        >
          <Terminal class="size-3.5" />
          &gt;_ CLI 终端
        </button>
        <button
          type="button"
          class="stv-btn stv-btn--icon"
          title="刷新对话记录"
          data-testid="transcript-refresh"
          :disabled="loading"
          @click="fetchTranscript(false)"
        >
          <RefreshCw class="size-3.5" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </div>

    <!-- Scrollable structured turn stream -->
    <div ref="scrollEl" class="stv-stream" data-testid="transcript-turns">
      <div v-if="loading && !displayTurns.length" class="stv-state" data-testid="transcript-loading">
        正在同步会话对话流…
      </div>

      <div v-else-if="errorMsg && !displayTurns.length" class="stv-state stv-state--error" data-testid="transcript-error">
        <span>{{ errorMsg }}</span>
        <button type="button" class="stv-btn" @click="fetchTranscript(false)">重试</button>
      </div>

      <div v-else-if="!displayTurns.length" class="stv-state" data-testid="transcript-empty">
        <p>当前会话暂无结构化对话轮次</p>
        <p class="stv-state-sub">在下方输入框直接发送指令，或切回 &gt;_ CLI 查看实时终端缓冲</p>
      </div>

      <template v-else>
        <div
          v-for="turn in displayTurns"
          :key="turn.id"
          class="stv-turn"
          :class="turn.role === 'user' ? 'stv-turn--user' : 'stv-turn--assistant'"
          :data-testid="`transcript-turn-${turn.role}`"
        >
          <template v-if="turn.role === 'user'">
            <div class="stv-user-bubble">
              <div class="stv-user-text">{{ turn.text }}</div>
              <div v-if="turn.timestamp" class="stv-turn-time">{{ turn.timestamp }}</div>
            </div>
          </template>

          <template v-else>
            <div class="stv-agent-card">
              <div class="stv-agent-head">
                <strong>🤖 {{ agentTool || tabTitle || 'Agent' }}</strong>
                <span v-if="turn.timestamp" class="stv-turn-time">{{ turn.timestamp }}</span>
              </div>
              <div class="stv-agent-body" v-html="formatAssistantHtml(turn.text)" />
              <div
                v-if="collectTurnFiles(turn).length > 0"
                class="stv-file-pills"
                data-testid="transcript-file-pills"
              >
                <button
                  v-for="f in collectTurnFiles(turn)"
                  :key="f.raw"
                  type="button"
                  class="stv-file-pill"
                  :data-testid="`transcript-file-pill-${f.raw}`"
                  :title="`点击在中央主画布打开 ${f.raw}`"
                  @click="onFilePillClick(f.path, f.line)"
                >📄 {{ f.raw }}</button>
              </div>
            </div>
          </template>
        </div>
      </template>
    </div>

    <!-- Docked Composer sharing composeLenses + draft with >_ CLI mode -->
    <div class="stv-composer" data-testid="transcript-composer">
      <!-- Phrase Arsenal popover -->
      <div
        v-if="lenses.arsenalOpen.value"
        class="stv-arsenal"
        data-testid="transcript-phrase-arsenal"
      >
        <div class="stv-pa-head">
          <div class="stv-pa-cats">
            <button
              type="button"
              class="stv-pa-cat"
              :class="{ 'is-active': lenses.phraseFilter.value === 'all' }"
              data-testid="transcript-phrase-cat-all"
              @click="lenses.phraseFilter.value = 'all'"
            >全部</button>
            <button
              type="button"
              class="stv-pa-cat"
              :class="{ 'is-active': lenses.phraseFilter.value === 'lens' }"
              data-testid="transcript-phrase-cat-lens"
              @click="lenses.phraseFilter.value = 'lens'"
            >🟣 叠加透镜</button>
            <button
              type="button"
              class="stv-pa-cat"
              :class="{ 'is-active': lenses.phraseFilter.value === 'tpl' }"
              data-testid="transcript-phrase-cat-tpl"
              @click="lenses.phraseFilter.value = 'tpl'"
            >🟢 动作模板</button>
          </div>
          <input
            v-model="lenses.phraseQuery.value"
            type="text"
            class="stv-pa-search"
            placeholder="搜短语…"
          />
          <button type="button" class="stv-btn stv-btn--icon" @click="lenses.closeArsenal()">✕</button>
        </div>

        <div class="stv-pa-grid">
          <div
            v-for="p in lenses.filteredPhrases.value"
            :key="p.id"
            class="stv-pa-card"
            :class="{ 'is-on': p.kind === 'lens' && lenses.isLensActive(p.id) }"
          >
            <div class="stv-pa-card-top">
              <span class="stv-pa-short">{{ p.short }}</span>
              <span class="stv-pa-kind" :class="`is-${p.kind}`">{{ p.kind === 'lens' ? '透镜' : '模板' }}</span>
            </div>
            <div class="stv-pa-desc">{{ p.desc }}</div>
            <div class="stv-pa-actions">
              <button
                v-if="p.kind === 'lens'"
                type="button"
                class="stv-pa-btn"
                :data-testid="`transcript-toggle-lens-${p.id}`"
                @click="lenses.toggleLens(p.id)"
              >{{ lenses.isLensActive(p.id) ? '✓ 已挂载' : '＋ 附加' }}</button>
              <button
                v-else
                type="button"
                class="stv-pa-btn is-tpl"
                :data-testid="`transcript-insert-tpl-${p.id}`"
                @click="onInsertTemplate(p)"
              >↳ 插入光标</button>
            </div>
          </div>
        </div>
      </div>

      <!-- 6s Undo bar -->
      <div
        v-if="lenses.hasUndoBackup.value"
        class="stv-undo-bar"
        data-testid="transcript-undo-bar"
      >
        <span>已清空草稿与挂载透镜（6秒内可恢复）</span>
        <button
          type="button"
          class="stv-undo-btn"
          data-testid="transcript-undo-btn"
          @click="handleUndo"
        >
          <RotateCcw class="size-3" /> 撤销恢复
        </button>
      </div>

      <!-- Toolbar: pinned lenses + arsenal + clear -->
      <div class="stv-composer-bar">
        <div class="stv-pinned-lenses" data-testid="transcript-pinned-lenses">
          <button
            v-for="lens in lenses.pinnedLenses.value"
            :key="lens.id"
            type="button"
            class="stv-lens-pill"
            :class="{ 'is-on': lenses.isLensActive(lens.id) }"
            :data-testid="`transcript-lens-pill-${lens.id}`"
            @click="lenses.toggleLens(lens.id)"
          >{{ lenses.isLensActive(lens.id) ? '✓ ' : '+ ' }}{{ lens.short }}</button>
        </div>

        <div class="stv-composer-tools">
          <button
            type="button"
            class="stv-btn"
            :class="{ 'is-active': lenses.arsenalOpen.value }"
            data-testid="transcript-arsenal-btn"
            @click="lenses.toggleArsenal()"
          >快捷短语 (/)</button>
          <button
            type="button"
            class="stv-btn stv-btn--danger"
            title="清空草稿与挂载透镜（6秒可撤销）"
            data-testid="transcript-clear-btn"
            @click="handleClear"
          >
            <Trash2 class="size-3.5" />
          </button>
        </div>
      </div>

      <!-- Active @file:line & stacked lens chips -->
      <div
        v-if="lenses.hasContextChips.value"
        class="stv-chips-row"
        data-testid="transcript-active-chips"
      >
        <span
          v-for="r in lenses.refChips.value"
          :key="`ref-${r}`"
          class="stv-chip stv-chip--ref"
          :data-testid="`transcript-ref-chip-${r}`"
        >
          📄 @{{ r }}
          <button type="button" class="stv-chip-x" @click="lenses.removeRefChip(r)">✕</button>
        </span>
        <span
          v-for="p in lenses.activeLensObjects.value"
          :key="`lens-${p.id}`"
          class="stv-chip stv-chip--lens"
          :data-testid="`transcript-active-lens-${p.id}`"
        >
          {{ p.short }}
          <button type="button" class="stv-chip-x" @click="lenses.toggleLens(p.id)">✕</button>
        </span>
      </div>

      <!-- Input + Send -->
      <div class="stv-input-row">
        <textarea
          ref="textareaRef"
          :value="lenses.sharedDraft.value"
          class="stv-textarea"
          data-testid="transcript-input"
          placeholder="输入指令发送至当前会话（与 >_ CLI 共享草稿 · 输入 / 唤起短语透镜 · Enter 发送）…"
          @input="onDraftInput"
          @keydown="onComposerKeydown"
          @compositionstart="composing = true"
          @compositionend="composing = false"
        />
        <button
          type="button"
          class="stv-send-btn"
          data-testid="transcript-send-btn"
          :disabled="sending"
          @click="handleSend"
        >{{ sending ? '发送中…' : '发送 ↵' }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stv-root {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
  background: #0d1017;
  color: #e4eaf5;
}

.stv-header {
  flex-shrink: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 14px;
  background: #131722;
  border-bottom: 1px solid #242c3e;
}
.stv-meta {
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
  font-size: 0.75rem;
}
.stv-mode-badge {
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(59, 130, 246, 0.16);
  border: 1px solid rgba(96, 165, 250, 0.38);
  color: #bfdbfe;
  font-size: 0.69rem;
  font-weight: 600;
}
.stv-title {
  color: #f1f5f9;
  font-size: 0.8rem;
}
.stv-tool-badge {
  padding: 1px 6px;
  border-radius: 5px;
  background: #1e2638;
  border: 1px solid #323e59;
  color: #a5b4fc;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.67rem;
  text-transform: uppercase;
}
.stv-sub {
  color: #94a3b8;
  font-size: 0.71rem;
}
.stv-cwd {
  color: #64748b;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.69rem;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.stv-header-actions {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.stv-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 9px;
  border-radius: 6px;
  border: 1px solid #313a52;
  background: #1b2130;
  color: #cbd5e8;
  font-size: 0.72rem;
  cursor: pointer;
}
.stv-btn:hover:not(:disabled) {
  background: #252e44;
  color: #f8fafc;
}
.stv-btn.is-active {
  background: #223458;
  border-color: #5b82d6;
  color: #e0edff;
}
.stv-btn--icon {
  padding: 0 7px;
}
.stv-btn--danger {
  color: #fca5a5;
  border-color: rgba(239, 68, 68, 0.35);
  background: rgba(127, 29, 29, 0.25);
}

.stv-stream {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.stv-state {
  margin: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  text-align: center;
  color: #8c96ae;
  font-size: 0.82rem;
}
.stv-state--error {
  color: #fca5a5;
}
.stv-state-sub {
  font-size: 0.73rem;
  color: #64748b;
}

.stv-turn {
  display: flex;
  width: 100%;
  max-width: 900px;
  margin: 0 auto;
}
.stv-turn--user {
  justify-content: flex-end;
}
.stv-turn--assistant {
  justify-content: flex-start;
}

.stv-user-bubble {
  max-width: min(76%, 680px);
  padding: 10px 13px;
  border-radius: 12px 12px 3px 12px;
  background: #1d3b72;
  border: 1px solid rgba(96, 165, 250, 0.4);
  color: #f0f6ff;
  font-size: 0.84rem;
  line-height: 1.5;
}
.stv-user-text {
  white-space: pre-wrap;
  word-break: break-word;
}

.stv-agent-card {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border-radius: 10px;
  background: #141925;
  border: 1px solid #252e42;
}
.stv-agent-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 0.72rem;
  color: #94a3b8;
}
.stv-turn-time {
  font-size: 0.67rem;
  color: #64748b;
  margin-top: 2px;
}
.stv-agent-body {
  font-size: 0.84rem;
  line-height: 1.62;
  color: #dce4f2;
  word-break: break-word;
}
.stv-agent-body :deep(pre) {
  margin: 6px 0;
  padding: 8px 10px;
  border-radius: 6px;
  background: #0b0e16;
  border: 1px solid #232b3e;
  overflow-x: auto;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.76rem;
}
.stv-agent-body :deep(code) {
  padding: 1px 4px;
  border-radius: 4px;
  background: rgba(148, 163, 184, 0.14);
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.78rem;
}
.stv-plain {
  white-space: pre-wrap;
}

.stv-file-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 2px;
}
.stv-file-pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 9px;
  border-radius: 999px;
  border: 1px solid rgba(96, 165, 250, 0.4);
  background: rgba(37, 99, 235, 0.14);
  color: #bfdbfe;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.71rem;
  cursor: pointer;
  transition: background 0.12s;
}
.stv-file-pill:hover {
  background: rgba(37, 99, 235, 0.28);
}

/* Docked composer */
.stv-composer {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: #131722;
  border-top: 1px solid #262e42;
  padding: 8px 14px 10px;
  gap: 6px;
}

.stv-arsenal {
  max-height: 220px;
  overflow-y: auto;
  padding: 8px 10px;
  border-radius: 8px;
  background: #0f131d;
  border: 1px solid #2d364f;
}
.stv-pa-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
}
.stv-pa-cats {
  display: inline-flex;
  gap: 4px;
}
.stv-pa-cat {
  height: 24px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid #323b56;
  background: #181e2e;
  color: #a8b3cc;
  font-size: 0.72rem;
  cursor: pointer;
}
.stv-pa-cat.is-active {
  background: #26365a;
  border-color: #60a5fa;
  color: #f0f6ff;
}
.stv-pa-search {
  flex: 1;
  min-width: 120px;
  height: 24px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid #323b56;
  background: #161b29;
  color: #e2e8f0;
  font-size: 0.73rem;
  outline: none;
}
.stv-pa-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 6px;
}
.stv-pa-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 7px 9px;
  border-radius: 7px;
  border: 1px solid #29324a;
  background: #161c2c;
}
.stv-pa-card.is-on {
  border-color: rgba(176, 132, 255, 0.7);
  background: rgba(112, 74, 196, 0.16);
}
.stv-pa-card-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.stv-pa-short {
  font-size: 0.75rem;
  font-weight: 600;
  color: #f1f5f9;
}
.stv-pa-kind {
  font-size: 0.64rem;
  padding: 1px 6px;
  border-radius: 999px;
}
.stv-pa-kind.is-lens {
  background: rgba(154, 106, 255, 0.2);
  color: #d9bfff;
}
.stv-pa-kind.is-tpl {
  background: rgba(61, 214, 140, 0.16);
  color: #96f2c2;
}
.stv-pa-desc {
  font-size: 0.68rem;
  color: #94a3b8;
  line-height: 1.35;
}
.stv-pa-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
}
.stv-pa-btn {
  height: 22px;
  padding: 0 8px;
  border-radius: 5px;
  border: 1px solid #485688;
  background: #212c4d;
  color: #cde0ff;
  font-size: 0.69rem;
  cursor: pointer;
}
.stv-pa-btn.is-tpl {
  border-color: rgba(61, 214, 140, 0.45);
  background: rgba(34, 120, 82, 0.28);
  color: #adf5ce;
}

.stv-undo-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 5px 10px;
  border-radius: 6px;
  background: #241a10;
  border: 1px solid #5a3a20;
  color: #e6bf8a;
  font-size: 0.73rem;
}
.stv-undo-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 9px;
  border-radius: 5px;
  background: #4a3018;
  border: 1px solid #7a5028;
  color: #ffd08f;
  font-size: 0.72rem;
  font-weight: 600;
  cursor: pointer;
}

.stv-composer-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}
.stv-pinned-lenses {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 5px;
}
.stv-lens-pill {
  height: 24px;
  padding: 0 8px;
  border-radius: 999px;
  border: 1px solid rgba(168, 128, 255, 0.35);
  background: rgba(132, 90, 220, 0.14);
  color: #d6c2ff;
  font-size: 0.71rem;
  cursor: pointer;
}
.stv-lens-pill.is-on {
  background: rgba(145, 96, 245, 0.34);
  border-color: #b48cff;
  color: #f4ecff;
}
.stv-composer-tools {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.stv-chips-row {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}
.stv-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 0.71rem;
}
.stv-chip--ref {
  background: rgba(68, 132, 244, 0.16);
  border: 1px solid rgba(98, 156, 255, 0.42);
  color: #bcd6ff;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
}
.stv-chip--lens {
  background: rgba(145, 96, 245, 0.22);
  border: 1px solid rgba(180, 140, 255, 0.5);
  color: #eadbff;
}
.stv-chip-x {
  background: none;
  border: none;
  color: inherit;
  cursor: pointer;
  font-size: 0.68rem;
}

.stv-input-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}
.stv-textarea {
  flex: 1;
  min-height: 40px;
  max-height: 180px;
  padding: 8px 12px;
  border-radius: 8px;
  border: 1px solid #35405c;
  background: #1a2030;
  color: #f1f5f9;
  font-size: 0.88rem;
  line-height: 1.45;
  resize: none;
  outline: none;
}
.stv-textarea:focus {
  border-color: #4a80d8;
  box-shadow: 0 0 0 2px rgba(74, 128, 216, 0.2);
}
.stv-send-btn {
  height: 40px;
  padding: 0 16px;
  border-radius: 8px;
  border: none;
  background: #1a4fd8;
  color: #ffffff;
  font-size: 0.83rem;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.stv-send-btn:hover:not(:disabled) {
  background: #2563eb;
}
.stv-send-btn:disabled {
  opacity: 0.55;
  cursor: default;
}
</style>
