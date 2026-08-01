<template>
  <div class="xterm-root" data-testid="terminal-surface" @click="focusTerminal">
    <div ref="terminalContainer" class="xterm-container" />
    <!-- Proxy textarea: mobile fallback for xterm input. Disabled on desktop
         WKWebView where it competes with xterm's built-in textarea, causing
         keystroke loss. [Ref: TH-0501-m9j root cause] -->
    <textarea
      v-if="!props.disableProxy"
      ref="terminalInputProxy"
      class="terminal-input-proxy"
      data-testid="terminal-input"
      aria-label="Terminal input"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      spellcheck="false"
      @input="onProxyInput"
      @keydown="onProxyKeydown"
    />
    <pre class="terminal-transcript" data-testid="terminal-transcript" aria-live="polite">{{ transcript }}</pre>
  </div>
</template>

<script setup lang="ts">
/**
 * XtermTerminal — xterm.js wrapper with WebSocket I/O.
 * Binary frames carry raw terminal data; zero modification to xterm.js (IR-01).
 * [Ref: CAP-terminal-io S2-3]
 */
import { nextTick, ref, onMounted, onUnmounted, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon, type ISearchOptions } from '@xterm/addon-search'
import { WebglAddon } from '@xterm/addon-webgl'
import type { TerminalFindOptions } from './terminalSearchOptions'
import { canMeasureTerminal } from './terminalFit'
import { noteRenderer, noteContextLost, noteRenderMetrics } from '@terminal/composables/cli/renderHealth'
import {
  attachCliInputDiagnostics,
  reportCliInputDiagnostic,
  reportServerEvent,
  summarizeText,
} from '@terminal/composables/cli/useCliInputDiagnostics'
import { useXtermKeyboardFallback } from '@terminal/composables/cli/useXtermKeyboardFallback'
import { clearXtermHelperTextareaValue } from '@terminal/composables/cli/useXtermHelperTextarea'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { createLogger } from '@ce/utils/obs'
import { createRenderMetrics, type RenderMetrics } from '@terminal/composables/cli/terminalRenderMetrics'
import { renderSyncEnabled, renderSyncOverride } from '@terminal/composables/cli/terminalRenderSync'
import '@xterm/xterm/css/xterm.css'

const props = defineProps<{
  /** Whether the terminal is active/visible */
  active?: boolean
  /** Disable proxy textarea (desktop WKWebView — proxy competes with xterm for input) */
  disableProxy?: boolean
  /** Enable mobile helper-keydown fallback for third-party IMEs that skip xterm onData */
  imeFallbackEnabled?: boolean
  /** Diagnostic surface label used by CLI input telemetry */
  diagnosticSurface?: string
}>()

const emit = defineEmits<{
  (e: 'data', data: Uint8Array): void
  (e: 'resize', cols: number, rows: number): void
  (e: 'ready', terminal: Terminal): void
}>()

// Mobile uses a smaller cell so the narrow phone screen fits MORE columns. Programs that draw
// inside the terminal (e.g. Claude Code's file-update diff: a left line-number gutter + indent)
// then get a wider content area, so less of the row is wasted as left/right margin and more of
// the file actually shows. Desktop keeps 14 for comfortable reading. isMobile is only resolved
// in useDeviceDetection's onMounted, so we read it when CONSTRUCTING the terminal (also onMounted,
// registered later) — NOT at setup time, where it would still be the false default.
const { isMobile } = useDeviceDetection()

