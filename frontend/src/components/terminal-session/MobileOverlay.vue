<template>
  <div class="mobile-overlay">
    <!-- Selection Overlay (anchors) -->
    <SelectionOverlay
      class="sel-overlay-pass"
      :visible="anchorState !== 'IDLE'"
      :anchor1="anchor1"
      :anchor2="anchor2"
      :cell-to-screen="cellToScreen"
      :screen-to-cell="screenToCell"
      :terminal-rows="terminalRows"
      :viewport-y="viewportY"
      @anchor-drag="(id, cell) => $emit('anchorDrag', id, cell)"
    />

    <!-- Copy button (floating, when both anchors placed) -->
    <div
      v-if="anchorState === 'HAS_BOTH' && copyBtnScreenPos"
      class="copy-floating-btn"
      :style="{ left: copyBtnScreenPos.x + 'px', top: copyBtnScreenPos.y + 'px' }"
    >
      <button class="copy-btn" @click.stop="$emit('selectionCopy')" @touchend.stop.prevent="$emit('selectionCopy')">Copy</button>
    </div>

    <!-- Touchball + cursor (ALWAYS visible) -->
    <VirtualTouchball
      ref="touchballRef"
      :visible="true"
      :cursor-position="cursorDisplayPos"
      @tap="onTouchballTap"
      @drag="onTouchballDrag"
      @double-tap="onTouchballDoubleTap"
      @triple-tap="onTouchballTripleTap"
      @long-press="onTouchballLongPress"
      @position-change="onBallPositionChange"
    />

    <!-- HUD Panel -->
    <HudPanel
      :visible="hudVisible"
      :events="hudEvents"
      :snapshot="hudSnapshot"
      @close="$emit('closeHud')"
      @clear="$emit('clearHud')"
      @upload="$emit('uploadHud')"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { AnchorState, CellCoord } from '@terminal/types/terminal'
import type { HudEvent, HudSnapshot } from '@terminal/composables/cli/useHudCollector'
import SelectionOverlay from './SelectionOverlay.vue'
import VirtualTouchball from './VirtualTouchball.vue'
import HudPanel from './HudPanel.vue'

const OFFSET_DIST = 70

const props = defineProps<{
  anchorState: AnchorState
  anchor1: CellCoord | null
  anchor2: CellCoord | null
  cellToScreen: (col: number, row: number) => { x: number; y: number }
  screenToCell: (x: number, y: number) => { col: number; row: number }
  terminalRows: number
  viewportY: number
  hudVisible: boolean
  hudEvents: readonly HudEvent[]
  hudSnapshot: Readonly<HudSnapshot>
}>()

const emit = defineEmits<{
  (e: 'selectionCopy'): void
  (e: 'touchballTap', x: number, y: number): void
  (e: 'touchballDoubleTap', x: number, y: number): void
  (e: 'touchballTripleTap', x: number, y: number): void
  (e: 'touchballLongPress', x: number, y: number): void
  (e: 'anchorDrag', anchorId: 1 | 2, cell: CellCoord): void
  (e: 'closeHud'): void
  (e: 'clearHud'): void
  (e: 'uploadHud'): void
}>()

const touchballRef = ref<InstanceType<typeof VirtualTouchball>>()
const ballPos = ref({ x: 200, y: 300 })

function onBallPositionChange(x: number, y: number) { ballPos.value = { x, y } }

const cursorDisplayPos = computed(() => {
  const b = ballPos.value
  const nearTop = b.y < OFFSET_DIST + 20
  return { x: b.x - 25, y: b.y + (nearTop ? OFFSET_DIST : -OFFSET_DIST) }
})

const copyBtnScreenPos = computed(() => {
  if (props.anchorState !== 'HAS_BOTH' || !props.anchor2) return null
  const vRow = props.anchor2.bufferRow != null ? props.anchor2.bufferRow - props.viewportY : props.anchor2.row
  if (vRow < 0 || vRow >= props.terminalRows) return { x: window.innerWidth / 2, y: 70 }
  const screen = props.cellToScreen(props.anchor2.col, vRow)
  let x = screen.x + 30, y = screen.y - 28
  if (x > window.innerWidth - 80) x = screen.x - 80
  if (y < 50) y = screen.y + 30
  return { x, y }
})

function onTouchballTap() { const c = cursorDisplayPos.value; emit('touchballTap', c.x, c.y) }
function onTouchballDoubleTap() { const c = cursorDisplayPos.value; emit('touchballDoubleTap', c.x, c.y) }
function onTouchballTripleTap() { const c = cursorDisplayPos.value; emit('touchballTripleTap', c.x, c.y) }
function onTouchballLongPress() { const c = cursorDisplayPos.value; emit('touchballLongPress', c.x, c.y) }
function onTouchballDrag() {}
function moveBall(x: number, y: number) { touchballRef.value?.moveTo(x, y) }

defineExpose({ moveBall })
</script>

<style scoped>
.mobile-overlay {
  position: fixed;
  inset: 0;
  z-index: 100;
  pointer-events: none;
}
.mobile-overlay > * { pointer-events: auto; }
.mobile-overlay > .sel-overlay-pass { pointer-events: none !important; }

.copy-floating-btn { position: fixed; z-index: 130; pointer-events: auto; }
.copy-btn {
  background: #1a8cff; color: white; border: none;
  padding: 7px 18px; border-radius: 8px; font-size: 0.85rem; font-weight: 700;
  cursor: pointer; touch-action: manipulation; box-shadow: 0 2px 10px rgba(0,0,0,0.35);
}
.copy-btn:active { transform: scale(0.92); background: #1070d0; }
</style>
