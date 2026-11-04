<template>
  <div v-if="entries.length" class="upf-root" :class="{ 'upf-mobile': isMobile }" data-testid="upload-progress-float">
    <div class="upf-pill">
      <template v-if="active.length">
        <div class="upf-row">
          <span class="upf-icon upf-spin" aria-hidden="true">⟳</span>
          <span class="upf-name" :title="active.length === 1 ? active[0].name : undefined">{{ entries.length === 1 ? active[0].name : `正在上传 ${active.length} 个文件` }}</span>
          <span class="upf-pct">{{ aggregatePct }}%</span>
        </div>
        <div class="upf-bar" role="progressbar" aria-label="上传进度" :aria-valuenow="aggregatePct" aria-valuemin="0" aria-valuemax="100">
          <div class="upf-bar-fill" :style="{ width: aggregatePct + '%' }" />
        </div>
        <div class="upf-metrics" data-testid="upload-progress-metrics">
          <span>{{ formatUploadSpeed(active[0].speedBps || 0) }}</span>
          <span>{{ saving ? '正在保存…' : formatUploadEta(active[0].etaSeconds ?? null) }}</span>
        </div>
      </template>
      <div v-else-if="done.length" class="upf-row upf-row--done">
        <span class="upf-icon" aria-hidden="true">✓</span>
        <span class="upf-name">{{ entries.length === 1 ? done[0].name : `已上传 ${done.length} 个文件` }}</span>
      </div>
      <div v-for="e in errors" :key="e.id">
        <div class="upf-row upf-row--error">
          <span class="upf-icon" aria-hidden="true">✕</span>
          <span class="upf-name" :title="e.name">{{ e.name }}</span>
          <button v-if="e.retry" type="button" class="upf-retry" data-testid="upload-progress-retry" @click="e.retry?.()">重试</button>
          <button type="button" class="upf-dismiss" title="关闭" aria-label="关闭" data-testid="upload-progress-dismiss" @click="$emit('dismiss', e.id)">✕</button>
        </div>
        <div class="upf-error-msg" :title="e.error">{{ e.error || '上传失败' }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import type { UploadEntry } from '@terminal/composables/cli/useUploadProgress'
import { formatUploadSpeed, formatUploadEta, uploadProgressPercent } from '@terminal/composables/cli/uploadTelemetry'
const props = defineProps<{ entries: UploadEntry[] }>()
defineEmits<{ (e: 'dismiss', id: string): void }>()
const { isMobile } = useDeviceDetection()
const active = computed(() => props.entries.filter(e => e.status === 'uploading'))
const done = computed(() => props.entries.filter(e => e.status === 'done'))
const errors = computed(() => props.entries.filter(e => e.status === 'error'))
const saving = computed(() => active.value.every(e => e.sent >= e.total))
const aggregatePct = computed(() => {
  const participating = props.entries.filter(e => e.status !== 'error')
  return uploadProgressPercent(participating.reduce((n, e) => n + e.sent, 0), participating.reduce((n, e) => n + e.total, 0))
})
</script>

<style scoped>
/* Anchored top-right INSIDE .terminal-body, below the notify/install icons row
   (which sits at top:4px, ~30px tall) so the two floats never overlap. Wrapper
   itself has no hit-testing footprint beyond its own tiny pill — it never steals
   taps meant for the terminal underneath. */
.upf-root {
  position: absolute;
  top: 40px;
  right: 8px;
  z-index: 45;
  pointer-events: none;
  width: 236px;
  max-width: calc(100% - 16px);
}
.upf-mobile {
  width: 220px;
  max-width: calc(100% - 12px);
  top: 38px;
  right: 6px;
}

.upf-pill {
  pointer-events: auto;
  background: rgba(20, 20, 28, 0.92);
  border: 1px solid #2a2a45;
  border-radius: 8px;
  padding: 6px 8px;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.35);
  backdrop-filter: blur(4px);
}

.upf-row {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  font-size: 0.72rem;
  line-height: 1.3;
  color: #d8d8e0;
}
.upf-row + .upf-row { margin-top: 4px; }

.upf-icon {
  flex-shrink: 0;
  width: 12px;
  display: inline-block;
  text-align: center;
  color: #8a8ab0;
}
.upf-spin { animation: upf-rotate 0.9s linear infinite; }
.upf-row--done .upf-icon, .upf-row--done { color: #4caf50; }
.upf-row--error .upf-icon, .upf-row--error { color: #ef5350; }

.upf-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upf-pct {
  flex-shrink: 0;
  color: #8a8ab0;
  font-variant-numeric: tabular-nums;
}

.upf-retry {
  flex-shrink: 0;
  border: 1px solid #ef5350;
  color: #ef5350;
  background: transparent;
  border-radius: 5px;
  padding: 1px 7px;
  font-size: 0.68rem;
  cursor: pointer;
  touch-action: manipulation;
}
.upf-retry:active { background: rgba(239, 83, 80, 0.15); }

/* Dismiss (✕): the ACTION, distinct from the red status ✕ on the left — neutral grey, brightens
   on hover. Generous hit target for touch (the pill is tiny and thumb-driven). */
.upf-dismiss {
  flex-shrink: 0;
  border: none;
  background: transparent;
  color: #8a8ab0;
  font-size: 0.78rem;
  line-height: 1;
  padding: 2px 5px;
  border-radius: 4px;
  cursor: pointer;
  touch-action: manipulation;
}
.upf-dismiss:hover { color: #e6e6ee; }
.upf-dismiss:active { background: rgba(255, 255, 255, 0.12); }

.upf-error-msg {
  margin-top: 2px;
  font-size: 0.65rem;
  color: #c99;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upf-bar {
  margin-top: 5px;
  height: 3px;
  border-radius: 2px;
  background: rgba(255, 255, 255, 0.08);
  overflow: hidden;
}
.upf-bar-fill {
  height: 100%;
  background: #5b8def;
  transition: width 0.15s ease-out;
}

@keyframes upf-rotate {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>

<style scoped>
.upf-metrics { display: flex; justify-content: space-between; gap: 12px; margin-top: 5px; color: #a1a1b5; font-size: 0.66rem; line-height: 1.4; font-variant-numeric: tabular-nums; white-space: nowrap; }
.upf-metrics span:first-child { color: #c3c3d1; }
@media (prefers-reduced-motion: reduce) { .upf-spin { animation: none; } .upf-bar-fill { transition: none; } }
</style>
