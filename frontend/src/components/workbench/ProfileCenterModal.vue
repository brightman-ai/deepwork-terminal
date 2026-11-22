<template>
  <div
    v-if="open"
    class="pm-backdrop"
    data-testid="profile-modal"
    @click.self="emit('close')"
    @keydown.escape.prevent="emit('close')"
  >
    <section
      class="pm-panel"
      role="dialog"
      aria-modal="true"
      aria-label="Profile 配置中心"
    >
      <div class="pm-head">
        <h2>👤 账号与网关配置中心（独立目录隔离 · 支持多配置并发）</h2>
        <button
          type="button"
          class="pm-close-btn"
          data-testid="btn-close-profile-modal"
          title="关闭"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>

      <div class="pm-body">
        <div class="pm-tabs">
          <button
            type="button"
            class="pm-tab"
            :aria-selected="activeTab === 'list'"
            data-testid="pm-tab-list"
            @click="activeTab = 'list'"
          >
            已启用配置 ({{ profiles.length }})
          </button>
          <button
            type="button"
            class="pm-tab"
            :aria-selected="activeTab === 'oauth'"
            data-testid="pm-tab-oauth"
            @click="activeTab = 'oauth'"
          >
            🌐 ＋登录新订阅账号 (OAuth)
          </button>
          <button
            type="button"
            class="pm-tab"
            :aria-selected="activeTab === 'gateway'"
            data-testid="pm-tab-gateway"
            @click="activeTab = 'gateway'"
          >
            🔑 ＋添加 API / 兼容网关
          </button>
          <button
            type="button"
            class="pm-btn-scan"
            data-testid="btn-scan-profiles"
            :disabled="loading"
            @click="emit('scan-local')"
          >
            🔍 扫描本机配置
          </button>
        </div>

        <div v-if="scanMessage" class="pm-notice ok">{{ scanMessage }}</div>
        <div v-if="errorMessage" class="pm-notice err" role="alert">⚠️ {{ errorMessage }}</div>

        <!-- Tab 1: 已启用配置 -->
        <div v-if="activeTab === 'list'" class="pm-list">
          <div
            v-for="prof in profiles"
            :key="prof.id"
            class="pm-card"
            :class="{ 'is-default': prof.id === defaultProfileId || prof.isDefault }"
            :data-testid="`profile-card-${prof.id}`"
          >
            <div class="pm-card-info">
              <div class="pm-card-title-row">
                <strong>{{ prof.name }}</strong>
                <span class="pm-badge">{{ prof.engine }} · {{ prof.model || '默认模型' }}</span>
                <span v-if="prof.badge" class="pm-badge">{{ prof.badge }}</span>
                <span
                  v-if="prof.id === defaultProfileId || prof.isDefault"
                  class="pm-default-pill"
                >
                  默认
                </span>
              </div>
              <div class="pm-card-meta">
                <span v-if="prof.configDir" class="pm-mono">{{ prof.configDir }}</span>
                <span v-if="prof.baseUrl" class="pm-mono">· {{ prof.baseUrl }}</span>
                <span v-if="prof.hasApiKey" class="pm-key-masked">
                  · 密钥: {{ prof.apiKeyMasked || '已密封脱敏' }}
                </span>
              </div>
            </div>

            <div class="pm-card-actions">
              <button
                v-if="prof.id !== defaultProfileId && !prof.isDefault"
                type="button"
                class="pm-btn"
                :data-testid="`btn-profile-default-${prof.id}`"
                :disabled="loading"
                @click="emit('set-default', prof.id)"
              >
                设为默认
              </button>
              <button
                type="button"
                class="pm-btn primary"
                :data-testid="`btn-profile-spawn-${prof.id}`"
                @click="emit('spawn-with-profile', prof)"
              >
                ＋新开此配置终端
              </button>
              <button
                type="button"
                class="pm-btn danger"
                :data-testid="`btn-profile-delete-${prof.id}`"
                :disabled="loading || prof.id === defaultProfileId || prof.isDefault"
                title="删除此配置"
                @click="emit('delete-profile', prof.id)"
              >
                删除
              </button>
            </div>
          </div>

          <div v-if="!profiles.length" class="pm-empty">
            尚未配置独立账号，可点击右上「扫描本机配置」或新建 API 网关。
          </div>
        </div>

        <!-- Tab 2: 新建独立目录订阅账号 (OAuth) -->
        <form
          v-else-if="activeTab === 'oauth'"
          class="pm-form-box"
          @submit.prevent="submitOAuth"
        >
          <div class="pm-form-hint">
            将在 <code>~/.dw-terminal/profiles/&lt;id&gt;/</code> 下创建 0700 独立配置目录，通过环境变量隔离
            <code>CLAUDE_CONFIG_DIR</code> / <code>CODEX_HOME</code>，互不覆盖登录态。
          </div>
          <div class="pm-grid-2">
            <label class="pm-field">
              <span>CLI 引擎</span>
              <select
                v-model="oauthEngine"
                class="pm-input"
                data-testid="oauth-engine-select"
              >
                <option value="claude">Claude Code (Pro/Max/Team)</option>
                <option value="codex">Codex CLI (Plus/Pro)</option>
                <option value="shell">自定义 Shell 环境</option>
              </select>
            </label>
            <label class="pm-field">
              <span>配置名称</span>
              <input
                v-model="oauthName"
                type="text"
                class="pm-input"
                data-testid="oauth-name-input"
                placeholder="例如：Claude 备用订阅 #2"
                required
              />
            </label>
          </div>
          <div class="pm-grid-2">
            <label class="pm-field">
              <span>默认模型（可选）</span>
              <input
                v-model="oauthModel"
                type="text"
                class="pm-input"
                data-testid="oauth-model-input"
                placeholder="如 claude-opus-4-6 / gpt-5.3-codex"
              />
            </label>
            <label class="pm-check-row">
              <input v-model="oauthIsDefault" type="checkbox" data-testid="oauth-default-check" />
              <span>创建后设为默认配置</span>
            </label>
          </div>
          <div class="pm-form-foot">
            <button
              type="submit"
              class="pm-btn-white"
              data-testid="btn-save-oauth-profile"
              :disabled="loading || !oauthName.trim()"
            >
              ✓ 创建隔离配置目录
            </button>
          </div>
        </form>

        <!-- Tab 3: 添加 API / 兼容网关 -->
        <form v-else class="pm-form-box" @submit.prevent="submitGateway">
          <div class="pm-vendor-pills">
            <button
              v-for="(preset, key) in VENDOR_PRESETS"
              :key="key"
              type="button"
              class="pm-vendor-pill"
              :class="{ active: gwPreset === key }"
              :data-testid="`vendor-preset-${key}`"
              @click="applyVendorPreset(key)"
            >
              {{ preset.label }}
            </button>
          </div>

          <div class="pm-grid-2">
            <label class="pm-field">
              <span>配置别名</span>
              <input
                v-model="gwName"
                type="text"
                class="pm-input"
                data-testid="gw-name-input"
                placeholder="例如：智谱 GLM Coding"
                required
              />
            </label>
            <label class="pm-field">
              <span>模型标识</span>
              <input
                v-model="gwModel"
                type="text"
                class="pm-input"
                data-testid="gw-model-input"
                placeholder="glm-5.1-coding"
              />
            </label>
          </div>

          <div class="pm-grid-2">
            <label class="pm-field">
              <span>网关 Base URL</span>
              <input
                v-model="gwBaseUrl"
                type="url"
                class="pm-input"
                data-testid="gw-url-input"
                placeholder="https://open.bigmodel.cn/api/anthropic"
                required
              />
            </label>
            <label class="pm-field">
              <span>API Key（服务端存储，接口全程脱敏）</span>
              <input
                v-model="gwApiKey"
                type="password"
                autocomplete="off"
                class="pm-input"
                data-testid="gw-apikey-input"
                placeholder="sk-..."
              />
            </label>
          </div>

          <div class="pm-grid-2">
            <label class="pm-field">
              <span>目标 CLI 引擎</span>
              <select v-model="gwEngine" class="pm-input" data-testid="gw-engine-select">
                <option value="claude">Claude Code 兼容网关</option>
                <option value="codex">Codex OpenAI 兼容网关</option>
              </select>
            </label>
            <label class="pm-check-row">
              <input v-model="gwIsDefault" type="checkbox" data-testid="gw-default-check" />
              <span>保存后设为默认配置</span>
            </label>
          </div>

          <div class="pm-form-foot">
            <button
              type="button"
              class="pm-btn-accent"
              data-testid="btn-test-gateway"
              :disabled="loading || !gwBaseUrl.trim()"
              @click="handleTestGateway"
            >
              ⚡ 测速自检
              <span v-if="testResult">
                ({{ testResult.ok ? `${testResult.latencyMs}ms 可用` : testResult.message }})
              </span>
            </button>
            <button
              type="submit"
              class="pm-btn-white"
              data-testid="btn-save-gateway"
              :disabled="loading || !gwName.trim() || !gwBaseUrl.trim()"
            >
              保存并启用 ↵
            </button>
          </div>
        </form>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type {
  CreateProfileRequest,
  ProfileEngine,
  ProfileItem,
  TestProfileResponse,
} from '@terminal/api/profiles'
import type { ProfileModalTab } from '@terminal/composables/cli/useTaskWorkbench'

