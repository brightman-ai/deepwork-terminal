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
import { Copy, Check, Folder, FileText, ChevronRight, ChevronsDownUp, Loader2, Download, Link2, Upload, Image as ImageIcon, FilePlus, FolderPlus, Pencil, Trash2, X, MoreVertical, Keyboard, Settings } from 'lucide-vue-next'
import { useUploadLimit } from '@terminal/composables/cli/uploadLimits'
import {
  filesRecent,
  filesTree,
  filesSearch,
  filesRaw,
  filesRawRenderUrl,
  filesDownloadUrl,
  filesMkdir,
  filesCreate,
  filesRename,
  filesDelete,
  type RecentFileItem,
  type TreeEntry,
  type SearchEntry,
  type RawResult,
} from '@terminal/api/files'
import { useTreeUpload } from '@terminal/composables/cli/useTreeUpload'
import { nextTick } from 'vue'
import { fuzzyMatch } from '@terminal/utils/fuzzyMatch'
import FilePreview from '@terminal/components/terminal-session/FilePreview.vue'
import DrawerSearchBox from '@terminal/components/terminal-session/DrawerSearchBox.vue'

// cwd is the drawer's EFFECTIVE pane working directory, OWNED by ResourceDrawer (CHG:
// drawer-workbench). In FOLLOW mode it tracks the live active pane; once the user LOCKS the
// drawer it freezes, so a main-area pane/window switch no longer yanks the file tree / preview
// the user is mid-read on. FilesPanel just consumes this prop + re-anchors when it changes.
// '' → server falls back to the session cwd.
// mode fixes this instance to ONE view. The drawer's top-level tabs (目录树 / 最近修改) each mount
// their own FilesPanel, so the panel no longer owns a sub-tab switcher — one mode in, one view out.
const props = defineProps<{ sessionId: string; cwd: string; mode: 'recent' | 'tree' }>()

const emit = defineEmits<{
  (e: 'inject', path: string): void
  (e: 'compose-draft', text: string): void
}>()

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
const treeCwd = ref('')
const treeLoading = ref(false)

// VSCode 式嵌套 lazy 展开树：每个目录节点首次展开时按需拉子目录（GET /files/tree?path=<rel>）。
// children=null → 目录未加载；[] → 空目录或文件。roots=cwd 顶层。
interface TreeNode {
  entry: TreeEntry
  rel: string // 相对 cwd 的完整路径
  depth: number
  expanded: boolean
  loading: boolean
  children: TreeNode[] | null
}
const roots = ref<TreeNode[]>([])

function mkNode(entry: TreeEntry, rel: string, depth: number): TreeNode {
  return { entry, rel, depth, expanded: false, loading: false, children: entry.isDir ? null : [] }
}

// reconcile：用新 entries 重建某一层，按 name 保留旧节点的展开态/已加载 children——
// 刷新目录（上传 / CRUD 后）不丢展开。isDir 变了则视为新节点。
function reconcile(old: TreeNode[] | null, entries: TreeEntry[], parentRel: string, childDepth: number): TreeNode[] {
  const byName = new Map((old || []).map((n) => [n.entry.name, n]))
  return entries.map((e) => {
    const rel = parentRel ? `${parentRel}/${e.name}` : e.name
    const prev = byName.get(e.name)
    if (prev && prev.entry.isDir === e.isDir) {
      prev.entry = e
      prev.rel = rel
      prev.depth = childDepth
      return prev
    }
    return mkNode(e, rel, childDepth)
  })
}

async function loadRoot(): Promise<void> {
  treeLoading.value = true
  try {
    const resp = await filesTree(props.sessionId, '', props.cwd)
    if (resp) {
      treeCwd.value = resp.cwd
      roots.value = reconcile(roots.value, resp.entries, '', 0)
    } else {
      roots.value = []
    }
  } finally {
    treeLoading.value = false
    bumpTree()
  }
}

// refreshDir：重拉某目录一层（null=root），reconcile 保留展开态。上传/建/删/改名后调用。
async function refreshDir(node: TreeNode | null): Promise<void> {
  const rel = node ? node.rel : ''
  const resp = await filesTree(props.sessionId, rel, props.cwd)
  if (!resp) return
  if (!node) {
    treeCwd.value = resp.cwd
    roots.value = reconcile(roots.value, resp.entries, '', 0)
  } else {
    node.children = reconcile(node.children, resp.entries, node.rel, node.depth + 1)
  }
  bumpTree()
}

async function ensureChildren(node: TreeNode): Promise<void> {
  if (node.children !== null) return
  node.loading = true
  bumpTree() // spinner 出现
  try {
    const resp = await filesTree(props.sessionId, node.rel, props.cwd)
    node.children = resp ? reconcile(null, resp.entries, node.rel, node.depth + 1) : []
  } finally {
    node.loading = false
    bumpTree() // 子项就位 / spinner 消失
  }
}

async function toggleNode(node: TreeNode): Promise<void> {
  if (!node.entry.isDir) return
  if (node.expanded) {
    node.expanded = false
    bumpTree()
    return
  }
  await ensureChildren(node)
  node.expanded = true
  bumpTree()
}

// 扁平化：走一遍已展开子树 → 单个 <ul> v-for（键盘导航也按这个顺序定位）。
function flatten(nodes: TreeNode[], out: TreeNode[]): void {
  for (const n of nodes) {
    out.push(n)
    if (n.entry.isDir && n.expanded && n.children) flatten(n.children, out)
  }
}
// treeVersion：每次结构变更（展开/折叠/加载子目录/刷新）自增；visibleNodes 读它 → 保证必重算。
// 为什么显式版本号而非纯靠 ref 深度追踪：prod 构建里递归 flatten 跨 node.children/node.expanded 的
// 依赖图不可靠——实测 caret 的 rotate-90 会响应式更新（render 函数直接读了 n.expanded），但 computed
// 的缓存不失效（它对那个深层节点的 expanded/children 没建立依赖），导致"箭头转了、子行不出来"。
// 显式版本号把"是否重算"从脆弱的深度追踪里解耦出来，确定性。
const treeVersion = ref(0)
function bumpTree(): void { treeVersion.value++ }
const visibleNodes = computed(() => {
  void treeVersion.value // 依赖：任一结构变更后必重算
  const out: TreeNode[] = []
  flatten(roots.value, out)
  return out
})

