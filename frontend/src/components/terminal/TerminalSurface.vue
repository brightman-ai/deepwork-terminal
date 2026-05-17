<template>
  <div class="terminal-surface" ref="containerRef">
    <div class="terminal-surface__xterm" ref="terminalRef" />
  </div>
</template>

<script setup lang="ts">
/**
 * TerminalSurface — 轻量终端挂载点。
 *
 * 职责:
 * 1. 创建 xterm.js Terminal 实例并挂载到 DOM
 * 2. 调用 useTerminalChannel.attach(terminal) 绑定 WS I/O
 * 3. 通过 ResizeObserver 自动触发 FitAddon + PTY resize
 * 4. 生命周期管理: connect on mount, dispose on unmount
 *
 * 设计约束:
 * - 不包含移动端工具栏 / 触摸交互 (保留给 CliTerminalSurface)
 * - 不包含 preempted banner / auth dialog (由上层 portal 处理)
 * - Props 最小化，保持可复用性
 *
 * [Ref: CAP-terminal-io, TH-0501-m9j 铁律 v2.0]
 */
import { ref, watch, onMounted, onUnmounted, type Ref } from 'vue'
import { Terminal } from 'xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import 'xterm/css/xterm.css'
import { useTerminalChannel } from '@ce/composables/channel/useTerminalChannel'
import type { AgentState } from '@/types/terminal'

// ─── Props & Emits ─────────────────────────────────────────────────────────────

const props = defineProps<{
  /** 当前 session ID (null = 未绑定) */
  sessionId: string | null
  /** 是否为活跃标签 (用于 connect / disconnect 时机控制) */
  active?: boolean
}>()

const emit = defineEmits<{
  /** shell 进程退出 */
  (e: 'shell-exit', exitCode: number): void
  /** agent 状态变化 */
  (e: 'agent-state-change', state: AgentState | null): void
}>()

// ─── Template refs ─────────────────────────────────────────────────────────────

const containerRef = ref<HTMLDivElement>()
const terminalRef = ref<HTMLDivElement>()

// ─── Channel ───────────────────────────────────────────────────────────────────

const sessionIdRef = ref<string | null>(props.sessionId) as Ref<string | null>

watch(() => props.sessionId, (val) => {
  sessionIdRef.value = val
})

const channel = useTerminalChannel({ sessionId: sessionIdRef })

channel.onShellExit((code) => {
  emit('shell-exit', code)
})

watch(channel.agentState, (state) => {
  emit('agent-state-change', state)
})

// ─── xterm.js 实例 ─────────────────────────────────────────────────────────────

let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let resizeObserver: ResizeObserver | null = null
let resizeDebounce: ReturnType<typeof setTimeout> | null = null
let activeFitTimeout1: ReturnType<typeof setTimeout> | null = null
let activeFitTimeout2: ReturnType<typeof setTimeout> | null = null

function createTerminal() {
  const el = terminalRef.value
  if (!el) return

  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: 'Menlo, Monaco, "Courier New", monospace',
    theme: {
      background: '#1e1e1e',
      foreground: '#e0e0e0',
    },
    allowProposedApi: true,
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.loadAddon(new WebLinksAddon())
  terminal.open(el)
  fitAddon.fit()

  // 绑定 WS channel (PTY I/O + control messages)
  channel.attach(terminal)

  // 设置 ResizeObserver 驱动 fit + PTY resize
  resizeObserver = new ResizeObserver(() => {
    if (resizeDebounce) clearTimeout(resizeDebounce)
    resizeDebounce = setTimeout(fitAndResize, 50)
  })
  resizeObserver.observe(el)
}

function fitAndResize() {
  if (!fitAddon || !terminal) return
  fitAddon.fit()
  if (terminal.cols > 0 && terminal.rows > 0) {
    channel.resize(terminal.cols, terminal.rows)
  }
}

function disposeTerminal() {
  if (resizeDebounce) { clearTimeout(resizeDebounce); resizeDebounce = null }
  if (activeFitTimeout1) { clearTimeout(activeFitTimeout1); activeFitTimeout1 = null }
  if (activeFitTimeout2) { clearTimeout(activeFitTimeout2); activeFitTimeout2 = null }
  resizeObserver?.disconnect()
  resizeObserver = null
  terminal?.dispose()
  terminal = null
  fitAddon = null
}

// ─── active prop: connect / disconnect ────────────────────────────────────────

watch(() => props.active, (isActive) => {
  if (isActive) {
    channel.connect()
    // tab 切换后重新 fit，消除 v-show 隐藏时无法测量尺寸的问题
    activeFitTimeout1 = setTimeout(fitAndResize, 50)
    activeFitTimeout2 = setTimeout(fitAndResize, 300)
  } else {
    channel.disconnect()
  }
}, { immediate: false })

// ─── Lifecycle ─────────────────────────────────────────────────────────────────

onMounted(() => {
  createTerminal()
  if (props.active !== false) {
    channel.connect()
  }
})

onUnmounted(() => {
  channel.disconnect()
  disposeTerminal()
})

// ─── Expose (供父组件访问 channel 状态) ────────────────────────────────────────

defineExpose({
  connected: channel.connected,
  reconnecting: channel.reconnecting,
  rawStatus: channel.rawStatus,
  netStats: channel.netStats,
  agentState: channel.agentState,
  /** 外部触发 fit + resize (例如父容器尺寸变化) */
  fitAndResize,
})
</script>

<style scoped>
.terminal-surface {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  background: #1e1e1e;
  overflow: hidden;
}

.terminal-surface__xterm {
  flex: 1;
  min-height: 0;
  width: 100%;
  overflow: hidden;
}
</style>
