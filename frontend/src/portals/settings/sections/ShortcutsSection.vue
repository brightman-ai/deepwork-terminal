<script setup lang="ts">
// D3 — tab shortcuts, with the PREFIX as the single source of truth.
//
// The user's mental model is "my shortcuts are the Alt ones", so the prefix is one control at the
// top and all six actions derive from it. Rebinding one action pins it (marked 已自定义) and it
// stops following later prefix changes, because an explicit personal choice outranks a bulk
// default. "跟随前缀" hands it back.
//
// Bindings are captured and stored as KeyboardEvent.code ("KeyW"), not the printed character:
// macOS Option is a compose modifier (Option+W prints "∑") and non-Latin layouts shift characters
// too, so a character-based binding silently dies on those setups.
import { onMounted, ref, computed } from 'vue'
import {
  useShortcutsConfig,
  bindingFor,
  bindingLabel,
  isDerived,
  type ShortcutAction,
  type ShortcutPrefix,
} from '@terminal/composables/cli/useShortcutsConfig'

const { config, loading, load, setPrefix, setOverride, clearOverride, resetToDefaults } = useShortcutsConfig()

// Which prefixes the BROWSER eats is platform-specific, and getting this wrong steers people away
// from the one option that works for them. On macOS the browser's own tab shortcuts are Cmd-based,
// so Ctrl+digit / Ctrl+W are entirely free there; on Windows/Linux they are Ctrl-based, so Ctrl is
// the unusable one and Alt is free. A blanket "Ctrl conflicts" warning is wrong half the time.
const IS_MAC = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '')

interface PrefixOption { value: ShortcutPrefix; label: string; note: string; reserved: boolean }

const PREFIXES = computed<PrefixOption[]>(() => [
  {
    value: 'Alt',
    label: IS_MAC ? 'Option' : 'Alt',
    note: '浏览器不占用',
    reserved: false,
  },
  {
    value: 'Ctrl',
    label: 'Ctrl',
    // macOS browsers bind Cmd+1..9 / Cmd+W, leaving Ctrl completely free.
    note: IS_MAC ? '浏览器不占用' : '浏览器占用 1-9 / W / T',
    reserved: !IS_MAC,
  },
  {
    value: 'Meta',
    label: IS_MAC ? 'Cmd' : 'Win',
    note: IS_MAC ? '浏览器占用 1-9 / W / T' : '系统任务栏占用',
    reserved: true,
  },
  { value: 'Ctrl+Shift', label: 'Ctrl + Shift', note: '双修饰键，几乎不会被占', reserved: false },
  { value: 'Alt+Shift', label: IS_MAC ? 'Option + Shift' : 'Alt + Shift', note: '双修饰键，几乎不会被占', reserved: false },
])

type ShortcutRow = { key: ShortcutAction; label: string; hint: string }

// Two groups, because the two families answer to different masters and merging them would lie:
// the tab actions all derive from the prefix above (change it once, they all move), while
// findInTerminal is pinned to the OS's idiomatic "find" gesture regardless of the prefix. Listing
// it under the prefix block would tell the user their prefix controls it — it does not.
const GROUPS: { title: string; sub: string; rows: ShortcutRow[] }[] = [
  {
    title: '标签',
    sub: '跟随上面的前缀键',
    rows: [
      { key: 'switchTab', label: '切到第 N 个标签', hint: '前缀 + 数字键 1-9' },
      { key: 'prevTab', label: '上一个标签', hint: '' },
      { key: 'nextTab', label: '下一个标签', hint: '' },
      { key: 'newTab', label: '新建标签', hint: '' },
      { key: 'closeTab', label: '关闭当前标签', hint: '' },
    ],
  },
  {
    title: '终端内',
    sub: '独立键位，不跟随前缀',
    rows: [
      { key: 'findInTerminal', label: '在终端里搜索', hint: '在当前终端已滚过的输出里查找文本' },
    ],
  },
]

/** The unpin affordance means different things per group: a tab action falls back to the prefix,
 *  findInTerminal falls back to its own fixed default. Saying "跟随前缀" for the latter is wrong. */
function unpinLabel(action: ShortcutAction): string {
  return action === 'findInTerminal' ? '已自定义 · 改回默认' : '已自定义 · 跟随前缀'
}

const capturing = ref<ShortcutAction | null>(null)

