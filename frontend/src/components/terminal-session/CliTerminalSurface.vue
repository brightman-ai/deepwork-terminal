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

    <!-- Per-surface status row — the SSOT. A SIBLING directly above .terminal-body (NOT
         inside it, so its taps never reach copy-mode touch handlers / the touchball). It
         is single-occupancy: when THIS session's shell is attached to tmux the pane bar
         REPLACES the single-session "终端 N idle" strip (one row, never stacked); otherwise
         the strip + connection/agent badges show. This is the exact dedup the hosts used
         to do — now owned by the surface so every host gets it for free. -->
    <!-- Per-surface status row (SSOT for BOTH hosts). One thin line, never stacked:
         · LEFT (ssr-main): scrollable — tmux window tabs when attached, else the agent badge.
           Only this zone scrolls.
         · RIGHT (ssr-health): the connection heartbeat, PINNED — it never scrolls and is never
           pushed off by a long tmux window list, so it stays fully visible regardless of pane
           count. This is the ONLY connection-health widget (the host tab bar no longer renders a
           duplicate) → no double 'ms', single source for terminal + pro. -->
    <div class="surface-status-row" :class="{ 'is-tmux': tmuxAttached }" data-testid="surface-status-row">
      <div class="ssr-main">
        <TmuxPaneBar
          v-if="tmuxReady && tmuxAttached"
          :session-id="sessionId"
          @send-key="onSendKey"
          @open-notify="openInstallGuide"
        />
        <AgentStatusBadge
          v-else-if="tmuxReady && (agentState || notifications.length > 0)"
          class="ssr-agent"
          :state="agentState"
          :notifications="notifications"
          data-testid="surface-agent-status"
        />
      </div>
      <ConnectionStatus
        class="ssr-health"
        :status="wsStatus"
        :rtt="netStats.rtt ?? 0"
        :tx-total="netStats.txTotal ?? 0"
        :rx-total="netStats.rxTotal ?? 0"
        :uptime-sec="netStats.uptimeSec ?? 0"
        data-testid="surface-connection-status"
      />
    </div>

    <!-- 终端区域 -->
    <div
      class="terminal-body"
      data-testid="terminal-body"
      ref="terminalBodyRef"
      :class="{ 'is-selecting': isSelecting }"
      @touchstart.passive="onTerminalTouchStart"
      @touchend.passive="onTerminalTouchEnd"
    >
      <!-- WS7 primary entry — always-visible install/notify icon, top-right of the
           terminal surface (the workbench title row lives in the parent CliTabBar). -->
      <InstallNotifyIcon class="surface-notify-icon" @open="installGuideOpen = true" />
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
        :keycast-on="keystrokeHudVisible"
        @send-key="onSendKey"
        @clipboard="onClipboard"
        @toggle-numpad="onTogglePanel('numpad')"
        @toggle-compose="onTogglePanel('compose')"
        @toggle-shift="stickyShift = !stickyShift"
        @toggle-ctrl="stickyCtrl = !stickyCtrl"
        @toggle-alt="stickyAlt = !stickyAlt"
        @toggle-hud="hudVisible = !hudVisible"
        @toggle-keycast="keystrokeHudVisible = !keystrokeHudVisible"
        @toggle-keyboard="onToggleKeyboard"
        @attach="onAttachClick"
      />
      <KeyboardPanel v-if="activeMode === 'numpad'" @send-key="onSendKey" @clipboard="onClipboard" @close="onToggleKeyboard" />
      <ComposeBar v-if="activeMode === 'compose'" :draft="composeDraft" @send="onComposeSend" @close="() => { activeMode = 'idle' }" />
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

    <!-- WS8: tmux status sheet (mobile bottom-sheet / desktop popover). -->
    <TmuxStatusSheet
      :session-id="sessionId"
      :open="tmuxSheetOpen"
      @close="tmuxSheetOpen = false"
      @send-key="onSendKey"
    />

    <!-- WS5: 收纳抽屉 — images / files / input history.
         @inject       → re-uses the clipboard-paste inject chokepoint (file path → PTY).
         @compose-draft → opens the ComposeBar with text inserted for editing (重发). -->
    <ResourceDrawer
      :session-id="sessionId"
      v-model:open="resourceDrawerOpen"
      @inject="onDrawerInject"
      @compose-draft="onDrawerComposeDraft"
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

    <!-- 隐藏文件输入 (📎 附件按钮) -->
    <input
      ref="attachInputRef"
      type="file"
      accept="image/*,.pdf,.txt,.md,.json,.csv,.log,.py,.go,.js,.ts,.sh,.yaml,.yml,.toml,.docx,.doc,.xlsx,.xls,.pptx,.ppt,.zip"
      multiple
      style="display: none"
      @change="onAttachFileSelected"
    />

    <!-- KeyCastr keystroke display (mobile only). Toggled from the main Toolbar's
         keycast button; defaults ON. No left-edge HUD tab — the toolbar is the SSOT toggle. -->
    <KeyCastrOverlay
      v-if="isMobile && keystrokeHudVisible"
      :entries="keycastEntries"
      :bottom-offset="keycastBottomOffset"
      data-testid="keystroke-hud"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { Terminal } from 'xterm'
