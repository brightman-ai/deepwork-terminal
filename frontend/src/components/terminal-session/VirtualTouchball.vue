<template>
  <template v-if="visible">
    <!-- Draggable ring — JumpDesktop style -->
    <div
      class="virtual-touchball"
      :style="{ left: position.x + 'px', top: position.y + 'px' }"
      @touchstart.prevent="onTouchStart"
      @touchmove.prevent="onTouchMove"
      @touchend.prevent="onTouchEnd"
    >
      <svg class="touchball-ring" width="32" height="32" viewBox="0 0 32 32">
        <circle cx="16" cy="16" r="13" fill="rgba(0,0,0,0.15)" stroke="rgba(255,255,255,0.7)" stroke-width="2"/>
        <circle cx="16" cy="16" r="5" fill="none" stroke="rgba(255,255,255,0.3)" stroke-width="1"/>
        <circle cx="16" cy="16" r="2" fill="rgba(255,255,255,0.6)"/>
      </svg>
    </div>

    <!-- Cursor indicator — white arrow pointer. `cursorPosition` is the ARROW TIP (the
         point the user aims with). The SVG's tip sits at local (CURSOR_TIP_X, CURSOR_TIP_Y),
         so we translate the box by that offset to put the tip exactly on cursorPosition.
         The copy-mode anchor is mapped from the SAME cursorPosition, so the selection starts
         precisely under the visible tip. -->
    <div
      v-if="cursorPosition"
      class="cursor-indicator"
      :style="{
        left: cursorPosition.x + 'px',
        top: cursorPosition.y + 'px',
        transform: `translate(${-CURSOR_TIP_X}px, ${-CURSOR_TIP_Y}px)`,
      }"
    >
      <svg width="18" height="22" viewBox="0 0 18 22" fill="none">
        <path d="M2 2 L2 19 L6.5 14.5 L9.5 21 L12.5 19.5 L9.5 13 L15.5 13 Z"
              fill="white" stroke="rgba(0,0,0,0.75)" stroke-width="1.5"
              stroke-linejoin="round" stroke-linecap="round"/>
      </svg>
    </div>
  </template>
</template>

<script setup lang="ts">
/**
 * VirtualTouchball — JumpDesktop-style draggable ring with separate cursor.
 * ALWAYS visible on mobile. Supports tap, double-tap, triple-tap, long-press, drag.
 */
import { reactive, onMounted } from 'vue'
import { CURSOR_TIP_X, CURSOR_TIP_Y } from './touchballCursorGeometry'

defineProps<{
  visible: boolean
  cursorPosition?: { x: number; y: number } | null
}>()

const emit = defineEmits<{
  (e: 'tap', x: number, y: number): void
  (e: 'doubleTap', x: number, y: number): void
  (e: 'tripleTap', x: number, y: number): void
  (e: 'drag', deltaX: number, deltaY: number): void
  (e: 'longPress', x: number, y: number): void
  (e: 'positionChange', x: number, y: number): void
}>()

const DRAG_RATIO = 1.5
const LONG_PRESS_MS = 500
const MULTI_TAP_MS = 300
const BALL_HALF = 36

const position = reactive({ x: 200, y: 300 })

onMounted(() => {
  // MOB-007: park at the bottom-right corner (just above the 58px quick bar) instead
  // of mid-terminal-body — the old x=82%·y=55% resting spot floated the 72px ring over
  // the terminal text (file-path lines were covered). Still fully draggable from here.
  const quickBarH = 58
  position.x = Math.round(window.innerWidth - BALL_HALF - 8)
  position.y = Math.round(window.innerHeight - quickBarH - BALL_HALF - 8)
  clampPosition()
  emit('positionChange', position.x, position.y)
})

let touchStartTime = 0
let touchStartX = 0
let touchStartY = 0
let hasMoved = false
let longPressTimer: ReturnType<typeof setTimeout> | null = null

