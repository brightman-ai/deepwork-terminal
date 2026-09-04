<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

interface TerminalSettings { shell: string; bufferSize: number; maxSessions: number }
const { cliFetch } = useCliAuth()
const s = ref<TerminalSettings | null>(null)
const loading = ref(true)

function formatBytes(b: number): string {
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(0)} KB`
  return `${b} B`
}

// ─── 新终端的环境变量（env overlay）────────────────────────────────────────────
//
// 在这里而不是新起一个导航项：它回答的就是「这台机器上的终端长什么样」，和上面那三条只读事实
// 同一个话题。
//
// 编辑态用**数组**而不是对象：直接绑一个 map 的键，会在用户改名字的中途重建 key、丢焦点、
// 还可能和半个旧名字撞上。数组每行有稳定身份，保存时才折成 map。
interface SetRow { key: string; value: string }
const setRows = ref<SetRow[]>([])
const unsetRows = ref<string[]>([])
const envLoading = ref(true)
/** 没读到就禁止保存 —— 否则一次失败的 GET 会让"空表单"被当成"用户清空了配置"存回去。
 *  同 useWorkbench 的 hydration gate，同一个教训的另一处应用。 */
const envHydrated = ref(false)
const envSaving = ref(false)
const envSaved = ref(false)
const envError = ref('')

async function loadEnv(): Promise<void> {
  envLoading.value = true
  try {
    const r = await cliFetch(cliApi('/env'))
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    const ov = await r.json() as { set?: Record<string, string>; unset?: string[] }
    setRows.value = Object.entries(ov.set ?? {}).map(([key, value]) => ({ key, value }))
    unsetRows.value = [...(ov.unset ?? [])]
    envHydrated.value = true
  } catch (e) {
    envError.value = e instanceof Error ? `读不到当前配置（${e.message}），先不要保存` : '读不到当前配置'
    envHydrated.value = false
  } finally {
    envLoading.value = false
  }
}

async function saveEnv(): Promise<void> {
  if (!envHydrated.value) return
  envSaving.value = true
  envError.value = ''
  try {
    const set: Record<string, string> = {}
    for (const row of setRows.value) {
      const k = row.key.trim()
      if (k) set[k] = row.value
    }
    const unset = unsetRows.value.map(k => k.trim()).filter(Boolean)
    const r = await cliFetch(cliApi('/env'), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ set, unset }),
    })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    envSaved.value = true
    setTimeout(() => { envSaved.value = false }, 2000)
  } catch (e) {
    envError.value = e instanceof Error ? `保存失败：${e.message}` : '保存失败'
  } finally {
    envSaving.value = false
  }
}

onMounted(async () => {
  try { const r = await cliFetch('/api/settings'); if (r.ok) s.value = await r.json() } catch { /* ignore */ } finally { loading.value = false }
  await loadEnv()
})
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-shell">
    <div class="ssec-header">Terminal</div>
    <div v-if="loading" class="ssec-loading">加载中…</div>
    <div v-else class="ssec-grid">
      <div class="ssec-card"><span class="ssec-card-label">Default Shell</span><span class="ssec-card-value ssec-mono">{{ s?.shell || '—' }}</span></div>
      <div class="ssec-card"><span class="ssec-card-label">Buffer Size</span><span class="ssec-card-value">{{ s ? formatBytes(s.bufferSize) : '—' }}</span></div>
      <div class="ssec-card"><span class="ssec-card-label">Max Sessions</span><span class="ssec-card-value">{{ s?.maxSessions ?? '—' }}</span></div>
    </div>
    <p class="ssec-hint">以上三项为只读。如需修改，请编辑启动参数或配置文件。</p>

    <div class="ssec-block env-block">
      <div class="ssec-header">新终端的环境变量</div>
      <p class="ssec-hint">
        新开的终端默认继承<strong>启动本服务那个 shell</strong>的环境变量。那份环境在服务启动的一瞬间就被
        定格了，所以过去要改它只能重启整个服务。这里的配置在<strong>每次新建终端时</strong>套用，改完
        下一个新终端立刻生效，不必重启。
      </p>
      <p class="ssec-hint">
        <strong>只影响新建的终端，不会改写已经在跑的 shell</strong> —— 这和 tmux 的
        <code>set-environment</code> 完全一致（已实测：改完之后同一个 pane 里读到的还是旧值，只有新开的
        window 才是新值）。原因是操作系统层面就没法从外部改一个已在运行的进程的环境。要让当前这个终端也
        用上，去标签上右键「把环境变量应用到此终端」，它会把对应的 <code>export</code> /
        <code>unset</code> 命令<strong>敲进你看得见的终端里</strong>。
      </p>

      <div v-if="envLoading" class="ssec-loading">加载中…</div>
      <template v-else>
        <div class="env-group-label">设置（覆盖或新增）</div>
        <div v-for="(row, i) in setRows" :key="`set-${i}`" class="env-row">
          <input v-model="row.key" class="env-input env-input--key" placeholder="变量名" spellcheck="false" />
          <span class="env-eq">=</span>
          <input v-model="row.value" class="env-input" placeholder="值" spellcheck="false" />
          <button class="env-del" title="删除这一行" @click="setRows.splice(i, 1)">✕</button>
        </div>
        <button class="env-add" @click="setRows.push({ key: '', value: '' })">+ 添加一个变量</button>

        <div class="env-group-label">
          移除（不再继承）
          <span class="env-group-note">和「设为空字符串」不一样：这里是让变量彻底消失，工具才会回落到它自己的默认值</span>
        </div>
        <div v-for="(_, i) in unsetRows" :key="`unset-${i}`" class="env-row">
          <input v-model="unsetRows[i]" class="env-input env-input--key" placeholder="要移除的变量名" spellcheck="false" />
          <button class="env-del" title="删除这一行" @click="unsetRows.splice(i, 1)">✕</button>
        </div>
        <button class="env-add" @click="unsetRows.push('')">+ 添加一个要移除的变量</button>

        <div class="env-actions">
          <button class="env-save" :disabled="envSaving || !envHydrated" @click="saveEnv">
            {{ envSaving ? '保存中…' : envSaved ? '已保存' : '保存' }}
          </button>
          <span v-if="envError" class="env-error">{{ envError }}</span>
          <span v-else-if="envSaved" class="env-ok">下一个新建的终端就会用上</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.env-block { margin-top: 24px; }
.env-group-label {
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: hsl(var(--muted-foreground));
  margin: 14px 0 6px;
}
.env-group-note {
  display: block;
  text-transform: none;
  letter-spacing: normal;
  font-size: 11px;
  margin-top: 3px;
}
.env-row { display: flex; align-items: center; gap: 6px; margin-bottom: 6px; }
.env-input {
  flex: 1 1 auto;
  min-width: 0;
  font-family: monospace;
  font-size: 12px;
  padding: 5px 8px;
  border: 1px solid hsl(var(--border));
  border-radius: 5px;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
}
.env-input--key { flex: 0 1 40%; }
.env-input:focus { outline: none; border-color: hsl(var(--ring, var(--border))); }
.env-eq { color: hsl(var(--muted-foreground)); font-family: monospace; }
.env-del {
  flex-shrink: 0;
  width: 26px; height: 26px;
  border: 1px solid hsl(var(--border));
  border-radius: 5px;
  background: transparent;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
}
.env-del:hover { color: hsl(0 70% 55%); border-color: hsl(0 70% 55% / 0.4); }
.env-add {
  font-size: 11px;
  padding: 4px 8px;
  border: 1px dashed hsl(var(--border));
  border-radius: 5px;
  background: transparent;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
}
.env-add:hover { color: hsl(var(--foreground)); }
.env-actions { display: flex; align-items: center; gap: 10px; margin-top: 16px; }
.env-save {
  font-size: 12px;
  font-weight: 600;
  padding: 6px 14px;
  border: 1px solid hsl(var(--border));
  border-radius: 5px;
  background: hsl(var(--muted) / 0.6);
  color: hsl(var(--foreground));
  cursor: pointer;
}
.env-save:disabled { opacity: 0.5; cursor: not-allowed; }
.env-error { font-size: 11px; color: hsl(0 70% 55%); }
.env-ok { font-size: 11px; color: hsl(140 50% 40%); }
</style>
