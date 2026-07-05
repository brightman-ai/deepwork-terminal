<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="rtd-scrim"
      data-testid="remote-term-dialog"
      @click.self="close"
    >
      <div class="rtd-card">
        <div class="rtd-head">
          <span class="rtd-title">远程终端</span>
          <button class="rtd-x" type="button" data-testid="remote-term-close" @click="close">&times;</button>
        </div>

        <!-- Saved peers -->
        <div v-if="peers.length" class="rtd-list">
          <div v-for="p in peers" :key="p.id" class="rtd-peer" :data-testid="`remote-peer-${p.id}`">
            <div class="rtd-peer-main" @click="connectPeer(p.id)">
              <Server :size="15" class="rtd-peer-icon" />
              <div class="rtd-peer-text">
                <div class="rtd-peer-name">{{ p.name }}</div>
                <div class="rtd-peer-addr">{{ p.tailscaleUrl || '' }}<template v-if="p.tailscaleUrl && p.cloudflareUrl"> · </template>{{ p.cloudflareUrl || '' }}</div>
              </div>
              <span v-if="connectingId === p.id" class="rtd-peer-busy" data-testid="remote-peer-connecting">连接中…</span>
            </div>
            <button class="rtd-peer-edit" type="button" :data-testid="`remote-peer-edit-${p.id}`" title="编辑" @click.stop="startEdit(p)"><Pencil :size="13" /></button>
            <button class="rtd-peer-del" type="button" :data-testid="`remote-peer-del-${p.id}`" title="删除" @click.stop="onRemove(p.id)">&times;</button>
          </div>
        </div>
        <div v-else class="rtd-empty">还没有远程主机，添加一个 →</div>

        <!-- Inline auth-code prompt (when a peer has no stored code, or it was wrong) -->
        <div v-if="pendingAuthPeerId" class="rtd-authrow" data-testid="remote-auth-row">
          <input
            :value="pendingAuthCode"
            class="rtd-input rtd-input--code"
            type="text"
            placeholder="该远程的认证码 如 E3X1-M6T2"
            data-testid="remote-auth-input"
            @input="pendingAuthCode = formatAuthCode(($event.target as HTMLInputElement).value)"
            @keyup.enter="submitPendingAuth"
          />
          <button class="rtd-btn rtd-btn--primary" type="button" :disabled="busy" @click="submitPendingAuth">保存并连接</button>
        </div>

        <!-- Add-new form -->
        <div v-if="mode === 'add'" class="rtd-form" data-testid="remote-add-form">
          <input v-model="f.name" class="rtd-input" placeholder="名称（如 stwork）" data-testid="remote-add-name" />
          <input v-model="f.tailscaleUrl" class="rtd-input" placeholder="tailscale/局域 http 地址  http://stwork:8087" data-testid="remote-add-tailscale" />
          <input v-model="f.cloudflareUrl" class="rtd-input" placeholder="cloudflare https 地址（可选）  https://xxx.trycloudflare.com" data-testid="remote-add-cloudflare" />
          <input :value="f.code" class="rtd-input rtd-input--code" type="text" placeholder="认证码 如 E3X1-M6T2（存本机浏览器）" data-testid="remote-add-code" @input="f.code = formatAuthCode(($event.target as HTMLInputElement).value)" />
          <div class="rtd-form-actions">
            <button class="rtd-btn" type="button" @click="mode = 'list'; editingId = null">取消</button>
            <button class="rtd-btn rtd-btn--primary" type="button" :disabled="busy" data-testid="remote-add-submit" @click="submitAdd">
              {{ busy ? '连接中…' : (editingId ? '保存并连接' : '添加并连接') }}
            </button>
          </div>
        </div>
        <button v-else class="rtd-add-toggle" type="button" data-testid="remote-add-toggle" @click="startAdd">＋ 添加远程主机</button>

        <p v-if="errorMsg" class="rtd-err" data-testid="remote-term-error">{{ errorMsg }}</p>
        <p class="rtd-hint">地址记在服务器（跨设备共享）；认证码只存这台浏览器。HTTPS 页面只能连 cloudflare 地址。</p>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { Server, Pencil } from 'lucide-vue-next'
import { useRemotePeers, type RemotePeer } from '@terminal/composables/cli/useRemotePeers'
import { formatAuthCode } from '@terminal/composables/cli/authCodeFormat'

