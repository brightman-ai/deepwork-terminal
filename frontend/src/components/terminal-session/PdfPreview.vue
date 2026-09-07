<script setup lang="ts">
/**
 * PdfPreview — pdf 的客户端渲染（pdfjs-dist 懒加载）。
 *
 * 形态（2026-09-07 拍定）：连续垂直滚页 + 按容器宽 fit（手机 390px 直接可读）；
 * 页级懒渲染（IntersectionObserver，接近视口才画，远离即卸下保内存）；
 * 缩放 50–300%（按钮 + PC ctrl+滚轮 + 手机双指 pinch，作用于页宽重排而非 CSS 拉伸）。
 * pdfjs 会【转移/detach】传入的 ArrayBuffer —— 先拷贝，别吃掉 prop。
 */
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'

const props = defineProps<{
  name: string
  data: ArrayBuffer
}>()

const scrollEl = ref<HTMLElement>()
const pagesEl = ref<HTMLElement>()
const rendering = ref(true)
const errorText = ref('')
const pageCount = ref(0)
const pageNum = ref(0)
const scalePct = ref(100)
const S_MIN = 50, S_MAX = 300
/** 第一页是横版（宽>高）——幻灯片就是这个形态，文档型 pdf 不是。 */
const landscape = ref(false)
/**
 * 页宽上限**按内容形态定**，不是一个拍脑袋的常数。
 *
 * 原来写死 `max-width: 900px`：抽屉拖到 2300px 宽，幻灯片还钉在 ~780px 居中，两边全是空的
 * （2026-09-08 用户实报截图）。但直接把上限拿掉又会伤到文档型 pdf —— A4 竖版铺满 2300px 是一行
 * 一百多字的阅读灾难。所以：**横版（幻灯片）铺满容器，竖版保 900px 阅读栏**。
 * 缩放百分比本来就是相对这个 fit 宽度算的，所以 100% = 适应宽度，不需要再多一个模式开关。
 */
const pagesMaxWidth = computed(() => (landscape.value ? 'none' : '900px'))

type PageSlot = { el: HTMLElement; rendered: boolean; height: number }
let slots: PageSlot[] = []
let doc: { numPages: number; getPage: (n: number) => Promise<PdfPage> } | null = null
let io: IntersectionObserver | null = null
let renderSeq = 0

interface PdfPage {
  getViewport(o: { scale: number }): { width: number; height: number }
  render(o: { canvasContext: CanvasRenderingContext2D; viewport: unknown }): { promise: Promise<void> }
}

async function load(): Promise<void> {
  const container = pagesEl.value
  if (!container) return
  rendering.value = true
  errorText.value = ''
  container.innerHTML = ''
  slots = []
  const seq = ++renderSeq
  try {
    const pdfjs = await import('pdfjs-dist')
    const workerUrl = (await import('pdfjs-dist/build/pdf.worker.min.mjs?url')).default
    pdfjs.GlobalWorkerOptions.workerSrc = workerUrl
    // 拷贝再交出去：getDocument 会 transfer/detach 传入的 buffer，prop 保住以便重开
    const bytes = new Uint8Array(props.data.slice(0))
    const loaded = await pdfjs.getDocument({ data: bytes }).promise
    if (seq !== renderSeq) return
    doc = loaded as typeof doc
    pageCount.value = loaded.numPages
    pageNum.value = 1
    // 形态取第一页：同一份 pdf 混排横竖页极少见，而它决定的是整篇的版心宽度。
    const v1 = (await doc.getPage(1)).getViewport({ scale: 1 })
    if (seq !== renderSeq) return
    landscape.value = v1.width > v1.height
    for (let i = 1; i <= loaded.numPages; i++) {
      const el = document.createElement('div')
      el.className = 'pdf-page'
      el.dataset.page = String(i)
      container.appendChild(el)
      slots.push({ el, rendered: false, height: 0 })
    }
    setupObserver()
    await nextTick()
  } catch (err) {
    if (seq === renderSeq) errorText.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (seq === renderSeq) rendering.value = false
  }
}

// 页级懒渲染：进前哨（rootMargin 提前 1.5 屏）就画；远离（>2.5 屏）就卸下换成等高占位，
// 长文档内存有界。当前页码不在这里猜（entries 顺序无保证、多页共现时后写者胜出会跳）——
// 由 onScrollThrottled 按视口中心所在页算。
function setupObserver(): void {
  io?.disconnect()
  io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const idx = slots.findIndex((s) => s.el === e.target)
        if (idx < 0) continue
        if (e.isIntersecting) void renderPage(idx)
        else if (e.intersectionRatio === 0) unrenderPage(idx)
      }
    },
    { root: scrollEl.value, rootMargin: '150% 0px', threshold: [0] },
  )
  slots.forEach((s) => io!.observe(s.el))
}

