<script setup lang="ts">
import {
  computed,
  getCurrentInstance,
  nextTick,
  onBeforeUnmount,
  ref,
  watch,
} from 'vue'
import type { RemoteClipboardClient } from '../../composables/cli/useRemoteClipboard'
import {
  CLIPBOARD_MAX_BYTES,
  type ClipboardEntry,
} from '../../composables/cli/clipboardHistory'
import { copyTerminalTextOnClick } from '../../composables/cli/terminalClipboard'
const props = defineProps<{
  client: RemoteClipboardClient
  active: boolean
  sessionId: string
  paneId?: string
}>()
const c = props.client,
  v = c.view
const inputId = 'clipboard-' + getCurrentInstance()!.uid
const settingsOpen = ref(false),
  editor = ref<HTMLTextAreaElement>(),
  scroll = ref<HTMLElement>()
let refreshTimer: ReturnType<typeof setInterval> | undefined
const rows = computed(() =>
  c.entries.value.filter((e) => {
    const query = v.query.trim().toLowerCase()
    return (
      (!query || (e.text + ' ' + e.source).toLowerCase().includes(query)) &&
      (v.filter === 'all' ||
        v.filter === e.direction ||
        (v.filter === 'pinned' && e.pinned))
    )
  }),
)
const size = computed(() => new TextEncoder().encode(v.draft).length)
const canSend = computed(
  () =>
    !!v.draft.trim() &&
    size.value <= CLIPBOARD_MAX_BYTES &&
    c.targetAvailable.value &&
    !c.busy.value,
)
function time(at: string) {
  return new Date(at).toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
function title(e: ClipboardEntry) {
  return (
    e.text
      .split('\n')
      .find((line) => line.trim())
      ?.slice(0, 100) || '文本'
  )
}
function changeTarget(event: Event) {
  const id = (event.target as HTMLSelectElement).value
  const target = c.targets.value.find((t) => t.id === id)
  if (target) v.target = { ...target }
  v.status = ''
}
function reuse(e: ClipboardEntry) {
  v.draft = e.text
  v.tab = 'send'
  v.stage = 'edit'
  v.status = ''
  c.selectDefault(props.sessionId, props.paneId)
}
async function paste() {
  try {
    v.draft = await navigator.clipboard.readText()
    v.status = ''
  } catch {
    v.status = '请在文本框中粘贴（Ctrl/Cmd+V 或长按粘贴）'
    await nextTick()
    editor.value?.focus()
  }
}
async function copyCommand() {
  if (!v.target) return
  let copied = false
  try {
    await navigator.clipboard.writeText(v.target.readCommand)
    copied = true
  } catch {
    copied = copyTerminalTextOnClick(v.target.readCommand)
  }
  v.status = copied
    ? '取用命令已复制，可在服务器终端执行'
    : '请选中下方命令手动复制'
}
function stageDraft() {
  if (!canSend.value) return
  v.stage = 'draft'
  v.status = '草稿尚未发送。确认目标和内容后再发送。'
}
function viewed() {
  if (props.active && v.tab === 'history') c.markSeen()
}
function retryConnection() {
  void c.poll()
  void c.refreshTargets()
}
function editDraft() {
  v.stage = 'edit'
  v.status = ''
}
watch(
  () => [props.active, v.tab],
  async (_, __, onCleanup) => {
    let cancelled = false
    onCleanup(() => {
      cancelled = true
      clearInterval(refreshTimer)
    })
    clearInterval(refreshTimer)
    if (!props.active) return
    viewed()
    await c.refreshTargets()
    c.selectDefault(props.sessionId, props.paneId)
    if (cancelled) return
    refreshTimer = setInterval(() => void c.refreshTargets(), 10000)
    await nextTick()
    if (scroll.value) scroll.value.scrollTop = v.scroll
  },
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(refreshTimer))
</script>
<template>
  <section class="rc-panel" data-testid="remote-clipboard">
    <div class="rc-heading">
      <strong>远程剪贴板</strong
      ><span class="rc-muted"
        >本机 ⇄ {{ c.host.value }} ·
        {{ c.connected.value ? '已连接' : '未连接' }}</span
      >
    </div>
    <div class="rc-tabs">
      <button
        :class="{ selected: v.tab === 'history' }"
        @click="v.tab = 'history'"
      >
        剪贴历史</button
      ><button :class="{ selected: v.tab === 'send' }" @click="v.tab = 'send'">
        发送到远端
      </button>
    </div>
    <div v-if="c.error.value" class="rc-notice" role="status">
      {{ c.error.value }}
      <button @click="retryConnection">重试</button>
    </div>
    <div v-if="c.persistenceError.value" class="rc-notice">
      {{ c.persistenceError.value }}
    </div>
    <div
      v-show="v.tab === 'history'"
      ref="scroll"
      class="rc-content"
      @scroll="v.scroll = ($event.target as HTMLElement).scrollTop"
    >
      <div class="rc-filters">
        <input
          v-model="v.query"
          type="search"
          placeholder="搜索内容、来源…"
          aria-label="搜索剪贴历史"
          data-testid="clipboard-search"
        /><select v-model="v.filter" aria-label="筛选剪贴来源">
          <option value="all">全部来源</option>
          <option value="remote">远端发来</option>
          <option value="local">本机发出</option>
          <option value="pinned">已固定</option>
        </select>
      </div>
      <div class="rc-muted rc-count">
        {{ rows.length }} 条记录<span v-if="c.settings.paused">
          · 已暂停记录</span
        >
      </div>
      <p v-if="!rows.length" class="rc-empty">
        {{
          v.query
            ? '没有匹配的内容'
            : '远端 cb、tmux 复制的内容会出现在这里。也可以在发送页粘贴本机文本。'
        }}
      </p>
      <article
        v-for="e in rows"
        :key="e.id"
        class="rc-card"
        :data-testid="`clipboard-entry-${e.id}`"
      >
        <button
          class="rc-title"
          :aria-expanded="v.selected === e.id"
          @click="v.selected = v.selected === e.id ? '' : e.id"
        >
          <span class="rc-direction"
            >{{ e.direction === 'remote' ? '↓ 远端发来' : '↑ 本机发出'
            }}{{ e.pinned ? ' · 已固定' : '' }}</span
          ><strong>{{ title(e) }}</strong
          ><span class="rc-muted"
            >{{ e.source }} · {{ e.replayed ? '恢复的历史' : time(e.at) }} ·
            {{ e.text.length }} 字符</span
          >
        </button>
        <pre v-if="v.selected === e.id" class="rc-preview" tabindex="0">{{
          e.text
        }}</pre>
        <div class="rc-actions">
          <button class="rc-primary" @click="c.writeLocal(e)">复制到本机</button
          ><button @click="reuse(e)">发送到远端…</button
          ><button @click="c.pin(e)">
            {{ e.pinned ? '取消固定' : '固定' }}</button
          ><button @click="c.remove(e.id)" aria-label="删除这条剪贴记录">
            删除
          </button>
        </div>
        <div class="rc-status" role="status">
          {{
            e.copiedAt
              ? `${time(e.copiedAt)} 已复制到本机`
              : e.action ||
                (e.direction === 'remote'
                  ? '已接收 · 可复制到本机'
                  : '已存到远端剪贴板')
          }}
        </div>
      </article>
    </div>
    <div v-show="v.tab === 'send'" class="rc-content">
      <label class="rc-label" :for="inputId + '-target'">{{
        v.stage === 'draft'
          ? '输入草稿 · 目标已固定'
          : '发送目标 · 切换主终端不改变此目标'
      }}</label>
      <div class="rc-target">
        <select
          :id="inputId + '-target'"
          :value="v.target?.id || ''"
          :disabled="v.stage === 'draft' || c.busy.value"
          @change="changeTarget"
        >
          <option value="" disabled>选择目标终端</option>
          <option
            v-if="
              v.target && !c.targets.value.some((t) => t.id === v.target?.id)
            "
            :value="v.target.id"
          >
            {{ v.target.name }}（已离线）
          </option>
          <option v-for="t in c.targets.value" :key="t.id" :value="t.id">
            {{ t.name }}
          </option></select
        ><button
          @click="c.refreshTargets()"
          :disabled="c.loadingTargets.value"
          aria-label="刷新目标"
        >
          ↻
        </button>
      </div>
      <div class="rc-edit-label">
        <label :for="inputId + '-editor'">{{
          v.stage === 'draft' ? '确认后发送给目标终端' : '粘贴或编辑本机内容'
        }}</label
        ><button v-if="v.stage === 'edit'" @click="paste">从本机粘贴</button>
      </div>
      <textarea
        :id="inputId + '-editor'"
        ref="editor"
        v-model="v.draft"
        :disabled="c.busy.value"
        placeholder="Ctrl/Cmd+V 或长按粘贴；可先编辑…"
        data-testid="clipboard-editor"
      ></textarea>
      <p class="rc-muted">
        {{ v.draft.length }} 字符
        <span v-if="size > CLIPBOARD_MAX_BYTES">· 内容超过 4 MiB，请分段</span>
      </p>
      <div v-if="v.stage === 'edit'" class="rc-send-actions">
        <button class="rc-primary" :disabled="!canSend" @click="c.saveRemote()">
          {{ c.busy.value ? '保存中…' : '存到远端剪贴板' }}</button
        ><button :disabled="!canSend" @click="stageDraft">填入输入草稿</button>
      </div>
      <div v-else class="rc-send-actions">
        <button class="rc-primary" :disabled="!canSend" @click="c.sendDraft()">
          {{ c.busy.value ? '发送中…' : '确认发送到此终端' }}</button
        ><button :disabled="c.busy.value" @click="editDraft">返回编辑</button>
      </div>
      <p v-if="v.target && !c.targetAvailable.value" class="rc-notice">
        目标暂不可用或已变化。草稿已保留，请刷新并重新选择目标。
      </p>
      <p class="rc-status" role="status" data-testid="clipboard-send-status">
        {{ v.status }}
      </p>
      <details v-if="v.target">
        <summary>如何在远端取用保存的内容</summary>
        <p class="rc-muted">
          保存后，在
          {{ c.host.value }}
          上执行下面的命令。可把输出交给其他命令或保存为文件。
        </p>
        <pre class="rc-preview">{{ v.target.readCommand }}</pre>
        <button @click="copyCommand">复制取用命令</button>
      </details>
    </div>
    <footer class="rc-footer">
      <span class="rc-muted"
        >最近 100 条 · 本浏览器保留 {{ c.settings.retentionDays }} 天</span
      ><button @click="settingsOpen = !settingsOpen">设置</button>
    </footer>
    <div v-if="settingsOpen" class="rc-settings">
      <label
        ><input
          type="checkbox"
          v-model="c.settings.autoCopy"
          @change="c.updateSettings()"
        />当前终端自动复制到本机</label
      >
      <label
        ><input
          type="checkbox"
          v-model="c.settings.paused"
          @change="c.updateSettings()"
        />暂停记录历史</label
      >
      <label
        >保留
        <select
          v-model.number="c.settings.retentionDays"
          @change="c.updateSettings()"
        >
          <option :value="1">1 天</option>
          <option :value="7">7 天</option>
          <option :value="30">30 天</option>
        </select></label
      >
      <p class="rc-muted">
        只记录经过这里的复制与发送。固定项保留至删除，总计最多 100 条 / 20 MiB。
      </p>
      <button @click="c.clear()">清空剪贴历史</button>
    </div>
  </section>
</template>
<style scoped>
.rc-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  color: #e7def5;
  font-size: 13px;
}
.rc-heading {
  padding: 14px 16px 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.rc-heading strong {
  font-size: 16px;
}
.rc-muted {
  font-size: 11px;
  color: #b1a7c1;
}
.rc-tabs {
  display: flex;
  gap: 16px;
  padding: 0 16px;
  border-bottom: 1px solid #362946;
}
.rc-tabs button {
  border: 0;
  background: none;
  border-radius: 0;
  padding: 10px 0;
  color: #b4a7c8;
}
.rc-tabs button.selected {
  color: #dabfff;
  box-shadow: 0 2px #ba91ef;
}
.rc-content {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 14px 16px;
}
.rc-filters,
.rc-target {
  display: flex;
  gap: 6px;
}
.rc-filters input {
  min-width: 0;
  flex: 1;
}
.rc-filters select {
  max-width: 112px;
}
.rc-target select {
  min-width: 0;
  flex: 1;
}
.rc-panel input,
.rc-panel select,
.rc-panel textarea {
  background: #17101f;
  border: 1px solid #463355;
  border-radius: 7px;
  color: inherit;
  padding: 8px;
  font: inherit;
}
.rc-panel textarea {
  width: 100%;
  min-height: 180px;
  resize: vertical;
  line-height: 1.6;
}
.rc-panel button {
  background: #2a1d39;
  border: 1px solid #493659;
  border-radius: 7px;
  padding: 7px 10px;
  color: inherit;
  cursor: pointer;
  font: inherit;
}
.rc-panel button:disabled {
  opacity: 0.45;
  cursor: default;
}
.rc-panel button:focus-visible,
.rc-panel input:focus-visible,
.rc-panel textarea:focus-visible,
.rc-panel select:focus-visible {
  outline: 2px solid #c09bef;
  outline-offset: 2px;
}
.rc-panel button.rc-primary {
  background: #c1a2ed;
  color: #21132e;
  border-color: #c1a2ed;
}
.rc-card {
  border: 1px solid #3c2b50;
  border-radius: 10px;
  margin: 12px 0;
  background: #21162c;
  overflow: hidden;
}
.rc-panel .rc-title {
  display: flex;
  flex-direction: column;
  gap: 5px;
  text-align: left;
  width: 100%;
  border: 0;
  background: none;
  padding: 12px;
  overflow-wrap: anywhere;
}
.rc-title strong {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  width: 100%;
  font-size: 13px;
}
.rc-direction {
  color: #bd9bdf;
  font-size: 11px;
}
.rc-preview {
  margin: 0 12px 12px;
  padding: 10px;
  border-radius: 6px;
  background: #130d1d;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 240px;
  overflow: auto;
  font:
    12px/1.65 ui-monospace,
    monospace;
  user-select: text;
}
.rc-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 0 12px 10px;
}
.rc-actions button {
  font-size: 11px;
}
.rc-status {
  padding: 0 12px 12px;
  font-size: 11px;
  color: #a2d6b9;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.rc-empty {
  padding: 24px 8px;
  color: #ac9bbd;
  line-height: 1.8;
}
.rc-count {
  padding-top: 10px;
}
.rc-label {
  display: block;
  margin-bottom: 8px;
  color: #b4a7c8;
}
.rc-edit-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin: 20px 0 8px;
}
.rc-edit-label button {
  font-size: 11px;
}
.rc-send-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 14px 0;
}
.rc-footer {
  border-top: 1px solid #362946;
  padding: 10px 16px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}
.rc-settings {
  padding: 14px 16px;
  border-top: 1px solid #362946;
  max-height: 240px;
  overflow: auto;
}
.rc-settings label {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.rc-notice {
  margin: 8px 16px;
  color: #e2bc98;
  font-size: 12px;
}
.rc-panel summary {
  cursor: pointer;
  color: #b9a7cf;
  font-size: 12px;
}
.rc-panel details {
  margin-top: 16px;
}
.rc-panel details .rc-preview {
  margin: 10px 0;
}
@media (max-width: 768px) {
  .rc-actions button,
  .rc-send-actions button,
  .rc-target button {
    min-height: 44px;
  }
  .rc-panel textarea,
  .rc-panel input {
    font-size: 16px;
  }
  .rc-send-actions button {
    flex: 1;
  }
}
</style>
