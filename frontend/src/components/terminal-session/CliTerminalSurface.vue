<template>
  <div
    class="cli-terminal-surface"
    data-testid="cli-terminal-surface"
    :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
  >
    <!-- 抢占横幅 -->
    <div v-if="wsStatus === 'preempted'" class="preempted-banner" data-testid="preempted-banner">
      <span>Session 已被其他设备接管。</span>
      <button class="btn-reconnect" data-testid="btn-reconnect" @click="wsReconnect()">重新连接</button>
    </div>

    <!-- 终端区域 -->
    <div
      class="terminal-body"
      data-testid="terminal-body"
      ref="terminalBodyRef"
      @touchstart.passive="onTerminalTouchStart"
      @touchend.passive="onTerminalTouchEnd"
    >
      <XtermTerminal
        ref="xtermRef"
        :active="active"
        :disable-proxy="!isMobile"
        :ime-fallback-enabled="isMobile && activeMode === 'keyboard'"
        diagnostic-surface="workbench"
        @data="onTerminalData"
        @resize="onTerminalResize"
        @ready="onTerminalReady"
      />
    </div>

    <!-- 底栏 (mobile only) -->
    <div v-if="isMobile" class="bottom-bar">
      <Toolbar
        :sticky-shift="stickyShift"
        :sticky-ctrl="stickyCtrl"
        :sticky-alt="stickyAlt"
        :active-panel="activePanelLabel"
        :tmux-detected="tmuxDetected"
        :keyboard-up="activeMode === 'keyboard'"
        @send-key="onSendKey"
        @toggle-numpad="onTogglePanel('numpad')"
        @toggle-compose="onTogglePanel('compose')"
        @toggle-shift="stickyShift = !stickyShift"
        @toggle-ctrl="stickyCtrl = !stickyCtrl"
        @toggle-alt="stickyAlt = !stickyAlt"
        @toggle-hud="hudVisible = !hudVisible"
        @toggle-tmux="onTogglePanel('tmux')"
        @toggle-keyboard="onToggleKeyboard"
        @attach="onAttachClick"
      />
      <KeyboardPanel v-if="activeMode === 'numpad'" @send-key="onSendKey" @clipboard="onClipboard" @close="onToggleKeyboard" />
      <TmuxPanel v-if="activeMode === 'tmux'" @send-key="onSendKey" @close="onToggleKeyboard" />
      <ComposeBar v-if="activeMode === 'compose'" @send="onComposeSend" @close="() => { activeMode = 'idle' }" />
    </div>

    <!-- 浮动层: touchball, 选区覆盖, 复制按钮, HUD -->
    <MobileOverlay
      v-if="isMobile"
      ref="mobileOverlayRef"
      :anchor-state="anchorState"
      :anchor1="anchor1"
      :anchor2="anchor2"
      :cell-to-screen="coordMapper.cellToScreen"
      :screen-to-cell="coordMapper.screenToCell"
      :terminal-rows="terminalRows"
      :viewport-y="viewportY"
      :hud-visible="hudVisible"
      :hud-events="hud.events.value"
      :hud-snapshot="hud.snapshot"
      @selection-copy="onSelectionCopy"
      @touchball-tap="onTouchballTap"
      @touchball-double-tap="onTouchballDoubleTap"
      @touchball-triple-tap="onTouchballTripleTap"
      @touchball-long-press="onTouchballLongPress"
      @anchor-drag="onAnchorDrag"
      @close-hud="hudVisible = false"
      @clear-hud="hud.clear()"
      @upload-hud="hud.upload(sessionId)"
    />

    <AuthDialog
      :visible="showAuthDialog"
      @dismiss="dismissAuthDialog"
      @authenticated="onAuthenticated"
    />

    <!-- 隐藏文件输入 (📎 附件按钮) -->
    <input
      ref="attachInputRef"
      type="file"
      accept="image/*,.pdf,.txt,.md,.json,.csv,.log,.py,.go,.js,.ts,.sh,.yaml,.yml,.toml"
      multiple
      style="display: none"
      @change="onAttachFileSelected"
    />

    <KeyCastrOverlay
      v-if="isMobile"
      :entries="keycastEntries"
      :bottom-offset="keycastBottomOffset"
      data-testid="keystroke-hud"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { Terminal } from 'xterm'
