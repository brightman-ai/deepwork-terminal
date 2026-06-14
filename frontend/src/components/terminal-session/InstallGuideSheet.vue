<template>
  <!-- WS7 — platform-aware install + notification guide. Same sheet for both entry
       icons (top title-bar app icon + tmux-bar bell). Mobile = bottom sheet,
       desktop = anchored popover. Branches on usePushNotifications().platform /
       isStandalone so each platform sees only the steps it can actually act on. -->
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

            <!-- ── Already standalone: notification toggle only ─────────────── -->
            <template v-if="push.isStandalone.value">
              <div class="igs-section">
                <div class="igs-section-lbl mono">通知</div>
                <div class="igs-toggle-row" data-testid="install-guide-notify-toggle">
                  <div class="igs-toggle-meta">
                    <span class="igs-toggle-state" :class="notifyStateClass">{{ notifyStateText }}</span>
                    <span class="igs-toggle-hint mono">{{ permissionHint }}</span>
                  </div>
                  <button
                    v-if="!push.supported.value"
                    class="igs-btn igs-btn--ghost"
                    disabled
                  >不支持</button>
                  <button
                    v-else-if="push.permission.value === 'denied'"
                    class="igs-btn igs-btn--ghost"
                    disabled
                    data-testid="install-guide-notify-denied"
                  >已被系统拒绝</button>
                  <button
                    v-else-if="push.subscribed.value"
                    class="igs-btn igs-btn--ghost"
                    :disabled="busy"
                    data-testid="install-guide-notify-off"
                    @click="onUnsubscribe"
                  >关闭通知</button>
                  <button
                    v-else
                    class="igs-btn igs-btn--accent"
                    :disabled="busy"
                    data-testid="install-guide-notify-on"
                    @click="onSubscribe"
                  >{{ busy ? '请稍候…' : '开启通知' }}</button>
                </div>
                <p v-if="errorText" class="igs-error">{{ errorText }}</p>
                <TestRow
                  v-if="canTest"
                  :busy="busy"
                  :result="testText"
                  @test="onSendTest"
                />
              </div>
            </template>

            <!-- ── Chromium / Android: native install prompt, then notify ───── -->
            <template v-else-if="push.platform.value === 'chromium'">
              <ol class="igs-steps">
                <li class="igs-step" :class="{ 'is-done': push.installed.value }">
                  <span class="igs-step-n mono">1</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">安装应用</span>
                    <span class="igs-step-d">添加到主屏 / 应用列表，获得独立窗口与后台推送。</span>
                  </div>
                  <button
                    v-if="push.canPromptInstall.value && !push.installed.value"
                    class="igs-btn igs-btn--accent"
                    data-testid="install-guide-install"
                    @click="onInstall"
                  >安装应用</button>
                  <span v-else-if="push.installed.value" class="igs-step-ok mono">已安装</span>
                  <span v-else class="igs-step-na mono">已安装或不可用</span>
                </li>
                <li class="igs-step">
                  <span class="igs-step-n mono">2</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">开启通知</span>
                    <span class="igs-step-d">agent 等待输入时推送提醒，无需盯着屏幕。</span>
                  </div>
                  <button
                    v-if="push.subscribed.value"
                    class="igs-btn igs-btn--ghost"
                    :disabled="busy"
                    @click="onUnsubscribe"
                  >关闭</button>
                  <button
                    v-else
                    class="igs-btn igs-btn--accent"
                    :disabled="busy || push.permission.value === 'denied'"
                    data-testid="install-guide-notify-on"
                    @click="onSubscribe"
                  >{{ push.permission.value === 'denied' ? '已被拒绝' : (busy ? '请稍候…' : '开启通知') }}</button>
                </li>
              </ol>
              <p v-if="errorText" class="igs-error">{{ errorText }}</p>
              <TestRow
                v-if="canTest"
                :busy="busy"
                :result="testText"
                @test="onSendTest"
              />
            </template>

            <!-- ── iOS Safari: illustrated Add-to-Home-Screen steps ─────────── -->
            <template v-else-if="push.platform.value === 'ios-safari'">
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
                    <span class="igs-step-t">添加到主屏幕</span>
                    <span class="igs-step-d">在分享菜单中选择 <IconPlusSquare /> “添加到主屏幕”。</span>
                  </div>
                </li>
                <li class="igs-step">
                  <span class="igs-step-n mono">3</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">从主屏图标打开本应用</span>
                    <span class="igs-step-d">iOS 仅对已安装的应用授予推送，且只有从主屏图标启动的窗口才算“已安装”。</span>
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
                <b>已添加？</b>请从主屏的图标打开本应用，再回到这里开启通知 —— 当前的 Safari 标签页无法检测到安装。
              </p>
              <p class="igs-note mono">
                iOS 16.4+ 支持 Web Push，但只在“添加到主屏幕”后生效。首次从主屏启动时存储是隔离的，可能需要重新输入一次授权码。
              </p>
            </template>

            <!-- ── iOS non-Safari: must switch to Safari ────────────────────── -->
            <template v-else-if="push.platform.value === 'ios-other'">
              <p class="igs-note igs-note--warn">
                当前浏览器无法安装为应用。请用 <b>Safari</b> 打开本页面，再按下方步骤添加到主屏幕。
              </p>
              <ol class="igs-steps igs-steps--muted">
                <li class="igs-step"><span class="igs-step-n mono">1</span><div class="igs-step-body"><span class="igs-step-t">在 Safari 中打开此链接</span></div></li>
                <li class="igs-step"><span class="igs-step-n mono">2</span><div class="igs-step-body"><span class="igs-step-t">分享 → 添加到主屏幕</span></div></li>
                <li class="igs-step"><span class="igs-step-n mono">3</span><div class="igs-step-body"><span class="igs-step-t">从主屏打开后开启通知</span></div></li>
              </ol>
            </template>

            <!-- ── Desktop Safari / Firefox / other ─────────────────────────── -->
            <!-- Desktop = no install required: Chrome/Edge/Firefox + Safari 16+ do
                 Web Push in a normal tab. "开启通知" is the primary, front-and-center
                 action; install/add-to-dock is not gated in front of it. -->
            <template v-else>
              <div class="igs-section">
                <div class="igs-toggle-row" data-testid="install-guide-notify-toggle">
                  <div class="igs-toggle-meta">
                    <span class="igs-toggle-state" :class="notifyStateClass">{{ notifyStateText }}</span>
                    <span class="igs-toggle-hint mono">{{ permissionHint }}</span>
                  </div>
                  <button
                    v-if="!push.supported.value"
                    class="igs-btn igs-btn--ghost"
                    disabled
                  >不支持</button>
                  <button
                    v-else-if="push.subscribed.value"
                    class="igs-btn igs-btn--ghost"
                    :disabled="busy"
                    data-testid="install-guide-notify-off"
                    @click="onUnsubscribe"
                  >关闭通知</button>
                  <button
                    v-else
                    class="igs-btn igs-btn--accent"
                    :disabled="busy || push.permission.value === 'denied'"
                    data-testid="install-guide-notify-on"
                    @click="onSubscribe"
                  >{{ push.permission.value === 'denied' ? '已被拒绝' : (busy ? '请稍候…' : '开启通知') }}</button>
                </div>
                <p class="igs-note" v-if="push.supported.value">
                  无需安装即可直接开启通知；安装为应用可获得独立窗口（可选）。
                </p>
                <p class="igs-note" v-else>
                  当前浏览器不支持 Web Push。可在 Chrome / Edge / Firefox，或已“添加到主屏幕”的 iOS Safari 中开启。
                </p>
                <p v-if="errorText" class="igs-error">{{ errorText }}</p>
                <TestRow
                  v-if="canTest"
                  :busy="busy"
                  :result="testText"
                  @test="onSendTest"
                />
              </div>
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