import XtermTerminal from '@terminal/components/terminal-session/XtermTerminal.vue'
import AuthDialog from '@terminal/components/terminal-session/AuthDialog.vue'
import MobileOverlay from '@terminal/components/terminal-session/MobileOverlay.vue'
import Toolbar from '@terminal/components/terminal-session/Toolbar.vue'
import KeyboardPanel from '@terminal/components/terminal-session/KeyboardPanel.vue'
import TmuxQuickBar from '@terminal/components/terminal-session/TmuxQuickBar.vue'
import TmuxStatusSheet from '@terminal/components/terminal-session/TmuxStatusSheet.vue'
import TmuxPaneBar from '@terminal/components/terminal-session/TmuxPaneBar.vue'
import ConnectionStatus from '@terminal/components/terminal-session/ConnectionStatus.vue'
import AgentStatusBadge from '@terminal/components/terminal-session/AgentStatusBadge.vue'
import ResourceDrawer from '@terminal/components/terminal-session/ResourceDrawer.vue'
import InstallGuideSheet from '@terminal/components/terminal-session/InstallGuideSheet.vue'
import InstallNotifyIcon from '@terminal/components/terminal-session/InstallNotifyIcon.vue'
import ComposeBar from '@terminal/components/terminal-session/ComposeBar.vue'
import KeyCastrOverlay from '@terminal/components/terminal-session/KeyCastrOverlay.vue'
import { useWebSocketClient } from '@terminal/composables/cli/useWebSocketClient'
import { useDeviceDetection } from '@terminal/composables/cli/useDeviceDetection'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { useFocusStateMachine } from '@terminal/composables/cli/useFocusStateMachine'
import { useAnchorStateMachine } from '@terminal/composables/cli/useAnchorStateMachine'
import { useTerminalCoordMapper } from '@terminal/composables/cli/useTerminalCoordMapper'
import { useComposeSendStrategy } from '@terminal/composables/cli/useComposeSendStrategy'
import { useHudCollector } from '@terminal/composables/cli/useHudCollector'
import { useCliPasteResolver } from '@terminal/composables/cli/useCliPasteResolver'
import { useClipboardText } from '@terminal/composables/cli/useClipboardText'
import { useAgentIntel } from '@terminal/composables/cli/useAgentIntel'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { useForegroundAgentNotify } from '@terminal/composables/cli/useForegroundAgentNotify'
import { useKeyCastrHud } from '@terminal/composables/cli/useKeyCastrHud'
import { focusWithoutViewportScroll, resetViewportScroll, useVisualKeyboardInset } from '@terminal/composables/cli/useVisualKeyboardInset'
import { reportCliInputDiagnostic, summarizeBytes, summarizeText, useCliTerminalInputTelemetry } from '@terminal/composables/cli/useCliInputDiagnostics'
import type { WSControlMessage, CellCoord, AnchorState, WSConnectionStatus } from '@terminal/types/terminal'
import type { AgentState } from '@terminal/types/terminal'

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
// True whenever a copy-mode selection is being placed/adjusted. While selecting we suppress
// the xterm viewport's own finger-scroll (Safari momentum scroll) so the gesture only moves
// anchors; intentional view movement still flows through edgeScroll / PgUp-PgDn.
const isSelecting = computed(() => anchorState.value !== 'IDLE')
const anchor1 = computed<CellCoord | null>(() => anchorSM.anchor1.value ?? null)
const anchor2 = computed<CellCoord | null>(() => anchorSM.anchor2.value ?? null)
const composeSend = useComposeSendStrategy()
const hud = useHudCollector()
const hudVisible = ref(false)
const { agentState, notifications, handleWSMessage: agentWSHandler } = useAgentIntel(() => props.sessionId)
const tmux = useTmuxState(() => props.sessionId)
// Drives the single-occupancy status row: pane bar when THIS shell is attached to a tmux
// client, else the single-session "终端 N <status>" strip — the exact mutual exclusion the
// hosts used to wire up, now owned by the surface (SSOT).
const tmuxAttached = computed(() => tmux.attached.value)
// Three-state gate: until the first tmux snapshot arrives the topology is UNKNOWN, so we
// render NEITHER the pane bar NOR the agent badge (both would be a guessed state). The row
// keeps its height from the always-present ConnectionStatus on the right → no layout jump.
const tmuxReady = computed(() => tmux.ready.value)
// WS7: open-but-unfocused-tab notification fallback (backend push covers no-tab).
useForegroundAgentNotify(() => props.sessionId)
const keyCastr = useKeyCastrHud()
const keycastEntries = keyCastr.entries

