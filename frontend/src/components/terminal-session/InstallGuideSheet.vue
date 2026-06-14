<template>
  <!-- WS7 — foolproof, state-driven notification onboarding. One clear primary
       action per state, derived from (platform, isStandalone, permission,
       subscribed) via a single `uiState`. No wall of options: the user always
       sees exactly the next thing to do. Mobile = bottom sheet, desktop =
       anchored popover. Reactively reflects permission/subscribed/standalone
       changes (launching from the home-screen icon or returning from Settings
       auto-updates the shown state via the composable's visibility re-eval). -->
  <Teleport to="body">
    <Transition name="igs-fade">
      <div
        v-if="open"
        class="igs-scrim"
        :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
        data-testid="install-guide-sheet"
        @click.self="$emit('close')"
      >
        <div class="igs-panel" @mousedown.prevent>
          <div class="igs-header">
            <span class="igs-title">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#f08a3c" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M4 17l6-6-6-6" /><path d="M12 19h8" />
              </svg>
              {{ headline }}
            </span>
            <button class="igs-close" title="关闭" data-testid="install-guide-close" @click="$emit('close')">&times;</button>
          </div>

          <div class="igs-body">
            <p class="igs-lede">{{ lede }}</p>

            <!-- ════════ STATE: already on ═══════════════════════════════════
                 subscribed OR permission granted. One reassuring line + the
                 end-to-end test. No toggle wall — turning off is a rare path,
                 offered as a quiet secondary link. -->
            <template v-if="uiState === 'on'">
              <div class="igs-status igs-status--on" data-testid="install-guide-status-on">
                <span class="igs-status-mark">✅</span>
                <div class="igs-status-body">
                  <span class="igs-status-t">通知已开启</span>
                  <span class="igs-status-d">agent 等待输入时，你会第一时间收到系统提醒。</span>
                </div>
              </div>
              <TestRow :busy="busy" :result="testText" @test="onSendTest" />
              <button
                v-if="push.subscribed.value"
                class="igs-optional-link mono"
                data-testid="install-guide-notify-off"
                :disabled="busy"
                @click="onUnsubscribe"
              >关闭通知</button>
            </template>

            <!-- ════════ STATE: can enable now ═══════════════════════════════
                 permission default + push supported (desktop any browser,
                 Android Chrome, or iOS-standalone). ONE prominent CTA: tap →
                 request permission → subscribe. -->
            <template v-else-if="uiState === 'enable'">
              <button
                class="igs-cta"
                data-testid="install-guide-notify-on"
                :disabled="busy"
                @click="onSubscribe"
              >
                <span class="igs-cta-icon">🔔</span>
                {{ busy ? '请稍候…' : '开启通知' }}
              </button>
              <p class="igs-note">{{ enableHint }}</p>
              <p v-if="errorText" class="igs-error">{{ errorText }}</p>
              <!-- Quiet, optional install (only when a real prompt is captured). -->
              <button
                v-if="push.canPromptInstall.value && !push.installed.value"
                class="igs-optional-link mono"
                data-testid="install-guide-install"
                @click="onInstall"
              >也可安装为应用（可选，获得独立窗口）</button>
            </template>

            <!-- ════════ STATE: denied — recovery ════════════════════════════
                 permission === 'denied'. The browser will NOT let the web app
                 re-prompt, so we give honest, platform-specific MANUAL steps to
                 re-enable in settings, plus a "我已恢复，重试" re-check button. -->
            <template v-else-if="uiState === 'denied'">
              <div class="igs-status igs-status--off" data-testid="install-guide-status-denied">
                <span class="igs-status-mark">🔕</span>
                <div class="igs-status-body">
                  <span class="igs-status-t">通知已被拒绝</span>
                  <span class="igs-status-d">浏览器不允许网页代为开启，请按下面步骤在设置里恢复。</span>
                </div>
              </div>
              <div class="igs-section">
                <div class="igs-section-lbl mono">{{ recover.label }}</div>
                <ol class="igs-steps">
                  <li v-for="(s, i) in recover.steps" :key="i" class="igs-step igs-step--compact">
                    <span class="igs-step-n mono">{{ i + 1 }}</span>
                    <div class="igs-step-body"><span class="igs-step-t" v-html="s" /></div>
                  </li>
                </ol>
              </div>
              <button
                class="igs-btn igs-btn--accent igs-btn--block"
                data-testid="install-guide-retry"
                :disabled="busy"
                @click="onRetry"
              >{{ busy ? '检查中…' : '我已恢复，重试' }}</button>
              <p v-if="errorText" class="igs-error">{{ errorText }}</p>
            </template>

            <!-- ════════ STATE: iOS Safari, NOT standalone — add to home ═════
                 The genuine prerequisite for iOS Web Push. Framed as the path
                 to notifications. Launching from the icon flips to 'enable'. -->
            <template v-else-if="uiState === 'ios-install'">
              <ol class="igs-steps">
                <li class="igs-step">
                  <span class="igs-step-n mono">1</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">点击分享按钮</span>
                    <span class="igs-step-d">Safari 底部工具栏的 <IconShare /> 分享图标。</span>
                  </div>
                </li>
                <li class="igs-step">
                  <span class="igs-step-n mono">2</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">选择「添加到主屏幕」</span>
                    <span class="igs-step-d">在分享菜单中点 <IconPlusSquare /> “添加到主屏幕”。</span>
                  </div>
                </li>
                <li class="igs-step">
                  <span class="igs-step-n mono">3</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">从主屏图标打开本应用</span>
                    <span class="igs-step-d">iOS 仅对从主屏图标启动的应用授予推送，Safari 标签页不算。</span>
                  </div>
                </li>
                <li class="igs-step">
                  <span class="igs-step-n mono">4</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">回到本面板开启通知</span>
                    <span class="igs-step-d">在主屏启动的应用里再次打开本面板，即可看到“开启通知”。</span>
                  </div>
                </li>
              </ol>
              <p class="igs-note igs-note--warn" data-testid="install-guide-ios-callout">
                <b>已添加？</b>请从主屏的图标打开本应用，再回到这里开启通知 —— 当前 Safari 标签页无法检测到安装。
              </p>
            </template>

            <!-- ════════ STATE: iOS non-Safari — must switch to Safari ═══════ -->
            <template v-else-if="uiState === 'ios-safari'">
              <p class="igs-note igs-note--warn">
                当前浏览器无法添加到主屏。请用 <b>Safari</b> 打开本页面，再按下方步骤操作。
              </p>
              <ol class="igs-steps igs-steps--muted">
                <li class="igs-step igs-step--compact"><span class="igs-step-n mono">1</span><div class="igs-step-body"><span class="igs-step-t">在 Safari 中打开此链接</span></div></li>
                <li class="igs-step igs-step--compact"><span class="igs-step-n mono">2</span><div class="igs-step-body"><span class="igs-step-t">分享 → 添加到主屏幕</span></div></li>
                <li class="igs-step igs-step--compact"><span class="igs-step-n mono">3</span><div class="igs-step-body"><span class="igs-step-t">从主屏图标打开后开启通知</span></div></li>
              </ol>
            </template>

            <!-- ════════ STATE: unsupported ══════════════════════════════════ -->
            <template v-else>
              <p class="igs-note">
                当前浏览器不支持 Web Push。可在 Chrome / Edge / Firefox，或已“添加到主屏幕”的 iOS Safari 中开启。
              </p>
            </template>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, h, watch } from 'vue'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { usePushNotifications } from '@terminal/composables/cli/usePushNotifications'