function collapseAll(): void {
  const walk = (nodes: TreeNode[]): void => {
    for (const n of nodes) {
      n.expanded = false
      if (n.children) walk(n.children)
    }
  }
  walk(roots.value)
  bumpTree()
}

// revealPath：展开到某 rel（搜索命中"下钻"用）——逐层 ensure + expand 祖先。
async function revealPath(rel: string): Promise<void> {
  const segs = rel.split('/').filter(Boolean)
  let level = roots.value
  for (const seg of segs) {
    const node = level.find((n) => n.entry.name === seg)
    if (!node) return
    if (node.entry.isDir) {
      await ensureChildren(node)
      node.expanded = true
      level = node.children || []
    }
  }
  bumpTree()
}

// basename of an absolute path ('' / '/' → '/'). Used so the breadcrumb's root segment
// reads as the actual anchored directory (e.g. "deepwork-terminal"), not a generic label.
function baseName(path: string): string {
  const trimmed = (path || '').replace(/\/+$/, '')
  const i = trimmed.lastIndexOf('/')
  return trimmed.slice(i + 1) || '/'
}

// 节点绝对路径（cwd + rel）——copy / inject / preview / agent-edit 高亮用。
function nodeAbsPath(node: TreeNode): string {
  return `${treeCwd.value.replace(/\/+$/, '')}/${node.rel}`
}

// ── agent-edit highlight (目录树 优于 sftp/vim: 直接标出 agent 刚碰过的文件) ──
// The SAME recent-edited set the 最近修改 tab shows (transcript tool_use signal), indexed by
// absolute path. Browsing the tree then reveals WHICH files the agent just wrote — a relative-time
// badge on the row — the one thing a plain file tree can't surface. One data source, two views.
const recentEditedMap = computed(() => {
  const m = new Map<string, number>()
  for (const f of recent.value) if (f.tsMs) m.set(f.path, f.tsMs)
  return m
})
function recentEditedAt(node: TreeNode): number | undefined {
  if (node.entry.isDir) return undefined
  return recentEditedMap.value.get(nodeAbsPath(node))
}

// ── upload INTO the browsed tree directory (目录树 顶部按钮 + 每目录「上传到此」) ──
// Puts a local file where the user is looking, not the tmp/clipboard bucket — the natural file-tree
// action, i.e. the "sftp put" half. Goes through the CHUNKED resumable protocol (useTreeUpload):
// arbitrary size, survives the Cloudflare ~100MB request-body cap + flaky mobile links, resumes
// from already-landed chunks on retry. Progress/cancel/retry render in the tree header strip.
// On each file landing, refresh its target dir so it shows immediately.
async function refreshUploadTarget(dir: string): Promise<void> {
  if (!dir || dir === '.') {
    await refreshDir(null)
  } else {
    const p = findNode(dir)
    if (p) { await ensureChildren(p); p.expanded = true; await refreshDir(p) }
    else await refreshDir(null)
  }
  void loadRecent(); bumpTree()
}
const treeUpload = useTreeUpload({
  sessionId: () => props.sessionId,
  cwd: () => props.cwd,
  onComplete: (dir) => { void refreshUploadTarget(dir) },
})
const uploadJobs = treeUpload.jobs
const uploadPct = (j: { sent: number; total: number }): number =>
  j.total > 0 ? Math.round((j.sent / j.total) * 100) : 0
const treeUploadInput = ref<HTMLInputElement>()
function pickTreeUpload(): void { treeUploadInput.value?.click() }
function onTreeUploadPicked(e: Event): void {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = '' // let the same file be re-picked later
  if (files.length) treeUpload.enqueue(files, '.') // 顶部按钮 → cwd 根
}

// ════ 目录树 CRUD（slice 2）════════════════════════════════════════════════════════════
// findNode：按 rel 逐层定位已加载的节点（CRUD 后定位父目录刷新用）。
function findNode(rel: string): TreeNode | null {
  if (!rel) return null
  const segs = rel.split('/').filter(Boolean)
  let level: TreeNode[] | null = roots.value
  let node: TreeNode | null = null
  for (const seg of segs) {
    if (!level) return null
    node = level.find((n) => n.entry.name === seg) || null
    if (!node) return null
    level = node.children
  }
  return node
}
function parentRelOf(rel: string): string {
  const i = rel.lastIndexOf('/')
  return i >= 0 ? rel.slice(0, i) : ''
}
// 变更后刷新受影响目录（reconcile 保留展开态）：父为根 → refreshDir(null)。
async function refreshAfter(rel: string): Promise<void> {
  const prel = parentRelOf(rel)
  await refreshDir(prel ? findNode(prel) : null)
  void loadRecent()
}

// ── 新建 文件 / 目录（树顶输入条）──
const creating = ref<{ parentRel: string; kind: 'file' | 'dir' } | null>(null)
const createName = ref('')
const createInput = ref<HTMLInputElement>()
const createBusy = ref(false)
async function startCreate(parentRel: string, kind: 'file' | 'dir'): Promise<void> {
  if (parentRel) {
    const p = findNode(parentRel)
    if (p) { await ensureChildren(p); p.expanded = true; bumpTree() } // 先展开父 → 新项可见
  }
  creating.value = { parentRel, kind }
  createName.value = ''
  await nextTick()
  createInput.value?.focus()
}
function cancelCreate(): void { creating.value = null; createName.value = '' }
async function commitCreate(): Promise<void> {
  if (!creating.value || createBusy.value) return
  const name = createName.value.trim()
  if (!name) { cancelCreate(); return }
  if (name.includes('/')) { toast('名称不能含 /'); return }
  const { parentRel, kind } = creating.value
  const rel = parentRel ? `${parentRel}/${name}` : name
  createBusy.value = true
  try {
    const r = kind === 'dir'
      ? await filesMkdir(props.sessionId, rel, props.cwd)
      : await filesCreate(props.sessionId, rel, props.cwd)
    if (r.ok) { await refreshAfter(rel); toast(kind === 'dir' ? '已新建目录' : '已新建文件'); cancelCreate() }
    else if (r.status === 409) toast('同名已存在')
    else toast(`新建失败（${r.status || '网络'}）`)
  } finally {
    createBusy.value = false
  }
}

