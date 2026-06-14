<template>
  <div class="hud-panel" v-if="visible">
    <div class="hud-header">
      <span class="hud-title">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#42a5f5" stroke-width="2" stroke-linecap="round">
          <circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.22 4.22l2.12 2.12M17.66 17.66l2.12 2.12M2 12h3M19 12h3"/>
        </svg>
        HUD 诊断
      </span>
      <button class="hud-close" @click="$emit('close')">&times;</button>
    </div>

    <div class="hud-grid">
      <div class="hud-row">
        <span class="hud-label">焦点</span>
        <span class="hud-value" :class="focusClass">{{ snapshot.focus }}</span>
      </div>
      <div class="hud-row">
        <span class="hud-label">键盘</span>
        <span class="hud-value">{{ snapshot.keyboard }}</span>
      </div>
      <div class="hud-row">
        <span class="hud-label">WS</span>
        <span class="hud-value" :class="wsStatusClass">{{ snapshot.ws }}</span>
      </div>
      <div class="hud-row">
        <span class="hud-label">PTY</span>
        <span class="hud-value">{{ snapshot.pty }}</span>
      </div>
      <div class="hud-row">
        <span class="hud-label">锚点</span>
        <span class="hud-value">{{ snapshot.anchor }}</span>
      </div>
      <div class="hud-row">
        <span class="hud-label">模式</span>
        <span class="hud-value">{{ snapshot.mode }}</span>
      </div>
    </div>

    <div class="hud-events">
      <div class="hud-events-header">
        <span>事件 ({{ events.length }})</span>
        <div class="hud-btns">
          <button class="hud-btn" @click="$emit('clear')">清空</button>
          <button class="hud-btn hud-btn--upload" @click="$emit('upload')">上传</button>
        </div>
      </div>
      <div class="hud-events-list">
        <div
          v-for="(evt, i) in recentEvents"
          :key="i"
          class="hud-event"
          :class="'event-' + evt.type"
        >
          <span class="event-time">{{ formatTime(evt.timestamp) }}</span>
          <span class="event-type">{{ evt.type }}</span>
          <span class="event-detail">{{ evt.detail }}</span>
        </div>
        <div v-if="events.length === 0" class="hud-empty">暂无事件</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * HudPanel — Semi-transparent diagnostic overlay showing real-time state.
 * [Ref: CAP-hud-diagnostics S2]
 */
import { computed } from 'vue'
import type { HudEvent, HudSnapshot } from '@terminal/composables/cli/useHudCollector'

const props = defineProps<{
  visible: boolean
  events: readonly HudEvent[]
  snapshot: Readonly<HudSnapshot>
}>()

defineEmits<{
  (e: 'close'): void
  (e: 'clear'): void
  (e: 'upload'): void
}>()

const recentEvents = computed(() => [...props.events].reverse().slice(0, 60))

const wsStatusClass = computed(() => ({
  'hud-ok': props.snapshot.ws === 'connected',
  'hud-warn': props.snapshot.ws === 'reconnecting' || props.snapshot.ws === 'connecting',
  'hud-error': props.snapshot.ws === 'disconnected',
}))

const focusClass = computed(() => ({
  'hud-ok': props.snapshot.focus === 'TERMINAL',
  'hud-warn': props.snapshot.focus === 'COMPOSE',
  'hud-dim': props.snapshot.focus === 'IDLE',
}))

function formatTime(ts: number): string {
  const d = new Date(ts)
  return d.toLocaleTimeString('en-US', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
}
</script>

<style scoped>
.hud-panel {
  position: fixed;
  top: 52px;
  right: 8px;
  width: 310px;
  max-height: 68vh;
  background: rgba(10, 10, 20, 0.92);
  color: #ccc;
  border: 1px solid #3a3a5a;
  border-radius: 10px;
  font-family: 'Cascadia Code', 'Fira Code', monospace;
  font-size: 0.68rem;
  z-index: 200;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0,0,0,0.6);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
}
.hud-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 7px 10px;
  background: rgba(255,255,255,0.04);
  border-bottom: 1px solid #3a3a5a;
}
.hud-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: #42a5f5;
  font-size: 0.72rem;
  letter-spacing: 0.5px;
}
.hud-close {
  background: none; border: none; color: #666; cursor: pointer;
  font-size: 1.1rem; line-height: 1; padding: 0 2px;
  transition: color 0.1s;
}
.hud-close:hover { color: #ff6060; }

.hud-grid { padding: 7px 10px 4px; }
.hud-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 2.5px 0;
  border-bottom: 1px solid rgba(255,255,255,0.04);
}
.hud-row:last-child { border-bottom: none; }
.hud-label { color: #666; font-size: 0.65rem; text-transform: uppercase; letter-spacing: 0.5px; }
.hud-value { color: #ddd; }
.hud-ok { color: #4caf50; }
.hud-warn { color: #ffb74d; }
.hud-error { color: #ef5350; }
.hud-dim { color: #666; }

.hud-events {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  border-top: 1px solid #3a3a5a;
}
.hud-events-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 5px 10px;
  color: #666;
  font-size: 0.65rem;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.hud-btns { display: flex; gap: 5px; }
.hud-btn {
  padding: 2px 7px;
  background: #252535;
  border: 1px solid #484868;
  color: #aaa;
  border-radius: 4px;
  font-size: 0.62rem;
  cursor: pointer;
  transition: background 0.1s;
}
.hud-btn:hover { background: #303048; }
.hud-btn--upload { color: #80b8ff; border-color: #3a5a88; background: #1c2d44; }
.hud-btn--upload:hover { background: #243650; }

.hud-events-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 8px 6px;
  scrollbar-width: thin;
  scrollbar-color: #3a3a5a transparent;
}
.hud-event {
  display: flex;
  gap: 5px;
  padding: 2px 0;
  border-bottom: 1px solid rgba(255,255,255,0.04);
  line-height: 1.4;
}
.hud-event:last-child { border-bottom: none; }
.event-time { color: #555; flex-shrink: 0; font-size: 0.62rem; }
.event-type { color: #42a5f5; flex-shrink: 0; min-width: 52px; }
.event-detail { color: #999; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.event-error .event-type { color: #ef5350; }
.event-error .event-detail { color: #ef9a9a; }
.event-touch .event-type { color: #ab47bc; }
.event-ws .event-type { color: #26c6da; }
.event-state .event-type { color: #ffca28; }
.hud-empty { color: #444; text-align: center; padding: 12px 0; font-style: italic; }
</style>
