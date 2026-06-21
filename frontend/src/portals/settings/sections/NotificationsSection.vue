<script setup lang="ts">
/**
 * Notifications — the full per-provider config panel. Lists every notify channel
 * (微信 iLink / 浏览器 web push / 飞书 / 钉钉 / 企业微信) with: an enable toggle,
 * live status (已配置 / 健康 / 配额 used·max / 今日发送 / activationHint), a [测试]
 * button that fires a REAL test and echoes the honest backend outcome, and — for
 * the webhook IM channels — an inline url + secret form.
 *
 * Reads/mutates through the shared useNotifyConfig SSOT, so toggling/testing here
 * stays in lock-step with the NotifyQuickSheet. Auth rides cliFetch (cli-auth).
 */
import { onMounted, reactive } from 'vue'
import { Settings2 } from 'lucide-vue-next'
import {
  useNotifyConfig,
  isWebhookKind,
  type NotifyProvider,
} from '@terminal/composables/cli/useNotifyConfig'
import { usePushNotifications } from '@terminal/composables/cli/usePushNotifications'
import ProviderHealthRow from '@terminal/components/terminal-session/ProviderHealthRow.vue'
import { relativeFromMs } from '@terminal/utils/time'

const notify = useNotifyConfig()
const { providers, metrics, loading } = notify
const push = usePushNotifications()

// Per-provider transient UI: expanded webhook form state (test/toggle live in the row).
const formOpen = reactive<Record<string, boolean>>({})
const formUrl = reactive<Record<string, string>>({})
const formSecret = reactive<Record<string, string>>({})
const busySettings = reactive<Record<string, boolean>>({})
const formSaved = reactive<Record<string, boolean>>({})

onMounted(() => { void notify.refresh() })

/**
 * Troubleshooting next-step for a dead webpush subscription (410 / BadJwtToken):
 * re-request permission + re-subscribe in place, then refresh so the row's health
 * light recovers. No session binding needed here (the settings portal isn't pane-scoped).
 */
async function onInstall(): Promise<void> {
  const ok = await push.subscribe('')
  if (ok) void notify.refresh()
}

function toggleForm(p: NotifyProvider): void {
  const open = !formOpen[p.kind]
  formOpen[p.kind] = open
  if (open) {
    // Prefill the masked url so the user sees what's already set; secret is never
    // returned (write-only), so its field starts blank.
    formUrl[p.kind] = p.settings?.url || ''
    formSecret[p.kind] = ''
    formSaved[p.kind] = false
  }
}