import XtermTerminal from '@/components/terminal-session/XtermTerminal.vue'
import AuthDialog from '@/components/terminal-session/AuthDialog.vue'
import MobileOverlay from '@/components/terminal-session/MobileOverlay.vue'
import Toolbar from '@/components/terminal-session/Toolbar.vue'
import KeyboardPanel from '@/components/terminal-session/KeyboardPanel.vue'
import TmuxPanel from '@/components/terminal-session/TmuxPanel.vue'
import ComposeBar from '@/components/terminal-session/ComposeBar.vue'
import KeyCastrOverlay from '@/components/terminal-session/KeyCastrOverlay.vue'
import { useWebSocketClient } from '@/composables/cli/useWebSocketClient'
import { useDeviceDetection } from '@/composables/cli/useDeviceDetection'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { useFocusStateMachine } from '@/composables/cli/useFocusStateMachine'
import { useAnchorStateMachine } from '@/composables/cli/useAnchorStateMachine'
import { useTerminalCoordMapper } from '@/composables/cli/useTerminalCoordMapper'
import { useComposeSendStrategy } from '@/composables/cli/useComposeSendStrategy'
import { useHudCollector } from '@/composables/cli/useHudCollector'
import { useCliPasteResolver } from '@/composables/cli/useCliPasteResolver'
import { useAgentIntel } from '@/composables/cli/useAgentIntel'
import { useKeyCastrHud } from '@/composables/cli/useKeyCastrHud'
import { focusWithoutViewportScroll, resetViewportScroll, useVisualKeyboardInset } from '@/composables/cli/useVisualKeyboardInset'
import { reportCliInputDiagnostic, summarizeBytes, summarizeText, useCliTerminalInputTelemetry } from '@/composables/cli/useCliInputDiagnostics'
import type { WSControlMessage, CellCoord, AnchorState, WSConnectionStatus } from '@/types/terminal'
import type { AgentState } from '@/types/terminal'

// ─── Props & Emits ────────────────────────────────────────────────────────────

const props = defineProps<{
  sessionId: string
  sessionName: string
  active: boolean
}>()

const emit = defineEmits<{
  (e: 'agent-state', state: AgentState | null): void
  (e: 'agent-notifications', state: AgentState[]): void
  (e: 'session-exit', exitCode: number): void
  (e: 'connection-change', status: WSConnectionStatus): void
}>()

// ─── Composables ─────────────────────────────────────────────────────────────

const { isMobile } = useDeviceDetection()
const { showAuthDialog, dismissAuthDialog } = useCliAuth()

const focusSM = useFocusStateMachine()
const anchorSM = useAnchorStateMachine()
const anchorState = computed<AnchorState>(() => anchorSM.state.value ?? 'IDLE')
const anchor1 = computed<CellCoord | null>(() => anchorSM.anchor1.value ?? null)
const anchor2 = computed<CellCoord | null>(() => anchorSM.anchor2.value ?? null)
const composeSend = useComposeSendStrategy()
const hud = useHudCollector()
const hudVisible = ref(false)
const { agentState, notifications, handleWSMessage: agentWSHandler } = useAgentIntel(() => props.sessionId)
const keyCastr = useKeyCastrHud()
const keycastEntries = keyCastr.entries

// ─── Template refs ────────────────────────────────────────────────────────────

const xtermRef = ref<InstanceType<typeof XtermTerminal>>()
const mobileOverlayRef = ref<InstanceType<typeof MobileOverlay>>()
const terminalBodyRef = ref<HTMLDivElement>()
const attachInputRef = ref<HTMLInputElement>()

// ─── State ────────────────────────────────────────────────────────────────────

const tmuxDetected = ref(false)
const activeMode = ref<'idle' | 'keyboard' | 'numpad' | 'tmux' | 'compose'>('idle')
const stickyShift = ref(false)
const stickyCtrl = ref(false)
const stickyAlt = ref(false)
const viewportY = ref(0)
const terminalRows = ref(24)
const { keyboardInset: keyboardHeight, syncKeyboardInset } = useVisualKeyboardInset({ enabled: () => isMobile.value })
let keyboardWanted = false

