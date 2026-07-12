<script setup lang="ts">
/**
 * FilePreview — read-only, format-aware document READER for the drawer's 文件 panel.
 *
 * Modes (by extension):
 *   · markdown → rich HTML via the markdown.ts pipeline (headings→TOC, fenced code, mermaid /
 *     graphviz diagrams, KaTeX math, callouts, task lists, wiki/relative links), with the heavy
 *     bits (highlight.js, mermaid, graphviz-wasm, katex) lazy-rendered post-mount (markdownEnhance).
 *   · code     → highlight.js (lazy, curated language set).
 *   · plain    → monospace text.
 *
 * Reader chrome (the point of this component, for long structured docs on a phone):
 *   · floating TOC outline + scroll-spy (jump to / know your section)
 *   · in-document find (app-owned: highlight + count + prev/next — browser find is flaky in the
 *     drawer's overflow container)
 *   · reading-progress bar + sticky current-section header + back-to-top
 *   · per-code-block copy, image tap-to-zoom, font-size, soft-wrap toggle
 *   · in-app doc-to-doc navigation: [[wikilinks]] + relative .md links open the target in-place.
 */
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { WrapText, List, Search, X, ChevronUp, ChevronDown, ArrowUp, Minus, Plus, Sun, Moon } from 'lucide-vue-next'
import { copyTextToClipboard } from '@ce/utils/clipboard'
import { renderMarkdown } from '@terminal/utils/markdown'
import { highlightCode, addCopyButtons, renderDiagrams, renderMath, loadHljs } from '@terminal/utils/markdownEnhance'

const props = defineProps<{ name: string; text: string; path?: string }>()
const emit = defineEmits<{
  (e: 'navigate', absPath: string): void
  (e: 'toast', msg: string): void
}>()

const ext = computed(() => {
  const base = props.name.split('/').pop() || props.name
  if (base.toLowerCase() === 'dockerfile') return 'dockerfile'
  const parts = base.split('.')
  return parts.length > 1 ? (parts.pop() || '').toLowerCase() : ''
})
// extension → highlight.js language id (for the code-file view).
const EXT_LANG: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'typescript', mts: 'typescript', cts: 'typescript',
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript', vue: 'xml',
  py: 'python', rs: 'rust', rb: 'ruby', java: 'java', kt: 'kotlin', swift: 'swift',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', hpp: 'cpp', cs: 'csharp', php: 'php',
  json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', conf: 'ini', env: 'ini',
  sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
  html: 'xml', xml: 'xml', svg: 'xml', css: 'css', scss: 'css', less: 'css',
  sql: 'sql', dockerfile: 'dockerfile', diff: 'diff', patch: 'diff', lua: 'lua', proto: 'protobuf',
}
const lang = computed(() => EXT_LANG[ext.value] || '')
const kind = computed<'markdown' | 'code' | 'plain'>(() => {
  if (['md', 'markdown', 'mdx'].includes(ext.value)) return 'markdown'
  return lang.value ? 'code' : 'plain'
})

// ── markdown pipeline ──────────────────────────────────────────────────────────────
const rendered = computed(() => (kind.value === 'markdown' ? renderMarkdown(props.text) : { html: '', toc: [] }))
const mdHtml = computed(() => rendered.value.html)
const toc = computed(() => rendered.value.toc)

// ── code-file view (whole-file highlight.js, async) ──────────────────────────────────
function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
const codeHtml = ref('')
watch(
  [() => props.text, () => props.name],
  async () => {
    if (kind.value !== 'code') { codeHtml.value = ''; return }
    codeHtml.value = escapeHtml(props.text)
    const want = props.text
    try {
      const hljs = await loadHljs()
      if (props.text !== want) return
      if (lang.value && hljs.getLanguage(lang.value)) {
        codeHtml.value = hljs.highlight(props.text, { language: lang.value, ignoreIllegals: true }).value
      }
    } catch { /* keep escaped text */ }
  },
  { immediate: true },
)

// ── reader chrome state ──────────────────────────────────────────────────────────────
const scrollEl = ref<HTMLElement>()
const contentEl = ref<HTMLElement>()
const wrap = ref(true)
const fontScale = ref(1)
const FONT_MIN = 0.8, FONT_MAX = 1.6
function bumpFont(d: number): void {
  fontScale.value = Math.min(FONT_MAX, Math.max(FONT_MIN, Math.round((fontScale.value + d) * 10) / 10))
}

// ── reading theme ────────────────────────────────────────────────────────────────────
// Dark (Tokyo Night) by default; light for a bright room, where a dark surface glares as
// badly as a light one does at night. Unlike wrap/font-size, this is a DURABLE preference —
// losing it on every reopen would be its own small annoyance — so it persists per device.
type ReadTheme = 'dark' | 'light'
const THEME_KEY = 'dw.filepreview.theme'
const theme = ref<ReadTheme>(((): ReadTheme => {
  try { return localStorage.getItem(THEME_KEY) === 'light' ? 'light' : 'dark' } catch { return 'dark' }
})())
function toggleTheme(): void {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  try { localStorage.setItem(THEME_KEY, theme.value) } catch { /* private mode: the session still switches */ }
}
// Diagrams carry their own colours, so they must be re-rendered against the new surface —
// a dark mermaid SVG dropped onto a light page is exactly the jarring thing we set out to fix.
watch(theme, () => void nextTick(enhance))

const tocOpen = ref(false)
const activeId = ref('')
const activeHeadingText = computed(() => toc.value.find((t) => t.id === activeId.value)?.text || '')

