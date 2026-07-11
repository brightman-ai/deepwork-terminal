import { createLogger } from '@ce/utils/obs'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

const log = createLogger('cli-input')
const QUERY_KEY = 'cli_diag'
const STORAGE_KEY = 'cli_input_diag'
const AUTH_STORAGE_KEY = 'cli_auth_code'
const FLUSH_INTERVAL_MS = 2_000
const MAX_BUFFER = 200
const MAX_CODE_POINTS = 8

// ── Server flush ──────────────────────────────────────────────────────────────
// Buffer events and POST to /debug/logs every FLUSH_INTERVAL_MS so they appear
// in the server's stderr without any DevTools required.
interface DiagEvent { ts: number; msg: string; [k: string]: unknown }
const _buf: DiagEvent[] = []
let _flushTimer: ReturnType<typeof setInterval> | null = null

function _startFlush(): void {
  if (_flushTimer !== null) return
  _flushTimer = setInterval(_flush, FLUSH_INTERVAL_MS)
}

async function _flush(): Promise<void> {
  if (_buf.length === 0) return
  const events = _buf.splice(0)
  const auth = typeof localStorage !== 'undefined' ? localStorage.getItem(AUTH_STORAGE_KEY) ?? '' : ''
  try {
    await fetch(cliApi('/debug/logs'), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CLI-Auth': auth,
        'X-Auth-Code': auth,
      },
      body: JSON.stringify({ events }),
    })
  } catch {
    // Best-effort — drop on network error.
  }
}

function _pushEvent(msg: string, ext: Record<string, unknown>): void {
  _buf.push({ ts: Date.now(), msg, ...ext })
  if (_buf.length > MAX_BUFFER) _buf.splice(0, _buf.length - MAX_BUFFER)
  _startFlush()
}
const OUTPUT_AFTER_SUBMIT_WINDOW_MS = 2_000
const MAX_OUTPUT_AFTER_SUBMIT_LOGS = 8
const OUTPUT_AFTER_SUBMIT_MIN_LOG_INTERVAL_MS = 250
const MAX_OUTPUT_WINDOW_CODEPOINTS = 4096

export interface TextSummary {
  len: number
  codes: string[]
  ascii: number
  cjk: number
  whitespace: number
}

type DiagnosticContext = Record<string, unknown>
type SessionIdGetter = () => string

export function isCliInputDiagnosticsEnabled(): boolean {
  if (typeof window === 'undefined') return false
  const params = new URLSearchParams(window.location.search)
  const query = params.get(QUERY_KEY)
  try {
    if (query === '1') {
      window.sessionStorage.setItem(STORAGE_KEY, '1')
      window.localStorage.removeItem(STORAGE_KEY)
      return true
    }
    if (query === '0') {
      window.localStorage.removeItem(STORAGE_KEY)
      window.sessionStorage.removeItem(STORAGE_KEY)
      return false
    }
    // Older builds persisted this flag in localStorage, so one diagnostic session could make
    // later normal terminal use slow on that origin. Treat it as stale and clear it.
    if (window.localStorage.getItem(STORAGE_KEY) === '1') {
      window.localStorage.removeItem(STORAGE_KEY)
      return false
    }
    return window.sessionStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return query === '1'
  }
}

export function summarizeText(value: string | null | undefined): TextSummary {
  const text = value ?? ''
  const codes: string[] = []
  let ascii = 0
  let cjk = 0
  let whitespace = 0

  for (const ch of text) {
    const code = ch.codePointAt(0) ?? 0
    if (codes.length < MAX_CODE_POINTS) codes.push(code.toString(16))
    if (code <= 0x7f) ascii++
    if (/\s/u.test(ch)) whitespace++
    if ((code >= 0x3400 && code <= 0x9fff) || (code >= 0xf900 && code <= 0xfaff)) cjk++
  }

  return { len: Array.from(text).length, codes, ascii, cjk, whitespace }
}

export function summarizeBytes(data: Uint8Array): TextSummary & { bytes: number } {
  let text = ''
  try {
    text = new TextDecoder().decode(data)
  } catch {
    text = ''
  }
  return { ...summarizeText(text), bytes: data.byteLength }
}

function viewportSnapshot() {
  if (typeof window === 'undefined') return {}
  const vv = window.visualViewport
  const root = document.documentElement
  const body = document.body
  return {
    innerH: window.innerHeight,
    innerW: window.innerWidth,
    vvH: vv ? Math.round(vv.height) : null,
    vvW: vv ? Math.round(vv.width) : null,
    vvTop: vv ? Math.round(vv.offsetTop) : null,
    vvLeft: vv ? Math.round(vv.offsetLeft) : null,
    vvScale: vv ? Number(vv.scale.toFixed(3)) : null,
    scrollY: Math.round(window.scrollY),
    rootScrollTop: Math.round(root.scrollTop),
    bodyScrollTop: Math.round(body.scrollTop),
    appViewportH: getComputedStyle(root).getPropertyValue('--dw-app-viewport-height').trim(),
  }
}

