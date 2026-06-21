<template>
  <!-- Quick notification glance — a bottom sheet (mobile) / anchored popover
       (desktop) mirroring the InstallGuideSheet chrome. Lists every notify
       provider with a toggle, a one-line status glance, and a [发测试] button.
       Reads the SAME /api/notify/config SSOT as the full settings section
       (NotificationsSection.vue) via useNotifyConfig, so toggling/testing here
       and there stay in lock-step. "完整设置 →" jumps to the settings portal. -->
  <Teleport to="body">
    <Transition name="nqs-fade">
      <div
        v-if="open"
        class="nqs-scrim"
        :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
        data-testid="notify-quick-sheet"
        @click.self="$emit('close')"
      >
        <div class="nqs-panel" @mousedown.prevent>
          <div class="nqs-header">
            <span class="nqs-title">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#f08a3c" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.73 21a2 2 0 0 1-3.46 0" />
              </svg>
              通知渠道
            </span>
            <button class="nqs-close" title="关闭" data-testid="notify-quick-close" @click="$emit('close')">&times;</button>
          </div>

          <div class="nqs-body">
            <p v-if="metrics.events > 0" class="nqs-metrics" data-testid="nqs-metrics">
              已通知 {{ metrics.events }} 次<template v-if="lastNotifyText"> · 最近 {{ lastNotifyText }}</template>
            </p>
            <p v-else class="nqs-metrics nqs-metrics--empty">尚无通知记录 —— agent 等待输入时自动推送。</p>

            <div v-if="loading && providers.length === 0" class="nqs-loading">加载中…</div>

            <div class="nqs-list">
              <ProviderHealthRow
                v-for="p in providers"
                :key="p.kind"
                :provider="p"
                @install="onOpenInstall"
              />
            </div>

            <p v-if="notify.error.value" class="nqs-error">{{ notify.error.value }}</p>

            <div class="nqs-actions">
              <button class="nqs-install" type="button" data-testid="nqs-open-install" @click="onOpenInstall">
                📲 安装应用 / 开启浏览器通知
              </button>
              <button class="nqs-full" type="button" data-testid="nqs-open-settings" @click="onOpenSettings">
                完整设置 →
              </button>
            </div>
            <p class="nqs-note">微信测试会真实发送并消耗本轮配额。浏览器(Apple/Chrome)推送需先「安装应用 / 开启通知」。Webhook 渠道（飞书/钉钉/企业微信）的地址与密钥在完整设置里配置。</p>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { useNotifyConfig } from '@terminal/composables/cli/useNotifyConfig'
import { relativeFromMs } from '@terminal/utils/time'
import ProviderHealthRow from '@terminal/components/terminal-session/ProviderHealthRow.vue'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'open-install'): void }>()

const { isMobile } = useDeviceDetection()
const router = useRouter()
const notify = useNotifyConfig()
const { providers, metrics, loading } = notify

// Refresh the shared SSOT whenever the sheet opens.
watch(() => props.open, (o) => { if (o) void notify.refresh() }, { immediate: true })

const lastNotifyText = computed(() => {
  const m = metrics.value
  return m.lastAtMs ? relativeFromMs(m.lastAtMs) : ''
})

function onOpenSettings(): void {
  emit('close')
  // Deep-link straight to the Notifications section (PortalSectionHost reads ?section=),
  // so "完整设置" lands where the user expects instead of the default first section.
  void router.push({ path: '/portal/settings', query: { section: 'terminal.notifications' } })
}

function onOpenInstall(): void {
  emit('close')
  emit('open-install') // hand off to the PWA-install / browser-push-subscribe guide
}
</script>

<style scoped>
.nqs-scrim { position: fixed; inset: 0; z-index: 320; background: rgba(8, 6, 10, 0.6); display: flex; }
.nqs-scrim.is-mobile { align-items: flex-end; justify-content: stretch; }
.nqs-scrim.is-desktop { align-items: flex-start; justify-content: flex-end; }

.nqs-panel {
  display: flex;
  flex-direction: column;
  background: #141416;
  border: 1px solid #252528;
  color: #e8e8ec;
  font-size: 0.8rem;
  box-shadow: 0 -8px 44px rgba(0, 0, 0, 0.65);
  overflow: hidden;
  user-select: none;
  -webkit-user-select: none;
}
.is-mobile .nqs-panel {
  width: 100%;
  max-height: 80vh;
  border-radius: 16px 16px 0 0;
  border-bottom: none;
  padding-bottom: env(safe-area-inset-bottom, 0px);
}
.is-desktop .nqs-panel {
  width: 360px;
  max-height: 78vh;
  margin: 52px 14px 0 0;
  border-radius: 12px;
}

.mono { font-family: 'JetBrains Mono', 'SF Mono', ui-monospace, monospace; }

.nqs-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  background: rgba(240, 138, 60, 0.06);
  border-bottom: 1px solid #252528;
  flex-shrink: 0;
}
.nqs-title { display: flex; align-items: center; gap: 7px; flex: 1; font-weight: 600; font-size: 0.86rem; color: #f5c79a; }
.nqs-close { background: none; border: none; color: #6b6b72; cursor: pointer; font-size: 1.4rem; line-height: 1; padding: 0 2px; }
.nqs-close:active { color: #f08a3c; }

.nqs-body { overflow-y: auto; padding: 14px; scrollbar-width: thin; scrollbar-color: #252528 transparent; }
.nqs-metrics { color: #9a9aa2; font-size: 0.7rem; line-height: 1.5; margin: 0 0 12px; }
.nqs-metrics--empty { color: #6b6b72; }
.nqs-loading { color: #8a8a92; font-size: 0.75rem; padding: 4px 0; }

.nqs-list { display: flex; flex-direction: column; gap: 8px; }
.nqs-error { color: #e08a8a; font-size: 0.72rem; margin: 10px 0 0; }

.nqs-actions { display: flex; gap: 8px; margin-top: 14px; }
.nqs-full, .nqs-install {
  display: block;
  flex: 1;
  padding: 10px;
  border-radius: 9px;
  border: 1px solid #353539;
  background: #232327;
  font-size: 0.78rem;
  font-weight: 600;
  cursor: pointer;
}
.nqs-full { color: #f5c79a; }
.nqs-install { color: #9ac8f5; }
.nqs-full:active, .nqs-install:active { background: #2c2c31; }
.nqs-note { color: #6b6b72; font-size: 0.66rem; line-height: 1.5; margin: 10px 0 0; }

.nqs-fade-enter-active, .nqs-fade-leave-active { transition: opacity 0.18s ease; }
.nqs-fade-enter-from, .nqs-fade-leave-to { opacity: 0; }
.nqs-fade-enter-active .nqs-panel, .nqs-fade-leave-active .nqs-panel { transition: transform 0.2s ease; }
.is-mobile .nqs-fade-enter-from .nqs-panel, .is-mobile .nqs-fade-leave-to .nqs-panel { transform: translateY(18px); }
.is-desktop .nqs-fade-enter-from .nqs-panel, .is-desktop .nqs-fade-leave-to .nqs-panel { transform: translateY(-12px); }
</style>
