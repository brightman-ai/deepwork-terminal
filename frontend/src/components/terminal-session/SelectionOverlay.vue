<template>
  <div class="selection-overlay" v-if="visible">
    <!-- Off-screen anchor indicators -->
    <div v-if="anchor1Offscreen === 'above'" class="offscreen-indicator offscreen-top">
      ⊳ 起点 ↑ 第 {{ anchor1?.bufferRow ?? '?' }} 行
    </div>
    <div v-if="anchor1Offscreen === 'below'" class="offscreen-indicator offscreen-bottom">
      ⊳ 起点 ↓ 第 {{ anchor1?.bufferRow ?? '?' }} 行
    </div>
    <div v-if="anchor2Offscreen === 'above'" class="offscreen-indicator offscreen-top offscreen-top--alt">
      ⊲ 终点 ↑ 第 {{ anchor2?.bufferRow ?? '?' }} 行
    </div>
    <div v-if="anchor2Offscreen === 'below'" class="offscreen-indicator offscreen-bottom offscreen-bottom--alt">
      ⊲ 终点 ↓ 第 {{ anchor2?.bufferRow ?? '?' }} 行
    </div>

    <!-- Anchor 1 marker (draggable) -->
    <div
      v-if="anchor1Screen && anchor1Offscreen === 'visible'"
      class="anchor-marker anchor-1"
      :style="{ left: anchor1Screen.x + 'px', top: anchor1Screen.y + 'px' }"
      @touchstart.prevent="(e) => onAnchorTouchStart(e, 1)"
      @touchmove.prevent="(e) => onAnchorTouchMove(e, 1)"
      @touchend.prevent="(e) => onAnchorTouchEnd(e, 1)"
    >
      <svg width="20" height="28" viewBox="0 0 20 28" class="anchor-svg anchor-svg--start">
        <path d="M2 0 L2 28 M2 4 L14 14 L2 24" fill="rgba(66,165,245,0.3)" stroke="#42a5f5" stroke-width="2" stroke-linejoin="round"/>
      </svg>
    </div>

    <!-- Anchor 2 marker (draggable) -->
    <div
      v-if="anchor2Screen && anchor2Offscreen === 'visible'"
      class="anchor-marker anchor-2"
      :style="{ left: anchor2Screen.x + 'px', top: anchor2Screen.y + 'px' }"
      @touchstart.prevent="(e) => onAnchorTouchStart(e, 2)"
      @touchmove.prevent="(e) => onAnchorTouchMove(e, 2)"
      @touchend.prevent="(e) => onAnchorTouchEnd(e, 2)"
    >
      <svg width="20" height="28" viewBox="0 0 20 28" class="anchor-svg anchor-svg--end">
        <path d="M18 0 L18 28 M18 4 L6 14 L18 24" fill="rgba(245,124,66,0.3)" stroke="#f57c42" stroke-width="2" stroke-linejoin="round"/>
      </svg>
    </div>

    <!-- Line count (Copy button rendered in MobileOverlay for z-index) -->
    <div
      v-if="lineCount > 0 && lineCountPos"
      class="line-count-badge"
      :style="{ left: lineCountPos.x + 'px', top: lineCountPos.y + 'px' }"
    >
      {{ lineCount }} 行
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { CellCoord } from '@/types/terminal'

const props = defineProps<{
  visible: boolean
  anchor1: CellCoord | null
  anchor2: CellCoord | null
  cellToScreen: (col: number, row: number) => { x: number; y: number }
  screenToCell: (x: number, y: number) => { col: number; row: number }
  terminalRows: number
  viewportY: number
  guideText?: string
}>()

const emit = defineEmits<{
  (e: 'anchorDrag', anchorId: 1 | 2, cell: CellCoord): void
}>()

// Compute viewport-relative row from buffer-absolute row
function viewportRow(anchor: CellCoord | null): number | null {
  if (!anchor || anchor.bufferRow == null) return anchor?.row ?? null
  return anchor.bufferRow - props.viewportY
}

type OffscreenState = 'visible' | 'above' | 'below'

function offscreenState(anchor: CellCoord | null): OffscreenState {
  if (!anchor) return 'visible'
  const vRow = viewportRow(anchor)
  if (vRow == null) return 'visible'
  if (vRow < 0) return 'above'
  if (vRow >= props.terminalRows) return 'below'
  return 'visible'
}

