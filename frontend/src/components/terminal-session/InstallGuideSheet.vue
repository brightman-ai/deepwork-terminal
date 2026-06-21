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
            <!-- Tabs: 通知/安装 (the live action) ↔ 帮助 (feature cheat-sheet). -->
            <div class="igs-tabs" role="tablist">
              <button class="igs-tab" :class="{ 'is-active': tab === 'notify' }" data-testid="igs-tab-notify" @click="tab = 'notify'">通知</button>
              <button class="igs-tab" :class="{ 'is-active': tab === 'help' }" data-testid="igs-tab-help" @click="tab = 'help'">帮助</button>
            </div>

            <div v-show="tab === 'notify'" data-testid="igs-tab-panel-notify">
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

            <!-- ════════ STATE: insecure context — needs HTTPS ══════════════
                 Plain HTTP over a LAN / Tailscale IP. Service Worker + Web Push
                 are disabled by the browser, so the real (and only) fix is to
                 access over HTTPS. Real, concrete options — no fake actions. -->
            <template v-else-if="uiState === 'insecure'">
              <div class="igs-status igs-status--off" data-testid="install-guide-insecure">
                <span class="igs-status-mark">🔒</span>
                <div class="igs-status-body">
                  <span class="igs-status-t">通知需要 HTTPS</span>
                  <span class="igs-status-d">
                    当前通过不安全的 HTTP 访问（{{ host }}），Service Worker 与推送无法启用。一键开启应用内置的 HTTPS 隧道即可：
                  </span>
                </div>
              </div>

              <!-- ── ONE-TAP CTA: the recommended action, a guidance color block. ──
                   default → progress → error/retry → ready(navigate). A pulsing hint
                   dot draws the eye to it. -->
              <!-- READY: tunnel up → tap navigates to the HTTPS origin (a secure
                   context), after which the normal 开启通知 flow becomes available. -->
              <button
                v-if="tunnel.publicURL.value"
                class="igs-tunnel-cta igs-tunnel-cta--ready"
                data-testid="igs-tunnel-cta"
                @click="onTunnelOpen"
              >
                <span class="igs-tunnel-cta-main">
                  <span class="igs-tunnel-cta-icon">✅</span>
                  <span class="igs-tunnel-cta-label">隧道已就绪 — 用 HTTPS 打开</span>
                </span>
                <span class="igs-tunnel-cta-sub">点此跳转到安全地址，再回来即可开启通知</span>
              </button>

              <!-- WORKING: download / start progress, inline. -->
              <button
                v-else-if="tunnel.starting.value"
                class="igs-tunnel-cta igs-tunnel-cta--working"
                data-testid="igs-tunnel-cta"
                disabled
              >
                <span class="igs-tunnel-cta-main">
                  <span class="igs-tunnel-spinner" />
                  <span class="igs-tunnel-cta-label">{{ tunnelProgressText }}</span>
                </span>
                <span v-if="tunnel.downloading.value" class="igs-tunnel-cta-sub">{{ tunnelDownloadText }}</span>
              </button>

              <!-- DEFAULT: the prominent one-tap entry point. -->
              <button
                v-else
                class="igs-tunnel-cta igs-tunnel-cta--idle"
                data-testid="igs-tunnel-cta"
                @click="onTunnelStart"
              >
                <span class="igs-tunnel-hint-dot" aria-hidden="true" />
                <span class="igs-tunnel-cta-main">
                  <span class="igs-tunnel-cta-icon">🚀</span>
                  <span class="igs-tunnel-cta-label">一键开启 HTTPS 通知 · Cloudflare 隧道</span>
                </span>
                <span class="igs-tunnel-cta-sub">应用内置，无需配置；PC 与手机同此一步</span>
              </button>

              <!-- READY: show the url + copy so PC users can paste / open in another tab. -->
              <div v-if="tunnel.publicURL.value" class="igs-tunnel-url" data-testid="igs-tunnel-url">
                <a
                  class="igs-tunnel-url-text"
                  :href="tunnel.publicURL.value"
                  target="_blank"
                  rel="noopener noreferrer"
                  data-testid="igs-tunnel-url-link"
                >{{ tunnel.publicURL.value }}</a>
                <button class="igs-btn igs-btn--ghost igs-tunnel-copy" data-testid="igs-tunnel-copy" @click="onTunnelCopy">
                  {{ tunnelCopied ? '已复制' : '复制' }}
                </button>
              </div>

              <p v-if="tunnel.error.value" class="igs-error" data-testid="igs-tunnel-error">{{ tunnel.error.value }}</p>
              <button
                v-if="tunnel.error.value"
                class="igs-btn igs-btn--accent igs-btn--block"
                data-testid="igs-tunnel-retry"
                @click="onTunnelStart"
              >重试</button>

              <!-- Secondary, smaller fallbacks. -->
              <p class="igs-note" data-testid="igs-insecure-localhost">
                <b>在电脑上？</b>直接用 <span class="mono">http://localhost:{{ port }}</span> 打开本应用，无需隧道即可开启通知。
              </p>
              <ol class="igs-steps igs-steps--muted" style="margin-top: 10px;">
                <li class="igs-step igs-step--compact">
                  <span class="igs-step-n mono">A</span>
                  <div class="igs-step-body">
                    <span class="igs-step-t">Tailscale Serve</span>
                    <span class="igs-step-d mono">tailscale serve --bg --https=443 http://127.0.0.1:{{ port || 'PORT' }}</span>
                  </div>
                </li>
              </ol>
            </template>

            <!-- ════════ STATE: unsupported ══════════════════════════════════ -->
            <template v-else>
              <p class="igs-note">
                当前浏览器不支持 Web Push。可在 Chrome / Edge / Firefox，或已“添加到主屏幕”的 iOS Safari 中开启。
              </p>
            </template>

            <!-- ════════ 微信通道 (channel B) — 扫码直达微信，与上面的浏览器推送并存 ════════
                 An independent transport: lands notifications in WeChat via the official
                 ClawBot/iLink, sidestepping the browser/PWA/Xiaomi-background fragility. -->
            <div class="igs-wechat" data-testid="igs-wechat">
              <div class="igs-wechat-head">
                <span class="igs-wechat-title">📲 微信通道</span>
                <span class="igs-wechat-sub">扫码直达微信，绕开浏览器 / PWA / 小米后台限制</span>
              </div>

              <!-- Connected: show live/dormant status + disconnect. -->
              <template v-if="ilink.status.value.loggedIn">
                <div
                  class="igs-status"
                  :class="ilink.status.value.active ? 'igs-status--on' : 'igs-status--off'"
                  data-testid="igs-wechat-status"
                >
                  <span class="igs-status-mark">{{ ilink.status.value.active ? '✅' : '😴' }}</span>
                  <div class="igs-status-body">
                    <span class="igs-status-t">{{ ilink.status.value.active ? '已连接微信' : '微信通道休眠' }}</span>
                    <span class="igs-status-d">{{ wechatStatusText }}</span>
                  </div>
                </div>
                <button
                  class="igs-optional-link mono"
                  data-testid="igs-wechat-logout"
                  :disabled="ilink.busy.value"
                  @click="onWechatLogout"
                >断开微信通道</button>
              </template>

              <!-- Not connected: QR (once generated) or the connect CTA. -->
              <template v-else>
                <div v-if="ilink.qrDataUrl.value" class="igs-wechat-qr" data-testid="igs-wechat-qr">
                  <img :src="ilink.qrDataUrl.value" alt="微信登录二维码" width="180" height="180" />
                  <p class="igs-note">
                    用<b>微信</b>扫码登录；登录后<b>给机器人发任意一条消息</b>即可激活通知。
                    每轮约可推送 10 条，配额将尽时会提示你回复任意字符续订。
                  </p>
                </div>
                <button
                  v-else
                  class="igs-btn igs-btn--ghost igs-btn--block"
                  data-testid="igs-wechat-connect"
                  :disabled="ilink.busy.value"
                  @click="onWechatConnect"
                >{{ ilink.busy.value ? '生成二维码…' : '连接微信（扫码）' }}</button>
                <p v-if="ilink.error.value" class="igs-error">{{ ilink.error.value }}</p>
              </template>
            </div>
            </div>

            <!-- ════════ TAB: 帮助 — feature cheat-sheet (高频 → 中频, 低频不教) ═════ -->
            <div v-show="tab === 'help'" class="igs-help" data-testid="igs-tab-panel-help">
              <p class="igs-help-lede">把终端用顺手的几招，由常用到偶尔用。</p>

              <div class="igs-help-grp">高频</div>
              <ul class="igs-help-list">
                <li class="igs-help-item"><span class="igs-help-ic">📎</span><div><b>上传附件</b><span>底栏「附件」键：手机直接传照片 / 文件进终端；远程也能推文件。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">⌨️</span><div><b>PC 粘贴</b><span>电脑浏览器里 <code>Ctrl/⌘+V</code>：本机文件和剪贴板图片直接粘进 tmux / 终端。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">🎯</span><div><b>悬浮球选区复制</b><span>拖悬浮球定位、双击复制一个词、拖两端锚点框选，点 Copy 取走。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">⬛</span><div><b>tmux 工具条</b><span><code>cp</code> 进 copy mode、PgUp/PgDn 翻页、<code>½↑</code> 上滚半屏、窗格 / 窗口一键切。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">📋</span><div><b>粘贴键</b><span>把剪贴板送进终端；HTTP 下读不了剪贴板时会自动转下方输入框粘贴 + 发送。</span></div></li>
              </ul>

              <div class="igs-help-grp">中频</div>
              <ul class="igs-help-list">
                <li class="igs-help-item"><span class="igs-help-ic">🔔</span><div><b>等待通知</b><span>HTTPS 下开通知：tmux 各窗格里 claude / codex 等你输入时，手机收推送。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">🗄️</span><div><b>收纳抽屉</b><span>右缘抽屉聚合跨会话的图片 / 文件 / 输入历史，点一下回插当前终端。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">🌐</span><div><b>公网访问</b><span>设置 → Network 一键 Cloudflare 隧道，给一个 <code>https</code> 链接把终端暴露到公网。</span></div></li>
                <li class="igs-help-item"><span class="igs-help-ic">⚙️</span><div><b>设置入口</b><span>左缘把手拉出导航 → 设置：认证码、外观、网络都在这。</span></div></li>
              </ul>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, h, watch, onUnmounted } from 'vue'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { usePushNotifications } from '@terminal/composables/cli/usePushNotifications'