// 当前页 = 覆盖滚动视口中心的那一页（节流；滚动惯性结束的 trailing 也会触发）
let scrollTimer: ReturnType<typeof setTimeout> | null = null
function onScrollThrottled(): void {
  if (scrollTimer) return
  scrollTimer = setTimeout(() => {
    scrollTimer = null
    const root = scrollEl.value
    if (!root || !slots.length) return
    const mid = root.scrollTop + root.clientHeight / 2
    for (let i = 0; i < slots.length; i++) {
      const top = slots[i].el.offsetTop
      const bottom = i + 1 < slots.length ? slots[i + 1].el.offsetTop : top + slots[i].el.offsetHeight
      if (mid >= top && mid < bottom) { pageNum.value = i + 1; return }
    }
  }, 120)
}

async function renderPage(idx: number): Promise<void> {
  const slot = slots[idx]
  if (!slot || slot.rendered || !doc || !pagesEl.value) return
  slot.rendered = true
  try {
    const page = await doc.getPage(idx + 1)
    const vp1 = page.getViewport({ scale: 1 })
    const avail = (pagesEl.value.clientWidth || 300) - 24 // 容器宽 − 页卡 padding/边距
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5)
    // 渲染分辨率按 dpr × 缩放比画，CSS 宽独立 —— 放大到 300% 仍是原生清晰度
    const cssW = Math.max(120, avail * (scalePct.value / 100))
    const vp = page.getViewport({ scale: (cssW / vp1.width) * dpr })
    const canvas = document.createElement('canvas')
    canvas.width = Math.floor(vp.width)
    canvas.height = Math.floor(vp.height)
    canvas.style.width = '100%'
    canvas.style.display = 'block'
    await page.render({ canvasContext: canvas.getContext('2d')!, viewport: vp }).promise
    slot.el.replaceChildren(canvas)
    slot.height = slot.el.offsetHeight
  } catch {
    slot.rendered = false
  }
}

function unrenderPage(idx: number): void {
  const slot = slots[idx]
  if (!slot || !slot.rendered) return
  if (slot.height > 0) slot.el.style.height = `${slot.height}px` // 占位保滚动位置稳定
  slot.el.replaceChildren()
  slot.rendered = false
}

// 已渲染页全部标脏重画（分辨率跟随），占位页由 IO 兜
function rerenderVisible(): void {
  slots.forEach((s) => { s.rendered = false; s.el.style.height = '' })
  slots.forEach((s, i) => {
    const r = s.el.getBoundingClientRect()
    if (r.top < innerHeight * 2.5 && r.bottom > -innerHeight * 2.5) void renderPage(i)
  })
}
// 缩放变化
watch(scalePct, rerenderVisible)

/**
 * 容器宽度变了（抽屉被拖宽/拖窄、双栏↔浮层、窗口 resize）就按新宽重画。
 *
 * canvas 的 CSS 宽是 100%，所以拖宽时视觉上会跟着拉伸 —— 但像素仍是按旧宽画的，拉大就糊。
 * **只认宽的变化**：渲染本身会改高，跟着高走会自激成死循环。
 */
let ro: ResizeObserver | null = null
let roTimer: ReturnType<typeof setTimeout> | null = null
let lastW = 0
function watchWidth(): void {
  const el = pagesEl.value
  if (!el || typeof ResizeObserver === 'undefined') return
  lastW = el.clientWidth
  ro = new ResizeObserver(() => {
    const w = el.clientWidth
    if (Math.abs(w - lastW) < 16) return // 拖动中的微抖不值得重画一遍
    lastW = w
    if (roTimer) clearTimeout(roTimer)
    roTimer = setTimeout(() => { roTimer = null; rerenderVisible() }, 120)
  })
  ro.observe(el)
}

