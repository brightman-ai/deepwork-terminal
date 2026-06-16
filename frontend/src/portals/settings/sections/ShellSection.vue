<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'

interface TerminalSettings { shell: string; bufferSize: number; maxSessions: number }
const { cliFetch } = useCliAuth()
const s = ref<TerminalSettings | null>(null)
const loading = ref(true)

function formatBytes(b: number): string {
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(0)} KB`
  return `${b} B`
}
onMounted(async () => {
  try { const r = await cliFetch('/api/settings'); if (r.ok) s.value = await r.json() } catch { /* ignore */ } finally { loading.value = false }
})
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-shell">
    <div class="ssec-header">Terminal</div>
    <div v-if="loading" class="ssec-loading">加载中…</div>
    <div v-else class="ssec-grid">
      <div class="ssec-card"><span class="ssec-card-label">Default Shell</span><span class="ssec-card-value ssec-mono">{{ s?.shell || '—' }}</span></div>
      <div class="ssec-card"><span class="ssec-card-label">Buffer Size</span><span class="ssec-card-value">{{ s ? formatBytes(s.bufferSize) : '—' }}</span></div>
      <div class="ssec-card"><span class="ssec-card-label">Max Sessions</span><span class="ssec-card-value">{{ s?.maxSessions ?? '—' }}</span></div>
    </div>
    <p class="ssec-hint">终端配置为只读。如需修改，请编辑启动参数或配置文件。</p>
  </div>
</template>
