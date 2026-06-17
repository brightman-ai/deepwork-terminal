<script setup lang="ts">
/**
 * FilesPanel — the drawer's 文件 panel (CHG-016 Tab 2).
 *
 * Two sub-tabs over ONE session's working tree:
 *   · 最近文件 — files agents recently wrote/edited (GET /files/recent, tool_use signal).
 *   · 目录树   — a navigable single-level tree (GET /files/tree), breadcrumb to ascend.
 * Clicking a file previews it inline (GET /files/raw → text | binary | tooLarge).
 * Every path carries a copy affordance; previews + recent rows bubble inject /
 * compose-draft up to the host exactly like the existing drawer tabs.
 *
 * Composition note: this composes the @ce v6 idiom directly (tokens + a styled <pre>
 * preview) rather than @ce WorkArea — WorkArea bakes in a synthetic Explorer tab plus
 * closable per-file content tabs, which fights the two-mode (recent/tree) + breadcrumb
 * model here. The pieces it would reuse (PanelPane search, CanvasPane readonly) are
 * lighter to inline at this size, and keep the panel self-contained for the drawer.
 */
import { ref, computed, watch, onMounted } from 'vue'
import { Copy, Check, Folder, FileText, ChevronLeft, Download } from 'lucide-vue-next'
import {
  filesRecent,
  filesTree,
  filesRaw,
  type RecentFileItem,
  type TreeEntry,
  type RawResult,
} from '@terminal/api/files'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'

const props = defineProps<{ sessionId: string }>()

// Live active-pane cwd → files (recent + tree) follow tmux pane/window switches; server
// falls back to the session's creation cwd when this is empty.
const tmux = useTmuxState(() => props.sessionId)
const emit = defineEmits<{
  (e: 'inject', path: string): void
  (e: 'compose-draft', text: string): void
}>()

type SubTab = 'recent' | 'tree'
const subTab = ref<SubTab>('recent')

// ── 最近文件 ──
const recent = ref<RecentFileItem[]>([])
const recentLoading = ref(false)

async function loadRecent(): Promise<void> {
  recentLoading.value = true
  try {
    recent.value = await filesRecent(props.sessionId, tmux.activeCwd.value)
  } finally {
    recentLoading.value = false
  }
}

// ── 目录树 ──
const treeRel = ref('') // '' = cwd root
const treeCwd = ref('')
const treeEntries = ref<TreeEntry[]>([])
const treeLoading = ref(false)

async function loadTree(rel: string): Promise<void> {
  treeLoading.value = true
  try {
    const resp = await filesTree(props.sessionId, rel, tmux.activeCwd.value)
    if (resp) {
      treeCwd.value = resp.cwd
      treeRel.value = resp.rel === '.' ? '' : resp.rel
      treeEntries.value = resp.entries
    } else {
      treeEntries.value = []
    }
  } finally {
    treeLoading.value = false
  }
}

// Breadcrumb segments of the current rel path (root + each dir), each clickable.
const crumbs = computed(() => {
  const segs = treeRel.value ? treeRel.value.split('/').filter(Boolean) : []
  const out: { label: string; rel: string }[] = [{ label: '根目录', rel: '' }]
  let acc = ''
  for (const s of segs) {
    acc = acc ? `${acc}/${s}` : s
    out.push({ label: s, rel: acc })
  }
  return out
})

function enterDir(entry: TreeEntry): void {
  const next = treeRel.value ? `${treeRel.value}/${entry.name}` : entry.name
  void loadTree(next)
}
function goCrumb(rel: string): void {
  void loadTree(rel)
}

// The absolute path of a tree entry (cwd + rel + name) — for copy + inject.
function entryAbsPath(entry: TreeEntry): string {
  const cwd = treeCwd.value.replace(/\/+$/, '')
  const relPart = treeRel.value ? `/${treeRel.value}` : ''
  return `${cwd}${relPart}/${entry.name}`
}
function entryRelPath(entry: TreeEntry): string {
  return treeRel.value ? `${treeRel.value}/${entry.name}` : entry.name
}

// ── Preview (shared by both sub-tabs) ──
interface Preview {
  name: string
  absPath: string
  result: RawResult
}
const preview = ref<Preview | null>(null)
const previewLoading = ref(false)

