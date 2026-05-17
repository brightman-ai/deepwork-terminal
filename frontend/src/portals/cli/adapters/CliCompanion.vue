<template>
  <div class="cli-companion" data-testid="cli-portal-companion">
    <div class="companion-header">
      <span class="companion-title">助手</span>
      <button class="companion-close" @click="emit('close')">×</button>
    </div>
    <div class="companion-body">
      <!-- Placeholder: compact chat-assist stub -->
      <!-- Future: wire to ChatStreamAdapter with terminal context -->
      <p class="companion-hint">快速提问终端操作</p>
    </div>
  </div>
</template>

<script setup lang="ts">
const emit = defineEmits<{
  (e: 'close'): void
}>()
</script>

<style scoped>
.cli-companion {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: hsl(var(--card));
  border-left: 1px solid hsl(var(--border));
  font-size: 0.8125rem;
  color: hsl(var(--card-foreground));
}

.companion-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  border-bottom: 1px solid hsl(var(--border));
  flex-shrink: 0;
}

.companion-title {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: hsl(var(--muted-foreground));
}

.companion-close {
  background: none;
  border: none;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  font-size: 1.1rem;
  line-height: 1;
  padding: 0 2px;
}
.companion-close:hover { color: hsl(var(--foreground)); }

.companion-body {
  flex: 1;
  padding: 12px;
  overflow-y: auto;
  min-height: 0;
}

.companion-hint {
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
  font-style: italic;
}
</style>
