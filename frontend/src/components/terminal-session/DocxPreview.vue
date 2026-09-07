<script setup lang="ts">
/**
 * DocxPreview — 讲解 docx 的客户端 reflow 渲染（docx-preview 库）。
 *
 * 形态决策（brainstorm 2026-09-06 拍定）：全端单一 reflow —— ignoreWidth/ignoreHeight 丢弃
 * Word 固定页宽，内容按预览容器宽重排（手机 ~390px 直接可读）；breakPages=false 连续流，
 * 不留分页空白。要看版式保真（分页/页宽）走「下载」——那是既有出路。
 * 失败路径：损坏 zip / 非 docx 内容 → renderAsync 抛错 → 内联原因；预览头部的「下载」按钮
 * 始终是逃生口。库（~160KB）仅在真打开 docx 时动态 import。
 */
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'

const props = defineProps<{
  name: string
  data: ArrayBuffer
}>()

const bodyEl = ref<HTMLElement>()
const styleEl = ref<HTMLElement>()
const rendering = ref(true)
const errorText = ref('')

async function render(): Promise<void> {
  const body = bodyEl.value
  if (!body) return
  rendering.value = true
  errorText.value = ''
  body.innerHTML = ''
  try {
    const { renderAsync } = await import('docx-preview')
    await renderAsync(props.data, body, styleEl.value, {
      ignoreWidth: true, // reflow：丢 Word 固定页宽，按容器宽重排
      ignoreHeight: true,
      ignoreFonts: false,
      breakPages: false, // 连续流，无分页空白
      experimental: false,
    })
    if (!body.childElementCount) {
      // 渲染"成功"却一个节点都没落（空/怪结构）——按失败讲，不给空白页。
      errorText.value = '文档没有可渲染的内容'
    } else {
      wireImages(body)
    }
  } catch (err) {
    errorText.value = err instanceof Error ? err.message : String(err)
  } finally {
    rendering.value = false
  }
}

// ── 图片缩放查看器（与 md 阅读器同级体验：点开全屏，滚轮/双指缩放，拖拽平移）─────────────
// 讲解 docx 的插图常是流程图/截图——reflow 压到容器宽后细节看不清，点开必须能放大细看。
// PC：滚轮对光标缩放 + 拖拽 + 双击切换 + ESC/✕；手机：双指 pinch + 单指拖 + 双击切换。
const zoomSrc = ref('')
const zoomScale = ref(1)
const zoomTx = ref(0)
const zoomTy = ref(0)
const Z_MIN = 0.2, Z_MAX = 8, Z_TOGGLE = 2.5
const zoomTransform = computed(() => `translate(${zoomTx.value}px, ${zoomTy.value}px) scale(${zoomScale.value})`)
const zoomPct = computed(() => Math.round(zoomScale.value * 100))