async function previewRel(name: string, absPath: string, rel: string): Promise<void> {
  previewLoading.value = true
  preview.value = { name, absPath, result: { kind: 'text', text: '' } }
  try {
    const result = await filesRaw(props.sessionId, rel, tmux.activeCwd.value)
    preview.value = { name, absPath, result }
  } finally {
    previewLoading.value = false
  }
}

// Recent files are previewed by their path relative to cwd when it sits under cwd;
// the backend resolves session→cwd, so we send the absolute path's cwd-relative tail.
// Recent items already carry an absolute path; derive a best-effort rel from dir+name.
async function previewRecent(f: RecentFileItem): Promise<void> {
  // The server anchors on session cwd; pass the absolute path and let safeResolve
  // collapse it (an absolute path is anchored into cwd, matching the contract).
  await previewRel(f.name, f.path, f.path)
}
async function previewTreeFile(entry: TreeEntry): Promise<void> {
  await previewRel(entry.name, entryAbsPath(entry), entryRelPath(entry))
}
function closePreview(): void {
  preview.value = null
}

// ── inject / compose bubbling ──
function injectPath(path: string): void {
  if (!path) { toast('无法插入：缺少路径'); return }
  emit('inject', path)
  toast('已插入对话')
}

// ── copy affordance ──
const copiedKey = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | null = null
async function copyPath(path: string, key: string): Promise<void> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(path)
    } else {
      const ta = document.createElement('textarea')
      ta.value = path
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    copiedKey.value = key
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => { copiedKey.value = '' }, 1400)
  } catch {
    toast('复制失败')
  }
}

// ── toast ──
const toastMsg = ref('')
let toastTimer: ReturnType<typeof setTimeout> | null = null
function toast(msg: string): void {
  toastMsg.value = msg
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toastMsg.value = '' }, 1500)
}

// ── formatting ──
function fmtSize(bytes: number): string {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
function relTime(ms: number): string {
  if (!ms) return ''
  const diff = Date.now() - ms
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}分前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}时前`
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)}天前`
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// Switching to a sub-tab lazily loads its data the first time.
watch(subTab, (t) => {
  closePreview()
  if (t === 'recent' && !recent.value.length) void loadRecent()
  if (t === 'tree' && !treeEntries.value.length) void loadTree(treeRel.value)
})
// Re-anchor when the target session OR the live active-pane cwd changes (the user switched
// tmux pane/window) — reset the tree to the new root and reload the visible sub-tab.
function reanchor(): void {
  recent.value = []
  treeEntries.value = []
  treeRel.value = ''
  closePreview()
  if (subTab.value === 'recent') void loadRecent()
  else void loadTree('')
}
watch(() => props.sessionId, reanchor)
watch(() => tmux.activeCwd.value, reanchor)

onMounted(() => { void loadRecent() })

defineExpose({ loadRecent, loadTree })
</script>