function elementSnapshot(element: HTMLElement | null | undefined) {
  if (!element) return {}
  const rect = element.getBoundingClientRect()
  const value = element instanceof HTMLTextAreaElement || element instanceof HTMLInputElement
    ? element.value
    : ''
  return {
    tag: element.tagName,
    cls: element.className,
    value: summarizeText(value),
    rectTop: Math.round(rect.top),
    rectBottom: Math.round(rect.bottom),
    rectH: Math.round(rect.height),
  }
}

function activeElementSnapshot() {
  return elementSnapshot(document.activeElement as HTMLElement | null)
}

export function reportCliInputDiagnostic(msg: string, ext: DiagnosticContext = {}): void {
  if (!isCliInputDiagnosticsEnabled()) return
  const ctx = { ...ext, viewport: viewportSnapshot(), active: activeElementSnapshot() }
  log.debug(msg, ctx)
  _pushEvent(msg, ctx)
}

function eventPayload(event: Event, element: HTMLElement, context: DiagnosticContext) {
  const payload: DiagnosticContext = {
    ...context,
    event: event.type,
    target: elementSnapshot(element),
  }

  if (event instanceof KeyboardEvent) {
    payload.key = summarizeText(event.key)
    payload.code = event.code
    payload.keyCode = event.keyCode
    payload.isComposing = event.isComposing
    payload.repeat = event.repeat
    payload.ctrl = event.ctrlKey
    payload.alt = event.altKey
    payload.meta = event.metaKey
    payload.shift = event.shiftKey
  } else if (event instanceof InputEvent) {
    payload.inputType = event.inputType
    payload.data = summarizeText(event.data)
    payload.isComposing = event.isComposing
  } else if (event instanceof CompositionEvent) {
    payload.data = summarizeText(event.data)
  } else if (event instanceof FocusEvent) {
    payload.related = elementSnapshot(event.relatedTarget as HTMLElement | null)
  }

  return payload
}

export function attachCliInputDiagnostics(
  element: HTMLElement | null | undefined,
  source: string,
  context: DiagnosticContext = {},
): () => void {
  if (!element || !isCliInputDiagnosticsEnabled()) return () => {}

  const eventTypes = [
    'focus',
    'blur',
    'keydown',
    'keyup',
    'beforeinput',
    'input',
    'compositionstart',
    'compositionupdate',
    'compositionend',
  ] as const

  const handler = (event: Event) => {
    reportCliInputDiagnostic('dom-event', eventPayload(event, element, { source, ...context }))
  }

  for (const type of eventTypes) element.addEventListener(type, handler, true)
  reportCliInputDiagnostic('attach', { source, ...context, target: elementSnapshot(element) })

  return () => {
    for (const type of eventTypes) element.removeEventListener(type, handler, true)
    reportCliInputDiagnostic('detach', { source, ...context })
  }
}

export interface CliTerminalInputTelemetryOptions {
  surface: string
  sessionId?: SessionIdGetter
}

