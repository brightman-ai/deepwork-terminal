<template>
  <div class="cli-terminal-page" data-testid="cli-terminal-page" :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }">
    <div class="terminal-header">
      <button class="btn-back" @click="$router.push('/cli')">
        <span class="back-arrow">&larr;</span>
        <span v-if="!isMobile" class="back-text">Sessions</span>
      </button>
      <span class="session-name">{{ sessionName }}</span>
      <ConnectionStatus :status="wsStatus" :rtt="netStats.rtt" :upload-bps="netStats.uploadBps" :download-bps="netStats.downloadBps" />
      <AgentStatusBadge :state="agentState" :notifications="notifications" />
      <!-- WS7 primary entry — install/notify icon in the title row. -->
      <InstallNotifyIcon @open="installGuideOpen = true" />
      <SetupWizardIcon :inline="true" />
    </div>

    <!-- tmux pane bar: its own row directly under the title, OUTSIDE .terminal-body,
         so taps don't hit copy-mode handlers / touchball. Self-hides unless attached. -->
    <TmuxPaneBar :session-id="sessionId" @send-key="onSendKey" @open-notify="installGuideOpen = true" />

    <div v-if="wsStatus === 'preempted'" class="preempted-banner">
      <span>Session taken over by another device.</span>
      <button class="btn-reconnect" @click="wsReconnect()">Reconnect</button>
    </div>

    <div
      class="terminal-body"
      data-testid="terminal-body"
      ref="terminalBodyRef"
      @touchstart.passive="onTerminalTouchStart"
      @touchend.passive="onTerminalTouchEnd"
    >
      <XtermTerminal
        ref="xtermRef"
        :active="true"
        :disable-proxy="!isMobile"
        :ime-fallback-enabled="isMobile && activeMode === 'keyboard'"
        diagnostic-surface="legacy"
        @data="onTerminalData"
        @resize="onTerminalResize"
        @ready="onTerminalReady"
      />
    </div>

    <!-- Bottom bar: flex child, NOT position:fixed — eliminates gap bugs -->
    <div v-if="isMobile" class="bottom-bar">
      <!-- WS4: persistent tmux quick row — sits directly above the main Toolbar. -->
      <TmuxQuickBar
        :session-id="sessionId"
        @send-key="onSendKey"
        @open-sheet="tmuxSheetOpen = true"
      />
      <Toolbar
        :session-id="sessionId"
        :sticky-shift="stickyShift"
        :sticky-ctrl="stickyCtrl"
        :sticky-alt="stickyAlt"
        :active-panel="activePanelLabel"
        :keyboard-up="activeMode === 'keyboard'"
        @send-key="onSendKey"
        @clipboard="onClipboard"
        @toggle-numpad="onTogglePanel('numpad')"
        @toggle-compose="onTogglePanel('compose')"
        @toggle-shift="stickyShift = !stickyShift"
        @toggle-ctrl="stickyCtrl = !stickyCtrl"
        @toggle-alt="stickyAlt = !stickyAlt"
        @toggle-hud="hudVisible = !hudVisible"
        @toggle-keyboard="onToggleKeyboard"
        @attach="onAttachClick"
      />
      <KeyboardPanel v-if="activeMode === 'numpad'" @send-key="onSendKey" @clipboard="onClipboard" @close="onToggleKeyboard" />
      <ComposeBar v-if="activeMode === 'compose'" @send="onComposeSend" @close="() => { activeMode = 'idle' }" />
    </div>

    <!-- Floating elements: touchball, selection overlay, copy btn, HUD -->
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

    <!-- WS8: tmux status sheet (mobile bottom-sheet / desktop popover). -->
    <TmuxStatusSheet
      :session-id="sessionId"
      :open="tmuxSheetOpen"
      @close="tmuxSheetOpen = false"
    />

    <!-- WS5: 收纳抽屉 — images / files / input history. Send reuses the canonical
         compose send path; edge handle (in the drawer) summons it. -->
    <ResourceDrawer
      :session-id="sessionId"
      v-model:open="resourceDrawerOpen"
      @send="onComposeSend"
    />

    <!-- WS7: platform-aware install + notification guide (shared by both entries). -->
    <InstallGuideSheet
      :session-id="sessionId"
      :open="installGuideOpen"
      @close="installGuideOpen = false"
    />

    <AuthDialog
      :visible="showAuthDialog"
      @dismiss="dismissAuthDialog"
      @authenticated="onAuthenticated"
    />

    <!-- Hidden file input for 📎 attach button -->
    <input
      ref="attachInputRef"
      type="file"
      accept="image/*,.pdf,.txt,.md,.json,.csv,.log,.py,.go,.js,.ts,.sh,.yaml,.yml,.toml"
      multiple
      style="display: none"
      @change="onAttachFileSelected"
    />

    <!-- KeyCastr-style keystroke overlay -->
    <KeyCastrOverlay
      v-if="keystrokeHudVisible"
      :entries="keycastEntries"
      :bottom-offset="keycastBottomOffset"
      data-testid="keystroke-hud"
    />

    <!-- Left-edge HUD tab ("菜单按钮"): the keystroke-HUD toggle lifted out of the
         header into a fixed left-edge affordance. Vertically draggable (useEdgeDrag,
         same composable as the drawer handle) so it can be slid off covered text; a
         short tap still toggles the HUD, a drag repositions + persists. -->
    <Teleport to="body">
      <button
        ref="hudTabEl"
        class="hud-edge-tab"
        :class="{ 'hud-edge-tab--on': keystrokeHudVisible, 'is-mobile': isMobile }"
        :style="hudTabStyle"
        type="button"
        title="按键 HUD（可上下拖动）"
        data-testid="hud-edge-tab"
        @click="keystrokeHudVisible = !keystrokeHudVisible"
      >HUD</button>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Terminal } from 'xterm'