const findOpen = ref(false)
const findQuery = ref('')
const matchCount = ref(0)
const matchIdx = ref(0)
let findMarks: HTMLElement[] = []

const progress = ref(0)
const showTop = ref(false)
const lightboxSrc = ref('')

function onScroll(): void {
  const el = scrollEl.value
  if (!el) return
  const max = el.scrollHeight - el.clientHeight
  progress.value = max > 0 ? el.scrollTop / max : 0
  showTop.value = el.scrollTop > 400
}
function scrollToTop(): void {
  scrollEl.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

// ── post-mount enhancers: run after the md html lands (and re-run when text changes) ──
let io: IntersectionObserver | null = null
async function enhance(): Promise<void> {
  const root = contentEl.value
  if (!root || kind.value !== 'markdown') return
  addCopyButtons(root, (t) => void copyText(t))
  wireLinks(root)
  wireImages(root)
  setupScrollSpy(root)
  if (findQuery.value.trim()) runFind()
  await Promise.allSettled([highlightCode(root), renderDiagrams(root, theme.value), renderMath(root)])
  wireDiagrams(root) // after renderDiagrams — the SVGs now exist and can be made openable
}
watch([mdHtml, () => props.name], () => {
  activeId.value = ''
  progress.value = 0
  void nextTick(enhance)
})
watch(codeHtml, () => { if (findQuery.value.trim()) void nextTick(runFind) })
void nextTick(enhance)

function setupScrollSpy(root: HTMLElement): void {
  io?.disconnect()
  const heads = Array.from(root.querySelectorAll<HTMLElement>('.dw-h'))
  if (!heads.length) return
  // A heading is "active" once its top passes into the upper quarter of the viewport.
  io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) if (e.isIntersecting) activeId.value = (e.target as HTMLElement).id
    },
    { root: scrollEl.value, rootMargin: '0px 0px -75% 0px', threshold: 0 },
  )
  heads.forEach((h) => io!.observe(h))
}
function scrollToId(id: string): void {
  const el = contentEl.value?.querySelector<HTMLElement>(`#${CSS.escape(id)}`)
  if (el) { el.scrollIntoView({ behavior: 'smooth', block: 'start' }); activeId.value = id }
  tocOpen.value = false
}

// ── in-app doc navigation (wiki / relative links) ────────────────────────────────────
function docDir(): string {
  return props.path ? props.path.replace(/\/[^/]*$/, '') : ''
}
function joinPath(dir: string, rel: string): string {
  const out: string[] = []
  for (const part of `${dir}/${rel}`.split('/')) {
    if (part === '' || part === '.') continue
    if (part === '..') out.pop()
    else out.push(part)
  }
  return '/' + out.join('/')
}
function resolveInternal(a: HTMLElement): string {
  const dir = docDir()
  if (a.dataset.wikilink) {
    const name = a.dataset.wikilink
    const withExt = /\.[a-z0-9]+$/i.test(name) ? name : `${name}.md`
    return dir ? joinPath(dir, withExt) : ''
  }
  const href = a.dataset.href || ''
  if (!href) return ''
  if (href.startsWith('/')) return href
  return dir ? joinPath(dir, href) : ''
}
function wireLinks(root: HTMLElement): void {
  root.querySelectorAll<HTMLElement>('a.dw-link').forEach((a) => {
    if (a.dataset.wired === '1') return
    a.dataset.wired = '1'
    a.addEventListener('click', (e) => {
      e.preventDefault()
      if (a.dataset.anchor) { scrollToId(a.dataset.anchor); return }
      const target = resolveInternal(a)
      if (target) emit('navigate', target)
      else emit('toast', '无法解析该链接（缺少当前文档路径）')
    })
  })
}
function wireImages(root: HTMLElement): void {
  root.querySelectorAll<HTMLImageElement>('img').forEach((img) => {
    if (img.dataset.wired === '1') return
    img.dataset.wired = '1'
    img.addEventListener('click', () => { lightboxSrc.value = img.src })
  })
}