// ── 重命名（行内输入）──
const renaming = ref<string | null>(null) // 正在改名节点的 rel
const renameName = ref('')
const renameBusy = ref(false)
async function startRename(node: TreeNode): Promise<void> {
  renaming.value = node.rel
  renameName.value = node.entry.name
  await nextTick()
  // 输入框在 v-for 内（string ref 会变数组），直接按 testid 取当前唯一那个。
  const el = document.querySelector<HTMLInputElement>('[data-testid="fp-tree-rename-input"]')
  el?.focus()
  el?.select()
}
function cancelRename(): void { renaming.value = null; renameName.value = '' }
async function commitRename(node: TreeNode): Promise<void> {
  if (renaming.value !== node.rel || renameBusy.value) return
  const name = renameName.value.trim()
  if (!name || name === node.entry.name) { cancelRename(); return }
  if (name.includes('/')) { toast('名称不能含 /'); return }
  const prel = parentRelOf(node.rel)
  const to = prel ? `${prel}/${name}` : name
  renameBusy.value = true
  try {
    const r = await filesRename(props.sessionId, node.rel, to, props.cwd)
    if (r.ok) { await refreshAfter(to); toast('已重命名'); cancelRename() }
    else if (r.status === 409) toast('目标名已存在')
    else toast(`重命名失败（${r.status || '网络'}）`)
  } finally {
    renameBusy.value = false
  }
}

// ── 删除（确认条）──
const deleting = ref<TreeNode | null>(null)
const deleteBusy = ref(false)
function askDelete(node: TreeNode): void { deleting.value = node }
function cancelDelete(): void { deleting.value = null }
async function confirmDelete(): Promise<void> {
  if (!deleting.value || deleteBusy.value) return
  const node = deleting.value
  deleteBusy.value = true
  try {
    const r = await filesDelete(props.sessionId, node.rel, props.cwd)
    if (r.ok) {
      await refreshAfter(node.rel)
      if (preview.value?.rel === node.rel) closePreview()
      toast('已删除'); deleting.value = null
    } else toast(`删除失败（${r.status || '网络'}）`)
  } finally {
    deleteBusy.value = false
  }
}

// ── 上传到某目录（context menu「上传到此」）── 同走分片可续传队列，落到该 rel 目录。
const ctxUploadInput = ref<HTMLInputElement>()
const ctxUploadTarget = ref('') // 目标 rel 目录
function pickCtxUpload(rel: string): void { ctxUploadTarget.value = rel; void nextTick(() => ctxUploadInput.value?.click()) }
function onCtxUploadPicked(e: Event): void {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (files.length) treeUpload.enqueue(files, ctxUploadTarget.value || '.')
}

// ── 右键 / 长按 上下文菜单（统一 CRUD 入口，桌面移动通用）──
const ctxMenu = ref<{ node: TreeNode; x: number; y: number } | null>(null)
function openCtxAt(node: TreeNode, x: number, y: number): void {
  const pad = 8, mw = 190, mh = 300
  ctxMenu.value = { node, x: Math.min(x, window.innerWidth - mw - pad), y: Math.min(y, window.innerHeight - mh - pad) }
}
function onRowContextMenu(node: TreeNode, ev: MouseEvent): void { ev.preventDefault(); openCtxAt(node, ev.clientX, ev.clientY) }
function closeCtx(): void { ctxMenu.value = null }
// 关菜单 → 执行动作（先捕获 node，再关，避免 ctxMenu 置空后取不到）。
function ctxAction(fn: (node: TreeNode) => void): void {
  const m = ctxMenu.value
  closeCtx()
  if (m) fn(m.node)
}
// ── 键盘导航状态（vim 键 + 方向键；焦点行高亮）──
const focusedRel = ref<string | null>(null)
const treeScrollEl = ref<HTMLElement>()
const showCheats = ref(false)
// focusTree：把键盘焦点落到树容器上，键盘导航从此生效（tabindex+keydown 都在容器）。
// preventScroll 避免 focus 触发跳动。容器 @click 也调它，保证点树任意处都能接管键盘。
function focusTree(): void { treeScrollEl.value?.focus({ preventScroll: true }) }

// 行点击：聚焦该行（键盘从此接管）+ 目录切展开/折叠、文件预览。
// suppressClick 让长按开菜单后随之而来的 tap 不再触发。
let suppressClick = false
function onRowClick(node: TreeNode): void {
  if (suppressClick) { suppressClick = false; return }
  focusedRel.value = node.rel
  focusTree()
  if (node.entry.isDir) void toggleNode(node)
  else void previewTreeFile(node)
}
// 长按（触屏）→ 上下文菜单；滚动（pointermove 超阈值）取消，不误触发。
let pressTimer: ReturnType<typeof setTimeout> | null = null
let pressNode: TreeNode | null = null
let pressXY = { x: 0, y: 0 }
function cancelPress(): void { if (pressTimer) { clearTimeout(pressTimer); pressTimer = null } pressNode = null }
function onRowPointerDown(node: TreeNode, ev: PointerEvent): void {
  if (ev.pointerType !== 'touch') return
  pressNode = node
  pressXY = { x: ev.clientX, y: ev.clientY }
  pressTimer = setTimeout(() => {
    if (pressNode) { suppressClick = true; openCtxAt(pressNode, pressXY.x, pressXY.y) }
    pressTimer = null
  }, 480)
}
function onRowPointerMove(ev: PointerEvent): void {
  if (!pressTimer) return
  if (Math.abs(ev.clientX - pressXY.x) > 10 || Math.abs(ev.clientY - pressXY.y) > 10) cancelPress()
}
function onRowPointerUp(): void { cancelPress() }