function wireImages(root: HTMLElement): void {
  root.querySelectorAll<HTMLImageElement>('img').forEach((img) => {
    if (img.dataset.zoomWired === '1') return
    img.dataset.zoomWired = '1'
    img.addEventListener('click', () => openZoom(img.src))
    img.title = '点击放大'
  })
}
function openZoom(src: string): void {
  zoomSrc.value = src
  zoomScale.value = 1; zoomTx.value = 0; zoomTy.value = 0
}
function closeZoom(): void { zoomSrc.value = '' }
function zoomAt(factor: number, cx: number, cy: number): void {
  const next = Math.min(Z_MAX, Math.max(Z_MIN, zoomScale.value * factor))
  // 缩放锚定在 (cx,cy)：光标/双指中点下的点在缩放前后不动
  zoomTx.value = cx - (cx - zoomTx.value) * (next / zoomScale.value)
  zoomTy.value = cy - (cy - zoomTy.value) * (next / zoomScale.value)
  zoomScale.value = next
}
function zoomStep(dir: 1 | -1): void { zoomAt(dir > 0 ? 1.25 : 1 / 1.25, window.innerWidth / 2, window.innerHeight / 2) }
function onZoomWheel(e: WheelEvent): void {
  e.preventDefault()
  zoomAt(e.deltaY < 0 ? 1.15 : 1 / 1.15, e.clientX, e.clientY)
}
function onZoomToggle(e: MouseEvent): void {
  // 双击在 1x ↔ 2.5x 间切换，锚定点击点
  const target = zoomScale.value > 1.01 ? 1 : Z_TOGGLE
  zoomAt(target / zoomScale.value, e.clientX, e.clientY)
}
// 指针拖拽（鼠标）；触摸走 touch 通道（单指拖 + 双指 pinch）
let zPan = false, zPX = 0, zPY = 0, zSTx = 0, zSTy = 0
function onZoomPointerDown(e: PointerEvent): void {
  if (e.pointerType === 'touch') return
  zPan = true; zPX = e.clientX; zPY = e.clientY; zSTx = zoomTx.value; zSTy = zoomTy.value
  ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
}
function onZoomPointerMove(e: PointerEvent): void {
  if (!zPan) return
  zoomTx.value = zSTx + (e.clientX - zPX)
  zoomTy.value = zSTy + (e.clientY - zPY)
}
function onZoomPointerUp(): void { zPan = false }
let zPinch = 0, zPinchScale = 1, zTapT = 0
function onZoomTouchStart(e: TouchEvent): void {
  if (e.touches.length === 2) {
    zPinch = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    zPinchScale = zoomScale.value
  } else if (e.touches.length === 1) {
    zPan = true; zPX = e.touches[0].clientX; zPY = e.touches[0].clientY; zSTx = zoomTx.value; zSTy = zoomTy.value
    // 双击检测（300ms 内第二次 touchstart）
    const now = Date.now()
    if (now - zTapT < 300) {
      const target = zoomScale.value > 1.01 ? 1 : Z_TOGGLE
      zoomAt(target / zoomScale.value, e.touches[0].clientX, e.touches[0].clientY)
      zTapT = 0
    } else zTapT = now
  }
}
function onZoomTouchMove(e: TouchEvent): void {
  if (e.touches.length === 2 && zPinch > 0) {
    e.preventDefault()
    const d = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    const next = Math.min(Z_MAX, Math.max(Z_MIN, (d / zPinch) * zPinchScale))
    const cx = (e.touches[0].clientX + e.touches[1].clientX) / 2
    const cy = (e.touches[0].clientY + e.touches[1].clientY) / 2
    zoomTx.value = cx - (cx - zoomTx.value) * (next / zoomScale.value)
    zoomTy.value = cy - (cy - zoomTy.value) * (next / zoomScale.value)
    zoomScale.value = next
  } else if (e.touches.length === 1 && zPan) {
    e.preventDefault()
    zoomTx.value = zSTx + (e.touches[0].clientX - zPX)
    zoomTy.value = zSTy + (e.touches[0].clientY - zPY)
  }
}
function onZoomTouchEnd(e: TouchEvent): void { if (e.touches.length < 2) zPinch = 0; if (e.touches.length === 0) zPan = false }
function onZoomKeydown(e: KeyboardEvent): void { if (e.key === 'Escape') closeZoom() }

onMounted(() => {
  void render()
  window.addEventListener('keydown', onZoomKeydown)
})
watch(() => props.data, () => void render())
onBeforeUnmount(() => window.removeEventListener('keydown', onZoomKeydown))
</script>

<template>
  <div class="relative h-full overflow-auto" data-testid="fp-preview-docx">
    <!-- docx-preview 把 <style> 注进这里；容器 display:none，样式仍全局生效 -->
    <div ref="styleEl" aria-hidden="true" style="display: none"></div>
    <div v-if="rendering && !errorText" class="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground animate-pulse">docx 渲染中…</div>
    <div v-else-if="errorText" class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <p class="text-xs text-muted-foreground">docx 渲染失败：文件可能已损坏或不是真正的 docx 内容</p>
      <p class="max-w-full text-[0.62rem] text-muted-foreground/70 break-all">{{ errorText }}</p>
      <p class="text-[0.62rem] text-muted-foreground/70">可用上方「下载」在本地应用中打开</p>
    </div>
    <!-- 纸面卡：docx run 颜色多为 auto（黑），深色面板上必须给恒定浅色阅读面 -->
    <div v-show="!errorText" ref="bodyEl" class="docx-body m-3 rounded-lg border border-border bg-[#fbfaf8] text-[#1a1a1a] shadow-sm"></div>

    <!-- 图片缩放查看器：全屏遮罩（teleport 到 body，覆盖抽屉自身的 overflow 裁剪） -->
    <Teleport to="body">
      <div
        v-if="zoomSrc"
        class="docx-zoom"
        data-testid="fp-docx-zoom"
        @wheel="onZoomWheel"
        @pointerdown="onZoomPointerDown"
        @pointermove="onZoomPointerMove"
        @pointerup="onZoomPointerUp"
        @pointercancel="onZoomPointerUp"
        @touchstart="onZoomTouchStart"
        @touchmove="onZoomTouchMove"
        @touchend="onZoomTouchEnd"
        @dblclick="onZoomToggle"
      >
        <img :src="zoomSrc" alt="" class="docx-zoom-img" draggable="false" :style="{ transform: zoomTransform }" />
        <div class="docx-zoom-bar">
          <button type="button" title="缩小" data-testid="fp-docx-zoom-out" @click="zoomStep(-1)">−</button>
          <button type="button" class="docx-zoom-pct" data-testid="fp-docx-zoom-pct" title="重置为 100%" @click="zoomScale = 1; zoomTx = 0; zoomTy = 0">{{ zoomPct }}%</button>
          <button type="button" title="放大" data-testid="fp-docx-zoom-in" @click="zoomStep(1)">＋</button>
          <button type="button" class="docx-zoom-close" title="关闭（ESC）" data-testid="fp-docx-zoom-close" @click="closeZoom">✕</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