import XtermTerminal from '@terminal/components/terminal-session/XtermTerminal.vue'
import ConnectionStatus from '@terminal/components/terminal-session/ConnectionStatus.vue'
import AgentStatusBadge from '@terminal/components/terminal-session/AgentStatusBadge.vue'
import SetupWizardIcon from '@ce/components/wizard/SetupWizardIcon.vue'
import AuthDialog from '@terminal/components/terminal-session/AuthDialog.vue'
import MobileOverlay from '@terminal/components/terminal-session/MobileOverlay.vue'
import Toolbar from '@terminal/components/terminal-session/Toolbar.vue'
import KeyboardPanel from '@terminal/components/terminal-session/KeyboardPanel.vue'
import TmuxPaneBar from '@terminal/components/terminal-session/TmuxPaneBar.vue'
import TmuxQuickBar from '@terminal/components/terminal-session/TmuxQuickBar.vue'
import TmuxStatusSheet from '@terminal/components/terminal-session/TmuxStatusSheet.vue'
import ResourceDrawer from '@terminal/components/terminal-session/ResourceDrawer.vue'
import InstallGuideSheet from '@terminal/components/terminal-session/InstallGuideSheet.vue'
import InstallNotifyIcon from '@terminal/components/terminal-session/InstallNotifyIcon.vue'
import ComposeBar from '@terminal/components/terminal-session/ComposeBar.vue'
import KeyCastrOverlay from '@terminal/components/terminal-session/KeyCastrOverlay.vue'
import { useWebSocketClient } from '@terminal/composables/cli/useWebSocketClient'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { useEdgeDrag } from '@terminal/composables/cli/useEdgeDrag'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { useFocusStateMachine } from '@terminal/composables/cli/useFocusStateMachine'
import { useAnchorStateMachine } from '@terminal/composables/cli/useAnchorStateMachine'
import { useTerminalCoordMapper } from '@terminal/composables/cli/useTerminalCoordMapper'
import { useComposeSendStrategy } from '@terminal/composables/cli/useComposeSendStrategy'
import { useHudCollector } from '@terminal/composables/cli/useHudCollector'
import { useKeyCastrHud } from '@terminal/composables/cli/useKeyCastrHud'
import { useCliPasteResolver } from '@terminal/composables/cli/useCliPasteResolver'
import { useClipboardText } from '@terminal/composables/cli/useClipboardText'
import { useAgentIntel } from '@terminal/composables/cli/useAgentIntel'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { useForegroundAgentNotify } from '@terminal/composables/cli/useForegroundAgentNotify'
import { focusWithoutViewportScroll, resetViewportScroll, useVisualKeyboardInset } from '@terminal/composables/cli/useVisualKeyboardInset'
import { reportCliInputDiagnostic, summarizeBytes, summarizeText, useCliTerminalInputTelemetry } from '@terminal/composables/cli/useCliInputDiagnostics'
import type { WSControlMessage, CellCoord, AnchorState, AgentState } from '@terminal/types/terminal'

const route = useRoute()
const router = useRouter()
const sessionId = computed(() => route.params.id as string)
const sessionName = ref('Terminal')
const xtermRef = ref<InstanceType<typeof XtermTerminal>>()
const mobileOverlayRef = ref<InstanceType<typeof MobileOverlay>>()
const terminalBodyRef = ref<HTMLDivElement>()
const attachInputRef = ref<HTMLInputElement>()
const { isMobile } = useDeviceDetection()
const { cliFetch, authCode, showAuthDialog, dismissAuthDialog } = useCliAuth()
const tmuxDetected = ref(false)
const tmuxSheetOpen = ref(false)
const installGuideOpen = ref(false) // WS7: install + notify guide sheet