export function useCliTerminalInputTelemetry(options: CliTerminalInputTelemetryOptions) {
  const decoder = new TextDecoder()
  let line = ''
  let framesSinceSubmit = 0
  let bytesSinceSubmit = 0
  let submitSeq = 0
  let lastSubmittedLine = ''
  let lastSubmitAt = 0
  let lastSubmitSeq = 0
  let outputLogsLeft = 0
  let outputWindowText = ''
  let outputChunks = 0
  let outputBytes = 0
  let outputLastLogAt = 0
  let outputOccurrences = 0

  function recordSend(data: Uint8Array, route = 'unknown'): void {
    if (data.byteLength === 0) return

    framesSinceSubmit++
    bytesSinceSubmit += data.byteLength

    let text = ''
    try {
      text = decoder.decode(data)
    } catch {
      text = ''
    }

    if (text.startsWith('\x1b')) {
      emitControl(route, data, 0x1b)
      return
    }

    let sawSubmit = false
    let sawControl = false
    for (const ch of text) {
      const code = ch.codePointAt(0) ?? 0
      if (ch === '\r' || ch === '\n') {
        emitSubmit(route, data)
        sawSubmit = true
        continue
      }
      if (ch === '\x7f') {
        line = Array.from(line).slice(0, -1).join('')
        sawControl = true
        continue
      }
      if (code > 0 && code < 0x20 && ch !== '\t') {
        emitControl(route, data, code)
        if (code === 0x03 || code === 0x15) resetLine()
        sawControl = true
        continue
      }
      line += ch
    }

    if (!sawSubmit && !sawControl && shouldLogTextFrame(text, data)) {
      // debug, not info: this fires per keystroke frame. At info it shipped to the
      // server telemetry log (minRemoteLevel=INFO) and flooded it (160MB / ~2 lines
      // per keypress). debug stays in the browser console for live input debugging
      // but is not persisted server-side. Raise minRemoteLevel/log level to capture.
      log.debug('cli.input.text_frame', {
        surface: options.surface,
        session_id: options.sessionId?.(),
        route,
        data: summarizeBytes(data),
        line: summarizeText(line),
        frames_since_submit: framesSinceSubmit,
        bytes_since_submit: bytesSinceSubmit,
      })
    }
  }

  function emitSubmit(route: string, data: Uint8Array): void {
    submitSeq++
    lastSubmittedLine = line
    lastSubmitAt = Date.now()
    lastSubmitSeq = submitSeq
    outputLogsLeft = MAX_OUTPUT_AFTER_SUBMIT_LOGS
    outputWindowText = ''
    outputChunks = 0
    outputBytes = 0
    outputLastLogAt = 0
    outputOccurrences = 0
    log.info('cli.input.submit', {
      surface: options.surface,
      session_id: options.sessionId?.(),
      route,
      submit_seq: submitSeq,
      line: summarizeText(line),
      data: summarizeBytes(data),
      frames_since_submit: framesSinceSubmit,
      bytes_since_submit: bytesSinceSubmit,
    })
    resetLine()
  }

  function recordOutput(data: Uint8Array, route = 'ws-binary'): void {
    if (data.byteLength === 0 || !lastSubmitAt || outputLogsLeft <= 0) return

    const elapsed = Date.now() - lastSubmitAt
    if (elapsed > OUTPUT_AFTER_SUBMIT_WINDOW_MS) return

    outputChunks++
    outputBytes += data.byteLength
    let text = ''
    try {
      text = decoder.decode(data)
    } catch {
      text = ''
    }
    if (text) {
      outputWindowText = trimCodepoints(outputWindowText + text, MAX_OUTPUT_WINDOW_CODEPOINTS)
    }
    const chunkOccurrences = lastSubmittedLine ? countOccurrences(text, lastSubmittedLine) : 0
    const windowOccurrences = lastSubmittedLine ? countOccurrences(outputWindowText, lastSubmittedLine) : 0
    const reason = outputLogReason(text, windowOccurrences)
    if (!reason) return

    outputLogsLeft--
    outputLastLogAt = Date.now()
    outputOccurrences = windowOccurrences
    // debug, not info — per-output-chunk diagnostic; see cli.input.text_frame note.
    log.debug('cli.output.after_submit', {
      surface: options.surface,
      session_id: options.sessionId?.(),
      route,
      submit_seq: lastSubmitSeq,
      reason,
      elapsed_ms: elapsed,
      data: summarizeBytes(data),
      submitted_line: summarizeText(lastSubmittedLine),
      output_window: summarizeText(outputWindowText),
      submitted_line_chunk_occurrences: chunkOccurrences,
      submitted_line_window_occurrences: windowOccurrences,
      chunks_since_submit: outputChunks,
      bytes_since_submit: outputBytes,
      logs_left: outputLogsLeft,
    })
  }

  function outputLogReason(text: string, windowOccurrences: number): string {
    if (outputChunks === 1) return 'first'
    if (windowOccurrences !== outputOccurrences) return 'submitted_line_occurrence_changed'
    if (text.includes('\r') || text.includes('\n')) return 'line_break'
    if (outputLastLogAt && Date.now() - outputLastLogAt >= OUTPUT_AFTER_SUBMIT_MIN_LOG_INTERVAL_MS) return 'interval'
    return ''
  }

  function emitControl(route: string, data: Uint8Array, code: number): void {
    // debug, not info — per control-key frame; see cli.input.text_frame note.
    log.debug('cli.input.control_frame', {
      surface: options.surface,
      session_id: options.sessionId?.(),
      route,
      control_code: code.toString(16),
      data: summarizeBytes(data),
      line: summarizeText(line),
      frames_since_submit: framesSinceSubmit,
      bytes_since_submit: bytesSinceSubmit,
    })
  }

  function resetLine(): void {
    line = ''
    framesSinceSubmit = 0
    bytesSinceSubmit = 0
  }

  return { recordSend, recordOutput }
}

function shouldLogTextFrame(text: string, data: Uint8Array): boolean {
  if (!text) return true
  const summary = summarizeText(text)
  return data.byteLength > 1 || summary.cjk > 0
}

function countOccurrences(text: string, needle: string): number {
  if (!text || !needle) return 0
  let count = 0
  let index = 0
  while (true) {
    index = text.indexOf(needle, index)
    if (index < 0) return count
    count++
    index += needle.length
  }
}

function trimCodepoints(text: string, limit: number): string {
  if (limit <= 0) return ''
  const chars = Array.from(text)
  if (chars.length <= limit) return text
  return chars.slice(chars.length - limit).join('')
}
