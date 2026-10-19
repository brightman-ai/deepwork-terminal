<script setup lang="ts">
/** One tab strip shared by directory browsing and reading; document state stays in FilesPanel. */
import { computed } from 'vue'
const props = defineProps<{
  tabs: readonly { key: string; name: string; absPath: string; loading: boolean }[]
  activeKey: string
}>()
const emit = defineEmits<{ (e: 'select', key: string): void; (e: 'close', key: string): void }>()
const labels = computed(() => new Map(props.tabs.map((tab): [string, string] => {
  const peers = props.tabs.filter((other) => other.key !== tab.key && other.name === tab.name)
  if (!peers.length) return [tab.key, tab.name]
  const parts = tab.absPath.replace(/\\/g, '/').split('/').filter(Boolean)
  for (let count = 2; count < parts.length; count++) {
    const suffix = parts.slice(-count).join('/')
    if (peers.every((other) => !other.absPath.replace(/\\/g, '/').endsWith('/' + suffix))) return [tab.key, suffix]
  }
  return [tab.key, parts.join('/')]
})))
</script>

<template>
  <div class="fp-preview-tabs shrink-0 flex items-center gap-1 overflow-x-auto border-b border-border/60 px-2 py-1 no-scrollbar" data-testid="fp-preview-tabs">
    <button
      v-for="(tab, tabIndex) in tabs"
      :key="tab.key"
      type="button"
      class="fp-preview-tab inline-flex min-w-0 max-w-[12rem] items-center gap-1 rounded px-2 py-1 text-[0.62rem]"
      :class="tab.key === activeKey ? 'bg-accent text-foreground' : 'text-muted-foreground hover:bg-muted/60'"
      :title="tab.absPath"
      :data-testid="`fp-preview-tab-${encodeURIComponent(tab.key)}`"
      @click="emit('select', tab.key)"
    >
      <span class="fp-preview-tab-index" :aria-label="`标签 ${tabIndex === 9 ? 0 : tabIndex + 1}`">{{ tabIndex === 9 ? 0 : tabIndex + 1 }}</span>
      <span class="min-w-0 truncate">{{ labels.get(tab.key) }}</span>
      <span v-if="tab.loading" class="shrink-0 opacity-70">…</span>
      <span
        class="shrink-0 rounded px-0.5 text-[0.7rem] leading-none opacity-70 hover:opacity-100"
        :aria-label="`关闭 ${tab.name}`"
        :data-testid="`fp-preview-tab-close-${encodeURIComponent(tab.key)}`"
        @click.stop="emit('close', tab.key)"
      >×</span>
    </button>
  </div>
</template>
<style scoped>
.fp-preview-tab-index {
  display: inline-flex; align-items: center; justify-content: center;
  width: 1.05rem; height: 1.05rem; flex: 0 0 auto;
  border: 1px solid hsl(var(--border)); border-radius: 999px;
  color: hsl(var(--muted-foreground)); font-size: 0.55rem; line-height: 1;
  font-variant-numeric: tabular-nums;
}
.fp-preview-tab.bg-accent .fp-preview-tab-index { border-color: hsl(var(--primary) / 0.7); color: hsl(var(--foreground)); }
.no-scrollbar { scrollbar-width: none; }
.no-scrollbar::-webkit-scrollbar { display: none; }
</style>
