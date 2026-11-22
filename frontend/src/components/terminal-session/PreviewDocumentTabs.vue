<script setup lang="ts">
/** One tab strip shared by directory browsing and reading; document state stays in FilesPanel. */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
const props = defineProps<{
  tabs: readonly { key: string; name: string; absPath: string; loading: boolean }[]
  activeKey: string
}>()
const emit = defineEmits<{
  (e: 'select', key: string): void
  (e: 'close', key: string): void
  (e: 'close-others', key: string): void
  (e: 'close-all'): void
}>()
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
// Keep the active document tab visible when many tabs overflow the strip.
const listEl = ref<HTMLElement | null>(null)
function scrollActiveIntoView(): void {
  const host = listEl.value
  if (!host || typeof host.querySelector !== 'function') return
  const active = host.querySelector<HTMLElement>('.fp-preview-tab.is-active')
  active?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
}
watch(() => [props.activeKey, props.tabs.length], () => { void nextTick(scrollActiveIntoView) })
// The strip is re-laid-out when the workbench is reopened / resized (v-show keeps it mounted), so a
// scroll offset computed at the old width can leave the active tab clipped at the right edge.
let stripObserver: ResizeObserver | null = null
watch(listEl, (el) => {
  stripObserver?.disconnect()
  stripObserver = null
  if (!el || typeof ResizeObserver === 'undefined') return
  stripObserver = new ResizeObserver(() => scrollActiveIntoView())
  stripObserver.observe(el)
})
onBeforeUnmount(() => stripObserver?.disconnect())
</script>

<template>
  <div class="fp-preview-tabs shrink-0 flex items-center gap-1 border-b border-border/60 px-2 py-1" data-testid="fp-preview-tabs">
    <div ref="listEl" class="fp-preview-tabs-list min-w-0 flex-1 flex items-center gap-1 overflow-x-auto no-scrollbar">
      <button
        v-for="(tab, tabIndex) in tabs"
        :key="tab.key"
        type="button"
        class="fp-preview-tab inline-flex shrink-0 min-w-0 max-w-[10rem] items-center gap-1 rounded px-2 py-1 text-[0.62rem]"
        :class="{ 'is-active': tab.key === activeKey }"
        :title="tab.absPath"
        :data-testid="`fp-preview-tab-${encodeURIComponent(tab.key)}`"
        @click="emit('select', tab.key)"
        @mousedown.middle.prevent
        @auxclick.middle.prevent="emit('close', tab.key)"
      >
        <span class="fp-preview-tab-index" :aria-label="`标签 ${tabIndex === 9 ? 0 : tabIndex + 1}`">{{ tabIndex === 9 ? 0 : tabIndex + 1 }}</span>
        <span class="min-w-0 truncate select-text cursor-text">{{ labels.get(tab.key) }}</span>
        <span v-if="tab.loading" class="shrink-0 opacity-70">…</span>
        <span
          class="fp-preview-tab-close shrink-0 rounded px-0.5 text-[0.7rem] leading-none opacity-70 hover:opacity-100"
          :aria-label="`关闭 ${tab.name}`"
          :data-testid="`fp-preview-tab-close-${encodeURIComponent(tab.key)}`"
          @click.stop="emit('close', tab.key)"
        >×</span>
      </button>
    </div>
    <div v-if="tabs.length > 1" class="fp-preview-tabs-actions shrink-0 flex items-center gap-1 pl-1">
      <button
        v-if="activeKey"
        type="button"
        class="fp-preview-tabs-act"
        title="关闭除当前外的所有标签"
        data-testid="fp-preview-tabs-close-others"
        @click="emit('close-others', activeKey)"
      >关其他</button>
      <button
        type="button"
        class="fp-preview-tabs-act"
        title="关闭全部打开的文件标签"
        data-testid="fp-preview-tabs-close-all"
        @click="emit('close-all')"
      >清空</button>
    </div>
  </div>
</template>
<style scoped>
.fp-preview-tabs {
  background: rgba(16, 11, 26, 0.72);
}
.fp-preview-tab {
  border: 1px solid rgba(139, 118, 178, 0.22);
  background: rgba(255, 255, 255, 0.03);
  color: #a99bbe;
  transition: background 0.12s ease, border-color 0.12s ease, color 0.12s ease;
}
.fp-preview-tab:hover {
  background: rgba(168, 85, 247, 0.14);
  border-color: rgba(168, 85, 247, 0.42);
  color: #efe8fb;
}
.fp-preview-tab.is-active {
  background: rgba(139, 92, 246, 0.28);
  border-color: #a855f7;
  color: #f8f5ff;
  font-weight: 600;
  box-shadow: inset 0 -2px 0 #c084fc;
}
.fp-preview-tab-index {
  display: inline-flex; align-items: center; justify-content: center;
  width: 1.05rem; height: 1.05rem; flex: 0 0 auto;
  border: 1px solid hsl(var(--border)); border-radius: 999px;
  color: #9e8eb8; font-size: 0.55rem; line-height: 1;
  font-variant-numeric: tabular-nums;
}
.fp-preview-tab.is-active .fp-preview-tab-index {
  border-color: #c084fc;
  background: rgba(168, 85, 247, 0.35);
  color: #ffffff;
}
.fp-preview-tabs-actions {
  border-left: 1px solid rgba(139, 118, 178, 0.24);
}
.fp-preview-tabs-act {
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid rgba(139, 118, 178, 0.28);
  background: rgba(255, 255, 255, 0.03);
  color: #b7a8cf;
  font-size: 0.58rem;
  line-height: 1.3;
  white-space: nowrap;
  cursor: pointer;
  transition: background 0.12s ease, color 0.12s ease, border-color 0.12s ease;
}
.fp-preview-tabs-act:hover {
  background: rgba(168, 85, 247, 0.18);
  border-color: rgba(168, 85, 247, 0.55);
  color: #f4eeff;
}
.no-scrollbar { scrollbar-width: none; }
.no-scrollbar::-webkit-scrollbar { display: none; }
</style>