/* docx-preview 默认把 wrapper 刷成灰底+30px 内边距、section 固定页宽 —— reflow 形态全部覆盖掉。 */
.docx-body :deep(.docx-wrapper) {
  background: transparent;
  padding: 0;
}
/* 抽屉面板整体 user-select:none（rd-panel）——正文必须显式开文本选择（2026-09-07「docx 不能
   复制」），与 md 阅读器（FilePreview .fp）同法。图片保留 zoom 光标不受影响。 */
.docx-body {
  user-select: text;
  -webkit-user-select: text;
}
.docx-body :deep(.docx-wrapper > section.docx) {
  width: auto;
  min-height: auto;
  padding: 14px 16px 20px;
  background: transparent;
  box-shadow: none;
}
/* 手机 ~390px：正文默认 11-12pt 偏小但可读；图片必须收缩进容器宽，否则 reflow 被一张图撑破。
   docx-preview 把图包在 width:<n>pt 的 inline-block div 里（实测 420pt）——只约束 img 没用
   （max-width:100% 按父盒算），必须连包裹盒一起封顶；固定 pt 高的包裹盒在变窄后留白，
   用 :has(> img) 把图盒的定高也放开。 */
.docx-body :deep(*) {
  max-width: 100%;
}
.docx-body :deep(img) {
  height: auto;
}
.docx-body :deep(div:has(> img)) {
  height: auto;
}
.docx-body :deep(table) {
  max-width: 100%;
}
/* 图片可点开放大（wireImages 加 title 提示） */
.docx-body :deep(img) {
  cursor: zoom-in;
}

/* ── 全屏缩放查看器 ── */
.docx-zoom {
  position: fixed;
  inset: 0;
  /* 抽屉族层级：scrim 300 / rd-lightbox 360 / fab 2500。必须在 300+ 之上才能真正全屏
     （首版 90 被抽屉压住 → 放大只在非抽屉半边可见，2026-09-07 用户实锤）；500 取
     "盖过抽屉全家族、让位 fab" 的中位。 */
  z-index: 500;
  background: rgba(8, 6, 14, 0.92);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  touch-action: none; /* 手势全交给自己的 touch 处理（pinch/pan） */
}
.docx-zoom-img {
  max-width: 92vw;
  max-height: 88vh;
  user-select: none;
  -webkit-user-drag: none;
  will-change: transform;
}
.docx-zoom-bar {
  position: absolute;
  bottom: max(14px, env(safe-area-inset-bottom));
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  border-radius: 999px;
  background: rgba(20, 14, 32, 0.9);
  border: 1px solid rgba(255, 255, 255, 0.14);
}
.docx-zoom-bar button {
  min-width: 34px; /* 触控目标 ≥44px 含 padding 的可点区（移动端一等公民） */
  height: 34px;
  padding: 0 10px;
  border-radius: 8px;
  color: #eee;
  background: rgba(255, 255, 255, 0.08);
  font-size: 15px;
  line-height: 1;
}
.docx-zoom-bar button:active { background: rgba(255, 255, 255, 0.2); }
.docx-zoom-pct { font-size: 12px; font-variant-numeric: tabular-nums; }
.docx-zoom-close { margin-left: 4px; }
</style>
