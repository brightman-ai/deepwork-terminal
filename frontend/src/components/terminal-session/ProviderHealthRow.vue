<template>
  <!-- Shared per-provider health row — used by BOTH the settings section
       (NotificationsSection.vue) and the quick sheet (NotifyQuickSheet.vue) so the
       two surfaces are byte-for-byte consistent and read the SAME useNotifyConfig
       SSOT. Mirrors the top-right "健康诊断" status-light pattern: a health light +
       one-line glance, click the row to expand the recent-3 troubleshooting trail
       and the next-step hint (failed webpush → 「开启浏览器通知」; dormant →
       activationHint). Test + toggle reuse useNotifyConfig. -->
  <div class="phr" :class="{ 'is-disabled': !p.enabled }" :data-testid="`phr-${p.kind}`">
    <!-- Clickable summary line (toggles the inline expansion) -->
    <button
      class="phr-summary"
      type="button"
      :aria-expanded="expanded"
      :data-testid="`phr-summary-${p.kind}`"
      @click="expanded = !expanded"
    >
      <span class="phr-light" :title="health.label" :data-testid="`phr-light-${p.kind}`">{{ health.dot }}</span>
      <span class="phr-id">
        <span class="phr-name">{{ p.name }}</span>
        <span class="phr-glance" :class="health.cls">{{ glanceText }}</span>
      </span>
      <span class="phr-caret" :class="{ 'is-open': expanded }" aria-hidden="true">▸</span>
    </button>

    <!-- Actions: [测试] + toggle (always visible, like the original rows) -->
    <div class="phr-actions">
      <button
        class="phr-test"
        type="button"
        :disabled="busyTest || !p.enabled"
        :data-testid="`phr-test-${p.kind}`"
        @click="onTest"
      >{{ busyTest ? '…' : '测试' }}</button>
      <button
        class="phr-toggle"
        :class="{ 'is-on': p.enabled }"
        type="button"
        role="switch"
        :aria-checked="p.enabled"
        :disabled="busyToggle"
        :data-testid="`phr-toggle-${p.kind}`"
        @click="onToggle"
      >
        <span class="phr-knob" />
      </button>
    </div>

    <!-- Inline test echo (honest backend outcome) -->
    <p v-if="testText" class="phr-test-result mono" :data-testid="`phr-test-result-${p.kind}`">{{ testText }}</p>

    <!-- Expanded: recent-3 trail + troubleshooting next-step -->
    <div v-if="expanded" class="phr-detail" :data-testid="`phr-detail-${p.kind}`">
      <div v-if="p.recent.length" class="phr-recent">
        <span class="phr-recent-label">最近 {{ p.recent.length }} 次</span>
        <ul class="phr-recent-list">
          <li
            v-for="(r, i) in recentNewestFirst"
            :key="i"
            class="phr-recent-item"
            :data-testid="`phr-recent-${p.kind}-${i}`"
          >
            <span class="phr-recent-icon">{{ OUTCOME_ICON[r.outcome] }}</span>
            <span class="phr-recent-time">{{ rel(r.atMs) }}</span>
            <span v-if="r.detail" class="phr-recent-detail">{{ r.detail }}</span>
          </li>
        </ul>
      </div>
      <p v-else class="phr-recent-empty">暂无发送记录。</p>

      <!-- Troubleshooting next-step — the 排障闭环 payload -->
      <div v-if="hint" class="phr-hint" :class="`is-${hint.tone}`" :data-testid="`phr-hint-${p.kind}`">
        <span class="phr-hint-text">{{ hint.text }}</span>
        <button
          v-if="hint.action === 'install'"
          class="phr-hint-btn"
          type="button"
          :data-testid="`phr-install-${p.kind}`"
          @click="emit('install')"
        >开启浏览器通知</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useNotifyConfig, type NotifyProvider, type NotifyOutcome, type TestResult } from '@terminal/composables/cli/useNotifyConfig'
import { relativeFromMs } from '@terminal/utils/time'

const props = defineProps<{ provider: NotifyProvider }>()
const emit = defineEmits<{ (e: 'install'): void }>()

const p = computed(() => props.provider)
const notify = useNotifyConfig()

const expanded = ref(false)
const busyToggle = ref(false)
const busyTest = ref(false)
const testText = ref('')

const rel = relativeFromMs

