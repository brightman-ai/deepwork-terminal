/**
 * Frontend observability — deepwork TS-OBS frontend pillar.
 *
 * Design principles:
 *   1. TID is explicit, not global — no race condition across panels
 *   2. Single public API — createLogger, createTrace, traceHeaders
 *   3. Security — TID format validation, sensitive key redaction, injection-proof headers
 *   4. Performance — zero blocking, microtask remote sink, no user-perceived latency
 *
 * Troubleshooting flow:
 *   1. User sees error → DevTools console → find TID in structured JSON log
 *   2. jq 'select(.tid=="<TID>")' deepwork.log → full backend story
 *
 * SSE model: SSE connections use `sse_tid` query param (connection lifecycle trace),
 * distinct from `X-Trace-ID` (action/request trace).
 */

import { apiUrl } from './runtimeBase'

// --- Constants ---

const TRACE_HEADER = 'X-Trace-ID'
const SSE_TID_PARAM = 'sse_tid'
const TID_RE = /^[a-z0-9]{8}$/
const STG_RE = /^[a-z0-9][a-z0-9/_-]{0,63}$/

// Security: redact keys that may contain secrets
const SENSITIVE_KEY_RE = /(authorization|cookie|token|secret|password|passwd|api[-_]?key|session[-_]?id)/i
const MAX_STRING_LEN = 256
const MAX_OBJECT_KEYS = 20
const MAX_DEPTH = 2

// --- Types ---

export type LogLevel = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR'

export interface LogEntry {
  l: LogLevel
  t: string
  tid?: string
  stg?: string
  mod: string
  msg: string
  ext?: Record<string, unknown>
}

interface IngestEnvelope {
  entries: LogEntry[]
}

interface RemoteSinkConfig {
  endpoint: string
  batchIntervalMs: number
  batchMaxEntries: number
  minRemoteLevel: LogLevel
  enabled: boolean
  bridgeConsole: boolean
}

// --- Level filtering ---

const LEVEL_ORDER: Record<LogLevel, number> = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 }
let currentLevel: LogLevel = 'DEBUG'
let remoteSink: RemoteSinkConfig = {
  endpoint: '/api/telemetry/log',
  batchIntervalMs: 500,
  batchMaxEntries: 50,
  minRemoteLevel: 'INFO',
  enabled: true,
  bridgeConsole: true,
}
let remoteTimer: ReturnType<typeof setTimeout> | null = null
let remoteInstalled = false
let consoleBridgeInstalled = false
const remoteQueue: LogEntry[] = []
const nativeConsole = {
  debug: typeof console !== 'undefined' && typeof console.debug === 'function'
    ? console.debug.bind(console)
    : (...args: unknown[]) => console.log(...args),
  info: typeof console !== 'undefined' && typeof console.info === 'function'
    ? console.info.bind(console)
    : (...args: unknown[]) => console.log(...args),
  log: typeof console !== 'undefined' && typeof console.log === 'function'
    ? console.log.bind(console)
    : (() => {}) as (...args: unknown[]) => void,
  warn: typeof console !== 'undefined' && typeof console.warn === 'function'
    ? console.warn.bind(console)
    : (...args: unknown[]) => console.log(...args),
  error: typeof console !== 'undefined' && typeof console.error === 'function'
    ? console.error.bind(console)
    : (...args: unknown[]) => console.log(...args),
}

/** Set the minimum log level. Messages below this level are silently dropped. */
export function setLogLevel(level: LogLevel): void {
  currentLevel = level
}

export function configureRemoteSink(config: Partial<RemoteSinkConfig> = {}): void {
  remoteSink = { ...remoteSink, ...config }
  installRemoteLifecycleHooks()
  installConsoleBridge()
}

export interface TraceContext {
  readonly tid: string
  readonly stg: string
}

// --- TID Generation ---

function randomAlphaNum(n: number): string {
  const a = '0123456789abcdefghijklmnopqrstuvwxyz'
  const bytes = new Uint8Array(n * 2) // over-allocate for rejection sampling
  crypto.getRandomValues(bytes)
  let s = '', i = 0
  while (s.length < n) {
    if (i >= bytes.length) { crypto.getRandomValues(bytes); i = 0 }
    const v = bytes[i++] & 63 // 6-bit mask: 0-63
    if (v < 36) s += a[v]     // reject 36-63 → perfectly uniform over 36 chars
  }
  return s
}