const props = defineProps<{
  open: boolean
  /** Open a remote tab against this peer. Parent wires useCliState.createRemoteTab; returns a
   *  result so the dialog can show a precise error. (A prop fn, not an emit — Vue 3 emit does
   *  not propagate a handler's return value back to the caller.) */
  onConnect: (peerId: string) => Promise<{ ok: boolean; error?: string }>
}>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()

const { peers, addPeer, updatePeer, removePeer, setPeerAuth, getPeerAuth, resolveTabConnection, probePeer } = useRemotePeers()

const mode = ref<'list' | 'add'>('list')
const busy = ref(false)
const connectingId = ref<string | null>(null) // which saved peer is currently probing → shows 连接中…
const errorMsg = ref('')
const pendingAuthPeerId = ref<string | null>(null)
const pendingAuthCode = ref('')
const f = reactive({ name: '', tailscaleUrl: '', cloudflareUrl: '', code: '' })
const editingId = ref<string | null>(null) // set when the add-form is reused to EDIT a saved peer

function close() {
  emit('update:open', false)
  // reset transient state so a re-open is clean
  mode.value = 'list'
  errorMsg.value = ''
  pendingAuthPeerId.value = null
  pendingAuthCode.value = ''
  editingId.value = null
}

function startAdd() {
  editingId.value = null
  f.name = ''; f.tailscaleUrl = ''; f.cloudflareUrl = ''
  // Pre-fill THIS browser's own auth code: the user's machines share one code, so a new peer
  // usually wants the same — still editable if not (R5). formatAuthCode keeps the XXXX-XXXX shape.
  f.code = formatAuthCode(resolveTabConnection({ remotePeerId: undefined }).authToken)
  errorMsg.value = ''
  mode.value = 'add'
}

// Reuse the add-form to EDIT a saved peer (R2): pre-fill its current name/addresses/code so the
// user can change any of them — especially the auth code, which otherwise has no edit affordance.
function startEdit(peer: RemotePeer) {
  editingId.value = peer.id
  f.name = peer.name
  f.tailscaleUrl = peer.tailscaleUrl || ''
  f.cloudflareUrl = peer.cloudflareUrl || ''
  f.code = formatAuthCode(getPeerAuth(peer.id))
  errorMsg.value = ''
  mode.value = 'add'
}

function onRemove(id: string) {
  removePeer(id)
  if (pendingAuthPeerId.value === id) pendingAuthPeerId.value = null
}

// Probe (verify reachability + code) then ask the parent to open the tab. Splits a wrong code
// (401) from an unreachable address so the message is precise, not a silent reconnect storm.
async function tryConnect(peerId: string): Promise<void> {
  errorMsg.value = ''
  const conn = resolveTabConnection({ remotePeerId: peerId })
  if (conn.error) { errorMsg.value = conn.error; return }
  if (!conn.authToken) { pendingAuthPeerId.value = peerId; pendingAuthCode.value = ''; return }
  busy.value = true
  connectingId.value = peerId
  try {
    const probe = await probePeer(conn.httpBase, conn.authToken)
    if (!probe.ok) {
      errorMsg.value = probe.error || '连接失败'
      // a wrong code → reopen the code prompt for this peer to re-enter
      if (probe.error === '认证码错误') { pendingAuthPeerId.value = peerId; pendingAuthCode.value = '' }
      return
    }
    const r = await props.onConnect(peerId)
    if (r && r.ok === false) { errorMsg.value = r.error || '打开失败'; return }
    close()
  } finally {
    busy.value = false
    connectingId.value = null
  }
}

function connectPeer(peerId: string) { void tryConnect(peerId) }

async function submitPendingAuth() {
  const id = pendingAuthPeerId.value
  if (!id) return
  if (!pendingAuthCode.value.trim()) { errorMsg.value = '请输入认证码'; return }
  setPeerAuth(id, pendingAuthCode.value.trim())
  pendingAuthPeerId.value = null
  await tryConnect(id)
}

