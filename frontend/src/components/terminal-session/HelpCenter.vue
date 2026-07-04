<script setup lang="ts">
/**
 * HelpCenter — a persistent top-right "?" entry + a slide-up sheet that onboards
 * a new user: a short quick-start checklist (notifications / public access / tmux),
 * a tmux keyboard cheat-sheet, and a link to the full tutorial.
 *
 * tmux install honors the "no black-box execution" paradigm: we SHOW the exact
 * command and let the user either [复制] it to run themselves, or [在终端执行] —
 * which injects the command into a real terminal session's PTY so it runs VISIBLY
 * (the click is the consent; nothing is run behind the user's back).
 *
 * A first-visit pulse draws the eye to the "?" once, then never nags again.
 */
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { HelpCircle, X, ClipboardCopy, Play, Check, Sparkles } from 'lucide-vue-next'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const { cliFetch } = useCliAuth()

const props = withDefaults(defineProps<{
  /**
   * Inline mode (pro embed, DESKTOP): render the "?" trigger INTO the host's
   * #dw-topbar-right outlet instead of a viewport-fixed fab. The fixed fab (position:fixed,
   * top-right) overlaps pro's 工作台 collapse/lock controls in split view. The host passes
   * inline only on desktop (the outlet is desktop-only); mobile keeps the fixed fab (no
   * split-view overlap on phones). Standalone terminal leaves this false → fixed fab.
   */
  inline?: boolean
}>(), { inline: false })

// Teleport gate: a target absent on the first render frame silently no-ops and never
// retries, so only mount the inline teleport AFTER onMounted (by which point the host
// shell — the parent that owns #dw-topbar-right — is already mounted, so it's present).
const ready = ref(false)
const inlineFab = computed(() => ready.value && props.inline)

const open = ref(false)
const os = ref('')
const tmuxInstalled = ref<boolean | null>(null)
const copied = ref(false)
const toast = ref('')

const SEEN_KEY = 'help_seen'
const pulse = ref(localStorage.getItem(SEEN_KEY) !== '1')

// Host-OS install command. Linux assumes Debian/Ubuntu (apt) — the most common
// case — with a subtext for other distros; we never guess-and-run, only display.
const tmuxCmd = computed(() => {
  if (os.value === 'darwin') return 'brew install tmux'
  if (os.value === 'linux') return 'sudo apt-get install -y tmux'
  return ''
})
const tmuxCmdNote = computed(() =>
  os.value === 'linux' ? '其他发行版：dnf install tmux · pacman -S tmux · apk add tmux' : '',
)

const cheats: Array<[string, string]> = [
  ['Ctrl-b c', '新建窗口'],
  ['Ctrl-b ,', '重命名当前窗口'],
  ['Ctrl-b n / p', '下一个 / 上一个窗口'],
  ['Ctrl-b 0-9', '按编号切换窗口'],
  ['Ctrl-b "', '上下分屏'],
  ['Ctrl-b %', '左右分屏'],
  ['Ctrl-b 方向键', '在分屏间移动'],
  ['Ctrl-b x', '关闭当前 pane'],
  ['Ctrl-b d', '退出(detach，会话仍在后台)'],
  ['Ctrl-b [', '进入滚动 / 复制模式(q 退出)'],
]

async function load() {
  try {
    const r = await cliFetch(cliApi('/system'))
    if (r.ok) {
      const d = await r.json()
      os.value = d.os || ''
      tmuxInstalled.value = !!d.tmuxInstalled
    }
  } catch { /* ignore — sheet still shows static help */ }
}

function toggle() {
  open.value = !open.value
  if (open.value && pulse.value) {
    pulse.value = false
    localStorage.setItem(SEEN_KEY, '1')
  }
  if (open.value && tmuxInstalled.value === null) load()
}

function showToast(msg: string) {
  toast.value = msg
  setTimeout(() => { if (toast.value === msg) toast.value = '' }, 3500)
}