/** Generate a short TID (8 chars, alphanumeric). */
export function generateTID(): string {
  return randomAlphaNum(8)
}

/** Validate TID format. Use before trusting external input. */
export function isValidTID(value: unknown): value is string {
  return typeof value === 'string' && TID_RE.test(value)
}

// --- TraceContext (explicit, no global state) ---

/**
 * Create a TraceContext for a user action boundary.
 * Each action gets its own TID — no global state, no race condition.
 *
 * Usage:
 *   const trace = createTrace('council/round/start')
 *   await fetchWithTrace('/api/...', { method: 'POST', body }, trace)
 *   log.info('round started', { roundId }, trace)
 */
export function createTrace(stg: string, tid?: string): TraceContext {
  if (stg && !STG_RE.test(stg)) {
    throw new Error(`[obs] invalid STG: "${stg}" — must match ${STG_RE}`)
  }
  return Object.freeze({
    tid: tid && isValidTID(tid) ? tid : generateTID(),
    stg,
  })
}

/** Create a child trace with a different STG but same TID. */
export function withStage(trace: TraceContext, stg: string): TraceContext {
  if (stg && !STG_RE.test(stg)) {
    throw new Error(`[obs] invalid STG: "${stg}" — must match ${STG_RE}`)
  }
  return Object.freeze({ tid: trace.tid, stg })
}

// --- HTTP Headers ---

/**
 * Build trace headers for API calls. TID is written last — cannot be overridden.
 * Returns plain object compatible with both fetch() and axios.
 */
export function traceHeaders(trace?: TraceContext, existing?: Record<string, string>): Record<string, string> {
  const headers: Record<string, string> = { ...(existing || {}) }
  // Remove any case variant of trace header — prevents duplicate keys across case forms
  for (const k of Object.keys(headers)) {
    if (k.toLowerCase() === 'x-trace-id') delete headers[k]
  }
  if (trace?.tid) {
    headers[TRACE_HEADER] = trace.tid  // canonical case, injection-proof
  }
  return headers
}

/**
 * Wrap fetch() with automatic trace header injection.
 *
 * Usage:
 *   const trace = createTrace('browser/navigate')
 *   const res = await tracedFetch('/api/browser/navigate', { method: 'POST', body }, trace)
 */
export async function tracedFetch(
  input: RequestInfo | URL,
  init: RequestInit = {},
  trace?: TraceContext,
): Promise<Response> {
  const merged = traceHeaders(trace, toPlainHeaders(init.headers))
  const target = typeof input === 'string' ? apiUrl(input) : input
  return fetch(target, { ...init, headers: merged })
}

/** Convert any HeadersInit to plain Record for merging. */
function toPlainHeaders(h?: HeadersInit): Record<string, string> {
  if (!h) return {}
  if (h instanceof Headers) {
    const out: Record<string, string> = {}
    h.forEach((v, k) => { out[k] = v })
    return out
  }
  if (Array.isArray(h)) {
    const out: Record<string, string> = {}
    for (const [k, v] of h) out[k] = v
    return out
  }
  return { ...h }
}

// --- Axios Integration ---

/**
 * Install trace interceptor on an Axios instance.
 * After this, any request with `{ trace }` config will inject X-Trace-ID.
 *
 * Usage:
 *   import { api } from '@ce/boot/axios'
 *   installAxiosTrace(api)
 *
 *   // Then in components:
 *   api.post('/sessions', payload, { trace })
 */
export function installAxiosTrace(axiosInstance: any): void {
  axiosInstance.interceptors.request.use((config: any) => {
    const trace = config.trace as TraceContext | undefined
    if (trace?.tid) {
      if (!config.headers) config.headers = {}
      config.headers[TRACE_HEADER] = trace.tid
    }
    return config
  })
}

// --- SSE Integration ---

