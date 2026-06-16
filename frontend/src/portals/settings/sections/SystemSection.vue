<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'

interface SystemInfo { port: number; pid: number; commit: string }
const { cliFetch } = useCliAuth()
const info = ref<SystemInfo | null>(null)
const loading = ref(true)
onMounted(async () => {
  try { const r = await cliFetch('/api/system'); if (r.ok) info.value = await r.json() } catch { /* ignore */ } finally { loading.value = false }
})
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-system">
    <div class="ssec-header">System Info</div>
    <div v-if="loading" class="ssec-loading">加载中…</div>
    <div v-else class="ssec-grid">
      <div class="ssec-card"><span class="ssec-card-label">Port</span><span class="ssec-card-value">{{ info?.port ?? '—' }}</span></div>
      <div class="ssec-card"><span class="ssec-card-label">PID</span><span class="ssec-card-value">{{ info?.pid ?? '—' }}</span></div>
      <div class="ssec-card"><span class="ssec-card-label">Version / Commit</span><span class="ssec-card-value">{{ info?.commit || 'dev' }}</span></div>
    </div>
  </div>
</template>
