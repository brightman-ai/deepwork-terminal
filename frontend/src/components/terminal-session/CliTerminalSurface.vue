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

    <!-- Remote unreachable from this page (e.g. an https page can't reach an http-only peer, or
         the peer config was removed). We do NOT connect (never silently fall back to localhost). -->
    <div v-if="remoteUnreachable" class="remote-unreachable-banner" data-testid="remote-unreachable-banner">
      <span>{{ connError || '该远程在当前页面不可达' }}</span>
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
    <!-- Machine identity is NOT duplicated here (方向 Y): remote tabs are marked on the tab strip
         (TopTabBar ServerIcon + peer name) and the ConnectionStatus widget already carries the
         target-label, so a standalone 本机/远端 chip in this row was pure redundancy. -->
    <div class="surface-status-row" :class="{ 'is-tmux': tmuxAttached }" data-testid="surface-status-row">
      <div class="ssr-main">
        <TmuxPaneBar
          v-if="tmuxReady && tmuxAttached"
          :session-id="sessionId"
          :overview-open="overviewOpen"
          :rollup="ovRollup"
          :status-by-index="ovStatusByIndex"
          @send-key="onSendKey"
          @open-notify="openInstallGuide"
          @toggle-overview="toggleOverview"
          @select-window="onOverviewSelect"
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
        :download-bps="netStats.downloadBps ?? 0"
        :upload-bps="netStats.uploadBps ?? 0"
        :tx-total="netStats.txTotal ?? 0"
        :rx-total="netStats.rxTotal ?? 0"
        :uptime-sec="netStats.uptimeSec ?? 0"
        :target-label="machineLabel || '本机'"
        :diagnostic="connDiagnostic"
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
      <!-- ONE notification entry: the platform-aware install/notify icon opens the
           quick notify-provider sheet (toggles/test/status). The PWA-install + browser
           push-subscribe guide is reached from INSIDE that sheet (its "安装应用 / 开启
           浏览器通知" action), so there is no second redundant bell. -->
      <div class="surface-notify-entries">
        <button
          v-if="tuiState === 'collapsed'"
          class="surface-tui-entry"
          type="button"
          title="复制/滚动失效 — 点此切到经典模式"
          aria-label="复制/滚动失效 — 切到经典模式"
          data-testid="tui-mode-entry"
          @click="tuiReopen()"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="14" rx="2" /><path d="M8 21h8" /><path d="M12 18v3" />
          </svg>
          <span class="surface-tui-dot" />
        </button>
        <InstallNotifyIcon @open="notifyQuickOpen = true" />
      </div>
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
      <!-- Agent Overview: an overlay over the (still-mounted) terminal so xterm keeps its state
           behind it. Picking a card switches to that window + closes back to the live terminal. -->
      <!-- Overlay sits INSIDE .terminal-body, whose touch handlers drive copy-mode. Stop touches
           here so a tap on a card switches windows WITHOUT leaking through to place a copy-mode
           selection (mobile-safari: the tap otherwise left the terminal stuck in a selection). -->
      <div
        v-if="overviewOpen"
        class="terminal-overview-overlay"
        @touchstart.stop
        @touchend.stop
        @pointerdown.stop
      >
        <AgentOverview
          :groups="ovGroups"
          :rollup="ovRollup"
          :is-mobile="isMobile"
          @select="onOverviewSelect"
        />
      </div>
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

    <!-- Dedicated paste-capture sheet (HTTP-only fallback). Its OWN focusable textarea — NOT
         the compose box — so the compose draft is never touched. inputmode="none" keeps the
         soft keyboard down while still allowing an OS long-press → 粘贴, which auto-sends to the
         terminal (onClipboardPaste) and dismisses. -->
    <Teleport to="body">
      <div v-if="pasteArmed" class="pc-scrim" data-testid="paste-capture" @click.self="disarmPaste">
        <div class="pc-card">
          <div class="pc-title">长按下方区域 → 粘贴 → 自动发送到终端</div>
          <textarea
            ref="pasteCaptureEl"
            class="pc-input"
            inputmode="none"
            placeholder="长按这里粘贴…"
            aria-label="粘贴捕获"
            @keydown.esc="disarmPaste"
          ></textarea>
          <button class="pc-cancel" type="button" @click="disarmPaste">取消</button>
        </div>
      </div>
    </Teleport>

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

    <!-- Quick notify-provider config sheet — same /api/notify/config SSOT as the
         settings Notifications section, so toggling/testing stays in lock-step. -->
    <NotifyQuickSheet
      :open="notifyQuickOpen"
      @close="notifyQuickOpen = false"
      @open-install="notifyQuickOpen = false; installGuideOpen = true"
    />

    <!-- Claude fullscreen → copy/scroll broken advisory; switch flips the live session to classic. -->
    <TuiModeSheet
      :open="tuiState === 'prompt'"
      :can-switch="agentState?.status !== 'running'"
      :busy="tuiSwitching"
      @close="tuiDefer()"
      @switch="onTuiSwitch"
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
import { copyTextToClipboard } from '@ce/utils/clipboard'
import AuthDialog from '@terminal/components/terminal-session/AuthDialog.vue'
import MobileOverlay from '@terminal/components/terminal-session/MobileOverlay.vue'
import Toolbar from '@terminal/components/terminal-session/Toolbar.vue'
import KeyboardPanel from '@terminal/components/terminal-session/KeyboardPanel.vue'
import TmuxQuickBar from '@terminal/components/terminal-session/TmuxQuickBar.vue'
import TmuxStatusSheet from '@terminal/components/terminal-session/TmuxStatusSheet.vue'
import TmuxPaneBar from '@terminal/components/terminal-session/TmuxPaneBar.vue'
import AgentOverview from '@terminal/components/terminal-session/AgentOverview.vue'
import ConnectionStatus from '@terminal/components/terminal-session/ConnectionStatus.vue'
import AgentStatusBadge from '@terminal/components/terminal-session/AgentStatusBadge.vue'
import ResourceDrawer from '@terminal/components/terminal-session/ResourceDrawer.vue'
import InstallGuideSheet from '@terminal/components/terminal-session/InstallGuideSheet.vue'
import InstallNotifyIcon from '@terminal/components/terminal-session/InstallNotifyIcon.vue'
import NotifyQuickSheet from '@terminal/components/terminal-session/NotifyQuickSheet.vue'
import TuiModeSheet from '@terminal/components/terminal-session/TuiModeSheet.vue'
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
import { useTuiAdvisory } from '@terminal/composables/cli/useTuiAdvisory'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { useAgentOverview } from '@terminal/composables/cli/useAgentOverview'
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
  /** Remote-tab connection (mesh). Empty/undefined → local same-origin terminal (unchanged).
   *  Resolved once per tab by useRemotePeers.resolveTabConnection (the single source). */
  wsBase?: string
  authToken?: string
  machineLabel?: string
  isRemote?: boolean
  /** Set when a remote tab can't be reached from the current page (bad scheme / deleted peer).
   *  The surface then shows an error and does NOT connect (so it can't silently fall back to a
   *  same-origin/local WS). */
  connError?: string
  /** Classifies a LIVE connection failure (auth vs unreachable vs HTTPS-block) by probing the
   *  peer's REST — surfaced through the connection chip so a stuck "Connecting…" isn't a dead end. */
  diagnose?: () => Promise<{ ok: boolean; error?: string }>
}>()