const activePanelLabel = computed<'none' | 'numpad' | 'tmux' | 'compose'>(() => {
  if (activeMode.value === 'numpad') return 'numpad'
  if (activeMode.value === 'tmux') return 'tmux'
  if (activeMode.value === 'compose') return 'compose'
  return 'none'
})

const keycastBottomOffset = computed(() =>
  80 + (activeMode.value === 'keyboard' ? keyboardHeight.value : 0)
)

const encoder = new TextEncoder()

// ─── WebSocket client ─────────────────────────────────────────────────────────

const {
  status: wsStatus,
  netStats,
  connect,
  reconnect: wsReconnect,
  disconnect: wsDisconnect,
  sendBinary: sendBinaryRaw,
  sendResize,
  onMessage,
} = useWebSocketClient(() => props.sessionId)

const inputTelemetry = useCliTerminalInputTelemetry({
  surface: 'workbench',
  sessionId: () => props.sessionId,
})

function sendBinary(data: Uint8Array, route = 'direct'): void {
  inputTelemetry.recordSend(data, route)
  sendBinaryRaw(data)
}

const pasteResolver = useCliPasteResolver({
  sessionId: () => props.sessionId,
  surface: 'workbench',
  isActive: () => props.active,
  sendBinary: (data) => sendBinary(data, 'clipboard'),
  openAttachmentPicker: () => attachInputRef.value?.click(),
  hudRecord: (kind, message) => hud.record(kind, message),
})

// ─── Robust resize: fit + sendResize, retries to handle DOM layout settling ──
function robustFitAndResize() {
  const xterm = xtermRef.value
  if (!xterm) return
  xterm.fit()
  const term = xterm.terminal?.()
  if (term && term.cols > 0 && term.rows > 0) {
    sendResize(term.cols, term.rows)
    hud.updateSnapshot({ pty: `${term.cols}x${term.rows}` })
  }
}

// Emit connection status changes
watch(wsStatus, (val) => {
  emit('connection-change', val)
  hud.updateSnapshot({ ws: val })
  if (val === 'connected') {
    // DOM layout 可能还没稳定 (特别是 Wails 首次渲染)，阶梯式 fit:
    // 100ms (快速响应) → 500ms (layout 稳定) → 1500ms (最终校准)
    setTimeout(robustFitAndResize, 100)
    setTimeout(robustFitAndResize, 500)
    setTimeout(robustFitAndResize, 1500)
  }
}, { immediate: true })

// Emit agent state changes
watch(agentState, (val) => {
  emit('agent-state', val)
})

watch(notifications, (val) => {
  emit('agent-notifications', val)
}, { immediate: true })

watch([agentState, notifications], ([state, list]) => {
  if (hasTmuxAgentTopology(state, list)) tmuxDetected.value = true
}, { immediate: true })

function hasTmuxAgentTopology(state: AgentState | null, list: AgentState[]): boolean {
  return state?.tmuxWindow != null || list.some(item => item.tmuxWindow != null)
}

// ─── active prop watch — connect / disconnect on tab switch ───────────────────

watch(() => props.active, (isActive) => {
  if (isActive) {
    connect()
    // tab 切换后重新 fit，消除 v-show 隐藏时 xterm 无法测量尺寸的问题
    nextTick(() => {
      const xterm = xtermRef.value
      if (xterm) {
        xterm.fit()
        const term = xterm.terminal?.()
        if (term) {
          sendResize(term.cols, term.rows)
          terminalRows.value = term.rows
        }
      }
    })
  } else {
    wsDisconnect()
  }
}, { immediate: false })

// ─── Page visibility ──────────────────────────────────────────────────────────

function onVisibilityChange() {
  if (document.visibilityState === 'visible') {
    const term = xtermRef.value?.terminal?.()
    if (term) term.refresh(0, term.rows - 1)
  }
}

let viewportScrollLockRaf = 0

function hasViewportScrollOffset(): boolean {
  return window.scrollY !== 0
    || document.documentElement.scrollTop !== 0
    || document.body.scrollTop !== 0
}