const props = defineProps<{ sessionId: string; open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const { isMobile } = useDeviceDetection()
const push = usePushNotifications()

const busy = ref(false)
const errorText = ref('')
const testText = ref('')

// ── Single source of truth: one state, one primary action. ───────────────────
// Order matters — earlier branches win. Reactively reflects every input so the
// sheet updates itself when the user relaunches from the icon or returns from
// Settings (the composable re-evaluates permission/subscribed/standalone on
// visibilitychange + focus).
type UiState = 'on' | 'enable' | 'denied' | 'ios-install' | 'ios-safari' | 'unsupported'
const uiState = computed<UiState>(() => {
  // Already on — highest priority, regardless of platform.
  if (push.subscribed.value || push.permission.value === 'granted') return 'on'
  const p = push.platform.value
  // iOS in a non-Safari shell can't add to home → must switch to Safari first.
  if (p === 'ios-other' && !push.isStandalone.value) return 'ios-safari'
  // iOS Safari tab (not standalone): install is the genuine prerequisite.
  if (p === 'ios-safari' && !push.isStandalone.value) return 'ios-install'
  // Denied: browser won't re-prompt → manual recovery steps.
  if (push.permission.value === 'denied') return 'denied'
  // Can enable now: push supported + permission still askable.
  if (push.supported.value && push.permission.value === 'default') return 'enable'
  return 'unsupported'
})

const canTest = computed(() =>
  push.subscribed.value ||
  (push.permission.value === 'granted' && push.supported.value),
)

// Inline glyphs referenced from the iOS step copy (no icon dep needed).
const IconShare = () => h('svg', { width: 13, height: 13, viewBox: '0 0 24 24', fill: 'none', stroke: '#7aa0ff', 'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', style: 'vertical-align:-2px' }, [
  h('path', { d: 'M12 16V4' }), h('path', { d: 'M8 8l4-4 4 4' }), h('path', { d: 'M20 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-5' }),
])
const IconPlusSquare = () => h('svg', { width: 13, height: 13, viewBox: '0 0 24 24', fill: 'none', stroke: '#f08a3c', 'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', style: 'vertical-align:-2px' }, [
  h('rect', { x: 3, y: 3, width: 18, height: 18, rx: 3 }), h('path', { d: 'M12 8v8M8 12h8' }),
])

// Shared "发送测试通知" row — only rendered in the 'on' state.
const TestRow = (rowProps: { busy: boolean; result: string }, { emit: rowEmit }: { emit: (e: 'test') => void }) =>
  canTest.value
    ? h('div', { class: 'igs-test' }, [
        h('button', {
          class: 'igs-btn igs-btn--ghost',
          disabled: rowProps.busy,
          'data-testid': 'install-guide-test',
          onClick: () => rowEmit('test'),
        }, rowProps.busy ? '发送中…' : '发送测试通知'),
        rowProps.result ? h('p', { class: 'igs-test-result mono' }, rowProps.result) : null,
      ])
    : null
TestRow.props = ['busy', 'result']
TestRow.emits = ['test']

// Refresh permission + subscription state whenever the sheet opens.
watch(() => props.open, (o) => { if (o) { errorText.value = ''; testText.value = ''; void push.refresh() } })

const headline = computed(() => {
  switch (uiState.value) {
    case 'on': return '通知已开启'
    case 'denied': return '恢复通知权限'
    case 'ios-install':
    case 'ios-safari': return '安装与通知'
    default: return '通知设置'
  }
})

const lede = computed(() => {
  switch (uiState.value) {
    case 'on': return '一切就绪。下面可发一条测试通知确认链路。'
    case 'denied': return ''
    case 'ios-install': return 'iOS 需先把页面添加到主屏并从图标打开，才能接收 agent 推送提醒。'
    case 'ios-safari': return '需要在 Safari 中打开才能添加到主屏并接收通知。'
    case 'unsupported': return ''
    default:
      return push.isStandalone.value
        ? '已作为应用运行。开启通知，让 agent 等待输入时主动提醒你。'
        : '开启通知 —— agent 需要确认时第一时间收到提醒。'
  }
})

// Sub-line under the 'enable' CTA: platform-honest one-liner.
const enableHint = computed(() => {
  if (push.isStandalone.value) return '点上方按钮，系统会弹出授权框，点“允许”即可。'
  if (push.platform.value === 'chromium' || push.platform.value === 'desktop-firefox') {
    return '无需安装即可直接开启；点上方按钮后在浏览器授权框点“允许”。'
  }
  if (push.platform.value === 'desktop-safari') {
    return '无需安装即可直接开启；也可在 Safari 菜单将本站“添加到程序坞”（可选）。'
  }
  return '点上方按钮，在系统授权框点“允许”即可。'
})

// ── Denied recovery: real UI labels, per platform. Short, numbered, honest. ──
const recover = computed<{ label: string; steps: string[] }>(() => {
  const p = push.platform.value
  if (push.isStandalone.value || p === 'ios-safari' || p === 'ios-other') {
    // iOS standalone PWA (or any iOS path where the app icon exists).
    return {
      label: 'iOS · 在「设置」中恢复',
      steps: [
        '打开 iOS <b>设置</b>',
        '向下找到 <b>「Deepwork」</b>',
        '进入 <b>通知</b>',
        '打开 <b>「允许通知」</b>，再回来点下方“重试”',
      ],
    }
  }
  if (p === 'chromium') {
    return {
      label: 'Chrome / Edge · 在地址栏恢复',
      steps: [
        '点地址栏最左的 <b>🔒 / ⓘ</b> 图标',
        '找到 <b>通知</b> 一项',
        '改为 <b>允许</b>（或点“重置权限”后刷新重试）',
        '刷新页面，再回来点下方“重试”',
      ],
    }
  }
  if (p === 'desktop-firefox') {
    return {
      label: 'Firefox · 清除拦截',
      steps: [
        '点地址栏的 <b>🔒</b> 图标',
        '清除对 <b>通知</b> 的拦截',
        '刷新页面，再回来点下方“重试”',
      ],
    }
  }
  if (p === 'desktop-safari') {
    return {
      label: 'Safari · 在设置中恢复',
      steps: [
        '打开 <b>Safari → 设置</b>',
        '进入 <b>网站 → 通知</b>',
        '把本站设为 <b>「允许」</b>',
        '刷新页面，再回来点下方“重试”',
      ],
    }
  }
  return {
    label: '在浏览器设置中恢复',
    steps: [
      '打开浏览器的 <b>站点权限 / 通知</b> 设置',
      '把本站的通知改为 <b>允许</b>',
      '刷新页面，再回来点下方“重试”',
    ],
  }
})

async function onSubscribe(): Promise<void> {
  busy.value = true
  errorText.value = ''
  try {
    const ok = await push.subscribe(props.sessionId)
    if (!ok) errorText.value = subscribeErrorText()
  } finally {
    busy.value = false
  }
}

// Map the composable's lastError into a concrete, actionable hint — never a bare "失败".
function subscribeErrorText(): string {
  switch (push.lastError.value) {
    case 'denied':
      return '系统已拒绝通知权限，请按上面的步骤在设置中恢复后重试。'
    case 'ios-reopen':
      return '开启未成功：请完全关闭本应用（上滑移除）后，从主屏图标重新打开，再点“开启通知”重试。'
    default:
      return '开启通知失败，请稍后重试。'
  }
}

async function onUnsubscribe(): Promise<void> {
  busy.value = true
  errorText.value = ''
  try { await push.unsubscribe() } finally { busy.value = false }
}

// "我已恢复，重试" — re-check permission, then attempt to subscribe if now askable.
async function onRetry(): Promise<void> {
  busy.value = true
  errorText.value = ''
  try {
    await push.refresh()
    if (push.permission.value === 'denied') {
      errorText.value = '仍是“已拒绝”状态。请确认已在设置里把本站/本应用的通知改为“允许”，并刷新页面后再试。'
      return
    }
    // Permission now default/granted → drive straight through to a subscription.
    const ok = await push.subscribe(props.sessionId)
    if (!ok) errorText.value = subscribeErrorText()
  } finally {
    busy.value = false
  }
}

async function onInstall(): Promise<void> {
  const outcome = await push.promptInstall()
  if (outcome === 'unavailable') {
    errorText.value = '安装提示不可用（可能已安装，或浏览器未触发）。'
  }
}

async function onSendTest(): Promise<void> {
  busy.value = true
  testText.value = ''
  try {
    const r = await push.sendTest()
    testText.value = r === 'sent'
      ? '已发送测试推送，请留意系统通知。'
      : r === 'local'
        ? '已触发本地测试通知（此设备为前台通知，未走后台推送）。'
        : '测试发送失败，请确认已开启通知后重试。'
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.igs-scrim {
  position: fixed;
  inset: 0;
  z-index: 320;
  background: rgba(8, 6, 10, 0.6);
  display: flex;
}
.igs-scrim.is-mobile { align-items: flex-end; justify-content: stretch; }
.igs-scrim.is-desktop { align-items: flex-start; justify-content: flex-end; }

.igs-panel {
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
.is-mobile .igs-panel {
  width: 100%;
  max-height: 80vh;
  border-radius: 16px 16px 0 0;
  border-bottom: none;
  padding-bottom: env(safe-area-inset-bottom, 0px);
}
.is-desktop .igs-panel {
  width: 380px;
  max-height: 78vh;
  margin: 52px 14px 0 0;
  border-radius: 12px;
}

.mono { font-family: 'JetBrains Mono', 'SF Mono', ui-monospace, monospace; }

.igs-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  background: rgba(240, 138, 60, 0.06);
  border-bottom: 1px solid #252528;
  flex-shrink: 0;
}
.igs-title {
  display: flex;
  align-items: center;
  gap: 7px;
  flex: 1;
  font-weight: 600;
  font-size: 0.86rem;
  letter-spacing: 0.2px;
  color: #f5c79a;
}
.igs-close {
  background: none; border: none; color: #6b6b72; cursor: pointer;
  font-size: 1.4rem; line-height: 1; padding: 0 2px;
}
.igs-close:active { color: #f08a3c; }

.igs-body {
  overflow-y: auto;
  padding: 14px;
  scrollbar-width: thin;
  scrollbar-color: #252528 transparent;
}
.igs-lede { color: #a8a8b0; line-height: 1.6; margin-bottom: 14px; }
.igs-lede:empty { display: none; }

/* Sections */
.igs-section { margin-top: 4px; }
.igs-section-lbl {
  font-size: 0.62rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: #6b6b72;
  margin-bottom: 8px;
}

/* Status banner (on / denied) */
.igs-status {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 13px;
  border-radius: 10px;
  border: 1px solid #252528;
  background: #1c1c1f;
}
.igs-status--on { border-color: #2f5a3a; background: #16221a; }
.igs-status--off { border-color: #4a2828; background: #221616; }
.igs-status-mark { font-size: 1.3rem; line-height: 1; flex-shrink: 0; }
.igs-status-body { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.igs-status-t { font-weight: 600; font-size: 0.86rem; color: #e8e8ec; }
.igs-status--on .igs-status-t { color: #70c88c; }
.igs-status--off .igs-status-t { color: #e08a8a; }
.igs-status-d { color: #9a9aa2; font-size: 0.72rem; line-height: 1.45; }

/* Primary CTA — the single foolproof "开启通知" action. */
.igs-cta {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 9px;
  padding: 14px;
  border: none;
  border-radius: 11px;
  background: #f08a3c;
  color: #0d0d0f;
  font-size: 0.95rem;
  font-weight: 700;
  letter-spacing: 0.3px;
  cursor: pointer;
  transition: background 0.1s, opacity 0.1s;
}
.igs-cta:not(:disabled):active { background: #d8772b; }
.igs-cta:disabled { opacity: 0.55; cursor: default; }
.igs-cta-icon { font-size: 1.05rem; line-height: 1; }

/* Steps */
.igs-steps { list-style: none; display: flex; flex-direction: column; gap: 10px; }
.igs-steps--muted { opacity: 0.85; }
.igs-step {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 11px;
  background: #1c1c1f;
  border: 1px solid #252528;
  border-radius: 10px;
}
.igs-step--compact { padding: 9px 11px; }
.igs-step-n {
  flex-shrink: 0;
  width: 22px; height: 22px;
  display: grid; place-items: center;
  border-radius: 6px;
  background: rgba(240, 138, 60, 0.14);
  color: #f08a3c;
  font-size: 0.72rem;
  font-weight: 700;
}
.igs-step-body { display: flex; flex-direction: column; gap: 2px; flex: 1; min-width: 0; }
.igs-step-t { color: #e8e8ec; font-weight: 600; font-size: 0.8rem; }
.igs-step-t :deep(b) { color: #f5c79a; font-weight: 700; }
.igs-step-d { color: #8a8a92; font-size: 0.72rem; line-height: 1.45; }

/* Buttons */
.igs-btn {
  flex-shrink: 0;
  padding: 7px 14px;
  border-radius: 8px;
  font-size: 0.76rem;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid transparent;
  transition: background 0.1s, border-color 0.1s, opacity 0.1s;
}
.igs-btn:disabled { opacity: 0.5; cursor: default; }
.igs-btn--accent { background: #f08a3c; color: #0d0d0f; }
.igs-btn--accent:not(:disabled):active { background: #d8772b; }
.igs-btn--ghost { background: #232327; color: #c8c8ce; border-color: #353539; }
.igs-btn--ghost:not(:disabled):active { background: #2c2c31; }
.igs-btn--block { width: 100%; margin-top: 14px; padding: 11px; font-size: 0.82rem; }

/* Optional secondary action — deliberately subtle so it never competes with the
   primary action. */
.igs-optional-link {
  display: inline-block;
  margin-top: 12px;
  padding: 0;
  background: none;
  border: none;
  color: #7a7a82;
  font-size: 0.68rem;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}
.igs-optional-link:active { color: #f08a3c; }
.igs-optional-link:disabled { opacity: 0.5; cursor: default; }

/* Notes & errors */
.igs-note {
  margin-top: 12px;
  padding: 9px 11px;
  background: #1a1a1d;
  border: 1px solid #252528;
  border-radius: 8px;
  color: #8a8a92;
  font-size: 0.68rem;
  line-height: 1.5;
}
.igs-note--warn { background: #251a14; border-color: #4a3320; color: #e0b08a; }
.igs-note--warn b { color: #f5c79a; }
.igs-error {
  margin-top: 10px;
  color: #e08a8a;
  font-size: 0.72rem;
  line-height: 1.5;
}

/* End-to-end test row */
.igs-test {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px dashed #2c2c31;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
}
.igs-test-result { color: #8a8a92; font-size: 0.68rem; line-height: 1.5; }

/* Sheet enter/leave */
.igs-fade-enter-active, .igs-fade-leave-active { transition: opacity 0.18s ease; }
.igs-fade-enter-from, .igs-fade-leave-to { opacity: 0; }
.igs-fade-enter-active .igs-panel, .igs-fade-leave-active .igs-panel { transition: transform 0.2s ease; }
.is-mobile .igs-fade-enter-from .igs-panel, .is-mobile .igs-fade-leave-to .igs-panel { transform: translateY(18px); }
.is-desktop .igs-fade-enter-from .igs-panel, .is-desktop .igs-fade-leave-to .igs-panel { transform: translateY(-12px); }
</style>
