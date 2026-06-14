<template>
  <!-- WS5 — 收纳抽屉 (CROSS-SESSION resource drawer). Right-side, three categories:
       图片 / 文件 = every clipboard image + uploaded file across ALL sessions (GET /uploads,
       index-backed); each item carries the originating SESSION chip + relative time so an old
       upload can be re-used from any new session. 输入 = human prompts parsed from the claude +
       codex transcripts (GET /inputs) — NOT the ComposeBar history. A compact filter bar (session
       · sort · search) narrows any tab. Desktop = a collapsible right column reached via a slim
       edge handle; mobile (≤768px) = a slide-out sheet from the right with a scrim. -->
  <Teleport to="body">
    <!-- Edge handle: always present so the drawer can be summoned; hidden while open.
         Vertically draggable (useEdgeDrag) so it can be moved off covered text; a
         short tap still opens the drawer, a drag repositions + persists. -->
    <button
      v-if="!open"
      ref="handleEl"
      class="rd-handle"
      :class="{ 'is-mobile': isMobile }"
      :style="handleStyle"
      type="button"
      title="收纳抽屉（可上下拖动）"
      data-testid="resource-drawer-handle"
      @click="$emit('update:open', true)"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" /><path d="M9 3v18" />
      </svg>
    </button>

    <Transition name="rd-fade">
      <div
        v-if="open"
        class="rd-scrim"
        :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
        data-testid="resource-drawer"
        @click.self="$emit('update:open', false)"
      >
        <div class="rd-panel" @mousedown.prevent>
          <div class="rd-header">
            <span class="rd-title">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#c080ff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 9h18" /><path d="M9 21V9" />
              </svg>
              收纳
            </span>
            <div class="rd-tabs">
              <button
                v-for="t in tabs"
                :key="t.key"
                class="rd-tab"
                :class="{ 'is-active': activeTab === t.key }"
                type="button"
                :data-testid="`rd-tab-${t.key}`"
                @click="activeTab = t.key"
              >
                {{ t.label }}<span v-if="t.count" class="rd-tab-count">{{ t.count }}</span>
              </button>
            </div>
            <button class="rd-close" title="收起" data-testid="resource-drawer-close" @click="$emit('update:open', false)">&times;</button>
          </div>

          <!-- Filter bar (v6 paradigm): session scope · sort · search. -->
          <div class="rd-filter">
            <select v-model="sessionFilter" class="rd-select" data-testid="rd-filter-session" title="按来源筛选">
              <option value="">{{ scopeAllLabel }}</option>
              <option v-for="s in scopeOptions" :key="s" :value="s">{{ s }}</option>
            </select>
            <button
              class="rd-sort"
              type="button"
              data-testid="rd-filter-sort"
              :title="sortNewest ? '最新在前' : '最早在前'"
              @click="sortNewest = !sortNewest"
            >{{ sortNewest ? '最新 ↓' : '最早 ↑' }}</button>
            <input
              v-model.trim="search"
              class="rd-search"
              type="search"
              inputmode="search"
              placeholder="搜索…"
              data-testid="rd-filter-search"
            />
          </div>

          <div class="rd-body">
            <!-- 图片 -->
            <div v-show="activeTab === 'images'" class="rd-pane">
              <div v-if="loading && !images.length" class="rd-empty">加载中…</div>
              <div v-else-if="!images.length" class="rd-empty">{{ uploads.length ? '无匹配图片' : '暂无图片' }}</div>
              <div v-else class="rd-grid">
                <button
                  v-for="img in images"
                  :key="img.id"
                  class="rd-thumb"
                  type="button"
                  :title="img.name"
                  @click="openLightbox(img)"
                >
                  <img class="rd-thumb-img" loading="lazy" :src="rawUrl(img.id)" :alt="img.name" />
                  <span class="rd-thumb-meta">
                    <span class="rd-thumb-name mono">{{ img.name }}</span>
                    <span class="rd-thumb-tags">
                      <span class="rd-chip" :title="img.cwd">{{ img.sessionName || '会话' }}</span>
                      <span class="rd-thumb-time">{{ relTime(img.mtimeMs) }}</span>
                    </span>
                  </span>
                </button>
              </div>
            </div>

            <!-- 文件 -->
            <div v-show="activeTab === 'files'" class="rd-pane">
              <div v-if="loading && !files.length" class="rd-empty">加载中…</div>
              <div v-else-if="!files.length" class="rd-empty">{{ uploads.length ? '无匹配文件' : '暂无文件' }}</div>
              <ul v-else class="rd-list">
                <li v-for="f in files" :key="f.id" class="rd-file">
                  <span class="rd-file-glyph" :class="glyphClass(f.name)">{{ glyphChar(f.name) }}</span>
                  <span class="rd-file-info">
                    <span class="rd-file-name mono">{{ f.name }}</span>
                    <span class="rd-file-meta">
                      <span class="rd-chip" :title="f.cwd">{{ f.sessionName || '会话' }}</span>
                      {{ fmtSize(f.size) }} · {{ relTime(f.mtimeMs) }}
                    </span>
                  </span>
                  <span class="rd-file-actions">
                    <button class="rd-act rd-act--send" type="button" title="插入到对话" @click="injectItem(f)">插入对话</button>
                    <button v-if="isPreviewable(f.name)" class="rd-act" type="button" title="预览" @click="previewFile(f)">预览</button>
                    <a class="rd-act" :href="rawUrl(f.id)" target="_blank" rel="noopener" :download="f.name" title="下载">下载</a>
                    <button class="rd-act" type="button" title="复制文件名" @click="copyText(f.name)">复制</button>
                  </span>
                </li>
              </ul>
            </div>

            <!-- 输入 (human prompts from claude/codex transcripts — cross-session) -->
            <div v-show="activeTab === 'inputs'" class="rd-pane">
              <div v-if="loading && !inputs.length" class="rd-empty">加载中…</div>
              <div v-else-if="!inputs.length" class="rd-empty">{{ allInputs.length ? '无匹配输入' : '暂无输入历史' }}</div>
              <ul v-else class="rd-list">
                <li
                  v-for="(item, i) in inputs"
                  :key="item.source + ':' + item.tsMs + ':' + i"
                  class="rd-input"
                  :class="{ 'is-expanded': expandedInput === i }"
                  @click="toggleInput(i)"
                >
                  <div class="rd-input-head">
                    <span class="rd-badge" :class="`is-${item.source}`">{{ item.source }}</span>
                    <span v-if="item.project" class="rd-input-proj mono">{{ item.project }}</span>
                    <!-- trace 总结: size of the (possibly clamped) prompt, so the user
                         knows how much is hidden before expanding. -->
                    <span class="rd-input-metric" :title="`${textLines(item.text)} 行 · ${textChars(item.text)} 字`">{{ textLines(item.text) }} 行 · {{ textChars(item.text) }} 字</span>
                    <span class="rd-input-time">{{ relTime(item.tsMs) }}</span>
                  </div>
                  <div class="rd-input-text" :class="{ 'is-clamped': expandedInput !== i }">{{ item.text }}</div>
                  <div class="rd-input-actions" @click.stop>
                    <button class="rd-act" type="button" title="复制" @click="copyText(item.text)">复制</button>
                    <button class="rd-act rd-act--send" type="button" title="载入输入框编辑后发送" @click="resend(item.text)">重发</button>
                  </div>
                </li>
              </ul>
            </div>
          </div>

          <div v-if="toast" class="rd-toast">{{ toast }}</div>
        </div>
      </div>
    </Transition>

    <!-- Lightbox: larger image preview with pinch-zoom + pan (mobile) / click-to-close (desktop). -->
    <Transition name="rd-fade">
      <div
        v-if="lightbox"
        class="rd-lightbox"
        data-testid="resource-drawer-lightbox"
        @click="onLightboxBackdrop"
        @touchstart="onLightboxTouchStart"
        @touchmove.prevent="onLightboxTouchMove"
        @touchend="onLightboxTouchEnd"
      >
        <div class="rd-lightbox-stage" @click.stop>
          <img
            class="rd-lightbox-img"
            :class="{ 'is-zoomed': zoom.scale > 1 }"
            :style="{ transform: imgTransform }"
            :src="rawUrl(lightbox.id)"
            :alt="lightbox.name"
            draggable="false"
          />
        </div>
        <div class="rd-lightbox-bar mono" @click.stop @touchstart.stop @touchend.stop>
          <span class="rd-lightbox-name">{{ lightbox.name }}</span>
          <span class="rd-lightbox-size">{{ fmtSize(lightbox.size) }}</span>
          <button class="rd-act rd-act--send" type="button" @click="injectItem(lightbox)">插入对话</button>
          <a class="rd-act" :href="rawUrl(lightbox.id)" target="_blank" rel="noopener" :download="lightbox.name">下载</a>
          <button class="rd-act" type="button" @click="lightbox = null">关闭</button>
        </div>
      </div>
    </Transition>

    <!-- Inline text preview: scrollable monospaced panel (replaces window.open). -->
    <Transition name="rd-fade">
      <div v-if="textPreview" class="rd-lightbox rd-textview" data-testid="resource-drawer-textview" @click="textPreview = null">
        <pre class="rd-textview-body mono" @click.stop>{{ textPreview.content }}</pre>
        <div class="rd-lightbox-bar mono" @click.stop>
          <span class="rd-lightbox-name">{{ textPreview.item.name }}</span>
          <span class="rd-lightbox-size">{{ fmtSize(textPreview.item.size) }}</span>
          <button class="rd-act rd-act--send" type="button" @click="injectItem(textPreview.item)">插入对话</button>
          <a class="rd-act" :href="rawUrl(textPreview.item.id)" target="_blank" rel="noopener" :download="textPreview.item.name">下载</a>
          <button class="rd-act" type="button" @click="textPreview = null">关闭</button>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { useEdgeDrag } from '@terminal/composables/cli/useEdgeDrag'