// ── diagram viewer: zoom / pan / hover-highlight for big mermaid & graphviz SVGs ──
// Fit-width makes a large flowchart's node text unreadable; clicking a rendered diagram opens it
// here at natural size with wheel/pinch zoom + drag pan (Typora-style). Node hover-highlight is
// pure CSS (:has(), see <style>) — it dims non-hovered nodes so a dense graph stays legible.
const dvSvg = ref('') // SVG markup of the diagram being viewed; '' = closed
const dvScale = ref(1)
const dvTx = ref(0)
const dvTy = ref(0)
const dvViewport = ref<HTMLElement>()
const dvTransform = computed(() => `translate(${dvTx.value}px, ${dvTy.value}px) scale(${dvScale.value})`)
const dvZoomPct = computed(() => Math.round(dvScale.value * 100))
function openDiagram(svg: string): void {
  dvSvg.value = svg
  dvScale.value = 1; dvTx.value = 0; dvTy.value = 0
}
function dvZoomAt(factor: number, cx: number, cy: number): void {
  const next = Math.min(8, Math.max(0.2, dvScale.value * factor))
  // Keep the point under (cx,cy) fixed while zooming (zoom-to-cursor).
  dvTx.value = cx - (cx - dvTx.value) * (next / dvScale.value)
  dvTy.value = cy - (cy - dvTy.value) * (next / dvScale.value)
  dvScale.value = next
}
function dvWheel(e: WheelEvent): void {
  e.preventDefault()
  const r = dvViewport.value?.getBoundingClientRect()
  dvZoomAt(e.deltaY < 0 ? 1.15 : 1 / 1.15, e.clientX - (r?.left ?? 0), e.clientY - (r?.top ?? 0))
}
function dvBtnZoom(dir: 1 | -1): void {
  const r = dvViewport.value?.getBoundingClientRect()
  dvZoomAt(dir > 0 ? 1.25 : 1 / 1.25, (r?.width ?? 0) / 2, (r?.height ?? 0) / 2)
}
function dvReset(): void { dvScale.value = 1; dvTx.value = 0; dvTy.value = 0 }
// Pan: mouse via pointer events (touch is handled separately to keep pinch clean).
let dvPan = false, dvPanX = 0, dvPanY = 0, dvStartTx = 0, dvStartTy = 0
function dvPointerDown(e: PointerEvent): void {
  if (e.pointerType === 'touch') return
  dvPan = true; dvPanX = e.clientX; dvPanY = e.clientY; dvStartTx = dvTx.value; dvStartTy = dvTy.value
  ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
}
function dvPointerMove(e: PointerEvent): void {
  if (!dvPan) return
  dvTx.value = dvStartTx + (e.clientX - dvPanX)
  dvTy.value = dvStartTy + (e.clientY - dvPanY)
}
function dvPointerUp(): void { dvPan = false }
// Touch: two-finger pinch-zoom + one-finger pan.
let dvPinchDist = 0, dvPinchScale = 1
function dvTouchStart(e: TouchEvent): void {
  if (e.touches.length === 2) {
    dvPinchDist = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    dvPinchScale = dvScale.value
  } else if (e.touches.length === 1) {
    dvPan = true; dvPanX = e.touches[0].clientX; dvPanY = e.touches[0].clientY; dvStartTx = dvTx.value; dvStartTy = dvTy.value
  }
}
function dvTouchMove(e: TouchEvent): void {
  if (e.touches.length === 2 && dvPinchDist > 0) {
    e.preventDefault()
    const d = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    dvScale.value = Math.min(8, Math.max(0.2, (d / dvPinchDist) * dvPinchScale))
  } else if (e.touches.length === 1 && dvPan) {
    e.preventDefault()
    dvTx.value = dvStartTx + (e.touches[0].clientX - dvPanX)
    dvTy.value = dvStartTy + (e.touches[0].clientY - dvPanY)
  }
}
function dvTouchEnd(e: TouchEvent): void { if (e.touches.length < 2) dvPinchDist = 0; if (e.touches.length === 0) dvPan = false }
// wireDiagrams makes each rendered diagram clickable → open it in the zoom/pan viewer.
function wireDiagrams(root: HTMLElement): void {
  root.querySelectorAll<HTMLElement>('.dw-diagram').forEach((el) => {
    if (el.dataset.wired === '1') return
    el.dataset.wired = '1'
    el.addEventListener('click', () => {
      const svg = el.querySelector('svg')
      if (svg) openDiagram(svg.outerHTML)
    })
  })
}

// ── in-document find ─────────────────────────────────────────────────────────────────
function clearMarks(): void {
  const root = contentEl.value
  if (!root) return
  root.querySelectorAll('mark.dw-find').forEach((m) => {
    const t = document.createTextNode(m.textContent || '')
    m.parentNode?.replaceChild(t, m)
  })
  root.normalize()
  findMarks = []
}
function markMatches(root: HTMLElement, q: string): HTMLElement[] {
  const ql = q.toLowerCase()
  const targets: Text[] = []
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const p = (node as Text).parentElement
      if (!p || p.closest('.dw-diagram, .dw-math, script, style, mark.dw-find, .dw-code-copy')) {
        return NodeFilter.FILTER_REJECT
      }
      return (node.nodeValue || '').toLowerCase().includes(ql) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT
    },
  })
  let n: Node | null
  while ((n = walker.nextNode())) targets.push(n as Text)
  const marks: HTMLElement[] = []
  for (const textNode of targets) {
    const text = textNode.nodeValue || ''
    const lower = text.toLowerCase()
    const frag = document.createDocumentFragment()
    let i = 0, idx: number
    while ((idx = lower.indexOf(ql, i)) !== -1) {
      if (idx > i) frag.appendChild(document.createTextNode(text.slice(i, idx)))
      const mark = document.createElement('mark')
      mark.className = 'dw-find'
      mark.textContent = text.slice(idx, idx + q.length)
      frag.appendChild(mark)
      marks.push(mark)
      i = idx + q.length
    }
    if (i < text.length) frag.appendChild(document.createTextNode(text.slice(i)))
    textNode.parentNode?.replaceChild(frag, textNode)
  }
  return marks
}
function focusMatch(i: number): void {
  findMarks.forEach((m) => m.classList.remove('is-current'))
  const m = findMarks[i]
  if (!m) return
  m.classList.add('is-current')
  m.scrollIntoView({ behavior: 'smooth', block: 'center' })
  matchIdx.value = i + 1
}
function runFind(): void {
  clearMarks()
  matchCount.value = 0
  matchIdx.value = 0
  const q = findQuery.value.trim()
  const root = contentEl.value
  if (!q || !root) return
  findMarks = markMatches(root, q)
  matchCount.value = findMarks.length
  if (findMarks.length) focusMatch(0)
}
function stepMatch(dir: 1 | -1): void {
  if (!findMarks.length) return
  const next = (matchIdx.value - 1 + dir + findMarks.length) % findMarks.length
  focusMatch(next)
}
let findTimer: ReturnType<typeof setTimeout> | null = null
watch(findQuery, () => {
  if (findTimer) clearTimeout(findTimer)
  findTimer = setTimeout(runFind, 200)
})
function toggleFind(): void {
  findOpen.value = !findOpen.value
  if (!findOpen.value) { findQuery.value = ''; clearMarks(); matchCount.value = 0 }
  else void nextTick(() => findInputEl.value?.focus())
}
const findInputEl = ref<HTMLInputElement>()