async function submitAdd() {
  errorMsg.value = ''
  if (!f.tailscaleUrl.trim() && !f.cloudflareUrl.trim()) { errorMsg.value = '至少填一个地址'; return }
  if (!f.code.trim()) { errorMsg.value = '请输入认证码'; return }
  const patch = { name: f.name, tailscaleUrl: f.tailscaleUrl.trim() || undefined, cloudflareUrl: f.cloudflareUrl.trim() || undefined }
  let id: string
  if (editingId.value) {
    id = editingId.value
    updatePeer(id, patch)
    editingId.value = null
  } else {
    id = addPeer(patch).id
  }
  setPeerAuth(id, f.code.trim())
  mode.value = 'list'
  await tryConnect(id)
}
</script>

<style scoped>
.rtd-scrim {
  position: fixed;
  inset: 0;
  z-index: 1100;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
@media (min-width: 640px) { .rtd-scrim { align-items: center; } }
.rtd-card {
  width: 100%;
  max-width: 460px;
  max-height: 82vh;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  background: #161320;
  border: 1px solid #2e2750;
  border-radius: 16px 16px 0 0;
  box-shadow: 0 -8px 40px rgba(0, 0, 0, 0.5);
}
@media (min-width: 640px) { .rtd-card { border-radius: 16px; } }
.rtd-head { display: flex; align-items: center; justify-content: space-between; }
.rtd-title { font-size: 0.95rem; font-weight: 600; color: #e6e1f0; }
.rtd-x { background: none; border: none; color: #9a8cc0; font-size: 1.4rem; line-height: 1; cursor: pointer; }

.rtd-list { display: flex; flex-direction: column; gap: 6px; }
.rtd-peer { display: flex; align-items: center; gap: 8px; padding: 8px 10px; background: #0e0b16; border: 1px solid #2a2348; border-radius: 10px; }
.rtd-peer-main { flex: 1; min-width: 0; display: flex; align-items: center; gap: 10px; cursor: pointer; }
.rtd-peer-icon { color: #7fb2ff; flex-shrink: 0; }
.rtd-peer-text { min-width: 0; }
.rtd-peer-name { font-size: 0.85rem; color: #e6e1f0; }
.rtd-peer-addr { font-size: 0.66rem; color: #8a7cae; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rtd-peer-busy { flex-shrink: 0; font-size: 0.68rem; color: #7fb2ff; white-space: nowrap; }
.rtd-peer-edit { display: inline-flex; align-items: center; background: none; border: none; color: #6a5a88; cursor: pointer; padding: 0 3px; }
.rtd-peer-edit:hover { color: #7fb2ff; }
.rtd-peer-del { background: none; border: none; color: #6a5a88; font-size: 1.1rem; line-height: 1; cursor: pointer; padding: 0 4px; }
.rtd-peer-del:hover { color: #ff6b6b; }
/* 认证码输入：等宽 + 字距，像输游戏激活码（配合 formatAuthCode 的 XXXX-XXXX 自动格式）。 */
.rtd-input--code { font-family: var(--dw-mono, ui-monospace, monospace); letter-spacing: 0.14em; }
.rtd-empty { font-size: 0.8rem; color: #8a7cae; padding: 4px 2px; }

.rtd-authrow { display: flex; gap: 8px; }
.rtd-form { display: flex; flex-direction: column; gap: 8px; }
.rtd-input {
  flex: 1;
  min-width: 0;
  padding: 9px 11px;
  border-radius: 9px;
  background: #0e0b16;
  color: #e6e1f0;
  border: 1px solid #3a2e5e;
  font-size: 0.85rem;
  outline: none;
}
.rtd-input::placeholder { color: #6a5a88; }
.rtd-input:focus { border-color: #60d890; }
.rtd-form-actions { display: flex; justify-content: flex-end; gap: 8px; }
.rtd-btn {
  padding: 8px 16px;
  border-radius: 8px;
  background: #221a36;
  color: #c8b8e8;
  border: 1px solid #3a2e5e;
  font-size: 0.82rem;
  cursor: pointer;
}
.rtd-btn:disabled { opacity: 0.5; cursor: default; }
.rtd-btn--primary { background: #2f6df0; border-color: #2f6df0; color: #fff; }
.rtd-add-toggle {
  align-self: flex-start;
  background: none;
  border: none;
  color: #7fb2ff;
  font-size: 0.82rem;
  cursor: pointer;
  padding: 2px 0;
}
.rtd-err { color: #ff8a65; font-size: 0.78rem; margin: 0; }
.rtd-hint { color: #6a5a88; font-size: 0.68rem; margin: 0; line-height: 1.5; }
</style>