// KeyCastr keystroke-display visibility. Defaults ON; toggled by the main Toolbar's
// keycast button (no left-edge HUD tab — the toolbar is the SSOT toggle).
const keystrokeHudVisible = ref(true)

// ─── Template refs ────────────────────────────────────────────────────────────

const xtermRef = ref<InstanceType<typeof XtermTerminal>>()
const mobileOverlayRef = ref<InstanceType<typeof MobileOverlay>>()
const terminalBodyRef = ref<HTMLDivElement>()
const attachInputRef = ref<HTMLInputElement>()

// ─── State ────────────────────────────────────────────────────────────────────

const tmuxDetected = ref(false)
const tmuxSheetOpen = ref(false)
const installGuideOpen = ref(false) // WS7: install + notify guide sheet

// WS5: resource drawer open state, persisted across reloads.
const RESOURCE_DRAWER_KEY = 'dw.resourceDrawer.open'
const resourceDrawerOpen = ref(localStorage.getItem(RESOURCE_DRAWER_KEY) === '1')
watch(resourceDrawerOpen, (v) => localStorage.setItem(RESOURCE_DRAWER_KEY, v ? '1' : '0'))
const activeMode = ref<'idle' | 'keyboard' | 'numpad' | 'compose'>('idle')
// Draft pushed into the ComposeBar by the drawer's 重发 action. A fresh object-less
// value would not re-trigger ComposeBar's watcher for an identical re-send, so we
// bump a nonce-suffixed ref only via the handler below.
const composeDraft = ref<string | undefined>(undefined)
const stickyShift = ref(false)
const stickyCtrl = ref(false)
const stickyAlt = ref(false)
const viewportY = ref(0)
const terminalRows = ref(24)
const { keyboardInset: keyboardHeight, syncKeyboardInset } = useVisualKeyboardInset({ enabled: () => isMobile.value })
let keyboardWanted = false