import { fetchUploads, fetchInputs, fetchRawText, rawUrl, type UploadItem, type InputItem } from '@terminal/api/uploads'

// sessionId is the RESEND TARGET (the live terminal the inject path targets) — it is
// no longer used to fetch resources, which are now global/cross-session.
const props = defineProps<{ sessionId: string; open: boolean }>()
const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  // inject: re-use an uploaded image/file — host routes the path to the SAME
  // clipboard-paste inject chokepoint (插入对话).
  (e: 'inject', path: string): void
  // compose-draft: open the ComposeBar with this text inserted for editing (重发).
  (e: 'compose-draft', text: string): void
}>()

const { isMobile } = useDeviceDetection()

// The summon handle is vertically draggable along the right edge so it can be moved
// off whatever terminal text it overlaps; offset persists per-handle.
const { el: handleEl, style: handleStyle } = useEdgeDrag({ storageKey: 'dw.rdHandle.top' })

type TabKey = 'images' | 'files' | 'inputs'
const activeTab = ref<TabKey>('images')

const uploads = ref<UploadItem[]>([])
const allInputs = ref<InputItem[]>([])
const loading = ref(false)

// --- filter state (shared across tabs) ---
const sessionFilter = ref('') // '' = 全部; else a sessionName (uploads) or source (inputs)
const sortNewest = ref(true)
const search = ref('')