// The end-to-end test is offered the moment a test could actually land: either a
// real subscription (→ backend /push/test → SW → OS) or, as a foreground-only
// fallback, granted permission (→ local Notification). Single button, single flow.
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

// Shared "发送测试通知" row — appears in every branch where a test can land
// (standalone / chromium / desktop). Emits 'test'; the parent owns the flow.
const TestRow = (props: { busy: boolean; result: string }, { emit }: { emit: (e: 'test') => void }) =>
  h('div', { class: 'igs-test' }, [
    h('button', {
      class: 'igs-btn igs-btn--ghost',
      disabled: props.busy,
      'data-testid': 'install-guide-test',
      onClick: () => emit('test'),
    }, props.busy ? '发送中…' : '发送测试通知'),
    props.result ? h('p', { class: 'igs-test-result mono' }, props.result) : null,
  ])
TestRow.props = ['busy', 'result']
TestRow.emits = ['test']

// Refresh permission + subscription state whenever the sheet opens.
watch(() => props.open, (o) => { if (o) { errorText.value = ''; testText.value = ''; void push.refresh() } })

const headline = computed(() => (push.isStandalone.value ? '通知设置' : '安装与通知'))

const lede = computed(() => {
  if (push.isStandalone.value) return '已作为应用运行。开启通知，让 agent 等待输入时主动提醒你。'
  if (push.platform.value === 'chromium') return '安装为应用并开启通知 —— agent 需要确认时第一时间收到提醒。'
  if (push.platform.value === 'ios-safari') return '把页面添加到主屏幕，即可在 iOS 上接收 agent 推送提醒。'
  if (push.platform.value === 'ios-other') return '需要在 Safari 中打开才能安装为应用并接收通知。'
  return '开启通知，在 agent 等待输入时收到提醒。'
})