// WS5: resource drawer open state, persisted across reloads.
const RESOURCE_DRAWER_KEY = 'dw.resourceDrawer.open'
const resourceDrawerOpen = ref(localStorage.getItem(RESOURCE_DRAWER_KEY) === '1')
watch(resourceDrawerOpen, (v) => localStorage.setItem(RESOURCE_DRAWER_KEY, v ? '1' : '0'))

const focusSM = useFocusStateMachine()
const anchorSM = useAnchorStateMachine()
const anchorState = computed<AnchorState>(() => anchorSM.state.value ?? 'IDLE')
const anchor1 = computed<CellCoord | null>(() => anchorSM.anchor1.value ?? null)
const anchor2 = computed<CellCoord | null>(() => anchorSM.anchor2.value ?? null)
const composeSend = useComposeSendStrategy()
const hud = useHudCollector()
const hudVisible = ref(false)
const keyCastr = useKeyCastrHud()
const keycastEntries = keyCastr.entries
const { agentState, notifications, handleWSMessage: agentWSHandler } = useAgentIntel(() => sessionId.value)
const tmux = useTmuxState(() => sessionId.value)
// WS7: open-but-unfocused-tab notification fallback (backend push covers no-tab).
useForegroundAgentNotify(() => sessionId.value)

watch([agentState, notifications], ([state, list]) => {
  if (hasTmuxAgentTopology(state, list)) tmuxDetected.value = true
}, { immediate: true })

function hasTmuxAgentTopology(state: AgentState | null, list: AgentState[]): boolean {
  return state?.tmuxWindow != null || list.some(item => item.tmuxWindow != null)
}

const coordMapper = useTerminalCoordMapper(() => {
  const term = xtermRef.value?.terminal?.()
  const container = xtermRef.value?.$el as HTMLElement | undefined
  if (!term || !container) {
    return { cols: 80, rows: 24, cellWidth: 9, cellHeight: 17, offsetX: 0, offsetY: 0 }
  }
  const rect = container.getBoundingClientRect()
  return {
    cols: term.cols,
    rows: term.rows,
    cellWidth: rect.width / term.cols,
    cellHeight: rect.height / term.rows,
    offsetX: rect.left,
    offsetY: rect.top,
  }
})

// --- JD-style state ---
const activeMode = ref<'idle' | 'keyboard' | 'numpad' | 'compose'>('idle')
const stickyShift = ref(false)
const stickyCtrl = ref(false)
const stickyAlt = ref(false)
const viewportY = ref(0)
const terminalRows = ref(24)
const { keyboardInset: keyboardHeight, syncKeyboardInset } = useVisualKeyboardInset({ enabled: () => isMobile.value })
let keyboardWanted = false // gate for xterm textarea focus prevention

// Toolbar panel label for active state
const activePanelLabel = computed<'none' | 'numpad' | 'compose'>(() => {
  if (activeMode.value === 'numpad') return 'numpad'
  if (activeMode.value === 'compose') return 'compose'
  return 'none'
})

const keycastBottomOffset = computed(() =>
  80 + (activeMode.value === 'keyboard' ? keyboardHeight.value : 0)
)

const encoder = new TextEncoder()

const {
  status: wsStatus,
  netStats,
  connect,
  reconnect: wsReconnect,
  sendBinary: sendBinaryRaw,
  sendResize,
  onMessage,
} = useWebSocketClient(() => sessionId.value, { authToken: authCode.value })

const inputTelemetry = useCliTerminalInputTelemetry({
  surface: 'legacy',
  sessionId: () => sessionId.value,
})

function sendBinary(data: Uint8Array, route = 'direct'): void {
  inputTelemetry.recordSend(data, route)
  sendBinaryRaw(data)
}

const pasteResolver = useCliPasteResolver({
  sessionId: () => sessionId.value,
  surface: 'legacy',
  sendBinary: (data) => sendBinary(data, 'clipboard'),
  openAttachmentPicker: () => attachInputRef.value?.click(),
  hudRecord: (kind, message) => hud.record(kind, message),
})

const clipboardText = useClipboardText({
  surface: 'legacy',
  sendBinary: (data) => sendBinary(data, 'clipboard'),
  hudRecord: (kind, message) => hud.record(kind, message),
})