import { useIlink } from '@terminal/composables/cli/useIlink'
// Shared @ce SSOT for the Cloudflare-tunnel lifecycle. It calls /api/tunnel/* through
// settingsApiFetch, which the terminal wires with cli-auth at startup (portals/settings/
// sections/index.ts) — so start()/status are authenticated here exactly as in pro's settings.
import { useTunnel } from '@ce/composables/useTunnel'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const props = defineProps<{ sessionId: string; open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const { isMobile } = useDeviceDetection()
const push = usePushNotifications()
const ilink = useIlink()

// Live one-liner for the WeChat channel status banner.
const wechatStatusText = computed(() => {
  const s = ilink.status.value
  if (s.active) return `本轮已发 ${s.sentCount}/${s.maxSends} 条；agent 等待输入时直接推到微信。`
  if (s.dormant) return `${s.dormant}。回复机器人任意字符即可续订；期间自动走浏览器推送兜底。`
  return '已登录，给机器人发一条消息以激活推送。'
})
async function onWechatConnect(): Promise<void> { await ilink.startLogin() }
async function onWechatLogout(): Promise<void> { await ilink.logout() }

// One-tap HTTPS: starting the built-in tunnel yields the secure context Web Push needs. Used
// only by the 'insecure' state, but mounted once so it can reflect an already-running tunnel.
const tunnel = useTunnel()
const tunnelCopied = ref(false)
onUnmounted(() => { tunnel.dispose(); ilink.stopPolling() })

const tab = ref<'notify' | 'help'>('notify')
const busy = ref(false)
const errorText = ref('')
const testText = ref('')

// Current host:port — shown verbatim in the insecure-context guidance so the user
// recognises exactly which (HTTP) address they're on.
const host = computed(() => (typeof location !== 'undefined' ? location.host : ''))
// The port the terminal is served on — used for the localhost / Tailscale fallback hints.
const port = computed(() => (typeof location !== 'undefined' ? location.port : ''))

// Inline progress copy for the working CTA (下载组件中… / 启动隧道中…).
const tunnelProgressText = computed(() =>
  tunnel.downloading.value ? '下载组件中…' : '启动隧道中…',
)
const tunnelDownloadText = computed(() => {
  const dl = tunnel.downloadedBytes.value
  const total = tunnel.totalBytes.value
  const fmt = (b: number) => (b >= 1 << 20 ? `${(b / (1 << 20)).toFixed(1)} MB` : `${Math.round(b / (1 << 10))} KB`)
  return total > 0 ? `${fmt(dl)} / ${fmt(total)}` : fmt(dl)
})

function onTunnelStart(): void { void tunnel.start() }
// Navigating to the https origin makes the page a secure context; the sheet's normal
// enable-notifications flow then becomes available on the new origin.
function onTunnelOpen(): void {
  if (tunnel.publicURL.value) window.location.assign(tunnel.publicURL.value)
}
async function onTunnelCopy(): Promise<void> {
  // Shared SSOT helper: secure-context writeText, else execCommand fallback. Direct
  // navigator.clipboard.writeText is undefined on iOS/HTTP, where copy silently failed.
  if (await copyTextToClipboard(tunnel.publicURL.value)) {
    tunnelCopied.value = true
    setTimeout(() => { tunnelCopied.value = false }, 2000)
  }
}

// ── Single source of truth: one state, one primary action. ───────────────────
// Order matters — earlier branches win. Reactively reflects every input so the
// sheet updates itself when the user relaunches from the icon or returns from
// Settings (the composable re-evaluates permission/subscribed/standalone on
// visibilitychange + focus).
type UiState = 'on' | 'enable' | 'denied' | 'ios-install' | 'ios-safari' | 'insecure' | 'unsupported'
const uiState = computed<UiState>(() => {
  // Already on — highest priority, regardless of platform.
  if (push.subscribed.value || push.permission.value === 'granted') return 'on'
  // Insecure context (plain HTTP over LAN/Tailscale IP): SW + Push are disabled by
  // the browser, so NO platform advice (add-to-home, enable…) is actionable. Surface
  // the real cause — needs HTTPS — above everything except an already-granted state.
  if (!push.secureContext) return 'insecure'
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

// Refresh permission + subscription state whenever the sheet opens. Also reflect any
// already-running tunnel so the insecure-state CTA can jump straight to its ready state.
watch(() => props.open, (o) => {
  if (o) {
    errorText.value = ''; testText.value = ''
    void push.refresh()
    if (!push.secureContext) void tunnel.refreshStatus()
    ilink.startPolling()
  } else {
    ilink.stopPolling()
  }
})

const headline = computed(() => {
  switch (uiState.value) {
    case 'on': return '通知已开启'
    case 'denied': return '恢复通知权限'
    case 'ios-install':
    case 'ios-safari': return '安装与通知'
    case 'insecure': return '通知需要 HTTPS'
    default: return '通知设置'
  }
})

const lede = computed(() => {
  switch (uiState.value) {
    case 'on': return '一切就绪。下面可发一条测试通知确认链路。'
    case 'denied': return ''
    case 'ios-install': return 'iOS 需先把页面添加到主屏并从图标打开，才能接收 agent 推送提醒。'
    case 'ios-safari': return '需要在 Safari 中打开才能添加到主屏并接收通知。'
    case 'insecure': return ''
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
    // Honest, per-outcome feedback — no false "已发送" when the push service refused.
    if (r.kind === 'delivered') {
      testText.value = `✅ 已送达 ${r.sent ?? 1} 台设备，请留意系统通知。`
    } else if (r.kind === 'rejected') {
      const status = r.rejected?.[0]?.status ?? 0
      testText.value = status === 403
        ? '⚠️ 推送被 Apple 拒绝 (403)，请检查 VAPID 配置（sub 必须是有效的 mailto:/https: 地址）。'
        : `⚠️ 推送被拒绝${status ? ` (${status})` : ''}，未送达。请检查推送配置后重试。`
    } else if (r.kind === 'local') {
      testText.value = '已触发本地测试通知（此设备为前台通知，未走后台推送）。'
    } else {
      testText.value = '测试发送失败，请确认已开启通知后重试。'
    }
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

/* ── One-tap tunnel CTA (insecure state) — THE guidance color block. ──────────
   A primary-tinted card that visually stands out as the recommended action,
   with a pulsing hint dot. idle → working → ready (success) variants. */
.igs-tunnel-cta {
  position: relative;
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 4px;
  margin-top: 14px;
  padding: 14px 15px;
  border-radius: 12px;
  border: 1px solid rgba(240, 138, 60, 0.5);
  background: linear-gradient(135deg, rgba(240, 138, 60, 0.16), rgba(240, 138, 60, 0.07));
  color: #f5c79a;
  text-align: left;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s, opacity 0.12s;
  box-shadow: 0 0 0 1px rgba(240, 138, 60, 0.08), 0 6px 18px rgba(240, 138, 60, 0.06);
}
.igs-tunnel-cta--idle:active { background: linear-gradient(135deg, rgba(240, 138, 60, 0.24), rgba(240, 138, 60, 0.12)); }
.igs-tunnel-cta--working { cursor: default; opacity: 0.92; }
.igs-tunnel-cta--ready {
  border-color: rgba(78, 196, 122, 0.55);
  background: linear-gradient(135deg, rgba(78, 196, 122, 0.18), rgba(78, 196, 122, 0.07));
  color: #8fdba6;
  box-shadow: 0 0 0 1px rgba(78, 196, 122, 0.08), 0 6px 18px rgba(78, 196, 122, 0.06);
}
.igs-tunnel-cta--ready:active { background: linear-gradient(135deg, rgba(78, 196, 122, 0.26), rgba(78, 196, 122, 0.12)); }
.igs-tunnel-cta-main { display: flex; align-items: center; gap: 9px; }
.igs-tunnel-cta-icon { font-size: 1.1rem; line-height: 1; flex-shrink: 0; }
.igs-tunnel-cta-label { font-weight: 700; font-size: 0.9rem; letter-spacing: 0.2px; }
.igs-tunnel-cta-sub { color: #b89a72; font-size: 0.68rem; line-height: 1.4; padding-left: 28px; }
.igs-tunnel-cta--ready .igs-tunnel-cta-sub { color: #6fae84; }

/* Pulsing hint dot — breathes to draw the eye to the recommended action. */
.igs-tunnel-hint-dot {
  position: absolute;
  top: 11px;
  right: 12px;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #f08a3c;
  box-shadow: 0 0 0 0 rgba(240, 138, 60, 0.6);
  animation: igs-dot-pulse 1.6s ease-out infinite;
}
@keyframes igs-dot-pulse {
  0% { box-shadow: 0 0 0 0 rgba(240, 138, 60, 0.55); opacity: 1; }
  70% { box-shadow: 0 0 0 9px rgba(240, 138, 60, 0); opacity: 0.85; }
  100% { box-shadow: 0 0 0 0 rgba(240, 138, 60, 0); opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .igs-tunnel-hint-dot { animation: none; }
}

/* Inline spinner for the working state. */
.igs-tunnel-spinner {
  width: 15px; height: 15px;
  flex-shrink: 0;
  border-radius: 50%;
  border: 2px solid rgba(240, 138, 60, 0.3);
  border-top-color: #f08a3c;
  animation: igs-spin 0.8s linear infinite;
}
@keyframes igs-spin { to { transform: rotate(360deg); } }

/* Ready-state URL row + copy. */
.igs-tunnel-url {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 9px;
  padding: 8px 10px;
  background: #16221a;
  border: 1px solid #2f5a3a;
  border-radius: 9px;
}
.igs-tunnel-url-text {
  flex: 1;
  min-width: 0;
  font-family: 'JetBrains Mono', 'SF Mono', ui-monospace, monospace;
  font-size: 0.7rem;
  color: #8fdba6;
  word-break: break-all;
  /* Tappable link (#5): open the HTTPS origin directly, not only copy. */
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}
.igs-tunnel-url-text:hover { color: #b6f0c5; }
.igs-tunnel-copy { flex-shrink: 0; padding: 5px 11px; }

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

/* ── 微信通道 (channel B) ─────────────────────────────────────────────── */
.igs-wechat {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px dashed #2c2c31;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.igs-wechat-head { display: flex; flex-direction: column; gap: 3px; }
.igs-wechat-title { font-weight: 600; font-size: 0.84rem; color: #7ad28c; }
.igs-wechat-sub { color: #8a8a92; font-size: 0.68rem; line-height: 1.4; }
.igs-wechat-qr { display: flex; flex-direction: column; align-items: center; gap: 10px; }
.igs-wechat-qr img {
  width: 180px; height: 180px;
  border-radius: 10px;
  background: #fff;
  padding: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}

/* Sheet enter/leave */
.igs-fade-enter-active, .igs-fade-leave-active { transition: opacity 0.18s ease; }
.igs-fade-enter-from, .igs-fade-leave-to { opacity: 0; }
.igs-fade-enter-active .igs-panel, .igs-fade-leave-active .igs-panel { transition: transform 0.2s ease; }
.is-mobile .igs-fade-enter-from .igs-panel, .is-mobile .igs-fade-leave-to .igs-panel { transform: translateY(18px); }
.is-desktop .igs-fade-enter-from .igs-panel, .is-desktop .igs-fade-leave-to .igs-panel { transform: translateY(-12px); }

/* ── Tabs (通知 ↔ 帮助) ─────────────────────────────────────────────── */
.igs-tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 14px;
  padding: 3px;
  background: #1c1c1f;
  border: 1px solid #252528;
  border-radius: 9px;
}
.igs-tab {
  flex: 1;
  padding: 7px 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #9a9aa2;
  font-size: 0.84rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
}
.igs-tab.is-active { background: #2a2118; color: #f5c79a; }

/* ── 帮助 cheat-sheet ──────────────────────────────────────────────── */
.igs-help-lede { color: #a8a8b0; font-size: 0.82rem; line-height: 1.55; margin: 0 0 12px; }
.igs-help-grp {
  font-size: 0.66rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: #f08a3c;
  text-transform: uppercase;
  margin: 4px 0 7px;
}
.igs-help-list { list-style: none; margin: 0 0 14px; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.igs-help-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 9px 10px;
  background: #1a1a1d;
  border: 1px solid #232326;
  border-radius: 8px;
}
.igs-help-ic { font-size: 1.05rem; line-height: 1.3; flex-shrink: 0; width: 22px; text-align: center; }
.igs-help-item > div { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.igs-help-item b { color: #e8e8ec; font-size: 0.82rem; font-weight: 600; }
.igs-help-item span { color: #98989f; font-size: 0.76rem; line-height: 1.5; }
.igs-help-item code {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  background: #26262a;
  color: #d8b48a;
  padding: 0 4px;
  border-radius: 4px;
}
</style>