interface VendorPreset {
  label: string
  url: string
  model: string
  engine: ProfileEngine
}

const VENDOR_PRESETS: Record<string, VendorPreset> = {
  glm: {
    label: '智谱 GLM Coding',
    url: 'https://open.bigmodel.cn/api/anthropic',
    model: 'glm-5.1-coding',
    engine: 'claude',
  },
  kimi: {
    label: 'Kimi K2.5',
    url: 'https://api.moonshot.cn/anthropic',
    model: 'kimi-k2.5-coding',
    engine: 'claude',
  },
  minimax: {
    label: 'MiniMax M2.7',
    url: 'https://api.minimax.chat/anthropic',
    model: 'MiniMax-M2.7',
    engine: 'claude',
  },
  openrouter: {
    label: 'OpenRouter',
    url: 'https://openrouter.ai/api/v1',
    model: 'anthropic/claude-sonnet-4.5',
    engine: 'claude',
  },
}

const props = withDefaults(
  defineProps<{
    open: boolean
    profiles: ProfileItem[]
    defaultProfileId?: string
    loading?: boolean
    initialTab?: ProfileModalTab
    testResult?: TestProfileResponse | null
    scanMessage?: string | null
    errorMessage?: string | null
  }>(),
  {
    defaultProfileId: '',
    loading: false,
    initialTab: 'list',
    testResult: null,
    scanMessage: null,
    errorMessage: null,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'create-profile', req: CreateProfileRequest): void
  (e: 'set-default', id: string): void
  (e: 'delete-profile', id: string): void
  (e: 'scan-local'): void
  (e: 'test-gateway', req: { baseUrl: string; apiKey?: string }): void
  (e: 'spawn-with-profile', profile: ProfileItem): void
}>()