async function copyText(text: string): Promise<void> {
  if (await copyTextToClipboard(text)) emit('toast', '已复制')
  else emit('toast', '复制失败')
}

onBeforeUnmount(() => { io?.disconnect(); if (findTimer) clearTimeout(findTimer) })
</script>

<template>
  <div
    ref="scrollEl"
    class="filepreview relative h-full overflow-auto"
    :data-fp-theme="theme"
    data-testid="file-preview"
    @scroll="onScroll"
  >
    <!-- reading-progress bar -->
    <div class="fp-progress"><span :style="{ transform: `scaleX(${progress})` }" /></div>

    <!-- sticky current-section (markdown only, once we know where we are) -->
    <div v-if="kind === 'markdown' && activeHeadingText" class="fp-crumb" data-testid="file-preview-crumb">
      <List class="size-3 shrink-0 opacity-70" />
      <span class="truncate">{{ activeHeadingText }}</span>
    </div>

    <!-- floating toolbar -->
    <div class="fp-toolbar">
      <button v-if="toc.length" class="fp-tb-btn" :class="{ 'is-on': tocOpen }" type="button" title="目录" data-testid="file-preview-toc" @click="tocOpen = !tocOpen"><List class="size-3.5" /></button>
      <button class="fp-tb-btn" :class="{ 'is-on': findOpen }" type="button" title="文档内查找" data-testid="file-preview-find" @click="toggleFind"><Search class="size-3.5" /></button>
      <button class="fp-tb-btn" type="button" title="缩小字号" @click="bumpFont(-0.1)"><Minus class="size-3.5" /></button>
      <button class="fp-tb-btn" type="button" title="放大字号" @click="bumpFont(0.1)"><Plus class="size-3.5" /></button>
      <button class="fp-tb-btn" :class="{ 'is-on': wrap }" type="button" :title="wrap ? '关闭自动换行' : '自动换行'" data-testid="file-preview-wrap" @click="wrap = !wrap"><WrapText class="size-3.5" /></button>
      <!-- Theme lives in the toolbar that already exists: no new row, no rearranged controls. -->
      <button
        class="fp-tb-btn"
        type="button"
        :title="theme === 'dark' ? '切换到亮色（白天/亮房间）' : '切换到暗色（夜间）'"
        data-testid="file-preview-theme"
        @click="toggleTheme"
      >
        <Sun v-if="theme === 'dark'" class="size-3.5" />
        <Moon v-else class="size-3.5" />
      </button>
    </div>

    <!-- find bar -->
    <div v-if="findOpen" class="fp-findbar" data-testid="file-preview-findbar">
      <Search class="size-3.5 shrink-0 opacity-60" />
      <input ref="findInputEl" v-model="findQuery" class="fp-find-input" placeholder="在文档中查找…" @keydown.enter.prevent="stepMatch(1)" @keydown.esc="toggleFind" />
      <span class="fp-find-count tabular-nums">{{ matchCount ? `${matchIdx}/${matchCount}` : (findQuery ? '0' : '') }}</span>
      <button class="fp-tb-btn" type="button" title="上一个" :disabled="!matchCount" @click="stepMatch(-1)"><ChevronUp class="size-3.5" /></button>
      <button class="fp-tb-btn" type="button" title="下一个" :disabled="!matchCount" @click="stepMatch(1)"><ChevronDown class="size-3.5" /></button>
      <button class="fp-tb-btn" type="button" title="关闭" @click="toggleFind"><X class="size-3.5" /></button>
    </div>

    <!-- TOC outline sheet -->
    <Transition name="fp-toc">
      <nav v-if="tocOpen" class="fp-toc" data-testid="file-preview-toc-sheet" @click.self="tocOpen = false">
        <div class="fp-toc-panel">
          <div class="fp-toc-head">目录</div>
          <ul class="fp-toc-list">
            <li v-for="t in toc" :key="t.id" :class="[`lv-${t.depth}`, { 'is-active': t.id === activeId }]">
              <button type="button" @click="scrollToId(t.id)">{{ t.text }}</button>
            </li>
          </ul>
        </div>
      </nav>
    </Transition>

    <!-- content -->
    <div v-if="kind === 'markdown'" ref="contentEl" class="fp-md" :class="wrap ? 'fp-md-wrap' : 'fp-md-nowrap'" :style="{ fontSize: `${0.78 * fontScale}rem` }" v-html="mdHtml" />
    <pre v-else ref="contentEl" class="fp-code hljs" :class="wrap ? 'fp-wrap' : 'fp-nowrap'" :style="{ fontSize: `${0.72 * fontScale}rem` }"><code v-html="codeHtml" /></pre>

    <!-- back-to-top -->
    <Transition name="fp-fade">
      <button v-if="showTop" class="fp-totop" type="button" title="回到顶部" data-testid="file-preview-totop" @click="scrollToTop"><ArrowUp class="size-4" /></button>
    </Transition>

    <!-- image lightbox -->
    <Transition name="fp-fade">
      <div v-if="lightboxSrc" class="fp-lightbox" data-testid="file-preview-lightbox" @click="lightboxSrc = ''">
        <img :src="lightboxSrc" alt="" />
      </div>
    </Transition>

    <!-- diagram viewer: zoom / pan / hover-highlight for big mermaid & graphviz SVGs -->
    <Transition name="fp-fade">
      <div v-if="dvSvg" class="fp-dv" data-testid="file-preview-diagram-viewer">
        <div class="fp-dv-bar">
          <button class="fp-tb-btn" type="button" title="放大" @click="dvBtnZoom(1)"><Plus class="size-4" /></button>
          <button class="fp-tb-btn" type="button" title="缩小" @click="dvBtnZoom(-1)"><Minus class="size-4" /></button>
          <button class="fp-tb-btn fp-dv-reset" type="button" title="重置" @click="dvReset">1:1</button>
          <span class="fp-dv-zoom tabular-nums">{{ dvZoomPct }}%</span>
          <button class="fp-tb-btn" type="button" title="关闭" data-testid="file-preview-diagram-close" @click="dvSvg = ''"><X class="size-4" /></button>
        </div>
        <div
          ref="dvViewport"
          class="fp-dv-vp"
          @wheel="dvWheel"
          @pointerdown="dvPointerDown"
          @pointermove="dvPointerMove"
          @pointerup="dvPointerUp"
          @pointercancel="dvPointerUp"
          @touchstart="dvTouchStart"
          @touchmove="dvTouchMove"
          @touchend="dvTouchEnd"
        >
          <div class="fp-dv-svg" :style="{ transform: dvTransform }" v-html="dvSvg" />
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.filepreview {
  background: var(--fp-bg);
  color: var(--fp-text);
  -webkit-user-select: text;
  user-select: text;
  scroll-behavior: smooth;
}

