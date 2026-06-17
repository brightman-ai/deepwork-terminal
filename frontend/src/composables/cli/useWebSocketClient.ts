/**
 * useWebSocketClient — WebSocket client for terminal I/O.
 * Binary frames carry raw terminal data; Text/JSON frames carry control messages.
 * Supports reconnection, heartbeat with RTT measurement, and bandwidth tracking.
 * [Ref: CAP-terminal-io S3, DDC-02]
 */
import { ref, reactive, onUnmounted } from 'vue'
import type { WSConnectionStatus, WSControlMessage } from '@terminal/types/terminal'
import { wsUrl } from '@ce/utils/runtimeBase'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export interface WebSocketClientOptions {
  authToken?: string
  maxReconnectAttempts?: number
  /** Telemetry tick in ms — RTT ping + bandwidth/traffic sampling cadence (default 2000). */
  telemetryInterval?: number
  /** @deprecated kept for back-compat; superseded by telemetryInterval. */
  heartbeatInterval?: number
}

export interface NetStats {
  /** Round-trip time in ms (from last heartbeat) */
  rtt: number
  /** Upload throughput, true bytes/second (sampled each telemetry tick) */
  uploadBps: number
  /** Download throughput, true bytes/second (sampled each telemetry tick) */
  downloadBps: number
  /** Cumulative bytes sent on the CURRENT connection (reset on (re)open) */
  txTotal: number
  /** Cumulative bytes received on the CURRENT connection (reset on (re)open) */
  rxTotal: number
  /** Seconds elapsed since the current connection opened (10s granularity) */
  uptimeSec: number
}

export function useWebSocketClient(sessionId: () => string, opts: WebSocketClientOptions = {}) {
  // Initial status is the neutral 'connecting' (amber pulse), NOT 'disconnected' (red).
  // The first frame is "connection not yet established", not "connection lost" — starting
  // at 'disconnected' flashed a red "Disconnected" chip before connect() runs. We only fall
  // to 'disconnected' on an actual close/error (lines below).
  const status = ref<WSConnectionStatus>('connecting')
  const maxAttempts = opts.maxReconnectAttempts ?? 10
  // Unified telemetry tick. The bandwidth/traffic/uptime sampling is a LOCAL computation
  // (no network send), so running it at 2s never competes with keystrokes — it just reads
  // byte counters already accumulated from frames that flow anyway. The RTT ping is the ONLY
  // network send here, and it is GUARDED below (skipped within rttGuardMs of a keystroke), so
  // the [TH-0501-m9j] WKWebView keystroke safety holds while latency still refreshes ~2s when
  // idle (the ping also doubles as keep-alive). Zero added bytes on PTY data.
  const telemetryMs = opts.telemetryInterval ?? opts.heartbeatInterval ?? 2_000
  const rttGuardMs = 400
  let lastInputAt = 0
  let lastSampleAt = 0

  // Network stats (reactive for UI binding)
  const netStats = reactive<NetStats>({ rtt: 0, uploadBps: 0, downloadBps: 0, txTotal: 0, rxTotal: 0, uptimeSec: 0 })
  let connectedAt = 0

  let ws: WebSocket | null = null
  let reconnectAttempts = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let telemetryTimer: ReturnType<typeof setInterval> | null = null
  let wasPreempted = false
  let queuedBinaryBytes = 0
  const maxQueuedBinaryBytes = 64 * 1024
  const queuedBinary: Uint8Array[] = []

  // Bandwidth tracking
  let bytesSentInWindow = 0
  let bytesReceivedInWindow = 0

  // Callbacks
  let onBinaryMessage: ((data: ArrayBuffer) => void) | null = null
  let onControlMessage: ((msg: WSControlMessage) => void) | null = null

  function getWsUrl(): string {
    let url = wsUrl(cliApi(`/sessions/${sessionId()}/ws`))
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
      // Fresh connection → reset cumulative traffic + uptime baseline.
      netStats.txTotal = 0
      netStats.rxTotal = 0
      netStats.uptimeSec = 0
      connectedAt = Date.now()
      lastInputAt = 0
      startTelemetry()
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
      stopTelemetry()
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
    stopTelemetry()
    if (ws) { ws.close(1000, 'client disconnect'); ws = null }
    clearQueuedBinary()
    status.value = 'disconnected'
  }

  // [TH-0501-m9j] Direct synchronous binary WS send. No intermediate layers.
  function sendBinary(data: Uint8Array) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      lastInputAt = Date.now()
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

  // Unified telemetry tick (~2s): samples bandwidth/traffic/uptime from byte counters that
  // already flow (zero added bytes), then sends ONE guarded RTT ping. The ping is skipped
  // within rttGuardMs of a keystroke so it never competes with input on WKWebView; when idle
  // it also serves as keep-alive. RTT lands in netStats.rtt via the heartbeat_ack handler.
  function startTelemetry() {
    stopTelemetry()
    lastSampleAt = Date.now()
    pingRtt(Date.now()) // instant first RTT (user is not typing at connect)
    telemetryTimer = setInterval(() => {
      const now = Date.now()
      const elapsedMs = now - lastSampleAt
      lastSampleAt = now
      // True throughput in bytes/second (pure local compute, no network).
      if (elapsedMs > 0) {
        netStats.uploadBps = Math.round((bytesSentInWindow / elapsedMs) * 1000)
        netStats.downloadBps = Math.round((bytesReceivedInWindow / elapsedMs) * 1000)
      }
      netStats.txTotal += bytesSentInWindow
      netStats.rxTotal += bytesReceivedInWindow
      if (connectedAt > 0) netStats.uptimeSec = Math.floor((now - connectedAt) / 1000)
      bytesSentInWindow = 0
      bytesReceivedInWindow = 0
      // Guarded RTT ping — never within rttGuardMs of a keystroke (keystroke safety).
      if (now - lastInputAt > rttGuardMs) pingRtt(now)
    }, telemetryMs)
  }

  function pingRtt(now: number) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      sendControl({ type: 'heartbeat', payload: { sentAt: now } })
    }
  }

  function stopTelemetry() {
    if (telemetryTimer) { clearInterval(telemetryTimer); telemetryTimer = null }
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
