<template>
  <!-- Advisory: Claude Code is in fullscreen (alternate-screen) mode, which breaks copy/scroll.
       Offers a one-tap switch to classic (sends `/tui default` to the live session) with an
       optional "remember" that persists tui=classic to ~/.claude/settings.json. Mirrors the
       NotifyQuickSheet chrome (bottom sheet on mobile / centered card on desktop). -->
  <Teleport to="body">
    <Transition name="tms-fade">
      <div
        v-if="open"
        class="tms-scrim"
        :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
        data-testid="tui-mode-sheet"
        @click.self="$emit('close')"
      >
        <div class="tms-panel" @mousedown.prevent>
          <div class="tms-header">
            <span class="tms-title">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#f08a3c" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="3" y="4" width="18" height="14" rx="2" /><path d="M8 21h8" /><path d="M12 18v3" />
              </svg>
              复制 / 滚动失效
            </span>
            <button class="tms-close" title="晚点" data-testid="tui-mode-close" @click="$emit('close')">&times;</button>
          </div>

          <div class="tms-body">
            <p class="tms-desc">
              Claude 正运行在<b>全屏（flicker）模式</b>，内容在「备用屏」里 —— 所以 tmux 复制模式、滚动复制、网页端框选都用不了。
            </p>
            <p class="tms-desc tms-desc--muted">
              切到<b>经典模式</b>即可恢复：内容回到正常缓冲区，复制和滚动全部正常。对话不会丢失。
            </p>

            <label class="tms-remember" data-testid="tui-mode-remember">
              <input type="checkbox" v-model="persist" />
              <span>以后默认经典模式<i>（写入 ~/.claude/settings.json）</i></span>
            </label>

            <div class="tms-actions">
              <button
                class="tms-primary"
                type="button"
                data-testid="tui-mode-switch"
                :disabled="!canSwitch || busy"
                @click="$emit('switch', { persist })"
              >
                {{ busy ? '切换中…' : '切到经典模式' }}
              </button>
              <button class="tms-later" type="button" data-testid="tui-mode-later" @click="$emit('close')">
                晚点
              </button>
            </div>
            <p v-if="!canSwitch" class="tms-hint" data-testid="tui-mode-busy-hint">
              ⏳ Claude 正在运行 —— 等它空闲再切换（避免打断本轮输出）。
            </p>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'

defineProps<{
  open: boolean
  canSwitch: boolean
  busy?: boolean
}>()
defineEmits<{
  (e: 'close'): void
  (e: 'switch', payload: { persist: boolean }): void
}>()

const { isMobile } = useDeviceDetection()
const persist = ref(false)
</script>

<style scoped>
.tms-scrim {
  position: fixed;
  inset: 0;
  z-index: 2100;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
}
.tms-scrim.is-mobile { align-items: flex-end; justify-content: center; }
.tms-scrim.is-desktop { align-items: center; justify-content: center; }
.tms-panel {
  background: #141416;
  color: #e8e8ec;
  border: 1px solid #2a2a2e;
  width: 100%;
  max-width: 420px;
  box-shadow: 0 -8px 40px rgba(0, 0, 0, 0.5);
}
.is-mobile .tms-panel { border-radius: 14px 14px 0 0; padding: 18px 18px calc(18px + env(safe-area-inset-bottom)); }
.is-desktop .tms-panel { border-radius: 12px; padding: 20px; }
.tms-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.tms-title { display: inline-flex; align-items: center; gap: 7px; font-size: 0.95rem; font-weight: 600; color: #fff; }
.tms-close {
  background: transparent; border: none; color: #8a8a92; font-size: 1.4rem;
  line-height: 1; cursor: pointer; padding: 0 4px;
}
.tms-close:hover { color: #e8e8ec; }
.tms-desc { font-size: 0.86rem; line-height: 1.55; color: #c4c4cc; margin: 0 0 10px; }
.tms-desc b { color: #fff; font-weight: 600; }
.tms-desc--muted { color: #9a9aa2; }
.tms-remember {
  display: flex; align-items: flex-start; gap: 8px; margin: 14px 0 4px;
  font-size: 0.82rem; color: #c4c4cc; cursor: pointer;
}
.tms-remember input { margin-top: 2px; accent-color: #f08a3c; }
.tms-remember i { color: #6f6f78; font-style: normal; }
.tms-actions { display: flex; gap: 10px; margin-top: 16px; }
.tms-primary {
  flex: 1; padding: 11px; border-radius: 8px; border: none; cursor: pointer;
  background: #f08a3c; color: #1a1208; font-size: 0.92rem; font-weight: 600;
  transition: background 0.12s, opacity 0.12s;
}
.tms-primary:hover:not(:disabled) { background: #f59a52; }
.tms-primary:disabled { opacity: 0.45; cursor: not-allowed; }
.tms-later {
  padding: 11px 16px; border-radius: 8px; cursor: pointer;
  background: #232327; border: 1px solid #34343a; color: #c4c4cc; font-size: 0.92rem;
}
.tms-later:hover { background: #2c2c31; color: #e8e8ec; }
.tms-hint { font-size: 0.76rem; color: #8a8a92; margin: 10px 0 0; }

.tms-fade-enter-active, .tms-fade-leave-active { transition: opacity 0.18s ease; }
.tms-fade-enter-from, .tms-fade-leave-to { opacity: 0; }
</style>
