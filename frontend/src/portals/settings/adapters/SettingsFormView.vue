<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { FormPane } from '@ce/components/pane'
import { usePortalEvents } from '@ce/composables/layout/usePortalEvents'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { Globe, ClipboardCopy } from 'lucide-vue-next'

interface Props {
  activeCategory: string
}
const props = defineProps<Props>()

// eslint-disable-next-line @typescript-eslint/no-unused-vars
const bus = usePortalEvents()
const { setAuthCode, cliFetch } = useCliAuth()

// ─── System Info ──────────────────────────────────────────────────────────────
interface SystemInfo {
  port: number
  pid: number
  commit: string
}
const systemInfo = ref<SystemInfo | null>(null)

// ─── Terminal / shared settings ───────────────────────────────────────────────
interface TerminalSettings {
  shell: string
  bufferSize: number
  maxSessions: number
  authCode: string
  tunnel: {
    running: boolean
    publicURL: string
  }
}
const terminalSettings = ref<TerminalSettings | null>(null)
const authCode = ref('')

// ─── Tunnel state ─────────────────────────────────────────────────────────────
const tunnel = reactive({
  running: false,
  starting: false,
  publicURL: '',
  downloading: false,
  downloadedBytes: 0,
  totalBytes: -1,
  downloadURL: '',
  binPath: '',
})
const tunnelError = ref('')
let pollInterval: ReturnType<typeof setInterval> | null = null
let startDeadline: ReturnType<typeof setTimeout> | null = null // only for post-download start phase

// Download speed tracking
let lastPollBytes = 0
let lastPollTime = 0
const downloadSpeedBps = ref(0) // bytes/sec, EMA smoothed

// ─── Load ─────────────────────────────────────────────────────────────────────
const loading = ref(false)

onMounted(loadAll)

async function loadAll() {
  loading.value = true
  try {
    await Promise.all([loadSystem(), loadSettings()])
  } finally {
    loading.value = false
  }
}

async function loadSystem() {
  try {
    const res = await cliFetch('/api/system')
    if (res.ok) systemInfo.value = await res.json()
  } catch { /* ignore */ }
}

async function loadSettings() {
  try {
    const res = await cliFetch('/api/settings')
    if (res.ok) {
      const data = await res.json() as TerminalSettings
      terminalSettings.value = data
      authCode.value = data.authCode || ''
      if (data.authCode) setAuthCode(data.authCode)
      if (data.tunnel) {
        tunnel.running = data.tunnel.running
        tunnel.publicURL = data.tunnel.publicURL || ''
      }
    }
  } catch { /* ignore */ }
}

// ─── Clipboard ────────────────────────────────────────────────────────────────
const copySuccess = ref(false)
async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copySuccess.value = true
    setTimeout(() => { copySuccess.value = false }, 2000)
  } catch { /* ignore */ }
}
function copyAuthCode() { copyText(authCode.value) }

// ─── Tunnel controls ──────────────────────────────────────────────────────────
function stopPolling() {
  if (pollInterval) { clearInterval(pollInterval); pollInterval = null }
  if (startDeadline) { clearTimeout(startDeadline); startDeadline = null }
}

function applyStatus(status: {
  running: boolean
  publicURL?: string
  downloading?: boolean
  downloadedBytes?: number
  totalBytes?: number
  downloadURL?: string
  binPath?: string
}) {
  tunnel.running = status.running
  tunnel.publicURL = status.publicURL ?? ''
  tunnel.downloading = status.downloading ?? false
  tunnel.downloadedBytes = status.downloadedBytes ?? 0
  tunnel.totalBytes = status.totalBytes ?? -1
  tunnel.downloadURL = status.downloadURL ?? ''
  tunnel.binPath = status.binPath ?? ''
}

