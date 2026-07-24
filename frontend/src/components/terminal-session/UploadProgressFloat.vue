<template>
  <!-- Non-blocking upload feedback. Renders NOTHING when `entries` is empty (the
       common case: a fast paste-upload never crosses the 300ms delayed-reveal
       threshold in useUploadProgress.ts, so this component never even gets an
       entry to show) — zero space, zero flicker. A slow/large upload, or ANY
       upload error, DOES cross that threshold and this becomes a tiny corner
       pill: never covers the terminal or the compose bar. -->
  <div
    v-if="entries.length > 0"
    class="upf-root"
    :class="{ 'upf-mobile': isMobile }"
    data-testid="upload-progress-float"
  >
    <div class="upf-pill">
      <!-- Single upload: full detail (name + live % + bar, or done/error state). -->
      <template v-if="entries.length === 1">
        <div class="upf-row" :class="rowClass(entries[0])">
          <span class="upf-icon" aria-hidden="true">{{ icon(entries[0]) }}</span>
          <span class="upf-name" :title="entries[0].name">{{ entries[0].name }}</span>
          <span v-if="entries[0].status === 'uploading'" class="upf-pct">{{ pct(entries[0]) }}%</span>
          <button
            v-if="entries[0].status === 'error' && entries[0].retry"
            type="button"
            class="upf-retry"
            data-testid="upload-progress-retry"
            @click="entries[0].retry?.()"
          >重试</button>
          <!-- Every error is dismissable — a non-retryable failure (oversize, bad type) has NO
               retry, so without this it was an unclosable pill (the reported bug). The left ✕ is
               a status glyph; this right ✕ is the action, greyed to read differently. -->
          <button
            v-if="entries[0].status === 'error'"
            type="button"
            class="upf-dismiss"
            title="关闭"
            aria-label="关闭"
            data-testid="upload-progress-dismiss"
            @click="$emit('dismiss', entries[0].id)"
          >✕</button>
        </div>
        <div v-if="entries[0].status === 'uploading'" class="upf-bar">
          <div class="upf-bar-fill" :style="{ width: pct(entries[0]) + '%' }" />
        </div>
        <div v-if="entries[0].status === 'error'" class="upf-error-msg">{{ entries[0].error || '上传失败' }}</div>
      </template>

      <!-- Multiple concurrent uploads: one aggregate row + slim bar (省空间, no
           per-file clutter) — EXCEPT errors, which always get their own row +
           重试 since that failure needs a specific action, not a shared one. -->
      <template v-else>
        <div v-if="uploadingCount > 0" class="upf-row">
          <span class="upf-icon upf-spin" aria-hidden="true">⟳</span>
          <span class="upf-name">上传中 {{ uploadingCount }}/{{ entries.length }}</span>
        </div>
        <div v-else-if="doneCount > 0" class="upf-row upf-row--done">
          <span class="upf-icon" aria-hidden="true">✓</span>
          <span class="upf-name">已上传 {{ doneCount }}</span>
        </div>
        <div v-if="uploadingCount > 0" class="upf-bar">
          <div class="upf-bar-fill" :style="{ width: aggregatePct + '%' }" />
        </div>
        <div v-for="e in erroredEntries" :key="e.id" class="upf-row upf-row--error">
          <span class="upf-icon" aria-hidden="true">✕</span>
          <span class="upf-name" :title="e.name">{{ e.name }}</span>
          <button
            v-if="e.retry"
            type="button"
            class="upf-retry"
            data-testid="upload-progress-retry"
            @click="e.retry?.()"
          >重试</button>
          <button
            type="button"
            class="upf-dismiss"
            title="关闭"
            aria-label="关闭"
            data-testid="upload-progress-dismiss"
            @click="$emit('dismiss', e.id)"
          >✕</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import type { UploadEntry } from '@terminal/composables/cli/useUploadProgress'

const props = defineProps<{
  entries: UploadEntry[]
}>()
// dismiss removes ONE errored entry from the store (the float is a pure reader; the host wires
// this to useUploadProgress.remove — the single deletion authority). Only errors are dismissable:
// uploading is in-flight, done auto-dismisses.
defineEmits<{ (e: 'dismiss', id: string): void }>()

const { isMobile } = useDeviceDetection()

const uploadingCount = computed(() => props.entries.filter(e => e.status === 'uploading').length)
const doneCount = computed(() => props.entries.filter(e => e.status === 'done').length)
const erroredEntries = computed(() => props.entries.filter(e => e.status === 'error'))

const aggregatePct = computed(() => {
  const active = props.entries.filter(e => e.status === 'uploading')
  if (active.length === 0) return 0
  const sum = active.reduce((acc, e) => acc + pct(e), 0)
  return Math.round(sum / active.length)
})

function pct(entry: UploadEntry): number {
  if (entry.total <= 0) return 0
  return Math.min(100, Math.round((entry.sent / entry.total) * 100))
}

function icon(entry: UploadEntry): string {
  if (entry.status === 'done') return '✓'
  if (entry.status === 'error') return '✕'
  return '⟳'
}

function rowClass(entry: UploadEntry): string {
  return `upf-row--${entry.status}`
}
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
  max-width: 220px;
}
.upf-mobile {
  max-width: 168px;
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