// HUD snapshot sync
watch(wsStatus, (val) => {
  hud.updateSnapshot({ ws: val })
  if (val === 'connected') {
    setTimeout(() => {
      const xterm = xtermRef.value
      if (xterm) {
        xterm.fit()
        const term = xterm.terminal?.()
        if (term) {
          sendResize(term.cols, term.rows)
          hud.updateSnapshot({ pty: `${term.cols}x${term.rows}` })
        }
      }
    }, 100)
  }
}, { immediate: true })

// Safari: refresh terminal when page becomes visible
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
  if (!isMobile.value || activeMode.value !== 'keyboard') return
  if (!hasViewportScrollOffset()) return
  if (viewportScrollLockRaf) return
  viewportScrollLockRaf = window.requestAnimationFrame(() => {
    viewportScrollLockRaf = 0
    resetViewportScroll()
  })
}

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  document.addEventListener('keydown', onKeydownDirect, { capture: true }) // L1: direct PTY bypass
  document.addEventListener('paste', onClipboardPaste, { capture: true })
  window.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('resize', lockKeyboardViewportScroll)
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

// Auto-detect iOS keyboard dismiss (checkmark button)
watch(keyboardHeight, (val, oldVal) => {
  reportCliInputDiagnostic('keyboard.inset', { surface: 'legacy', val, oldVal, activeMode: activeMode.value })
  lockKeyboardViewportScroll()
  if (oldVal > 100 && val < 50 && activeMode.value === 'keyboard') {
    activeMode.value = 'idle'
    keyboardWanted = false
    resetViewportScroll()
    hud.record('state', 'keyboard auto-dismissed (iOS)')
  }
})

// Refit terminal when panel/keyboard changes
watch(activeMode, () => {
  reportCliInputDiagnostic('active-mode.change', { surface: 'legacy', activeMode: activeMode.value })
  nextTick(() => {
    xtermRef.value?.fit()
    const term = xtermRef.value?.terminal?.()
    if (term) {
      sendResize(term.cols, term.rows)
      terminalRows.value = term.rows
    }
  })
})

// ── Keystroke HUD — 3-layer diagnosis ───────────────────────────────────────
const keystrokeHudVisible = ref(true)
// Left-edge HUD tab: vertically draggable along the left edge (shared useEdgeDrag).
const { el: hudTabEl, style: hudTabStyle } = useEdgeDrag({ storageKey: 'dw.hudTab.top' })
interface HudEntry { key: string; l1: boolean; l2: boolean; l3: boolean; dt: number; ts: number }
const keystrokeLog = ref<HudEntry[]>([])
const decoder = new TextDecoder()

function hudKeydown(e: KeyboardEvent) {
  if (!keystrokeHudVisible.value) return
  if (e.key === 'Shift' || e.key === 'Control' || e.key === 'Alt' || e.key === 'Meta') return
  const label = e.key.length === 1 ? e.key : `[${e.key}]`
  keystrokeLog.value.push({ key: label, l1: true, l2: false, l3: false, dt: 0, ts: performance.now() })
  if (keystrokeLog.value.length > 30) keystrokeLog.value.shift()
}
function hudXterm(data: Uint8Array) {
  if (!keystrokeHudVisible.value) return
  const text = decoder.decode(data)
  const now = performance.now()
  for (const ch of text) {
    if (ch.charCodeAt(0) < 32) continue
    const entry = [...keystrokeLog.value].reverse().find((e: HudEntry) => e.key === ch && !e.l2)
    if (entry) { entry.l2 = true; entry.dt = Math.round(now - entry.ts) }
    else {
      keystrokeLog.value.push({ key: ch, l1: false, l2: true, l3: false, dt: 0, ts: now })
      if (keystrokeLog.value.length > 30) keystrokeLog.value.shift()
    }
  }
}
function hudWs(data: Uint8Array) {
  if (!keystrokeHudVisible.value) return
  const text = decoder.decode(data)
  for (const ch of text) {
    if (ch.charCodeAt(0) < 32) continue
    const entry = [...keystrokeLog.value].reverse().find((e: HudEntry) => e.key === ch && e.l2 && !e.l3)
    if (entry) entry.l3 = true
  }
}

async function onClipboardPaste(e: ClipboardEvent) {
  await pasteResolver.handlePasteEvent(e)
}

// ── Attach file handler ──────────────────────────────────────────────────────
function onAttachClick() {
  attachInputRef.value?.click()
}

async function onAttachFileSelected() {
  const input = attachInputRef.value
  if (!input?.files?.length) return

  await pasteResolver.uploadFilesFromInput(Array.from(input.files), 'manual-attach')
  // Reset input so same file can be selected again
  input.value = ''
}

