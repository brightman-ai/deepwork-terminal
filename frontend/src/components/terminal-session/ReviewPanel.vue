<script setup lang="ts">
/**
 * ReviewPanel — the drawer's 审核 panel (`git diff` + inline line-level review notes).
 *
 * Shows `git diff` for the workbench cwd's repository: the changed-file list (status +
 * path + +N/-N) with each file's unified diff, PC quick preview/split actions, and
 * inline diff line note editor (`✨ 发送批注`) that delivers structured `@file:line`
 * review feedback directly to the active terminal session.
 */
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { RefreshCw, ChevronRight } from 'lucide-vue-next'
import { gitDiff, type GitDiffFile, type GitDiffResult } from '@terminal/api/files'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { addSessionRefChip } from '@terminal/composables/cli/composeLenses'
import { fuzzyMatch } from '@terminal/utils/fuzzyMatch'
import DrawerSearchBox from '@terminal/components/terminal-session/DrawerSearchBox.vue'

export interface DiffLineNote {
  id: string
  file: string
  line: number
  rowIndex: number
  codeSnippet: string
  text: string
  sent: boolean
}

const props = withDefaults(
  defineProps<{
    sessionId: string
    cwd: string
    active?: boolean
    autoPostNotes?: boolean
  }>(),
  { active: true, autoPostNotes: true },
)

const emit = defineEmits<{
  (e: 'open-file', path: string): void
  (e: 'open-center-preview', path: string): void
  (e: 'split-file', path: string): void
  (e: 'send-notes', payload: { text: string; notes: DiffLineNote[] }): void
}>()

const isMobileViewport = useMediaQuery('(max-width: 768px)')

const loading = ref(false)
const result = ref<GitDiffResult | null>(null)
const expanded = ref<string>('') // path of the currently-open file (accordion, one at a time)
const query = ref('') // client-side filename/path filter over the changed-file list (S-3)

// ── Inline line notes state ──
const notes = ref<DiffLineNote[]>([])
const editingNote = ref<{
  file: string
  line: number
  rowIndex: number
  codeSnippet: string
  text: string
} | null>(null)
const noteInputRef = ref<HTMLInputElement | null>(null)
const sendingNotes = ref(false)
const toastText = ref('')
let toastTimer: ReturnType<typeof setTimeout> | null = null

let request: AbortController | null = null
let needsLoad = true

function showToast(msg: string): void {
  toastText.value = msg
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toastText.value = ''
  }, 2200)
}

async function load(): Promise<void> {
  if (!props.active) return
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  try {
    const data = await gitDiff(props.sessionId, props.cwd, current.signal)
    if (request === current && !current.signal.aborted) {
      result.value = data
      needsLoad = false
    }
  } finally {
    if (request === current) {
      request = null
      loading.value = false
    }
  }
}