// On the 输入 tab the scope dropdown filters by SOURCE (claude/codex); elsewhere by
// the originating session name. The "全部" label adapts so the control reads naturally.
const scopeAllLabel = computed(() => (activeTab.value === 'inputs' ? '全部来源' : '全部会话'))
const scopeOptions = computed<string[]>(() => {
  const set = new Set<string>()
  if (activeTab.value === 'inputs') {
    for (const it of allInputs.value) set.add(it.source)
  } else {
    for (const u of uploads.value) if (u.sessionName) set.add(u.sessionName)
  }
  return [...set].sort()
})

function matchesSearch(text: string): boolean {
  const q = search.value.toLowerCase()
  return !q || text.toLowerCase().includes(q)
}

function sortByTime<T extends { t: number }>(arr: T[]): T[] {
  return [...arr].sort((a, b) => (sortNewest.value ? b.t - a.t : a.t - b.t))
}

const filteredUploads = computed(() => {
  const rows = uploads.value.filter((u) => {
    if (sessionFilter.value && u.sessionName !== sessionFilter.value) return false
    return matchesSearch(u.name) || matchesSearch(u.sessionName)
  })
  return sortByTime(rows.map((u) => ({ ...u, t: u.mtimeMs }))) as (UploadItem & { t: number })[]
})

const images = computed(() => filteredUploads.value.filter((u) => u.kind === 'image'))
const files = computed(() => filteredUploads.value.filter((u) => u.kind === 'file'))

const inputs = computed(() => {
  const rows = allInputs.value.filter((it) => {
    if (sessionFilter.value && it.source !== sessionFilter.value) return false
    return matchesSearch(it.text) || matchesSearch(it.project)
  })
  return sortByTime(rows.map((it) => ({ ...it, t: it.tsMs }))) as (InputItem & { t: number })[]
})