// ── 键盘导航（vim-literal + 方向键）。容器 tabindex=0 + @keydown；焦点在树内即生效。──
function focusedNode(): TreeNode | null {
  return focusedRel.value ? visibleNodes.value.find((n) => n.rel === focusedRel.value) || null : null
}
function focusRow(rel: string | null): void {
  focusedRel.value = rel
  if (!rel) return
  void nextTick(() => {
    const rows = treeScrollEl.value?.querySelectorAll('li[data-testid^="fp-tree-"]')
    if (!rows) return
    for (const row of Array.from(rows)) {
      if (row.getAttribute('data-testid') === `fp-tree-${rel}`) { (row as HTMLElement).scrollIntoView({ block: 'nearest' }); break }
    }
  })
}
function moveFocus(delta: number): void {
  const list = visibleNodes.value
  if (!list.length) return
  const cur = focusedRel.value ? list.findIndex((n) => n.rel === focusedRel.value) : -1
  const idx = cur < 0 ? (delta > 0 ? 0 : list.length - 1) : Math.max(0, Math.min(list.length - 1, cur + delta))
  focusRow(list[idx].rel)
}
// 新建/上传的目标目录：焦点是目录→它本身；焦点是文件→其父目录；无焦点→根。
function targetDirOf(node: TreeNode | null): string {
  if (!node) return ''
  return node.entry.isDir ? node.rel : parentRelOf(node.rel)
}
async function onTreeKeydown(e: KeyboardEvent): Promise<void> {
  const tag = (e.target as HTMLElement).tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA') return          // 输入中不劫持
  if (creating.value || renaming.value || deleting.value) return
  const node = focusedNode()
  switch (e.key) {
    case 'j': case 'ArrowDown': e.preventDefault(); moveFocus(1); break
    case 'k': case 'ArrowUp': e.preventDefault(); moveFocus(-1); break
    case 'h': case 'ArrowLeft':
      e.preventDefault()
      if (node) {
        if (node.entry.isDir && node.expanded) { node.expanded = false; bumpTree() } // 折叠
        else { const p = parentRelOf(node.rel); if (p) focusRow(p) }                  // 或跳父
      }
      break
    case 'l': case 'ArrowRight':
      e.preventDefault()
      if (node?.entry.isDir) {
        if (!node.expanded) { await ensureChildren(node); node.expanded = true; bumpTree() } // 展开
        else if (node.children && node.children.length) focusRow(node.children[0].rel)        // 或进首子
      }
      break
    case 'Enter': case 'o':
      e.preventDefault()
      if (node) { if (node.entry.isDir) void toggleNode(node); else void previewTreeFile(node) }
      break
    case 'a': e.preventDefault(); void startCreate(targetDirOf(node), 'file'); break
    case 'A': e.preventDefault(); void startCreate(targetDirOf(node), 'dir'); break
    case 'r': if (node) { e.preventDefault(); void startRename(node) } break
    case 'd': if (node) { e.preventDefault(); askDelete(node) } break
    case 'u': e.preventDefault(); pickCtxUpload(targetDirOf(node)); break
    case '/': e.preventDefault(); document.querySelector<HTMLInputElement>('input[data-testid="fp-tree-search"], [data-testid="fp-tree-search"] input')?.focus(); break
    case 'g': e.preventDefault(); if (visibleNodes.value.length) focusRow(visibleNodes.value[0].rel); break
    case 'G': e.preventDefault(); if (visibleNodes.value.length) focusRow(visibleNodes.value[visibleNodes.value.length - 1].rel); break
    case '?': e.preventDefault(); showCheats.value = !showCheats.value; break
    case 'Escape': focusedRel.value = null; showCheats.value = false; break
  }
}
// 键位速查表（?⌨ 弹出）
const CHEATS: { k: string; d: string }[] = [
  { k: 'j / k', d: '上下移动' },
  { k: 'h / l', d: '折叠 / 展开（或跳父/进子）' },
  { k: 'Enter', d: '打开（目录展开 / 文件预览）' },
  { k: 'a / A', d: '新建 文件 / 目录' },
  { k: 'r', d: '重命名' },
  { k: 'd', d: '删除' },
  { k: 'u', d: '上传到此' },
  { k: '/', d: '搜索' },
  { k: 'g / G', d: '顶部 / 底部' },
  { k: '? ', d: '本速查 · Esc 关闭' },
]

