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
})
const tunnelLog = ref<string[]>([])
let pollInterval: ReturnType<typeof setInterval> | null = null
let pollTimeout: ReturnType<typeof setTimeout> | null = null

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

// ─── Auth code copy ───────────────────────────────────────────────────────────
const copySuccess = ref(false)
async function copyAuthCode() {
  try {
    await navigator.clipboard.writeText(authCode.value)
    copySuccess.value = true
    setTimeout(() => { copySuccess.value = false }, 2000)
  } catch { /* ignore */ }
}

// ─── Tunnel controls ──────────────────────────────────────────────────────────
async function startTunnel() {
  tunnel.starting = true
  tunnelLog.value = ['Checking cloudflared binary...']

  try {
    const resp = await cliFetch('/api/tunnel/start', { method: 'POST' })
    if (!resp.ok) {
      tunnelLog.value.push('Failed to start tunnel')
      tunnel.starting = false
      return
    }
  } catch {
    tunnelLog.value.push('Network error starting tunnel')
    tunnel.starting = false
    return
  }

  tunnelLog.value.push('Starting Cloudflare Tunnel...')

  // Poll for status until running
  pollInterval = setInterval(async () => {
    try {
      const status = await cliFetch('/api/tunnel/status').then(r => r.json())
      if (status.running) {
        clearInterval(pollInterval!)
        clearTimeout(pollTimeout!)
        pollInterval = null
        pollTimeout = null
        tunnel.running = true
        tunnel.starting = false
        tunnel.publicURL = status.publicURL
        tunnelLog.value.push(`Connected: ${status.publicURL}`)
      }
    } catch { /* ignore transient errors */ }
  }, 1000)

  // Timeout after 60s
  pollTimeout = setTimeout(() => {
    if (!tunnel.running) {
      if (pollInterval) { clearInterval(pollInterval); pollInterval = null }
      tunnel.starting = false
      tunnelLog.value.push('Timeout — check console for errors')
    }
  }, 60000)
}

async function stopTunnel() {
  try {
    await cliFetch('/api/tunnel/stop', { method: 'POST' })
  } catch { /* ignore */ }
  tunnel.running = false
  tunnel.publicURL = ''
  tunnelLog.value = []
}

function formatBytes(bytes: number): string {
  if (bytes >= 1 << 20) return `${bytes >> 20} MB`
  if (bytes >= 1 << 10) return `${bytes >> 10} KB`
  return `${bytes} B`
}

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
          </div>

          <!-- Starting state -->
          <div v-if="tunnel.starting" class="tunnel-starting">
            <div class="progress-bar"><div class="progress-bar-fill" /></div>
            <div class="tunnel-log">
              <p v-for="(line, i) in tunnelLog" :key="i">{{ line }}</p>
            </div>
          </div>

          <!-- Active state -->
          <div v-if="tunnel.running" class="tunnel-active">
            <div class="tunnel-url-row">
              <Globe :size="14" class="tunnel-url-icon" />
              <a :href="tunnel.publicURL" target="_blank" class="tunnel-url-link">{{ tunnel.publicURL }}</a>
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

.progress-bar {
  height: 3px;
  background: hsl(var(--muted));
  border-radius: 2px;
  overflow: hidden;
  margin-bottom: 8px;
}

.progress-bar-fill {
  height: 100%;
  width: 40%;
  background: hsl(var(--primary));
  border-radius: 2px;
  animation: progress-slide 1.4s ease-in-out infinite;
}

@keyframes progress-slide {
  0%   { transform: translateX(-100%); }
  100% { transform: translateX(350%); }
}

.tunnel-log {
  font-family: monospace;
  font-size: 11px;
  color: hsl(var(--muted-foreground));
  line-height: 1.6;
}

.tunnel-log p {
  margin: 0;
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
