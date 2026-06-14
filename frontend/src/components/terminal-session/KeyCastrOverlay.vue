<template>
  <TransitionGroup
    name="keycast"
    tag="div"
    class="keycast-stack"
    :style="{ bottom: `${bottomOffset}px` }"
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
  bottomOffset: number
}>()
</script>

<style scoped>
.keycast-stack {
  position: fixed;
  left: 50%;
  transform: translateX(-50%);
  z-index: 9999;
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
  font-size: 22px;
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
