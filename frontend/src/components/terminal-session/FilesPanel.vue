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
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { copyTextToClipboard } from '@ce/utils/clipboard'
import { Copy, Check, Folder, FileText, ChevronLeft, Download, Link2, Image as ImageIcon } from 'lucide-vue-next'
import {
  filesRecent,
  filesTree,
  filesSearch,
  filesRaw,
  filesDownload,
  type RecentFileItem,
  type TreeEntry,
  type SearchEntry,
  type RawResult,
} from '@terminal/api/files'
import { fuzzyMatch } from '@terminal/utils/fuzzyMatch'
import FilePreview from '@terminal/components/terminal-session/FilePreview.vue'
import DrawerSearchBox from '@terminal/components/terminal-session/DrawerSearchBox.vue'

// cwd is the drawer's EFFECTIVE pane working directory, OWNED by ResourceDrawer (CHG:
// drawer-workbench). In FOLLOW mode it tracks the live active pane; once the user LOCKS the
// drawer it freezes, so a main-area pane/window switch no longer yanks the file tree / preview
// the user is mid-read on. FilesPanel just consumes this prop + re-anchors when it changes.
// '' → server falls back to the session cwd.
const props = defineProps<{ sessionId: string; cwd: string }>()

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
    recent.value = await filesRecent(props.sessionId, props.cwd)
  } finally {
    recentLoading.value = false
  }
}