const emit = defineEmits<{
  (e: 'agent-state', state: AgentState | null): void
  (e: 'agent-notifications', state: AgentState[]): void
  (e: 'session-exit', exitCode: number): void
  (e: 'connection-change', status: WSConnectionStatus): void
}>()

// ─── Composables ─────────────────────────────────────────────────────────────

const { isMobile } = useDeviceDetection()
const { showAuthDialog, dismissAuthDialog, cliFetch } = useCliAuth()

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
// ── Agent Overview: the dashboard view of THIS tmux session. ONE useAgentOverview instance is
// the SSOT for both the pane bar's roll-up/badge and the overview grid (they share seen-state).
const overviewOpen = ref(false)
const {
  groups: ovGroups,
  rollup: ovRollup,
  statusByIndex: ovStatusByIndex,
  markViewed: ovMarkViewed,
} = useAgentOverview(tmux.windows, overviewOpen)
function toggleOverview(): void {
  overviewOpen.value = !overviewOpen.value
}
// Pick a window from the overview: switch to it (PRIMARY — select-window, any index) + mark seen
// + close back to the live terminal.
function onOverviewSelect(index: number): void {
  onSendKey(tmux.selectWindowSeq(index))
  const w = tmux.windows.value.find((win) => win.index === index)
  if (w) ovMarkViewed(w)
  overviewOpen.value = false
}
// Single sync point: any open/close (toggle OR card-tap) tells the server to gate tail capture.
watch(overviewOpen, (open) => { void tmux.setOverviewActive(open) })
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
const notifyQuickOpen = ref(false) // quick notify-provider config sheet