// ── 上传限额 ⚙ 快调（服务端可配，全局设置）──
const { uploadMaxMb, bounds: uploadBounds, load: loadUploadLimit, save: saveUploadLimit } = useUploadLimit()
const showSettings = ref(false)
const settingsMb = ref<number>(uploadMaxMb.value)
const settingsBusy = ref(false)
async function openSettings(): Promise<void> {
  showSettings.value = true
  await loadUploadLimit()
  settingsMb.value = uploadMaxMb.value
}
async function saveSettings(): Promise<void> {
  if (settingsBusy.value) return
  settingsBusy.value = true
  try {
    const eff = await saveUploadLimit(Math.round(Number(settingsMb.value) || 0))
    if (eff != null) { settingsMb.value = eff; toast(`上传上限已设为 ${eff} MB`); showSettings.value = false }
    else toast('设置失败')
  } finally {
    settingsBusy.value = false
  }
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
    void revealPath(entry.rel)
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
async function previewTreeFile(node: TreeNode): Promise<void> {
  await previewRel(node.entry.name, nodeAbsPath(node), node.rel)
}
function closePreview(): void {
  revokePreviewUrl()
  preview.value = null
}
onUnmounted(revokePreviewUrl)

// An .html/.htm preview also gets a 渲染 URL, so FilePreview can offer the 源码/渲染 toggle.
// Built from the SAME rel the preview fetched, so both views resolve to one file.
const previewRenderSrc = computed(() => {
  const p = preview.value
  if (!p || p.result.kind !== 'text') return ''
  return /\.html?$/i.test(p.name) ? filesRawRenderUrl(props.sessionId, p.rel, props.cwd) : ''
})

// previewErrorText turns a failed /files/raw into a human reason (the ask: "allow it to fail,
// but say WHY"). The 403 case is the common one — a path outside the workbench-anchored root
// (e.g. ~/.claude/projects/…/memory/x.md while the drawer is anchored to a project) — which the
// bare "预览失败" never explained. Falls back to the backend's error string, then the status.
function previewErrorText(r: RawResult): string {
  if (r.kind !== 'error') return '预览失败'
  switch (r.status) {
    case 403: return '此文件在工作台锚定目录之外，无法预览（切到该文件所在项目，或在目录树中打开）'
    case 404: return '文件不存在或已被移动'
    case 400: return '这是一个目录，无法作为文件预览'
  }
  if (r.reason) return `预览失败：${r.reason}`
  return r.status ? `预览失败（HTTP ${r.status}）` : '预览失败（网络错误，请重试）'
}

// onDocNavigate — the reader followed an in-doc link ([[wikilink]] or a relative .md path,
// resolved to an absolute path by FilePreview). Open it in the SAME preview overlay so reading
// flows doc-to-doc without leaving the drawer. filesRaw takes the absolute path as its rel arg;
// safeResolve accepts an in-cwd absolute path as-is, and a failure now explains itself (403 →
// "outside the workbench root"), so a cross-project link degrades to a clear reason, not a dead tap.
function onDocNavigate(absPath: string): void {
  const name = absPath.split('/').pop() || absPath
  void previewRel(name, absPath, absPath)
}

// ── inject / compose bubbling ──
function injectPath(path: string): void {
  if (!path) { toast('无法插入：缺少路径'); return }
  emit('inject', path)
  toast('已插入对话')
}

// triggerDownload — the "sftp get" half: stream a file's real bytes to disk for ANY format
// (text / image / binary / oversized). Uses a DIRECT authed URL (filesDownloadUrl) on a synthetic
// <a download>, NOT fetch→blob: the browser streams straight to disk instead of buffering the whole
// file in JS memory (a large file / mobile would OOM), and the server's ServeContent download branch
// carries Accept-Ranges so a dropped download resumes. Same rel the preview/tree row uses.
function triggerDownload(rel: string, name: string): void {
  const url = filesDownloadUrl(props.sessionId, rel, props.cwd)
  if (!url) { toast('下载失败'); return }
  const a = document.createElement('a')
  a.href = url
  a.download = name || 'download'
  document.body.appendChild(a)
  a.click()
  a.remove()
}
function doDownload(): void {
  const p = preview.value
  if (p) triggerDownload(p.rel, p.name)
}
// 树内文件行「下载」（右键/长按菜单）——不必先开预览即可取回，sftp get 对称。
function downloadNode(node: TreeNode): void {
  if (node.entry.isDir) { toast('暂不支持下载目录'); return }
  triggerDownload(node.rel, node.entry.name)
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

// Re-anchor when the target session OR the ANCHORED cwd prop changes — i.e. ONLY when the
// user explicitly re-anchors via the drawer's pane pill (or a session switch). A plain tmux
// pane/window switch no longer touches props.cwd, so the file tree / preview the user is
// mid-read on stays put. On re-anchor: reset the tree to the new root + reload the sub-tab.
function reanchor(): void {
  recent.value = []
  roots.value = []
  recentQuery.value = ''
  treeQuery.value = ''
  searchResults.value = []
  searching.value = false
  closePreview()
  if (props.mode === 'recent') void loadRecent()
  else { void loadRoot(); void loadRecent() } // tree also loads recent → agent-edit highlight
}
watch(() => props.sessionId, reanchor)
watch(() => props.cwd, reanchor)

// tree mode loads BOTH the browse level and the recent-edited set: the latter feeds the in-tree
// "agent just touched this" highlight (see recentEditedAt). recent mode only needs the list.
onMounted(() => {
  if (props.mode === 'recent') { void loadRecent(); return }
  void loadRoot(); void loadRecent(); void loadUploadLimit() // tree: 也预取上传限额
})

defineExpose({ loadRecent, refreshRoot: () => refreshDir(null) })
</script>

<template>
  <div class="fp flex flex-col h-full bg-background text-foreground" :data-testid="`files-panel-${mode}`">
    <!-- ── 最近修改 (mode='recent') ── -->
    <div v-if="mode === 'recent'" class="flex-1 flex flex-col overflow-hidden">
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
    <div v-else class="flex flex-col flex-1 overflow-hidden">
      <!-- Recursive search (debounced) — replaces the browse with a flat results list. -->
      <DrawerSearchBox
        v-model="treeQuery"
        placeholder="搜索文件 / 目录（递归）…"
        testid="fp-tree-search"
        class="shrink-0 border-b border-border/40 px-2 py-1.5"
      />

      <!-- root header：cwd 名 + 绝对路径 + 操作（浏览模式）——嵌套树下钻靠就地展开，不需面包屑 -->
      <div v-show="!treeQuery.trim()" class="shrink-0 flex flex-col border-b border-border">
        <div class="flex items-center gap-1 px-2 py-1.5">
          <Folder class="size-3.5 shrink-0 text-primary/80" />
          <span class="min-w-0 flex-1 text-[0.72rem] font-medium text-foreground truncate" :title="treeCwd">{{ treeCwd ? baseName(treeCwd) : '目录树' }}</span>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button" title="在根目录新建文件"
            data-testid="fp-tree-new-file"
            @click="startCreate('', 'file')"
          ><FilePlus class="size-3.5" /></button>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button" title="在根目录新建文件夹"
            data-testid="fp-tree-new-folder"
            @click="startCreate('', 'dir')"
          ><FolderPlus class="size-3.5" /></button>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button" title="全部折叠"
            data-testid="fp-tree-collapse-all"
            @click="collapseAll"
          ><ChevronsDownUp class="size-3.5" /></button>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button" title="键盘快捷键（?）"
            data-testid="fp-tree-cheats"
            @click="showCheats = !showCheats; focusTree()"
          ><Keyboard class="size-3.5" /></button>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button" title="上传设置"
            data-testid="fp-tree-settings"
            @click="openSettings"
          ><Settings class="size-3.5" /></button>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button"
            title="上传文件到根目录（分片可续传，大文件/弱网也稳）"
            data-testid="fp-tree-upload"
            @click="pickTreeUpload"
          ><Upload class="size-3.5" :class="{ 'animate-pulse': uploadJobs.some((j) => j.status === 'active') }" /></button>
          <button
            class="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 shrink-0"
            type="button" title="复制根目录路径"
            data-testid="fp-tree-copy-cwd"
            @click="copyText(treeCwd.replace(/\/+$/, ''), 'cwd')"
          >
            <Check v-if="copiedKey === 'cwd'" class="size-3.5 text-green-500" />
            <Copy v-else class="size-3.5" />
          </button>
          <input ref="treeUploadInput" type="file" multiple class="hidden" data-testid="fp-tree-upload-input" @change="onTreeUploadPicked" />
        </div>
        <div
          v-if="treeCwd"
          class="px-2 pb-1 text-[0.56rem] text-muted-foreground/50 truncate select-all"
          :title="treeCwd"
          data-testid="fp-tree-cwd-abs"
        >{{ treeCwd }}</div>
      </div>

      <!-- 新建 文件/目录 输入条 -->
      <div v-if="creating" class="shrink-0 flex items-center gap-1.5 px-2 py-1.5 border-b border-border bg-primary/[0.06]" data-testid="fp-tree-create-bar">
        <FolderPlus v-if="creating.kind === 'dir'" class="size-3.5 shrink-0 text-primary/80" />
        <FilePlus v-else class="size-3.5 shrink-0 text-primary/80" />
        <span class="shrink-0 max-w-[38%] truncate text-[0.6rem] text-muted-foreground" :title="creating.parentRel || baseName(treeCwd)">{{ (creating.parentRel || baseName(treeCwd)) + '/' }}</span>
        <input
          ref="createInput"
          v-model="createName"
          class="min-w-0 flex-1 rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground outline-none focus:border-primary"
          :placeholder="creating.kind === 'dir' ? '目录名' : '文件名'"
          data-testid="fp-tree-create-input"
          @keydown.enter="commitCreate"
          @keydown.esc="cancelCreate"
        />
        <button class="p-1 rounded text-green-500 hover:bg-muted/50 shrink-0" type="button" title="确定" :disabled="createBusy" @click="commitCreate"><Check class="size-3.5" /></button>
        <button class="p-1 rounded text-muted-foreground hover:bg-muted/50 shrink-0" type="button" title="取消" @click="cancelCreate"><X class="size-3.5" /></button>
      </div>

      <!-- 删除确认条 -->
      <div v-if="deleting" class="shrink-0 flex items-center gap-2 px-2 py-1.5 border-b border-red-500/30 bg-red-500/10" data-testid="fp-tree-delete-bar">
        <Trash2 class="size-3.5 shrink-0 text-red-500" />
        <span class="min-w-0 flex-1 text-[0.66rem] text-foreground truncate">删除 <b>{{ deleting.entry.name }}</b><template v-if="deleting.entry.isDir">（含内容）</template>？不可撤销</span>
        <button class="px-2 py-0.5 rounded text-[0.62rem] text-muted-foreground border border-border hover:bg-muted/50 shrink-0" type="button" @click="cancelDelete">取消</button>
        <button class="px-2 py-0.5 rounded text-[0.62rem] text-white bg-red-500 hover:bg-red-600 shrink-0 disabled:opacity-50" type="button" :disabled="deleteBusy" data-testid="fp-tree-delete-confirm" @click="confirmDelete">删除</button>
      </div>

      <!-- 上传进度条（分片可续传：每文件一行，进度/取消/重试）-->
      <div v-if="uploadJobs.length" class="shrink-0 border-b border-border bg-muted/20" data-testid="fp-upload-strip">
        <div class="flex items-center justify-between px-2 pt-1">
          <span class="text-[0.56rem] font-medium uppercase tracking-wide text-muted-foreground/70">上传（分片可续传）</span>
          <button class="text-[0.56rem] text-muted-foreground/70 hover:text-foreground" type="button" @click="treeUpload.clearSettled()">清除已完成</button>
        </div>
        <ul class="max-h-28 overflow-y-auto px-2 py-1 space-y-1">
          <li v-for="j in uploadJobs" :key="j.id" class="flex items-center gap-2" :data-testid="`fp-upload-job`">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5">
                <span class="min-w-0 flex-1 truncate text-[0.62rem] text-foreground" :title="j.name">{{ j.name }}</span>
                <span
                  class="shrink-0 text-[0.56rem] tabular-nums"
                  :class="j.status === 'error' ? 'text-red-500' : j.status === 'done' ? 'text-green-500' : 'text-muted-foreground/70'"
                >{{ j.status === 'done' ? '完成' : j.status === 'canceled' ? '已取消' : j.status === 'error' ? '' : uploadPct(j) + '%' }}</span>
              </div>
              <div class="mt-0.5 h-1 rounded-full bg-border/60 overflow-hidden">
                <div
                  class="h-full rounded-full transition-all"
                  :class="j.status === 'error' ? 'bg-red-500/70' : j.status === 'done' ? 'bg-green-500' : 'bg-primary'"
                  :style="{ width: (j.status === 'done' ? 100 : uploadPct(j)) + '%' }"
                ></div>
              </div>
              <div v-if="j.status === 'error'" class="mt-0.5 text-[0.54rem] text-red-500/90 truncate" :title="j.error">{{ j.error }}</div>
            </div>
            <button
              v-if="j.status === 'error'"
              class="shrink-0 px-1.5 py-0.5 rounded text-[0.56rem] text-primary border border-primary/50 hover:bg-primary/10"
              type="button" data-testid="fp-upload-retry"
              @click="treeUpload.retry(j.id)"
            >重试</button>
            <button
              v-if="j.status === 'active'"
              class="shrink-0 p-0.5 rounded text-muted-foreground/70 hover:text-red-500 hover:bg-muted/50"
              type="button" title="取消" data-testid="fp-upload-cancel"
              @click="treeUpload.cancel(j.id)"
            ><X class="size-3" /></button>
          </li>
        </ul>
      </div>

      <!-- 隐藏：context menu「上传到此」的文件选择 -->
      <input ref="ctxUploadInput" type="file" multiple class="hidden" data-testid="fp-tree-ctx-upload-input" @change="onCtxUploadPicked" />

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

      <!-- ── browse（嵌套 lazy 展开树 — VSCode 式）；tabindex+keydown = vim 键盘导航 ── -->
      <div v-else ref="treeScrollEl" tabindex="0" class="flex-1 overflow-y-auto py-1 outline-none" data-testid="fp-tree" @keydown="onTreeKeydown" @click="focusTree">
        <div v-if="treeLoading && !roots.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">加载中…</div>
        <div v-else-if="!roots.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic">空目录</div>
        <ul v-else class="flex flex-col">
          <li
            v-for="n in visibleNodes"
            :key="n.rel"
            class="group flex items-center rounded-md pr-1 hover:bg-muted/40 transition-colors"
            :class="[recentEditedAt(n) ? 'fp-row-recent' : '', focusedRel === n.rel ? 'fp-row-focused' : '']"
            :data-testid="`fp-tree-${n.rel}`"
            @contextmenu="onRowContextMenu(n, $event)"
            @pointerdown="onRowPointerDown(n, $event)"
            @pointermove="onRowPointerMove"
            @pointerup="onRowPointerUp"
            @pointercancel="onRowPointerUp"
          >
            <!-- 行内重命名 -->
            <div
              v-if="renaming === n.rel"
              class="min-w-0 flex-1 flex items-center gap-1.5 py-1.5"
              :style="{ paddingLeft: `${6 + n.depth * 14}px` }"
            >
              <span class="shrink-0 w-4 flex items-center justify-center text-muted-foreground">
                <ChevronRight v-if="n.entry.isDir" class="size-3.5" :class="{ 'rotate-90': n.expanded }" />
              </span>
              <Folder v-if="n.entry.isDir" class="size-4 shrink-0 text-primary/80" />
              <FileText v-else class="size-4 shrink-0 text-muted-foreground" />
              <input
                v-model="renameName"
                class="min-w-0 flex-1 rounded border border-primary bg-background px-1 py-0.5 text-xs text-foreground outline-none"
                data-testid="fp-tree-rename-input"
                @keydown.enter="commitRename(n)"
                @keydown.esc="cancelRename"
                @blur="commitRename(n)"
                @click.stop
                @pointerdown.stop
              />
            </div>
            <!-- 常规行：整行可点（大触控区，修"箭头点不中"）——目录切展开/折叠，文件预览 -->
            <template v-else>
              <button
                type="button"
                class="min-w-0 flex-1 flex items-center gap-1.5 py-1.5 text-left"
                :style="{ paddingLeft: `${6 + n.depth * 14}px` }"
                :data-testid="`fp-tree-row-${n.rel}`"
                @click="onRowClick(n)"
              >
                <span v-if="n.entry.isDir" class="shrink-0 w-4 flex items-center justify-center text-muted-foreground">
                  <Loader2 v-if="n.loading" class="size-3.5 animate-spin" />
                  <ChevronRight v-else class="size-3.5 transition-transform" :class="{ 'rotate-90': n.expanded }" />
                </span>
                <span v-else class="shrink-0 w-4"></span>
                <Folder v-if="n.entry.isDir" class="size-4 shrink-0 text-primary/80" />
                <ImageIcon v-else-if="isImage(n.entry.name)" class="size-4 shrink-0 text-muted-foreground" />
                <FileText v-else class="size-4 shrink-0 text-muted-foreground" :class="recentEditedAt(n) ? 'text-primary/80' : ''" />
                <span class="min-w-0 flex-1 truncate text-xs" :class="n.entry.isDir ? 'text-foreground font-medium' : 'text-foreground'">{{ n.entry.name }}<span v-if="n.entry.isDir" class="text-muted-foreground/60">/</span></span>
              </button>
              <!-- agent 刚碰过徽标 / 文件大小 -->
              <span v-if="recentEditedAt(n)" class="shrink-0 px-1 text-[0.56rem] text-primary/80 tabular-nums" title="agent 最近修改">{{ relTime(recentEditedAt(n)!) }}</span>
              <span v-else-if="!n.entry.isDir" class="shrink-0 px-1 text-[0.56rem] text-muted-foreground/70 tabular-nums opacity-0 group-hover:opacity-100">{{ fmtSize(n.entry.size) }}</span>
              <!-- 更多操作（⋮）——与右键/长按同款菜单，给桌面一个可见入口 -->
              <button
                type="button"
                class="shrink-0 p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/60 opacity-0 group-hover:opacity-100 focus:opacity-100"
                title="更多操作"
                :data-testid="`fp-tree-more-${n.rel}`"
                @click.stop="openCtxAt(n, $event.clientX, $event.clientY)"
              ><MoreVertical class="size-3.5" /></button>
            </template>
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
          <FilePreview v-else-if="preview.result.kind === 'text'" :name="preview.name" :text="preview.result.text" :path="preview.absPath" :render-src="previewRenderSrc" @navigate="onDocNavigate" @toast="toast" />
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
          <div v-else class="flex flex-col items-center justify-center gap-2 h-full px-5 text-center">
            <FileText class="size-7 text-muted-foreground/50" />
            <p class="text-xs text-muted-foreground leading-snug">{{ previewErrorText(preview.result) }}</p>
            <p class="text-[0.6rem] text-muted-foreground/60 break-all select-all" :title="preview.absPath">{{ preview.absPath }}</p>
            <button
              class="mt-1 px-2 py-1 rounded text-[0.62rem] text-green-500 border border-border hover:bg-muted/50"
              type="button"
              @click="copyText(preview.absPath, 'perr')"
            >
              <Check v-if="copiedKey === 'perr'" class="inline size-3 text-green-500" /> 复制路径
            </button>
          </div>
        </div>
      </div>
    </Transition>

    <div v-if="toastMsg" class="fp-toast">{{ toastMsg }}</div>

    <!-- 键盘快捷键速查（?⌨ 或按 ? 弹出）-->
    <div v-if="showCheats" class="fp-cheats" data-testid="fp-tree-cheats-panel" @click="showCheats = false">
      <div class="fp-cheats-card" @click.stop>
        <div class="mb-2 flex items-center justify-between">
          <span class="flex items-center gap-1.5 text-xs font-medium text-foreground"><Keyboard class="size-3.5" />键盘快捷键</span>
          <button class="p-0.5 rounded text-muted-foreground hover:text-foreground" type="button" @click="showCheats = false"><X class="size-3.5" /></button>
        </div>
        <ul class="flex flex-col gap-1">
          <li v-for="c in CHEATS" :key="c.k" class="flex items-center gap-2 text-[0.66rem]">
            <kbd class="fp-kbd">{{ c.k }}</kbd>
            <span class="text-muted-foreground">{{ c.d }}</span>
          </li>
        </ul>
      </div>
    </div>

    <!-- 上传限额 ⚙ 快调（服务端可配，全局）-->
    <div v-if="showSettings" class="fp-cheats" data-testid="fp-tree-settings-panel" @click="showSettings = false">
      <div class="fp-cheats-card" @click.stop>
        <div class="mb-2 flex items-center justify-between">
          <span class="flex items-center gap-1.5 text-xs font-medium text-foreground"><Settings class="size-3.5" />上传设置</span>
          <button class="p-0.5 rounded text-muted-foreground hover:text-foreground" type="button" @click="showSettings = false"><X class="size-3.5" /></button>
        </div>
        <label class="mb-1 block text-[0.66rem] text-muted-foreground">单文件上限（MB）</label>
        <div class="flex items-center gap-2">
          <input
            v-model.number="settingsMb"
            type="number"
            :min="uploadBounds.floorMb"
            :max="uploadBounds.ceilingMb"
            class="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1 text-xs text-foreground outline-none focus:border-primary"
            data-testid="fp-tree-settings-input"
            @keydown.enter="saveSettings"
          />
          <button
            class="fp-btn-primary shrink-0 px-3 py-1 text-xs font-medium disabled:opacity-50"
            type="button" :disabled="settingsBusy"
            data-testid="fp-tree-settings-save"
            @click="saveSettings"
          >保存</button>
        </div>
        <p class="mt-1.5 text-[0.6rem] text-muted-foreground/70">可调 {{ uploadBounds.floorMb }}–{{ uploadBounds.ceilingMb }} MB（硬顶 1GB）。改后服务端按此放行，超限仍返 413（可压缩成 zip）。</p>
      </div>
    </div>

    <!-- 右键 / 长按 / ⋮ 上下文菜单（统一 CRUD 入口，桌面移动通用）-->
    <Teleport to="body">
      <div
        v-if="ctxMenu"
        class="fixed inset-0 z-[400]"
        data-testid="fp-tree-ctxmenu"
        @click="closeCtx"
        @contextmenu.prevent="closeCtx"
      >
        <ul
          class="fp-ctx absolute min-w-[178px] rounded-lg border border-border bg-card py-1 text-xs shadow-xl"
          :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }"
          @click.stop
        >
          <li v-if="ctxMenu.node.entry.isDir"><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => startCreate(nd.rel, 'file'))"><FilePlus class="size-3.5" />新建文件</button></li>
          <li v-if="ctxMenu.node.entry.isDir"><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => startCreate(nd.rel, 'dir'))"><FolderPlus class="size-3.5" />新建文件夹</button></li>
          <li v-if="ctxMenu.node.entry.isDir"><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => pickCtxUpload(nd.rel))"><Upload class="size-3.5" />上传到此</button></li>
          <li v-if="!ctxMenu.node.entry.isDir"><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => injectPath(nodeAbsPath(nd)))"><Link2 class="size-3.5" />插入到对话</button></li>
          <li v-if="!ctxMenu.node.entry.isDir"><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => downloadNode(nd))"><Download class="size-3.5" />下载</button></li>
          <li><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => startRename(nd))"><Pencil class="size-3.5" />重命名</button></li>
          <li><button type="button" class="fp-ctx-item" @click="ctxAction((nd) => copyText(nodeAbsPath(nd), 'ctx'))"><Copy class="size-3.5" />复制路径</button></li>
          <li class="fp-ctx-sep"></li>
          <li><button type="button" class="fp-ctx-item fp-ctx-danger" @click="ctxAction((nd) => askDelete(nd))"><Trash2 class="size-3.5" />删除</button></li>
        </ul>
      </div>
    </Teleport>
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
/* 上下文菜单 */
.fp-ctx { z-index: 401; }
.fp-ctx-item {
  display: flex; align-items: center; gap: 8px; width: 100%;
  padding: 7px 12px; text-align: left; color: hsl(var(--foreground));
  transition: background 0.12s;
}
.fp-ctx-item:hover { background: hsl(var(--muted) / 0.6); }
.fp-ctx-danger { color: #ef4444; }
.fp-ctx-sep { height: 1px; margin: 4px 6px; background: hsl(var(--border)); }
/* 键盘速查 */
.fp-cheats {
  position: absolute; inset: 0; z-index: 30;
  display: flex; align-items: center; justify-content: center;
  background: rgba(0, 0, 0, 0.42);
}
.fp-cheats-card {
  width: min(90%, 286px); border-radius: 12px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--card));
  padding: 12px 14px; box-shadow: 0 10px 32px rgba(0, 0, 0, 0.5);
}
.fp-kbd {
  min-width: 52px; text-align: center; padding: 1px 6px; border-radius: 5px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--muted) / 0.5);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.6rem;
  color: hsl(var(--foreground)); white-space: nowrap;
}
/* 键盘焦点行：显式 CSS（不走 Tailwind JIT，保证可见）——填充色 + 左侧强调条，j/k 移动一眼可见 */
.fp-row-focused {
  background: hsl(var(--primary) / 0.16);
  box-shadow: inset 2px 0 0 hsl(var(--primary));
}
/* agent 刚改过的行高亮（同样显式 CSS，避免动态 Tailwind arbitrary-opacity 被 JIT 漏掉） */
.fp-row-recent { background: hsl(var(--primary) / 0.08); }
.fp-row-recent.fp-row-focused { background: hsl(var(--primary) / 0.2); }
/* 主按钮（显式 CSS，避免 bg-primary 被 JIT 漏） */
.fp-btn-primary {
  border-radius: 6px; background: hsl(var(--primary)); color: hsl(var(--primary-foreground));
}
.fp-btn-primary:hover { opacity: 0.9; }
</style>