const notifyStateText = computed(() => {
  if (!push.supported.value) return '不支持'
  if (push.subscribed.value) return '已开启'
  if (push.permission.value === 'denied') return '已拒绝'
  return '未开启'
})
const notifyStateClass = computed(() => {
  if (push.subscribed.value) return 'is-on'
  if (push.permission.value === 'denied' || !push.supported.value) return 'is-off'
  return 'is-idle'
})
const permissionHint = computed(() => {
  switch (push.permission.value) {
    case 'granted': return 'permission: granted'
    case 'denied': return 'permission: denied — 需在系统设置中恢复'
    case 'unsupported': return 'notifications unsupported'
    default: return 'permission: default'
  }
})

async function onSubscribe(): Promise<void> {
  busy.value = true
  errorText.value = ''
  try {
    const ok = await push.subscribe(props.sessionId)
    if (!ok) {
      errorText.value = push.permission.value === 'denied'
        ? '系统已拒绝通知权限，请在浏览器/系统设置中恢复后重试。'
        : '开启通知失败，请稍后重试。'
    }
  } finally {
    busy.value = false
  }
}

async function onUnsubscribe(): Promise<void> {
  busy.value = true
  errorText.value = ''
  try { await push.unsubscribe() } finally { busy.value = false }
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

/* Sections */
.igs-section { margin-top: 4px; }
.igs-section-lbl {
  font-size: 0.62rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: #6b6b72;
  margin-bottom: 8px;
}

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
.igs-step.is-done { border-color: #2f5a3a; background: #16221a; }
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
.igs-step.is-done .igs-step-n { background: rgba(112, 200, 140, 0.16); color: #70c88c; }
.igs-step-body { display: flex; flex-direction: column; gap: 2px; flex: 1; min-width: 0; }
.igs-step-t { color: #e8e8ec; font-weight: 600; font-size: 0.8rem; }
.igs-step-d { color: #8a8a92; font-size: 0.72rem; line-height: 1.45; }
.igs-step-ok { color: #70c88c; font-size: 0.66rem; flex-shrink: 0; }
.igs-step-na { color: #6b6b72; font-size: 0.62rem; flex-shrink: 0; text-align: right; max-width: 72px; }

/* Toggle row (standalone / desktop) */
.igs-toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px;
  background: #1c1c1f;
  border: 1px solid #252528;
  border-radius: 10px;
}
.igs-toggle-meta { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.igs-toggle-state { font-weight: 600; font-size: 0.82rem; }
.igs-toggle-state.is-on { color: #70c88c; }
.igs-toggle-state.is-off { color: #c87070; }
.igs-toggle-state.is-idle { color: #d8d8de; }
.igs-toggle-hint { color: #6b6b72; font-size: 0.62rem; }

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
  margin-top: 12px;
  padding-top: 12px;
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
