<script setup lang="ts">
/**
 * ImageZoomViewer — 预览里任何一张图的全屏查看器。**一份实现，所有预览器共用。**
 *
 * 「涉及图片的要支持放大」是一条**横切要求**，不是某个格式的功能：docx 里的流程图、xlsx 里的
 * 图表截图、xmind 的导图缩略图、md 里的插图 —— 只要预览里出现图，就该能点开看清。写三份就会
 * 漂成三种手感（这正是本轮要修的那类病），所以它在这里只有一份。
 *
 * 交互：PC 滚轮对光标缩放 + 拖拽平移 + 双击 1x↔2.5x + ESC/✕；手机双指 pinch + 单指拖 + 双击。
 * Teleport 到 body：抽屉自己有 overflow 裁剪，不出去就只能在半个面板里"全屏"。
 */
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{ src: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const scale = ref(1)
const tx = ref(0)
const ty = ref(0)
const Z_MIN = 0.2, Z_MAX = 8, Z_TOGGLE = 2.5
const transform = computed(() => `translate(${tx.value}px, ${ty.value}px) scale(${scale.value})`)
const pct = computed(() => Math.round(scale.value * 100))

function reset(): void { scale.value = 1; tx.value = 0; ty.value = 0 }
watch(() => props.src, reset) // 换一张图 = 从 100% 重新开始

function zoomAt(factor: number, cx: number, cy: number): void {
  const next = Math.min(Z_MAX, Math.max(Z_MIN, scale.value * factor))
  // 缩放锚定在 (cx,cy)：光标/双指中点下的那个点在缩放前后不动
  tx.value = cx - (cx - tx.value) * (next / scale.value)
  ty.value = cy - (cy - ty.value) * (next / scale.value)
  scale.value = next
}
function step(dir: 1 | -1): void { zoomAt(dir > 0 ? 1.25 : 1 / 1.25, window.innerWidth / 2, window.innerHeight / 2) }
function onWheel(e: WheelEvent): void {
  e.preventDefault()
  zoomAt(e.deltaY < 0 ? 1.15 : 1 / 1.15, e.clientX, e.clientY)
}
function onToggle(e: MouseEvent): void {
  const target = scale.value > 1.01 ? 1 : Z_TOGGLE
  zoomAt(target / scale.value, e.clientX, e.clientY)
}

// 指针拖拽（鼠标）；触摸走 touch 通道（单指拖 + 双指 pinch）
let pan = false, pX = 0, pY = 0, sTx = 0, sTy = 0
function onPointerDown(e: PointerEvent): void {
  if (e.pointerType === 'touch') return
  pan = true; pX = e.clientX; pY = e.clientY; sTx = tx.value; sTy = ty.value
  ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
}
function onPointerMove(e: PointerEvent): void {
  if (!pan) return
  tx.value = sTx + (e.clientX - pX)
  ty.value = sTy + (e.clientY - pY)
}
function onPointerUp(): void { pan = false }

let pinch = 0, pinchScale = 1, tapT = 0
function onTouchStart(e: TouchEvent): void {
  if (e.touches.length === 2) {
    pinch = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    pinchScale = scale.value
  } else if (e.touches.length === 1) {
    pan = true; pX = e.touches[0].clientX; pY = e.touches[0].clientY; sTx = tx.value; sTy = ty.value
    const now = Date.now() // 双击检测（300ms 内第二次 touchstart）
    if (now - tapT < 300) {
      const target = scale.value > 1.01 ? 1 : Z_TOGGLE
      zoomAt(target / scale.value, e.touches[0].clientX, e.touches[0].clientY)
      tapT = 0
    } else tapT = now
  }
}
function onTouchMove(e: TouchEvent): void {
  if (e.touches.length === 2 && pinch > 0) {
    e.preventDefault()
    const d = Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
    const next = Math.min(Z_MAX, Math.max(Z_MIN, (d / pinch) * pinchScale))
    const cx = (e.touches[0].clientX + e.touches[1].clientX) / 2
    const cy = (e.touches[0].clientY + e.touches[1].clientY) / 2
    tx.value = cx - (cx - tx.value) * (next / scale.value)
    ty.value = cy - (cy - ty.value) * (next / scale.value)
    scale.value = next
  } else if (e.touches.length === 1 && pan) {
    e.preventDefault()
    tx.value = sTx + (e.touches[0].clientX - pX)
    ty.value = sTy + (e.touches[0].clientY - pY)
  }
}
function onTouchEnd(e: TouchEvent): void { if (e.touches.length < 2) pinch = 0; if (e.touches.length === 0) pan = false }
function onKeydown(e: KeyboardEvent): void { if (e.key === 'Escape') emit('close') }

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div
      class="img-zoom"
      data-testid="fp-img-zoom"
      @wheel="onWheel"
      @pointerdown="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
      @touchstart="onTouchStart"
      @touchmove="onTouchMove"
      @touchend="onTouchEnd"
      @dblclick="onToggle"
    >
      <img :src="src" alt="" class="img-zoom-img" draggable="false" :style="{ transform }" />
      <div class="img-zoom-bar">
        <button type="button" title="缩小" data-testid="fp-img-zoom-out" @click="step(-1)">−</button>
        <button type="button" class="img-zoom-pct" data-testid="fp-img-zoom-pct" title="重置为 100%" @click="reset">{{ pct }}%</button>
        <button type="button" title="放大" data-testid="fp-img-zoom-in" @click="step(1)">＋</button>
        <button type="button" class="img-zoom-close" title="关闭（ESC）" data-testid="fp-img-zoom-close" @click="emit('close')">✕</button>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.img-zoom {
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
.img-zoom-img {
  max-width: 92vw;
  max-height: 88vh;
  user-select: none;
  -webkit-user-drag: none;
  will-change: transform;
}
.img-zoom-bar {
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
.img-zoom-bar button {
  min-width: 34px; /* 触控目标 ≥44px 含 padding 的可点区（移动端一等公民） */
  height: 34px;
  padding: 0 10px;
  border-radius: 8px;
  color: #eee;
  background: rgba(255, 255, 255, 0.08);
  font-size: 15px;
  line-height: 1;
}
.img-zoom-bar button:active { background: rgba(255, 255, 255, 0.2); }
.img-zoom-pct { font-variant-numeric: tabular-nums; }
</style>