function lockKeyboardViewportScroll(): void {
  if (!props.active || !isMobile.value || activeMode.value !== 'keyboard') return
  if (!hasViewportScrollOffset()) return
  if (viewportScrollLockRaf) return
  viewportScrollLockRaf = window.requestAnimationFrame(() => {
    viewportScrollLockRaf = 0
    resetViewportScroll()
  })
}

// ─── Keyboard auto-dismiss (iOS) ──────────────────────────────────────────────

watch(keyboardHeight, (val, oldVal) => {
  reportCliInputDiagnostic('keyboard.inset', { surface: 'workbench', val, oldVal, activeMode: activeMode.value })
  lockKeyboardViewportScroll()
  if (oldVal > 100 && val < 50 && activeMode.value === 'keyboard') {
    activeMode.value = 'idle'
    keyboardWanted = false
    resetViewportScroll()
    hud.record('state', 'keyboard auto-dismissed (iOS)')
  }
})

// Refit on panel change
watch(activeMode, () => {
  reportCliInputDiagnostic('active-mode.change', { surface: 'workbench', activeMode: activeMode.value })
  nextTick(() => {
    xtermRef.value?.fit()
    const term = xtermRef.value?.terminal?.()
    if (term) {
      sendResize(term.cols, term.rows)
      terminalRows.value = term.rows
    }
  })
})

// ─── Lifecycle ────────────────────────────────────────────────────────────────

const coordMapper = useTerminalCoordMapper(() => {
  const term = xtermRef.value?.terminal?.()
  // Map against xterm's actual rendered grid element (.xterm-screen): its rect is EXACTLY
  // cols*cellWidth × rows*cellHeight and its origin is the true top-left of the char grid.
  // The previous code used the outer container (.xterm-root) with rect.width/cols, which
  // overcounts the scrollbar gutter (~15px) and FitAddon's sub-cell remainder, producing
  // systematic drift that grows toward the right/bottom edges (mobile-only touch path).
  const screenEl = term?.element?.querySelector('.xterm-screen') as HTMLElement | null
  if (!term || !screenEl) {
    return { cols: 80, rows: 24, cellWidth: 9, cellHeight: 17, offsetX: 0, offsetY: 0 }
  }
  const rect = screenEl.getBoundingClientRect()
  return {
    cols: term.cols,
    rows: term.rows,
    cellWidth: rect.width / term.cols,
    cellHeight: rect.height / term.rows,
    offsetX: rect.left,
    offsetY: rect.top,
  }
})

const isWKWebView = navigator.userAgent.includes('AppleWebKit') &&
  !navigator.userAgent.includes('Chrome') &&
  !navigator.userAgent.includes('Safari')

function onKeydownDirect(e: KeyboardEvent) {
  if (!props.active) return
  keyCastr.feed(e)
  reportCliInputDiagnostic('document.keydown', {
    surface: 'workbench',
    key: summarizeText(e.key),
    code: e.code,
    isComposing: e.isComposing,
    route: isWKWebView ? 'wk-candidate' : 'observe-only',
  })
  if (!isWKWebView) return
  if (e.isComposing || e.metaKey || e.altKey || e.ctrlKey) return
  if (e.key.length !== 1) return
  // Don't capture when a non-xterm input/textarea has focus (e.g. tab rename)
  const active = document.activeElement
  if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA')
    && !active.classList.contains('xterm-helper-textarea')) return
  // Prevent default browser action (char insertion into xterm textarea) so xterm.js's
  // internal onData/onKey don't double-send. xterm.js still processes the event for
  // cursor management, but our onKey guard (XtermTerminal.vue) also skips printable ASCII.
  e.preventDefault()
  sendBinary(encoder.encode(e.key), 'document-keydown')
}

async function onClipboardPaste(e: ClipboardEvent) {
  await pasteResolver.handlePasteEvent(e)
}

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  document.addEventListener('keydown', onKeydownDirect, { capture: true })
  document.addEventListener('paste', onClipboardPaste, { capture: true })
  window.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('resize', lockKeyboardViewportScroll)
  // Connect immediately if active
  if (props.active) connect()
})