async function startTunnel() {
  tunnel.starting = true
  tunnelError.value = ''

  try {
    const resp = await cliFetch('/api/tunnel/start', { method: 'POST' })
    if (!resp.ok) {
      tunnelError.value = 'Failed to request tunnel start'
      tunnel.starting = false
      return
    }
  } catch {
    tunnelError.value = 'Network error — server unreachable'
    tunnel.starting = false
    return
  }

  lastPollBytes = 0
  lastPollTime = Date.now()
  downloadSpeedBps.value = 0

  // Poll every second.
  // Timeout logic: only applies to the cloudflared *start* phase (after download).
  // While downloading, we never time out — download can take minutes.
  pollInterval = setInterval(async () => {
    try {
      const status = await cliFetch('/api/tunnel/status').then(r => r.json())
      applyStatus(status)

      // ── Download progress ──
      if (status.downloading) {
        const now = Date.now()
        const dt = (now - lastPollTime) / 1000
        if (dt > 0) {
          const instant = (status.downloadedBytes - lastPollBytes) / dt
          // EMA smoothing (α=0.3) to avoid jitter
          downloadSpeedBps.value = downloadSpeedBps.value === 0
            ? instant
            : 0.3 * instant + 0.7 * downloadSpeedBps.value
        }
        lastPollBytes = status.downloadedBytes
        lastPollTime = now
        // Cancel any start-phase deadline while still downloading
        if (startDeadline) { clearTimeout(startDeadline); startDeadline = null }
        return
      }

      // ── Cloudflared start phase ──
      if (!status.running && !status.downloading) {
        // Binary is ready; cloudflared is starting. Start 90s deadline once.
        if (!startDeadline) {
          startDeadline = setTimeout(() => {
            if (!tunnel.running) {
              stopPolling()
              tunnel.starting = false
              tunnelError.value = 'Timeout — cloudflared did not produce a URL in 90s. Check server logs.'
            }
          }, 90_000)
        }
        return
      }

      // ── Running ──
      if (status.running) {
        stopPolling()
        tunnel.starting = false
        downloadSpeedBps.value = 0
      }
    } catch { /* ignore transient poll errors */ }
  }, 1000)
}

async function stopTunnel() {
  stopPolling()
  try { await cliFetch('/api/tunnel/stop', { method: 'POST' }) } catch { /* ignore */ }
  tunnel.running = false
  tunnel.starting = false
  tunnel.publicURL = ''
  tunnel.downloading = false
  tunnelError.value = ''
  downloadSpeedBps.value = 0
}

