/**
 * useTerminalChannel — 标准化终端 WebSocket 通道。
 *
 * 对 useWebSocketClient 的轻量包装，暴露统一命名接口：
 * - connected / reconnecting (映射自 status)
 * - attach(terminal)         (绑定 xterm.js Terminal 实例，处理 PTY I/O)
 * - send(data)               (发送字符串输入)
 * - resize(cols, rows)       (发送 PTY resize 控制帧)
 * - agentState               (来自同一 WS 的 agent_state 消息)
 * - connect / disconnect     (生命周期)
 *
 * 不重复 WS 核心逻辑，所有 transport 细节保留在 useWebSocketClient。
 */
import { computed, type Ref } from 'vue'
import { Terminal } from 'xterm'
import { useWebSocketClient, type WebSocketClientOptions } from '@terminal/composables/cli/useWebSocketClient'
import { useAgentIntel } from '@terminal/composables/cli/useAgentIntel'
import type { AgentState, WSControlMessage } from '@terminal/types/terminal'

export interface TerminalChannelOptions {
  /** Reactive session ID (null = no active session) */
  sessionId: Ref<string | null>
  wsOptions?: WebSocketClientOptions
}

export function useTerminalChannel(options: TerminalChannelOptions) {
  const { sessionId, wsOptions } = options

  // ── 内部 composables ───────────────────────────────────────────────
  const wsClient = useWebSocketClient(
    () => sessionId.value ?? '',
    wsOptions,
  )

  const agentIntel = useAgentIntel(() => sessionId.value ?? '')

  // ── 对外状态 (标准化命名) ──────────────────────────────────────────

  /** true = WS 已建立且可用 */
  const connected = computed(() => wsClient.status.value === 'connected')

  /** true = 正在重连 */
  const reconnecting = computed(() => wsClient.status.value === 'reconnecting')

  // ── Terminal 绑定 ─────────────────────────────────────────────────

  /**
   * 将 xterm.js Terminal 实例绑定到此 channel。
   * - 注册 WS binary handler → terminal.write()
   * - 注册 WS control handler → 路由 shell_exit / agent_state
   * 应在 terminal "ready" 事件触发后调用。
   */
  function attach(terminal: Terminal) {
    wsClient.onMessage(
      (data: ArrayBuffer) => {
        terminal.write(new Uint8Array(data))
      },
      (msg: WSControlMessage) => {
        switch (msg.type) {
          case 'shell_exit': {
            const payload = msg.payload as Record<string, unknown>
            const exitCode = payload?.exit_code
            const code = typeof exitCode === 'number' ? exitCode : 0
            terminal.write('\r\n[进程已退出]\r\n')
            _onShellExit?.(code)
            break
          }
          case 'preempted':
            terminal.write('\r\n[Session 已被其他客户端接管]\r\n')
            break
          case 'agent_state':
            agentIntel.handleWSMessage(msg.payload)
            break
          case 'error':
            console.error('[TerminalChannel] WS error:', msg.payload)
            break
        }
      },
    )
  }

  // ── 可选回调 (attach 前可设置) ────────────────────────────────────

  let _onShellExit: ((code: number) => void) | null = null

  function onShellExit(cb: (code: number) => void) {
    _onShellExit = cb
  }

  // ── Terminal I/O ──────────────────────────────────────────────────

  const encoder = new TextEncoder()

  /** 发送字符串输入 (编码为 UTF-8 binary frame) */
  function send(data: string) {
    wsClient.sendBinary(encoder.encode(data))
  }

  /** 发送 PTY resize 控制帧 */
  function resize(cols: number, rows: number) {
    wsClient.sendResize(cols, rows)
  }

  // ── Lifecycle ─────────────────────────────────────────────────────

  function connect() {
    if (sessionId.value) wsClient.connect()
  }

  function disconnect() {
    wsClient.disconnect()
  }

  return {
    connected,
    reconnecting,
    rawStatus: wsClient.status,
    netStats: wsClient.netStats,

    attach,
    send,
    resize,

    agentState: agentIntel.agentState as Ref<AgentState | null>,
    agentNotifications: agentIntel.notifications,

    onShellExit,
    connect,
    disconnect,

    // 底层 client 透传 (供需要 sendBinary / sendControl 的高级用法)
    wsClient,
  }
}
