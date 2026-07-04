<script setup lang="ts">
/**
 * The merged "Access" settings section: the auth code (view / copy / manual EDIT / rotate) +
 * the带码 share link & QR, ABOVE the shared @ce Internet Access (Cloudflare tunnel) section it
 * composes. Auth and network are one story — reach this server remotely — so they live on one
 * page. The tunnel UI is REUSED from @ce (not duplicated); sections/index.ts unregisters its
 * standalone nav item.
 */
import { onMounted, ref, computed, watch } from 'vue'
import { ClipboardCopy, RefreshCw, Check, X, Pencil } from 'lucide-vue-next'
import QRCode from 'qrcode'
import InternetAccessSection from '@ce/portals/settings/sections/InternetAccessSection.vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const { cliFetch, setAuthCode } = useCliAuth()
const authCode = ref('')
const publicURL = ref('')
const qrDataUrl = ref('')
const copiedCode = ref(false)
const copiedLink = ref(false)
const busy = ref(false)
const confirmingRotate = ref(false)
const editing = ref(false)
const editValue = ref('')
const editError = ref('')
let confirmTimer: ReturnType<typeof setTimeout> | undefined

// The one-tap share artifact: the public URL with the auth code embedded so a
// scanned/opened link lets a friend straight in (?auth= is consumed + stripped by
// useCliAuth on load). Empty until a tunnel is up — nothing public to share.
const shareLink = computed(() =>
  publicURL.value && authCode.value
    ? publicURL.value.replace(/\/+$/, '') + '/?auth=' + encodeURIComponent(authCode.value)
    : '',
)

async function loadSettings() {
  try {
    const r = await cliFetch(cliApi('/settings'))
    if (r.ok) {
      const d = await r.json()
      authCode.value = d.authCode || ''
      if (d.authCode) setAuthCode(d.authCode)
    }
  } catch { /* ignore */ }
  // Tunnel status from the HOST's OWN tunnel endpoint (NOT the /cli-prefixed one):
  // standalone → the terminal's tunnel, pro → pro's tunnel — whichever fronts the public URL.
  try {
    const r = await cliFetch('/api/tunnel/status')
    if (r.ok) {
      const t = await r.json()
      publicURL.value = t.publicURL || t.public_url || t.url || ''
    }
  } catch { /* no tunnel → share section stays hidden */ }
}

async function renderQR() {
  if (!shareLink.value) { qrDataUrl.value = ''; return }
  try {
    qrDataUrl.value = await QRCode.toDataURL(shareLink.value, { margin: 1, width: 220 })
  } catch { qrDataUrl.value = '' }
}
watch(shareLink, renderQR)
onMounted(loadSettings)

async function copyCode() {
  if (await copyTextToClipboard(authCode.value)) {
    copiedCode.value = true
    setTimeout(() => { copiedCode.value = false }, 2000)
  }
}
async function copyLink() {
  if (shareLink.value && await copyTextToClipboard(shareLink.value)) {
    copiedLink.value = true
    setTimeout(() => { copiedLink.value = false }, 2000)
  }
}

// ONE helper for both rotate (random) and manual save (custom): POST /auth/rotate with an
// optional {code}. No body → server rotates to a fresh random code; {code} → server sets that
// custom code. Either way the old code (and any shared link/QR) is invalidated at once.
async function setCode(custom?: string): Promise<boolean> {
  if (busy.value) return false
  busy.value = true
  editError.value = ''
  try {
    const init: RequestInit = { method: 'POST' }
    if (custom !== undefined) {
      init.headers = { 'Content-Type': 'application/json' }
      init.body = JSON.stringify({ code: custom })
    }
    const r = await cliFetch(cliApi('/auth/rotate'), init)
    if (r.ok) {
      const d = await r.json()
      if (d.authCode) { authCode.value = d.authCode; setAuthCode(d.authCode) }
      editing.value = false
      return true
    }
    try { editError.value = (await r.json()).error || '保存失败' } catch { editError.value = '保存失败' }
    return false
  } catch { editError.value = '网络错误'; return false } finally {
    busy.value = false
  }
}

// Rotate uses a two-step INLINE confirm, not window.confirm() (a native dialog blocks the JS
// thread + wedges headless automation incl. our own dw-browser Witness, and reads as a jarring
// system popup on mobile).
function askRotate() {
  confirmingRotate.value = true
  clearTimeout(confirmTimer)
  confirmTimer = setTimeout(() => { confirmingRotate.value = false }, 5000)
}
function cancelRotate() { confirmingRotate.value = false; clearTimeout(confirmTimer) }
async function doRotate() { cancelRotate(); await setCode() }