// ── Terminal input handler ───────────────────────────────────────────────────
// [TH-0501-m9j] WKWebView: document keydown sends printable ASCII directly.
// xterm's onData skips single ASCII on WKWebView (handled here instead).
// xterm's onKey sends special keys/Ctrl combos on WKWebView.
// Chrome/Edge: everything goes through xterm's onData (this handler is no-op).
const isWKWebView = navigator.userAgent.includes('AppleWebKit') &&
  !navigator.userAgent.includes('Chrome') &&
  !navigator.userAgent.includes('Safari')

function onKeydownDirect(e: KeyboardEvent) {
  hudKeydown(e)
  keyCastr.feed(e)
  reportCliInputDiagnostic('document.keydown', {
    surface: 'legacy',
    key: summarizeText(e.key),
    code: e.code,
    isComposing: e.isComposing,
    route: isWKWebView ? 'wk-candidate' : 'observe-only',
  })
  if (!isWKWebView) return
  if (e.isComposing || e.metaKey || e.altKey || e.ctrlKey) return
  if (e.key.length !== 1) return
  // Prevent default browser action (char insertion into xterm textarea) so xterm.js's
  // internal onData/onKey don't double-send. xterm.js still processes the event for
  // cursor management, but our onKey guard (XtermTerminal.vue) also skips printable ASCII.
  e.preventDefault()
  sendBinary(encoder.encode(e.key), 'document-keydown')
}

function onTerminalData(data: Uint8Array) {
  hudXterm(data)
  reportCliInputDiagnostic('terminal-data', { surface: 'legacy', data: summarizeBytes(data) })
  sendTerminalData(data)
}

function sendTerminalData(data: Uint8Array) {
  if (data.length === 1) {
    let byte = data[0]
    // Ctrl-sticky + v/V → real OS-clipboard paste (universal Ctrl+V muscle memory),
    // not the \x16 (quoted-insert) byte that Ctrl-masking would otherwise produce.
    if (stickyCtrl.value && (byte === 0x76 || byte === 0x56)) {
      stickyCtrl.value = false
      void clipboardText.pasteFromClipboard('sticky-ctrl-v')
      return
    }
    if (stickyCtrl.value) { byte = byte & 0x1f; stickyCtrl.value = false }
    if (stickyAlt.value) { sendBinary(encoder.encode('\x1b'), 'sticky-alt'); stickyAlt.value = false }
    if (stickyShift.value) { if (byte >= 0x61 && byte <= 0x7a) byte -= 0x20; stickyShift.value = false }
    const out = new Uint8Array([byte])
    hudWs(out)
    sendBinary(out, 'xterm-data')
  } else {
    hudWs(data)
    sendBinary(data, 'xterm-data')
  }
}

function onTerminalResize(cols: number, rows: number) {
  sendResize(cols, rows)
  terminalRows.value = rows
  hud.record('resize', `${cols}x${rows}`)
}

function onTerminalReady(terminal: Terminal) {
  if (isMobile.value) {
    focusSM.focusTerminal()
    // Prevent xterm from opening system keyboard on touch (only open via keyboard button)
    const helperTA = terminal.element?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (helperTA) {
      helperTA.addEventListener('focus', () => {
        if (!keyboardWanted) {
          helperTA.blur()
        }
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
        case 'shell_exit':
          xtermRef.value?.write('\r\n[Process exited]\r\n')
          break
        case 'preempted':
          xtermRef.value?.write('\r\n[Session taken over by another client]\r\n')
          break
        case 'agent_state':
          agentWSHandler(msg.payload)
          break
        case 'tmux_state':
          tmux.handleWSMessage(msg.payload)
          break
        case 'error':
          console.error('WS error:', msg.payload)
          break
      }
    }
  )
  connect()
}

// --- Keyboard/Panel mode management ---

function showKeyboard() {
  activeMode.value = 'keyboard'
  keyboardWanted = true
  reportCliInputDiagnostic('keyboard.show', { surface: 'legacy' })
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
    // Dismiss
    activeMode.value = 'idle'
    keyboardWanted = false
    const textarea = xtermRef.value?.$el?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (textarea) textarea.blur()
    reportCliInputDiagnostic('keyboard.dismiss', { surface: 'legacy', source: 'toolbar' })
    hud.record('state', 'dismiss keyboard')
  } else {
    showKeyboard()
  }
}

function onTogglePanel(panel: 'numpad' | 'compose') {
  if (activeMode.value === panel) {
    // Toggle back to system keyboard
    showKeyboard()
  } else {
    activeMode.value = panel
    // Blur to dismiss system keyboard when showing a panel
    const textarea = xtermRef.value?.$el?.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null
    if (textarea) textarea.blur()
  }
  hud.record('state', `panel: ${activeMode.value}`)
}