// "Claude is in fullscreen → copy/scroll broken" advisory. Fed by the terminal buffer-type change
// (alternate = fullscreen). Switching sends `/tui default` to the live session (normal buffer →
// copy/scroll restored); optional persist writes tui=classic to ~/.claude/settings.json.
const { state: tuiState, noteFullscreen: tuiNoteFullscreen, reopen: tuiReopen, defer: tuiDefer, resolved: tuiResolved } = useTuiAdvisory()
// Attaching to tmux retroactively makes a showing advisory a false positive (tmux copy-mode works)
// → clear it. Detaching re-evaluates on the next buffer-type change.
watch(tmuxAttached, (att) => { if (att) tuiNoteFullscreen(false) })
const tuiSwitching = ref(false)
async function onTuiSwitch({ persist }: { persist: boolean }): Promise<void> {
  if (agentState.value?.status === 'running') return // idle gate (UI already disables; defensive)
  tuiSwitching.value = true
  try {
    // `/tui default` relaunches claude in the classic (normal-buffer) renderer with the conversation
    // intact — we just type it at the idle prompt over the same input channel as the keyboard.
    sendBinary(encoder.encode('/tui default\r'), 'tui-switch')
    if (persist) {
      try { await cliFetch('/api/claude/tui-classic', { method: 'POST' }) } catch { /* best-effort */ }
    }
    tuiResolved()
  } finally {
    tuiSwitching.value = false
  }
}

// WS5: resource drawer open state, persisted across reloads.
const RESOURCE_DRAWER_KEY = 'dw.resourceDrawer.open'
const resourceDrawerOpen = ref(localStorage.getItem(RESOURCE_DRAWER_KEY) === '1')
watch(resourceDrawerOpen, (v) => localStorage.setItem(RESOURCE_DRAWER_KEY, v ? '1' : '0'))
const activeMode = ref<'idle' | 'keyboard' | 'numpad' | 'compose'>('idle')
// Draft pushed into the ComposeBar by the drawer's 重发 action. A fresh object-less
// value would not re-trigger ComposeBar's watcher for an identical re-send, so we
// bump a nonce-suffixed ref only via the handler below.
const composeDraft = ref<string | undefined>(undefined)
// HTTP paste flow: when the toolbar 粘贴 button can't read the clipboard programmatically
// (plain HTTP), it shows a DEDICATED paste-capture sheet ARMED — the next native paste caught
// there is sent straight to the terminal. It is its OWN textarea (never the compose box), so
// the compose draft is never disturbed. Disarmed once used or when the sheet is dismissed.
const pasteArmed = ref(false)
const pasteCaptureEl = ref<HTMLTextAreaElement | null>(null)
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
} = useWebSocketClient(() => props.sessionId, { wsBase: () => props.wsBase, authToken: () => props.authToken })

// A remote tab that can't be reached FROM THIS PAGE (https→no cloudflare addr, peer deleted,
// missing code → empty wsBase) must NOT connect: without this guard the empty wsBase would fall
// back to the same-origin (local) WS and silently attach this tab to localhost. We show an error
// banner instead and skip every connect path.
const remoteUnreachable = computed(() => !!props.isRemote && (!!props.connError || !props.wsBase))
function connectGuarded() { if (!remoteUnreachable.value) connect() }

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
    // Ghosting guard: a resize/reflow (mobile keyboard show/hide, rotation, reattach) can leave
    // stale cells when a fullscreen TUI repaints differentially. Force a full repaint after the fit.
    term.refresh(0, term.rows - 1)
  }
}