// ── 格式分类筛选 (recent) ──
// Group recent files by a small set of human categories so a long mixed list can be
// filtered to "just the markdown" / "just the code" with one tap (mobile chip row).
// image:[…] mirrors the backend's imageContentType() (files.go) — the raster types served
// as image/* and rendered inline. Keep the two lists in step. svg stays under style (it's
// XML, previewed as text).
const CAT_EXT: Record<string, string[]> = {
  doc: ['md', 'markdown', 'mdx', 'txt', 'rst', 'adoc', 'org'],
  code: ['go', 'ts', 'tsx', 'js', 'jsx', 'mjs', 'cjs', 'vue', 'py', 'rs', 'rb', 'java', 'kt', 'swift', 'c', 'h', 'cpp', 'cc', 'hpp', 'cs', 'php', 'sh', 'bash', 'zsh', 'lua', 'sql', 'proto'],
  config: ['json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'env', 'xml', 'lock', 'dockerfile'],
  style: ['css', 'scss', 'less', 'html', 'svg'],
  image: ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico', 'avif'],
}
const CAT_LABEL: Record<string, string> = { all: '全部', doc: '文档', code: '代码', config: '配置', style: '样式', image: '图片', other: '其他' }
function fileExt(name: string): string {
  if (name.toLowerCase() === 'dockerfile') return 'dockerfile'
  const parts = name.split('.')
  return parts.length > 1 ? (parts.pop() || '').toLowerCase() : ''
}
function catOf(name: string): string {
  const e = fileExt(name)
  for (const [cat, exts] of Object.entries(CAT_EXT)) if (exts.includes(e)) return cat
  return 'other'
}
// isImage reuses the category SSOT (CAT_EXT.image) so the icon/preview never drift from the chips.
function isImage(name: string): boolean {
  return catOf(name) === 'image'
}
const activeCat = ref('all')
// Only surface categories that actually have files, in a stable order, each with a count.
const recentCats = computed(() => {
  const counts: Record<string, number> = {}
  for (const f of recent.value) counts[catOf(f.name)] = (counts[catOf(f.name)] || 0) + 1
  const order = ['doc', 'code', 'config', 'style', 'image', 'other']
  const cats = [{ key: 'all', label: CAT_LABEL.all, count: recent.value.length }]
  for (const k of order) if (counts[k]) cats.push({ key: k, label: CAT_LABEL[k], count: counts[k] })
  return cats
})
// Client-side quick filter — composes with the category chip filter (both must pass).
const recentQuery = ref('')
const filteredRecent = computed(() => {
  return recent.value.filter((f) => {
    if (activeCat.value !== 'all' && catOf(f.name) !== activeCat.value) return false
    if (!fuzzyMatch(recentQuery.value, f.name)) return false
    return true
  })
})
// Reset the active chip if the data changes such that it no longer exists.
watch(recentCats, (cats) => {
  if (!cats.some(c => c.key === activeCat.value)) activeCat.value = 'all'
})

// ── 目录树 ──
const treeRel = ref('') // '' = cwd root
const treeCwd = ref('')
const treeEntries = ref<TreeEntry[]>([])
const treeLoading = ref(false)

async function loadTree(rel: string): Promise<void> {
  treeLoading.value = true
  try {
    const resp = await filesTree(props.sessionId, rel, props.cwd)
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

// ── 目录树 recursive search (VS-Code quick-open) ──
// A non-empty treeQuery replaces the single-level browse with a FLAT, recursive results
// list from GET /files/search; clearing it returns to breadcrumb browse at the current dir.
const treeQuery = ref('')
const searchResults = ref<SearchEntry[]>([])
const searchTruncated = ref(false) // server hit a cap → results incomplete (huge cwd)
const searching = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null
let searchSeq = 0 // guards against out-of-order responses clobbering a newer query

async function runSearch(q: string): Promise<void> {
  const seq = ++searchSeq
  searching.value = true
  try {
    const res = await filesSearch(props.sessionId, props.cwd, q)
    if (seq === searchSeq) {
      searchResults.value = res.entries
      searchTruncated.value = res.truncated
    }
  } finally {
    if (seq === searchSeq) searching.value = false
  }
}

// Debounce ~250ms; an empty query instantly drops back to browse mode.
watch(treeQuery, (q) => {
  if (searchTimer) { clearTimeout(searchTimer); searchTimer = null }
  const trimmed = q.trim()
  if (!trimmed) {
    searchSeq++ // cancel any in-flight result
    searching.value = false
    searchResults.value = []
    searchTruncated.value = false
    return
  }
  searchTimer = setTimeout(() => { void runSearch(trimmed) }, 250)
})

// The parent rel dir of a search hit (dimmed in the row, VS-Code style); '' for a top-level hit.
function parentRel(rel: string): string {
  const i = rel.lastIndexOf('/')
  return i >= 0 ? rel.slice(0, i) : ''
}
// Absolute path of a search hit (treeCwd may be unset before first browse → fall back to live cwd).
function searchAbsPath(entry: SearchEntry): string {
  const base = (treeCwd.value || props.cwd || '').replace(/\/+$/, '')
  return base ? `${base}/${entry.rel}` : entry.rel
}
// Click a FILE hit → preview by its rel path. Click a DIR hit → browse into it + clear search.
function onSearchHit(entry: SearchEntry): void {
  if (entry.isDir) {
    treeQuery.value = ''
    void loadTree(entry.rel)
  } else {
    void previewRel(entry.name, searchAbsPath(entry), entry.rel)
  }
}

// ── Preview (shared by both sub-tabs) ──
interface Preview {
  name: string
  absPath: string
  /** The exact rel/path handed to filesRaw — reused verbatim by download so path
   *  resolution is identical to what the preview already opened. */
  rel: string
  result: RawResult
}
const preview = ref<Preview | null>(null)
const previewLoading = ref(false)

// Free a previous image preview's object URL (created in filesRaw) before it's replaced,
// so blob bytes don't leak for the session's lifetime.
function revokePreviewUrl(): void {
  if (preview.value?.result.kind === 'image') URL.revokeObjectURL(preview.value.result.url)
}

async function previewRel(name: string, absPath: string, rel: string): Promise<void> {
  revokePreviewUrl()
  previewLoading.value = true
  preview.value = { name, absPath, rel, result: { kind: 'text', text: '' } }
  try {
    const result = await filesRaw(props.sessionId, rel, props.cwd)
    preview.value = { name, absPath, rel, result }
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
  revokePreviewUrl()
  preview.value = null
}
onUnmounted(revokePreviewUrl)

// ── inject / compose bubbling ──
function injectPath(path: string): void {
  if (!path) { toast('无法插入：缺少路径'); return }
  emit('inject', path)
  toast('已插入对话')
}

// Download the previewed file's real bytes — ANY format, incl. binary / oversized that can't
// preview inline. Reuses the SAME rel the preview fetched, so /files/raw resolves identically.
async function doDownload(): Promise<void> {
  const p = preview.value
  if (!p) return
  if (!(await filesDownload(props.sessionId, p.rel, p.name, props.cwd))) toast('下载失败')
}

// ── copy affordance ──
const copiedKey = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | null = null
// copyText copies any string to the clipboard — same affordance for a file's PATH and its
// CONTENT. navigator.clipboard needs a secure context (HTTPS/localhost); over plain LAN
// HTTP it's undefined, so we fall back to the execCommand+textarea path. key drives the
// transient ✓ on whichever button fired.
async function copyText(text: string, key: string): Promise<void> {
  try {
    // SSOT helper: iOS-aware fallback (the old bare ta.select() silently no-ops on iOS Safari).
    if (!(await copyTextToClipboard(text))) throw new Error('copy failed')
    copiedKey.value = key
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => { copiedKey.value = '' }, 1400)
  } catch {
    toast('复制失败')
  }
}

// copyContent copies the previewed file's TEXT — the primary action in a content preview
// (mainstream: a viewer's copy means "copy what I'm reading", not the path). Only text
// previews carry content; binary/too-large states expose path actions instead.
function copyContent(): void {
  if (preview.value?.result.kind === 'text') void copyText(preview.value.result.text, 'content')
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
// Re-anchor when the target session OR the ANCHORED cwd prop changes — i.e. ONLY when the
// user explicitly re-anchors via the drawer's pane pill (or a session switch). A plain tmux
// pane/window switch no longer touches props.cwd, so the file tree / preview the user is
// mid-read on stays put. On re-anchor: reset the tree to the new root + reload the sub-tab.
function reanchor(): void {
  recent.value = []
  treeEntries.value = []
  treeRel.value = ''
  recentQuery.value = ''
  treeQuery.value = ''
  searchResults.value = []
  searching.value = false
  closePreview()
  if (subTab.value === 'recent') void loadRecent()
  else void loadTree('')
}
watch(() => props.sessionId, reanchor)
watch(() => props.cwd, reanchor)

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
    <div v-show="subTab === 'recent'" class="flex-1 flex flex-col overflow-hidden">
      <!-- Quick filter (client-side, instant) — composes with the category chips below. -->
      <DrawerSearchBox
        v-if="recent.length"
        v-model="recentQuery"
        placeholder="筛选最近文件…"
        testid="fp-recent-search"
        class="shrink-0 border-b border-border/40 px-2 py-1.5"
      />
      <!-- Format filter chips — only when the recent set actually spans >1 category. -->
      <div v-if="recent.length && recentCats.length > 2" class="flex gap-1.5 overflow-x-auto px-2 py-1.5 shrink-0 border-b border-border/40 no-scrollbar">
        <button
          v-for="c in recentCats"
          :key="c.key"
          type="button"
          class="shrink-0 rounded-full border px-2.5 py-0.5 text-[0.62rem] font-medium transition-colors"
          :class="activeCat === c.key ? 'bg-primary/15 border-primary/60 text-foreground' : 'bg-card border-border text-muted-foreground hover:text-foreground'"
          :data-testid="`fp-cat-${c.key}`"
          @click="activeCat = c.key"
        >{{ c.label }}<span class="ml-1 opacity-60 tabular-nums">{{ c.count }}</span></button>
      </div>
      <div class="flex-1 overflow-y-auto p-2">
      <div v-if="recentLoading && !recent.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">加载中…</div>
      <div v-else-if="!recent.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">暂无最近文件</div>
      <div v-else-if="!filteredRecent.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">无匹配文件</div>
      <ul v-else class="flex flex-col gap-1.5">
        <li
          v-for="f in filteredRecent"
          :key="f.path"
          class="group flex items-center gap-2 rounded-md border border-border bg-card px-2.5 py-2"
          :class="{ 'opacity-50': !f.exists }"
          :data-testid="`fp-recent-${f.name}`"
        >
          <ImageIcon v-if="isImage(f.name)" class="size-4 shrink-0 text-muted-foreground" />
          <FileText v-else class="size-4 shrink-0 text-muted-foreground" />
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
              @click="copyText(f.path, 'r:' + f.path)"
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
    </div>

    <!-- ── 目录树 ── -->
    <div v-show="subTab === 'tree'" class="flex flex-col flex-1 overflow-hidden">
      <!-- Recursive search (debounced) — replaces the browse with a flat results list. -->
      <DrawerSearchBox
        v-model="treeQuery"
        placeholder="搜索文件 / 目录（递归）…"
        testid="fp-tree-search"
        class="shrink-0 border-b border-border/40 px-2 py-1.5"
      />

      <!-- breadcrumb + copy current path (browse mode only) -->
      <div v-show="!treeQuery.trim()" class="shrink-0 flex flex-col border-b border-border">
        <div class="flex items-center gap-1 px-2 py-1.5 text-[0.62rem] text-muted-foreground overflow-x-auto">
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
          @click="copyText((treeCwd.replace(/\/+$/, '')) + (treeRel ? '/' + treeRel : ''), 'cwd')"
        >
          <Check v-if="copiedKey === 'cwd'" class="size-3 text-green-500" />
          <Copy v-else class="size-3" />
        </button>
        </div>
        <!-- 根目录时显示绝对路径，帮助区分多工程 -->
        <div
          v-if="!treeRel && treeCwd"
          class="px-2 pb-1 text-[0.58rem] text-muted-foreground/50 truncate select-all"
          :title="treeCwd"
          data-testid="fp-tree-cwd-abs"
        >{{ treeCwd }}</div>
      </div>

      <!-- ── search results (recursive, flat) — VS-Code quick-open style ── -->
      <div v-if="treeQuery.trim()" class="flex-1 overflow-y-auto p-2" data-testid="fp-search-results">
        <div
          v-if="searchTruncated"
          class="mx-2 mb-1.5 rounded-md bg-amber-500/10 px-2 py-1.5 text-[0.62rem] leading-snug text-amber-600 dark:text-amber-400"
          data-testid="fp-search-truncated"
        >
          结果过多，已截断 — 当前目录很大（可能不是你以为的工程根）。请缩小搜索词，或切到目标目录再搜。
        </div>
        <div v-if="searching && !searchResults.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">搜索中…</div>
        <div v-else-if="!searchResults.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">无匹配文件</div>
        <ul v-else class="flex flex-col gap-0.5">
          <li
            v-for="e in searchResults"
            :key="e.rel"
            class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-muted/40 transition-colors"
            :data-testid="`fp-search-${e.rel}`"
          >
            <Folder v-if="e.isDir" class="size-4 shrink-0 text-primary/80" />
            <ImageIcon v-else-if="isImage(e.name)" class="size-4 shrink-0 text-muted-foreground" />
            <FileText v-else class="size-4 shrink-0 text-muted-foreground" />
            <button
              class="min-w-0 flex-1 text-left"
              type="button"
              :title="e.rel"
              @click="onSearchHit(e)"
            >
              <span class="block text-xs truncate" :class="e.isDir ? 'text-foreground font-medium' : 'text-foreground'">{{ e.name }}<span v-if="e.isDir" class="text-muted-foreground/60">/</span></span>
              <span v-if="parentRel(e.rel)" class="block text-[0.58rem] text-muted-foreground/60 truncate">{{ parentRel(e.rel) }}</span>
            </button>
            <span v-if="!e.isDir" class="text-[0.58rem] text-muted-foreground/70 tabular-nums shrink-0">{{ fmtSize(e.size) }}</span>
            <button
              class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors shrink-0 opacity-0 group-hover:opacity-100"
              type="button" title="复制路径"
              @click="copyText(searchAbsPath(e), 's:' + e.rel)"
            >
              <Check v-if="copiedKey === 's:' + e.rel" class="size-3 text-green-500" />
              <Copy v-else class="size-3" />
            </button>
          </li>
        </ul>
      </div>

      <!-- ── browse (single level) ── -->
      <div v-else class="flex-1 overflow-y-auto p-2">
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
            <ImageIcon v-else-if="isImage(e.name)" class="size-4 shrink-0 text-muted-foreground" />
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
              @click="copyText(entryAbsPath(e), 't:' + e.name)"
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
          <ImageIcon v-if="isImage(preview.name)" class="size-4 shrink-0 text-muted-foreground" />
          <FileText v-else class="size-4 shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 text-xs font-medium truncate text-foreground" :title="preview.absPath">{{ preview.name }}</span>
          <!-- 复制内容 (primary — a content preview's copy means "copy what I'm reading") -->
          <button
            v-if="preview.result.kind === 'text'"
            class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50"
            type="button" title="复制内容"
            data-testid="fp-preview-copy-content"
            @click="copyContent"
          >
            <Check v-if="copiedKey === 'content'" class="size-3.5 text-green-500" />
            <Copy v-else class="size-3.5" />
          </button>
          <!-- 复制路径 (secondary — distinct Link2 glyph so it isn't mistaken for content) -->
          <button
            class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50"
            type="button" title="复制路径"
            data-testid="fp-preview-copy-path"
            @click="copyText(preview.absPath, 'p')"
          >
            <Check v-if="copiedKey === 'p'" class="size-3.5 text-green-500" />
            <Link2 v-else class="size-3.5" />
          </button>
          <!-- 下载 (works for EVERY format — text/image/binary/oversized — via /files/raw?download=1) -->
          <button
            class="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50"
            type="button" title="下载文件"
            data-testid="fp-preview-download"
            @click="doDownload"
          >
            <Download class="size-3.5" />
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
          <FilePreview v-else-if="preview.result.kind === 'text'" :name="preview.name" :text="preview.result.text" />
          <div v-else-if="preview.result.kind === 'image'" class="flex h-full items-center justify-center overflow-auto p-3" style="background:#0e0b16" data-testid="fp-preview-image">
            <img :src="preview.result.url" :alt="preview.name" class="max-w-full max-h-full object-contain" />
          </div>
          <div v-else-if="preview.result.kind === 'binary'" class="flex flex-col items-center justify-center gap-2 h-full px-4 text-center">
            <Download class="size-7 text-muted-foreground/60" />
            <p class="text-xs text-muted-foreground">二进制文件，无法预览</p>
            <p class="text-[0.62rem] text-muted-foreground/70 tabular-nums">{{ fmtSize(preview.result.size) }}</p>
            <div class="mt-1 flex items-center gap-2">
              <button class="px-2 py-1 rounded text-[0.62rem] font-medium text-green-500 border border-green-500/60 hover:bg-green-500/10" type="button" @click="doDownload">下载</button>
              <button class="px-2 py-1 rounded text-[0.62rem] text-green-500 border border-border hover:bg-muted/50" type="button" @click="injectPath(preview.absPath)">插入路径到对话</button>
            </div>
          </div>
          <div v-else-if="preview.result.kind === 'tooLarge'" class="flex flex-col items-center justify-center gap-2 h-full px-4 text-center">
            <FileText class="size-7 text-muted-foreground/60" />
            <p class="text-xs text-muted-foreground">文件过大（&gt;1MB），无法预览</p>
            <p class="text-[0.62rem] text-muted-foreground/70 tabular-nums">{{ fmtSize(preview.result.size) }}</p>
            <div class="mt-1 flex items-center gap-2">
              <button class="px-2 py-1 rounded text-[0.62rem] font-medium text-green-500 border border-green-500/60 hover:bg-green-500/10" type="button" @click="doDownload">下载</button>
              <button class="px-2 py-1 rounded text-[0.62rem] text-green-500 border border-border hover:bg-muted/50" type="button" @click="injectPath(preview.absPath)">插入路径到对话</button>
            </div>
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
/* Horizontal chip row: scrollable but without a visible scrollbar (mobile). */
.no-scrollbar { scrollbar-width: none; -ms-overflow-style: none; }
.no-scrollbar::-webkit-scrollbar { display: none; }
.fp-fade-enter-active, .fp-fade-leave-active { transition: opacity 0.15s ease, transform 0.15s ease; }
.fp-fade-enter-from, .fp-fade-leave-to { opacity: 0; transform: translateX(12px); }
.fp-toast {
  position: absolute; bottom: 14px; left: 50%; transform: translateX(-50%);
  background: hsl(var(--card)); border: 1px solid hsl(var(--border)); color: hsl(var(--foreground));
  padding: 6px 14px; border-radius: 999px; font-size: 0.68rem;
  box-shadow: 0 4px 18px rgba(0, 0, 0, 0.5); pointer-events: none; z-index: 20;
}
</style>