function formatBytes(bytes: number): string {
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`
  if (bytes >= 1 << 10) return `${(bytes / (1 << 10)).toFixed(0)} KB`
  return `${bytes} B`
}

function formatSpeed(bps: number): string {
  if (bps <= 0) return '—'
  if (bps >= 1 << 20) return `${(bps / (1 << 20)).toFixed(1)} MB/s`
  if (bps >= 1 << 10) return `${(bps / (1 << 10)).toFixed(0)} KB/s`
  return `${bps.toFixed(0)} B/s`
}

function formatEta(remainingBytes: number, bps: number): string {
  if (bps <= 0 || remainingBytes <= 0) return '—'
  const s = Math.round(remainingBytes / bps)
  if (s >= 3600) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  if (s >= 60)   return `${Math.floor(s / 60)}m ${s % 60}s`
  return `${s}s`
}

const downloadPct = computed(() => {
  if (tunnel.totalBytes <= 0 || tunnel.downloadedBytes <= 0) return 0
  return Math.min(100, Math.round((tunnel.downloadedBytes / tunnel.totalBytes) * 100))
})

const downloadEta = computed(() =>
  formatEta(tunnel.totalBytes - tunnel.downloadedBytes, downloadSpeedBps.value)
)

// ─── FormPane sections ────────────────────────────────────────────────────────
const categoryFieldIds: Record<string, string[]> = {
  'system-info': ['__system_info'],
  'terminal':    ['__terminal'],
  'network':     ['__network'],
}

const formSections = computed(() => {
  const fieldIds = categoryFieldIds[props.activeCategory] ?? ['__placeholder']
  return [{
    id: props.activeCategory,
    title: '',
    fields: fieldIds.map(id => ({ id, type: 'text' as const, label: '' })),
  }]
})
</script>

<template>
  <FormPane :sections="formSections" class="h-full">
    <template #actions><span /></template>

    <template #field="{ field }">

      <!-- ── System Info ──────────────────────────────────────────────────── -->
      <template v-if="field.id === '__system_info'">
        <div class="section-header">System Info</div>
        <div v-if="loading" class="loading-text">加载中…</div>
        <div v-else class="info-grid">
          <div class="info-card">
            <span class="info-card__label">Port</span>
            <span class="info-card__value">{{ systemInfo?.port ?? '—' }}</span>
          </div>
          <div class="info-card">
            <span class="info-card__label">PID</span>
            <span class="info-card__value">{{ systemInfo?.pid ?? '—' }}</span>
          </div>
          <div class="info-card">
            <span class="info-card__label">Version / Commit</span>
            <span class="info-card__value">{{ systemInfo?.commit || 'dev' }}</span>
          </div>
        </div>
      </template>

      <!-- ── Terminal ───────────────────────────────────────────────────────── -->
      <template v-else-if="field.id === '__terminal'">
        <div class="section-header">Terminal</div>
        <div v-if="loading" class="loading-text">加载中…</div>
        <div v-else class="info-grid">
          <div class="info-card">
            <span class="info-card__label">Default Shell</span>
            <span class="info-card__value font-mono text-sm">{{ terminalSettings?.shell || '—' }}</span>
          </div>
          <div class="info-card">
            <span class="info-card__label">Buffer Size</span>
            <span class="info-card__value">{{ terminalSettings ? formatBytes(terminalSettings.bufferSize) : '—' }}</span>
          </div>
          <div class="info-card">
            <span class="info-card__label">Max Sessions</span>
            <span class="info-card__value">{{ terminalSettings?.maxSessions ?? '—' }}</span>
          </div>
        </div>
        <p class="hint-text">终端配置为只读。如需修改，请编辑启动参数或配置文件。</p>
      </template>

      <!-- ── Network ────────────────────────────────────────────────────────── -->
      <template v-else-if="field.id === '__network'">

        <!-- Auth Code (always shown) -->
        <div class="settings-section">
          <div class="section-header">Authentication</div>
          <p class="hint-text">远程连接（非本机）需要此认证码。</p>
          <div class="auth-code-row">
            <code class="auth-code">{{ authCode || '—' }}</code>
            <button class="copy-btn" :class="{ 'copy-btn--done': copySuccess }" :title="copySuccess ? '已复制' : '复制'" @click="copyAuthCode">
              <ClipboardCopy :size="14" />
            </button>
          </div>
          <p class="hint-text">
            在 Header <code>X-CLI-Auth</code> 或 URL 参数 <code>?auth=</code> 中携带此码。
          </p>
        </div>

        <!-- Internet Access -->
        <div class="settings-section">
          <div class="section-header">Internet Access</div>
          <p class="hint-text">通过 Cloudflare Tunnel 将终端暴露到公网。</p>

          <!-- Off state -->
          <div v-if="!tunnel.running && !tunnel.starting" class="tunnel-off">
            <button class="btn-primary" @click="startTunnel">
              <Globe :size="14" />
              Enable Internet Access
            </button>
            <p v-if="tunnelError" class="tunnel-error">{{ tunnelError }}</p>
          </div>

          <!-- Starting: downloading cloudflared -->
          <div v-if="tunnel.starting && tunnel.downloading" class="tunnel-starting">
            <div class="download-header">
              <span class="download-label">Downloading cloudflared...</span>
              <span class="download-pct">{{ downloadPct }}%</span>
            </div>
            <div class="progress-bar">
              <div class="progress-bar-fill--determinate" :style="{ width: downloadPct + '%' }" />
            </div>
            <div class="download-stats">
              <span>{{ formatBytes(tunnel.downloadedBytes) }}{{ tunnel.totalBytes > 0 ? ` / ${formatBytes(tunnel.totalBytes)}` : '' }}</span>
              <span>↓ {{ formatSpeed(downloadSpeedBps) }}</span>
              <span v-if="tunnel.totalBytes > 0">ETA {{ downloadEta }}</span>
            </div>
            <div class="download-manual">
              <span class="download-manual__label">慢？手动下载后重新点击 Enable：</span>
              <div class="download-url-row">
                <code class="download-url-text">{{ tunnel.downloadURL }}</code>
                <button class="copy-btn" title="复制链接" @click="copyText(tunnel.downloadURL)">
                  <ClipboardCopy :size="12" />
                </button>
              </div>
              <div class="download-dest-row">
                <span class="download-manual__label">放置路径：</span>
                <code class="download-url-text">{{ tunnel.binPath }}</code>
                <button class="copy-btn" title="复制路径" @click="copyText(tunnel.binPath)">
                  <ClipboardCopy :size="12" />
                </button>
              </div>
            </div>
          </div>

          <!-- Starting: waiting for cloudflared URL -->
          <div v-if="tunnel.starting && !tunnel.downloading" class="tunnel-starting">
            <div class="progress-bar"><div class="progress-bar-fill--indeterminate" /></div>
            <p class="tunnel-status-text">Starting Cloudflare Tunnel...</p>
          </div>

          <!-- Active state -->
          <div v-if="tunnel.running" class="tunnel-active">
            <div class="tunnel-url-row">
              <Globe :size="14" class="tunnel-url-icon" />
              <a :href="tunnel.publicURL" target="_blank" class="tunnel-url-link">{{ tunnel.publicURL }}</a>
              <button class="copy-btn" title="复制链接" @click="copyText(tunnel.publicURL)">
                <ClipboardCopy :size="12" />
              </button>
            </div>
            <button class="btn-ghost" @click="stopTunnel">Disconnect</button>
          </div>
        </div>

      </template>

    </template>
  </FormPane>
</template>

<style scoped>
.section-header {
  font-size: 13px;
  font-weight: 600;
  color: hsl(var(--foreground));
  padding-bottom: 8px;
  border-bottom: 1px solid hsl(var(--border));
  margin-bottom: 10px;
}

.settings-section {
  margin-bottom: 20px;
}

.loading-text {
  font-size: 12px;
  color: hsl(var(--muted-foreground));
  padding: 4px 0;
}

.hint-text {
  font-size: 11px;
  color: hsl(var(--muted-foreground));
  margin-top: 4px;
  margin-bottom: 8px;
  line-height: 1.5;
}

.hint-text code {
  font-family: monospace;
  background: hsl(var(--muted));
  padding: 0 3px;
  border-radius: 3px;
}

/* Info grid */
.info-grid {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 8px;
}

.info-card {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px;
  background: hsl(var(--muted) / 0.5);
  border: 1px solid hsl(var(--border));
  border-radius: 6px;
}

.info-card__label {
  font-size: 10px;
  color: hsl(var(--muted-foreground));
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.info-card__value {
  font-size: 13px;
  font-weight: 600;
  color: hsl(var(--foreground));
}

/* Auth code */
.auth-code-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.auth-code {
  font-family: monospace;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.15em;
  color: hsl(var(--foreground));
  background: hsl(var(--muted));
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid hsl(var(--border));
}

.copy-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 5px;
  border: 1px solid hsl(var(--border));
  background: transparent;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  transition: background-color 0.1s, color 0.1s;
}

.copy-btn:hover {
  background: hsl(var(--muted) / 0.6);
  color: hsl(var(--foreground));
}

.copy-btn--done {
  color: hsl(140 50% 40%);
  border-color: hsl(140 50% 40% / 0.4);
}

/* Tunnel: off */
.tunnel-off {
  margin-top: 4px;
}

/* Tunnel: starting */
.tunnel-starting {
  margin-top: 6px;
}

.download-header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  margin-bottom: 5px;
}
.download-label {
  font-size: 12px;
  font-weight: 500;
  color: hsl(var(--foreground));
}
.download-pct {
  font-size: 12px;
  font-weight: 600;
  color: hsl(var(--primary));
  font-variant-numeric: tabular-nums;
}

.progress-bar {
  height: 4px;
  background: hsl(var(--muted));
  border-radius: 2px;
  overflow: hidden;
  margin-bottom: 6px;
}

.progress-bar-fill--determinate {
  height: 100%;
  background: hsl(var(--primary));
  border-radius: 2px;
  transition: width 0.8s linear;
}

.progress-bar-fill--indeterminate {
  height: 100%;
  width: 35%;
  background: hsl(var(--primary));
  border-radius: 2px;
  animation: progress-slide 1.4s ease-in-out infinite;
}

@keyframes progress-slide {
  0%   { transform: translateX(-100%); }
  100% { transform: translateX(340%); }
}

.download-stats {
  display: flex;
  gap: 14px;
  font-size: 11px;
  color: hsl(var(--muted-foreground));
  font-variant-numeric: tabular-nums;
  margin-bottom: 10px;
}

.download-manual {
  border-top: 1px solid hsl(var(--border));
  padding-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.download-manual__label {
  font-size: 10px;
  color: hsl(var(--muted-foreground));
}
.download-url-row,
.download-dest-row {
  display: flex;
  align-items: center;
  gap: 4px;
}
.download-url-text {
  font-family: monospace;
  font-size: 10px;
  color: hsl(var(--foreground));
  background: hsl(var(--muted));
  padding: 2px 5px;
  border-radius: 3px;
  word-break: break-all;
  flex: 1;
}

.tunnel-status-text {
  font-size: 11px;
  color: hsl(var(--muted-foreground));
  margin: 0;
}

.tunnel-error {
  font-size: 11px;
  color: hsl(0 65% 50%);
  margin: 6px 0 0;
}

/* Tunnel: active */
.tunnel-active {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 4px;
}

.tunnel-url-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
  background: hsl(140 50% 40% / 0.08);
  border: 1px solid hsl(140 50% 40% / 0.25);
  border-radius: 6px;
}

.tunnel-url-icon {
  color: hsl(140 50% 40%);
  flex-shrink: 0;
}

.tunnel-url-link {
  font-size: 12px;
  font-weight: 500;
  color: hsl(140 50% 40%);
  text-decoration: none;
  word-break: break-all;
}

.tunnel-url-link:hover {
  text-decoration: underline;
}

/* Buttons */
.btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  font-size: 12px;
  font-weight: 500;
  border-radius: 6px;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  cursor: pointer;
  transition: opacity 0.1s;
}

.btn-primary:hover {
  opacity: 0.85;
}

.btn-ghost {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  font-size: 11px;
  font-weight: 500;
  border-radius: 5px;
  border: 1px solid hsl(var(--border));
  background: transparent;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  transition: background-color 0.1s, color 0.1s;
  align-self: flex-start;
}

.btn-ghost:hover {
  background: hsl(var(--muted) / 0.6);
  color: hsl(var(--foreground));
}
</style>