// Tab counts reflect the UNFILTERED totals so the badge is a stable inventory.
const tabs = computed(() => [
  { key: 'images' as TabKey, label: '图片', count: uploads.value.filter((u) => u.kind === 'image').length },
  { key: 'files' as TabKey, label: '文件', count: uploads.value.filter((u) => u.kind === 'file').length },
  { key: 'inputs' as TabKey, label: '输入', count: allInputs.value.length },
])

const expandedInput = ref<number | null>(null)
const lightbox = ref<UploadItem | null>(null)
const toast = ref('')
let toastTimer: ReturnType<typeof setTimeout> | null = null

// ── Lightbox pinch-zoom + pan (hand-rolled, minimal). scale/tx/ty drive a CSS
// transform on the <img>. Two touches → pinch (distance ratio → scale about the
// gesture midpoint). One touch while zoomed → pan. Double-tap toggles 1×/2.5×.
// Clamped to [MIN,MAX]; reset whenever the lightbox opens/closes. Desktop click to
// close is preserved (only fires when not zoomed / not mid-gesture). ──
const MIN_SCALE = 1
const MAX_SCALE = 5
const zoom = ref({ scale: 1, tx: 0, ty: 0 })
const imgTransform = computed(
  () => `translate(${zoom.value.tx}px, ${zoom.value.ty}px) scale(${zoom.value.scale})`,
)
let pinchStartDist = 0
let pinchStartScale = 1
let panStartX = 0
let panStartY = 0
let panStartTx = 0
let panStartTy = 0
let gestureMoved = false
let lastTapAt = 0

function resetZoom(): void {
  zoom.value = { scale: 1, tx: 0, ty: 0 }
}
function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v))
}
function touchDist(t0: Touch, t1: Touch): number {
  return Math.hypot(t0.clientX - t1.clientX, t0.clientY - t1.clientY)
}

function onLightboxTouchStart(e: TouchEvent): void {
  if (e.touches.length === 2) {
    e.preventDefault()
    pinchStartDist = touchDist(e.touches[0], e.touches[1])
    pinchStartScale = zoom.value.scale
    gestureMoved = true
  } else if (e.touches.length === 1) {
    const t = e.touches[0]
    panStartX = t.clientX
    panStartY = t.clientY
    panStartTx = zoom.value.tx
    panStartTy = zoom.value.ty
    gestureMoved = false
  }
}

function onLightboxTouchMove(e: TouchEvent): void {
  if (e.touches.length === 2 && pinchStartDist > 0) {
    e.preventDefault()
    const dist = touchDist(e.touches[0], e.touches[1])
    zoom.value = { ...zoom.value, scale: clamp((dist / pinchStartDist) * pinchStartScale, MIN_SCALE, MAX_SCALE) }
    gestureMoved = true
  } else if (e.touches.length === 1 && zoom.value.scale > MIN_SCALE) {
    // Pan only when zoomed in; otherwise let the tap/close behaviour stand.
    e.preventDefault()
    const t = e.touches[0]
    const dx = t.clientX - panStartX
    const dy = t.clientY - panStartY
    if (Math.abs(dx) > 4 || Math.abs(dy) > 4) gestureMoved = true
    zoom.value = { ...zoom.value, tx: panStartTx + dx, ty: panStartTy + dy }
  }
}

function onLightboxTouchEnd(e: TouchEvent): void {
  if (e.touches.length === 0) {
    pinchStartDist = 0
    if (zoom.value.scale <= MIN_SCALE) resetZoom() // snap pan back when fully zoomed out
    // Double-tap toggles zoom (only when this wasn't a pinch/pan gesture).
    if (!gestureMoved) {
      const now = Date.now()
      if (now - lastTapAt < 300) {
        zoom.value = zoom.value.scale > MIN_SCALE ? { scale: 1, tx: 0, ty: 0 } : { scale: 2.5, tx: 0, ty: 0 }
        lastTapAt = 0
      } else {
        lastTapAt = now
      }
    }
  }
}

// Desktop / tap-to-close: only close when not zoomed and the gesture was a clean tap.
function onLightboxBackdrop(): void {
  if (zoom.value.scale <= MIN_SCALE && !gestureMoved) lightbox.value = null
}