const activeTab = ref<ProfileModalTab>(props.initialTab)

// OAuth form
const oauthEngine = ref<ProfileEngine>('claude')
const oauthName = ref<string>('Claude 独立订阅 #2')
const oauthModel = ref<string>('claude-opus-4-6')
const oauthIsDefault = ref<boolean>(false)

// Gateway form
const gwPreset = ref<string>('glm')
const gwName = ref<string>(VENDOR_PRESETS.glm.label)
const gwModel = ref<string>(VENDOR_PRESETS.glm.model)
const gwBaseUrl = ref<string>(VENDOR_PRESETS.glm.url)
const gwApiKey = ref<string>('')
const gwEngine = ref<ProfileEngine>('claude')
const gwIsDefault = ref<boolean>(false)

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      activeTab.value = props.initialTab
      gwApiKey.value = ''
    }
  },
)

watch(
  () => props.initialTab,
  (tab) => {
    activeTab.value = tab
  },
)

function applyVendorPreset(key: string): void {
  const preset = VENDOR_PRESETS[key]
  if (!preset) return
  gwPreset.value = key
  gwName.value = preset.label
  gwBaseUrl.value = preset.url
  gwModel.value = preset.model
  gwEngine.value = preset.engine
}

function submitOAuth(): void {
  const name = oauthName.value.trim()
  if (!name) return
  emit('create-profile', {
    name,
    engine: oauthEngine.value,
    kind: 'oauth',
    model: oauthModel.value.trim() || undefined,
    isDefault: oauthIsDefault.value,
  })
  activeTab.value = 'list'
}

function handleTestGateway(): void {
  const baseUrl = gwBaseUrl.value.trim()
  if (!baseUrl) return
  emit('test-gateway', {
    baseUrl,
    apiKey: gwApiKey.value.trim() || undefined,
  })
}

function submitGateway(): void {
  const name = gwName.value.trim()
  const baseUrl = gwBaseUrl.value.trim()
  if (!name || !baseUrl) return
  emit('create-profile', {
    name,
    engine: gwEngine.value,
    kind: 'gateway',
    model: gwModel.value.trim() || undefined,
    baseUrl,
    apiKey: gwApiKey.value.trim() || undefined,
    isDefault: gwIsDefault.value,
  })
  gwApiKey.value = ''
  activeTab.value = 'list'
}
</script>