// outcome: 0=not-configured · 1=sent · 2=dormant · 3=failed
const OUTCOME_ICON: Record<NotifyOutcome, string> = { 0: '⚪', 1: '✅', 2: '🟡', 3: '❌' }

// recent[] is newest-LAST from the backend; show newest-first for reading.
const recentNewestFirst = computed(() => p.value.recent.slice().reverse())

/** The most recent attempt (newest), or null if none recorded. */
const latest = computed(() => (p.value.recent.length ? p.value.recent[p.value.recent.length - 1] : null))

/**
 * Health light — derived from Status + recent send result (mirrors the 健康诊断 dots):
 *   ⚪ disabled / not-configured
 *   🔴 enabled but the last send FAILED, or enabled+configured but unhealthy with no hint
 *   🟡 dormant (last send dormant) OR quota nearly exhausted (used >= max-2)
 *   🟢 configured + healthy
 */
const health = computed(() => {
  const v = p.value
  if (!v.enabled) return { dot: '⚪', cls: 'is-off', label: '已关闭' }
  if (!v.configured) return { dot: '⚪', cls: 'is-off', label: '未配置' }
  if (latest.value?.outcome === 3) return { dot: '🔴', cls: 'is-bad', label: '最近发送失败' }
  const quotaLow = !!v.quota && v.quota.used >= v.quota.max - 2
  if (latest.value?.outcome === 2 || quotaLow || !v.healthy) {
    return { dot: '🟡', cls: 'is-warn', label: quotaLow ? '配额将尽' : (v.healthy ? '休眠' : '待激活') }
  }
  return { dot: '🟢', cls: 'is-ok', label: '健康' }
})

const glanceText = computed(() => {
  const v = p.value
  if (!v.enabled) return '已关闭'
  if (!v.configured) return v.activationHint || '未配置'
  const last = v.lastSuccessAtMs ? `最近成功 ${rel(v.lastSuccessAtMs)}` : '从未成功'
  return `今日 ${v.todaySent} · ${last}`
})

/**
 * Troubleshooting next-step — the 排障闭环.
 *   failed webpush (subscription dead: 410 / BadJwtToken) → 「开启浏览器通知」重订
 *   failed other channel → surface the reason
 *   dormant → the channel's activationHint (e.g. 回机器人任意字符续订)
 */
const hint = computed<{ text: string; tone: 'bad' | 'warn'; action?: 'install' } | null>(() => {
  const v = p.value
  const l = latest.value
  if (l?.outcome === 3) {
    if (v.kind === 'webpush') {
      return { text: `订阅已失效${l.detail ? `（${l.detail}）` : ''}，点「开启浏览器通知」重新订阅。`, tone: 'bad', action: 'install' }
    }
    return { text: `最近一次发送失败${l.detail ? `：${l.detail}` : '。'}`, tone: 'bad' }
  }
  if ((l?.outcome === 2 || (!v.healthy && v.configured)) && v.activationHint) {
    return { text: v.activationHint, tone: 'warn' }
  }
  return null
})

async function onToggle(): Promise<void> {
  if (busyToggle.value) return
  busyToggle.value = true
  try { await notify.setEnabled(p.value.kind, !p.value.enabled) } finally { busyToggle.value = false }
}

const TEST_TEXT: Record<TestResult, string> = {
  sent: '✅ 已发出，请留意对应渠道',
  dormant: '😴 渠道休眠未发（回机器人任意字符续订）',
  failed: '⚠️ 发送失败 / 投递未知',
  'not-configured': '未配置，无法测试',
  cooldown: '冷却中，请 8 秒后再试',
  error: '请求失败，请稍后重试',
}
async function onTest(): Promise<void> {
  if (busyTest.value) return
  busyTest.value = true
  testText.value = ''
  try {
    const res = await notify.test(p.value.kind)
    testText.value = TEST_TEXT[res] ?? res
    // A real test moved recent/last-success → reveal the trail so the user sees it.
    if (res === 'failed' || res === 'sent' || res === 'dormant') expanded.value = true
  } finally {
    busyTest.value = false
  }
}
</script>

<style scoped>
.mono { font-family: 'JetBrains Mono', 'SF Mono', ui-monospace, monospace; }