async function refresh(): Promise<void> {
  loading.value = true
  try {
    const [up, ins] = await Promise.all([fetchUploads(), fetchInputs()])
    uploads.value = up
    allInputs.value = ins
  } finally {
    loading.value = false
  }
}

function onUploadSuccess(): void {
  // A new upload landed; if the drawer is open, pull the fresh cross-session list.
  if (props.open) void refresh()
}

// Refetch whenever the drawer opens. Reset the per-open expansion state.
watch(() => props.open, (isOpen) => {
  if (isOpen) {
    expandedInput.value = null
    void refresh()
  } else {
    lightbox.value = null
    textPreview.value = null
    resetZoom()
  }
})

// Switching tabs clears a scope filter that no longer applies to the new axis.
watch(activeTab, () => { sessionFilter.value = ''; expandedInput.value = null })

onMounted(() => {
  window.addEventListener('dw:upload-success', onUploadSuccess)
})
onBeforeUnmount(() => {
  window.removeEventListener('dw:upload-success', onUploadSuccess)
  if (toastTimer) clearTimeout(toastTimer)
})

function openLightbox(img: UploadItem): void { lightbox.value = img; resetZoom() }

// Inline text preview (no new browser tab). For text-like files we FETCH the raw
// bytes and show them in a scrollable monospaced panel; images go to the lightbox;
// anything else (binary) falls back to a download (handled in the row template).
const textPreview = ref<{ item: UploadItem; content: string } | null>(null)

async function previewFile(f: UploadItem): Promise<void> {
  if (isImageName(f.name)) { openLightbox(f); return }
  const content = await fetchRawText(f.id)
  if (content == null) { showToast('预览失败'); return }
  textPreview.value = { item: f, content }
}

function toggleInput(i: number): void {
  expandedInput.value = expandedInput.value === i ? null : i
}

// "插入对话" — re-use an already-uploaded item. It carries an absolute on-disk path
// (item.path); we emit it to the host, which routes it through the SAME inject
// chokepoint the clipboard-paste flow uses post-upload. One source of truth.
function injectItem(item: UploadItem): void {
  if (!item.path) { showToast('无法插入：缺少路径'); return }
  emit('inject', item.path)
  showToast('已插入对话')
}

// 重发 — open the ComposeBar with the text inserted for editing (NOT a direct send).
function resend(text: string): void {
  emit('compose-draft', text)
  emit('update:open', false)
  showToast('已插入输入框')
}

async function copyText(text: string): Promise<void> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    showToast('已复制')
  } catch {
    showToast('复制失败')
  }
}

function showToast(msg: string): void {
  toast.value = msg
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 1600)
}

// --- input "trace 总结" metrics (lightweight, per-string) ---
// 行 = newline-delimited line count; 字 = non-whitespace character count, which is a
// meaningful "size" for both Chinese (per-char) and English (collapses spacing) text.
function textLines(text: string): number {
  return text ? text.split('\n').length : 0
}
function textChars(text: string): number {
  return text ? text.replace(/\s/g, '').length : 0
}

// --- formatting / glyph helpers ---
function fmtSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

// relTime renders a compact "刚刚 / 5分 / 3时 / 2天" relative time; falls back to a
// MM-DD date for anything older than a week, and to '' for unknown (0) timestamps.
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

function ext(name: string): string {
  const i = name.lastIndexOf('.')
  return i >= 0 ? name.slice(i + 1).toLowerCase() : ''
}

const IMAGE_EXT = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif'])
const TEXT_EXT = new Set(['txt', 'md', 'json', 'csv', 'log', 'yaml', 'yml', 'toml', 'xml', 'js', 'ts', 'go', 'py', 'sh', 'css', 'html'])

function isImageName(name: string): boolean { return IMAGE_EXT.has(ext(name)) }
function isPreviewable(name: string): boolean { return isImageName(name) || TEXT_EXT.has(ext(name)) }

function glyphChar(name: string): string {
  const e = ext(name)
  if (e === 'pdf') return 'PDF'
  if (IMAGE_EXT.has(e)) return 'IMG'
  if (TEXT_EXT.has(e)) return e.slice(0, 3).toUpperCase()
  return 'BIN'
}
function glyphClass(name: string): string {
  const e = ext(name)
  if (e === 'pdf') return 'is-pdf'
  if (IMAGE_EXT.has(e)) return 'is-img'
  if (TEXT_EXT.has(e)) return 'is-text'
  return 'is-bin'
}
</script>