async function copyTmux() {
  if (await copyTextToClipboard(tmuxCmd.value)) {
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}

// First-run hint bar: a newcomer staring at an empty terminal needs a louder nudge than the
// subtle "?" fab. `pulse` (SEEN_KEY not set) drives BOTH; the bar text adapts to whether tmux is
// installed (its install is the single highest-value onboarding step). Dismissed by ×, by opening
// the sheet, or by the user starting to type — then it never auto-shows again (the fab remains).
const hintText = computed(() =>
  tmuxInstalled.value === false
    ? '装 tmux → 多窗口 · 后台会话 · 断线重连'
    : '新手指引：快捷键 · 开启通知 · 公网访问',
)
function dismissHint() {
  pulse.value = false
  localStorage.setItem(SEEN_KEY, '1')
}
function onFirstKey() {
  if (pulse.value) dismissHint() // user started working → the newcomer hint is now noise
}

// Consent-gated, transparent execution: inject the command into a real terminal
// session's PTY (existing one, else create) so it runs where the user can watch it.
async function runTmux() {
  try {
    let sid = ''
    const listResp = await cliFetch(cliApi('/sessions'))
    if (listResp.ok) {
      const list = await listResp.json() as Array<{ id?: string; session_id?: string }>
      sid = list[0]?.id || list[0]?.session_id || ''
    }
    if (!sid) {
      const created = await cliFetch(cliApi('/sessions'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'tmux 安装', cwd: '~' }),
      })
      if (created.ok) {
        const d = await created.json() as { id?: string; session_id?: string }
        sid = d.id || d.session_id || ''
      }
    }
    if (!sid) { showToast('无法创建终端会话，请改用「复制」手动执行'); return }
    await cliFetch(cliApi(`/sessions/${sid}/input`), {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: tmuxCmd.value + '\n',
    })
    showToast('已在终端执行 ✓ 切到终端查看输出（sudo 可能需要输入密码）')
  } catch {
    showToast('执行失败，请改用「复制」手动执行')
  }
}
onMounted(() => {
  void load()
  ready.value = true
  if (pulse.value) window.addEventListener('keydown', onFirstKey, { once: true })
})
onUnmounted(() => window.removeEventListener('keydown', onFirstKey))
</script>

