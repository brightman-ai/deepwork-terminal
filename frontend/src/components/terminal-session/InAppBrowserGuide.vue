<script setup lang="ts">
/**
 * InAppBrowserGuide — full-screen overlay shown when the app is opened inside an
 * in-app webview (WeChat etc.). It points the user at the app's top-right "···"
 * menu to reopen in the system browser, and offers a copy-link fallback. WeChat
 * blocks any programmatic jump to the system browser, so guidance is the only
 * reliable option; the overlay is dismissible (respect user agency) and remembers
 * the dismissal for the session so it does not nag on every navigation.
 */
import { ref } from 'vue'
import { X } from 'lucide-vue-next'
import { useInAppBrowser } from '@terminal/composables/cli/useInAppBrowser'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const info = useInAppBrowser()
const DISMISS_KEY = 'inapp_guide_dismissed'
const dismissed = ref(sessionStorage.getItem(DISMISS_KEY) === '1')
const copied = ref(false)

const currentUrl = window.location.href

function dismiss() {
  dismissed.value = true
  sessionStorage.setItem(DISMISS_KEY, '1')
}
async function copyLink() {
  if (await copyTextToClipboard(currentUrl)) {
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}
</script>

<template>
  <div v-if="info.isInApp && !dismissed" class="inapp-overlay" role="dialog" aria-modal="true">
    <!-- Arrow pointing at the top-right "···" menu WeChat/QQ render there. -->
    <div class="inapp-arrow" aria-hidden="true">↗</div>

    <div class="inapp-card">
      <button class="inapp-close" title="仍继续（部分功能可能异常）" @click="dismiss"><X :size="18" /></button>
      <h2 class="inapp-title">请在浏览器中打开</h2>
      <p class="inapp-body">
        当前在<strong>{{ info.app }}</strong>内置浏览器中打开，<strong>通知、剪贴板</strong>等功能无法正常使用。
      </p>
      <p class="inapp-steps">
        点击右上角 <span class="inapp-dots">···</span> 菜单 → 选择<strong>「在浏览器打开」</strong>（iOS 为 Safari）。
      </p>

      <div class="inapp-fallback">
        <p class="inapp-or">或复制链接，手动在浏览器粘贴打开：</p>
        <div class="inapp-url-row">
          <code class="inapp-url">{{ currentUrl }}</code>
          <button class="inapp-copy" :class="{ done: copied }" @click="copyLink">{{ copied ? '已复制' : '复制' }}</button>
        </div>
      </div>

      <button class="inapp-dismiss" @click="dismiss">仍在此继续（部分功能可能异常）</button>
    </div>
  </div>
</template>

<style scoped>
.inapp-overlay {
  position: fixed; inset: 0; z-index: 3000;
  background: rgba(6, 8, 12, 0.92);
  display: flex; align-items: center; justify-content: center;
  padding: 24px; backdrop-filter: blur(2px);
}
.inapp-arrow {
  position: fixed; top: 8px; right: 16px;
  font-size: 40px; line-height: 1; color: #f59e0b;
  animation: nudge 1s ease-in-out infinite;
}
@keyframes nudge {
  0%, 100% { transform: translate(0, 0); opacity: 1; }
  50% { transform: translate(6px, -6px); opacity: 0.6; }
}
.inapp-card {
  position: relative; width: 100%; max-width: 360px;
  background: #16181d; border: 1px solid #2a2d35; border-radius: 14px;
  padding: 22px 20px 16px; color: #e6e8ec;
}
.inapp-close {
  position: absolute; top: 10px; right: 10px;
  background: transparent; border: none; color: #6b7280; cursor: pointer; padding: 4px;
}
.inapp-title { margin: 0 0 10px; font-size: 19px; font-weight: 700; }
.inapp-body { margin: 0 0 10px; font-size: 14px; line-height: 1.55; color: #c3c7cf; }
.inapp-steps { margin: 0 0 16px; font-size: 14px; line-height: 1.55; }
.inapp-steps strong { color: #f59e0b; }
.inapp-dots {
  display: inline-block; padding: 0 8px; border-radius: 6px;
  background: #2a2d35; font-weight: 700; letter-spacing: 1px;
}
.inapp-fallback { border-top: 1px solid #23262d; padding-top: 12px; }
.inapp-or { margin: 0 0 6px; font-size: 12px; color: #8b909a; }
.inapp-url-row { display: flex; align-items: center; gap: 8px; }
.inapp-url {
  flex: 1; font-family: monospace; font-size: 11px; word-break: break-all;
  background: #0e0f13; padding: 8px 10px; border-radius: 6px; color: #9aa0aa;
}
.inapp-copy {
  flex-shrink: 0; padding: 8px 12px; border-radius: 6px; cursor: pointer;
  background: #f59e0b; color: #16181d; border: none; font-weight: 600; font-size: 13px;
}
.inapp-copy.done { background: #22c55e; color: #06280f; }
.inapp-dismiss {
  display: block; width: 100%; margin-top: 14px; padding: 6px;
  background: transparent; border: none; color: #6b7280; font-size: 12px; cursor: pointer;
  text-decoration: underline;
}
</style>