/* ── reading palette ──────────────────────────────────────────────────────────
 * ONE token set, two themes. Every colour below used to be a hex literal repeated
 * across ~30 rules, so a second theme would have meant a second copy of all of them.
 *
 * Three rules the old palette broke, and why they are what actually caused the eye
 * strain (they are structural, not matters of taste):
 *
 *   1. INLINE CODE MUST BE QUIETER THAN BODY TEXT. It was BRIGHTER (L* 89 vs 86) — and
 *      an engineering doc is mostly identifiers, so every line was speckled with the
 *      brightest thing on the page. The eye never finds a resting level. Inline code is
 *      a texture, not an emphasis: it now carries the BODY colour on a low-chroma chip.
 *   2. NO PURE WHITE ON NEAR-BLACK. Headings were #fff at 18.8:1 against the surface.
 *   3. A CODE BLOCK IS A RAISED SURFACE, NOT A HOLE. Its background was DARKER than the
 *      page (L* 1.8 vs 5.2), so scanning the page swung the eye through a black pit.
 *
 * Dark = Tokyo Night. Light = a soft off-white counterpart (never pure white, same
 * reason we never use pure black). Every syntax colour is ≥ 4.5:1 on its own surface
 * (WCAG AA), and the hue count is held to 6 + red — past ~8 a palette reads as noise.
 * ─────────────────────────────────────────────────────────────────────────────── */
.filepreview {
  /* surfaces */
  --fp-bg: #1a1b26;
  --fp-surface: #24283b;      /* code blocks / tables: RAISED above the page, never below */
  --fp-surface-soft: rgba(86, 95, 137, 0.22);
  --fp-border: #2f3549;
  --fp-border-soft: #262b3d;
  /* text */
  --fp-text: #a9b1d6;         /* 8.1:1 — comfortable for long reading, not glaring */
  --fp-heading: #c0caf5;      /* 10.6:1 — emphatic without going pure white */
  --fp-muted: #7f88b3;
  --fp-link: #7aa2f7;
  /* inline code: the SAME lightness as body text. This is the whole fix. */
  --fp-code-fg: #a9b1d6;
  --fp-code-bg: rgba(86, 95, 137, 0.22);
  /* semantic accents (callouts, find) */
  --fp-warn: #e0af68;
  --fp-ok: #9ece6a;
  --fp-danger: #f7768e;
  --fp-mark: rgba(224, 175, 104, 0.32);
  --fp-mark-on: #e0af68;
  --fp-mark-on-fg: #1a1b26;
  /* syntax — 6 hues + red, each ≥4.5:1 on --fp-surface */
  --sx-comment: #868fb9;
  --sx-keyword: #bb9af7;
  --sx-string: #9ece6a;
  --sx-number: #ff9e64;
  --sx-func: #7aa2f7;
  --sx-type: #2ac3de;
  --sx-danger: #f7768e;
}
.filepreview[data-fp-theme='light'] {
  --fp-bg: #f7f8fa;           /* off-white: pure white in a dark room glares as badly as pure black */
  --fp-surface: #eceef2;
  --fp-surface-soft: rgba(70, 80, 120, 0.09);
  --fp-border: #dde0e7;
  --fp-border-soft: #e6e8ee;
  --fp-text: #3d4256;         /* 9.4:1 */
  --fp-heading: #1c2030;      /* 15.2:1 */
  --fp-muted: #6f778c;
  --fp-link: #2e5cd6;
  --fp-code-fg: #3d4256;
  --fp-code-bg: rgba(70, 80, 120, 0.09);
  --fp-warn: #9a5000;
  --fp-ok: #4d6b2f;
  --fp-danger: #c02c4a;
  --fp-mark: rgba(224, 175, 104, 0.45);
  --fp-mark-on: #e0af68;
  --fp-mark-on-fg: #1c2030;
  --sx-comment: #5f6788;
  --sx-keyword: #7440b8;
  --sx-string: #4d6b2f;
  --sx-number: #9a5000;
  --sx-func: #1b5fc0;
  --sx-type: #00647f;
  --sx-danger: #c02c4a;
}
.filepreview :deep(.fp-md),
.filepreview :deep(.fp-code),
.filepreview :deep(.fp-md *),
.filepreview :deep(.fp-code *) {
  -webkit-user-select: text;
  user-select: text;
}

