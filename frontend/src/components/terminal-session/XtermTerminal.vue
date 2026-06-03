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
import { Terminal } from 'xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import {
  attachCliInputDiagnostics,
  reportCliInputDiagnostic,
  summarizeText,
} from '@/composables/cli/useCliInputDiagnostics'
import { useXtermKeyboardFallback } from '@/composables/cli/useXtermKeyboardFallback'
import 'xterm/css/xterm.css'

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

const terminalContainer = ref<HTMLDivElement>()
const terminalInputProxy = ref<HTMLTextAreaElement>()
const transcript = ref('')
let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let resizeObserver: ResizeObserver | null = null
let resizeDebounce: ReturnType<typeof setTimeout> | null = null
const diagnosticCleanups: Array<() => void> = []
const encoder = new TextEncoder()
const decoder = new TextDecoder()
const transcriptLimit = 24000
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

function appendTranscript(data: string | Uint8Array) {
  const raw = typeof data === 'string' ? data : decoder.decode(data, { stream: true })
  const clean = raw
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '')
    .replace(/\x1b\][^\x07]*(\x07|\x1b\\)/g, '')
    .replace(/\r/g, '\n')
  if (!clean) return
  transcript.value = (transcript.value + clean).slice(-transcriptLimit)
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

function attachXtermKeydownFallback(textarea: HTMLTextAreaElement | null): () => void {
  if (!textarea) return () => {}
  const onKeydown = (event: KeyboardEvent) => {
    xtermKeydownFallback.handleKeydown(event)
  }
  const onComposition = (event: CompositionEvent) => {
    xtermKeydownFallback.notifyComposition(event.type)
  }

  textarea.addEventListener('keydown', onKeydown, true)
  textarea.addEventListener('compositionstart', onComposition, true)
  textarea.addEventListener('compositionupdate', onComposition, true)
  textarea.addEventListener('compositionend', onComposition, true)

  return () => {
    textarea.removeEventListener('keydown', onKeydown, true)
    textarea.removeEventListener('compositionstart', onComposition, true)
    textarea.removeEventListener('compositionupdate', onComposition, true)
    textarea.removeEventListener('compositionend', onComposition, true)
  }
}

function initTerminal() {
  if (terminal) return
  if (!terminalContainer.value) return

  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 14,
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

  terminal.open(terminalContainer.value)
  const helperTextarea = configureInputAnchor()
  diagnosticCleanups.push(attachXtermKeydownFallback(helperTextarea))
  diagnosticCleanups.push(
    attachCliInputDiagnostics(helperTextarea, 'xterm-helper', { disableProxy: props.disableProxy }),
  )
  diagnosticCleanups.push(
    attachCliInputDiagnostics(terminalInputProxy.value, 'xterm-proxy', { disableProxy: props.disableProxy }),
  )

  // Initial fit.
  try {
    fitAddon.fit()
  } catch {
    // Container may not be visible yet.
  }

  // [TH-0501-m9j] Platform-aware input routing.
  // WKWebView's textarea input events intermittently fail to trigger xterm's onData
  // for single ASCII characters (WebKit engine scheduling difference vs Blink).
  // Fix: on WKWebView, single ASCII chars are sent by TerminalPage's document
  // keydown handler (100% reliable). onData only handles IME/paste (multi-char/non-ASCII).
  // On Chrome/Edge, onData handles everything normally.
  terminal.onData((data: string) => {
    const bytes = encoder.encode(data)
    xtermKeydownFallback.notifyTerminalData(bytes)
    if (isWKWebView && data.length === 1 && data.charCodeAt(0) < 128) {
      reportCliInputDiagnostic('xterm.onData.skip', {
        route: 'wk-single-ascii',
        data: summarizeText(data),
      })
      return // WKWebView: single ASCII handled by document keydown in TerminalPage
    }
    reportCliInputDiagnostic('xterm.onData.emit', { data: summarizeText(data) })
    emit('data', bytes)
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
  resizeObserver = new ResizeObserver(() => {
    if (resizeDebounce) clearTimeout(resizeDebounce)
    resizeDebounce = setTimeout(() => {
      if (fitAddon && terminal) {
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
  if (resizeDebounce) clearTimeout(resizeDebounce)
  if (resizeObserver) resizeObserver.disconnect()
  if (terminal) terminal.dispose()
})

/**
 * Write data to the terminal display.
 * Called when binary data arrives from the WebSocket.
 */
function write(data: string | Uint8Array) {
  initTerminal()
  terminal?.write(data)
  appendTranscript(data)
}

/**
 * Fit the terminal to its container.
 */
function fit() {
  if (!terminal) {
    initTerminal()
    if (!terminal) return
  }
  fitAddon?.fit()
}

watch(() => props.active, (active) => {
  if (active) {
    void nextTick(() => setTimeout(() => fit(), 50))
  }
})

defineExpose({ write, fit, terminal: () => terminal })
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