onUnmounted(() => {
  if (viewportScrollLockRaf) window.cancelAnimationFrame(viewportScrollLockRaf)
  document.removeEventListener('keydown', onKeydownDirect, { capture: true })
  document.removeEventListener('paste', onClipboardPaste, { capture: true })
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('scroll', lockKeyboardViewportScroll)
  window.visualViewport?.removeEventListener('scroll', lockKeyboardViewportScroll)
  window.visualViewport?.removeEventListener('resize', lockKeyboardViewportScroll)
})

// ─── Terminal callbacks ───────────────────────────────────────────────────────

function onTerminalReady(terminal: Terminal) {
  if (isMobile.value) {
    focusSM.focusTerminal()
    const helperTA = terminal.element?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (helperTA) {
      helperTA.addEventListener('focus', () => {
        if (!keyboardWanted) helperTA.blur()
      })
    }
  }

  terminal.onScroll(() => {
    viewportY.value = terminal.buffer.active.viewportY
  })
  terminalRows.value = terminal.rows

  onMessage(
    (data: ArrayBuffer) => {
      const bytes = new Uint8Array(data)
      inputTelemetry.recordOutput(bytes, 'ws-binary')
      xtermRef.value?.write(bytes)
    },
    (msg: WSControlMessage) => {
      switch (msg.type) {
        case 'shell_exit': {
          xtermRef.value?.write('\r\n[进程已退出]\r\n')
          const code = (msg.payload as any)?.exitCode ?? 0
          emit('session-exit', code)
          break
        }
        case 'preempted':
          xtermRef.value?.write('\r\n[Session 已被其他客户端接管]\r\n')
          break
        case 'agent_state':
          agentWSHandler(msg.payload)
          break
        case 'error':
          console.error('WS error:', msg.payload)
          break
      }
    }
  )
  connect()
}

function onTerminalData(data: Uint8Array) {
  reportCliInputDiagnostic('terminal-data', { surface: 'workbench', data: summarizeBytes(data) })
  sendTerminalData(data)
}

function sendTerminalData(data: Uint8Array) {
  if (data.length === 1) {
    let byte = data[0]
    if (stickyCtrl.value) { byte = byte & 0x1f; stickyCtrl.value = false }
    if (stickyAlt.value) { sendBinary(encoder.encode('\x1b'), 'sticky-alt'); stickyAlt.value = false }
    if (stickyShift.value) { if (byte >= 0x61 && byte <= 0x7a) byte -= 0x20; stickyShift.value = false }
    sendBinary(new Uint8Array([byte]), 'xterm-data')
  } else {
    sendBinary(data, 'xterm-data')
  }
}

