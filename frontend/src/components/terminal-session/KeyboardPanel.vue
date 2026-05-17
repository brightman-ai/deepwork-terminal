<template>
  <div
    class="keyboard-panel"
    @touchstart="onTouchStart"
    @touchend="onTouchEnd"
  >
    <div class="kp-close-row">
      <button class="kp-close-btn" @click="$emit('close')" title="Close keyboard panel">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="6,9 12,15 18,9"/>
        </svg>
      </button>
    </div>

    <div class="kp-pages-wrapper">
      <div
        class="kp-pages-track"
        :style="{ transform: `translateX(-${currentPage * 25}%)` }"
      >
        <!-- Page 0: Navigation -->
        <div class="kp-page kp-grid kp-grid--4x4">
          <button class="kp-btn" @click="send('\x1b[H')">HOME</button>
          <button class="kp-btn" @click="send('\x1b[F')">END</button>
          <button class="kp-btn" @click="send('\x1b[5~')">PgUp</button>
          <button class="kp-btn" @click="send('\x7f')">Bksp</button>

          <button class="kp-btn" @click="send('\x1b[2~')">Ins</button>
          <button class="kp-btn kp-btn--nav" @click="send('\x1b[A')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="18,15 12,9 6,15"/></svg>
          </button>
          <button class="kp-btn" @click="send('\x1b[6~')">PgDn</button>
          <button class="kp-btn" @click="send('\x1b[3~')">Del</button>

          <button class="kp-btn kp-btn--nav" @click="send('\x1b[D')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="15,18 9,12 15,6"/></svg>
          </button>
          <button class="kp-btn kp-btn--nav" @click="send('\x1b[B')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="6,9 12,15 18,9"/></svg>
          </button>
          <button class="kp-btn kp-btn--nav" @click="send('\x1b[C')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="9,18 15,12 9,6"/></svg>
          </button>
          <button class="kp-btn kp-btn--enter" @click="send('\r')">Enter</button>
        </div>

        <!-- Page 1: Numpad -->
        <div class="kp-page kp-grid kp-grid--5x4">
          <button class="kp-btn" @click="send('7')">7</button>
          <button class="kp-btn" @click="send('8')">8</button>
          <button class="kp-btn" @click="send('9')">9</button>
          <button class="kp-btn kp-btn--op" @click="send('-')">-</button>

          <button class="kp-btn" @click="send('4')">4</button>
          <button class="kp-btn" @click="send('5')">5</button>
          <button class="kp-btn" @click="send('6')">6</button>
          <button class="kp-btn kp-btn--op" @click="send('*')">*</button>

          <button class="kp-btn" @click="send('1')">1</button>
          <button class="kp-btn" @click="send('2')">2</button>
          <button class="kp-btn" @click="send('3')">3</button>
          <button class="kp-btn kp-btn--op" @click="send('/')">/</button>

          <button class="kp-btn" @click="send('0')">0</button>
          <button class="kp-btn" @click="send('.')">.</button>
          <button class="kp-btn" @click="send('\x7f')">Bksp</button>
          <button class="kp-btn kp-btn--op" @click="send('+')">+</button>
        </div>

        <!-- Page 2: Clipboard / Edit (browser API, not terminal ctrl chars) -->
        <div class="kp-page kp-grid kp-grid--clip">
          <button class="kp-btn kp-btn--wide" @click="$emit('clipboard', 'cut')">Cut</button>
          <button class="kp-btn kp-btn--wide" @click="$emit('clipboard', 'copy')">Copy</button>
          <button class="kp-btn kp-btn--wide" @click="$emit('clipboard', 'paste')">Paste</button>

          <button class="kp-btn kp-btn--wide" @click="$emit('clipboard', 'undo')">Undo</button>
          <button class="kp-btn kp-btn--wide" @click="$emit('clipboard', 'selectAll')">SelAll</button>
          <button class="kp-btn kp-btn--wide" @click="$emit('clipboard', 'find')">Find</button>

          <button class="kp-btn kp-btn--wide kp-btn--ctrl" @click="send('\x1a')">^Z</button>
          <button class="kp-btn kp-btn--wide kp-btn--ctrl" @click="send('\x01')">^A</button>
          <button class="kp-btn kp-btn--wide kp-btn--ctrl" @click="send('\x05')">^E</button>
        </div>

        <!-- Page 3: Function Keys -->
        <div class="kp-page kp-grid kp-grid--4x4">
          <button class="kp-btn kp-btn--fn" @click="send('\x1bOP')">F1</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1bOQ')">F2</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1bOR')">F3</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1bOS')">F4</button>

          <button class="kp-btn kp-btn--fn" @click="send('\x1b[15~')">F5</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1b[17~')">F6</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1b[18~')">F7</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1b[19~')">F8</button>

          <button class="kp-btn kp-btn--fn" @click="send('\x1b[20~')">F9</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1b[21~')">F10</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1b[23~')">F11</button>
          <button class="kp-btn kp-btn--fn" @click="send('\x1b[24~')">F12</button>
        </div>
      </div>
    </div>

    <!-- Page indicator dots -->
    <div class="kp-dots">
      <span
        v-for="i in 4"
        :key="i"
        class="kp-dot"
        :class="{ 'kp-dot--active': currentPage === i - 1 }"
        @click="currentPage = i - 1"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const emit = defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'clipboard', op: 'copy' | 'paste' | 'cut' | 'undo' | 'selectAll' | 'find'): void
  (e: 'close'): void
}>()