/**
 * Build SSE URL with trace query parameter.
 * SSE connections use `sse_tid` (connection lifecycle), not `X-Trace-ID` (action).
 *
 * Usage:
 *   const trace = createTrace('sse/council')
 *   const url = buildSSEUrl('/api/events', { sessionId, trace })
 *   const es = new EventSource(url)
 */
export function buildSSEUrl(
  path: string,
  options: { sessionId?: string | number | null; trace?: TraceContext } = {},
): string {
  const [basePath, existingQS] = path.split('?', 2)
  const params = new URLSearchParams(existingQS || '')
  if (options.sessionId != null && options.sessionId !== '') {
    params.set('session_id', String(options.sessionId))
  }
  if (options.trace?.tid) {
    params.set(SSE_TID_PARAM, options.trace.tid)
  }
  const qs = params.toString()
  return qs ? `${basePath}?${qs}` : basePath
}

// --- Structured Logging ---

/** Sanitize a value for safe logging (redact secrets, truncate strings). */
function sanitizeValue(value: unknown, depth: number): unknown {
  if (value == null) return value
  if (depth > MAX_DEPTH) return '[truncated]'
  if (typeof value === 'string') {
    return value.length > MAX_STRING_LEN ? value.slice(0, MAX_STRING_LEN) + '...' : value
  }
  if (typeof value === 'number' || typeof value === 'boolean') return value
  if (Array.isArray(value)) {
    return value.slice(0, MAX_OBJECT_KEYS).map(item => sanitizeValue(item, depth + 1))
  }
  if (typeof value === 'object') {
    const out: Record<string, unknown> = {}
    let count = 0
    for (const [key, raw] of Object.entries(value as Record<string, unknown>)) {
      if (count++ >= MAX_OBJECT_KEYS) break
      out[key] = SENSITIVE_KEY_RE.test(key) ? '[redacted]' : sanitizeValue(raw, depth + 1)
    }
    return out
  }
  return String(value)
}

function sanitizeExt(ext?: Record<string, unknown>): Record<string, unknown> | undefined {
  if (!ext || Object.keys(ext).length === 0) return undefined
  return sanitizeValue(ext, 0) as Record<string, unknown>
}

function emit(
  level: LogLevel,
  mod: string,
  msg: string,
  ext?: Record<string, unknown>,
  trace?: TraceContext,
): void {
  if (LEVEL_ORDER[level] < LEVEL_ORDER[currentLevel]) return

  const entry: LogEntry = {
    l: level,
    t: new Date().toISOString(),
    mod,
    msg,
  }
  if (trace?.tid) entry.tid = trace.tid
  if (trace?.stg) entry.stg = trace.stg

  const safeExt = sanitizeExt(ext)
  if (safeExt) entry.ext = safeExt

  const json = JSON.stringify(entry)
  switch (level) {
    case 'ERROR': nativeConsole.error(json); break
    case 'WARN':  nativeConsole.warn(json); break
    case 'DEBUG': nativeConsole.debug(json); break
    default:      nativeConsole.log(json)
  }

  enqueueRemote(entry)
}

function shouldRemote(entry: LogEntry): boolean {
  if (!remoteSink.enabled) return false
  return LEVEL_ORDER[entry.l] >= LEVEL_ORDER[remoteSink.minRemoteLevel]
}

function installRemoteLifecycleHooks(): void {
  if (remoteInstalled || typeof window === 'undefined') return
  remoteInstalled = true

  window.addEventListener('pagehide', () => {
    flushRemote(true)
  })

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      flushRemote(true)
    }
  })
}

function installConsoleBridge(): void {
  if (
    consoleBridgeInstalled ||
    !remoteSink.bridgeConsole ||
    typeof window === 'undefined' ||
    typeof console === 'undefined'
  ) {
    return
  }
  consoleBridgeInstalled = true

  const bridge = (
    method: 'debug' | 'info' | 'log' | 'warn' | 'error',
    level: LogLevel,
    nativeWrite: (...args: unknown[]) => void,
  ) => {
    console[method] = (...args: unknown[]) => {
      nativeWrite(...args)
      bridgeConsoleCall(level, args)
    }
  }

  bridge('debug', 'DEBUG', nativeConsole.debug)
  bridge('info', 'INFO', nativeConsole.info)
  bridge('log', 'INFO', nativeConsole.log)
  bridge('warn', 'WARN', nativeConsole.warn)
  bridge('error', 'ERROR', nativeConsole.error)
}

