<template>
  <div
    class="cli-terminal-surface"
    data-testid="cli-terminal-surface"
    :class="{ 'is-mobile': isMobile, 'is-desktop': !isMobile }"
    :style="surfaceStyle"
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
         · LEFT (ssr-main): scrollable — tmux window tabs when attached, else THIS terminal's
           action bar (see the non-tmux branch below). Only this zone scrolls.
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
        <!-- 非 tmux：这半边过去挂的是一枚状态徽标（"● Codex 运行中"，没有 agent 时退化成"终端"
             两个字）。它复述的正是正上方标签栏已经有的那枚状态点 —— 常驻 chrome 里最贵的一格，
             拿来说一句已经说过的话；而且它读的还是另一条推送（useAgentIntel），于是同一时刻这里
             写着红色"等待输入"、顶上标签点却是绿色"运行中"（Human 实测截图）。
             现在这半边是【当前终端的动作条】：
               · 左区**锚死**（flex-shrink:0，永远是这一行最左那一格）：动作按固定顺序出场，条件
                 动作只在自己的位置上出现/消失，位置从不因内容而移动；这张表和它的出现条件都在
                 surfaceActionBar.ts 里（纯函数 + 测试），模板对它一无所知。
               · 中区一行"此刻最具体的真话"：agent 那一句 → 正在跑的命令 → cwd，逐级回落、永不空着。
                 前面那枚状态色点走 STATUS_COLOR/STATUS_MOTION 同一套 SSOT，状态读的是标签点读的
                 那同一帧 —— 状态与内容合成一行，所以不再单独渲染一枚复述标签栏的徽标。
             tmux 那半边一个字未动。 -->
        <template v-else-if="tmuxReady">
          <div class="ssr-actions" data-testid="surface-actions">
            <button
              v-for="a in surfaceActions"
              :key="a.id"
              class="ssr-action"
              :class="{ 'is-alert': a.id === 'interrupt' }"
              type="button"
              :title="a.hint ? `${a.label} · ${a.hint}` : a.label"
              :aria-label="a.label"
              :data-testid="`surface-action-${a.id}`"
              @click="a.run()"
            ><component :is="a.icon" :size="15" aria-hidden="true" /><span
              v-if="a.badge"
              class="ssr-action-badge"
              :data-testid="`surface-action-badge-${a.id}`"
            >{{ a.badge }}</span></button>
          </div>
          <!-- 窄屏降级：这一格只占剩余宽度（flex:1 + min-width:0），文字超出就省略号，永远不换行、
               不把动作或右侧心跳挤走；再窄先砍 cwd 档（价值最低），最后整格收起，按钮一个不砍。 -->
          <div
            v-if="surfaceSlotContent.text"
            class="ssr-note"
            :class="`ssr-note--${surfaceSlotContent.tier}`"
            :title="surfaceSlotContent.text"
            :data-tier="surfaceSlotContent.tier"
            data-testid="surface-note"
          ><span
            v-if="surfaceDotColor"
            class="ssr-dot"
            :class="`ssr-dot--${surfaceDotStatus}`"
            :style="surfaceDotStyle"
            :title="surfaceDotLabel"
            :data-status="surfaceDotStatus"
            data-testid="surface-status-dot"
          /><span class="ssr-note-text">{{ surfaceSlotContent.text }}</span></div>
        </template>
      </div>
      <ConnectionChip
        class="ssr-health"
        :state="wsStatus"
        :rtt="netStats.rtt ?? 0"
        :download-bps="netStats.downloadBps ?? 0"
        :upload-bps="netStats.uploadBps ?? 0"
        :tx-total="netStats.txTotal ?? 0"
        :rx-total="netStats.rxTotal ?? 0"
        :uptime-sec="netStats.uptimeSec ?? 0"
        :target-label="machineLabel || '本机'"
        :diagnostic="connDiagnostic"
        :labels="CLI_CONN_LABELS"
        :refreshable="true"
        testid-prefix="cli-connection"
        data-testid="surface-connection-status"
        @refresh="wsReconnect"
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
      <!-- TUI-mode fallback entry — shown only when copy/scroll is degraded (fullscreen TUI).
           The notify entry point lives on the pane bar's bell → the shared NotifyQuickSheet
           (and the settings Notifications section); there is no install/PWA icon here. -->
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
      </div>
      <!-- Non-blocking paste-upload feedback: reads the SSOT upload-progress store owned
           by useClipboardPaste (via pasteResolver.uploads). Delayed-reveal (300ms) means a
           normal fast paste shows nothing at all — only a slow upload or an error surfaces
           this pill, which is exactly the case that used to look "silent" and get retried
           into 3-4 duplicate paths in the PTY. -->
      <UploadProgressFloat :entries="pasteResolver.uploads.value" @dismiss="pasteResolver.dismissUpload" />
      <!-- KeyCastr keystroke display (mobile only). Toggled from the main Toolbar's keycast button;
           defaults ON. No left-edge HUD tab — the toolbar is the SSOT toggle.
           Lives INSIDE .terminal-body (its `position: relative` is the anchor) because the pills are
           `position: absolute`: anchored to the viewport they stacked down over .cli-tab-bar and the
           pane bar, burying the very status dots R1/R3 are polishing. See KeyCastrOverlay.vue's
           style comment. Sibling of UploadProgressFloat, which is body-relative for the same
           reason. -->
      <KeyCastrOverlay
        v-if="isMobile && keystrokeHudVisible"
        :entries="keycastEntries"
        data-testid="keystroke-hud"
      />
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
      <!-- 终端内查找 (Ctrl/Cmd+F 等价物, findInTerminal shortcut). v-if'd — a fresh instance each
           open, no state to leak across closes. Bottom-docked so it never fights the top-right
           notify/upload floats for the same pixels (see TerminalSearchBar.vue's own style note). -->
      <!-- 不在这里再挂一个 data-testid：组件自己带 `terminal-search-bar`，父层传的同名属性会
           顺着 fallthrough 把它盖掉（实测 DOM 上只剩父层那个），于是组件自己的测试契约失效。 -->
      <TerminalSearchBar
        v-if="terminalSearchOpen"
        ref="terminalSearchBarRef"
        :result-index="terminalSearchResultIndex"
        :result-count="terminalSearchResultCount"
        @search="onTerminalSearch"
        @next="onTerminalSearchNext"
        @previous="onTerminalSearchPrevious"
        @close="closeTerminalSearch"
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
          @close="overviewOpen = false"
        />
      </div>
    </div>

    <!-- 底栏 (mobile only) -->
    <div v-if="isMobile" ref="bottomBarRef" class="bottom-bar">
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

    <!-- WS8: tmux status sheet (mobile bottom-sheet / desktop popover). statusByIndex = the
         SAME useAgentOverview instance TmuxPaneBar reads (one seen-aware status per window,
         defined once below) — so this sheet's dots can never disagree with the bar's. -->
    <TmuxStatusSheet
      :session-id="sessionId"
      :open="tmuxSheetOpen"
      :status-by-index="ovStatusByIndex"
      @close="tmuxSheetOpen = false"
      @send-key="onSendKey"
    />

    <!-- WS5 → drawer-per-pane: 收纳抽屉 — images / files / input history. ONE instance PER known
         tmux pane (paneKnown), lazily grown as each pane becomes current — kept MOUNTED (v-show
         inside ResourceDrawer itself) once created so a background pane's state (open-ness, tab,
         opened doc, scroll, selection) survives untouched. `is-active` ANDs the tab-active flag
         with "is this pane the current one" so S1 dedup holds (only the active tab's active
         pane's handle/panel ever renders) — see ResourceDrawer.vue's own isActive gating.
         @inject       → re-uses the clipboard-paste inject chokepoint (file path → PTY).
         @compose-draft → opens the ComposeBar with text inserted for editing (重发). -->
    <ResourceDrawer
      v-for="paneKey in paneKnown"
      :key="paneKey"
      :ref="(el) => setDrawerRef(paneKey, el)"
      :session-id="sessionId"
      :is-active="active && paneKey === currentPaneKey"
      :cwd="paneCwdFor(paneKey)"
      :tool="paneToolFor(paneKey)"
      :layout="drawerLayout"
      :layout-mode="drawerLayoutMode"
      :split-disabled="splitDisabled"
      :compose-reserve="composeReserve"
      :open="paneOpenMap[paneKey] ?? false"
      @update:open="(v: boolean) => { paneOpenMap[paneKey] = v }"
      @inject="onDrawerInject"
      @compose-draft="onDrawerComposeDraft"
      @update:layout-mode="drawerLayoutMode = $event"
    />

    <!-- Quick notify-provider config sheet — same /api/notify/config SSOT as the
         settings Notifications section, so toggling/testing stays in lock-step. -->
    <NotifyQuickSheet
      :open="notifyQuickOpen"
      @close="notifyQuickOpen = false"
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

    <!-- 隐藏文件输入 (📎 附件按钮).
         NO `accept` filter, deliberately. It used to carry an extension allowlist that had to
         mirror the server's MIME allowlist — two lists, two vocabularies (extension vs MIME),
         guaranteed to drift. Both are gone: the server stores whatever it is given (it does no
         parsing and never executes it), so greying a legitimate file out of the picker only
         ever cost the user a file it would have happily accepted. -->
    <input
      ref="attachInputRef"
      type="file"
      multiple
      style="display: none"
      @change="onAttachFileSelected"
    />

    <!-- Attention HUD (R1): teleports into #dw-topbar-right (see AttentionHud.vue). Both PC and
         mobile — never gated on isMobile, unlike KeyCastr, since a HUD that pulls your eye to a
         window needing you is exactly as valuable with a physical keyboard as with a thumb. -->
    <AttentionHud
      :card="attentionHud.card.value"
      :top-offset="headerBottom"
      @activate="onHudActivate"
      @dismiss="onHudDismiss"
    />
  </div>
</template>

<script lang="ts">
// Module scope (shared across every CliTerminalSurface instance — one per tab, all kept
// mounted via v-show): arbitrates which tab currently "owns" the global --dw-drawer-width
// CSS var (see the FIX-1 block below). Declared in a plain (non-setup) script block because
// `<script setup>` bindings are re-created PER INSTANCE — this needs real module-level state.
let drawerWidthCssVarOwner: string | null = null
</script>

<script setup lang="ts">
import { ref, reactive, computed, watch, nextTick, onMounted, onUnmounted, type Component } from 'vue'
import { ArrowDownToLine, Copy, Search, Square } from 'lucide-vue-next'
import { Terminal } from '@xterm/xterm'
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
// 连接健康 chip = @ce SSOT 组件 (与 workbench/pro 共享同一实现)。终端传英文文案 + cli
// testid 前缀保持既有 UX/测试契约; WS 是持续流量, 内联吞吐保持默认开 (inlineThroughput=true)。
import ConnectionChip from '@ce/components/connection/ConnectionChip.vue'
import ResourceDrawer from '@terminal/components/terminal-session/ResourceDrawer.vue'
import NotifyQuickSheet from '@terminal/components/terminal-session/NotifyQuickSheet.vue'
import TuiModeSheet from '@terminal/components/terminal-session/TuiModeSheet.vue'
import ComposeBar from '@terminal/components/terminal-session/ComposeBar.vue'
import KeyCastrOverlay from '@terminal/components/terminal-session/KeyCastrOverlay.vue'
import UploadProgressFloat from '@terminal/components/terminal-session/UploadProgressFloat.vue'
import AttentionHud from '@terminal/components/terminal-session/AttentionHud.vue'
import TerminalSearchBar from '@terminal/components/terminal-session/TerminalSearchBar.vue'
import type { TerminalFindOptions } from '@terminal/components/terminal-session/terminalSearchOptions'
import { handleFindShortcut } from '@terminal/components/terminal-session/terminalFindShortcut'
import {
  surfaceSlot,
  visibleSurfaceActions,
  type SurfaceActionId,
} from '@terminal/components/terminal-session/surfaceActionBar'
import { canMeasureTerminal } from '@terminal/components/terminal-session/terminalFit'
import { useWebSocketClient } from '@terminal/composables/cli/useWebSocketClient'
import {
  GHOST_ECHO_WINDOW,
  ghostRefreshDeferredForTyping,
  ghostRefreshSuppressed,
  ghostRefreshWait,
} from '@terminal/composables/cli/ghostRefresh'
import { useDrawerDock } from '@terminal/composables/cli/useDrawerDock'
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
import { useTmuxState, paneStateKey } from '@terminal/composables/cli/useTmuxState'
import {
  applySessionsOverviewFrame,
  sessionEntry,
  sessionNoteText,
  sessionRawStatus,
  sessionSignalText,
} from '@terminal/composables/cli/useSessionsOverview'
import { applyAgentSignalFrame } from '@terminal/composables/cli/useAgentSignals'
import { STATUS_COLOR, STATUS_MOTION, useAgentOverview, type EffectiveStatus } from '@terminal/composables/cli/useAgentOverview'
import { useAttentionHud } from '@terminal/composables/cli/useAttentionHud'
import { useForegroundAgentNotify } from '@terminal/composables/cli/useForegroundAgentNotify'
import { useKeyCastrHud } from '@terminal/composables/cli/useKeyCastrHud'
import { useShortcutsConfig, bindingFor, bindingLabel } from '@terminal/composables/cli/useShortcutsConfig'
import { matchesBinding } from '@terminal/composables/cli/useTabShortcuts'
import { focusWithoutViewportScroll, resetViewportScroll, useVisualKeyboardInset } from '@terminal/composables/cli/useVisualKeyboardInset'
import { reportCliInputDiagnostic, summarizeBytes, summarizeText, useCliTerminalInputTelemetry } from '@terminal/composables/cli/useCliInputDiagnostics'
import type { WSControlMessage, CellCoord, AnchorState, WSConnectionStatus } from '@terminal/types/terminal'
import type { AgentState, AgentTool, TmuxWindowState, TmuxPaneState } from '@terminal/types/terminal'

// ─── Props & Emits ────────────────────────────────────────────────────────────

const props = defineProps<{
  sessionId: string
  sessionName: string
  active: boolean
  /** Remote-tab connection (mesh). Empty/undefined → local same-origin terminal (unchanged).
   *  Resolved once per tab by useRemotePeers.resolveTabConnection (the single source). */
  httpBase?: string
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

// 终端英文文案 (仅非连接态内联显示; connected 无内联文本)。覆盖 @ce ConnectionChip 默认中文。
// WSConnectionStatus 与 @ce ConnectionState 枚举同构 (connecting/connected/reconnecting/disconnected/preempted)。
const CLI_CONN_LABELS: Partial<Record<WSConnectionStatus, string>> = {
  connecting: 'Connecting...',
  reconnecting: 'Reconnecting...',
  disconnected: 'Disconnected',
  preempted: 'Taken over',
}

// ─── Composables ─────────────────────────────────────────────────────────────

const { isMobile } = useDeviceDetection()
const { dock } = useDrawerDock() // which side the drawer docks — mirrors the squeeze gutter
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
  effectiveStatus: ovEffectiveStatus,
  dismiss: ovDismiss,
} = useAgentOverview(tmux.windows, overviewOpen)
function toggleOverview(): void {
  overviewOpen.value = !overviewOpen.value
}
// Pick a window from the overview: switch to it (PRIMARY — select-window, any index) + dismiss its
// needs-you dot (deliberate triage tap) + close back to the live terminal. Auto-clear on the next
// response still happens via the backend; this just hides the dot for the one you're jumping to.
function onOverviewSelect(index: number): void {
  void tmux.selectWindow(index)
  const w = tmux.windows.value.find((win) => win.index === index)
  if (w) ovDismiss(w)
  overviewOpen.value = false
}
// Single sync point: any open/close (toggle OR card-tap) tells the server to gate tail capture.
watch(overviewOpen, (open) => { void tmux.setOverviewActive(open) })
// WS7: open-but-unfocused-tab notification fallback (backend push covers no-tab).
useForegroundAgentNotify(() => props.sessionId)

// ── Attention HUD (R1) ─────────────────────────────────────────────────────────────────────
// The ACTIVE half of the pane bar's passive dot: a top-right card that interrupts once when a
// non-visible window IN THIS SESSION flips to waiting/done-unseen. Wired straight to the ONE
// shared useAgentOverview instance above (ovEffectiveStatus/ovDismiss) — the HUD can never
// disagree with the dots because it reads the very same status fn and writes the very same seen
// layer, never a second copy of either (see useAttentionHud.ts's header).
//
// Scoped to only the ACTIVE workbench tab via the injected `hidden` seam: every tab (v-show, all
// kept mounted) constructs its own hud instance, but `hidden` also folds in `!props.active` — so
// an inactive tab's gate never delivers a card, matching the explicit non-goal "no per-tab HUD"
// (request.md §3) without adding a second suppression mechanism. `document.hidden` (the actual
// page-hidden case) is untouched and still belongs to useForegroundAgentNotify (ATT-8: hidden →
// Web Notification only, never both).
const attentionHud = useAttentionHud({
  windows: tmux.windows,
  effectiveStatus: ovEffectiveStatus,
  markSeen: ovDismiss,
  overviewOpen,
  hidden: () => !props.active || (typeof document !== 'undefined' && document.hidden),
})
// Tap: the composable already marks the target seen + silences it + closes (see activate()'s
// return); this call site's ONLY job is the actual window switch, exactly the split
// onOverviewSelect already uses for the overview grid's own cards (AOV-9/AOV-14).
function onHudActivate(): void {
  const target = attentionHud.activate()
  if (target) void tmux.selectWindow(target.index)
}
function onHudDismiss(): void {
  attentionHud.dismiss()
}
// Leaving this tab collapses whatever card is already on screen. The `hidden` seam above only
// stops NEW cards; an ALREADY-RAISED one survives, and because every tab stays mounted (v-show)
// with its own `position: fixed` teleported card, that stale card keeps floating over WHATEVER
// tab you switched to — for up to the full 8s auto-dismiss, since refreshCard/mergeCard keep
// re-seating it. Worse, tapping it calls selectWindow on the OLD tab's session, so the visible
// terminal does nothing at all. Two tabs each holding a card stack two fixed cards on the same
// pixels. `collapse()` (not `dismiss()`) is the correct verb: leaving a tab is not reading its
// output, so no seen state is written and the pane-bar dots stay lit (ATT-4).
watch(() => props.active, (isActive) => { if (!isActive) attentionHud.collapse() })
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
const bottomBarRef = ref<HTMLDivElement>()

// ─── Terminal find (Ctrl/Cmd+F equivalent, findInTerminal shortcut) ───────────────────────────
// TerminalSearchBar (v-if in the template) owns the query text + toggle state; XtermTerminal owns
// the @xterm/addon-search wrapper. This surface is just the wire between them, plus the global
// shortcut that opens the bar in the first place — the exact same "own the mount point, relay to
// the addon" split the rest of this file uses for xterm.
const terminalSearchOpen = ref(false)
const terminalSearchBarRef = ref<InstanceType<typeof TerminalSearchBar>>()
// NB: xtermRef's instance type auto-unwraps refs exposed via defineExpose (Vue component-instance
// typing), so these are already plain numbers here — NOT `.value` on a Ref.
const terminalSearchResultIndex = computed(() => xtermRef.value?.searchResultIndex ?? -1)
const terminalSearchResultCount = computed(() => xtermRef.value?.searchResultCount ?? 0)
const { config: shortcutsConfig, load: loadShortcutsConfig } = useShortcutsConfig()

function openTerminalSearch(): void {
  terminalSearchOpen.value = true
}
/** 搜索条已经开着时的"再来一次"：把焦点送回输入框并全选已有查询词（直接改词重搜）。 */
function refocusTerminalSearch(): void {
  terminalSearchBarRef.value?.focus(true)
}
/** 键盘和动作条按钮走同一条：没开就开，已开就送回焦点 —— 两个入口一个行为。 */
function openOrRefocusTerminalSearch(): void {
  if (terminalSearchOpen.value) refocusTerminalSearch()
  else openTerminalSearch()
}
function closeTerminalSearch(): void {
  terminalSearchOpen.value = false
  xtermRef.value?.clearSearch()
  // Escape/关闭必须把焦点还给终端 — 否则会停在一个已经卸载的 <input> 上，敲键盘毫无反应。
  void nextTick(() => xtermRef.value?.terminal?.()?.focus())
}
function onTerminalSearch(term: string, options: TerminalFindOptions, incremental: boolean): void {
  xtermRef.value?.findNext(term, options, incremental)
}
function onTerminalSearchNext(term: string, options: TerminalFindOptions): void {
  xtermRef.value?.findNext(term, options)
}
function onTerminalSearchPrevious(term: string, options: TerminalFindOptions): void {
  xtermRef.value?.findPrevious(term, options)
}
// Capture-phase so it wins ahead of both the browser's native find-in-page AND xterm's own
// keydown handling — mirrors onKeydownDirect below. Gated on `props.active`: every tab's surface
// stays mounted (v-show) so ALL of them receive this event, but only the visible one may act on
// it.
//
// 判定顺序是这里的全部要害，所以它被抽成了纯函数（terminalFindShortcut.ts，带测试）：**先匹配按键、
// 再决定开还是聚焦**。反过来写（先看搜索条开没开就早退）会让第二次 Cmd+F 走不到 preventDefault，
// 按键漏给浏览器，于是屏幕上同时出现两个查找框（Human 实测截图）。被接管的快捷键必须每一次都吃掉。
function onFindShortcutKeydown(e: KeyboardEvent): void {
  handleFindShortcut(
    e,
    {
      active: props.active,
      matches: matchesBinding(e, bindingFor(shortcutsConfig.value, 'findInTerminal')),
      open: terminalSearchOpen.value,
    },
    { open: openTerminalSearch, refocus: refocusTerminalSearch },
  )
}

// ─── 当前终端的动作条 ─────────────────────────────────────────────────────────────────────────
//
// 状态行左半边（非 tmux 时）装的东西。判定全在 surfaceActionBar.ts（纯函数，带测试）：出场顺序、
// 每个动作的出现条件、中区那句话的逐级回落。这里只做它做不到的事 —— 把 xterm/WS 的现状读成一组
// 布尔值喂进去，再把返回的表配上图标渲染出来。
//
// 这一行值得放按钮的理由写在那个模块的头部（一句话：我们是浏览器里的终端，可见控件的权重天然高于
// 原生终端，而右侧心跳让这一行的空间成本早已付出）。
//
// ── 终端滚动位置：贴底了没有、下面积了多少新行 ──────────────────────────────────────────────
// 数据来自 xterm 自己的 buffer（viewportY / baseY），不另造滚动监听 —— 见 onTerminalReady 里
// 已有的 onScroll/onRender 两个回调，这里只是搭车。
const terminalAtBottom = ref(true)
const terminalNewLinesBelow = ref(0)
// 上一次"贴着底"时的 baseY。离底后新推进来的行数 = 现在的 baseY - 它。非响应式：它只是算差值的锚点。
let bottomBaseY = 0
function noteTerminalScrollPosition(term: Terminal): void {
  const b = term.buffer.active
  // alt-screen（全屏 TUI）没有滚动回滚的概念，viewportY 恒为 0：那里"回到最新"毫无意义，
  // 恒定按贴底处理，按钮就不会在一个滚不动的界面上出现。
  const atBottom = b.type === 'alternate' || b.viewportY >= b.baseY
  if (atBottom) {
    bottomBaseY = b.baseY
    if (!terminalAtBottom.value) terminalAtBottom.value = true
    if (terminalNewLinesBelow.value !== 0) terminalNewLinesBelow.value = 0
    return
  }
  if (terminalAtBottom.value) terminalAtBottom.value = false
  const behind = Math.max(0, b.baseY - bottomBaseY)
  if (terminalNewLinesBelow.value !== behind) terminalNewLinesBelow.value = behind
}
function scrollTerminalToBottom(): void {
  const term = xtermRef.value?.terminal?.()
  if (!term) return
  term.scrollToBottom()
  noteTerminalScrollPosition(term)
  hud.record('state', '回到最新')
}

// ── 选区：有没有可复制的东西 ────────────────────────────────────────────────────────────────
const terminalHasSelection = ref(false)
async function copyTerminalSelection(): Promise<void> {
  const term = xtermRef.value?.terminal?.()
  const sel = term?.getSelection() ?? ''
  if (!term || !sel) return
  const ok = await clipboardWrite(sel)
  hud.record('state', ok ? `copy ok: ${sel.length} chars` : `copy FAILED (${sel.length} chars)`)
  // 成功才清选区：按钮随之消失，就是"复制到了"的反馈；失败留着选区，让人能再按一次。
  if (ok) {
    term.clearSelection()
    terminalHasSelection.value = false
  }
}

// ── 终端标题（OSC 0/2）：多数 shell 会把正在跑的命令写进去，原生终端标题栏显示的就是它 ──────
const terminalTitle = ref('')

/** 这个会话此刻的状态帧 —— 和顶部标签那枚点读的是同一帧（sessions_overview），不是第二条推送。 */
const surfaceEntry = computed(() => sessionEntry(props.sessionId))
/** 只有"检测到 agent 且它在跑"才允许出现「中断」：裸 shell 的一个单击不该能中断任何东西。 */
const surfaceAgentRunning = computed(
  () => !!surfaceEntry.value?.agentTool && sessionRawStatus(surfaceEntry.value) === 'running',
)

const SURFACE_ACTION_ICON: Record<SurfaceActionId, Component> = {
  search: Search,
  'to-bottom': ArrowDownToLine,
  copy: Copy,
  interrupt: Square,
}

const surfaceActions = computed(() =>
  visibleSurfaceActions(
    {
      atBottom: terminalAtBottom.value,
      newLinesBelow: terminalNewLinesBelow.value,
      hasSelection: terminalHasSelection.value,
      agentRunning: surfaceAgentRunning.value,
    },
    {
      openSearch: openOrRefocusTerminalSearch,
      scrollToBottom: scrollTerminalToBottom,
      copySelection: () => { void copyTerminalSelection() },
      sendKey: (seq: string) => { sendBinary(encoder.encode(seq)) },
    },
    // 快捷键文案取自用户自己的配置（可改键），措辞用 bindingLabel —— 和设置页里显示的逐字同一套。
    bindingLabel(bindingFor(shortcutsConfig.value, 'findInTerminal')),
  ).map((a) => ({ ...a, icon: SURFACE_ACTION_ICON[a.id] })),
)

/**
 * 中区那句话 —— 「这个终端此刻在发生什么」，逐级回落，永不空着。
 *
 * 第一档（agent 那一句）来自 `sessionNoteText`，也就是**顶部标签那枚状态点读的同一帧**。改动前这里
 * 读的是 useAgentIntel(sessionId) 的另一条推送，于是同一个 session 在同一时刻，这里写着红色
 * 「Codex 等待输入」、顶上标签点却是绿色「运行中」（Human 实测截图）。两条判定路径就是两个真相；
 * 现在只剩一条，逐字相等由 useSessionsOverview.test.ts 钉死。
 */
const surfaceSlotContent = computed(() =>
  surfaceSlot({
    agent: sessionNoteText(props.sessionId),
    title: terminalTitle.value,
    cwd: surfaceEntry.value?.cwd ?? '',
  }),
)

/**
 * 状态色点。走 STATUS_COLOR / STATUS_MOTION 这一套 SSOT（和标签点、总览卡片同一套颜色与动效），
 * 状态则来自上面那同一帧 —— 于是"状态"和"内容"合成一行，不再单独渲染一枚复述标签栏的徽标。
 *
 * idle 没有颜色也没有点（STATUS_COLOR 里就没有这一档），和标签点一模一样：一个闲着的终端不需要
 * 一枚常亮的点来宣告它闲着。
 */
const surfaceDotStatus = computed<EffectiveStatus>(() => {
  const e = surfaceEntry.value
  if (!e) return 'idle'
  const raw = sessionRawStatus(e)
  if (raw !== 'idle') return raw
  return e.awaitingUser ? 'done-unseen' : 'idle'
})
const surfaceDotColor = computed(() => {
  const s = surfaceDotStatus.value
  return s === 'idle' ? '' : STATUS_COLOR[s]
})
/** 颜色 + 脉动参数，全部现取自 STATUS_COLOR / STATUS_MOTION；动画声明本身在 scoped CSS 里
 *  （Vue 给 @keyframes 改了名，内联 animation 追不到）。 */
const surfaceDotStyle = computed<Record<string, string>>(() => {
  const s = surfaceDotStatus.value
  if (s === 'idle') return {}
  const style: Record<string, string> = { background: STATUS_COLOR[s] }
  const motion = STATUS_MOTION[s]
  if (motion) {
    style['--dot-duration'] = motion.duration
    style['--dot-easing'] = motion.easing
    style['--dot-min-opacity'] = String(motion.minOpacity)
  }
  return style
})
/** 点的 hover 文案 = 标签点那句状态话（"Codex 运行中"），同一个措辞 SSOT。 */
const surfaceDotLabel = computed(() => sessionSignalText(props.sessionId))

// ─── State ────────────────────────────────────────────────────────────────────

const tmuxDetected = ref(false)
const tmuxSheetOpen = ref(false)
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

// ─── WS5 → drawer-per-pane (20260710-124400): one ResourceDrawer instance PER TMUX PANE ───────
// (design doc: docs/.tg/work/20260710-124400-drawer-per-pane-term/request.md). Was: ONE drawer per
// TAB, content following `tmux.activeCwd` in place (switching the main pane just refreshed the
// same instance's cwd). Now: each tmux pane gets its OWN drawer instance + full interactive state
// (open/closed, top/sub tab, opened doc, scroll position, text selection) — switching panes SWAPS
// to that pane's instance wholesale rather than refreshing one shared instance. Human-decided
// (2026-07-10): restore exactly as left (open stays open + same content; closed stays closed); a
// never-visited pane defaults CLOSED; session-memory only — NO localStorage (unlike the old
// RESOURCE_DRAWER_KEY persistence this replaces, deliberately dropped: there is no single global
// boolean to persist once "open" is per-pane).
//
// Stable identity: windowId("@N") + tmux's own stable pane_id("%N") via paneStateKey() — NEVER
// pane.index, which tmux recycles once a pane closes (see useTmuxState.paneStateKey). A plain
// (non-tmux) shell has no pane concept at all, so it gets one fixed pseudo-key — this preserves
// the exact pre-refactor single-drawer behaviour for the common non-tmux case.
const noTmuxPaneKey = `session:${props.sessionId}`

// paneKnown: paneKeys with a MOUNTED drawer instance. Lazily grown — a pane's instance is created
// the first time it becomes the CURRENT pane (not for every pane tmux happens to report), so a
// session with many panes only ever pays for the ones actually visited. It has to be the CURRENT
// pane (not "first opened") because the closed-state HANDLE (the small edge tab that invites
// opening) lives inside ResourceDrawer itself — an instance must exist for the handle to render at
// all, and it must be created before the user can tap it open the first time. Starts EMPTY (not
// pre-seeded with noTmuxPaneKey) — the `watch(currentPaneKey, ..., {immediate:true})` below mounts
// whichever key resolves first, so a tmux-attached tab never carries a permanently-unused pseudo
// instance alongside its real per-pane ones.
const paneKnown = ref<string[]>([])
// open/closed per paneKey — in-memory only (see note above), defaults to CLOSED for any key the
// moment it first appears (never-visited pane → full xterm).
const paneOpenMap = reactive<Record<string, boolean>>({})

// currentPaneKey: the pane the user is actually focused on right now — tmux's active WINDOW's
// active PANE (a window with split panes reports exactly one as `active`; switching windows is
// just switching to a different pane by this same definition, so both window-tabs and in-window
// pane-splits drive the SAME drawer swap). Falls back to the fixed pseudo-key while detached/
// unknown, matching the pre-refactor single-drawer fallback.
const currentPaneKey = computed<string>(() => {
  if (!tmux.attached.value) return noTmuxPaneKey
  const ws = tmux.windows.value
  if (ws.length === 0) return noTmuxPaneKey
  const win = ws.find(w => w.active) ?? ws[0]
  const panes = win.panes ?? []
  if (panes.length === 0) return noTmuxPaneKey
  const pane = panes.find(p => p.active) ?? panes[0]
  return paneStateKey(win, pane)
})

// Ensure the current pane always has a mounted (possibly still-closed) instance, so its handle is
// always reachable. Registered BEFORE the selection watcher below so paneOpenMap is guaranteed
// populated by the time restoreSelectionFor reads it on the same currentPaneKey change.
watch(currentPaneKey, (key) => {
  if (!paneKnown.value.includes(key)) paneKnown.value.push(key)
  if (!(key in paneOpenMap)) paneOpenMap[key] = false
}, { immediate: true })

// Convenience two-way binding onto "is the CURRENT pane's drawer open" — the only pane whose
// open-ness ever affects this surface's own layout (squeeze/composeReserve/etc).
const currentPaneOpen = computed<boolean>({
  get: () => paneOpenMap[currentPaneKey.value] ?? false,
  set: (v) => { paneOpenMap[currentPaneKey.value] = v },
})

// Live lookup: find a known paneKey's underlying window+pane in the CURRENT tmux snapshot (it may
// no longer be the active one — a background pane's drawer still tracks ITS OWN pane's live cwd,
// not whatever the terminal happens to be showing).
function findPane(key: string): { win: TmuxWindowState; pane: TmuxPaneState } | undefined {
  for (const w of tmux.windows.value) {
    for (const p of w.panes) {
      if (paneStateKey(w, p) === key) return { win: w, pane: p }
    }
  }
  return undefined
}
function paneCwdFor(key: string): string {
  if (key === noTmuxPaneKey) return tmux.activeCwd.value
  return findPane(key)?.pane.cwd ?? ''
}
function paneToolFor(key: string): AgentTool {
  if (key === noTmuxPaneKey) return tmux.activeTool.value
  return findPane(key)?.pane.agentTool ?? ''
}

// Cleanup (MUST 4): once a paneKey drops out of the live tmux snapshot (pane closed/killed), drop
// its mounted instance + open flag + saved selection range — no leaked state for a pane that no
// longer exists. The pseudo (non-tmux) key is a real pane only while genuinely detached — it's
// pruned same as any other stale key once tmux attaches (unless it's still the current key or the
// user left it open), so a tmux-attached tab never carries the one-tick pseudo instance mounted
// during the split-second before the first tmux snapshot arrives (attached defaults to false until
// then, so currentPaneKey's very first — immediate — resolution is always the pseudo key).
const validPaneKeys = computed<Set<string>>(() => {
  const set = new Set<string>()
  if (!tmux.attached.value) set.add(noTmuxPaneKey)
  for (const w of tmux.windows.value) {
    for (const p of w.panes) set.add(paneStateKey(w, p))
  }
  // Never prune the key currently in view or one the user left open — both must survive until the
  // user actually navigates away / closes it, even if attached flipped in the very same tick.
  set.add(currentPaneKey.value)
  if (paneOpenMap[noTmuxPaneKey]) set.add(noTmuxPaneKey)
  return set
})
watch(validPaneKeys, (valid) => {
  const stale = paneKnown.value.filter(k => !valid.has(k))
  if (stale.length === 0) return
  paneKnown.value = paneKnown.value.filter(k => valid.has(k))
  for (const k of stale) {
    delete paneOpenMap[k]
    delete drawerInstanceRefs[k]
    selectionRangeMap.delete(k)
  }
})

// Per-instance component refs (drag-resized panel width + panel DOM root for selection
// containment checks below) — a plain (non-reactive) map is fine: each property READ inside a
// computed/watch still tracks the underlying exposed refs reactively regardless of the map itself.
const drawerInstanceRefs: Record<string, { effectivePanelWidthPx?: number; panelRootEl?: HTMLElement } | null> = {}
function setDrawerRef(key: string, el: unknown): void {
  if (el) drawerInstanceRefs[key] = el as { effectivePanelWidthPx?: number; panelRootEl?: HTMLElement }
  else delete drawerInstanceRefs[key]
}

// ── Selection keep-alive across pane switches ───────────────────────────────────────────────
// There is only ONE global window.getSelection(); v-show keeps every visited pane's drawer DOM
// mounted (scroll position + opened doc survive for free), but the Selection itself does NOT
// survive its anchor subtree going display:none in every browser — so we explicitly clone the
// Range into a per-paneKey map on the way OUT and re-apply it on the way back IN. No serialization
// needed (session-memory only + DOM never unmounts, so the range's node references stay valid).
const selectionRangeMap = new Map<string, Range>()
function saveSelectionFor(key: string): void {
  const rootEl = drawerInstanceRefs[key]?.panelRootEl
  if (!rootEl) return
  const sel = window.getSelection()
  if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return
  const range = sel.getRangeAt(0)
  if (rootEl.contains(range.commonAncestorContainer)) {
    selectionRangeMap.set(key, range.cloneRange())
  }
}
function restoreSelectionFor(key: string): void {
  const range = selectionRangeMap.get(key)
  if (!range || !paneOpenMap[key]) return // nothing visible to select into
  const sel = window.getSelection()
  if (!sel) return
  try {
    sel.removeAllRanges()
    sel.addRange(range)
  } catch { /* the range's nodes may have gone away (content refetched) — best-effort restore */ }
}
watch(currentPaneKey, (newKey, oldKey) => {
  if (oldKey && oldKey !== newKey) saveSelectionFor(oldKey)
  if (newKey) nextTick(() => restoreSelectionFor(newKey))
})

// ─── Right-drawer dual-mode layout — THE SSOT (design doc: 20260710-094132-right-drawer-model-
// term/design.md §7) ────────────────────────────────────────────────────────────────────────
// drawerLayout is the ONE computed that decides how the drawer coexists with the terminal;
// every width/height the drawer touches (terminal-column squeeze, compose column width, the
// drawer's own scrim/panel reach) derives from it — nothing else hardcodes a full-width/
// full-height assumption. 'split' (≥900px AND open): the drawer docks as a column and the whole
// terminal column (xterm + status row + bottom-bar/compose) is squeezed by exactly the panel's
// own width — geometrically side-by-side, zero overlap. 'overlay' (narrow, or width<900 even on
// desktop, or simply closed): the drawer floats; ResourceDrawer keeps compose reachable there via
// composeReserve below (a geometry reservation, not a z-index fight).
//
// drawerLayoutMode (discoverability fix, design doc §7 signifier notes): the viewport auto-detect
// above is still the DEFAULT ('auto'), but the drawer header's 双栏⇄浮层 toggle can override it —
// 'split' or 'overlay' then wins outright over the width check, persisted so the override survives
// reloads. A forced 'split' still respects the viewport floor: below the breakpoint the terminal
// has no room for ~80 columns, so it silently degrades to 'overlay' rather than becoming unusable
// (the header toggle also disables 双栏 there via splitDisabled, so this is a defensive floor, not
// the primary guard). Whenever the drawer is CLOSED the layout always reads 'overlay' regardless of
// mode — nothing to squeeze for, so a stored 'split' preference can never leave the terminal
// squeezed with no visible panel.
const DRAWER_SPLIT_BREAKPOINT = 900
const LAYOUT_MODE_KEY = 'dw.drawerLayoutMode'
type DrawerLayoutMode = 'auto' | 'split' | 'overlay'
function loadLayoutMode(): DrawerLayoutMode {
  const v = localStorage.getItem(LAYOUT_MODE_KEY)
  return v === 'split' || v === 'overlay' ? v : 'auto'
}
const drawerLayoutMode = ref<DrawerLayoutMode>(loadLayoutMode())
watch(drawerLayoutMode, (v) => localStorage.setItem(LAYOUT_MODE_KEY, v))

const viewportWidth = ref(window.innerWidth)
function onSurfaceViewportResize(): void { viewportWidth.value = window.innerWidth }
// Handed to the drawer header so it can grey out 双栏 with a "需更宽窗口" title instead of
// letting the user pick a mode that would immediately degrade back to overlay.
const splitDisabled = computed(() => viewportWidth.value < DRAWER_SPLIT_BREAKPOINT)
const drawerLayout = computed<'split' | 'overlay'>(() => {
  if (!currentPaneOpen.value) return 'overlay'
  const canSplit = viewportWidth.value >= DRAWER_SPLIT_BREAKPOINT
  if (drawerLayoutMode.value === 'overlay') return 'overlay'
  if (drawerLayoutMode.value === 'split') return canSplit ? 'split' : 'overlay'
  return canSplit ? 'split' : 'overlay' // 'auto'
})

// How many px the OPEN drawer currently occupies (its own drag-resized/fullscreen width — owned
// and exposed by ResourceDrawer itself, the single owner of that state). Looked up on the CURRENT
// pane's instance specifically — that's the only one ever visible/squeezing. 0 whenever we're not
// actually squeezing (closed, or narrow/overlay) — no separate "is it squeezing" boolean needed.
const drawerPanelWidthPx = computed(() => drawerInstanceRefs[currentPaneKey.value]?.effectivePanelWidthPx ?? 0)
const drawerSqueezePx = computed(() => (drawerLayout.value === 'split' ? drawerPanelWidthPx.value : 0))

// ─── FIX 1: publish the drawer's live docked width as ONE global CSS var so viewport-fixed chrome
// living OUTSIDE this surface (currently just HelpCenter's "?" fab) can shift clear of the panel
// geometrically — no z-index war.
//
// [D-1 deviation from the literal design-doc wording] The design note reads "...width in px when
// open in SPLIT layout, else 0px" — but testing FIX 2's new 双栏/浮层 override surfaced a REAL
// click-target bug that formula misses: on a non-mobile viewport the panel is flush-docked to the
// right edge in BOTH 'split' AND 'overlay' (`.rd-scrim.is-desktop` and `.rd-scrim.is-split` share
// the same right-anchored chrome — see ResourceDrawer.vue) — only the xterm-squeeze differs. In
// desktop-overlay the fab's z-index (2500) sits ABOVE the drawer's (300), so with the var pinned
// to 0 there the fab doesn't just look overlapped, it SWALLOWS the click meant for the drawer's own
// 收起 button — and desktop's scrim is `pointer-events:none` (no click-outside-to-close escape
// hatch the way mobile's real scrim backdrop has), so that button is the ONLY way to close the
// drawer. That dead-end was reachable before this task too (a desktop *window* narrower than
// 900px already auto-resolved to overlay), but FIX 2's manual override now reaches it from a full
// WIDE viewport as well, which is why it surfaced during this task's own verification. Fix: the var
// tracks "is the panel flush-docked on a non-mobile viewport" (split OR desktop-overlay), not
// "is it split" — mobile is deliberately EXCLUDED (its real scrim already closes on an outside tap,
// and shifting a fixed circular fab across near-full phone width would be impractical anyway), so
// mobile behavior is untouched. See implementation-notes.md [D-1] for the full note.
const drawerDockedWidthPx = computed(() =>
  !isMobile.value && currentPaneOpen.value ? drawerPanelWidthPx.value : 0
)
// Only the currently-ACTIVE tab's width counts (inactive tabs are still mounted via v-show, but
// they must not fight over the var); drawerWidthCssVarOwner (module scope, see the plain <script>
// block above) arbitrates so a tab only clears the var back to 0px if it is still the one that
// last set it — correct regardless of which surface's watcher fires first during a tab switch.
const myDrawerWidthPx = computed(() => (props.active ? drawerDockedWidthPx.value : 0))
watch(myDrawerWidthPx, (px) => {
  if (px > 0) {
    document.documentElement.style.setProperty('--dw-drawer-width', `${px}px`)
    drawerWidthCssVarOwner = props.sessionId
  } else if (drawerWidthCssVarOwner === props.sessionId) {
    document.documentElement.style.setProperty('--dw-drawer-width', '0px')
    drawerWidthCssVarOwner = null
  }
}, { immediate: true })

// The squeeze applies to the WHOLE surface (not just .terminal-body): the bottom-bar/compose row
// is a sibling of .terminal-body under this same root, and squeezing the root in one place is what
// makes "compose spans only the terminal column" true for free — no separate width binding on the
// bottom-bar to keep in sync (one geometry point, not two that could drift).
// The gutter opens on whichever side the drawer docks: shrinking width alone frees the RIGHT
// (root is left-anchored), so a LEFT dock also pushes the root right by the same px (margin-left)
// to free the gutter on the left. One number (drawerSqueezePx), one direction (dock).
const surfaceStyle = computed(() => {
  if (drawerSqueezePx.value <= 0) return {}
  const width = `max(0px, calc(100% - ${drawerSqueezePx.value}px))`
  return dock.value === 'left' ? { width, marginLeft: `${drawerSqueezePx.value}px` } : { width }
})

// composeReserve: live px height of OUR OWN bottom toolbar (measured, never guessed), handed to
// ResourceDrawer so its overlay-mode scrim/panel can stop above it. Only meaningful when there IS
// a bottom toolbar to protect (isMobile) AND the drawer is actually overlaying (not split, where
// the squeeze already guarantees non-overlap on a different axis).
const bottomBarHeight = ref(0)
let bottomBarObserver: ResizeObserver | null = null
watch(bottomBarRef, (el) => {
  bottomBarObserver?.disconnect()
  bottomBarObserver = null
  if (el && 'ResizeObserver' in window) {
    bottomBarObserver = new ResizeObserver((entries) => {
      bottomBarHeight.value = entries[0]?.contentRect.height ?? 0
    })
    bottomBarObserver.observe(el)
  } else {
    bottomBarHeight.value = 0
  }
}, { immediate: true })
const composeReserve = computed(() =>
  isMobile.value && drawerLayout.value === 'overlay' ? bottomBarHeight.value : 0
)

// headerBottom: the measured (never guessed) bottom edge of this surface's OWN header stack —
// the topbar above it PLUS this surface's own pane-bar/status row — handed to AttentionHud so its
// card floats clear of the pane bar's hit targets (ATT-10) instead of guessing a row height the
// way the old KeyCastr offset once did (see KC-5). `.terminal-body` is the sibling immediately
// below both rows, so its own top edge in viewport coordinates IS that boundary. Re-measured
// whenever `.terminal-body` itself resizes — attaching/detaching tmux swaps the row above it
// between the pane bar and the agent badge, which are different heights — via the SAME
// ResizeObserver idiom `bottomBarHeight` just above already uses.
const headerBottom = ref(0)
let headerBottomObserver: ResizeObserver | null = null
function measureHeaderBottom(): void {
  // 同一条不变量（terminalFit.ts）：量不到就不记，上一次量准的值继续有效。这个 RO 和 xterm 那个
  // 一样会在标签隐藏时收到一次 0×0 回调，而 `display: none` 元素的 getBoundingClientRect() 全是
  // 0 —— 记下去就是把"顶部"写成 0。这里的后果比 PTY 那处轻（卡片本来就只在当前标签出现，且
  // 下面还有一次 flush:'post' 的补测），但**能量到才有资格记**这件事不该分轻重两套标准。
  const el = terminalBodyRef.value
  if (!canMeasureTerminal(el)) return
  headerBottom.value = el!.getBoundingClientRect().top
}
watch(terminalBodyRef, (el) => {
  headerBottomObserver?.disconnect()
  headerBottomObserver = null
  if (el && 'ResizeObserver' in window) {
    headerBottomObserver = new ResizeObserver(measureHeaderBottom)
    headerBottomObserver.observe(el)
  }
  measureHeaderBottom()
}, { immediate: true })
onMounted(() => window.addEventListener('resize', measureHeaderBottom))
onUnmounted(() => {
  headerBottomObserver?.disconnect()
  window.removeEventListener('resize', measureHeaderBottom)
})
// …plus a re-measure the instant a card appears, which closes the ResizeObserver's one structural
// blind spot: an RO fires on SIZE, never on pure TRANSLATION. A host shell that inserts or removes
// an equal-height row above this surface (pro's chrome differs from standalone's) moves
// `.terminal-body` down or up without changing its box by a single pixel, so `headerBottom` goes
// silently stale and the card lands over the pane bar it is supposed to clear. A card changes at
// most once a second and a getBoundingClientRect is cheap, so measuring at the moment of display
// is strictly better than trusting the last resize: it is the value for the frame the user will
// actually see. Flushed post-render so the read happens against settled layout.
watch(() => attentionHud.card.value, (c) => { if (c) measureHeaderBottom() }, { flush: 'post' })

// Debounced reflow: a split-mode squeeze change (open/close, or a live drag-resize of the panel)
// changes the terminal's actual pixel width, so it needs the SAME robust-resize path every other
// reflow uses (fit + sendResize), followed by the established ghosting guard (tmux refresh-client)
// — see scheduleGhostRefresh below. Debounced so a continuous drag settles into ONE reflow, not one
// per pixel (the "拖宽一次性 debounce reflow" requirement).
let drawerReflowTimer: ReturnType<typeof setTimeout> | null = null
watch(drawerSqueezePx, () => {
  if (drawerReflowTimer) clearTimeout(drawerReflowTimer)
  drawerReflowTimer = setTimeout(() => {
    drawerReflowTimer = null
    nextTick(() => {
      robustFitAndResize()
      // force: a reflow really does change every cell's position — this is not the guard's echo.
      scheduleGhostRefresh({ force: true })
    })
  }, 180)
})

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

const encoder = new TextEncoder()

// ─── WebSocket client ─────────────────────────────────────────────────────────

const {
  status: wsStatus,
  netStats,
  connect,
  reconnect: wsReconnect,
  // NB: `disconnect` is deliberately NOT pulled in. The socket's lifetime is the surface's
  // lifetime (useWebSocketClient closes it on unmount); nothing in this component may close it
  // on a visibility change — that was the "Reconnecting…" on every tab switch. If you find
  // yourself needing it here, re-read the `props.active` watch below first.
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
  httpBase: () => props.httpBase,
  authToken: () => props.authToken,
  isRemote: () => !!props.isRemote,
  activeCwd: () => tmux.activeCwd.value,
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
    const wasReconnect = everConnected // true here ⇒ we'd connected before ⇒ this is a RE-connect
    everConnected = true
    runtimeDiag.value = '' // healthy again → drop any stale reason
    // DOM layout 可能还没稳定 (特别是 Wails 首次渲染)，阶梯式 fit:
    // 100ms (快速响应) → 500ms (layout 稳定) → 1500ms (最终校准)
    setTimeout(robustFitAndResize, 100)
    setTimeout(robustFitAndResize, 500)
    setTimeout(robustFitAndResize, 1500)
    // On a RE-connect the server replays up to 256KB of ring buffer onto a screen that was never
    // cleared; for an alt-screen TUI that overlays stale frame fragments (garble). Once the resize
    // ladder above has re-asserted the size, force one clean full repaint (refresh-client) to
    // discard the replay residue. First connect starts from a blank screen, so it needs none.
    // force: a reconnect replays a bounded tail into a fresh grid, so one authoritative resend is
    // owed regardless of whatever echo window a pre-reconnect fire may have left open.
    if (wasReconnect) setTimeout(() => scheduleGhostRefresh({ force: true }), 1600)
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

// ─── active prop watch — become visible ───────────────────────────────────────
//
// The attachment (WebSocket) is NOT torn down when the tab goes to the background. A terminal
// attachment binds to a long-running process; its lifetime is a property of the SESSION, not of
// whether you happen to be looking at it — the same invariant that made every surface stay
// mounted (v-show) instead of being keyed on the active id.
//
// It used to `wsDisconnect()` on deactivate and reconnect on activate. Keeping the surface mounted
// removed the xterm rebuild (the bulk of the delay), but the socket still re-handshook and replayed
// up to `wsReplayMaxBytes` of scrollback on EVERY switch — which is the residual "Reconnecting…"
// flash that survived that fix. Half the cure looked like a cure because the other half was fast.
//
// The cost is real and bounded: N open tabs means N open sockets. That is what a terminal
// multiplexer's client is supposed to hold — and the server-side overview snapshot is already
// memoized per tick, so N clients cost one computation, not N. `connectGuarded()` stays
// idempotent (useWebSocketClient no-ops when already connected), so a re-activate is free.
watch(() => props.active, (isActive) => {
  if (!isActive) return
  // Still connect here: a tab that was never activated has no socket yet (onMounted only connects
  // the active one), so the FIRST activation is what opens it. Later ones are no-ops.
  connectGuarded()
  // Re-fit on show: xterm cannot measure itself while v-show has it display:none, so its cols/rows
  // are stale from whatever the viewport was when it was last visible.
  nextTick(() => {
    const xterm = xtermRef.value
    if (!xterm) return
    xterm.fit()
    const term = xterm.terminal?.()
    if (term) {
      sendResize(term.cols, term.rows)
      terminalRows.value = term.rows
    }
  })
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
  void loadShortcutsConfig()
  document.addEventListener('visibilitychange', onVisibilityChange)
  document.addEventListener('keydown', onKeydownDirect, { capture: true })
  document.addEventListener('keydown', onFindShortcutKeydown, { capture: true })
  document.addEventListener('paste', onClipboardPaste, { capture: true })
  terminalBodyRef.value?.addEventListener('touchmove', onTerminalBodyTouchMove, { passive: false })
  window.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('scroll', lockKeyboardViewportScroll, { passive: true })
  window.visualViewport?.addEventListener('resize', lockKeyboardViewportScroll)
  window.addEventListener('resize', onSurfaceViewportResize)
  // Connect immediately if active
  if (props.active) connectGuarded()
})

onUnmounted(() => {
  if (viewportScrollLockRaf) window.cancelAnimationFrame(viewportScrollLockRaf)
  if (ghostRefreshTimer) clearTimeout(ghostRefreshTimer)
  if (drawerReflowTimer) clearTimeout(drawerReflowTimer)
  bottomBarObserver?.disconnect()
  // FIX 1: release the global CSS var if this tab still owned it (e.g. its tab was closed
  // while active) so a stale non-zero width never survives the surface that set it.
  if (drawerWidthCssVarOwner === props.sessionId) {
    document.documentElement.style.setProperty('--dw-drawer-width', '0px')
    drawerWidthCssVarOwner = null
  }
  terminalBodyRef.value?.removeEventListener('touchmove', onTerminalBodyTouchMove)
  document.removeEventListener('keydown', onKeydownDirect, { capture: true })
  document.removeEventListener('keydown', onFindShortcutKeydown, { capture: true })
  document.removeEventListener('paste', onClipboardPaste, { capture: true })
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('scroll', lockKeyboardViewportScroll)
  window.visualViewport?.removeEventListener('scroll', lockKeyboardViewportScroll)
  window.visualViewport?.removeEventListener('resize', lockKeyboardViewportScroll)
  window.removeEventListener('resize', onSurfaceViewportResize)
})

// ─── Terminal callbacks ───────────────────────────────────────────────────────

// Last-seen xterm buffer type ('normal' | 'alternate'); a change drives the ghosting refresh.
let lastBufferType = ''

// Ghosting guard for the ALTERNATE screen (fullscreen TUI: claude-code "flicker mode", tmux).
// Symptom (reproduced): tmux's pane model (capture-pane) is CLEAN, but the web terminal shows
// stale glyphs from a previous frame (e.g. a "0" column left after two-digit content scrolls away)
// — residue that lives in xterm's BUFFER, diverged from tmux. Proven in-session: a client-side
// `term.refresh()` does NOT clear it (it re-renders the same diverged buffer); only a server-side
// `tmux refresh-client` (resend every cell) does.
//
// LEADING-EDGE THROTTLE (v3, 2026-07-26): replaces a trailing-debounce-with-maxWait-cap (v2) that
// only guaranteed a fire "eventually, within maxWait of BURST START" — production caught residue
// within that window even though the mechanism was confirmed firing correctly live (~once/sec,
// ~15ms/call, 100% success). The first output frame after idle fires a refresh-client on the next
// tick; further frames are ignored while one is pending/cooling down; the next frame after
// GHOST_REFRESH_MIN_INTERVAL fires again immediately. This bounds staleness from the LAST
// correction instead of from burst start. A real per-call cost of ~15ms means the interval can be
// small without approaching any request-cost ceiling. See ghostRefresh.ts for the math + test.
//
// SELF-FEEDING LOOP (v4, 2026-07-31): v3 was correct about WHEN to correct and wrong about what
// counts as evidence. `refresh-client` resends every cell, those cells come back as output frames,
// and this function treated them as "new output → maybe new residue → correct again". Idle pane,
// nobody typing, measured: 7.7 fires/s and 15 KB/s of pure echo (scripts/diag/wsprobe A/B). The window
// below closes that edge — see GHOST_ECHO_WINDOW for the numbers and why a longer minInterval
// could never have fixed it.
const GHOST_REFRESH_MIN_INTERVAL = 120
let ghostRefreshTimer: ReturnType<typeof setTimeout> | null = null
let ghostLastFiredAt: number | null = null
// End of the echo window opened by the last fire; output arriving before it is OUR redraw.
let ghostEchoUntil: number | null = null
// When the user last sent a keystroke — a correction defers while they are still typing. Set in
// sendTerminalData, the single exit every input path funnels through (keys, paste, toolbar, IME),
// so no route can bypass the deferral.
let ghostLastInputAt: number | null = null
// force: the caller has a real reason (reflow / reconnect / buffer switch), not an output frame.
function scheduleGhostRefresh(opts?: { force?: boolean }): void {
  if (!tmux.attached.value) return
  const term = xtermRef.value?.terminal?.()
  if (!term || term.buffer.active.type !== 'alternate') return
  const nowMs = Date.now()
  if (!opts?.force && ghostRefreshSuppressed(ghostEchoUntil, nowMs)) return
  if (!opts?.force && ghostRefreshDeferredForTyping(ghostLastInputAt, ghostLastFiredAt, nowMs)) return
  if (ghostRefreshTimer) return // a fire is already pending/cooling down — later frames in the same window are no-ops
  const wait = ghostRefreshWait(ghostLastFiredAt, Date.now(), GHOST_REFRESH_MIN_INTERVAL)
  ghostRefreshTimer = setTimeout(() => {
    ghostRefreshTimer = null
    ghostLastFiredAt = Date.now()
    const t = xtermRef.value?.terminal?.()
    if (!t || t.buffer.active.type !== 'alternate') return
    // Opened BEFORE the request, not after it resolves: the resent cells can reach the socket
    // while the POST is still in flight, and those are exactly the frames that must not re-arm.
    ghostEchoUntil = Date.now() + GHOST_ECHO_WINDOW
    void tmux.runRefreshClient()
  }, wait)
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
    // 同一个事件顺手回答"贴底了没有 / 下面积了多少新行"——「回到最新」按钮的全部输入。
    // xterm 在滚动**和**新行推进缓冲区时都会 fire 这个事件，所以这里既盖住"用户滚上去了"，
    // 也盖住"用户没动、但底下又来了新输出"。
    noteTerminalScrollPosition(terminal)
  })
  // 有选区才出现「复制」——问 xterm 自己，不猜。
  terminal.onSelectionChange(() => {
    const has = terminal.hasSelection()
    if (terminalHasSelection.value !== has) terminalHasSelection.value = has
  })
  // 终端标题（OSC 0/2）。多数 shell 会把正在跑的命令写进去，这是"此刻在跑什么"最诚实的客户端来源
  // ——原生终端标题栏显示的就是它，我们没有第二份数据可用（后端那一帧不带命令字段）。
  terminal.onTitleChange((title) => {
    const t = (title || '').trim()
    if (terminalTitle.value !== t) terminalTitle.value = t
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
    // 同上：onScroll 在 alt-screen 的 PgUp/PgDn（原地重绘）里不 fire，靠这里兜住。
    noteTerminalScrollPosition(terminal)
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
        // Non-tmux Agent Overview: every session's status + live tail, pushed on THIS surface's
        // socket but owned by the portal. Handed to a module-level singleton rather than emitted,
        // since the consumer (tab strip / overview popover) is not an ancestor of this component.
        case 'sessions_overview':
          applySessionsOverviewFrame(msg.payload)
          break
        // Explicit "I need you" signals (BEL / OSC 9 / 777 / 99) — the ONE frame that carries the
        // agent's own words. Same singleton hand-off as the frame above, and for the same reason:
        // it describes EVERY session (a bell can ring where no socket is open) while its consumers
        // (overview cards, tab strip) live in the portal, not under this component.
        case 'agent_signal':
          applyAgentSignalFrame(msg.payload)
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
  // Every input route ends here, so this is where "the user is typing" is known. See
  // GHOST_TYPING_QUIET: a keystroke buys the terminal quiet from full-screen resends.
  ghostLastInputAt = Date.now()
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
// surface's body. openInstallGuide opens the notify-provider quick sheet (the notify
// entry point backing the pane bar's bell); the name is kept for the host API.
function openInstallGuide() { notifyQuickOpen.value = true }
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

/* 非 tmux 时的动作条。flex-shrink:0 + 排在最前 = 位置锚死在行首：右边那句话出现/消失/变长，
   动作一个像素都不动（一个会移动的常驻按钮等于每次都要重新找它）。 */
.ssr-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 4px;
}
/* 常驻 chrome：默认几乎透明（muted-foreground，无边框无底色），只在 hover/focus 时才浮起来。
   视觉重量必须低于终端内容本身 —— 它是随时可用的工具，不是要人看的东西。 */
.ssr-action {
  position: relative; /* 行数角标绝对定位在它上面 */
  display: inline-grid;
  place-items: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: hsl(var(--muted-foreground, 240 5% 64%));
  cursor: pointer;
  flex-shrink: 0;
  touch-action: manipulation;
  transition: color 0.1s, background 0.1s;
}
.ssr-action:hover { color: hsl(var(--foreground, 0 0% 98%)); background: hsl(var(--accent, 240 4% 22%)); }
.ssr-action:focus-visible { outline: 1px solid hsl(var(--ring, 240 5% 64%)); outline-offset: -1px; }
.ssr-action:active { transform: scale(0.92); }
/* 触屏上 24px 太小 —— 手指目标放大到 30px，但视觉尺寸（图标）不变。 */
.is-mobile .ssr-action { width: 30px; height: 30px; }
/* 「中断」是唯一一个会改变正在跑的东西的动作，hover 时用红色说明这一点（静止态仍与其余同重量，
   它不该在没事发生时也喊）。 */
.ssr-action.is-alert:hover { color: #ff6b6b; background: rgba(255, 82, 82, 0.12); }
/* 「回到最新」角上的行数。绝对定位 → 它不改变按钮尺寸，所以数字从 1 变到 99+ 时按钮不会变宽、
   后面的按钮也就不会被推着走。 */
.ssr-action-badge {
  position: absolute;
  top: -1px;
  right: -3px;
  min-width: 13px;
  padding: 0 2px;
  border-radius: 7px;
  background: hsl(var(--muted-foreground, 240 5% 64%));
  color: hsl(var(--background, 240 6% 10%));
  font-size: 0.55rem;
  line-height: 13px;
  font-variant-numeric: tabular-nums;
  text-align: center;
  pointer-events: none;
}

/* "此刻最具体的真话"。单行、省略号、低对比度：它是一句旁白，不是标题。 */
.ssr-note {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 0 6px;
  font-size: 0.72rem;
  line-height: 1.4;
  color: hsl(var(--muted-foreground, 240 5% 64%));
}
.ssr-note-text {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* cwd 档是这行里价值最低的一句（它只回答"我在哪"），所以它也是窄屏第一个被砍的。 */
.ssr-note--cwd { opacity: 0.85; }
.ssr-dot {
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  border-radius: 50%;
}
/* 节奏本身（时长/曲线/谷底不透明度）由 surfaceDotStyle 从 STATUS_MOTION 现算成三个 CSS 变量注进来
   —— 和 TmuxPaneBar / TmuxStatusSheet / AgentOverview 读的是同一个常量，四处的点不可能各跳各的。
   动画声明必须留在这个 scoped 块里：Vue 会把 @keyframes 改名加 scope hash，内联 style 里的
   animation 追不到那个新名字。done-unseen 在 STATUS_MOTION 里是 null（静止），所以没有它的规则
   —— 缺席即契约，不是漏了。 */
.ssr-dot--waiting,
.ssr-dot--running {
  animation: status-dot-pulse var(--dot-duration) var(--dot-easing) infinite;
}
@keyframes status-dot-pulse {
  0%, 100% { opacity: var(--dot-min-opacity); }
  50% { opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .ssr-dot--waiting, .ssr-dot--running { animation: none; opacity: 1; }
}

/* 窄屏降级顺序（按钮永不被砍 —— 手机上它们价值最高）：
   1) ≤560px：先砍 cwd 档，那是三档里最不具体的一句；
   2) ≤420px：整格中区收起，只剩动作 + 右侧心跳。 */
@media (max-width: 560px) {
  .ssr-note--cwd { display: none; }
}
@media (max-width: 420px) {
  .ssr-note { display: none; }
}
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