const activePanelLabel = computed<'none' | 'numpad' | 'compose'>(() => {
  if (activeMode.value === 'numpad') return 'numpad'
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

const clipboardText = useClipboardText({
  surface: 'workbench',
  sendBinary: (data) => sendBinary(data, 'clipboard'),
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

// While a selection is active, swallow finger-drag scrolling on the terminal body so the
// gesture adjusts anchors instead of scrolling the xterm viewport / page out from under the
// selection (the source of the "selection jumps on Safari scroll" bug). Must be a NON-passive
// listener — a `@touchmove.passive` template binding cannot call preventDefault. Intentional
// scrolling still happens via edgeScroll (anchor at top/bottom edge) and the PgUp/PgDn keys.
function onTerminalBodyTouchMove(e: TouchEvent) {
  if (!isMobile.value || !isSelecting.value) return
  e.preventDefault()
}

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  document.addEventListener('keydown', onKeydownDirect, { capture: true })
  document.addEventListener('paste', onClipboardPaste, { capture: true })
  terminalBodyRef.value?.addEventListener('touchmove', onTerminalBodyTouchMove, { passive: false })
  window.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('resize', lockKeyboardViewportScroll)
  // Connect immediately if active
  if (props.active) connect()
})

onUnmounted(() => {
  if (viewportScrollLockRaf) window.cancelAnimationFrame(viewportScrollLockRaf)
  terminalBodyRef.value?.removeEventListener('touchmove', onTerminalBodyTouchMove)
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
  // Keep the reactive `viewportY` AUTHORITATIVE for the overlay's anchor RENDER math.
  // onScroll alone is insufficient: in tmux copy-mode (alt-screen) PgUp/PgDn redraw the grid
  // IN PLACE without an xterm scroll event, and a main↔alt buffer switch fires no scroll either
  // — so the ref keeps a STALE value while the selection path reads the live one. That split is
  // exactly the "选区定位不准 after PgUp; fixed after switching panes" bug (pane switch forces a
  // redraw that happens to resync). onRender fires on every repaint; the guarded write no-ops
  // when unchanged (alt-screen viewportY is structurally 0), so this stays cheap.
  terminal.onRender(() => {
    const vY = terminal.buffer.active.viewportY
    if (viewportY.value !== vY) viewportY.value = vY
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

function onTerminalData(data: Uint8Array) {
  reportCliInputDiagnostic('terminal-data', { surface: 'workbench', data: summarizeBytes(data) })
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

function onTogglePanel(panel: 'numpad' | 'compose') {
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
  if (stickyCtrl.value && (modified === 'v' || modified === 'V')) {
    // Ctrl-sticky + v/V → real OS-clipboard paste, not the \x16 quoted-insert byte.
    stickyCtrl.value = false
    void clipboardText.pasteFromClipboard('sticky-ctrl-v')
    return
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
      // Secure context (HTTPS / localhost) → read the clipboard and inject straight into the
      // terminal. Insecure context (plain HTTP on a LAN / Tailscale hostname like stwork:8087)
      // BLOCKS navigator.clipboard.readText entirely, so the button could only fail silently.
      // Fall back to the compose bar — the OS paste gesture works in a real <textarea>, then Send
      // pushes it to the terminal. Generic: same path for tmux and a plain shell, iOS and PC.
      if (window.isSecureContext) {
        void clipboardText.pasteFromClipboard('paste-button')
      } else {
        composeDraft.value = ''
        activeMode.value = 'compose'
      }
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
  composeDraft.value = undefined
  hud.record('keyboard', `compose: ${text.length} chars`)
}

// Drawer "插入对话" — re-use an already-uploaded image/file. The drawer hands us the
// item's on-disk path; we route it through the SAME inject chokepoint the
// clipboard-paste flow uses post-upload (shell-quoted path → PTY), so claude/codex
// can @-reference it exactly as a fresh paste.
function onDrawerInject(path: string) {
  if (!path) return
  pasteResolver.injectKnownPaths([path])
  hud.record('state', `inject: ${path}`)
}

// Drawer 重发 — open the ComposeBar with the past prompt inserted for editing (NOT a
// direct send). Reset the draft first so an identical re-send still re-triggers the
// ComposeBar watcher on the next tick.
function onDrawerComposeDraft(text: string) {
  if (text == null) return
  composeDraft.value = undefined
  nextTick(() => {
    composeDraft.value = text
    activeMode.value = 'compose'
  })
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

// Live, authoritative viewport top. xterm's buffer is the source of truth; the reactive
// `viewportY` ref (updated via term.onScroll) can lag a frame behind during momentum scroll,
// which is why a default selection sometimes landed on the wrong line and "needed several tries".
function liveViewportY(): number {
  return xtermRef.value?.terminal?.()?.buffer.active.viewportY ?? viewportY.value
}

function onTouchballTap(x: number, y: number) {
  const cell = coordMapper.screenToCell(x, y)
  const cellBuf: CellCoord = { ...cell, bufferRow: liveViewportY() + cell.row }
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
  // Snapshot the live viewport top ONCE so getLine / select / anchors all agree on the same
  // buffer row even if a scroll fires mid-handler.
  const vY = term.buffer.active.viewportY
  const cell = coordMapper.screenToCell(x, y)
  const line = term.buffer.active.getLine(cell.row + vY)
  if (!line) return
  const lineStr = line.translateToString(true)
  let start = cell.col, end = cell.col
  while (start > 0 && /\S/.test(lineStr[start - 1] || '')) start--
  while (end < lineStr.length - 1 && /\S/.test(lineStr[end + 1] || '')) end++
  term.select(start, cell.row + vY, end - start + 1)
  const startBuf = vY + cell.row
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
  const top = term.buffer.active.viewportY
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
  const cellBuf: CellCoord = { ...cell, bufferRow: liveViewportY() + cell.row }
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
  // Re-derive bufferRow ATOMICALLY from xterm's authoritative scroll position at THIS instant,
  // pairing it with the viewport-relative `cell.row` the overlay mapped from the same touch.
  // The overlay's own `bufferRow` is computed from the reactive `viewportY` ref, which can lag
  // xterm during Safari momentum scroll — using the ref there would split row vs. bufferRow and
  // make the selection jump. We recompute here so the two are always consistent.
  const resolved: CellCoord = term
    ? { ...cell, bufferRow: term.buffer.active.viewportY + cell.row }
    : cell
  // Edge auto-scroll while selecting (D3): dragging an anchor onto the top/bottom row scrolls
  // the view so the selection can extend to content that is currently off-screen.
  if (term) {
    if (cell.row <= 0) edgeScroll(term, -1)
    else if (cell.row >= term.rows - 1) edgeScroll(term, 1)
  }
  if (anchorId === 1) anchorSM.placeAnchor1(resolved)
  else anchorSM.placeAnchor2(resolved)
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

// onSendKey + openInstallGuide are exposed so the host (CliPortal) can drive the
// relocated tmux pane bar — which now lives in the header/status row, outside this
// surface's body. openInstallGuide backs the pane bar's contextual notify bell.
function openInstallGuide() { installGuideOpen.value = true }
defineExpose({ wsStatus, agentState, notifications, netStats, onSendKey, openInstallGuide })
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

/* Per-surface status row (SSOT for both hosts). A single horizontal bar above .terminal-body.
   ssr-main grows + scrolls (tmux windows / agent badge); ssr-health is PINNED to the trailing
   edge and never scrolls, so the heartbeat stays fully visible no matter how many tmux windows
   exist. flex-shrink:0 so the row never eats terminal height. */
.surface-status-row {
  display: flex;
  align-items: stretch;
  flex-shrink: 0;
  background: hsl(var(--muted, 240 4% 16%));
  border-bottom: 1px solid hsl(var(--border, 240 4% 24%));
}
/* tmux mode: match the pane bar's own palette so the pinned health zone is seamless with it. */
.surface-status-row.is-tmux {
  background: #16121f;
  border-bottom-color: #2a1f3a;
}
.ssr-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: none;
}
.ssr-main::-webkit-scrollbar { display: none; }
.ssr-main :deep(.tmux-pane-bar) { flex: 1; min-width: 0; }
.ssr-agent { padding: 0 8px; }
/* Pinned heartbeat — never scrolls, never pushed off by a long window list. */
.ssr-health {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  padding: 0 8px;
}

/* WS7 primary entry — floats top-right above xterm; small and unobtrusive so it
   never covers terminal content the user is reading. */
.surface-notify-icon {
  position: absolute;
  top: 4px;
  right: 8px;
  z-index: 40;
}

/* Copy-mode active: stop the browser from initiating a scroll/pan from a finger-drag on the
   terminal, so the selection gesture cannot be hijacked by Safari's momentum scroll. */
.terminal-body.is-selecting,
.terminal-body.is-selecting :deep(.xterm-viewport) {
  touch-action: none;
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
  z-index: 102;
}

/* Bottom safe-area padding — standalone PWA ONLY.
   --dw-app-viewport-height is window.visualViewport.height (main.ts), the runtime
   VISIBLE area. In a browser tab (mobile Safari) the visual viewport already ends
   ABOVE the browser's bottom chrome / home-indicator zone, so adding
   env(safe-area-inset-bottom) on top of it DOUBLE-reserves that strip → the toolbar
   content floats ~34px up, leaving wasted empty space below it.
   In standalone the app paints edge-to-edge UNDER the home indicator, so the visual
   viewport spans the full screen and the inset padding is genuinely needed to lift
   the toolbar above the home indicator. Gating on (display-mode: standalone) applies
   the inset exactly where it is real and zeroes it in a tab → flush in both. */
.is-mobile .bottom-bar {
  padding-bottom: 0;
}
@media (display-mode: standalone) {
  .is-mobile .bottom-bar {
    padding-bottom: env(safe-area-inset-bottom, 0px);
  }
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
