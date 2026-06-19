<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ClipboardCopy } from 'lucide-vue-next'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const { cliFetch, setAuthCode } = useCliAuth()
const authCode = ref('')
const copied = ref(false)

onMounted(async () => {
  try {
    const r = await cliFetch('/api/settings')
    if (r.ok) {
      const d = await r.json()
      authCode.value = d.authCode || ''
      if (d.authCode) setAuthCode(d.authCode)
    }
  } catch { /* ignore */ }
})
async function copy() {
  // SSOT helper: works on iOS/HTTP (navigator.clipboard is undefined there).
  if (await copyTextToClipboard(authCode.value)) {
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-auth">
    <div class="ssec-header">Authentication</div>
    <p class="ssec-hint">远程连接（非本机）需要此认证码。</p>
    <div class="auth-row">
      <code class="auth-code">{{ authCode || '—' }}</code>
      <button class="ssec-copy-btn" :class="{ 'ssec-copy-btn--done': copied }" :title="copied ? '已复制' : '复制'" @click="copy">
        <ClipboardCopy :size="14" />
      </button>
    </div>
    <p class="ssec-hint">在 Header <code>X-CLI-Auth</code> 或 URL 参数 <code>?auth=</code> 中携带此码。</p>
  </div>
</template>

<style scoped>
.auth-row { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
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
</style>