/* ── reading-progress bar (pinned to the scroll viewport top) ── */
.fp-progress {
  position: sticky; top: 0; left: 0; height: 2px; z-index: 6;
  background: var(--fp-surface-soft); margin-bottom: -2px;
}
.fp-progress span {
  display: block; height: 100%; transform-origin: left; transform: scaleX(0);
  background: var(--fp-link); transition: transform 0.1s linear;
}

/* ── sticky current-section crumb ── */
.fp-crumb {
  position: sticky; top: 2px; z-index: 5;
  display: flex; align-items: center; gap: 6px;
  padding: 4px 10px; font-size: 0.62rem; color: var(--fp-muted);
  background: var(--fp-bg); backdrop-filter: blur(6px);
  border-bottom: 1px solid var(--fp-border-soft);
}

/* ── floating toolbar ── */
.fp-toolbar {
  position: absolute; top: 6px; right: 8px; z-index: 7;
  display: flex; gap: 3px; padding: 2px;
  border-radius: 8px; background: var(--fp-surface); border: 1px solid var(--fp-border);
}
.fp-tb-btn {
  display: inline-flex; align-items: center; justify-content: center;
  padding: 4px; border-radius: 6px; color: var(--fp-muted);
}
.fp-tb-btn:hover { color: var(--fp-heading); background: var(--fp-surface-soft); }
.fp-tb-btn.is-on { color: var(--fp-link); background: var(--fp-surface-soft); }
.fp-tb-btn:disabled { opacity: 0.35; }

/* ── find bar ── */
.fp-findbar {
  position: sticky; top: 2px; z-index: 6;
  display: flex; align-items: center; gap: 4px;
  padding: 5px 8px; margin: 30px 8px 0;
  border-radius: 9px; background: var(--fp-surface); border: 1px solid var(--fp-border);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.5);
}
.fp-find-input {
  flex: 1; min-width: 0; background: transparent; border: none; outline: none;
  color: var(--fp-text); font-size: 0.72rem;
}
.fp-find-count { font-size: 0.62rem; color: var(--fp-muted); padding: 0 4px; white-space: nowrap; }

/* ── TOC sheet (slides in from the right, over the doc) ── */
.fp-toc { position: absolute; inset: 0; z-index: 8; display: flex; justify-content: flex-end; background: rgba(6, 4, 12, 0.45); }
.fp-toc-panel {
  width: min(78%, 300px); height: 100%; overflow-y: auto;
  background: var(--fp-surface); border-left: 1px solid var(--fp-border); padding: 8px 0;
}
.fp-toc-head { padding: 4px 14px 8px; font-size: 0.66rem; color: var(--fp-muted); border-bottom: 1px solid var(--fp-border-soft); margin-bottom: 4px; }
.fp-toc-list { list-style: none; margin: 0; padding: 0; }
.fp-toc-list li button {
  display: block; width: 100%; text-align: left; padding: 4px 12px;
  font-size: 0.68rem; color: #cfc8e6; line-height: 1.35;
}
.fp-toc-list li button:hover { background: var(--fp-surface-soft); color: var(--fp-heading); }
.fp-toc-list li.is-active button { color: var(--fp-link); border-left: 2px solid var(--fp-link); padding-left: 10px; background: var(--fp-surface-soft); }
.fp-toc-list li.lv-1 button { padding-left: 12px; font-weight: 600; }
.fp-toc-list li.lv-2 button { padding-left: 22px; }
.fp-toc-list li.lv-3 button { padding-left: 32px; }
.fp-toc-list li.lv-4 button, .fp-toc-list li.lv-5 button, .fp-toc-list li.lv-6 button { padding-left: 42px; color: var(--fp-muted); }
.fp-toc-enter-active, .fp-toc-leave-active { transition: opacity 0.16s ease; }
.fp-toc-enter-active .fp-toc-panel, .fp-toc-leave-active .fp-toc-panel { transition: transform 0.18s ease; }
.fp-toc-enter-from, .fp-toc-leave-to { opacity: 0; }
.fp-toc-enter-from .fp-toc-panel, .fp-toc-leave-to .fp-toc-panel { transform: translateX(100%); }

/* ── back-to-top ── */
.fp-totop {
  position: fixed; bottom: 74px; right: 18px; z-index: 7;
  display: inline-flex; align-items: center; justify-content: center;
  width: 34px; height: 34px; border-radius: 999px;
  color: #e6e1f0; background: rgba(30, 20, 48, 0.92); border: 1px solid #4a3f70;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
}

/* ── image lightbox ── */
.fp-lightbox {
  position: fixed; inset: 0; z-index: 40; display: flex; align-items: center; justify-content: center;
  background: rgba(4, 3, 8, 0.92); padding: 16px;
}
.fp-lightbox img { max-width: 100%; max-height: 100%; object-fit: contain; border-radius: 6px; }

.fp-fade-enter-active, .fp-fade-leave-active { transition: opacity 0.16s ease; }
.fp-fade-enter-from, .fp-fade-leave-to { opacity: 0; }