// Connection diagnostic: a REMOTE tab that never opens its WS just shows "Connecting…" forever
// with no clue why. On the first failure we classify it once (probe the peer's REST via props.diagnose,
// which reuses probePeer's SSOT: 401=auth code, timeout/refused=IP/port unreachable, HTTPS→HTTP block)
// and surface the reason through the connection chip's ⓘ. Cleared on a successful connect.
const runtimeDiag = ref('')
let everConnected = false
let diagInFlight = false
async function classifyFailure(): Promise<void> {
  if (diagInFlight || !props.isRemote || !props.diagnose || everConnected) return
  diagInFlight = true
  try {
    const r = await props.diagnose()
    // ok = auth + network fine, so the failure is the WS channel itself (proxy / path / upgrade).
    runtimeDiag.value = r.ok ? 'REST 可达且认证通过 —— WS 通道/代理/路径异常，非认证或地址问题' : (r.error || '无法连接')
  } catch { /* keep whatever we had */ } finally {
    diagInFlight = false
  }
}
// connError (config-level: bad scheme / deleted peer) takes priority over the runtime probe.
const connDiagnostic = computed(() => props.connError || runtimeDiag.value)

// Emit connection status changes
watch(wsStatus, (val) => {
  emit('connection-change', val)
  hud.updateSnapshot({ ws: val })
  if (val === 'connected') {
    everConnected = true
    runtimeDiag.value = '' // healthy again → drop any stale reason
    // DOM layout 可能还没稳定 (特别是 Wails 首次渲染)，阶梯式 fit:
    // 100ms (快速响应) → 500ms (layout 稳定) → 1500ms (最终校准)
    setTimeout(robustFitAndResize, 100)
    setTimeout(robustFitAndResize, 500)
    setTimeout(robustFitAndResize, 1500)
  } else if ((val === 'disconnected' || val === 'reconnecting') && props.isRemote && !everConnected) {
    void classifyFailure()
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
    connectGuarded()
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
  // ESC closes the Agent Overview (back to the pane you were on) instead of leaking to the PTY —
  // ONLY while it's open, so a normal ESC (vim / TUIs) is untouched when the overview is closed.
  if (overviewOpen.value && e.key === 'Escape') {
    overviewOpen.value = false
    e.preventDefault()
    e.stopPropagation()
    return
  }
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
  // Armed HTTP paste: the dedicated paste-capture sheet is open only to catch ONE paste. A
  // paste EVENT exposes clipboardData even on insecure HTTP (it's a user gesture, unlike
  // navigator.clipboard.readText), so read the text, send it straight to the terminal (same
  // encoder as compose Send), and dismiss the sheet. preventDefault keeps it out of any field.
  // Non-text pastes (images/files) fall through to the normal resolver.
  if (pasteArmed.value) {
    const text = e.clipboardData?.getData('text/plain') ?? ''
    if (text) {
      e.preventDefault()
      e.stopImmediatePropagation()
      pasteArmed.value = false
      for (const chunk of composeSend.encode(text)) sendBinary(chunk)
      return
    }
  }
  await pasteResolver.handlePasteEvent(e)
}

// Show + focus the dedicated paste-capture sheet (own textarea, NOT the compose box). The
// inputmode="none" textarea is focusable for an OS long-press paste without popping the
// keyboard, so it can sit anywhere and never touches the compose draft.
function armPasteCapture(): void {
  pasteArmed.value = true
  void nextTick(() => pasteCaptureEl.value?.focus())
}
function disarmPaste(): void {
  pasteArmed.value = false
}

// Non-passive touchmove listener (a `@touchmove.passive` template binding cannot preventDefault):
//   1. While SELECTING — swallow the finger-drag so it adjusts anchors instead of scrolling the
//      viewport / page out from under the selection ("selection jumps on Safari scroll" bug).
//   2. Idle, NORMAL buffer — let it through: xterm's own viewport momentum-scroll handles it.
//   3. Idle, ALTERNATE screen (fullscreen TUI) — xterm has no scrollback, so a finger swipe would
//      do NOTHING (the reported "touch scroll is bad" in flicker mode). Convert the swipe into the
//      app's own scroll via scrollGesture (mouse-wheel / PgUp-PgDn), one cell-height per step.
function onTerminalBodyTouchMove(e: TouchEvent) {
  if (!isMobile.value) return
  if (isSelecting.value) { e.preventDefault(); return }
  const term = xtermRef.value?.terminal?.()
  if (!term || term.buffer.active.type !== 'alternate') return
  const touch = e.touches[0]
  if (!touch) return
  const cellH = (terminalBodyRef.value?.clientHeight ?? 0) / Math.max(1, term.rows) || 18
  const dy = touch.clientY - termLastScrollY
  if (Math.abs(dy) < cellH) return  // accumulate until at least one full cell of travel
  const lines = Math.trunc(dy / cellH)
  termLastScrollY = touch.clientY
  // finger DOWN (dy>0) reveals EARLIER content → scroll back (dir -1); finger UP → forward (+1).
  scrollGesture(term, lines > 0 ? -1 : 1, Math.min(Math.abs(lines), term.rows))
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
  if (props.active) connectGuarded()
})

onUnmounted(() => {
  if (viewportScrollLockRaf) window.cancelAnimationFrame(viewportScrollLockRaf)
  if (ghostRefreshTimer) clearTimeout(ghostRefreshTimer)
  terminalBodyRef.value?.removeEventListener('touchmove', onTerminalBodyTouchMove)
  document.removeEventListener('keydown', onKeydownDirect, { capture: true })
  document.removeEventListener('paste', onClipboardPaste, { capture: true })
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('scroll', lockKeyboardViewportScroll)
  window.visualViewport?.removeEventListener('scroll', lockKeyboardViewportScroll)
  window.visualViewport?.removeEventListener('resize', lockKeyboardViewportScroll)
})

// ─── Terminal callbacks ───────────────────────────────────────────────────────

// Last-seen xterm buffer type ('normal' | 'alternate'); a change drives the ghosting refresh.
let lastBufferType = ''

// Ghosting guard for the ALTERNATE screen (fullscreen TUI: claude-code "flicker mode", tmux).
// Symptom (reproduced): tmux's pane model (capture-pane) is CLEAN, but the web terminal shows
// stale glyphs from a previous frame (e.g. a "0" column left after two-digit content scrolls away)
// — residue that lives in xterm's BUFFER, diverged from tmux. Proven in-session: a client-side
// `term.refresh()` does NOT clear it (it re-renders the same diverged buffer); only a server-side
// `tmux refresh-client` (resend every cell) does. So we debounce a refresh-client: during a
// continuous stream this stays armed and does NOT fire (no extra full redraws mid-stream); it
// fires once the output settles — when the user reads and residue would be visible.
let ghostRefreshTimer: ReturnType<typeof setTimeout> | null = null
function scheduleGhostRefresh(): void {
  if (!tmux.attached.value) return
  const term = xtermRef.value?.terminal?.()
  if (!term || term.buffer.active.type !== 'alternate') return
  if (ghostRefreshTimer) clearTimeout(ghostRefreshTimer)
  ghostRefreshTimer = setTimeout(() => {
    ghostRefreshTimer = null
    const t = xtermRef.value?.terminal?.()
    if (t && t.buffer.active.type === 'alternate') void tmux.runRefreshClient()
  }, 160)
}

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
    // Ghosting guard: when Claude Code's fullscreen TUI switches buffers (normal↔alternate on
    // launch/exit/resize-reflow), stale cells from the previous buffer can linger in the canvas
    // renderer. Force a full repaint on the transition so no residue survives. Guarded on the
    // type CHANGE so it fires once per switch (refresh re-renders, but type is then unchanged).
    const bt = terminal.buffer.active.type
    if (bt !== lastBufferType) {
      lastBufferType = bt
      terminal.refresh(0, terminal.rows - 1)
      // alternate = Claude entered fullscreen; normal = it left → advisory clears. BUT only prompt
      // OUTSIDE tmux: inside tmux the user has copy-mode + our forwarded mouse-wheel scroll, so
      // alt-screen does NOT break their scroll/copy — firing there is a false positive (the whole
      // advisory is aimed at the raw web terminal). See tmuxAttached watcher for the live clear.
      tuiNoteFullscreen(bt === 'alternate' && !tmuxAttached.value)
    }
  })
  terminalRows.value = terminal.rows

  onMessage(
    (data: ArrayBuffer) => {
      const bytes = new Uint8Array(data)
      inputTelemetry.recordOutput(bytes, 'ws-binary')
      xtermRef.value?.write(bytes)
      scheduleGhostRefresh()
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
  connectGuarded()
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

// Sentinels emitted by TmuxQuickBar's ½↑/½↓ buttons (NOT byte sequences sent to the PTY) — they
// route a half-page scroll through onSendKey so it is buffer-aware with a STABLE distance.
const HALF_PAGE_UP = 'dw:scroll-half-up'
const HALF_PAGE_DOWN = 'dw:scroll-half-down'

function onSendKey(key: string) {
  // ½↑/½↓: alt screen (fullscreen TUI) → scroll the app a fixed half-screen via scrollGesture
  // (stable, predictable distance per press); normal buffer → tmux copy-mode half-page, which
  // reaches tmux's full scrollback history. Intercept BEFORE any byte is sent.
  if (key === HALF_PAGE_UP || key === HALF_PAGE_DOWN) {
    const t = xtermRef.value?.terminal?.()
    const dir: 1 | -1 = key === HALF_PAGE_UP ? -1 : 1
    if (t && t.buffer.active.type === 'alternate') {
      scrollGesture(t, dir, Math.max(1, Math.floor(t.rows / 2)))
    } else {
      void tmux.runCopyMotion(dir < 0 ? 'halfpage-up' : 'halfpage-down')
    }
    hud.record('keyboard', `½${dir < 0 ? '↑' : '↓'} half-page scroll`)
    return
  }
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
      // One goal on EVERY origin: read the clipboard and push it STRAIGHT to the terminal —
      // the same end effect as the compose Send button, but in one tap and WITHOUT touching the
      // textarea. On a secure context (HTTPS / the cloudflare tunnel / localhost) that's exactly
      // what pasteFromClipboard does. On plain HTTP (LAN host like stwork:8087) the browser
      // BLOCKS programmatic clipboard reads, so it fails (and surfaces a 'needs HTTPS' hint); we
      // then fall back to opening the compose bar for a manual long-press paste + Send — and we
      // PRESERVE any existing draft (the old code wrongly cleared it, and never injected).
      void clipboardText.pasteFromClipboard('paste-button').then((ok) => {
        // HTTP read failed: show the DEDICATED paste-capture sheet (never the compose box, so
        // the compose draft is left completely untouched) and arm it — the next native paste
        // there auto-sends to the terminal (see onClipboardPaste).
        if (!ok) armPasteCapture()
      })
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
// Running anchor for incremental swipe-to-scroll (alt-screen): advanced one cell-height at a time.
let termLastScrollY = 0

function onTerminalTouchStart(e: TouchEvent) {
  if (!isMobile.value) return
  const touch = e.touches[0]
  if (touch) { termTouchStartX = touch.clientX; termTouchStartY = touch.clientY; termLastScrollY = touch.clientY }
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

// Buffer- & mouse-mode-aware scroll: the ONE place that knows HOW to scroll the current surface,
// so every scroll affordance (finger-swipe, anchor-drag edge scroll) behaves the same across the
// two Claude Code TUI render modes.
//   · Normal buffer (old inline TUI / shell): xterm owns the scrollback → scroll its viewport.
//   · Alternate screen (new "fullscreen"/"flicker" TUI: tmux, claude-code): the APP owns the
//     screen and xterm holds NO scrollback. Forward the gesture to the app instead:
//       - app enabled mouse tracking (claude-code does, DECSET 1003) → SGR mouse-wheel (smooth,
//         line-wise; the app scrolls its own transcript);
//       - otherwise (plain pager: less/man) → PgUp/PgDn.
//   dir: -1 = back/up (toward history), +1 = forward/down.
function scrollGesture(term: Terminal, dir: 1 | -1, lines = 1): void {
  if (term.buffer.active.type !== 'alternate') {
    term.scrollLines(dir * lines)
    return
  }
  const mouseMode = (term as any).modes?.mouseTrackingMode as string | undefined
  if (mouseMode && mouseMode !== 'none') {
    const btn = dir < 0 ? 64 : 65 // SGR mouse wheel: 64 = up, 65 = down
    const col = Math.max(1, Math.min(term.cols, Math.ceil(term.cols / 2)))
    const row = Math.max(1, Math.min(term.rows, Math.ceil(term.rows / 2)))
    const seq = `\x1b[<${btn};${col};${row}M`
    for (let i = 0; i < lines; i++) sendBinary(encoder.encode(seq))
  } else {
    // Pager without mouse tracking (less/man): no per-line scroll key, so approximate by paging —
    // ~one PgUp/PgDn per screenful of requested lines (at least one nudge).
    const pages = Math.max(1, Math.round(lines / Math.max(1, term.rows - 1)))
    for (let i = 0; i < pages; i++) sendBinary(encoder.encode(dir < 0 ? '\x1b[5~' : '\x1b[6~'))
  }
}

let lastEdgeScrollAt = 0
function edgeScroll(term: Terminal, dir: 1 | -1) {
  const now = Date.now()
  if (now - lastEdgeScrollAt < 120) return  // throttle
  lastEdgeScrollAt = now
  // Caveat (alt-screen): the selection cannot extend across an app-managed scroll — off-screen
  // lines are not in xterm's buffer. The scroll itself still works via scrollGesture.
  scrollGesture(term, dir, 1)
  hud.record('touch', `edge scroll ${dir > 0 ? 'down' : 'up'}`)
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

// Clipboard write delegates to the shared SSOT helper (@ce/utils/clipboard): secure-context
// writeText with an iOS-aware execCommand fallback. (This component is where that logic
// originated; it now lives in @ce so every copy button shares one correct implementation.)
function clipboardWrite(text: string): Promise<boolean> {
  return copyTextToClipboard(text)
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

/* Agent Overview overlay — covers the terminal (kept mounted behind) while open. */
.terminal-overview-overlay {
  position: absolute;
  inset: 0;
  z-index: 15;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  background: #0e0b16;
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

/* WS7 primary entries — float top-right above xterm; small and unobtrusive so they
   never cover terminal content the user is reading. A quick notify bell sits beside
   the install/notify guide icon. */
.surface-notify-entries {
  position: absolute;
  top: 4px;
  right: 8px;
  z-index: 40;
  display: flex;
  align-items: center;
  gap: 2px;
}

/* Collapsed advisory entry — sits beside the install/notify icon, amber to match its nudge dot. */
.surface-tui-entry {
  position: relative;
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 6px;
  background: transparent;
  border: 1px solid transparent;
  color: #f08a3c;
  cursor: pointer;
  flex-shrink: 0;
  touch-action: manipulation;
  transition: color 0.1s, background 0.1s;
}
.surface-tui-entry:hover { background: rgba(255, 255, 255, 0.06); }
.surface-tui-entry:active { transform: scale(0.94); }
.surface-tui-dot {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #f08a3c;
  box-shadow: 0 0 0 2px #141416;
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

.remote-unreachable-banner {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 10px 16px;
  background: rgba(255, 152, 0, 0.14);
  border-bottom: 1px solid #ff9800;
  color: #ffb74d;
  font-size: 0.8rem;
  text-align: center;
  flex-shrink: 0;
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

/* Dedicated paste-capture sheet (HTTP fallback). Teleported to body; scoped styles still apply
   via the data-v scope id carried on the elements. A centered modal with its OWN textarea. */
.pc-scrim {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.55);
}
.pc-card {
  width: 100%;
  max-width: 420px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  background: #161320;
  border: 1px solid #2e2750;
  border-radius: 14px;
  box-shadow: 0 16px 50px rgba(0, 0, 0, 0.6);
}
.pc-title {
  color: #c8b8e8;
  font-size: 0.82rem;
  text-align: center;
}
.pc-input {
  min-height: 96px;
  resize: none;
  padding: 12px;
  border-radius: 10px;
  background: #0e0b16;
  color: #e6e1f0;
  border: 1px solid #60d890;
  box-shadow: 0 0 0 2px rgba(96, 216, 144, 0.16);
  font-family: inherit;
  font-size: 0.95rem;
  outline: none;
}
.pc-input::placeholder { color: #6a5a88; }
.pc-cancel {
  align-self: center;
  padding: 7px 22px;
  border-radius: 8px;
  background: #221a36;
  color: #b8a8d8;
  border: 1px solid #3a2e5e;
  cursor: pointer;
  font-size: 0.8rem;
}
.pc-cancel:active { background: #2c2246; }
</style>