<template>
  <div class="fp flex flex-col h-full bg-background text-foreground" data-testid="files-panel">
    <!-- sub-tabs -->
    <div class="shrink-0 flex items-center border-b border-border bg-card" role="tablist">
      <button
        class="px-3 py-2 text-xs font-medium border-b-2 -mb-px transition-colors"
        :class="subTab === 'recent' ? 'text-foreground border-b-primary' : 'text-muted-foreground border-b-transparent hover:text-foreground'"
        type="button"
        data-testid="fp-subtab-recent"
        @click="subTab = 'recent'"
      >最近文件</button>
      <button
        class="px-3 py-2 text-xs font-medium border-b-2 -mb-px transition-colors"
        :class="subTab === 'tree' ? 'text-foreground border-b-primary' : 'text-muted-foreground border-b-transparent hover:text-foreground'"
        type="button"
        data-testid="fp-subtab-tree"
        @click="subTab = 'tree'"
      >目录树</button>
    </div>

    <!-- ── 最近文件 ── -->
    <div v-show="subTab === 'recent'" class="flex-1 overflow-y-auto p-2">
      <div v-if="recentLoading && !recent.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">加载中…</div>
      <div v-else-if="!recent.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">暂无最近文件</div>
      <ul v-else class="flex flex-col gap-1.5">
        <li
          v-for="f in recent"
          :key="f.path"
          class="group flex items-center gap-2 rounded-md border border-border bg-card px-2.5 py-2"
          :class="{ 'opacity-50': !f.exists }"
          :data-testid="`fp-recent-${f.name}`"
        >
          <FileText class="size-4 shrink-0 text-muted-foreground" />
          <button class="min-w-0 flex-1 text-left" type="button" :title="f.path" @click="previewRecent(f)">
            <span class="block text-xs font-medium truncate text-foreground">{{ f.name }}</span>
            <span class="mt-0.5 flex items-center gap-1.5 text-[0.6rem] text-muted-foreground truncate">
              <span v-if="f.tool" class="px-1 rounded bg-muted text-muted-foreground">{{ f.tool }}</span>
              <span class="truncate">{{ f.dir }}</span>
            </span>
            <span class="text-[0.58rem] text-muted-foreground/70 tabular-nums">{{ fmtSize(f.size) }}<template v-if="f.size && f.tsMs"> · </template>{{ relTime(f.tsMs) }}</span>
          </button>
          <div class="flex shrink-0 items-center gap-1">
            <button
              class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors"
              type="button" title="复制路径"
              :data-testid="`fp-copy-${f.name}`"
              @click="copyPath(f.path, 'r:' + f.path)"
            >
              <Check v-if="copiedKey === 'r:' + f.path" class="size-3.5 text-green-500" />
              <Copy v-else class="size-3.5" />
            </button>
            <button
              class="px-1.5 py-1 rounded text-[0.6rem] text-green-500 border border-border hover:bg-muted/50 transition-colors"
              type="button" title="插入到对话"
              @click="injectPath(f.path)"
            >插入</button>
          </div>
        </li>
      </ul>
    </div>

    <!-- ── 目录树 ── -->
    <div v-show="subTab === 'tree'" class="flex flex-col flex-1 overflow-hidden">
      <!-- breadcrumb + copy current path -->
      <div class="shrink-0 flex items-center gap-1 border-b border-border px-2 py-1.5 text-[0.62rem] text-muted-foreground overflow-x-auto">
        <button
          v-if="treeRel"
          class="p-0.5 rounded hover:bg-muted/50 shrink-0"
          type="button" title="上一级"
          data-testid="fp-tree-up"
          @click="goCrumb(crumbs[crumbs.length - 2]?.rel ?? '')"
        ><ChevronLeft class="size-3.5" /></button>
        <template v-for="(c, i) in crumbs" :key="c.rel">
          <span v-if="i > 0" class="text-muted-foreground/50 shrink-0">/</span>
          <button
            class="shrink-0 hover:text-foreground transition-colors"
            :class="{ 'text-foreground font-medium': i === crumbs.length - 1 }"
            type="button"
            @click="goCrumb(c.rel)"
          >{{ c.label }}</button>
        </template>
        <button
          class="ml-auto p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
          type="button" title="复制当前目录路径"
          data-testid="fp-tree-copy-cwd"
          @click="copyPath((treeCwd.replace(/\/+$/, '')) + (treeRel ? '/' + treeRel : ''), 'cwd')"
        >
          <Check v-if="copiedKey === 'cwd'" class="size-3 text-green-500" />
          <Copy v-else class="size-3" />
        </button>
      </div>

      <div class="flex-1 overflow-y-auto p-2">
        <div v-if="treeLoading && !treeEntries.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">加载中…</div>
        <div v-else-if="!treeEntries.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">空目录</div>
        <ul v-else class="flex flex-col gap-0.5">
          <li
            v-for="e in treeEntries"
            :key="e.name"
            class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-muted/40 transition-colors"
            :data-testid="`fp-tree-${e.name}`"
          >
            <Folder v-if="e.isDir" class="size-4 shrink-0 text-primary/80" />
            <FileText v-else class="size-4 shrink-0 text-muted-foreground" />
            <button
              class="min-w-0 flex-1 text-left text-xs truncate"
              :class="e.isDir ? 'text-foreground font-medium' : 'text-foreground'"
              type="button"
              @click="e.isDir ? enterDir(e) : previewTreeFile(e)"
            >{{ e.name }}<span v-if="e.isDir" class="text-muted-foreground/60">/</span></button>
            <span v-if="!e.isDir" class="text-[0.58rem] text-muted-foreground/70 tabular-nums shrink-0">{{ fmtSize(e.size) }}</span>
            <button
              class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors shrink-0 opacity-0 group-hover:opacity-100"
              type="button" title="复制路径"
              @click="copyPath(entryAbsPath(e), 't:' + e.name)"
            >
              <Check v-if="copiedKey === 't:' + e.name" class="size-3 text-green-500" />
              <Copy v-else class="size-3" />
            </button>
          </li>
        </ul>
      </div>
    </div>

    <!-- ── Preview overlay (slides over the panel; readonly text / placeholder) ── -->
    <Transition name="fp-fade">
      <div v-if="preview" class="fp-preview absolute inset-0 flex flex-col bg-background z-10" data-testid="fp-preview">
        <div class="shrink-0 flex items-center gap-2 border-b border-border bg-card px-3 py-2">
          <FileText class="size-4 shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 text-xs font-medium truncate text-foreground" :title="preview.absPath">{{ preview.name }}</span>
          <button
            class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50"
            type="button" title="复制路径"
            @click="copyPath(preview.absPath, 'p')"
          >
            <Check v-if="copiedKey === 'p'" class="size-3.5 text-green-500" />
            <Copy v-else class="size-3.5" />
          </button>
          <button
            class="px-1.5 py-1 rounded text-[0.6rem] text-green-500 border border-border hover:bg-muted/50"
            type="button" title="插入到对话"
            @click="injectPath(preview.absPath)"
          >插入</button>
          <button class="p-1 rounded text-muted-foreground hover:text-foreground" type="button" title="关闭" data-testid="fp-preview-close" @click="closePreview">&times;</button>
        </div>
        <div class="flex-1 overflow-auto">
          <div v-if="previewLoading" class="flex items-center justify-center h-full text-xs text-muted-foreground animate-pulse">加载中…</div>
          <pre v-else-if="preview.result.kind === 'text'" class="fp-pre m-0 p-3 text-[0.7rem] leading-relaxed text-foreground whitespace-pre-wrap break-words">{{ preview.result.text }}</pre>
          <div v-else-if="preview.result.kind === 'binary'" class="flex flex-col items-center justify-center gap-2 h-full px-4 text-center">
            <Download class="size-7 text-muted-foreground/60" />
            <p class="text-xs text-muted-foreground">二进制文件，无法预览</p>
            <p class="text-[0.62rem] text-muted-foreground/70 tabular-nums">{{ fmtSize(preview.result.size) }}</p>
            <button class="mt-1 px-2 py-1 rounded text-[0.62rem] text-green-500 border border-border hover:bg-muted/50" type="button" @click="injectPath(preview.absPath)">插入路径到对话</button>
          </div>
          <div v-else-if="preview.result.kind === 'tooLarge'" class="flex flex-col items-center justify-center gap-2 h-full px-4 text-center">
            <FileText class="size-7 text-muted-foreground/60" />
            <p class="text-xs text-muted-foreground">文件过大（&gt;1MB），无法预览</p>
            <p class="text-[0.62rem] text-muted-foreground/70 tabular-nums">{{ fmtSize(preview.result.size) }}</p>
            <button class="mt-1 px-2 py-1 rounded text-[0.62rem] text-green-500 border border-border hover:bg-muted/50" type="button" @click="injectPath(preview.absPath)">插入路径到对话</button>
          </div>
          <div v-else class="flex items-center justify-center h-full text-xs text-muted-foreground italic">预览失败</div>
        </div>
      </div>
    </Transition>

    <div v-if="toastMsg" class="fp-toast">{{ toastMsg }}</div>
  </div>
</template>

<style scoped>
.fp { position: relative; }
.fp-pre { font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', monospace; }
.fp-fade-enter-active, .fp-fade-leave-active { transition: opacity 0.15s ease, transform 0.15s ease; }
.fp-fade-enter-from, .fp-fade-leave-to { opacity: 0; transform: translateX(12px); }
.fp-toast {
  position: absolute; bottom: 14px; left: 50%; transform: translateX(-50%);
  background: hsl(var(--card)); border: 1px solid hsl(var(--border)); color: hsl(var(--foreground));
  padding: 6px 14px; border-radius: 999px; font-size: 0.68rem;
  box-shadow: 0 4px 18px rgba(0, 0, 0, 0.5); pointer-events: none; z-index: 20;
}
</style>