function reload(): void {
  needsLoad = true
  result.value = null
  expanded.value = ''
  query.value = ''
  editingNote.value = null
  void load()
}
watch(() => props.sessionId, reload)
watch(() => props.cwd, reload)
watch(
  () => props.active,
  (active) => {
    if (active && needsLoad) void load()
    else if (!active) {
      request?.abort()
      request = null
      loading.value = false
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  request?.abort()
  if (toastTimer) clearTimeout(toastTimer)
})

function toggle(path: string): void {
  expanded.value = expanded.value === path ? '' : path
}

const files = computed<GitDiffFile[]>(() => result.value?.files ?? [])
const filteredFiles = computed<GitDiffFile[]>(() =>
  files.value.filter((f) => fuzzyMatch(query.value, f.path)),
)
const unsentNotes = computed(() => notes.value.filter((n) => !n.sent))

function resolveRepoFilePath(relPath: string): string {
  const root = (result.value?.root || props.cwd || '').replace(/\/+$/, '')
  return root ? `${root}/${relPath}` : relPath
}

function openWorkbenchFileFor(relPath: string): void {
  emit('open-file', resolveRepoFilePath(relPath))
}

function splitFileFor(relPath: string): void {
  emit('split-file', resolveRepoFilePath(relPath))
}

// ── status letter → label + color chip ──
function statusMeta(s: string): { label: string; cls: string } {
  switch (s) {
    case 'M':
      return {
        label: 'M',
        cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/40',
      }
    case 'A':
      return {
        label: 'A',
        cls: 'bg-green-500/15 text-green-600 dark:text-green-400 border-green-500/40',
      }
    case 'D':
      return { label: 'D', cls: 'bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/40' }
    case 'R':
      return { label: 'R', cls: 'bg-sky-500/15 text-sky-600 dark:text-sky-400 border-sky-500/40' }
    case '?':
      return { label: '?', cls: 'bg-muted text-muted-foreground border-border' }
    default:
      return { label: s || '·', cls: 'bg-muted text-muted-foreground border-border' }
  }
}

function baseName(path: string): string {
  const i = path.lastIndexOf('/')
  return i >= 0 ? path.slice(i + 1) : path
}
function parentDir(path: string): string {
  const i = path.lastIndexOf('/')
  return i >= 0 ? path.slice(0, i) : ''
}

// ── unified-diff line classification + line number tracking ──
const MAX_DIFF_LINES = 2000
const HUNK_RE = /^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/

interface DiffLine {
  index: number
  text: string
  cls: string
  lineNum: number | null
  commentable: boolean
}

function diffLines(diff: string): DiffLine[] {
  if (!diff) return []
  const raw = diff.split('\n')
  const clipped = raw.length > MAX_DIFF_LINES
  const lines = clipped ? raw.slice(0, MAX_DIFF_LINES) : raw
  let oldLn = 1
  let newLn = 1
  const out: DiffLine[] = []

  for (let i = 0; i < lines.length; i++) {
    const text = lines[i]
    const hunk = HUNK_RE.exec(text)
    if (hunk) {
      oldLn = Number(hunk[1]) || 1
      newLn = Number(hunk[2]) || 1
      out.push({ index: i, text, cls: 'rp-ln-hunk', lineNum: null, commentable: false })
      continue
    }
    const cls = lineClass(text)
    if (cls === 'rp-ln-meta') {
      out.push({ index: i, text, cls, lineNum: null, commentable: false })
    } else if (cls === 'rp-ln-add') {
      const ln = newLn++
      out.push({ index: i, text, cls, lineNum: ln, commentable: true })
    } else if (cls === 'rp-ln-del') {
      const ln = oldLn++
      out.push({ index: i, text, cls, lineNum: ln, commentable: true })
    } else {
      const ln = newLn++
      oldLn++
      out.push({ index: i, text, cls, lineNum: ln, commentable: true })
    }
  }

  if (clipped) {
    out.push({
      index: lines.length,
      text: `… (超 ${MAX_DIFF_LINES} 行，已折叠)`,
      cls: 'rp-ln-meta',
      lineNum: null,
      commentable: false,
    })
  }
  return out
}

function lineClass(line: string): string {
  if (line.startsWith('@@')) return 'rp-ln-hunk'
  if (
    line.startsWith('diff --git') ||
    line.startsWith('index ') ||
    line.startsWith('+++') ||
    line.startsWith('---') ||
    line.startsWith('new file') ||
    line.startsWith('deleted file') ||
    line.startsWith('rename ') ||
    line.startsWith('similarity ') ||
    line.startsWith('old mode') ||
    line.startsWith('new mode') ||
    line.startsWith('Binary files') ||
    line.startsWith('\\ No newline')
  ) {
    return 'rp-ln-meta'
  }
  if (line.startsWith('+')) return 'rp-ln-add'
  if (line.startsWith('-')) return 'rp-ln-del'
  return 'rp-ln-ctx'
}

// ── Inline note actions ──
function openLineNoteEditor(file: string, ln: DiffLine): void {
  if (!ln.commentable || ln.lineNum == null) return
  editingNote.value = {
    file,
    line: ln.lineNum,
    rowIndex: ln.index,
    codeSnippet: ln.text.slice(1).trim().slice(0, 80),
    text: '',
  }
  void nextTick(() => {
    noteInputRef.value?.focus?.()
  })
}

function cancelInlineNote(): void {
  editingNote.value = null
}

function saveInlineNote(): DiffLineNote | null {
  const ed = editingNote.value
  if (!ed) return null
  const text = ed.text.trim()
  if (!text) return null
  const created: DiffLineNote = {
    id: `note-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
    file: ed.file,
    line: ed.line,
    rowIndex: ed.rowIndex,
    codeSnippet: ed.codeSnippet,
    text,
    sent: false,
  }
  notes.value.push(created)
  editingNote.value = null
  return created
}

function deleteNote(noteId: string): void {
  notes.value = notes.value.filter((n) => n.id !== noteId)
}

function notesForRow(file: string, rowIndex: number): DiffLineNote[] {
  return notes.value.filter((n) => n.file === file && n.rowIndex === rowIndex)
}

async function postNotesToSession(text: string): Promise<boolean> {
  if (!props.sessionId || !text) return false
  const { cliFetch } = useCliAuth()
  try {
    const pasteBody = text.includes('\n') ? `\x1b[200~${text}\x1b[201~` : text
    const r1 = await cliFetch(cliApi(`/sessions/${encodeURIComponent(props.sessionId)}/input`), {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
      body: pasteBody,
    })
    if (!r1.ok) return false
    const r2 = await cliFetch(cliApi(`/sessions/${encodeURIComponent(props.sessionId)}/input`), {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
      body: '\r',
    })
    return r2.ok
  } catch {
    return false
  }
}

async function sendNotesBatch(batch: DiffLineNote[]): Promise<void> {
  if (!batch.length || sendingNotes.value) return
  const formatted = [
    '请处理以下 Diff 行级批注：',
    ...batch.map((n) => `• @${n.file}:${n.line} — ${n.text}`),
  ].join('\n')

  for (const n of batch) {
    addSessionRefChip(props.sessionId, `${n.file}:${n.line}`)
  }

  sendingNotes.value = true
  try {
    if (props.autoPostNotes && props.sessionId) {
      await postNotesToSession(formatted)
    }
    for (const n of batch) {
      n.sent = true
    }
    emit('send-notes', { text: formatted, notes: [...batch] })
    showToast(`✨ 已发送 ${batch.length} 条批注至当前会话`)
  } finally {
    sendingNotes.value = false
  }
}

async function saveAndSendSingleNote(): Promise<void> {
  const created = saveInlineNote()
  if (created) {
    await sendNotesBatch([created])
  }
}

async function sendAllUnsentNotes(): Promise<void> {
  if (editingNote.value?.text.trim()) {
    saveInlineNote()
  }
  await sendNotesBatch(unsentNotes.value)
}
</script>

<template>
  <div class="rp relative flex flex-col h-full bg-background text-foreground" data-testid="review-panel">
    <!-- header: summary + send notes + refresh -->
    <div class="shrink-0 flex items-center gap-2 border-b border-border bg-card px-3 py-2">
      <span class="text-xs font-medium text-foreground">审核</span>
      <span v-if="result && !result.notGit && !result.noCwd" class="text-[0.62rem] text-muted-foreground tabular-nums">
        {{ files.length }} 个文件改动
      </span>
      <button
        v-if="unsentNotes.length > 0"
        type="button"
        class="ml-auto px-2 py-0.5 rounded-full text-[0.66rem] font-semibold bg-purple-600/25 text-purple-200 border border-purple-400/50 hover:bg-purple-600/40 transition-colors"
        data-testid="rp-send-notes"
        :disabled="sendingNotes"
        @click="sendAllUnsentNotes"
      >✨ 发送批注 ({{ unsentNotes.length }})</button>
      <button
        :class="unsentNotes.length > 0 ? '' : 'ml-auto'"
        class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors"
        type="button" title="刷新"
        data-testid="rp-refresh"
        :disabled="loading"
        @click="load"
      >
        <RefreshCw class="size-3.5" :class="{ 'animate-spin': loading }" />
      </button>
    </div>

    <!-- filename/path filter over the changed-file list (S-3) -->
    <DrawerSearchBox
      v-if="files.length"
      v-model="query"
      placeholder="筛选改动文件…"
      testid="rp-search"
      class="shrink-0 border-b border-border/40 px-2 py-1.5"
    />

    <!-- truncated banner -->
    <div
      v-if="result?.truncated"
      class="mx-2 mt-1.5 rounded-md bg-amber-500/10 px-2 py-1.5 text-[0.62rem] leading-snug text-amber-600 dark:text-amber-400"
      data-testid="rp-truncated"
    >
      改动较多，部分文件的 diff 已截断。
    </div>

    <div class="flex-1 overflow-y-auto p-2">
      <!-- loading -->
      <div v-if="loading && !result" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-loading">加载中…</div>
      <!-- empty states (R-4) -->
      <div v-else-if="result?.noCwd" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-nocwd">未获取到工作目录</div>
      <div v-else-if="result?.error" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-error">
        加载失败
        <button class="ml-2 underline hover:text-foreground" type="button" @click="load">重试</button>
      </div>
      <div v-else-if="result?.notGit" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-notgit">当前目录不是 git 仓库</div>
      <div v-else-if="!files.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-clean">工作树无改动</div>
      <div v-else-if="!filteredFiles.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-nomatch">无匹配文件</div>

      <!-- changed-file list (accordion) -->
      <template v-else>
        <div class="mb-1.5 px-1 text-[0.62rem] text-muted-foreground">
          提示：展开文件后点击任意代码行即可添加行级批注并直发终端
        </div>
        <ul class="flex flex-col gap-1">
          <li
            v-for="f in filteredFiles"
            :key="f.path"
            class="rounded-md border border-border bg-card overflow-hidden"
            :data-testid="`rp-file-${f.path}`"
          >
            <div class="flex items-center hover:bg-muted/40 transition-colors pr-1.5">
              <button
                class="min-w-0 flex-1 flex items-center gap-2 px-2 py-1.5 text-left"
                type="button"
                :title="f.orig ? `${f.orig} → ${f.path}` : f.path"
                @click="toggle(f.path)"
              >
                <ChevronRight class="size-3.5 shrink-0 text-muted-foreground transition-transform" :class="{ 'rotate-90': expanded === f.path }" />
                <span
                  class="shrink-0 inline-flex items-center justify-center w-5 h-4 rounded border text-[0.6rem] font-bold tabular-nums"
                  :class="statusMeta(f.status).cls"
                >{{ statusMeta(f.status).label }}</span>
                <span class="min-w-0 flex-1 truncate text-xs">
                  <span v-if="parentDir(f.path)" class="text-muted-foreground/60">{{ parentDir(f.path) }}/</span>
                  <span class="text-foreground font-medium">{{ baseName(f.path) }}</span>
                </span>
                <span v-if="f.binary" class="shrink-0 text-[0.58rem] text-muted-foreground">二进制</span>
                <template v-else>
                  <span v-if="f.added" class="shrink-0 text-[0.6rem] tabular-nums text-green-600 dark:text-green-400">+{{ f.added }}</span>
                  <span v-if="f.deleted" class="shrink-0 text-[0.6rem] tabular-nums text-red-600 dark:text-red-400">-{{ f.deleted }}</span>
                </template>
              </button>

              <!-- PC quick preview / split buttons -->
              <template v-if="!isMobileViewport && f.status !== 'D'">
                <button
                  type="button"
                  class="shrink-0 px-1.5 py-0.5 rounded text-[0.6rem] text-muted-foreground hover:text-foreground hover:bg-muted/60"
                  title="在工作台多标签打开文件"
                  :data-testid="`rp-open-preview-${f.path}`"
                  @click.stop="openWorkbenchFileFor(f.path)"
                >📄</button>
                <button
                  type="button"
                  class="shrink-0 px-1.5 py-0.5 rounded text-[0.6rem] text-muted-foreground hover:text-foreground hover:bg-muted/60"
                  title="左右分屏边聊边看"
                  :data-testid="`rp-split-file-${f.path}`"
                  @click.stop="splitFileFor(f.path)"
                >◫</button>
              </template>
            </div>

            <!-- expanded unified diff -->
            <div v-if="expanded === f.path" class="border-t border-border/60" :data-testid="`rp-diff-${f.path}`">
              <div v-if="f.binary" class="px-3 py-3 text-[0.66rem] text-muted-foreground italic">二进制文件，无法显示差异</div>
              <div v-else-if="!f.diff" class="px-3 py-3 text-[0.66rem] text-muted-foreground italic">无差异内容</div>
              <div v-else class="rp-diff overflow-x-auto">
                <div style="min-width: max-content">
                  <template v-for="ln in diffLines(f.diff)" :key="ln.index">
                    <div
                      class="rp-ln"
                      :class="[ln.cls, { 'rp-ln--commentable': ln.commentable }]"
                      :data-testid="ln.commentable ? `rp-diff-line-${f.path}-${ln.index}` : undefined"
                      :title="ln.commentable ? `点击添加第 ${ln.lineNum} 行批注` : undefined"
                      @click="openLineNoteEditor(f.path, ln)"
                    >
                      <span class="rp-ln-gutter">{{ ln.lineNum != null ? ln.lineNum : '' }}</span>
                      <span class="rp-ln-plus" v-if="ln.commentable">＋</span>
                      <span>{{ ln.text || ' ' }}</span>
                    </div>

                    <!-- Inline diff note editor -->
                    <div
                      v-if="editingNote && editingNote.file === f.path && editingNote.rowIndex === ln.index"
                      class="rp-note-editor"
                      data-testid="rp-inline-note-editor"
                      @click.stop
                    >
                      <input
                        ref="noteInputRef"
                        v-model="editingNote.text"
                        type="text"
                        class="rp-note-input"
                        data-testid="rp-note-input"
                        :placeholder="`输入 @${f.path}:${editingNote.line} 的修改建议…`"
                        @keydown.enter.prevent="saveInlineNote"
                        @keydown.esc.stop.prevent="cancelInlineNote"
                      />
                      <div class="rp-note-actions">
                        <button
                          type="button"
                          class="rp-note-btn"
                          data-testid="rp-note-cancel"
                          @click="cancelInlineNote"
                        >取消</button>
                        <button
                          type="button"
                          class="rp-note-btn rp-note-btn--save"
                          data-testid="rp-note-save"
                          @click="saveInlineNote"
                        >暂存批注</button>
                        <button
                          type="button"
                          class="rp-note-btn rp-note-btn--send"
                          data-testid="rp-note-send-now"
                          :disabled="sendingNotes"
                          @click="saveAndSendSingleNote"
                        >✨ 发送批注</button>
                      </div>
                    </div>

                    <!-- Saved inline notes for this row -->
                    <div
                      v-for="note in notesForRow(f.path, ln.index)"
                      :key="note.id"
                      class="rp-note-card"
                      :class="{ 'is-sent': note.sent }"
                      data-testid="rp-note-card"
                    >
                      <span class="rp-note-body">
                        <strong>行 {{ note.line }}：</strong>{{ note.text }}
                        <em v-if="note.sent" class="rp-note-sent-tag">✓ 已发送</em>
                      </span>
                      <button
                        type="button"
                        class="rp-note-del"
                        title="删除批注"
                        :data-testid="`rp-del-note-${note.id}`"
                        @click.stop="deleteNote(note.id)"
                      >🗑</button>
                    </div>
                  </template>
                </div>
              </div>
            </div>
          </li>
        </ul>
      </template>
    </div>

    <div v-if="toastText" class="rp-toast" data-testid="rp-toast">{{ toastText }}</div>
  </div>
</template>

<style scoped>
.rp-diff {
  background: #120c1c;
  font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', monospace;
  font-size: 0.66rem;
  line-height: 1.55;
}
.rp-ln {
  display: flex;
  align-items: baseline;
  white-space: pre;
  padding: 0 10px 0 4px;
}
.rp-ln--commentable {
  cursor: pointer;
}
.rp-ln--commentable:hover {
  filter: brightness(1.18);
}
.rp-ln-gutter {
  display: inline-block;
  width: 34px;
  text-align: right;
  padding-right: 6px;
  color: #6b6284;
  user-select: none;
  flex-shrink: 0;
}
.rp-ln-plus {
  display: inline-block;
  width: 14px;
  color: #a855f7;
  opacity: 0;
  font-weight: 700;
  user-select: none;
  flex-shrink: 0;
}
.rp-ln--commentable:hover .rp-ln-plus {
  opacity: 1;
}
.rp-ln-add { background: rgba(96, 216, 144, 0.12); color: #7fe0a8; }
.rp-ln-del { background: rgba(255, 128, 128, 0.10); color: #f0a0a0; }
.rp-ln-hunk { color: #6fb8ff; background: rgba(96, 160, 255, 0.06); }
.rp-ln-meta { color: #7a6a96; }
.rp-ln-ctx { color: #c4b4dc; }

.rp-note-editor {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 10px;
  background: #1a1528;
  border-top: 1px solid #4c3575;
  border-bottom: 1px solid #4c3575;
  font-family: system-ui, -apple-system, sans-serif;
}
.rp-note-input {
  width: 100%;
  height: 28px;
  padding: 0 8px;
  border-radius: 5px;
  border: 1px solid #56427a;
  background: #0e0b16;
  color: #f0ebfa;
  font-size: 0.73rem;
  outline: none;
}
.rp-note-input:focus {
  border-color: #a855f7;
}
.rp-note-actions {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
}
.rp-note-btn {
  height: 24px;
  padding: 0 8px;
  border-radius: 5px;
  border: 1px solid #3f3656;
  background: #241e36;
  color: #cfc6e6;
  font-size: 0.69rem;
  cursor: pointer;
}
.rp-note-btn--save {
  border-color: #4f6396;
  background: #1f2b48;
  color: #cde0ff;
}
.rp-note-btn--send {
  border-color: #a855f7;
  background: rgba(147, 51, 234, 0.32);
  color: #f5ebff;
  font-weight: 600;
}

.rp-note-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 5px 10px;
  background: rgba(147, 51, 234, 0.14);
  border-left: 3px solid #a855f7;
  color: #ebdfff;
  font-family: system-ui, -apple-system, sans-serif;
  font-size: 0.7rem;
}
.rp-note-card.is-sent {
  opacity: 0.75;
  border-left-color: #3dd68c;
}
.rp-note-sent-tag {
  margin-left: 6px;
  font-style: normal;
  font-size: 0.64rem;
  color: #8ce8b5;
}
.rp-note-del {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 0.72rem;
  opacity: 0.75;
}
.rp-note-del:hover {
  opacity: 1;
}

.rp-toast {
  position: absolute;
  left: 50%;
  bottom: 12px;
  transform: translateX(-50%);
  padding: 4px 12px;
  border-radius: 999px;
  background: rgba(20, 16, 34, 0.94);
  border: 1px solid rgba(168, 85, 247, 0.5);
  color: #f3e8ff;
  font-size: 0.7rem;
  pointer-events: none;
}
</style>