// Multi-tap tracking
let tapCount = 0
let multiTapTimer: ReturnType<typeof setTimeout> | null = null

function clampPosition() {
  position.x = Math.max(BALL_HALF, Math.min(window.innerWidth - BALL_HALF, position.x))
  position.y = Math.max(BALL_HALF, Math.min(window.innerHeight - BALL_HALF, position.y))
}

function onTouchStart(e: TouchEvent) {
  const touch = e.touches[0]
  touchStartX = touch.clientX
  touchStartY = touch.clientY
  touchStartTime = Date.now()
  hasMoved = false

  longPressTimer = setTimeout(() => {
    if (!hasMoved) {
      emit('longPress', position.x, position.y)
    }
  }, LONG_PRESS_MS)
}

function onTouchMove(e: TouchEvent) {
  const touch = e.touches[0]
  const rawDx = touch.clientX - touchStartX
  const rawDy = touch.clientY - touchStartY

  if (Math.abs(rawDx) > 3 || Math.abs(rawDy) > 3) {
    hasMoved = true
    if (longPressTimer) { clearTimeout(longPressTimer); longPressTimer = null }
  }

  position.x += rawDx
  position.y += rawDy
  clampPosition()

  touchStartX = touch.clientX
  touchStartY = touch.clientY

  emit('positionChange', position.x, position.y)
  emit('drag', rawDx * DRAG_RATIO, rawDy * DRAG_RATIO)
}

function onTouchEnd() {
  if (longPressTimer) { clearTimeout(longPressTimer); longPressTimer = null }

  if (!hasMoved && Date.now() - touchStartTime < LONG_PRESS_MS) {
    // This is a tap — count for multi-tap detection
    tapCount++

    if (multiTapTimer) { clearTimeout(multiTapTimer) }

    multiTapTimer = setTimeout(() => {
      if (tapCount === 1) {
        emit('tap', position.x, position.y)
      } else if (tapCount === 2) {
        emit('doubleTap', position.x, position.y)
      } else if (tapCount >= 3) {
        emit('tripleTap', position.x, position.y)
      }
      tapCount = 0
    }, MULTI_TAP_MS)
  }
}

/** Move ball to a specific screen position (called by parent for terminal tap-to-move) */
function moveTo(x: number, y: number) {
  position.x = x
  position.y = y
  clampPosition()
  emit('positionChange', position.x, position.y)
}

defineExpose({ moveTo })
</script>

<style scoped>
.virtual-touchball {
  position: fixed;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  transform: translate(-50%, -50%);
  touch-action: none;
  user-select: none;
  -webkit-user-select: none;
  z-index: 110;
  cursor: grab;
  background: radial-gradient(circle at 38% 32%,
    rgba(255,255,255,0.14) 0%,
    rgba(255,255,255,0.04) 55%,
    rgba(0,0,0,0.12) 100%);
  backdrop-filter: blur(6px) saturate(1.4);
  -webkit-backdrop-filter: blur(6px) saturate(1.4);
  box-shadow:
    0 4px 20px rgba(0,0,0,0.45),
    inset 0 1px 0 rgba(255,255,255,0.18),
    inset 0 -1px 0 rgba(0,0,0,0.25);
}
.virtual-touchball:active { cursor: grabbing; opacity: 1; }

/* MOB-007: smaller + semi-transparent at rest on phones so the corner ring never
   competes with terminal text; full opacity on touch (see :active above). */
@media (max-width: 768px) {
  .virtual-touchball {
    width: 56px;
    height: 56px;
    opacity: 0.62;
  }
}

.touchball-ring {
  pointer-events: none;
  filter: drop-shadow(0 1px 4px rgba(0,0,0,0.5));
}

.cursor-indicator {
  position: fixed;
  pointer-events: none;
  z-index: 115;
  /* transform set inline → shifts the SVG so its arrow tip lands on cursorPosition. */
  filter: drop-shadow(0 1px 3px rgba(0,0,0,0.6));
}
</style>