const terminalContainer = ref<HTMLDivElement>()
const terminalInputProxy = ref<HTMLTextAreaElement>()
const transcript = ref('')
// Search (in-terminal find, [Ref: TerminalSearchBar]). resultIndex is 0-based, -1 = no active
// match (empty query, or the addon hasn't computed results for this term/options yet — see
// buildSearchOptions: results are only tracked when `decorations` is passed).
const searchResultIndex = ref(-1)
const searchResultCount = ref(0)
let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let searchAddon: SearchAddon | null = null
// Held so teardown can release the GPU context explicitly, and so a context loss can null it out.
let webglAddon: WebglAddon | null = null
// Client-side cost of the bytes this terminal receives. Created with the terminal so a summary is
// always attributable to one surface, one renderer, one grid size.
let renderMetrics: RenderMetrics | null = null
// Whether this terminal forces full-grid repaints. Resolved once, when the renderer is known —
// see terminalRenderSync for why it is the renderer that decides.
let renderSyncOn = true
let searchResultsSub: { dispose(): void } | null = null
let resizeObserver: ResizeObserver | null = null
let resizeDebounce: ReturnType<typeof setTimeout> | null = null
let renderSyncRaf = 0
let renderSyncTrailing: ReturnType<typeof setTimeout> | null = null
let renderSyncBurstReset: ReturnType<typeof setTimeout> | null = null
let renderSyncBurstTotal = 0
let renderSyncLastRefreshAt = 0
const diagnosticCleanups: Array<() => void> = []
const encoder = new TextEncoder()
const decoder = new TextDecoder()
const renderSyncDecoder = new TextDecoder()
const transcriptLimit = 24000
const renderSyncLargeWriteBytes = 4096
const renderSyncBurstBytes = 8192
const renderSyncBurstWindowMs = 120
const renderSyncMinIntervalMs = 120
const renderSyncTrailingMs = 80
const isWKWebView = typeof navigator !== 'undefined' && navigator.userAgent.includes('AppleWebKit') &&
  !navigator.userAgent.includes('Chrome') &&
  !navigator.userAgent.includes('Safari')
const xtermKeydownFallback = useXtermKeyboardFallback({
  surface: props.diagnosticSurface ?? 'terminal',
  enabled: () => props.active !== false && props.imeFallbackEnabled === true && !isWKWebView,
  send: (data) => emit('data', data),
})

function configureInputAnchor(): HTMLTextAreaElement | null {
  const textarea = terminal?.element?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
  if (!textarea) return null
  textarea.removeAttribute('data-testid')
  textarea.setAttribute('aria-label', 'Terminal screen input')
  textarea.setAttribute('autocomplete', 'off')
  textarea.setAttribute('autocorrect', 'off')
  textarea.setAttribute('autocapitalize', 'off')
  textarea.setAttribute('spellcheck', 'false')
  return textarea
}

function focusTerminal() {
  terminal?.focus()
}

// The transcript is the screen-reader/e2e mirror of the terminal (sr-only, aria-live="polite").
// Decoding must stay per-frame — `decoder` is stateful, so a multi-byte glyph split across two PTY
// reads only survives if every frame passes through it in order — but the reactive WRITE is
// batched, because that write is what costs:
//
//   every frame → new 24 000-char string → Vue patch → the browser re-lays-out a `white-space:
//   pre-wrap` node inside a 1px-wide sr-only box, where 24 000 chars wrap into ~24 000 line boxes.
//
// At an agent TUI's frame rate that is the most expensive thing on the main thread per byte, and
// none of it is visible to anyone. Coalescing to ~120ms keeps the mirror's CONTENT identical (same
// bytes, same order, same cap) while collapsing many layouts into one — and a polite live region
// is supposed to be announced in settled chunks anyway, so batching is also the correct a11y
// behaviour, not a compromise of it.
const transcriptFlushMs = 120
let transcriptPending = ''
let transcriptFlushTimer: ReturnType<typeof setTimeout> | null = null

function flushTranscript() {
  transcriptFlushTimer = null
  if (!transcriptPending) return
  transcript.value = (transcript.value + transcriptPending).slice(-transcriptLimit)
  transcriptPending = ''
}

function appendTranscript(data: string | Uint8Array) {
  const raw = typeof data === 'string' ? data : decoder.decode(data, { stream: true })
  const clean = raw
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '')
    .replace(/\x1b\][^\x07]*(\x07|\x1b\\)/g, '')
    .replace(/\r/g, '\n')
  if (!clean) return
  transcriptPending = (transcriptPending + clean).slice(-transcriptLimit)
  if (!transcriptFlushTimer) transcriptFlushTimer = setTimeout(flushTranscript, transcriptFlushMs)
}