// --- Key sending with sticky modifier support ---

function onSendKey(key: string) {
  // PgUp/PgDn: tmux → send to PTY (tmux handles scrollback). Non-tmux → xterm.js viewport scroll.
  const term = xtermRef.value?.terminal?.()
  if (term && (key === '\x1b[5~' || key === '\x1b[6~')) {
    const isPgUp = key === '\x1b[5~'
    // Always send to PTY — works in tmux copy mode, less, vi, shell history, etc.
    sendBinary(encoder.encode(key))
    // Extend anchors if in selection mode
    if (anchorSM.state.value === 'HAS_BOTH' || anchorSM.state.value === 'HAS_ANCHOR_1') {
      if (isPgUp) {
        const a1 = anchorSM.anchor1.value
        if (a1) {
          const newBuf = Math.max(0, (a1.bufferRow ?? a1.row) - term.rows)
          anchorSM.placeAnchor1({ col: a1.col, row: 0, bufferRow: newBuf })
          applyXtermSelection()
        }
      } else if (anchorSM.state.value === 'HAS_BOTH') {
        const a2 = anchorSM.anchor2.value
        if (a2) {
          const newBuf = (a2.bufferRow ?? a2.row) + term.rows
          anchorSM.placeAnchor2({ col: a2.col, row: term.rows - 1, bufferRow: newBuf })
          applyXtermSelection()
        }
      }
    }
    hud.record('keyboard', `${isPgUp ? 'PgUp' : 'PgDn'} (${tmuxDetected.value ? 'tmux PTY' : 'xterm scroll'})`)
    return
  }

  // Arrow keys and Home/End in tmux: send to PTY (tmux handles them)
  if (tmuxDetected.value && (key === '\x1b[H' || key === '\x1b[F' ||
      key === '\x1b[A' || key === '\x1b[B' || key === '\x1b[C' || key === '\x1b[D')) {
    sendBinary(encoder.encode(key))
    hud.record('keyboard', 'nav key → tmux PTY')
    return
  }

  let modified = key
  // Sticky Shift: handle special keys first, then uppercase for single chars
  if (stickyShift.value) {
    if (key === '\t') { modified = '\x1b[Z'; stickyShift.value = false } // Shift+Tab = backtab
    else if (key === '\r') { modified = '\r'; stickyShift.value = false } // Shift+Enter = Enter
    else if (key.length === 1) { modified = key.toUpperCase(); stickyShift.value = false }
    else { stickyShift.value = false } // consume shift for multi-byte sequences
  }
  if (stickyCtrl.value && (modified === 'v' || modified === 'V')) {
    // Ctrl-sticky + v/V → real OS-clipboard paste, not the \x16 quoted-insert byte.
    stickyCtrl.value = false
    void clipboardText.pasteFromClipboard('sticky-ctrl-v')
    return
  }
  if (stickyCtrl.value) {
    if (modified.length === 1) {
      modified = String.fromCharCode(modified.charCodeAt(0) & 0x1f)
    }
    stickyCtrl.value = false
  }
  if (stickyAlt.value) {
    modified = '\x1b' + modified
    stickyAlt.value = false
  }
  sendBinary(encoder.encode(modified))
  hud.record('keyboard', `key: ${JSON.stringify(modified).slice(0, 20)}`)
}

// Clipboard operations via browser API (not terminal control chars)
function onClipboard(op: string) {
  const term = xtermRef.value?.terminal?.()
  switch (op) {
    case 'copy':
    case 'cut':
      if (term) {
        const sel = term.getSelection()
        if (sel) {
          clipboardWrite(sel)
          hud.record('state', `clipboard ${op}: ${sel.length} chars`)
        }
      }
      break
    case 'paste':
      void clipboardText.pasteFromClipboard('paste-button')
      break
    case 'undo':
      sendBinary(encoder.encode('\x1a')) // Ctrl+Z
      break
    case 'selectAll':
      if (term) term.selectAll()
      break
    case 'find':
      sendBinary(encoder.encode('\x06')) // Ctrl+F (for tmux/less search)
      break
  }
}

function onComposeSend(text: string) {
  const chunks = composeSend.encode(text)
  for (const chunk of chunks) sendBinary(chunk)
  activeMode.value = 'idle'
  hud.record('keyboard', `compose: ${text.length} chars`)
}

// --- Touchball + cursor ---