const currentPage = ref(0)
const touchStartX = ref(0)

function send(key: string) {
  emit('sendKey', key)
}

function onTouchStart(ev: TouchEvent) {
  if (ev.touches.length === 1) {
    touchStartX.value = ev.touches[0].clientX
  }
}

function onTouchEnd(ev: TouchEvent) {
  if (ev.changedTouches.length === 0) return
  const deltaX = ev.changedTouches[0].clientX - touchStartX.value
  if (Math.abs(deltaX) > 50) {
    if (deltaX < 0 && currentPage.value < 3) {
      currentPage.value++
    } else if (deltaX > 0 && currentPage.value > 0) {
      currentPage.value--
    }
  }
}
</script>

<style scoped>
.keyboard-panel {
  --kp-bg: #1a1a2e;
  --kp-btn-bg: #2c2c2e;
  --kp-btn-border: #3a3a3c;
  --kp-btn-depth: #111;
  --kp-btn-active: #3a3a3c;
  --kp-btn-color: #e8e8ea;

  background: var(--kp-bg);
  border-top: 1px solid var(--kp-btn-border);
  overflow: hidden;
  touch-action: pan-y;
  user-select: none;
  -webkit-user-select: none;
}

.kp-close-row {
  display: flex;
  justify-content: center;
  padding: 2px 0;
}

.kp-close-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 20px;
  background: transparent;
  border: none;
  color: #666;
  cursor: pointer;
  touch-action: manipulation;
}
.kp-close-btn:active {
  color: #999;
}

.kp-pages-wrapper {
  overflow: hidden;
  width: 100%;
}

.kp-pages-track {
  display: flex;
  width: 400%;
  transition: transform 0.25s ease;
}

.kp-page {
  width: 25%;
  flex-shrink: 0;
  padding: 0 6px;
  box-sizing: border-box;
}

/* Grid layouts */
.kp-grid {
  display: grid;
  gap: 4px;
}

.kp-grid--4x4 {
  grid-template-columns: repeat(4, 1fr);
}

.kp-grid--5x4 {
  grid-template-columns: repeat(4, 1fr);
}

.kp-grid--clip {
  grid-template-columns: repeat(3, 1fr);
}

/* Button base */
.kp-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 44px;
  background: var(--kp-btn-bg);
  color: var(--kp-btn-color);
  border: 1px solid var(--kp-btn-border);
  border-bottom: 2px solid var(--kp-btn-depth);
  border-radius: 6px;
  font-size: 0.8rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  user-select: none;
  -webkit-user-select: none;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
}

.kp-btn:active {
  background: var(--kp-btn-active);
  transform: translateY(1px) scale(0.96);
  border-bottom-width: 1px;
}

/* Navigation arrow keys */
.kp-btn--nav {
  color: #8ec5ff;
  border-color: #2a4a6a;
  border-bottom-color: #111;
  background: #1a2d44;
}
.kp-btn--nav:active {
  background: #1e3550;
}

/* Enter key */
.kp-btn--enter {
  color: #60d890;
  border-color: #104828;
  border-bottom-color: #111;
  background: #082018;
}
.kp-btn--enter:active {
  background: #0e2e20;
}

/* Numpad operators */
.kp-btn--op {
  color: #ffd080;
  border-color: #4a3a10;
  border-bottom-color: #111;
  background: #221a08;
}
.kp-btn--op:active {
  background: #2e2410;
}

/* Clipboard wide buttons */
.kp-btn--wide {
  height: 48px;
  font-size: 0.82rem;
}

/* Ctrl combo keys */
.kp-btn--ctrl {
  color: #80e8e8;
  border-color: #1a4a4a;
  border-bottom-color: #111;
  background: #0a2020;
}
.kp-btn--ctrl:active {
  background: #102c2c;
}

/* Function keys */
.kp-btn--fn {
  color: #c0a0ff;
  border-color: #3a2a5a;
  border-bottom-color: #111;
  background: #1a1030;
}
.kp-btn--fn:active {
  background: #221840;
}

/* Page indicator dots */
.kp-dots {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 8px;
  padding: 6px 0 8px;
}

.kp-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #444;
  cursor: pointer;
  transition: background 0.2s;
}

.kp-dot--active {
  background: #42a5f5;
}
</style>