function dataLength(data: string | Uint8Array): number {
  return typeof data === 'string' ? data.length : data.byteLength
}

function hasEscapeByte(data: string | Uint8Array): boolean {
  return typeof data === 'string' ? data.includes('\x1b') : data.includes(0x1b)
}

function decodeForRenderSyncScan(data: string | Uint8Array): string {
  return typeof data === 'string' ? data : renderSyncDecoder.decode(data)
}

function containsFullscreenRedrawSequence(data: string | Uint8Array): boolean {
  const raw = decodeForRenderSyncScan(data)
  return /\x1b\[\?25[lh]/.test(raw)
    || /\x1b\[(?:2J|3J|[0-9;]*K|[0-9;]+[Hf]|[0-9;]+r)/.test(raw)
    || /\x1b\[\?(?:1049|47|1047)[hl]/.test(raw)
}

function shouldSyncRenderAfterWrite(data: string | Uint8Array): boolean {
  const len = dataLength(data)
  if (len >= renderSyncLargeWriteBytes) return true
  renderSyncBurstTotal += len
  if (renderSyncBurstReset) clearTimeout(renderSyncBurstReset)
  renderSyncBurstReset = setTimeout(() => {
    renderSyncBurstReset = null
    renderSyncBurstTotal = 0
  }, renderSyncBurstWindowMs)
  if (renderSyncBurstTotal >= renderSyncBurstBytes) {
    renderSyncBurstTotal = 0
    return true
  }
  if (!hasEscapeByte(data)) return false
  return containsFullscreenRedrawSequence(data)
}

function refreshVisibleRows() {
  if (!terminal || terminal.rows <= 0) return
  // Counted, because this is a WHOLE-GRID repaint on top of the damage-driven one xterm already
  // did — and its trigger (see containsFullscreenRedrawSequence) matches a bare erase-line, which
  // is in almost every frame a TUI emits. Whether that is worth its cost is a measurement, not an
  // opinion; renderMetrics is where the answer accumulates.
  renderMetrics?.noteForcedRepaint()
  terminal.refresh(0, terminal.rows - 1)
}

function scheduleRenderSyncRefresh() {
  const now = Date.now()
  if (!renderSyncRaf && now - renderSyncLastRefreshAt >= renderSyncMinIntervalMs) {
    renderSyncRaf = window.requestAnimationFrame(() => {
      renderSyncRaf = 0
      renderSyncLastRefreshAt = Date.now()
      refreshVisibleRows()
    })
  }
  if (renderSyncTrailing) clearTimeout(renderSyncTrailing)
  renderSyncTrailing = setTimeout(() => {
    renderSyncTrailing = null
    refreshVisibleRows()
  }, renderSyncTrailingMs)
}

function sendProxyText(text: string) {
  if (!text) return
  emit('data', encoder.encode(text))
}

function onProxyInput(event: Event) {
  const target = event.target as HTMLTextAreaElement | null
  if (!target) return
  const value = target.value
  if (!value) return
  sendProxyText(value)
  target.value = ''
}

function onProxyKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    event.preventDefault()
    sendProxyText('\r')
    return
  }
  if (event.key === 'Backspace') {
    event.preventDefault()
    sendProxyText('\x7f')
    return
  }
  if (event.key === 'Tab') {
    event.preventDefault()
    sendProxyText('\t')
  }
}

// composingText tracks the CURRENT in-progress composition text from the
// xterm-helper-textarea. Updated on every compositionupdate; cleared on
// compositionend. Used by the onData filter below to strip the trailing
// in-progress text that xterm.js accidentally reads when it flushes a
// compositionend result asynchronously and the next composition has already
// started. (Log evidence: compositionend at T+0ms, compositionstart at T+8ms,
// onData fires at T+25ms reading textarea that now contains prevComposed+newKey.)
let composingText = ''
let compositionActive = false

function attachXtermKeydownFallback(textarea: HTMLTextAreaElement | null): () => void {
  if (!textarea) return () => {}
  const onKeydown = (event: KeyboardEvent) => {
    xtermKeydownFallback.handleKeydown(event)
  }
  const onComposition = (event: CompositionEvent) => {
    xtermKeydownFallback.notifyComposition(event.type)
    if (event.type === 'compositionstart') {
      compositionActive = true
    } else if (event.type === 'compositionupdate') {
      compositionActive = true
      composingText = event.data ?? ''
    } else if (event.type === 'compositionend') {
      compositionActive = false
      composingText = ''
    }
  }
  const onBlur = () => {
    if (!compositionActive) clearXtermHelperTextareaValue(textarea, 'blur')
  }

  textarea.addEventListener('keydown', onKeydown, true)
  textarea.addEventListener('compositionstart', onComposition, true)
  textarea.addEventListener('compositionupdate', onComposition, true)
  textarea.addEventListener('compositionend', onComposition, true)
  textarea.addEventListener('blur', onBlur, true)

  return () => {
    textarea.removeEventListener('keydown', onKeydown, true)
    textarea.removeEventListener('compositionstart', onComposition, true)
    textarea.removeEventListener('compositionupdate', onComposition, true)
    textarea.removeEventListener('compositionend', onComposition, true)
    textarea.removeEventListener('blur', onBlur, true)
    composingText = ''
    compositionActive = false
  }
}

// Fixed highlight palette for search matches. `matchOverviewRuler` / `activeMatchColorOverviewRuler`
// are mandatory on ISearchDecorationOptions even though this terminal has no overview ruler
// enabled, so they just reuse the match colors. Kept visually distinct from the existing
// selection color (rgba(80, 160, 255, 0.5), blue) so a search highlight is never mistaken for a
// live text selection: amber for "a match", a brighter orange for "the current match".
const SEARCH_DECORATIONS = {
  matchBackground: '#5a4415',
  matchBorder: '#d9a441',
  matchOverviewRuler: '#d9a441',
  activeMatchBackground: '#8a5a12',
  activeMatchBorder: '#f5b342',
  activeMatchColorOverviewRuler: '#f5b342',
}

function buildSearchOptions(options?: TerminalFindOptions): ISearchOptions {
  return {
    caseSensitive: options?.caseSensitive,
    wholeWord: options?.wholeWord,
    regex: options?.regex,
    // Match tracking (onDidChangeResults / resultCount) only runs when `decorations` is set —
    // without it the addon still moves the selection but never computes the total count, so this
    // is not optional even though callers never author it themselves.
    decorations: SEARCH_DECORATIONS,
  }
}

// GPU renderer. xterm.js's DEFAULT is the DOM renderer: one element per cell, restyled through the
// browser's layout engine on every repaint. Measured on this app's own traffic shape — 200×50 grid,
// agent-TUI cursor repaints, CJK, plus the full-screen resends a tmux window switch produces — the
// DOM renderer does 3.0× the layouts and 3.45× the style recalcs of WebGL for the identical byte
// stream. That cost lands exactly where it is felt: a window switch repaints the whole grid.
//
// Loaded AFTER open() because the addon takes over a live render service, and guarded so a machine
// without WebGL2 is never left with a blank terminal:
//   - construction/activation throws (no WebGL2, blocklisted driver) → stay on the DOM renderer;
//   - context lost later (GPU reset, bfcache restore, driver update) → dispose, which drops xterm
//     back to the DOM renderer for the rest of the session.
// A failure is a console warning, never a throw: degraded rendering must not cost the user their
// terminal, and an exception here would abort the rest of initTerminal.
//
// NOTE on versions: this needs the addon built for the SAME core. `xterm-addon-webgl@0.16` on the
// old `xterm@5.3` core reaches for internals that moved — its dispose() throws
// "Cannot read properties of undefined (reading 'onRequestRedraw')", which fires on component
// teardown and on context loss, i.e. exactly the paths meant to keep things safe. That is why this
// arrived together with the move to @xterm/xterm 6, not before it.
// Which renderer a terminal ACTUALLY got is reported to the server log, not just the console.
// "is the GPU renderer on?" decides how to read every latency complaint, and the honest answer
// depends on the user's machine, driver and browser — not on what the code intends. Without this
// the question can only be answered by asking the user to open DevTools, which is not a diagnostic
// procedure, it is a guess with extra steps. One line per terminal mount; nothing periodic.
const rendererLog = createLogger('cli-renderer')

interface RendererChoice {
  renderer: 'webgl' | 'dom'
  /** Why the GPU renderer was declined. Absent when it was taken. */
  reason?: string
}

// Decides and records the renderer; it deliberately does NOT report. The report belongs after the
// initial fit — see initTerminal — because the grid size is half of what makes the report useful.
function enableWebglRenderer(term: Terminal): RendererChoice {
  try {
    const addon = new WebglAddon()
    addon.onContextLoss(() => {
      const lost = { surface: props.diagnosticSurface ?? 'terminal' }
      rendererLog.info('cli.renderer.context_lost', lost)
      reportServerEvent('cli.renderer.context_lost', lost)
      // 同一个事实也留一份给用户看得见的地方（「关于」面板）——这一档刷新就能修，
      // 而在此之前它只进服务端日志，用户完全不知道自己已经掉回 CPU 渲染。
      noteContextLost()
      addon.dispose()
      webglAddon = null
    })
    term.loadAddon(addon)
    webglAddon = addon
    return { renderer: 'webgl' }
  } catch (err) {
    // Not an error path for the user: the DOM renderer is a working terminal, just a slower one.
    webglAddon = null
    return { renderer: 'dom', reason: String(err instanceof Error ? err.message : err) }
  }
}

function initTerminal() {
  if (terminal) return
  if (!terminalContainer.value) return

  terminal = new Terminal({
    // Required for @xterm/addon-search's match highlighting: SearchAddon's decorations call
    // Terminal.registerDecoration(), which xterm.js gates behind `allowProposedApi` — with it
    // unset (default false) EVERY registerDecoration() call throws "You must set the
    // allowProposedApi option to true to use proposed API", which aborts findNext/findPrevious
    // BEFORE they reach _fireResults()/_findNextAndSelect() — search silently always reports 0
    // matches and never moves the selection, with no visible console error surfaced through the
    // normal event-handler path. [Ref: search-always-zero-matches root cause]
    allowProposedApi: true,
    cursorBlink: true,
    fontSize: isMobile.value ? 12 : 14,
    fontFamily: "'Cascadia Code', 'Fira Code', 'Source Code Pro', Menlo, Monaco, monospace",
    theme: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#aeafad',
      // Explicit, clearly-visible selection color. Default is a faint rgba(255,255,255,0.3)
      // that is easy to miss on a busy TUI; set both active + inactive so the selection
      // stays visible even when the terminal loses focus (mobile copy mode blurs the
      // helper textarea).
      selectionBackground: 'rgba(80, 160, 255, 0.5)',
      selectionInactiveBackground: 'rgba(80, 160, 255, 0.5)',
    },
    scrollback: 5000,
    convertEol: true,
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.loadAddon(new WebLinksAddon())

  searchAddon = new SearchAddon()
  terminal.loadAddon(searchAddon)
  searchResultsSub = searchAddon.onDidChangeResults(({ resultIndex, resultCount }) => {
    searchResultIndex.value = resultIndex
    searchResultCount.value = resultCount
  })

  terminal.open(terminalContainer.value)
  const rendererChoice: RendererChoice = enableWebglRenderer(terminal)
  renderSyncOn = renderSyncEnabled(rendererChoice.renderer, renderSyncOverride())
  renderMetrics = createRenderMetrics((summary) => {
    const facts = {
      ...summary,
      renderer: rendererChoice.renderer,
      renderSync: renderSyncOn,
      surface: props.diagnosticSurface ?? 'terminal',
    }
    rendererLog.info('cli.render.metrics', facts)
    reportServerEvent('cli.render.metrics', facts)
    noteRenderMetrics(summary)
  })
  const helperTextarea = configureInputAnchor()
  diagnosticCleanups.push(attachXtermKeydownFallback(helperTextarea))
  diagnosticCleanups.push(
    attachCliInputDiagnostics(helperTextarea, 'xterm-helper', { disableProxy: props.disableProxy }),
  )
  diagnosticCleanups.push(
    attachCliInputDiagnostics(terminalInputProxy.value, 'xterm-proxy', { disableProxy: props.disableProxy }),
  )

  // Initial fit — only if this terminal is actually on screen. A terminal born inside a hidden
  // tab keeps xterm's 80×24 default until it is first shown; measuring it here would size it to
  // ~10×6 (see terminalFit.ts) and every replayed byte would wrap at 10 columns.
  const measurable = canMeasureTerminal(terminalContainer.value)
  if (measurable) {
    try {
      fitAddon.fit()
    } catch {
      // Container may not be visible yet.
    }
  }

  // Reported AFTER the fit, and carrying `measured`, because the size is half the answer. Reporting
  // inside enableWebglRenderer looked right and was useless: it ran before any fit, so every
  // terminal claimed xterm's 80×24 default and a genuinely unfitted terminal — the case worth
  // catching, since an 80-column grid makes a wide TUI reflow and repaint constantly — was
  // indistinguishable from one simply measured too early. `measured: false` now says which.
  const rendererFacts = {
    ...rendererChoice,
    surface: props.diagnosticSurface ?? 'terminal',
    measured: measurable,
    cols: terminal.cols,
    rows: terminal.rows,
  }
  rendererLog.info('cli.renderer.active', rendererFacts)
  reportServerEvent('cli.renderer.active', rendererFacts)
  noteRenderer(rendererChoice.renderer, rendererChoice.reason)

  // [TH-0501-m9j] Platform-aware input routing.
  // WKWebView's textarea input events intermittently fail to trigger xterm's onData
  // for single ASCII characters (WebKit engine scheduling difference vs Blink).
  // Fix: on WKWebView, single ASCII chars are sent by TerminalPage's document
  // keydown handler (100% reliable). onData only handles IME/paste (multi-char/non-ASCII).
  // On Chrome/Edge, onData handles everything normally.
  terminal.onData((data: string) => {
    // Strip any trailing in-progress composition text that xterm.js accidentally
    // included due to its async compositionend read-back: when compositionend fires
    // and xterm.js reads the textarea value ~25ms later, a new composition may have
    // already started and written its first key(s) into the textarea, causing xterm
    // to send prevComposed+newComposingKey as one chunk. We know the in-progress
    // text because compositionupdate keeps composingText current.
    let cleanData = data
    if (composingText && data.length > composingText.length && data.endsWith(composingText)) {
      cleanData = data.slice(0, data.length - composingText.length)
      reportCliInputDiagnostic('xterm.onData.strip-composing', {
        original: summarizeText(data),
        stripped: summarizeText(cleanData),
        composingText: summarizeText(composingText),
      })
    }
    const bytes = encoder.encode(cleanData)
    xtermKeydownFallback.notifyTerminalData(bytes)
    if (isWKWebView && cleanData.length === 1 && cleanData.charCodeAt(0) < 128) {
      reportCliInputDiagnostic('xterm.onData.skip', {
        route: 'wk-single-ascii',
        data: summarizeText(cleanData),
      })
      if (!compositionActive) clearXtermHelperTextareaValue(helperTextarea, 'xterm.onData.skip')
      return // WKWebView: single ASCII handled by document keydown in TerminalPage
    }
    reportCliInputDiagnostic('xterm.onData.emit', { data: summarizeText(cleanData) })
    emit('data', bytes)
    if (!compositionActive) clearXtermHelperTextareaValue(helperTextarea, 'xterm.onData.emit')
  })

  // onKey: special keys + Ctrl combos on WKWebView.
  // onKey's `key` is the pre-translated terminal sequence (\x1b[A, \x03, \r, etc.)
  // IMPORTANT: xterm.js DOES fire onKey with non-empty key for printable chars
  // (e.g. key='l' for 'l'). On WKWebView, printable ASCII is already sent by the
  // parent's onKeydownDirect (document capture handler), so onKey must skip those
  // to avoid double-sending. Only special keys (Enter, arrows, Ctrl combos) that
  // onKeydownDirect does NOT handle should be sent here.
  terminal.onKey(({ key, domEvent }) => {
    if (!key) return
    if (isWKWebView) {
      // Skip printable ASCII that onKeydownDirect already handles:
      // onKeydownDirect sends when: key.length === 1, no modifiers, not composing
      if (domEvent && domEvent.key.length === 1
        && !domEvent.ctrlKey && !domEvent.altKey && !domEvent.metaKey
        && !domEvent.isComposing) {
        reportCliInputDiagnostic('xterm.onKey.skip', {
          route: 'wk-printable',
          key: summarizeText(domEvent.key),
          code: domEvent.code,
          isComposing: domEvent.isComposing,
          xtermKey: summarizeText(key),
        })
        return
      }
      reportCliInputDiagnostic('xterm.onKey.emit', {
        key: summarizeText(domEvent?.key),
        code: domEvent?.code,
        isComposing: domEvent?.isComposing,
        xtermKey: summarizeText(key),
      })
      emit('data', new TextEncoder().encode(key))
    }
    // Chrome/Edge: onData already handles these via triggerDataEvent — don't double-send
  })

  // ResizeObserver → debounce → emit resize.
  // [Ref: CAP-mobile-interaction S3, DDC-12]
  //
  // 隐藏也会触发这个回调（元素失去盒子 = 一次 0×0 的尺寸变化），而那正是最不能量的一刻 ——
  // 修复前这条路径把 10×6 一路发到了服务端的 pty.Setsize。闸门见 terminalFit.ts。
  resizeObserver = new ResizeObserver(() => {
    if (resizeDebounce) clearTimeout(resizeDebounce)
    resizeDebounce = setTimeout(() => {
      if (fitAddon && terminal && canMeasureTerminal(terminalContainer.value)) {
        try {
          fitAddon.fit()
          emit('resize', terminal.cols, terminal.rows)
        } catch {
          // Ignore if terminal is disposed.
        }
      }
    }, 150)
  })
  resizeObserver.observe(terminalContainer.value)

  emit('ready', terminal)
}

onMounted(() => {
  if (props.active !== false) {
    void nextTick(initTerminal)
  }
})

onUnmounted(() => {
  xtermKeydownFallback.dispose()
  for (const cleanup of diagnosticCleanups.splice(0)) cleanup()
  searchResultsSub?.dispose()
  searchResultsSub = null
  if (resizeDebounce) clearTimeout(resizeDebounce)
  if (renderSyncRaf) window.cancelAnimationFrame(renderSyncRaf)
  if (renderSyncTrailing) clearTimeout(renderSyncTrailing)
  if (renderSyncBurstReset) clearTimeout(renderSyncBurstReset)
  if (resizeObserver) resizeObserver.disconnect()
  // Before terminal.dispose(): release the GPU context explicitly. A workbench opens and closes
  // terminals freely and browsers cap live WebGL contexts (~16); silently losing them past that cap
  // is how a long session ends up with every terminal back on the DOM renderer.
  renderMetrics?.dispose()
  renderMetrics = null
  webglAddon?.dispose()
  webglAddon = null
  // Land whatever was still buffered so the mirror ends the session complete, not one batch short.
  if (transcriptFlushTimer) {
    clearTimeout(transcriptFlushTimer)
    flushTranscript()
  }
  if (terminal) terminal.dispose()
})

/**
 * Write data to the terminal display.
 * Called when binary data arrives from the WebSocket.
 */
function write(data: string | Uint8Array) {
  initTerminal()
  // Short-circuit, not just a guarded call: shouldSyncRenderAfterWrite fully TextDecoder's the
  // frame and runs three regexes over it. With the forced repaint off, that scan has no consumer,
  // and paying it on every frame to reach a branch that cannot be taken is pure waste.
  const shouldRefresh = renderSyncOn && shouldSyncRenderAfterWrite(data)
  // Two clocks, because they answer different questions: the write callback fires when the PARSER
  // is done with this frame, while the next onRender fires when the screen actually changed. A
  // terminal can be fast at one and slow at the other, and only the second is what a user sees.
  const started = performance.now()
  let renderSub: { dispose(): void } | null = null
  if (terminal && renderMetrics) {
    renderSub = terminal.onRender(() => {
      renderSub?.dispose()
      renderSub = null
      renderMetrics?.noteRender(performance.now() - started)
    })
  }
  terminal?.write(data, () => {
    renderMetrics?.noteFrame(dataLength(data), performance.now() - started)
    if (shouldRefresh) scheduleRenderSyncRefresh()
  })
  appendTranscript(data)
}

/**
 * Fit the terminal to its container.
 *
 * 不可测量时**保持上一次量准的尺寸**（见 terminalFit.ts）。调用方（robustFitAndResize、
 * 变可见、连接后的阶梯 fit、抽屉挤压重排）随后照常读 terminal.cols/rows 上报 —— 读到的
 * 是那个仍然正确的旧值，发到服务端就是一次无变化的 setsize（内核不会发 SIGWINCH），
 * 所以调用方一个字都不用改。
 */
function fit() {
  if (!terminal) {
    initTerminal()
    if (!terminal) return
  }
  if (!canMeasureTerminal(terminalContainer.value)) return
  fitAddon?.fit()
}

/** Search forward for `term` from the current position (wraps). Empty term is a no-op clear —
 *  calling the addon with '' would just drop the selection without resetting our own counters.
 *  `incremental` is findNext-only (native addon semantics): pass true for as-you-type calls so a
 *  still-matching longer query expands the current match instead of jumping to a new position;
 *  leave it false/omitted for an explicit "next match" (Enter / the ▼ button). */
function findNext(term: string, options?: TerminalFindOptions, incremental?: boolean): boolean {
  if (!searchAddon || !term) {
    searchResultIndex.value = -1
    searchResultCount.value = 0
    return false
  }
  return searchAddon.findNext(term, { ...buildSearchOptions(options), incremental })
}

/** Search backward for `term` from the current position (wraps). */
function findPrevious(term: string, options?: TerminalFindOptions): boolean {
  if (!searchAddon || !term) return false
  return searchAddon.findPrevious(term, buildSearchOptions(options))
}

/** Clears match highlights/selection and resets the exposed counters. `clearDecorations()` alone
 *  does NOT fire `onDidChangeResults` (see addon source: clearResults() just empties the internal
 *  array without emitting), so the counters are reset here explicitly rather than relying on it. */
function clearSearch(): void {
  searchAddon?.clearActiveDecoration()
  searchAddon?.clearDecorations()
  searchResultIndex.value = -1
  searchResultCount.value = 0
}

watch(() => props.active, (active) => {
  if (active) {
    void nextTick(() => setTimeout(() => fit(), 50))
  }
})

defineExpose({
  write,
  fit,
  terminal: () => terminal,
  findNext,
  findPrevious,
  clearSearch,
  searchResultIndex,
  searchResultCount,
})
</script>

<style scoped>
.xterm-root {
  width: 100%;
  height: 100%;
  position: relative;
}

.xterm-container {
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.terminal-transcript {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: pre-wrap;
  border: 0;
}

.terminal-input-proxy {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  border: 0;
}
</style>