// Terminal area tap → move ball (cursor follows via fixed offset)
let termTouchStartX = 0
let termTouchStartY = 0
function onTerminalTouchStart(e: TouchEvent) {
  if (!isMobile.value) return
  const touch = e.touches[0]
  if (touch) {
    termTouchStartX = touch.clientX
    termTouchStartY = touch.clientY
  }
}
function onTerminalTouchEnd(e: TouchEvent) {
  if (!isMobile.value) return
  const touch = e.changedTouches[0]
  if (!touch) return
  const dx = Math.abs(touch.clientX - termTouchStartX)
  const dy = Math.abs(touch.clientY - termTouchStartY)
  if (dx < 10 && dy < 10) {
    // Move touchball to tap position (cursor follows via fixed offset)
    mobileOverlayRef.value?.moveBall(touch.clientX, touch.clientY)
  }
}

// Touchball tap — behavior depends on anchor state:
// IDLE: just move cursor (no accidental anchor placement)
// NO_ANCHOR: place anchor 1
// HAS_ANCHOR_1: place anchor 2
// HAS_BOTH: move nearest anchor
function onTouchballTap(x: number, y: number) {
  const cell = coordMapper.screenToCell(x, y)
  const cellBuf: CellCoord = { ...cell, bufferRow: viewportY.value + cell.row }

  if (anchorSM.state.value === 'IDLE') {
    // Just move cursor — no anchor. Long-press enters selection.
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

// Double-tap → select word at cursor position
function onTouchballDoubleTap(x: number, y: number) {
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  const cell = coordMapper.screenToCell(x, y)
  // Use xterm.js to select the word at this position
  // Simple heuristic: select from cell expanding outward
  const line = term.buffer.active.getLine(cell.row + viewportY.value)
  if (!line) return
  const lineStr = line.translateToString(true)
  let start = cell.col, end = cell.col
  while (start > 0 && /\S/.test(lineStr[start - 1] || '')) start--
  while (end < lineStr.length - 1 && /\S/.test(lineStr[end + 1] || '')) end++
  term.select(start, cell.row, end - start + 1)
  // Set anchors for visual
  const startBuf = viewportY.value + cell.row
  anchorSM.enterSelection()
  anchorSM.placeAnchor1({ col: start, row: cell.row, bufferRow: startBuf })
  anchorSM.placeAnchor2({ col: end, row: cell.row, bufferRow: startBuf })
  hud.record('touch', `double-tap: select word at (${cell.col},${cell.row})`)
}

// Triple-tap → select entire line
function onTouchballTripleTap(x: number, y: number) {
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  const cell = coordMapper.screenToCell(x, y)
  term.select(0, cell.row, term.cols)
  const bufRow = viewportY.value + cell.row
  anchorSM.enterSelection()
  anchorSM.placeAnchor1({ col: 0, row: cell.row, bufferRow: bufRow })
  anchorSM.placeAnchor2({ col: term.cols - 1, row: cell.row, bufferRow: bufRow })
  hud.record('touch', `triple-tap: select line ${cell.row}`)
}

// Long-press — dual purpose:
// If HAS_BOTH (selection exists): copy + clear selection
// Otherwise: enter selection mode + place anchor 1 at cursor position
function onTouchballLongPress(x: number, y: number) {
  if (anchorSM.state.value === 'HAS_BOTH') {
    // Copy selection to clipboard + clear
    const term = xtermRef.value?.terminal?.()
    if (term) {
      const sel = term.getSelection()
      if (sel) {
        clipboardWrite(sel)
        hud.record('state', `long-press copy: ${sel.length} chars`)
      }
    }
    anchorSM.cancel()
    return
  }

  // Enter selection mode + place anchor 1
  const cell = coordMapper.screenToCell(x, y)
  const cellBuf: CellCoord = { ...cell, bufferRow: viewportY.value + cell.row }
  anchorSM.enterSelection()
  anchorSM.placeAnchor1(cellBuf)
  applyXtermSelection()
  hud.record('touch', `long-press: enter selection at (${cell.col},${cell.row})`)
}

// Robust clipboard write — works in non-secure contexts (HTTP) and iOS Safari
function clipboardWrite(text: string) {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => {
      clipboardWriteFallback(text)
    })
  } else {
    clipboardWriteFallback(text)
  }
}
function clipboardWriteFallback(text: string) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.cssText = 'position:fixed;left:-9999px;top:-9999px;opacity:0'
  document.body.appendChild(ta)
  ta.select()
  try { document.execCommand('copy') } catch {}
  document.body.removeChild(ta)
}

// Copy from "Copy" button — copy + clear anchors
function onSelectionCopy() {
  const term = xtermRef.value?.terminal?.()
  if (term) {
    const sel = term.getSelection()
    if (sel) {
      clipboardWrite(sel)
      hud.record('state', `copy: ${sel.length} chars`)
    }
    term.clearSelection()
  }
  anchorSM.cancel()
}