<style scoped>
.mono { font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', monospace; }

/* Edge handle — slim tab clinging to the right viewport edge. */
.rd-handle {
  position: fixed;
  top: 50%;
  right: 0;
  transform: translateY(-50%);
  z-index: 290;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 54px;
  padding: 0;
  background: #160f22;
  border: 1px solid #3a2860;
  border-right: none;
  border-radius: 9px 0 0 9px;
  color: #c080ff;
  cursor: pointer;
  box-shadow: -4px 0 18px rgba(0, 0, 0, 0.45);
  touch-action: none; /* own the vertical drag (useEdgeDrag); no page scroll hijack */
}
.rd-handle:active { background: #1f1533; }
.rd-handle.is-mobile { width: 30px; height: 62px; }

/* Scrim + panel — mirrors TmuxStatusSheet's overlay idiom, anchored to the right. */
.rd-scrim {
  position: fixed;
  inset: 0;
  z-index: 300;
  display: flex;
  justify-content: flex-end;
  align-items: stretch;
}
.rd-scrim.is-mobile { background: rgba(8, 6, 14, 0.55); }
/* Desktop: a collapsible column; the scrim is click-through except the panel itself. */
.rd-scrim.is-desktop { background: transparent; pointer-events: none; }
.rd-scrim.is-desktop .rd-panel { pointer-events: auto; }

.rd-panel {
  display: flex;
  flex-direction: column;
  background: #160f22;
  border-left: 1px solid #3a2860;
  color: #e0d4f0;
  font-size: 0.78rem;
  box-shadow: -8px 0 40px rgba(0, 0, 0, 0.6);
  overflow: hidden;
  user-select: none;
  -webkit-user-select: none;
}
.is-desktop .rd-panel {
  width: 320px;
  height: 100%;
}
.is-mobile .rd-panel {
  width: 330px;
  max-width: calc(100vw - 24px);
  height: 100%;
  padding-bottom: env(safe-area-inset-bottom, 0px);
}

/* Header + tabs */
.rd-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 10px;
  background: rgba(192, 128, 255, 0.06);
  border-bottom: 1px solid #2a1f3a;
  flex-shrink: 0;
}
.rd-title {
  display: flex; align-items: center; gap: 5px;
  font-weight: 600; color: #c080ff; font-size: 0.8rem; letter-spacing: 0.4px;
  flex-shrink: 0;
}
.rd-tabs { display: flex; gap: 3px; flex: 1; justify-content: center; }
.rd-tab {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 3px 9px;
  background: #1c1430;
  border: 1px solid #2e2050;
  border-radius: 999px;
  color: #9a86ba;
  font-size: 0.7rem;
  cursor: pointer;
}
.rd-tab.is-active { background: #4a2a7a; border-color: #7a4ab0; color: #f0e0ff; }
.rd-tab-count {
  font-variant-numeric: tabular-nums; font-size: 0.6rem;
  background: rgba(0, 0, 0, 0.25); border-radius: 5px; padding: 0 4px;
}
.rd-close {
  background: none; border: none; color: #6f5a90; cursor: pointer;
  font-size: 1.3rem; line-height: 1; padding: 0 2px; flex-shrink: 0;
}
.rd-close:active { color: #ff8080; }

/* Filter bar */
.rd-filter {
  display: flex; align-items: center; gap: 6px;
  padding: 7px 10px;
  background: rgba(192, 128, 255, 0.03);
  border-bottom: 1px solid #241934;
  flex-shrink: 0;
}
.rd-select, .rd-sort, .rd-search {
  background: #1a1228; border: 1px solid #2e2050; border-radius: 6px;
  color: #c8a0e8; font-size: 0.66rem; padding: 4px 7px; min-height: 26px;
  outline: none;
}
.rd-select { flex: 0 1 auto; max-width: 38%; }
.rd-sort { cursor: pointer; flex-shrink: 0; white-space: nowrap; }
.rd-sort:active { background: #2e1c52; }
.rd-search { flex: 1; min-width: 0; }
.rd-search::placeholder { color: #5a4a78; }
.rd-search:focus, .rd-select:focus { border-color: #7a4ab0; }

/* Body */
.rd-body {
  flex: 1;
  overflow-y: auto;
  padding: 10px;
  scrollbar-width: thin;
  scrollbar-color: #3a2860 transparent;
}
.rd-pane { min-height: 100%; }
.rd-empty { color: #5a4a78; font-style: italic; font-size: 0.72rem; padding: 14px 4px; text-align: center; }

/* session chip (uploads) + source badge (inputs) */
.rd-chip {
  display: inline-block; max-width: 100%;
  padding: 0 5px; border-radius: 5px;
  background: #2a1f3a; border: 1px solid #3a2860; color: #b08fd0;
  font-size: 0.56rem; line-height: 1.5;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: middle;
}
.rd-badge {
  display: inline-flex; align-items: center;
  padding: 0 6px; height: 15px; border-radius: 5px;
  font-size: 0.56rem; font-weight: 700; letter-spacing: 0.4px; text-transform: uppercase;
}
.rd-badge.is-claude { background: rgba(192, 128, 255, 0.15); color: #c080ff; border: 1px solid #4a2a7a; }
.rd-badge.is-codex { background: rgba(96, 216, 144, 0.12); color: #60d890; border: 1px solid #1f5238; }

/* 图片 grid */
.rd-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 8px; }
.rd-thumb {
  display: flex; flex-direction: column; gap: 4px;
  padding: 0; background: #1a1228; border: 1px solid #2a1f3a; border-radius: 8px;
  overflow: hidden; cursor: pointer; text-align: left;
}
.rd-thumb:active { border-color: #7a4ab0; }
.rd-thumb-img {
  width: 100%; aspect-ratio: 1 / 1; object-fit: cover;
  background: #0d0916; display: block;
}
.rd-thumb-meta { display: flex; flex-direction: column; gap: 2px; padding: 4px 6px 6px; }
.rd-thumb-name {
  color: #c8a0e8; font-size: 0.62rem;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.rd-thumb-tags { display: flex; align-items: center; gap: 4px; min-width: 0; }
.rd-thumb-tags .rd-chip { flex: 0 1 auto; }
.rd-thumb-time { color: #6f5a90; font-size: 0.56rem; flex-shrink: 0; }

/* lists (文件 / 输入) */
.rd-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }

.rd-file {
  display: flex; align-items: center; gap: 8px;
  padding: 7px 8px; background: #1a1228; border: 1px solid #2a1f3a; border-radius: 8px;
}
.rd-file-glyph {
  flex-shrink: 0; width: 30px; height: 30px; border-radius: 6px;
  display: flex; align-items: center; justify-content: center;
  font-size: 0.52rem; font-weight: 700; letter-spacing: 0.3px;
  font-family: 'Cascadia Code', monospace;
  background: #221636; border: 1px solid #3a2860; color: #b08fd0;
}
.rd-file-glyph.is-img { color: #60d890; border-color: #1f5238; }
.rd-file-glyph.is-pdf { color: #ff8080; border-color: #5a2030; }
.rd-file-glyph.is-text { color: #80c0ff; border-color: #284a6a; }
.rd-file-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; flex: 1; }
.rd-file-name { color: #d8c4f0; font-size: 0.7rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rd-file-meta { color: #6f5a90; font-size: 0.6rem; display: flex; align-items: center; gap: 5px; min-width: 0; }
.rd-file-meta .rd-chip { max-width: 46%; }
.rd-file-actions { display: flex; gap: 4px; flex-shrink: 0; }

.rd-input {
  padding: 7px 8px; background: #1a1228; border: 1px solid #2a1f3a; border-radius: 8px;
  cursor: pointer;
}
.rd-input.is-expanded { border-color: #4a2a7a; }
.rd-input-head { display: flex; align-items: center; gap: 6px; margin-bottom: 5px; }
.rd-input-proj { color: #9a86ba; font-size: 0.58rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; }
.rd-input-metric {
  flex-shrink: 0; color: #8a76aa; font-size: 0.54rem;
  font-variant-numeric: tabular-nums; white-space: nowrap;
  padding: 0 5px; border-radius: 5px; background: rgba(192, 128, 255, 0.08);
}
.rd-input-time { color: #6f5a90; font-size: 0.56rem; flex-shrink: 0; }
.rd-input-text {
  color: #d8c4f0; font-size: 0.7rem; line-height: 1.4; white-space: pre-wrap; word-break: break-word;
  font-family: 'Cascadia Code', 'Fira Code', monospace;
}
.rd-input-text.is-clamped {
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
/* Expanded: show the FULL prompt; cap height so a huge prompt scrolls instead of
   pushing the list, keeping the drawer usable. */
.rd-input.is-expanded .rd-input-text { max-height: 46vh; overflow-y: auto; }
.rd-input-actions { display: flex; gap: 5px; margin-top: 6px; }

/* shared small action button */
.rd-act {
  display: inline-flex; align-items: center; justify-content: center;
  padding: 3px 8px; min-height: 24px;
  background: #221636; border: 1px solid #3a2860; border-radius: 6px;
  color: #b08fd0; font-size: 0.62rem; cursor: pointer; text-decoration: none;
}
.rd-act:active { background: #2e1c52; }
.rd-act--send { color: #80ffb0; border-color: #1f5238; }

/* toast */
.rd-toast {
  position: absolute; bottom: 14px; left: 50%; transform: translateX(-50%);
  background: rgba(20, 14, 32, 0.95); border: 1px solid #4a2a7a; color: #e0d4f0;
  padding: 6px 14px; border-radius: 999px; font-size: 0.68rem;
  box-shadow: 0 4px 18px rgba(0, 0, 0, 0.5); pointer-events: none;
}

/* lightbox — near-fullscreen overlay. The stage takes the whole viewport minus a
   slim padding and reserves room at the bottom for the action bar; the image fits
   ~95vw × ~88vh so detail is readable on mobile. Pinch/double-tap zoom + pan are
   unchanged (they drive the <img> transform, which scales from whatever base size
   `object-fit: contain` resolves to). */
.rd-lightbox {
  position: fixed; inset: 0; z-index: 360;
  background: rgba(6, 4, 10, 0.92);
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 10px;
  padding: max(8px, env(safe-area-inset-top, 8px)) 8px
           calc(env(safe-area-inset-bottom, 0px) + 8px);
}
/* Stage fills the viewport above the action bar and clips the zoomed/panned image
   so it never escapes the bounds. */
.rd-lightbox-stage {
  flex: 1; min-height: 0;
  width: 100%;
  max-width: min(95vw, 1400px);
  display: flex; align-items: center; justify-content: center;
  overflow: hidden; touch-action: none;
}
.rd-lightbox-img {
  max-width: 95vw; max-height: 88vh; object-fit: contain;
  border-radius: 8px; box-shadow: 0 12px 60px rgba(0, 0, 0, 0.7);
  transform-origin: center center;
  transition: transform 0.08s ease-out;
  will-change: transform;
  -webkit-user-select: none; user-select: none; -webkit-user-drag: none;
}
.rd-lightbox-img.is-zoomed { cursor: grab; transition: none; }

/* Inline text preview — sizes itself within the same flex column; capping max-height
   keeps it from pushing the action bar off-screen. */
.rd-textview-body {
  width: 100%; max-width: min(94vw, 1000px); min-height: 0; max-height: 100%; overflow: auto;
  margin: 0; padding: 14px 16px;
  background: #120c1c; border: 1px solid #3a2860; border-radius: 10px;
  color: #d8c4f0; font-size: 0.72rem; line-height: 1.5;
  white-space: pre-wrap; word-break: break-word;
  box-shadow: 0 12px 60px rgba(0, 0, 0, 0.7);
  -webkit-overflow-scrolling: touch;
}
/* Action bar (插入对话/下载/关闭) is pinned: never shrinks, always reachable below
   the near-fullscreen image / text panel. */
.rd-lightbox-bar {
  flex-shrink: 0;
  display: flex; align-items: center; gap: 10px;
  background: #160f22; border: 1px solid #3a2860; border-radius: 10px;
  padding: 8px 12px; color: #d8c4f0; font-size: 0.72rem;
  max-width: 95vw; flex-wrap: wrap; justify-content: center;
}
.rd-lightbox-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 50vw; }
.rd-lightbox-size { color: #6f5a90; }

/* transitions (match TmuxStatusSheet idiom: opacity + slide) */
.rd-fade-enter-active, .rd-fade-leave-active { transition: opacity 0.18s ease; }
.rd-fade-enter-from, .rd-fade-leave-to { opacity: 0; }
.rd-fade-enter-active .rd-panel, .rd-fade-leave-active .rd-panel { transition: transform 0.2s ease; }
.rd-fade-enter-from .rd-panel, .rd-fade-leave-to .rd-panel { transform: translateX(24px); }
</style>