const anchor1Offscreen = computed(() => offscreenState(props.anchor1))
const anchor2Offscreen = computed(() => offscreenState(props.anchor2))

const anchor1Screen = computed(() => {
  if (!props.anchor1) return null
  const vRow = viewportRow(props.anchor1)
  if (vRow == null) return null
  return props.cellToScreen(props.anchor1.col, vRow)
})

const anchor2Screen = computed(() => {
  if (!props.anchor2) return null
  const vRow = viewportRow(props.anchor2)
  if (vRow == null) return null
  return props.cellToScreen(props.anchor2.col, vRow)
})

const lineCount = computed(() => {
  if (!props.anchor1 || !props.anchor2) return 0
  const r1 = props.anchor1.bufferRow ?? props.anchor1.row
  const r2 = props.anchor2.bufferRow ?? props.anchor2.row
  return Math.abs(r2 - r1) + 1
})

const lineCountPos = computed(() => {
  if (!anchor1Screen.value || !anchor2Screen.value) return null
  return {
    x: (anchor1Screen.value.x + anchor2Screen.value.x) / 2,
    y: (anchor1Screen.value.y + anchor2Screen.value.y) / 2,
  }
})

// --- Touch drag for anchor adjustment ---
const draggingAnchor = ref<1 | 2 | null>(null)

function onAnchorTouchStart(_e: TouchEvent, id: 1 | 2) {
  draggingAnchor.value = id
}

function onAnchorTouchMove(e: TouchEvent, id: 1 | 2) {
  if (draggingAnchor.value !== id) return
  const touch = e.touches[0]
  const cell = props.screenToCell(touch.clientX, touch.clientY)
  // Add bufferRow for scroll-aware tracking
  const cellWithBuffer: CellCoord = {
    ...cell,
    bufferRow: props.viewportY + cell.row,
  }
  emit('anchorDrag', id, cellWithBuffer)
}

function onAnchorTouchEnd(_e: TouchEvent, _id: 1 | 2) {
  draggingAnchor.value = null
}
</script>

<style scoped>
.selection-overlay {
  position: fixed;
  inset: 0;
  pointer-events: none;
  z-index: 120;
}

/* Anchor markers */
.anchor-marker {
  position: fixed;
  transform: translate(-4px, -50%);
  width: 36px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  pointer-events: auto;
  touch-action: none;
  cursor: grab;
  z-index: 55;
}
.anchor-marker:active { cursor: grabbing; }

.anchor-svg {
  filter: drop-shadow(0 1px 3px rgba(0,0,0,0.5));
  animation: anchor-breathe 2s ease-in-out infinite;
}
.anchor-svg--end {
  animation-delay: 1s;
}
@keyframes anchor-breathe {
  0%, 100% { opacity: 0.85; }
  50% { opacity: 1; }
}

/* Off-screen indicators */
.offscreen-indicator {
  position: fixed;
  left: 50%;
  transform: translateX(-50%);
  padding: 4px 14px;
  border-radius: 12px;
  font-size: 0.68rem;
  font-weight: 600;
  white-space: nowrap;
  pointer-events: none;
  z-index: 60;
  animation: offscreen-pulse 2.5s ease-in-out infinite;
}
.offscreen-top {
  top: 4px;
  background: rgba(66, 165, 245, 0.25);
  color: #8ec5ff;
  border: 1px solid rgba(66, 165, 245, 0.4);
}
.offscreen-top--alt { top: 28px; }
.offscreen-bottom {
  bottom: 58px;
  background: rgba(245, 124, 66, 0.25);
  color: #f5a060;
  border: 1px solid rgba(245, 124, 66, 0.4);
}
.offscreen-bottom--alt { bottom: 82px; }

@keyframes offscreen-pulse {
  0%, 100% { opacity: 0.7; }
  50% { opacity: 1; }
}

.line-count-badge {
  position: fixed;
  transform: translate(-50%, -50%);
  background: rgba(66, 165, 245, 0.85);
  color: white;
  padding: 3px 10px;
  border-radius: 10px;
  font-size: 0.68rem;
  font-weight: 600;
  white-space: nowrap;
  pointer-events: none;
}
</style>