function bridgeConsoleCall(level: LogLevel, args: unknown[]): void {
  const parsed = parseConsoleArgs(args)
  emit(level, parsed.mod, parsed.msg, parsed.ext)
}

function parseConsoleArgs(args: unknown[]): {
  mod: string
  msg: string
  ext?: Record<string, unknown>
} {
  if (args.length === 0) {
    return { mod: 'console', msg: 'console call' }
  }

  const first = args[0]
  const firstText = consoleArgText(first)
  let mod = 'console'
  let msg = firstText || 'console call'

  const match = msg.match(/^\[([^\]]+)\]\s*(.*)$/)
  if (match) {
    mod = match[1] || mod
    msg = match[2] || match[1] || msg
  }

  const rest = args.slice(1).map((arg) => sanitizeValue(arg, 0))
  const ext: Record<string, unknown> = {}
  if (rest.length > 0) {
    ext.args = rest
  }
  if (first instanceof Error) {
    ext.error = sanitizeValue({ message: first.message, stack: first.stack }, 0)
  }
  return { mod, msg, ext: Object.keys(ext).length > 0 ? ext : undefined }
}

function consoleArgText(value: unknown): string {
  if (typeof value === 'string') return value
  if (value instanceof Error) return value.message || 'error'
  if (value == null) return String(value)
  if (typeof value === 'object') {
    try {
      return JSON.stringify(sanitizeValue(value, 0))
    } catch {
      return '[object]'
    }
  }
  return String(value)
}

function enqueueRemote(entry: LogEntry): void {
  if (!shouldRemote(entry)) return

  installRemoteLifecycleHooks()

  if (entry.l === 'WARN' || entry.l === 'ERROR') {
    // Runtime WARN/ERROR should arrive promptly in TS-OBS. Beacon is reserved for
    // unload/background flush because some browser runtimes treat it as best-effort.
    sendEnvelope({ entries: [entry] }, false)
    return
  }

  remoteQueue.push(entry)
  if (remoteQueue.length > remoteSink.batchMaxEntries) {
    remoteQueue.splice(0, remoteQueue.length - remoteSink.batchMaxEntries)
  }
  if (remoteTimer != null) return

  remoteTimer = setTimeout(() => {
    remoteTimer = null
    flushRemote(false)
  }, remoteSink.batchIntervalMs)
}

function flushRemote(sync: boolean): void {
  if (remoteTimer != null) {
    clearTimeout(remoteTimer)
    remoteTimer = null
  }
  if (remoteQueue.length === 0) return
  const entries = remoteQueue.splice(0, remoteQueue.length)
  sendEnvelope({ entries }, sync)
}

function sendEnvelope(envelope: IngestEnvelope, preferBeacon: boolean): void {
  const body = JSON.stringify(envelope)

  if (preferBeacon && typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
    const ok = navigator.sendBeacon(
      remoteSink.endpoint,
      new Blob([body], { type: 'application/json' }),
    )
    if (ok) return
  }

  void fetch(apiUrl(remoteSink.endpoint), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
    keepalive: true,
  }).catch(() => {
    // Observability must never block user actions.
  })
}

/**
 * Create a module-scoped logger.
 * Convention: mod = Vue component or composable name.
 *
 * Usage:
 *   const log = createLogger('CouncilChatPage')
 *   const trace = createTrace('council/round/start')
 *   log.info('round started', { roundId }, trace)
 */
export function createLogger(mod: string) {
  return {
    debug: (msg: string, ext?: Record<string, unknown>, trace?: TraceContext) =>
      emit('DEBUG', mod, msg, ext, trace),
    info: (msg: string, ext?: Record<string, unknown>, trace?: TraceContext) =>
      emit('INFO', mod, msg, ext, trace),
    warn: (msg: string, ext?: Record<string, unknown>, trace?: TraceContext) =>
      emit('WARN', mod, msg, ext, trace),
    error: (msg: string, ext?: Record<string, unknown>, trace?: TraceContext) =>
      emit('ERROR', mod, msg, ext, trace),
  }
}