/* ── code (whole-file view) ── */
.fp-code {
  margin: 0; padding: 34px 12px 28px;
  font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', ui-monospace, monospace;
  line-height: 1.6; color: var(--fp-text); background: transparent; tab-size: 4;
}
.fp-wrap { white-space: pre-wrap; word-break: break-word; overflow-wrap: anywhere; }
.fp-nowrap { white-space: pre; overflow-x: auto; }
/* Syntax: 6 hues + red. The old set ran to 10 and mixed pink/orange/green/purple/yellow/cyan
   at full chroma — past ~8 hues a palette stops carrying meaning and just adds noise. */
.filepreview :deep(.hljs-comment), .filepreview :deep(.hljs-quote) { color: var(--sx-comment); font-style: italic; }
.filepreview :deep(.hljs-keyword), .filepreview :deep(.hljs-selector-tag), .filepreview :deep(.hljs-doctag) { color: var(--sx-keyword); }
.filepreview :deep(.hljs-literal), .filepreview :deep(.hljs-number), .filepreview :deep(.hljs-symbol), .filepreview :deep(.hljs-bullet) { color: var(--sx-number); }
.filepreview :deep(.hljs-string), .filepreview :deep(.hljs-regexp), .filepreview :deep(.hljs-addition) { color: var(--sx-string); }
.filepreview :deep(.hljs-title), .filepreview :deep(.hljs-title.function_), .filepreview :deep(.hljs-section) { color: var(--sx-func); }
.filepreview :deep(.hljs-built_in), .filepreview :deep(.hljs-type), .filepreview :deep(.hljs-class .hljs-title) { color: var(--sx-type); }
.filepreview :deep(.hljs-name), .filepreview :deep(.hljs-tag) { color: var(--sx-type); }
.filepreview :deep(.hljs-attr), .filepreview :deep(.hljs-attribute), .filepreview :deep(.hljs-variable), .filepreview :deep(.hljs-template-variable) { color: var(--sx-func); }
.filepreview :deep(.hljs-meta) { color: var(--sx-comment); }
.filepreview :deep(.hljs-deletion) { color: var(--sx-danger); }
.filepreview :deep(.hljs-emphasis) { font-style: italic; }
.filepreview :deep(.hljs-strong) { font-weight: 700; }

/* ── markdown prose ── */
.fp-md {
  padding: 34px 16px 40px;
  line-height: 1.7; color: var(--fp-text); word-break: break-word; overflow-wrap: anywhere;
}
.fp-md :deep(.dw-h) { color: var(--fp-heading); font-weight: 700; line-height: 1.3; margin: 1em 0 0.45em; scroll-margin-top: 40px; }
.fp-md :deep(h1.dw-h) { font-size: 1.5em; border-bottom: 1px solid var(--fp-border); padding-bottom: 0.25em; }
.fp-md :deep(h2.dw-h) { font-size: 1.35em; border-bottom: 1px solid var(--fp-border-soft); padding-bottom: 0.2em; }
.fp-md :deep(h3.dw-h) { font-size: 1.2em; }
.fp-md :deep(h4.dw-h), .fp-md :deep(h5.dw-h), .fp-md :deep(h6.dw-h) { color: var(--fp-heading); font-weight: 600; margin: 0.85em 0 0.3em; font-size: 1.05em; opacity: 0.92; }
.fp-md :deep(p) { margin: 0.55em 0; }
.fp-md :deep(a) { color: var(--fp-link); text-decoration: none; }
.fp-md :deep(a:hover) { text-decoration: underline; }
.fp-md :deep(a.dw-link) { color: var(--fp-link); cursor: pointer; border-bottom: 1px dashed currentColor; }
.fp-md :deep(ul), .fp-md :deep(ol) { margin: 0.55em 0; padding-left: 1.5em; }
.fp-md :deep(li) { margin: 0.25em 0; }
.fp-md :deep(li::marker) { color: var(--fp-muted); }
.fp-md :deep(li.task-list-item) { list-style: none; margin-left: -1.2em; }
.fp-md :deep(input[type='checkbox']) { margin-right: 0.5em; accent-color: var(--fp-link); }
.fp-md :deep(strong) { color: var(--fp-heading); font-weight: 700; }
.fp-md :deep(em) { font-style: italic; }
/* Inline code carries the BODY colour on a low-chroma chip. It is a texture, not an
   emphasis — an engineering doc is mostly identifiers, and making them the brightest
   thing on the page is what made this view painful to read. */
.fp-md :deep(code) {
  font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', ui-monospace, monospace;
  font-size: 0.9em; background: var(--fp-code-bg); color: var(--fp-code-fg); padding: 0.1em 0.4em; border-radius: 5px;
}
/* A code block is a RAISED surface. It used to be darker than the page — a pit the eye
   fell into on every scan. */