<style scoped>
.pm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(5, 7, 12, 0.74);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 42px 12px 16px;
  z-index: 2200;
}
.pm-panel {
  width: min(640px, 100%);
  max-height: calc(100svh - 56px);
  background: var(--bg-surface, #161a22);
  color: var(--text, #eef1f6);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  border-radius: 12px;
  box-shadow: 0 24px 72px rgba(0, 0, 0, 0.75);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.pm-head {
  padding: 11px 15px;
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  gap: 8px;
  background: #131720;
}
.pm-head h2 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
}
.pm-close-btn {
  margin-left: auto;
  width: 26px;
  height: 26px;
  border-radius: 5px;
  background: transparent;
  border: 0;
  color: var(--text-secondary, #a2acc0);
  cursor: pointer;
}
.pm-close-btn:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
.pm-body {
  padding: 12px 15px 15px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pm-tabs {
  display: flex;
  align-items: center;
  gap: 5px;
  flex-wrap: wrap;
}
.pm-tab {
  padding: 4px 9px;
  border-radius: 5px;
  font-size: 11.5px;
  color: var(--text-secondary, #a2acc0);
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid transparent;
  cursor: pointer;
}
.pm-tab[aria-selected='true'] {
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #d6e6ff;
  border-color: rgba(110, 168, 254, 0.45);
  font-weight: 600;
}
.pm-btn-scan {
  margin-left: auto;
  padding: 4px 8px;
  border-radius: 5px;
  font-size: 11px;
  background: #24293d;
  border: 1px solid #3c4363;
  color: #d4dcfa;
  cursor: pointer;
}
.pm-btn-scan:hover:not(:disabled) {
  background: #303752;
  color: #fff;
}
.pm-notice {
  padding: 6px 10px;
  border-radius: 6px;
  font-size: 11.5px;
}
.pm-notice.ok {
  background: rgba(61, 214, 140, 0.12);
  border: 1px solid rgba(61, 214, 140, 0.35);
  color: #b7f5cb;
}
.pm-notice.err {
  background: rgba(255, 107, 107, 0.14);
  border: 1px solid rgba(255, 107, 107, 0.45);
  color: #ffd0d0;
}
.pm-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.pm-card {
  padding: 10px 11px;
  border-radius: 8px;
  background: var(--bg-canvas, #0c0e12);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.pm-card.is-default {
  border-color: rgba(110, 168, 254, 0.5);
  background: rgba(110, 168, 254, 0.06);
}
.pm-card-info {
  min-width: 0;
  flex: 1;
}
.pm-card-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 12.5px;
}
.pm-badge {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  color: var(--text-secondary, #a2acc0);
}
.pm-default-pill {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 99px;
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  border: 1px solid rgba(110, 168, 254, 0.45);
  color: #d6e6ff;
  font-weight: 600;
}
.pm-card-meta {
  margin-top: 3px;
  font-size: 11px;
  color: var(--text-muted, #6c7688);
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.pm-mono {
  font-family: var(--font-mono, ui-monospace, monospace);
}
.pm-key-masked {
  color: #8ce8b5;
  font-family: var(--font-mono, ui-monospace, monospace);
}
.pm-card-actions {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}
.pm-btn {
  padding: 3px 8px;
  border-radius: 5px;
  font-size: 11px;
  background: #24293d;
  border: 1px solid #3c4363;
  color: #d4dcfa;
  cursor: pointer;
}
.pm-btn.primary {
  background: #214368;
  border-color: #4880c2;
  color: #e8f2ff;
}
.pm-btn.danger:hover:not(:disabled) {
  background: rgba(255, 107, 107, 0.22);
  border-color: rgba(255, 107, 107, 0.5);
  color: #ffd0d0;
}
.pm-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.pm-form-box {
  display: flex;
  flex-direction: column;
  gap: 10px;
  background: var(--bg-canvas, #0c0e12);
  padding: 12px;
  border-radius: 8px;
  border: 1px solid var(--line, rgba(255, 255, 255, 0.075));
}
.pm-form-hint {
  font-size: 11.5px;
  color: var(--text-secondary, #a2acc0);
  line-height: 1.5;
}
.pm-vendor-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}
.pm-vendor-pill {
  padding: 4px 9px;
  border-radius: 6px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-surface, #161a22);
  font-size: 11.5px;
  color: var(--text-secondary, #a2acc0);
  cursor: pointer;
}
.pm-vendor-pill.active {
  border-color: #6ea8fe;
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #fff;
  font-weight: 600;
}
.pm-grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.pm-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
}
.pm-input {
  height: 32px;
  padding: 0 10px;
  border-radius: 6px;
  background: var(--bg-surface, #161a22);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  font-size: 12px;
  color: var(--text, #eef1f6);
  width: 100%;
}
.pm-check-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary, #a2acc0);
  align-self: end;
  height: 32px;
  cursor: pointer;
}
.pm-form-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.pm-btn-accent {
  height: 30px;
  padding: 0 10px;
  border-radius: 6px;
  border: 1px solid rgba(110, 168, 254, 0.42);
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #d6e6ff;
  font-size: 11.5px;
  cursor: pointer;
}
.pm-btn-white {
  margin-left: auto;
  height: 30px;
  padding: 0 13px;
  border-radius: 6px;
  background: var(--cta-white, #f2f4f8);
  color: var(--cta-white-fg, #0d1017);
  border: 0;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}
.pm-btn-white:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.pm-empty {
  padding: 20px;
  text-align: center;
  color: var(--text-muted, #6c7688);
  font-size: 12px;
}
@media (max-width: 640px) {
  .pm-grid-2 {
    grid-template-columns: 1fr;
  }
}
</style>
