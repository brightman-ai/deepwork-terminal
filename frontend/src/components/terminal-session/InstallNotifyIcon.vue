<template>
  <!-- WS7 primary entry — lives in the terminal title/tab row, always visible.
       Platform-aware glyph (install-arrow when not yet a PWA, bell once standalone);
       a subtle amber dot nudges when notifications are not yet enabled. Opens the
       shared InstallGuideSheet. -->
  <button
    class="ini-btn"
    :class="{ 'ini-nudge': showNudge }"
    type="button"
    :title="title"
    :aria-label="title"
    data-testid="install-notify-icon"
    @click="$emit('open')"
    @pointerup.stop
    @touchend.stop
  >
    <!-- standalone → bell (notifications), else → install/download glyph -->
    <svg v-if="push.isStandalone.value" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.73 21a2 2 0 0 1-3.46 0" />
    </svg>
    <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M12 3v12" /><path d="M8 11l4 4 4-4" /><path d="M4 19h16" />
    </svg>
    <span v-if="showNudge" class="ini-dot" data-testid="install-notify-dot" />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { usePushNotifications } from '@terminal/composables/cli/usePushNotifications'

defineEmits<{ (e: 'open'): void }>()
const push = usePushNotifications()

// Nudge whenever notifications could be on but aren't yet — installable-but-not-installed,
// or installed-but-not-subscribed. Stays silent when fully set up or hard-denied.
const showNudge = computed(() => {
  if (push.permission.value === 'denied') return false
  if (push.subscribed.value) return false
  if (push.isStandalone.value) return push.supported.value
  return true // not installed → always worth a nudge
})

const title = computed(() => {
  if (push.isStandalone.value) return push.subscribed.value ? '通知已开启' : '开启通知'
  return '安装应用 / 开启通知'
})
</script>

<style scoped>
.ini-btn {
  position: relative;
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 6px;
  background: transparent;
  border: 1px solid transparent;
  color: #8a8a92;
  cursor: pointer;
  flex-shrink: 0;
  touch-action: manipulation;
  transition: color 0.1s, background 0.1s;
}
.ini-btn:hover { color: #e8e8ec; background: rgba(255, 255, 255, 0.06); }
.ini-btn:active { transform: scale(0.94); }
.ini-btn.ini-nudge { color: #f08a3c; }

.ini-dot {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #f08a3c;
  box-shadow: 0 0 0 2px #141416;
  animation: ini-pulse 2s infinite;
}
@keyframes ini-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.45; } }
</style>