.fp-md :deep(pre.dw-pre) {
  position: relative;
  background: var(--fp-surface); border: 1px solid var(--fp-border); border-radius: 8px;
  padding: 11px 13px; overflow-x: auto; margin: 0.7em 0;
}
.fp-md :deep(pre.dw-pre code) { background: none; padding: 0; color: var(--fp-text); font-size: 0.86em; line-height: 1.55; }
.fp-md-wrap :deep(pre.dw-pre) { white-space: pre-wrap; word-break: break-word; overflow-wrap: anywhere; overflow-x: visible; }
.fp-md-nowrap :deep(pre.dw-pre) { white-space: pre; overflow-x: auto; }
/* per-code-block copy button (injected post-mount) */
.fp-md :deep(.dw-code-copy) {
  position: absolute; top: 5px; right: 6px;
  padding: 2px 8px; font-size: 0.6rem; border-radius: 6px;
  color: var(--fp-muted); background: var(--fp-bg); border: 1px solid var(--fp-border);
  opacity: 0; transition: opacity 0.12s ease;
}
.fp-md :deep(pre.dw-pre:hover .dw-code-copy) { opacity: 1; }
/* diagrams (mermaid / graphviz) */
.fp-md :deep(.dw-diagram) { margin: 0.9em 0; text-align: center; overflow-x: auto; }
.fp-md :deep(.dw-diagram svg) { max-width: 100%; height: auto; }
.fp-md :deep(.dw-diagram-src) { display: none; }
.fp-md :deep(.dw-diagram-err) { color: var(--fp-danger); font-size: 0.72rem; text-align: left; margin-bottom: 6px; }
/* math */
.fp-md :deep(.dw-math[data-display='1']) { display: block; overflow-x: auto; margin: 0.7em 0; text-align: center; }
/* callouts / admonitions */
.fp-md :deep(.dw-callout) { margin: 0.8em 0; padding: 0.5em 0.9em; border-radius: 8px; border-left: 3px solid var(--fp-border); background: var(--fp-surface-soft); }
.fp-md :deep(.dw-callout-title) { font-size: 0.66rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; color: var(--fp-muted); margin-bottom: 0.2em; }
.fp-md :deep(.dw-callout-warning), .fp-md :deep(.dw-callout-caution) { border-left-color: var(--fp-warn); }
.fp-md :deep(.dw-callout-warning .dw-callout-title), .fp-md :deep(.dw-callout-caution .dw-callout-title) { color: var(--fp-warn); }
.fp-md :deep(.dw-callout-important), .fp-md :deep(.dw-callout-tip) { border-left-color: var(--fp-ok); }
.fp-md :deep(.dw-callout-important .dw-callout-title), .fp-md :deep(.dw-callout-tip .dw-callout-title) { color: var(--fp-ok); }
.fp-md :deep(blockquote) { border-left: 3px solid var(--fp-border); margin: 0.7em 0; padding: 0.15em 0 0.15em 0.9em; color: var(--fp-muted); }
.fp-md :deep(table) { border-collapse: collapse; margin: 0.7em 0; font-size: 0.9em; display: block; overflow-x: auto; }
.fp-md :deep(th), .fp-md :deep(td) { border: 1px solid var(--fp-border); padding: 5px 9px; text-align: left; }
.fp-md :deep(th) { background: var(--fp-surface); color: var(--fp-heading); font-weight: 600; }
.fp-md :deep(hr) { border: none; border-top: 1px solid var(--fp-border); margin: 1.1em 0; }
.fp-md :deep(img) { max-width: 100%; border-radius: 6px; cursor: zoom-in; }
/* in-document find highlight */
.fp-md :deep(mark.dw-find), .fp-code :deep(mark.dw-find) { background: var(--fp-mark); color: inherit; border-radius: 2px; }
.fp-md :deep(mark.dw-find.is-current), .fp-code :deep(mark.dw-find.is-current) { background: var(--fp-mark-on); color: var(--fp-mark-on-fg); }

/* ── diagram: clickable → opens the zoom/pan viewer; a hover hint says so ── */
.fp-md :deep(.dw-diagram.dw-diagram-done) { cursor: zoom-in; position: relative; }
.fp-md :deep(.dw-diagram.dw-diagram-done)::after {
  content: '🔍 点击放大'; position: absolute; top: 6px; right: 8px;
  font-size: 0.56rem; color: #b6aee0; background: rgba(20, 14, 32, 0.85); border: 1px solid #3a2860;
  padding: 1px 6px; border-radius: 6px; opacity: 0; transition: opacity 0.12s ease; pointer-events: none;
}
.fp-md :deep(.dw-diagram.dw-diagram-done:hover)::after { opacity: 1; }
/* inline node hover-highlight (Typora-style): dim non-hovered nodes. mermaid AND graphviz
   both wrap each node in <g class="node">; :has() (Chromium) does the dimming with zero JS. */
.fp-md :deep(.dw-diagram g.node) { transition: opacity 0.12s ease; }
.fp-md :deep(.dw-diagram svg:has(g.node:hover) g.node:not(:hover)) { opacity: 0.28; }

/* ── diagram viewer (fullscreen zoom / pan) ── */
.fp-dv { position: fixed; inset: 0; z-index: 45; display: flex; flex-direction: column; background: var(--fp-bg); }
.fp-dv-bar {
  position: absolute; top: 8px; right: 10px; z-index: 2;
  display: flex; align-items: center; gap: 4px; padding: 3px;
  border-radius: 9px; background: rgba(20, 14, 32, 0.92); border: 1px solid #3a2860;
}
.fp-dv-reset { font-size: 0.56rem; font-weight: 600; }
.fp-dv-zoom { font-size: 0.62rem; color: var(--fp-muted); padding: 0 4px; min-width: 3.5ch; text-align: center; }
.fp-dv-vp { flex: 1; overflow: hidden; touch-action: none; cursor: grab; display: flex; align-items: center; justify-content: center; }
.fp-dv-vp:active { cursor: grabbing; }
.fp-dv-svg { transform-origin: center center; will-change: transform; }
.fp-dv-svg :deep(svg) { max-width: none; max-height: none; }
.fp-dv-svg :deep(g.node) { transition: opacity 0.12s ease; }
.fp-dv-svg :deep(svg:has(g.node:hover) g.node:not(:hover)) { opacity: 0.28; }
</style>