<template>
  <div>
    <!-- inline (pro desktop): teleport the "?" into the host topbar-right so it can no
         longer overlap 工作台's collapse/lock controls; else the original viewport-fixed
         fab (standalone terminal + mobile, where there's no split-view overlap). -->
    <Teleport v-if="inlineFab" to="#dw-topbar-right">
      <button class="help-fab-inline" title="帮助 / 新手指引" @click="toggle">
        <HelpCircle :size="16" />
      </button>
    </Teleport>
    <button v-else class="help-fab" title="帮助 / 新手指引" @click="toggle">
      <HelpCircle :size="20" />
    </button>

    <!-- First-run floating hint bar: louder than the fab for a newcomer's empty terminal. Tap →
         opens the help sheet; × → never auto-show again; also auto-dismisses on the first keypress. -->
    <Transition name="hint-fade">
      <div v-if="pulse" class="help-hint" role="button" tabindex="0" @click="toggle" @keyup.enter="toggle">
        <Sparkles :size="15" class="help-hint-ico" />
        <span class="help-hint-txt">{{ hintText }}</span>
        <span class="help-hint-cta">看指引</span>
        <button class="help-hint-x" title="不再提示" @click.stop="dismissHint"><X :size="14" /></button>
      </div>
    </Transition>

    <div v-if="open" class="help-scrim" @click.self="open = false">
      <div class="help-sheet" role="dialog" aria-modal="true">
        <div class="help-head">
          <h2>帮助 · 新手指引</h2>
          <button class="help-x" @click="open = false"><X :size="18" /></button>
        </div>

        <!-- 1) Quick start -->
        <section class="help-sec">
          <h3>快速上手</h3>
          <div class="help-item">
            <span class="help-dot">1</span>
            <div>
              <b>开启通知</b>：设置 → 通知 中开启，agent 完成 / 需要你时推送到手机。
            </div>
          </div>
          <div class="help-item">
            <span class="help-dot">2</span>
            <div>
              <b>开启公网访问</b>：让手机 / 异地也能连，两种按需选 ——
              <span class="help-subtle">
                · <b>分享给别人（对方免装）</b>：设置 → 网络 开 Cloudflare 隧道 + Auth 分区的带码链接 / 二维码，对方扫码即用。CF 边缘节点多，无客户端的公网访问里延迟已接近最优。<br>
                · <b>自己常用 / 嫌慢</b>：两端各装一次 <b>Tailscale</b>（点对点 WireGuard 直连 ≈ 本地速度、免费 3 用户/100 设备）—— 真正低延迟的方式。换 CF named tunnel 或 Tailscale Funnel 都不会更快。
              </span>
            </div>
          </div>
          <div class="help-item">
            <span class="help-dot">3</span>
            <div style="flex:1">
              <b>安装 tmux</b>（多窗口 / 后台会话 / 断线重连的基础）
              <div v-if="tmuxInstalled === true" class="help-ok"><Check :size="14" /> 已安装</div>
              <div v-else-if="tmuxCmd" class="help-cmd-block">
                <code class="help-cmd">{{ tmuxCmd }}</code>
                <div class="help-cmd-actions">
                  <button class="help-btn" :class="{ done: copied }" @click="copyTmux"><ClipboardCopy :size="13" /> {{ copied ? '已复制' : '复制' }}</button>
                  <button class="help-btn primary" @click="runTmux"><Play :size="13" /> 在终端执行</button>
                </div>
                <p v-if="tmuxCmdNote" class="help-note">{{ tmuxCmdNote }}</p>
                <p class="help-note">执行会把命令发到终端里可见地运行 —— 不会在后台黑盒执行；你也可复制后自己跑。</p>
              </div>
              <div v-else class="help-note">请参考 tmux 官方安装文档。</div>
            </div>
          </div>
        </section>

        <!-- 2) tmux cheatsheet -->
        <section class="help-sec">
          <h3>tmux 速查（先按 <code>Ctrl-b</code>，再按）</h3>
          <div class="help-cheats">
            <div v-for="[k, v] in cheats" :key="k" class="help-cheat">
              <kbd>{{ k }}</kbd><span>{{ v }}</span>
            </div>
          </div>
        </section>

        <!-- 3) Full docs -->
        <section class="help-sec">
          <h3>完整教程</h3>
          <a class="help-link" href="https://github.com/brightman-ai/deepwork-terminal/blob/main/README_CN.md" target="_blank" rel="noopener">
            deepwork-terminal 使用文档（README_CN） →
          </a>
        </section>

        <p v-if="toast" class="help-toast">{{ toast }}</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.help-fab {
  position: fixed; top: calc(env(safe-area-inset-top, 0px) + 8px); right: 10px; z-index: 2500;
  width: 30px; height: 30px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: rgba(30, 33, 40, 0.55); color: #cbd0d8; border: 1px solid #333842;
  cursor: pointer; backdrop-filter: blur(4px);
  /* Idle = faint so it never "blocks" the terminal corner; solid on hover/focus. */
  opacity: 0.4; transition: opacity 0.2s;
}
.help-fab:hover, .help-fab:focus-visible { opacity: 1; }
.help-fab:hover { background: rgba(40, 44, 52, 0.9); color: #fff; }
.help-fab.pulse { box-shadow: 0 0 0 0 rgba(245, 158, 11, 0.6); animation: hp 1.6s ease-out infinite; }
@keyframes hp {
  0% { box-shadow: 0 0 0 0 rgba(245, 158, 11, 0.55); }
  70% { box-shadow: 0 0 0 10px rgba(245, 158, 11, 0); }
  100% { box-shadow: 0 0 0 0 rgba(245, 158, 11, 0); }
}

/* Inline "?" — pro-embed DESKTOP variant, teleported into #dw-topbar-right. A borderless
   28px iconb that blends with the host topbar controls (matches the .v2-panel-toggle idiom),
   replacing the viewport-fixed fab so it can no longer overlap 工作台's collapse/lock buttons.
   Uses host CSS vars with standalone-safe fallbacks (it only ever renders inside pro). */
.help-fab-inline {
  display: inline-flex; align-items: center; justify-content: center;
  width: 28px; height: 28px; flex-shrink: 0; border-radius: 7px;
  color: var(--dw-mu, #9aa0aa); background: none; border: none; cursor: pointer;
  transition: background 0.12s, color 0.12s;
}
.help-fab-inline:hover, .help-fab-inline:focus-visible { background: var(--dw-sf2, #262a32); color: var(--dw-ac, #e6e8ec); }

/* First-run floating hint bar — a bottom-center pill, clear of the mobile input toolbar. */
.help-hint {
  position: fixed; left: 50%; transform: translateX(-50%);
  bottom: calc(env(safe-area-inset-bottom, 0px) + 72px); z-index: 2500;
  display: flex; align-items: center; gap: 8px; max-width: min(92vw, 460px);
  padding: 8px 10px 8px 12px; border-radius: 12px;
  background: rgba(20, 22, 27, 0.96); border: 1px solid #3a3f4a;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.5); color: #e6e8ec; font-size: 13px;
  cursor: pointer; backdrop-filter: blur(4px);
}
.help-hint-ico { color: #f59e0b; flex-shrink: 0; }
.help-hint-txt { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.help-hint-cta { flex-shrink: 0; color: #16181d; background: #f59e0b; font-weight: 600; padding: 3px 9px; border-radius: 7px; font-size: 12px; }
.help-hint-x { flex-shrink: 0; background: transparent; border: none; color: #7f858f; cursor: pointer; padding: 2px; display: inline-flex; }
.help-hint-x:hover { color: #cbd0d8; }
.hint-fade-enter-active, .hint-fade-leave-active { transition: opacity 0.25s, transform 0.25s; }
.hint-fade-enter-from, .hint-fade-leave-to { opacity: 0; transform: translateX(-50%) translateY(8px); }

.help-scrim {
  position: fixed; inset: 0; z-index: 2600; background: rgba(0,0,0,0.55);
  display: flex; align-items: flex-end; justify-content: center;
}
.help-sheet {
  width: 100%; max-width: 460px; max-height: 88vh; overflow-y: auto;
  background: #14161b; color: #e6e8ec; border-radius: 16px 16px 0 0;
  border: 1px solid #262a32; padding: 18px 18px calc(env(safe-area-inset-bottom, 0px) + 22px);
}
.help-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
.help-head h2 { margin: 0; font-size: 18px; font-weight: 700; }
.help-x { background: transparent; border: none; color: #6b7280; cursor: pointer; padding: 4px; }
.help-sec { border-top: 1px solid #21242b; padding: 14px 0 6px; }
.help-sec:first-of-type { border-top: none; }
.help-sec h3 { margin: 0 0 10px; font-size: 14px; color: #9aa0aa; font-weight: 600; }
.help-sec h3 code, .help-item code { background: #262a32; padding: 1px 6px; border-radius: 4px; font-size: 12px; }
.help-item { display: flex; gap: 10px; margin-bottom: 12px; font-size: 13.5px; line-height: 1.5; color: #c9cdd5; }
.help-item b { color: #e6e8ec; }
.help-dot {
  flex-shrink: 0; width: 20px; height: 20px; border-radius: 50%; background: #2a2e37;
  color: #f59e0b; font-size: 12px; font-weight: 700; display: flex; align-items: center; justify-content: center;
}
.help-ok { display: inline-flex; align-items: center; gap: 4px; color: #22c55e; font-size: 13px; margin-top: 4px; }
.help-cmd-block { margin-top: 8px; }
.help-cmd {
  display: block; background: #0e0f13; color: #d7dbe2; font-family: monospace; font-size: 12.5px;
  padding: 9px 11px; border-radius: 7px; word-break: break-all; border: 1px solid #23262d;
}
.help-cmd-actions { display: flex; gap: 8px; margin-top: 8px; }
.help-btn {
  display: inline-flex; align-items: center; gap: 5px; padding: 6px 11px; border-radius: 6px;
  border: 1px solid #333842; background: #1c1f26; color: #cbd0d8; cursor: pointer; font-size: 12.5px;
}
.help-btn.primary { background: #f59e0b; color: #16181d; border-color: #f59e0b; font-weight: 600; }
.help-btn.done { color: #22c55e; }
.help-note { margin: 6px 0 0; font-size: 11.5px; color: #7f858f; line-height: 1.5; }
.help-subtle { display: block; margin-top: 4px; font-size: 11.5px; color: #7f858f; line-height: 1.5; }
.help-cheats { display: grid; grid-template-columns: 1fr; gap: 6px; }
.help-cheat { display: flex; align-items: center; gap: 10px; font-size: 13px; color: #c9cdd5; }
.help-cheat kbd {
  flex-shrink: 0; min-width: 118px; background: #0e0f13; border: 1px solid #2a2e37; border-radius: 5px;
  padding: 3px 7px; font-family: monospace; font-size: 12px; color: #e6e8ec;
}
.help-link { color: #f59e0b; text-decoration: none; font-size: 13.5px; }
.help-link:hover { text-decoration: underline; }
.help-toast {
  margin: 14px 0 0; padding: 9px 12px; background: #1c2230; border: 1px solid #2b3444;
  border-radius: 8px; font-size: 12.5px; color: #a9c2e8;
}
</style>