function onTerminalResize(cols: number, rows: number) {
  sendResize(cols, rows)
  terminalRows.value = rows
  hud.record('resize', `${cols}x${rows}`)
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

function onAuthenticated() {
  dismissAuthDialog()
}

// ─── Attach ───────────────────────────────────────────────────────────────────

function onAttachClick() {
  attachInputRef.value?.click()
}

async function onAttachFileSelected() {
  const input = attachInputRef.value
  if (!input?.files?.length) return
  await pasteResolver.uploadFilesFromInput(Array.from(input.files), 'manual-attach')
  input.value = ''
}

// ─── Keyboard / Panel mode ────────────────────────────────────────────────────

function showKeyboard() {
  activeMode.value = 'keyboard'
  keyboardWanted = true
  reportCliInputDiagnostic('keyboard.show', { surface: 'workbench' })
  nextTick(() => {
    const textarea = xtermRef.value?.$el?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (textarea) {
      textarea.style.setProperty('pointer-events', 'auto', 'important')
      focusWithoutViewportScroll(textarea)
      resetViewportScroll()
      syncKeyboardInset()
      setTimeout(syncKeyboardInset, 150)
      setTimeout(syncKeyboardInset, 400)
      setTimeout(() => { textarea.style.setProperty('pointer-events', 'none', 'important') }, 300)
    }
  })
  hud.record('state', 'show keyboard')
}

function onToggleKeyboard() {
  if (activeMode.value === 'keyboard') {
    activeMode.value = 'idle'
    keyboardWanted = false
    const textarea = xtermRef.value?.$el?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (textarea) textarea.blur()
    reportCliInputDiagnostic('keyboard.dismiss', { surface: 'workbench', source: 'toolbar' })
    hud.record('state', 'dismiss keyboard')
  } else {
    showKeyboard()
  }
}

function onTogglePanel(panel: 'numpad' | 'tmux' | 'compose') {
  if (activeMode.value === panel) {
    showKeyboard()
  } else {
    activeMode.value = panel
    const textarea = xtermRef.value?.$el?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (textarea) textarea.blur()
  }
  hud.record('state', `panel: ${activeMode.value}`)
}

function onSendKey(key: string) {
  const term = xtermRef.value?.terminal?.()
  if (term && (key === '\x1b[5~' || key === '\x1b[6~')) {
    sendBinary(encoder.encode(key))
    hud.record('keyboard', `${key === '\x1b[5~' ? 'PgUp' : 'PgDn'} (PTY)`)
    return
  }
  if (tmuxDetected.value && (key === '\x1b[H' || key === '\x1b[F' ||
      key === '\x1b[A' || key === '\x1b[B' || key === '\x1b[C' || key === '\x1b[D')) {
    sendBinary(encoder.encode(key))
    hud.record('keyboard', 'nav key → tmux PTY')
    return
  }
  let modified = key
  if (stickyShift.value) {
    if (key === '\t') { modified = '\x1b[Z'; stickyShift.value = false }
    else if (key === '\r') { modified = '\r'; stickyShift.value = false }
    else if (key.length === 1) { modified = key.toUpperCase(); stickyShift.value = false }
    else { stickyShift.value = false }
  }
  if (stickyCtrl.value) {
    if (modified.length === 1) modified = String.fromCharCode(modified.charCodeAt(0) & 0x1f)
    stickyCtrl.value = false
  }
  if (stickyAlt.value) {
    modified = '\x1b' + modified
    stickyAlt.value = false
  }
  sendBinary(encoder.encode(modified))
  hud.record('keyboard', `key: ${JSON.stringify(modified).slice(0, 20)}`)
}

function onClipboard(op: string) {
  const term = xtermRef.value?.terminal?.()
  switch (op) {
    case 'copy':
    case 'cut':
      if (term) {
        const sel = term.getSelection()
        if (sel) { clipboardWrite(sel); hud.record('state', `clipboard ${op}: ${sel.length} chars`) }
      }
      break
    case 'paste':
      navigator.clipboard.readText().then(text => {
        if (text) sendBinary(encoder.encode(text))
        hud.record('state', `clipboard paste: ${text.length} chars`)
      }).catch(() => {})
      break
    case 'undo':
      sendBinary(encoder.encode('\x1a'))
      break
    case 'selectAll':
      if (term) term.selectAll()
      break
    case 'find':
      sendBinary(encoder.encode('\x06'))
      break
  }
}

function onComposeSend(text: string) {
  const chunks = composeSend.encode(text)
  for (const chunk of chunks) sendBinary(chunk)
  activeMode.value = 'idle'
  hud.record('keyboard', `compose: ${text.length} chars`)
}

// ─── Touch interactions ───────────────────────────────────────────────────────

let termTouchStartX = 0
let termTouchStartY = 0

function onTerminalTouchStart(e: TouchEvent) {
  if (!isMobile.value) return
  const touch = e.touches[0]
  if (touch) { termTouchStartX = touch.clientX; termTouchStartY = touch.clientY }
}

function onTerminalTouchEnd(e: TouchEvent) {
  if (!isMobile.value) return
  const touch = e.changedTouches[0]
  if (!touch) return
  const dx = Math.abs(touch.clientX - termTouchStartX)
  const dy = Math.abs(touch.clientY - termTouchStartY)
  if (dx < 10 && dy < 10) {
    mobileOverlayRef.value?.moveBall(touch.clientX, touch.clientY)
  }
}

function onTouchballTap(x: number, y: number) {
  const cell = coordMapper.screenToCell(x, y)
  const cellBuf: CellCoord = { ...cell, bufferRow: viewportY.value + cell.row }
  if (anchorSM.state.value === 'IDLE') {
    hud.record('touch', `tap (idle) cell (${cell.col},${cell.row})`)
    return
  } else if (anchorSM.state.value === 'NO_ANCHOR') {
    anchorSM.placeAnchor1(cellBuf)
  } else if (anchorSM.state.value === 'HAS_ANCHOR_1') {
    anchorSM.placeAnchor2(cellBuf)
  } else if (anchorSM.state.value === 'HAS_BOTH') {
    anchorSM.moveNearestAnchor(cellBuf)
  }
  applyXtermSelection()
  hud.record('touch', `tap anchor cell (${cell.col},${cell.row})`)
}

function onTouchballDoubleTap(x: number, y: number) {
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  const cell = coordMapper.screenToCell(x, y)
  const line = term.buffer.active.getLine(cell.row + viewportY.value)
  if (!line) return
  const lineStr = line.translateToString(true)
  let start = cell.col, end = cell.col
  while (start > 0 && /\S/.test(lineStr[start - 1] || '')) start--
  while (end < lineStr.length - 1 && /\S/.test(lineStr[end + 1] || '')) end++
  term.select(start, cell.row + viewportY.value, end - start + 1)
  const startBuf = viewportY.value + cell.row
  anchorSM.enterSelection()
  anchorSM.placeAnchor1({ col: start, row: cell.row, bufferRow: startBuf })
  anchorSM.placeAnchor2({ col: end, row: cell.row, bufferRow: startBuf })
  hud.record('touch', `double-tap: select word at (${cell.col},${cell.row})`)
}

function onTouchballTripleTap(_x: number, _y: number) {
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  // Select the ENTIRE visible screen. Pairs with PgUp/PgDn paging: scroll a page, triple-tap
  // to grab the whole screen, copy — no dependence on precise edge-scroll selection.
  const top = viewportY.value
  const bottom = top + term.rows - 1
  term.select(0, top, term.rows * term.cols)
  anchorSM.selectAll(
    { col: 0, row: 0, bufferRow: top },
    { col: term.cols - 1, row: term.rows - 1, bufferRow: bottom },
  )
  hud.record('touch', `triple-tap: select full screen (${term.rows}×${term.cols})`)
}

function onTouchballLongPress(x: number, y: number) {
  if (anchorSM.state.value === 'HAS_BOTH') {
    const term = xtermRef.value?.terminal?.()
    if (term) {
      const sel = term.getSelection()
      if (sel) { clipboardWrite(sel); hud.record('state', `long-press copy: ${sel.length} chars`) }
    }
    anchorSM.cancel()
    return
  }
  const cell = coordMapper.screenToCell(x, y)
  const cellBuf: CellCoord = { ...cell, bufferRow: viewportY.value + cell.row }
  anchorSM.enterSelection()
  anchorSM.placeAnchor1(cellBuf)
  applyXtermSelection()
  hud.record('touch', `long-press: enter selection at (${cell.col},${cell.row})`)
}

async function onSelectionCopy() {
  const term = xtermRef.value?.terminal?.()
  if (!term) { anchorSM.cancel(); return }
  const sel = term.getSelection()
  if (!sel) {
    hud.record('state', 'copy: empty selection')
    term.clearSelection(); anchorSM.cancel(); return
  }
  const ok = await clipboardWrite(sel)
  hud.record('state', ok ? `copy ok: ${sel.length} chars` : `copy FAILED (${sel.length} chars)`)
  // Only drop the selection on success so the user can retry a failed copy.
  if (ok) { term.clearSelection(); anchorSM.cancel() }
}

function onAnchorDrag(anchorId: 1 | 2, cell: CellCoord) {
  const term = xtermRef.value?.terminal?.()
  // Edge auto-scroll while selecting (D3): dragging an anchor onto the top/bottom row scrolls
  // the view so the selection can extend to content that is currently off-screen.
  if (term) {
    if (cell.row <= 0) edgeScroll(term, -1)
    else if (cell.row >= term.rows - 1) edgeScroll(term, 1)
  }
  if (anchorId === 1) anchorSM.placeAnchor1(cell)
  else anchorSM.placeAnchor2(cell)
  applyXtermSelection()
}

let lastEdgeScrollAt = 0
function edgeScroll(term: Terminal, dir: 1 | -1) {
  const now = Date.now()
  if (now - lastEdgeScrollAt < 120) return  // throttle
  lastEdgeScrollAt = now
  if (term.buffer.active.type === 'alternate') {
    // Alt-screen TUI (tmux / claude-code): xterm holds NO scrollback here — history lives in
    // the app/tmux. Nudge the app's own pager with PgUp/PgDn so the view scrolls. Caveat:
    // the selection cannot extend across an app-managed scroll (off-screen lines are not in
    // xterm's buffer).
    sendBinary(encoder.encode(dir < 0 ? '\x1b[5~' : '\x1b[6~'))
    hud.record('touch', 'copy-mode edge scroll → PgUp/PgDn (alt-screen)')
  } else {
    term.scrollLines(dir)
    hud.record('touch', `copy-mode edge scroll ${dir > 0 ? 'down' : 'up'}`)
  }
}

function applyXtermSelection() {
  const ordered = anchorSM.orderedAnchors.value
  if (!ordered) return
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  // xterm select(col, row, length): `row` is an ABSOLUTE buffer line (incl. scrollback),
  // NOT a viewport-relative row (SelectionService uses buffer-absolute coords). Pass bufferRow
  // directly; subtracting viewportY was harmless only in alt-screen (viewportY===0).
  const startRow = ordered.start.bufferRow ?? ordered.start.row
  const endRow = ordered.end.bufferRow ?? ordered.end.row
  term.select(ordered.start.col, startRow,
    (endRow - startRow) * term.cols + (ordered.end.col - ordered.start.col) + 1)
}

// ─── Clipboard helpers ────────────────────────────────────────────────────────

function clipboardWrite(text: string): Promise<boolean> {
  // Secure-context API (HTTPS / localhost). On iOS Safari over plain HTTP (e.g. a Tailscale
  // http://host:PORT link) navigator.clipboard is UNDEFINED, so we fall through to execCommand,
  // which must run synchronously inside the tap gesture.
  if (navigator.clipboard?.writeText) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => clipboardWriteFallback(text))
  }
  return Promise.resolve(clipboardWriteFallback(text))
}

