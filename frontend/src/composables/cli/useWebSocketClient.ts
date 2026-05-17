/**
 * useWebSocketClient — WebSocket client for terminal I/O.
 * Binary frames carry raw terminal data; Text/JSON frames carry control messages.
 * Supports reconnection, heartbeat with RTT measurement, and bandwidth tracking.
 * [Ref: CAP-terminal-io S3, DDC-02]
 */
import { ref, reactive, onUnmounted } from 'vue'
import type { WSConnectionStatus, WSControlMessage } from '@/types/terminal'
import { wsUrl } from '@/utils/runtimeBase'

export interface WebSocketClientOptions {
  authToken?: string
  maxReconnectAttempts?: number
  /** Heartbeat interval in ms (default 1000 for per-second RTT updates) */
  heartbeatInterval?: number
}

export interface NetStats {
  /** Round-trip time in ms (from last heartbeat) */
  rtt: number
  /** Upload bytes in the last second */
  uploadBps: number
  /** Download bytes in the last second */
  downloadBps: number
}

export function useWebSocketClient(sessionId: () => string, opts: WebSocketClientOptions = {}) {
  const status = ref<WSConnectionStatus>('disconnected')
  const maxAttempts = opts.maxReconnectAttempts ?? 10
  // [TH-0501-m9j 铁律 v2.0] WKWebView drops keystrokes during JS busy loops.
  // 1s heartbeat was competing with keyboard events. 30s is sufficient for
  // keep-alive (TCP idle timeout is typically 60-120s).
  const heartbeatMs = opts.heartbeatInterval ?? 30_000

  // Network stats (reactive for UI binding)
  const netStats = reactive<NetStats>({ rtt: 0, uploadBps: 0, downloadBps: 0 })

  let ws: WebSocket | null = null
  let reconnectAttempts = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let heartbeatTimer: ReturnType<typeof setInterval> | null = null
  let wasPreempted = false
  let queuedBinaryBytes = 0
  const maxQueuedBinaryBytes = 64 * 1024
  const queuedBinary: Uint8Array[] = []

  // Bandwidth tracking
  let bytesSentInWindow = 0
  let bytesReceivedInWindow = 0
  let bandwidthTimer: ReturnType<typeof setInterval> | null = null

  // Callbacks
  let onBinaryMessage: ((data: ArrayBuffer) => void) | null = null
  let onControlMessage: ((msg: WSControlMessage) => void) | null = null

  function getWsUrl(): string {
    let url = wsUrl(`/api/sessions/${sessionId()}/ws`)
    const token = opts.authToken || localStorage.getItem('cli_auth_code') || ''
    if (token) url += `?auth=${encodeURIComponent(token)}`
    return url
  }

  function connect() {
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return
    status.value = reconnectAttempts > 0 ? 'reconnecting' : 'connecting'

    ws = new WebSocket(getWsUrl())
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      status.value = 'connected'
      reconnectAttempts = 0
      startHeartbeat()
      startBandwidthTracker()
      flushQueuedBinary()
    }

    ws.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        bytesReceivedInWindow += event.data.byteLength
        onBinaryMessage?.(event.data)
      } else if (typeof event.data === 'string') {
        bytesReceivedInWindow += event.data.length
        try {
          const msg: WSControlMessage = JSON.parse(event.data)
          if (msg.type === 'preempted') wasPreempted = true

          // Handle heartbeat_ack for RTT measurement
          if (msg.type === 'heartbeat_ack' && msg.payload) {
            const payload = msg.payload as Record<string, unknown>
            const sentAt = payload.sentAt as number
            if (sentAt) {
              netStats.rtt = Date.now() - sentAt
            }
          }

          onControlMessage?.(msg)
        } catch { /* ignore malformed */ }
      }
    }

    ws.onclose = () => {
      stopHeartbeat()
      stopBandwidthTracker()
      if (wasPreempted) {
        status.value = 'preempted'
        wasPreempted = false
        return
      }
      status.value = 'disconnected'
      netStats.rtt = 0
      netStats.uploadBps = 0
      netStats.downloadBps = 0
      scheduleReconnect()
    }

    ws.onerror = () => { /* followed by onclose */ }
  }

  function reconnect() {
    wasPreempted = false
    reconnectAttempts = 0
    if (ws) { ws.close(1000, 'manual reconnect'); ws = null }
    connect()
  }

  function disconnect() {
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null }
    reconnectAttempts = maxAttempts
    stopHeartbeat()
    stopBandwidthTracker()
    if (ws) { ws.close(1000, 'client disconnect'); ws = null }
    clearQueuedBinary()
    status.value = 'disconnected'
  }

  // [TH-0501-m9j] Direct synchronous binary WS send. No intermediate layers.
  function sendBinary(data: Uint8Array) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      bytesSentInWindow += data.byteLength
      ws.send(data)
    } else if (ws && ws.readyState === WebSocket.CONNECTING) {
      queueBinary(data)
    }
  }

  function queueBinary(data: Uint8Array) {
    if (data.byteLength <= 0) return
    if (queuedBinaryBytes + data.byteLength > maxQueuedBinaryBytes) {
      return
    }
    const copy = new Uint8Array(data.byteLength)
    copy.set(data)
    queuedBinary.push(copy)
    queuedBinaryBytes += copy.byteLength
  }

  function flushQueuedBinary() {
    if (!ws || ws.readyState !== WebSocket.OPEN || queuedBinary.length === 0) return
    while (queuedBinary.length > 0 && ws.readyState === WebSocket.OPEN) {
      const data = queuedBinary.shift()
      if (!data) continue
      queuedBinaryBytes -= data.byteLength
      bytesSentInWindow += data.byteLength
      ws.send(data)
    }
    if (queuedBinary.length === 0) queuedBinaryBytes = 0
  }

  function clearQueuedBinary() {
    queuedBinary.length = 0
    queuedBinaryBytes = 0
  }

  function sendControl(msg: WSControlMessage) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      const json = JSON.stringify(msg)
      bytesSentInWindow += json.length
      ws.send(json)
    }
  }

  function sendResize(cols: number, rows: number) {
    sendControl({ type: 'resize', payload: { cols, rows } })
  }

  function scheduleReconnect() {
    if (reconnectAttempts >= maxAttempts) return
    const delay = Math.min(1000 * Math.pow(2, reconnectAttempts), 30000)
    reconnectAttempts++
    reconnectTimer = setTimeout(connect, delay)
  }

  // Heartbeat with timestamp for RTT
  function startHeartbeat() {
    stopHeartbeat()
    heartbeatTimer = setInterval(() => {
      sendControl({ type: 'heartbeat', payload: { sentAt: Date.now() } })
    }, heartbeatMs)
  }

  function stopHeartbeat() {
    if (heartbeatTimer) { clearInterval(heartbeatTimer); heartbeatTimer = null }
  }

  // Bandwidth: sample every 10s (was 1s — WKWebView keystroke loss).
  // [TH-0501-m9j 铁律 v2.0 Rule 5]
  function startBandwidthTracker() {
    stopBandwidthTracker()
    bandwidthTimer = setInterval(() => {
      netStats.uploadBps = bytesSentInWindow
      netStats.downloadBps = bytesReceivedInWindow
      bytesSentInWindow = 0
      bytesReceivedInWindow = 0
    }, 10_000)
  }

  function stopBandwidthTracker() {
    if (bandwidthTimer) { clearInterval(bandwidthTimer); bandwidthTimer = null }
  }

  function onMessage(binaryHandler: (data: ArrayBuffer) => void, controlHandler?: (msg: WSControlMessage) => void) {
    onBinaryMessage = binaryHandler
    if (controlHandler) onControlMessage = controlHandler
  }

  onUnmounted(disconnect)

  return {
    status,
    netStats,
    connect,
    reconnect,
    disconnect,
    sendBinary,
    sendControl,
    sendResize,
    onMessage,
  }
}