function prefixIsReserved(p: ShortcutPrefix): boolean {
  return PREFIXES.value.find((o) => o.value === p)?.reserved ?? false
}
const prefixWarning = computed(() => {
  if (!prefixIsReserved(config.value.prefix)) return ''
  const alt = IS_MAC ? 'Option 或 Ctrl' : 'Alt'
  return `浏览器/系统占用了这个前缀加数字 / W / T，页面收不到这几个键。建议改用 ${alt}，或用双修饰键组合。`
})

/** A system-wide third-party hotkey tool can claim a modifier the browser leaves alone. Nothing in
 *  the page can detect that, so surface it as advice rather than silently letting keys "do nothing". */
const thirdPartyHint = '若某个前缀按下去毫无反应，多半是被系统级工具抢了（如 macOS 的 Contexts 占用 Option）——换一个前缀或改用双修饰键组合即可。' 

/** "Alt+KeyW" → "Alt + W"; a KeyboardEvent.code is an implementation detail, never user-facing.
 *  措辞住在 useShortcutsConfig（绑定的 SSOT），因为终端里那枚搜索按钮的 tooltip 也要显示同一个
 *  快捷键 —— 两处各写一份就会写成两种样子。 */
function display(action: ShortcutAction): string {
  return action === 'switchTab'
    ? `${bindingLabel(bindingFor(config.value, 'switchTab'))} + 1…9`
    : bindingLabel(bindingFor(config.value, action))
}

function startCapture(action: ShortcutAction): void {
  if (action === 'switchTab') return // the digit family follows the prefix; nothing to capture
  capturing.value = action
}

function onCaptureKey(e: KeyboardEvent): void {
  const action = capturing.value
  if (!action) return
  e.preventDefault()
  e.stopPropagation()

  if (e.key === 'Escape') { capturing.value = null; return }
  if (['Alt', 'Control', 'Shift', 'Meta'].includes(e.key)) return // still assembling the combo

  const parts: string[] = []
  if (e.ctrlKey) parts.push('Ctrl')
  if (e.altKey) parts.push('Alt')
  if (e.shiftKey) parts.push('Shift')
  if (e.metaKey) parts.push('Meta')
  // A bare key would swallow ordinary typing in the terminal — require at least one modifier.
  if (parts.length === 0) { capturing.value = null; return }

  setOverride(action, [...parts, e.code].join('+'))
  capturing.value = null
}

onMounted(() => { void load() })
</script>

<template>
  <div class="ssec-body" data-testid="settings-section-shortcuts" @keydown="onCaptureKey">
    <div class="ssec-header">快捷键</div>
    <div v-if="loading" class="ssec-loading">加载中…</div>
    <template v-else>
      <!-- The SSOT control: one prefix, every action follows. -->
      <div class="sc-prefix">
        <div class="sc-prefix-head">
          <span class="sc-prefix-label">前缀键</span>
          <span class="sc-prefix-sub">改这一个，下面「标签」那组一起变（已自定义的除外）</span>
        </div>
        <div class="sc-prefix-opts">
          <button
            v-for="p in PREFIXES"
            :key="p.value"
            type="button"
            class="sc-prefix-btn"
            :class="{ on: config.prefix === p.value, warn: p.reserved }"
            :data-testid="`shortcut-prefix-${p.value}`"
            @click="setPrefix(p.value)"
          >{{ p.label }}<small>{{ p.note }}</small></button>
        </div>
        <p v-if="prefixWarning" class="sc-prefix-warn">{{ prefixWarning }}</p>
        <p class="sc-prefix-tip">{{ thirdPartyHint }}</p>
      </div>

      <div v-for="group in GROUPS" :key="group.title" class="sc-list">
        <div class="sc-group-head">
          <span class="sc-group-title">{{ group.title }}</span>
          <span class="sc-group-sub">{{ group.sub }}</span>
        </div>
        <div v-for="row in group.rows" :key="row.key" class="sc-row">
          <div class="sc-meta">
            <span class="sc-label">{{ row.label }}</span>
            <span v-if="row.hint" class="sc-hint">{{ row.hint }}</span>
          </div>
          <button
            type="button"
            class="sc-key"
            :class="{ capturing: capturing === row.key, pinned: !isDerived(config, row.key) }"
            :disabled="row.key === 'switchTab'"
            :data-testid="`shortcut-${row.key}`"
            @click="startCapture(row.key)"
          >{{ capturing === row.key ? '按下新的组合…' : display(row.key) }}</button>
          <button
            v-if="!isDerived(config, row.key)"
            type="button"
            class="sc-unpin"
            :data-testid="`shortcut-unpin-${row.key}`"
            :title="unpinLabel(row.key)"
            @click="clearOverride(row.key)"
          >{{ unpinLabel(row.key) }}</button>
        </div>
      </div>

      <div class="sc-actions">
        <button type="button" class="sc-reset" data-testid="shortcuts-reset" @click="resetToDefaults">恢复默认</button>
        <span class="sc-default-note">默认前缀 Alt · 数字键切换 · N 新建 · W 关闭 · 重命名双击标签 · 搜索 {{ IS_MAC ? 'Cmd + F' : 'Ctrl + Shift + F' }}</span>
      </div>
      <p class="ssec-hint">
        快捷键按<b>物理按键</b>识别，因此 macOS 上 Option+W 打出 “∑”、或使用非拉丁键盘布局时，一样能触发。
        快捷键在终端页面获得焦点时生效。
      </p>
    </template>
  </div>
