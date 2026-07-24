<template>
  <TransitionGroup
    name="keycast"
    tag="div"
    class="keycast-stack"
  >
    <div v-for="entry in entries" :key="entry.id" class="keycast-pill">
      <span class="keycast-text">{{ entry.display }}</span>
    </div>
  </TransitionGroup>
</template>

<script setup lang="ts">
import type { KeyCastrEntry } from '@terminal/composables/cli/useKeyCastrHud'

defineProps<{
  entries: readonly KeyCastrEntry[]
}>()
</script>

<style scoped>
/* Anchored to the TOP of the TERMINAL BODY, centered — never the bottom (that's the thumb/typing
   zone these pills used to bury, request.md §8.2/§8.3). Stacks DOWNWARD as new entries append
   (flex-direction: column + top anchor), the opposite of the old bottom-anchored upward growth.

   `position: absolute` inside `.terminal-body` (already `position: relative` — CliTerminalSurface's
   own style block), NOT `position: fixed`. Fixed pinned the stack to the LAYOUT VIEWPORT's top, so
   the first pill landed over `.cli-tab-bar` and, once 3–5 pills stacked (~26px + 6px gap each), the
   second one down already covered `.surface-status-row` — the pane bar whose status dots and window
   buttons are the whole point of the attention work. Burying the attention system while you type is
   the same mistake as burying the keyboard, one row up. Anchoring to the body's own box makes the
   pills start BELOW every chrome row by construction, with no offset to guess (KC-5) and no
   safe-area math: the body is already inset by the layout above it. Same reason UploadProgressFloat
   is body-relative.

   z-index is a normal float layer (not all-time-highest-in-app anymore) — no z-index war with
   UploadProgressFloat (top:38/40px; right:8px, also inside .terminal-body): that one is
   right-aligned, this one is centered, so the two never collide (KC-6). */
.keycast-stack {
  position: absolute;
  top: 8px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 200;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  pointer-events: none;
}
.keycast-pill {
  background: rgba(0, 0, 0, 0.82);
  border: 1px solid rgba(255, 255, 255, 0.15);
  border-radius: 8px;
  padding: 4px 14px;
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
}
.keycast-text {
  font-family: 'SF Pro Display', -apple-system, 'Cascadia Code', monospace;
  font-size: 16px;
  font-weight: 600;
  color: #fff;
  letter-spacing: 0.5px;
  white-space: nowrap;
}
.keycast-enter-from {
  opacity: 0;
  transform: scale(0.7) translateY(10px);
}
.keycast-enter-active {
  transition: all 0.15s cubic-bezier(0.16, 1, 0.3, 1);
}
.keycast-leave-active {
  transition: all 0.4s cubic-bezier(0.4, 0, 1, 1);
}
.keycast-leave-to {
  opacity: 0;
  transform: translateY(-12px);
}
.keycast-move {
  transition: transform 0.2s ease;
}
</style>
