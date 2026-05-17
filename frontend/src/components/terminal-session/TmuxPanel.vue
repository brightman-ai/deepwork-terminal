<template>
  <div
    class="tmux-panel"
    @touchstart.passive="onTouchStart"
    @touchend="onTouchEnd"
  >
    <!-- Page 0: Pane / Window ops (uniform 3-col grid) -->
    <div v-if="currentPage === 0" class="tp-page">
      <div class="tp-grid">
        <button class="tp-btn" @click="send('\x02z')"><span class="tp-key">^Bz</span> zoom</button>
        <button class="tp-btn" @click="send('\x02h')"><span class="tp-key">^Bh</span> left</button>
        <button class="tp-btn" @click="send('\x02k')"><span class="tp-key">^Bk</span> up</button>
        <button class="tp-btn" @click="send('\x02q')"><span class="tp-key">^Bq</span> panes</button>
        <button class="tp-btn" @click="send('\x02j')"><span class="tp-key">^Bj</span> down</button>
        <button class="tp-btn" @click="send('\x02l')"><span class="tp-key">^Bl</span> right</button>
        <button class="tp-btn tp-btn--danger" @click="send('\x03')">^C</button>
        <button class="tp-btn tp-btn--enter" @click="send('\r')">Enter</button>
        <button class="tp-btn" @click="send('\x02c')"><span class="tp-key">^Bc</span> new</button>
      </div>
      <div class="tp-row tp-row--nums">
        <button class="tp-btn tp-btn--num" v-for="n in 10" :key="n" @click="send('\x02' + String(n % 10))">{{ n % 10 }}</button>
      </div>
      <div class="tp-row tp-row--indicator">
        <div class="tp-dots">
          <span class="tp-dot tp-dot--active" />
          <span class="tp-dot" @click="currentPage = 1" />
        </div>
      </div>
    </div>

    <!-- Page 1: Copy / Session (uniform 3-col grid) -->
    <div v-else class="tp-page">
      <div class="tp-grid">
        <button class="tp-btn" @click="send('\x02[')">^B[ copy</button>
        <button class="tp-btn" @click="send('\x1b[5~')">PgUp</button>
        <button class="tp-btn" @click="send('\x1b[6~')">PgDn</button>
        <button class="tp-btn" @click="send('\x1b[H')">Home</button>
        <button class="tp-btn" @click="send('\x1b[F')">End</button>
        <button class="tp-btn tp-btn--danger" @click="send('\x03')">^C</button>
        <button class="tp-btn" @click="send('\x02,')">^B, rename</button>
        <button class="tp-btn" @click="send('\x02s')">^Bs sessions</button>
        <button class="tp-btn" @click="send('\x02d')">^Bd detach</button>
        <button class="tp-btn tp-btn--enter" @click="send('tmux attach\r')">attach</button>
        <button class="tp-btn tp-btn--enter" @click="send('\r')">Enter</button>
        <button class="tp-btn" @click="send('\x1b')">Esc</button>
      </div>
      <div class="tp-row tp-row--indicator">
        <div class="tp-dots">
          <span class="tp-dot" @click="currentPage = 0" />
          <span class="tp-dot tp-dot--active" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const emit = defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'close'): void
}>()

const currentPage = ref(0)
let touchStartX = 0

function send(key: string) {
  emit('sendKey', key)
}

function onTouchStart(ev: TouchEvent) {
  if (ev.touches.length === 1) {
    touchStartX = ev.touches[0].clientX
  }
}

function onTouchEnd(ev: TouchEvent) {
  if (ev.changedTouches.length === 0) return
  const deltaX = ev.changedTouches[0].clientX - touchStartX
  if (Math.abs(deltaX) > 50) {
    if (deltaX < 0 && currentPage.value === 0) {
      currentPage.value = 1
    } else if (deltaX > 0 && currentPage.value === 1) {
      currentPage.value = 0
    }
  }
}
</script>

<style scoped>
.tmux-panel {
  background: #1a0e28;
  height: 250px;
  display: flex;
  flex-direction: column;
  user-select: none;
  -webkit-user-select: none;
  touch-action: pan-y;
  overflow: hidden;
}

.tp-page {
  display: flex;
  flex-direction: column;
  flex: 1;
  padding: 6px 10px;
  gap: 4px;
}

.tp-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
}

.tp-row--nums {
  gap: 4px;
}

.tp-row--indicator {
  flex: 0 0 auto;
  justify-content: center;
  min-height: 18px;
}

.tp-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 4px;
  padding: 0 6px;
}

.tp-btn {
  flex: 1;
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1px;
  height: 44px;
  background: #22143a;
  color: #9b59b6;
  border: 1px solid #4a2a7a;
  border-bottom: 2px solid #2a1050;
  border-radius: 6px;
  font-size: 0.82rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  user-select: none;
  -webkit-user-select: none;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
  padding: 2px 4px;
}

.tp-btn:active {
  background: #30205a;
  transform: translateY(1px) scale(0.96);
  border-bottom-width: 1px;
}

.tp-key {
  font-size: 0.78rem;
  font-weight: 600;
  color: #c8a0e8;
  line-height: 1.2;
}

.tp-label {
  font-size: 0.6rem;
  font-weight: 400;
  color: #7a5a9a;
  line-height: 1;
  letter-spacing: 0.3px;
}

.tp-btn--num {
  flex: 0 0 auto;
  min-width: 40px;
  height: 44px;
  font-variant-numeric: tabular-nums;
  font-size: 0.9rem;
  font-weight: 600;
  color: #c8a0e8;
}

.tp-btn--enter {
  color: #60d890;
  border-color: #104828;
  background: #082018;
}
.tp-btn--enter:active { background: #0e2e20; }

.tp-btn--danger {
  color: #ff6b6b;
  border-color: #5a2030;
  border-bottom-color: #2a1020;
  background: #2a1020;
}
.tp-btn--danger:active {
  background: #3a1828;
}
.tp-btn--danger .tp-key {
  color: #ff8080;
}
.tp-btn--danger .tp-label {
  color: #994040;
}

.tp-placeholder {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 44px;
  color: #4a3060;
  font-size: 0.75rem;
  font-style: italic;
  border: 1px dashed #3a2060;
  border-radius: 6px;
}

/* Page indicator dots */
.tp-dots {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.tp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #444;
  cursor: pointer;
  transition: background 0.15s;
}

.tp-dot--active {
  background: #9b59b6;
  cursor: default;
}
</style>