</template>

<style scoped>
.sc-prefix { padding: 12px 0 14px; border-bottom: 1px solid hsl(var(--border)); }
.sc-prefix-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 9px; }
.sc-prefix-label { font-size: 0.9rem; font-weight: 600; }
.sc-prefix-sub { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.sc-prefix-opts { display: flex; gap: 8px; flex-wrap: wrap; }
.sc-prefix-btn {
  display: flex; flex-direction: column; align-items: flex-start; gap: 2px;
  padding: 7px 13px; border-radius: 8px; cursor: pointer;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background)); color: hsl(var(--foreground));
  font-size: 0.82rem;
}
.sc-prefix-btn small { font-size: 0.65rem; color: hsl(var(--muted-foreground)); }
.sc-prefix-btn:hover { border-color: hsl(var(--muted-foreground)); }
.sc-prefix-btn.on { border-color: #4a9eff; color: #4a9eff; }
.sc-prefix-btn.on small { color: #4a9eff; }
.sc-prefix-btn.warn small { color: #ff9800; }
.sc-prefix-warn { margin: 9px 0 0; font-size: 0.72rem; color: #ff9800; }
.sc-prefix-tip { margin: 7px 0 0; font-size: 0.72rem; color: hsl(var(--muted-foreground)); }

.sc-list { display: flex; flex-direction: column; }
.sc-group-head { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; padding: 14px 0 4px; }
.sc-group-title { font-size: 0.82rem; font-weight: 600; }
.sc-group-sub { font-size: 0.7rem; color: hsl(var(--muted-foreground)); }
.sc-row {
  display: grid; grid-template-columns: 1fr auto; align-items: center; gap: 8px 10px;
  padding: 10px 0; border-bottom: 1px solid hsl(var(--border));
}
.sc-meta { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.sc-label { font-size: 0.875rem; }
.sc-hint { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.sc-key {
  min-width: 150px; padding: 5px 12px; border-radius: 6px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background));
  color: hsl(var(--foreground)); font-family: ui-monospace, Menlo, monospace; font-size: 0.8rem;
  cursor: pointer; white-space: nowrap;
}
.sc-key:disabled { cursor: default; opacity: 0.75; }
.sc-key:not(:disabled):hover { border-color: hsl(var(--muted-foreground)); }
.sc-key.capturing { border-color: #4a9eff; color: #4a9eff; }
.sc-key.pinned { border-color: #4a9eff33; }
.sc-unpin {
  grid-column: 2; justify-self: end; padding: 2px 8px; border-radius: 5px;
  border: none; background: transparent; color: #4a9eff; font-size: 0.68rem; cursor: pointer;
}
.sc-unpin:hover { text-decoration: underline; }
.sc-actions { display: flex; align-items: center; gap: 12px; margin-top: 14px; flex-wrap: wrap; }
.sc-reset {
  padding: 5px 12px; border-radius: 6px; border: 1px solid hsl(var(--border));
  background: transparent; color: hsl(var(--foreground)); font-size: 0.8rem; cursor: pointer;
}
.sc-reset:hover { background: hsl(var(--accent)); }
.sc-default-note { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
</style>