function nudgeScale(d: 1 | -1): void { scalePct.value = Math.min(S_MAX, Math.max(S_MIN, scalePct.value + d * 25)) }
function onWheel(e: WheelEvent): void {
  if (!e.ctrlKey) return // 普通滚轮=翻页滚动；ctrl+滚轮=缩放（浏览器/触控板惯例）
  e.preventDefault()
  nudgeScale(e.deltaY < 0 ? 1 : -1)
}
let pPinch = 0
function onTouchStart(e: TouchEvent): void {
  if (e.touches.length === 2) pPinch = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
}
function onTouchMove(e: TouchEvent): void {
  if (e.touches.length === 2 && pPinch > 0) {
    e.preventDefault()
    const d = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    if (Math.abs(d - pPinch) > 18) { // 25% 一档，避免细碎跳变
      nudgeScale(d > pPinch ? 1 : -1)
      pPinch = d
    }
  }
}
function onTouchEnd(e: TouchEvent): void { if (e.touches.length < 2) pPinch = 0 }
function goPage(delta: 1 | -1): void {
  const target = Math.min(pageCount.value, Math.max(1, pageNum.value + delta))
  slots[target - 1]?.el.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const pctLabel = computed(() => `${scalePct.value}%`)
onMounted(() => { void load(); watchWidth() })
watch(() => props.data, () => void load())
onBeforeUnmount(() => {
  io?.disconnect(); io = null; renderSeq++; doc = null
  ro?.disconnect(); ro = null
  if (scrollTimer) { clearTimeout(scrollTimer); scrollTimer = null }
  if (roTimer) { clearTimeout(roTimer); roTimer = null }
})
</script>

<template>
  <div class="relative h-full overflow-hidden bg-[#14101c]" data-testid="fp-preview-pdf" @wheel="onWheel" @touchstart="onTouchStart" @touchmove="onTouchMove" @touchend="onTouchEnd">
    <div v-if="rendering && !errorText" class="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground animate-pulse">pdf 解析中…</div>
    <div v-else-if="errorText" class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <p class="text-xs text-muted-foreground">pdf 渲染失败：文件可能已损坏或不是真正的 pdf 内容</p>
      <p class="max-w-full text-[0.62rem] text-muted-foreground/70 break-all">{{ errorText }}</p>
      <p class="text-[0.62rem] text-muted-foreground/70">可用上方「下载」在本地应用中打开</p>
    </div>
    <div v-show="!errorText" ref="scrollEl" class="h-full overflow-auto" @scroll="onScrollThrottled">
      <div ref="pagesEl" class="pdf-pages flex flex-col items-center gap-3 py-3" :style="{ maxWidth: pagesMaxWidth }"></div>
    </div>
    <!-- 页码 + 缩放条 -->
    <div v-if="pageCount && !errorText" class="pdf-bar" data-testid="fp-pdf-bar">
      <button type="button" title="上一页" data-testid="fp-pdf-prev" @click="goPage(-1)">‹</button>
      <span class="pdf-bar-num" :title="`共 ${pageCount} 页`">{{ pageNum || 1 }} / {{ pageCount }}</span>
      <button type="button" title="下一页" data-testid="fp-pdf-next" @click="goPage(1)">›</button>
      <span class="pdf-bar-sep"></span>
      <button type="button" title="缩小（ctrl+滚轮 / 双指）" data-testid="fp-pdf-out" @click="nudgeScale(-1)">−</button>
      <button type="button" class="pdf-bar-num" title="重置 100%（适应宽度）" data-testid="fp-pdf-pct" @click="scalePct = 100">{{ pctLabel }}</button>
      <button type="button" title="放大（ctrl+滚轮 / 双指）" data-testid="fp-pdf-in" @click="nudgeScale(1)">＋</button>
    </div>
  </div>
</template>

<style scoped>
/* 版心宽由 pagesMaxWidth 绑定（横版铺满 / 竖版 900px 阅读栏）—— 这里只留居中。 */
.pdf-pages { margin: 0 auto; }
/* 抽屉面板 user-select:none 的例外：失败原因等文字可选中复制（pdf 正文是 canvas 无可选文本） */
[data-testid='fp-preview-pdf'] {
  user-select: text;
  -webkit-user-select: text;
}
.pdf-page {
  width: 100%;
  background: #fbfaf8;
  border-radius: 6px;
  box-shadow: 0 1px 6px rgba(0, 0, 0, 0.35);
  overflow: hidden;
  min-height: 60px; /* 未渲染占位的最小高度，避免滚动条跳动 */
}
.pdf-bar {
  position: absolute; /* 悬浮在滚动容器上（外层 relative、不滚动） */
  left: 50%;
  transform: translateX(-50%);
  bottom: max(10px, env(safe-area-inset-bottom));
  margin: 0 auto;
  width: fit-content;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 5px 8px;
  border-radius: 999px;
  background: rgba(20, 14, 32, 0.92);
  border: 1px solid rgba(255, 255, 255, 0.14);
}
.pdf-bar button {
  min-width: 34px;
  height: 34px;
  padding: 0 10px;
  border-radius: 8px;
  color: #eee;
  background: rgba(255, 255, 255, 0.08);
  font-size: 15px;
  line-height: 1;
}
.pdf-bar button:active { background: rgba(255, 255, 255, 0.2); }
.pdf-bar-num { font-size: 12px; color: #ddd; font-variant-numeric: tabular-nums; }
.pdf-bar-sep { width: 1px; height: 18px; background: rgba(255, 255, 255, 0.18); margin: 0 3px; }
</style>
