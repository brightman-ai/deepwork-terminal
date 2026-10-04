<script setup lang="ts">
import { History } from 'lucide-vue-next'

withDefaults(defineProps<{
  placement: 'status' | 'toolbar'
  disabled?: boolean
}>(), { disabled: false })

const emit = defineEmits<{ (event: 'open'): void }>()
</script>

<template>
  <button
    type="button"
    class="copy-mode-entry"
    :class="`copy-mode-entry--${placement}`"
    :disabled="disabled"
    :title="disabled ? '远程终端暂不支持回看历史' : '回看历史（应用 Copy Mode）'"
    :aria-label="disabled ? '回看历史不可用：远程终端暂不支持' : '回看历史 Copy Mode'"
    data-testid="app-copy-mode-entry"
    @click="emit('open')"
  >
    <History :size="14" aria-hidden="true" />
    <span>回看</span>
  </button>
</template>

<style scoped>
.copy-mode-entry {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  white-space: nowrap;
  cursor: pointer;
  touch-action: manipulation;
  transition: color 0.1s, background 0.1s, transform 0.08s;
}
.copy-mode-entry--status {
  min-width: 56px;
  height: 30px;
  padding: 0 7px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: hsl(var(--muted-foreground, 240 5% 64%));
  font-size: 0.68rem;
}
.copy-mode-entry--status:hover:not(:disabled) {
  color: hsl(var(--foreground, 0 0% 98%));
  background: hsl(var(--accent, 240 4% 22%));
}
.copy-mode-entry--toolbar {
  min-width: 44px;
  min-height: 44px;
  padding: 0 9px;
  border: 1px solid var(--tb-btn-border, #333);
  border-bottom: 2px solid var(--tb-btn-depth, #111);
  border-radius: 6px;
  background: var(--tb-btn-bg, #242424);
  color: var(--tb-btn-color, #e8e8ea);
  font-size: 0.7rem;
  font-weight: 600;
}
.copy-mode-entry--toolbar:hover:not(:disabled) { background: #303039; }
.copy-mode-entry:focus-visible { outline: 2px solid hsl(var(--ring, 240 5% 64%)); outline-offset: 1px; }
.copy-mode-entry:active:not(:disabled) { transform: translateY(1px) scale(0.96); }
.copy-mode-entry:disabled { opacity: 0.48; cursor: not-allowed; }
</style>