// Anchor drag from SelectionOverlay
function onAnchorDrag(anchorId: 1 | 2, cell: CellCoord) {
  if (anchorId === 1) anchorSM.placeAnchor1(cell)
  else anchorSM.placeAnchor2(cell)
  applyXtermSelection()
}

function applyXtermSelection() {
  const ordered = anchorSM.orderedAnchors.value
  if (!ordered) return
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  const startRow = (ordered.start.bufferRow ?? ordered.start.row) - viewportY.value
  const endRow = (ordered.end.bufferRow ?? ordered.end.row) - viewportY.value
  term.select(ordered.start.col, startRow,
    (endRow - startRow) * term.cols + (ordered.end.col - ordered.start.col) + 1)
}

// --- Auth ---

function onAuthenticated() {
  dismissAuthDialog()
  fetchSessionDetails()
}

async function fetchSessionDetails() {
  try {
    const resp = await cliFetch(`/api/sessions/${sessionId.value}`)
    if (resp.status === 404) { router.replace('/cli'); return }
    if (resp.ok) {
      const data = await resp.json()
      sessionName.value = data.title || data.name
      if (data.tmuxDetected) tmuxDetected.value = true
    }
  } catch { /* ignore */ }
}

onMounted(fetchSessionDetails)
</script>

<style scoped>
.cli-terminal-page {
  display: flex;
  flex-direction: column;
  height: var(--dw-app-viewport-height, 100dvh);
  max-height: var(--dw-app-viewport-height, 100dvh);
  width: 100vw;
  background: hsl(var(--background));
  overflow: hidden;
}
.terminal-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  padding-top: max(8px, env(safe-area-inset-top, 8px));
  background: hsl(var(--card));
  border-bottom: 1px solid hsl(var(--border));
  min-height: 40px;
  flex-shrink: 0;
}
.is-mobile .terminal-header {
  gap: 4px;
  padding-left: 8px;
  padding-right: 8px;
}
.is-desktop .terminal-header {
  padding-left: 80px; /* macOS 红绿灯按钮宽度 */
}
.btn-back {
  display: flex;
  align-items: center;
  gap: 4px;
  background: none;
  border: 1px solid hsl(var(--border));
  color: hsl(var(--foreground));
  padding: 4px 10px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.875rem;
  flex-shrink: 0;
}
.btn-back:hover { background: hsl(var(--accent)); }
.session-name { color: hsl(var(--foreground)); font-weight: 500; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.is-mobile .session-name {
  flex: 1 1 56px;
  max-width: 96px;
  font-size: 0.78rem;
}
.is-mobile .terminal-header :deep(.connection-status) {
  padding: 2px 5px;
}
.is-mobile .terminal-header :deep(.status-text),
.is-mobile .terminal-header :deep(.net-bw) {
  display: none;
}
.is-mobile .terminal-header :deep(.agent-circles) {
  max-width: 76px;
  overflow-x: auto;
  scrollbar-width: none;
}
.is-mobile .terminal-header :deep(.agent-circles::-webkit-scrollbar) {
  display: none;
}
.terminal-body {
  flex: 1;
  position: relative;
  overflow: hidden;
  width: 100%;
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

/* Bottom bar: flex child — no position:fixed, no gap */
.is-mobile .bottom-bar {
  flex-shrink: 0;
  background: #1a1a2e;
  padding-bottom: env(safe-area-inset-bottom, 0px);
  z-index: 102;
}

/* Mobile: xterm textarea hidden by default (keyboard triggered explicitly) */
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
/* Left-edge HUD tab — slim vertical affordance clinging to the LEFT viewport edge,
   mirror of ResourceDrawer's .rd-handle. Draggable via useEdgeDrag (touch_action:none
   so the gesture isn't stolen by page scroll). top:50%/translateY centers it until a
   persisted drag offset overrides `top` (composable sets transform:none then). */
.hud-edge-tab {
  position: fixed;
  top: 50%;
  left: 0;
  transform: translateY(-50%);
  z-index: 290;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 54px;
  padding: 0;
  font-size: 10px;
  letter-spacing: 0.5px;
  background: #160f22;
  border: 1px solid #3a2860;
  border-left: none;
  border-radius: 0 9px 9px 0;
  color: #666;
  cursor: pointer;
  box-shadow: 4px 0 18px rgba(0, 0, 0, 0.45);
  touch-action: none;
}
.hud-edge-tab.is-mobile { width: 30px; height: 62px; }
.hud-edge-tab:active { background: #1f1533; }
.hud-edge-tab--on { color: #4ade80; border-color: #4ade80; }
</style>