async function onSaveSettings(p: NotifyProvider): Promise<void> {
  if (busySettings[p.kind]) return
  busySettings[p.kind] = true
  formSaved[p.kind] = false
  try {
    const ok = await notify.setSettings(p.kind, formUrl[p.kind] || '', formSecret[p.kind] || '')
    if (ok) { formSaved[p.kind] = true; formSecret[p.kind] = ''; formOpen[p.kind] = false }
  } finally {
    busySettings[p.kind] = false
  }
}
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-notifications">
    <div class="ssec-header">Notifications</div>
    <p class="ssec-hint">agent 等待输入 / 需要确认时，通过下列渠道主动提醒你。默认开启微信与浏览器两通道。</p>

    <!-- Aggregate delivery metrics (SSOT from the coordinator). -->
    <p v-if="metrics.events > 0" class="nsec-metrics" data-testid="notify-metrics">
      已通知 {{ metrics.events }} 次<template v-if="metrics.lastAtMs"> · 最近 {{ relativeFromMs(metrics.lastAtMs) }}</template>
    </p>
    <p v-else class="nsec-metrics nsec-metrics--empty" data-testid="notify-metrics-empty">尚无通知记录。</p>

    <div v-if="loading && providers.length === 0" class="ssec-loading">加载中…</div>

    <div class="nsec-list">
      <div
        v-for="p in providers"
        :key="p.kind"
        class="nsec-card"
        :data-testid="`notify-provider-${p.kind}`"
      >
        <!-- Shared health row: light + glance + recent-3 + troubleshooting + test/toggle.
             Same component the quick sheet uses, so the two surfaces stay consistent. -->
        <ProviderHealthRow :provider="p" @install="onInstall" />

        <!-- Webhook config addendum (飞书 / 钉钉 / 企业微信) — settings-only. -->
        <div v-if="isWebhookKind(p.kind)" class="nsec-webhook">
          <button
            class="nsec-btn nsec-btn--ghost"
            type="button"
            :data-testid="`notify-configure-${p.kind}`"
            @click="toggleForm(p)"
          >
            <Settings2 :size="12" /> {{ formOpen[p.kind] ? '收起配置' : '配置 Webhook' }}
          </button>
        </div>

        <!-- Webhook config form (飞书 / 钉钉 / 企业微信) -->
        <div v-if="isWebhookKind(p.kind) && formOpen[p.kind]" class="nsec-form" :data-testid="`notify-form-${p.kind}`">
          <label class="nsec-field">
            <span class="nsec-field-lbl">Webhook URL</span>
            <input
              v-model="formUrl[p.kind]"
              class="nsec-input mono"
              type="url"
              placeholder="https://…"
              autocomplete="off"
              spellcheck="false"
              :data-testid="`notify-url-${p.kind}`"
            />
          </label>
          <label class="nsec-field">
            <span class="nsec-field-lbl">Secret（加签，可选）</span>
            <input
              v-model="formSecret[p.kind]"
              class="nsec-input mono"
              type="password"
              placeholder="留空表示不修改 / 不加签"
              autocomplete="off"
              spellcheck="false"
              :data-testid="`notify-secret-${p.kind}`"
            />
          </label>
          <div class="nsec-form-actions">
            <button
              class="nsec-btn nsec-btn--accent"
              type="button"
              :disabled="busySettings[p.kind] || !formUrl[p.kind]"
              :data-testid="`notify-save-${p.kind}`"
              @click="onSaveSettings(p)"
            >{{ busySettings[p.kind] ? '保存中…' : '保存' }}</button>
            <span v-if="formSaved[p.kind]" class="nsec-saved">已保存</span>
          </div>
          <p class="nsec-field-hint">URL/Secret 加密存储；展示时已脱敏，Secret 不回显。</p>
        </div>
      </div>
    </div>

    <p v-if="notify.error.value" class="nsec-error" data-testid="notify-error">{{ notify.error.value }}</p>
  </div>
</template>

<style scoped>
/* `.ssec-*` shared chrome is loaded globally by @ce's SettingsPortal wrapper (section-ui.css). */
.mono { font-family: monospace; }

.nsec-metrics { font-size: 11px; color: hsl(var(--muted-foreground)); margin: 0 0 12px; line-height: 1.5; }
.nsec-metrics--empty { opacity: 0.8; }

.nsec-list { display: flex; flex-direction: column; gap: 10px; }
.nsec-card {
  padding: 10px 12px;
  background: hsl(var(--muted) / 0.4);
  border: 1px solid hsl(var(--border));
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* Webhook config addendum (settings-only; health/status/test/toggle live in ProviderHealthRow) */
.nsec-webhook { display: flex; gap: 6px; flex-wrap: wrap; }
.nsec-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 11px;
  font-size: 11px;
  font-weight: 500;
  border-radius: 6px;
  border: 1px solid hsl(var(--border));
  cursor: pointer;
  transition: background-color 0.1s, opacity 0.1s;
}
.nsec-btn:disabled { opacity: 0.5; cursor: default; }
.nsec-btn--ghost { background: transparent; color: hsl(var(--muted-foreground)); }
.nsec-btn--ghost:not(:disabled):hover { background: hsl(var(--muted) / 0.6); color: hsl(var(--foreground)); }
.nsec-btn--accent { background: hsl(var(--primary)); color: hsl(var(--primary-foreground)); border-color: hsl(var(--primary)); }
.nsec-btn--accent:not(:disabled):hover { opacity: 0.85; }

/* Webhook form */
.nsec-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 2px;
  padding-top: 8px;
  border-top: 1px dashed hsl(var(--border));
}
.nsec-field { display: flex; flex-direction: column; gap: 3px; }
.nsec-field-lbl { font-size: 10px; color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.05em; }
.nsec-input {
  width: 100%;
  padding: 6px 8px;
  font-size: 11px;
  border-radius: 5px;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--background));
  color: hsl(var(--foreground));
}
.nsec-input:focus { outline: none; border-color: hsl(var(--primary)); }
.nsec-form-actions { display: flex; align-items: center; gap: 10px; }
.nsec-saved { font-size: 11px; color: hsl(140 50% 42%); }
.nsec-field-hint { font-size: 10px; color: hsl(var(--muted-foreground)); margin: 0; line-height: 1.4; }

.nsec-error { font-size: 11px; color: hsl(0 65% 55%); margin: 10px 0 0; }
</style>