.phr {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: center;
  gap: 6px 10px;
  padding: 10px 11px;
  background: var(--phr-bg, #1a1a1d);
  border: 1px solid var(--phr-border, #252528);
  border-radius: 9px;
}
.phr.is-disabled { opacity: 0.78; }

/* Summary line — clickable, expands the detail */
.phr-summary {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  text-align: left;
  color: inherit;
  font: inherit;
}
.phr-light { font-size: 0.78rem; line-height: 1; flex-shrink: 0; }
.phr-id { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.phr-name { font-weight: 600; font-size: 0.82rem; color: var(--phr-name, #e8e8ec); }
.phr-glance { font-size: 0.66rem; line-height: 1.3; font-variant-numeric: tabular-nums; }
.phr-glance.is-ok { color: #70c88c; }
.phr-glance.is-warn { color: #d8b48a; }
.phr-glance.is-bad { color: #e08a8a; }
.phr-glance.is-off { color: var(--phr-muted, #6b6b72); }
.phr-caret {
  font-size: 0.62rem;
  color: var(--phr-muted, #6b6b72);
  flex-shrink: 0;
  transition: transform 0.15s;
}
.phr-caret.is-open { transform: rotate(90deg); }

/* Actions */
.phr-actions { display: flex; align-items: center; gap: 8px; }
.phr-test {
  padding: 5px 10px;
  border-radius: 7px;
  border: 1px solid var(--phr-btn-border, #353539);
  background: var(--phr-btn-bg, #232327);
  color: var(--phr-btn-fg, #c8c8ce);
  font-size: 0.7rem;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.phr-test:not(:disabled):active { background: var(--phr-btn-active, #2c2c31); }
.phr-test:disabled { opacity: 0.5; cursor: default; }

.phr-toggle {
  position: relative;
  flex-shrink: 0;
  width: 38px;
  height: 22px;
  border-radius: 999px;
  border: 1px solid var(--phr-btn-border, #353539);
  background: var(--phr-toggle-bg, #2a2a2f);
  cursor: pointer;
  padding: 0;
  transition: background-color 0.15s, border-color 0.15s;
}
.phr-toggle.is-on { background: #3a8a52; border-color: #3a8a52; }
.phr-toggle:disabled { opacity: 0.5; cursor: default; }
.phr-knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.35);
  transition: transform 0.15s;
}
.phr-toggle.is-on .phr-knob { transform: translateX(16px); }

.phr-test-result { grid-column: 1 / -1; color: var(--phr-muted, #9a9aa2); font-size: 0.66rem; line-height: 1.4; margin: 0; }

/* Expanded detail */
.phr-detail {
  grid-column: 1 / -1;
  margin-top: 2px;
  padding-top: 8px;
  border-top: 1px dashed var(--phr-border, #2c2c30);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.phr-recent { display: flex; flex-direction: column; gap: 5px; }
.phr-recent-label { font-size: 0.62rem; color: var(--phr-muted, #6b6b72); text-transform: uppercase; letter-spacing: 0.04em; }
.phr-recent-empty { font-size: 0.68rem; color: var(--phr-muted, #6b6b72); margin: 0; }
.phr-recent-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
.phr-recent-item { display: flex; align-items: baseline; gap: 7px; font-size: 0.68rem; line-height: 1.35; }
.phr-recent-icon { flex-shrink: 0; }
.phr-recent-time { color: var(--phr-muted, #9a9aa2); flex-shrink: 0; font-variant-numeric: tabular-nums; }
.phr-recent-detail { color: var(--phr-detail, #c8a08a); min-width: 0; word-break: break-word; }

/* Troubleshooting hint */
.phr-hint {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 0.68rem;
  line-height: 1.4;
  padding: 7px 9px;
  border-radius: 7px;
}
.phr-hint.is-bad { background: rgba(224, 138, 138, 0.1); color: #e8a0a0; border: 1px solid rgba(224, 138, 138, 0.25); }
.phr-hint.is-warn { background: rgba(216, 180, 138, 0.1); color: #d8b48a; border: 1px solid rgba(216, 180, 138, 0.22); }
.phr-hint-text { flex: 1; min-width: 0; }
.phr-hint-btn {
  flex-shrink: 0;
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid #9ac8f5;
  background: rgba(154, 200, 245, 0.12);
  color: #9ac8f5;
  font-size: 0.66rem;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.phr-hint-btn:active { background: rgba(154, 200, 245, 0.22); }
</style>