function startEdit() { editValue.value = authCode.value; editError.value = ''; editing.value = true }
function cancelEdit() { editing.value = false; editError.value = '' }
async function saveEdit() {
  const v = editValue.value.trim()
  if (!v) { editError.value = '请输入认证码'; return }
  await setCode(v)
}
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-auth">
    <div class="ssec-header">认证码</div>
    <p class="ssec-hint">远程连接（非本机）需要此认证码。不区分大小写，<code>-</code> 可不输。</p>

    <!-- View mode: code + copy + edit + rotate -->
    <div v-if="!editing" class="auth-row">
      <code class="auth-code">{{ authCode || '—' }}</code>
      <button class="ssec-icon-btn" :class="{ 'is-done': copiedCode }" :title="copiedCode ? '已复制' : '复制'" @click="copyCode">
        <ClipboardCopy :size="14" />
      </button>
      <template v-if="!confirmingRotate">
        <button class="ssec-icon-btn" title="手动修改认证码" @click="startEdit"><Pencil :size="14" /></button>
        <button class="ssec-icon-btn" :disabled="busy" title="轮换为随机码（旧链接失效）" @click="askRotate">
          <RefreshCw :size="14" :class="{ spin: busy }" />
        </button>
      </template>
      <template v-else>
        <span class="warn-txt">旧链接将失效</span>
        <button class="ssec-icon-btn danger" title="确认轮换" @click="doRotate"><Check :size="14" /></button>
        <button class="ssec-icon-btn" title="取消" @click="cancelRotate"><X :size="14" /></button>
      </template>
    </div>

    <!-- Edit mode: type a custom code -->
    <div v-else class="auth-row">
      <input
        v-model="editValue" class="auth-input" placeholder="自定义认证码"
        autocapitalize="off" autocomplete="off" spellcheck="false"
        @keyup.enter="saveEdit" @keyup.esc="cancelEdit"
      />
      <button class="ssec-icon-btn primary" :disabled="busy" title="保存" @click="saveEdit"><Check :size="14" /></button>
      <button class="ssec-icon-btn" title="取消" @click="cancelEdit"><X :size="14" /></button>
    </div>
    <p v-if="editError" class="auth-err">{{ editError }}</p>

    <!-- Share surface: only meaningful once a public tunnel is up. -->
    <div v-if="shareLink" class="share-block">
      <p class="ssec-hint">扫码或点链接即可访问（已带认证码）。微信里请点右上「···」→ 在浏览器打开。</p>
      <div class="share-row">
        <a class="share-link" :href="shareLink" target="_blank" rel="noopener">{{ shareLink }}</a>
        <button class="ssec-icon-btn" :class="{ 'is-done': copiedLink }" :title="copiedLink ? '已复制' : '复制链接'" @click="copyLink">
          <ClipboardCopy :size="14" />
        </button>
      </div>
      <img v-if="qrDataUrl" class="share-qr" :src="qrDataUrl" alt="分享二维码" width="180" height="180" />
    </div>
    <p v-else class="ssec-hint">开启下方 Internet Access（Cloudflare 隧道）后，这里会生成带认证码的分享链接与二维码。</p>

    <!-- Composed shared @ce Internet Access (Cloudflare tunnel): auth + network = one "Access" page. -->
    <div class="access-divider"></div>
    <InternetAccessSection />
  </div>
</template>

<style scoped>
.auth-row { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.auth-code {
  font-family: monospace; font-size: 18px; font-weight: 700; letter-spacing: 0.15em;
  color: hsl(var(--foreground)); background: hsl(var(--muted));
  padding: 6px 12px; border-radius: 6px; border: 1px solid hsl(var(--border));
}
.auth-input {
  flex: 1; max-width: 240px; font-family: monospace; font-size: 16px; letter-spacing: 0.1em;
  color: hsl(var(--foreground)); background: hsl(var(--muted));
  padding: 7px 12px; border-radius: 6px; border: 1px solid hsl(var(--primary));
}
.auth-input:focus { outline: none; }
.auth-err { color: hsl(var(--destructive, 0 72% 51%)); font-size: 12px; margin: 2px 0 6px; }
.ssec-icon-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 30px; height: 30px; border-radius: 6px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--muted));
  color: hsl(var(--foreground)); cursor: pointer;
}
.ssec-icon-btn:hover { background: hsl(var(--accent)); }
.ssec-icon-btn:disabled { opacity: 0.5; cursor: default; }
.ssec-icon-btn.is-done { color: hsl(var(--primary)); }
.ssec-icon-btn.primary { background: hsl(var(--primary)); color: hsl(var(--primary-foreground, 0 0% 100%)); border-color: hsl(var(--primary)); }
.ssec-icon-btn.danger { color: hsl(var(--destructive, 0 72% 51%)); border-color: hsl(var(--destructive, 0 72% 51%)); }
.warn-txt { font-size: 12px; color: hsl(var(--destructive, 0 72% 51%)); white-space: nowrap; }
.spin { animation: spin 0.8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.share-block { margin-top: 12px; }
.share-row { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.share-link {
  font-family: monospace; font-size: 12px; word-break: break-all;
  color: hsl(var(--primary)); text-decoration: none;
  background: hsl(var(--muted)); padding: 6px 10px; border-radius: 6px; flex: 1;
}
.share-qr { background: #fff; padding: 8px; border-radius: 8px; border: 1px solid hsl(var(--border)); }
.access-divider { height: 1px; background: hsl(var(--border)); margin: 20px 0 4px; }
</style>