function clipboardWriteFallback(text: string): boolean {
  // iOS Safari requires a focused, contentEditable, selected element with an explicit range —
  // a bare offscreen `ta.select()` silently no-ops. Keep it in-viewport but invisible.
  const ta = document.createElement('textarea')
  ta.value = text
  ta.readOnly = false
  ta.contentEditable = 'true'
  ta.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;padding:0;border:0;opacity:0;font-size:16px'
  document.body.appendChild(ta)
  ta.focus()
  const range = document.createRange()
  range.selectNodeContents(ta)
  const winSel = window.getSelection()
  winSel?.removeAllRanges()
  winSel?.addRange(range)
  ta.setSelectionRange(0, text.length)
  let threw = false
  try { document.execCommand('copy') } catch { threw = true }
  winSel?.removeAllRanges()
  document.body.removeChild(ta)
  // iOS Safari's execCommand('copy') often returns false even when the copy succeeded, so its
  // boolean is unreliable — treat "did not throw" as success. (HTTPS uses navigator.clipboard.)
  return !threw
}

// ─── Expose for parent ────────────────────────────────────────────────────────

defineExpose({ wsStatus, agentState, notifications, netStats })
</script>

<style scoped>
.cli-terminal-surface {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  background: #1e1e1e;
  overflow: hidden;
}

.terminal-body {
  flex: 1;
  position: relative;
  overflow: hidden;
  width: 100%;
  min-height: 0;
}

.preempted-banner {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 10px 16px;
  background: rgba(255, 87, 34, 0.15);
  border-bottom: 1px solid #ff5722;
  color: #ff8a65;
  font-size: 0.875rem;
  flex-shrink: 0;
}

.btn-reconnect {
  padding: 4px 16px;
  background: #ff5722;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}

.is-mobile .bottom-bar {
  flex-shrink: 0;
  background: #1a1a2e;
  padding-bottom: env(safe-area-inset-bottom, 0px);
  z-index: 102;
}

/* Mobile: 隐藏 xterm 系统键盘 (只通过工具栏按钮触发) */
.is-mobile .terminal-body :deep(.xterm-helper-textarea) {
  position: fixed !important;
  top: 0 !important;
  left: 0 !important;
  width: 1px !important;
  height: 1px !important;
  margin: 0 !important;
  transform: none !important;
  pointer-events: none !important;
  opacity: 0 !important;
}

</style>
