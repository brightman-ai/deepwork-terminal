<template>
  <div class="cli-terminal-view" data-testid="cli-portal-terminal-view">
    <!-- Loading / error / empty states -->
    <div v-if="loading" class="terminal-state-msg" data-testid="cli-portal-loading">
      加载中…
    </div>
    <div
      v-else-if="error"
      class="terminal-state-msg terminal-state-msg--error"
      data-testid="cli-portal-error"
    >{{ error }}</div>
    <div
      v-else-if="!activeTabId"
      class="terminal-state-msg"
      data-testid="cli-portal-empty"
    >
      <p>正在准备终端…</p>
    </div>

    <!-- One slot per tab — v-show for tab switching, keep DOM alive. A slot renders ONE surface
         (today's shape, unchanged) unless `tab.split` is set (terminal-split scope), in which case
         it renders two + a draggable divider. Unsplit is the overwhelmingly common case, so it
         stays the cheap path: no extra wrapper div, no split-only computed work per tab. -->
    <template v-else>
      <template v-for="tab in tabsWithSession" :key="tab.id">
        <div v-show="tab.id === activeTabId" class="cli-terminal-view__slot">
          <!-- 分屏按钮常驻、不随分屏状态消失（2026-10-12 拍定），但不再占独立一行（2026-10-13
               Human："两行占我的屏幕空间"）：经 #status-extra 插槽放进格子自己的状态行。
               splitAllowed 是"当前激活 tab"的判定（移动端 + tmux-attached 都排除，tmux 已有自己的
               vsplit/hsplit/zoom），对所有 tab 复用同一个值是安全的简化：非激活 tab 此刻本就是
               v-show 隐藏的，真正切过去时 activeTabSplitAllowed 会随 activeTab 重新算出那个 tab
               自己的结果。 -->
          <CliTerminalSurface
            v-if="!tab.split"
            :session-id="tab.sessionId!"
            :session-name="tab.name"
            :active="tab.id === activeTabId"
            :http-base="tab.httpBase"
            :ws-base="tab.wsBase"
            :auth-token="tab.authToken"
            :machine-label="tab.machineLabel"
            :is-remote="tab.isRemote"
            :conn-error="tab.connError"
            :diagnose="tab.diagnose"
            :dw-rollup="dwRollup"
            :dw-overview-open="dwOverviewOpen"
            :ref="(el) => onSurfaceRef(tab.id, el)"
            :data-testid="`cli-portal-surface-${tab.id}`"
            @agent-state="(s) => emit('agent-state', tab.id, s)"
            @agent-notifications="(s) => emit('agent-notifications', tab.id, s)"
            @session-exit="(code) => emit('session-exit', tab.id, code)"
            @connection-change="(s) => emit('connection-change', tab.id, s)"
            @dw-toggle-overview="emit('dw-toggle-overview')"
          >
            <template v-if="splitAllowed" #status-extra>
              <SplitControlBar
                :data-testid="`cli-portal-split-ctl-${tab.id}`"
                @split-orientation="(o) => emit('split-orientation', tab.id, o)"
              />
            </template>
          </CliTerminalSurface>

          <!-- terminal-split：原生浏览器内分屏（docs/product/terminal-split/spec.md）。两个真正独立
               的 CliTerminalSurface（各自 session/xterm/WS），中间一条可拖拽分隔条。主格继续走
               `tab.id` 这个既有 key（未分屏 tab 的键盘/leader/copy-mode 路由全部不受影响——那些路由
               只认 `surfaceRefs[activeTab.id]`，分屏只影响这里的渲染，不改那条路由，见 spec §7
               已知约束）；副格注册在 `${tab.id}__split2` 这个派生 key 下。
               zoom：被放大的那格用 v-show 隐藏另一格（不是 v-if——连接必须活着，退出 zoom 要原样
               恢复，不是重新连），:style 覆盖成占满交叉轴；分隔条在 zoom 态也隐藏，拖不动一条看
               不见的线没有意义。 -->
          <div
            v-else
            class="cli-terminal-view__split"
            :class="tab.split.orientation"
            :data-testid="`cli-portal-split-${tab.id}`"
          >
            <CliTerminalSurface
              v-show="!tab.split.zoomed || tab.split.zoomed === 'first'"
              :style="paneStyleFor(tab.id, tab.split.ratio, false, tab.split.zoomed)"
              :session-id="tab.sessionId!"
              :session-name="tab.name"
              :active="tab.id === activeTabId"
              :http-base="tab.httpBase"
              :ws-base="tab.wsBase"
              :auth-token="tab.authToken"
              :machine-label="tab.machineLabel"
              :is-remote="tab.isRemote"
              :conn-error="tab.connError"
              :diagnose="tab.diagnose"
              :dw-rollup="dwRollup"
              :dw-overview-open="dwOverviewOpen"
              :ref="(el) => onSurfaceRef(tab.id, el)"
              :data-testid="`cli-portal-surface-${tab.id}`"
              :class="{ 'is-focused-pane': isFocusedPane(tab.id, tab.split, 'first') }"
              :paste-fallback="currentPane(tab.split, focusedPaneOf(tab.id)) === 'first'"
              @focusin="(e: FocusEvent) => onPaneFocusIn(tab.id, 'first', e)"
              @agent-state="(s) => emit('agent-state', tab.id, s)"
              @agent-notifications="(s) => emit('agent-notifications', tab.id, s)"
              @session-exit="(code) => emit('session-exit', tab.id, code)"
              @connection-change="(s) => emit('connection-change', tab.id, s)"
              @dw-toggle-overview="emit('dw-toggle-overview')"
            >
              <!-- 主格：分屏方向（高亮那个 = 切换焦点）+ 放大本格。主格被藏起来（副格放大）时方向
                   按钮由副格接手，见下。 -->
              <template v-if="splitAllowed" #status-extra>
                <SplitControlBar
                  :split="tab.split"
                  pane="first"
                  :focused-pane="focusedPaneOf(tab.id)"
                  :data-testid="`cli-portal-split-ctl-${tab.id}`"
                  @split-orientation="(o) => emit('split-orientation', tab.id, o)"
                  @switch-pane="switchPane(tab)"
                  @toggle-zoom="(w) => emit('toggle-zoom', tab.id, w)"
                />
              </template>
              <template #overlay>
                <div
                  v-if="flashOn(tab.id, 'first')"
                  :key="paneFlash[tab.id]!.seq"
                  class="pane-focus-flash"
                  data-testid="pane-focus-flash"
                  @animationend="clearFlash(tab.id)"
                ><span class="pane-focus-flash__word">{{ paneWord(tab.split.orientation, 'first') }}</span></div>
              </template>
            </CliTerminalSurface>

            <!-- 空副格（session 还在起，瞬时态）：不渲染第二个 surface（没有 sessionId 可绑），
                 占位条让分隔条/布局不塌。起败了 useCliState.splitTab 会把 `tab.split` 整个撤掉，
                 不会卡在这个占位态。 -->
            <template v-if="tab.split.secondSessionId">
              <div
                v-show="!tab.split.zoomed"
                class="cli-terminal-view__divider"
                :class="tab.split.orientation"
                :data-testid="`cli-portal-split-divider-${tab.id}`"
                @pointerdown="onDividerDown($event, tab.id, tab.split.orientation)"
              />
              <CliTerminalSurface
                v-show="!tab.split.zoomed || tab.split.zoomed === 'second'"
                :style="paneStyleFor(tab.id, 1 - tab.split.ratio, true, tab.split.zoomed)"
                :session-id="tab.split.secondSessionId"
                :session-name="`${tab.name} · 2`"
                :active="tab.id === activeTabId"
                :http-base="tab.httpBase"
                :ws-base="tab.wsBase"
                :auth-token="tab.authToken"
                :machine-label="tab.machineLabel"
                :is-remote="tab.isRemote"
                :conn-error="tab.connError"
                :diagnose="tab.diagnose"
                :dw-rollup="dwRollup"
                :dw-overview-open="dwOverviewOpen"
                :ref="(el) => onSurfaceRef(`${tab.id}__split2`, el)"
                :data-testid="`cli-portal-surface-${tab.id}__split2`"
                class="is-second-pane"
                :class="{ 'is-focused-pane': isFocusedPane(tab.id, tab.split, 'second') }"
                :paste-fallback="currentPane(tab.split, focusedPaneOf(tab.id)) === 'second'"
                @focusin="(e: FocusEvent) => onPaneFocusIn(tab.id, 'second', e)"
                @agent-state="(s) => emit('agent-state', `${tab.id}__split2`, s)"
              >
                <template v-if="splitAllowed" #status-extra>
                  <SplitControlBar
                    :split="tab.split"
                    pane="second"
                    :show-split-toggles="tab.split.zoomed === 'second'"
                    :focused-pane="focusedPaneOf(tab.id)"
                    :data-testid="`cli-portal-split-ctl-${tab.id}__split2`"
                    @split-orientation="(o) => emit('split-orientation', tab.id, o)"
                    @switch-pane="switchPane(tab)"
                    @toggle-zoom="(w) => emit('toggle-zoom', tab.id, w)"
                    @close-pane="emit('close-split', tab.id)"
                  />
                </template>
                <template #overlay>
                  <div
                    v-if="flashOn(tab.id, 'second')"
                    :key="paneFlash[tab.id]!.seq"
                    class="pane-focus-flash"
                    data-testid="pane-focus-flash"
                    @animationend="clearFlash(tab.id)"
                  ><span class="pane-focus-flash__word">{{ paneWord(tab.split.orientation, 'second') }}</span></div>
                </template>
              </CliTerminalSurface>
            </template>
            <div v-else class="cli-terminal-view__split-pending" data-testid="cli-portal-split-pending">
              正在打开新终端…
            </div>
          </div>
        </div>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { nextTick, reactive, ref } from 'vue'
import CliTerminalSurface from '@terminal/components/terminal-session/CliTerminalSurface.vue'
import SplitControlBar from '@terminal/components/terminal-session/SplitControlBar.vue'
import { currentPane, otherPane, paneWord, type PaneId } from '@terminal/components/terminal-session/splitControl'
import type { EffectiveStatus } from '@terminal/composables/cli/useAgentOverview'
import type { AgentState, WSConnectionStatus } from '@terminal/types/terminal'
import type { TabSplit } from '@terminal/types/workbench'

interface TabWithSession {
  id: string
  name: string
  sessionId?: string
  // Remote-tab connection (mesh), resolved upstream by resolveTabConnection. Local tabs leave
  // httpBase/wsBase/authToken empty → CliTerminalSurface falls back to same-origin behavior.
  httpBase?: string
  wsBase?: string
  authToken?: string
  machineLabel?: string
  isRemote?: boolean
  connError?: string
  diagnose?: () => Promise<{ ok: boolean; error?: string }>
  split?: TabSplit
}

defineProps<{
  loading: boolean
  error: string | null
  activeTabId: string | undefined
  tabsWithSession: TabWithSession[]
  /** 非 tmux 底栏的状态卷起与总览开关。这一层只**转发** —— 标签的身份/顺序/状态归 portal
   *  （useCliState），终端表面不自己推导（否则就有第二个真相源）。 */
  dwRollup?: Record<EffectiveStatus, number>
  dwOverviewOpen?: boolean
  /** terminal-toolbar/terminal-split 常驻控制条的开关——移动端、tmux-attached 都排除在外（tmux
   *  自己就有 vsplit/hsplit/zoom，物理键盘也通常在场）。宿主算（useCliState.activeTabSplitAllowed），
   *  这里只转发，不重新判断。 */
  splitAllowed?: boolean
}>()

const emit = defineEmits<{
  (e: 'register-surface', tabId: string, el: InstanceType<typeof CliTerminalSurface> | null): void
  (e: 'agent-state', tabId: string, state: AgentState | null): void
  (e: 'agent-notifications', tabId: string, states: AgentState[]): void
  (e: 'session-exit', tabId: string, exitCode: number): void
  (e: 'connection-change', tabId: string, status: WSConnectionStatus): void
  /** 底栏的总览胶囊，原样上交给拥有标签的那一层执行。 */
  (e: 'dw-toggle-overview'): void
  /** 拖拽分隔条松手后的最终比例——宿主负责持久化（useCliState.setSplitRatio）。拖拽过程中的
   *  高频中间值只在这个组件内部做视觉反馈，不逐帧上抛，见下方 onDividerMove。 */
  (e: 'split-ratio', tabId: string, ratio: number): void
  /** 未分屏 → 分屏；已分屏 → 换方向。永远不取消分屏（见 splitControl.ts 头注）。 */
  (e: 'split-orientation', tabId: string, orientation: 'row' | 'column'): void
  (e: 'toggle-zoom', tabId: string, which: 'first' | 'second'): void
  /** 已确认（点了两次）的"关闭第二格"——会结束那格的 session。 */
  (e: 'close-split', tabId: string): void
}>()

/** 本地也记一份 surface 实例——切换焦点要直接调某一格的 focusTerminal()。 */
const paneRefs: Record<string, InstanceType<typeof CliTerminalSurface> | null> = {}

function onSurfaceRef(tabId: string, el: unknown) {
  paneRefs[tabId] = el as InstanceType<typeof CliTerminalSurface> | null
  emit('register-surface', tabId, el as InstanceType<typeof CliTerminalSurface> | null)
}

// ── 分屏焦点：哪一格是"当前格" ─────────────────────────────────────────────────────────────
// 纯前端状态（不落盘、不跨设备）：最后获得焦点的那格。点进格子里的终端/输入框/按钮都算；
// 分屏控件自己的按钮不算——点"切换焦点"按钮那一下会先让按钮拿到焦点，若也算，就永远切不走。
const focusedPane = reactive<Record<string, PaneId>>({})

function focusedPaneOf(tabId: string): PaneId {
  return focusedPane[tabId] ?? 'first'
}

/** 当前格的常驻标记（状态行底边一道强调色）。放大态只剩一格，不需要标。 */
function isFocusedPane(tabId: string, split: TabSplit, pane: PaneId): boolean {
  return !split.zoomed && focusedPaneOf(tabId) === pane
}

function onPaneFocusIn(tabId: string, pane: PaneId, e: FocusEvent): void {
  const target = e.target as Element | null
  if (target?.closest?.('.split-ctl')) return
  focusedPane[tabId] = pane
}

// ── 切换焦点时闪一下的大字（LEFT/RIGHT · UP/DOWN）+ 格子高亮 ─────────────────────────────────
// 2026-10-13 Human：切的那一下要"一个大写的瞬间的聚焦……同时窗口高亮一下"，且"不要影响 xterm
// 的文本流，而是前端 WEB 来做，避免影响到快照回看和 F5 刷新"。所以是盖在终端上的 DOM 浮层
// （CliTerminalSurface 的 #overlay 插槽），一段 CSS 动画放完自己消失；seq 做 key，连点也能重播。
// 只在"切换焦点"这个显式动作上闪；鼠标直接点进某格不闪——那一下用户自己知道点在哪。
const paneFlash = reactive<Record<string, { pane: PaneId; seq: number }>>({})
let flashSeq = 0

function flashOn(tabId: string, pane: PaneId): boolean {
  return paneFlash[tabId]?.pane === pane
}

function clearFlash(tabId: string): void {
  delete paneFlash[tabId]
}

/** 切到另一格：放大态下改为放大另一格（另一格此刻看不见，只换焦点没有意义），否则只移焦点。 */
function switchPane(tab: TabWithSession): void {
  const split = tab.split
  if (!split) return
  const target = otherPane(currentPane(split, focusedPaneOf(tab.id)))
  if (split.zoomed) emit('toggle-zoom', tab.id, target)
  focusedPane[tab.id] = target
  paneFlash[tab.id] = { pane: target, seq: ++flashSeq }
  // 放大目标切换要等 v-show 生效后那格才可聚焦（display:none 的元素拿不到焦点）。
  void nextTick(() => requestAnimationFrame(() => {
    paneRefs[target === 'first' ? tab.id : `${tab.id}__split2`]?.focusTerminal()
  }))
}

// 主格固定一个 flex-basis 百分比（副格用 1-ratio 的互补值），两格各自撑满交叉轴——这就是
// VSCode/iTerm 分屏那种"中间一条可拖的分隔线"手感的最小实现，不依赖任何第三方布局库。
/** 按比例分的是**扣掉分隔条之后**的空间（basis 0 + grow=比例）。原来是 `0 0 50%` ×2 + 4px 分隔条
 *  = 100% + 4px：第二格最后 4px 被父级 overflow:hidden 裁掉，而 xterm 按整格宽度排列，最右一列
 *  会被切掉一截（2026-10-13 一行化验收时量出：1920 宽视口里第二格右缘在 1924）。 */
function paneStyle(ratio: number): { flex: string } {
  return { flex: `${ratio.toFixed(4)} 1 0%` }
}

// ── 分隔条拖拽：pointerdown 记起点 → window 级 pointermove 算新比例（本地态，纯视觉）→
//    pointerup 才把最终值上抛持久化。本地 ref 而非直接改 props（props 只读），所以拖拽中的帧
//    由这个 ref 驱动 paneStyleLive（覆盖 props 里的 ratio），松手后父层写回真值，ref 复位。
const dragState = ref<{ tabId: string; orientation: 'row' | 'column'; startRect: DOMRect } | null>(null)
const liveRatio = ref<number | null>(null)

function onDividerDown(e: PointerEvent, tabId: string, orientation: 'row' | 'column'): void {
  const container = (e.currentTarget as HTMLElement).parentElement
  if (!container) return
  dragState.value = { tabId, orientation, startRect: container.getBoundingClientRect() }
  liveRatio.value = null
  window.addEventListener('pointermove', onDividerMove)
  window.addEventListener('pointerup', onDividerUp, { once: true })
  e.preventDefault()
}

function onDividerMove(e: PointerEvent): void {
  const d = dragState.value
  if (!d) return
  const { startRect, orientation } = d
  const raw = orientation === 'row'
    ? (e.clientX - startRect.left) / startRect.width
    : (e.clientY - startRect.top) / startRect.height
  liveRatio.value = Math.min(0.9, Math.max(0.1, raw))
}

function onDividerUp(): void {
  window.removeEventListener('pointermove', onDividerMove)
  const d = dragState.value
  const r = liveRatio.value
  dragState.value = null
  liveRatio.value = null
  if (d && r != null) emit('split-ratio', d.tabId, r)
}

// 拖拽进行中用本地 live 值覆盖 props 里还没写回的旧 ratio，松手之后父层把新值存回 workbench，
// props 更新，这个覆盖自然让位（liveRatio 已在 onDividerUp 里清空）。`isSecond` 翻转副格的值
// ——liveRatio 算的始终是"主格那一侧"的比例，副格永远是它的互补值，两者在拖拽的每一帧都必须
// 加起来等于 1，否则中间会露缝或重叠一帧。
// zoomed 存在时整个 ratio 计算让位——被放大的格子占满交叉轴（另一格靠 v-show 隐藏，不占流），
// 这样退出 zoom 时两格的 flex-basis 立刻恢复成拖拽前的 ratio，不需要额外记一份"zoom 前的值"。
function paneStyleFor(
  tabId: string,
  restRatio: number,
  isSecond = false,
  zoomed?: 'first' | 'second',
): { flex: string } {
  if (zoomed) {
    const isZoomedPane = (isSecond && zoomed === 'second') || (!isSecond && zoomed === 'first')
    return paneStyle(isZoomedPane ? 1 : 0)
  }
  if (dragState.value?.tabId === tabId && liveRatio.value != null) {
    return paneStyle(isSecond ? 1 - liveRatio.value : liveRatio.value)
  }
  return paneStyle(restRatio)
}
</script>

<style scoped>
.cli-terminal-view {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  position: relative;
  display: flex;
  flex-direction: column;
}

.terminal-state-msg {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 48px;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}
.terminal-state-msg--error { color: #f87171; }

/* 每个 tab 槽位：常驻控制条（auto 高）+ 下面的 surface/split 区域（吃掉剩余空间）。v-show 整个
   槽位切 tab，槽位内部这层 flex 布局跟 tab 切换无关，所有 tab 共用同一套规则。 */
.cli-terminal-view__slot {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* Surface fills remaining vertical space (unsplit path; the split path below overrides via
   inline :style, which always wins over this class rule). */
.cli-terminal-view :deep(.cli-terminal-surface) {
  flex: 1;
  min-height: 0;
}

/* terminal-split：分屏容器撑满 tab 槽位，按 orientation 横/竖排两个 surface。 */
.cli-terminal-view__split {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}
.cli-terminal-view__split.row { flex-direction: row; }
.cli-terminal-view__split.column { flex-direction: column; }
.cli-terminal-view__split :deep(.cli-terminal-surface) {
  flex: initial; /* 交给内联 :style 的 flex-basis 百分比决定宽/高，不被上面的全局规则抢走 */
  min-width: 0;
  min-height: 0;
}

.cli-terminal-view__divider {
  flex: 0 0 auto;
  background: #2a1f3a;
  touch-action: none;
  position: relative;
}
.cli-terminal-view__divider.row { width: 4px; cursor: col-resize; }
.cli-terminal-view__divider.column { height: 4px; cursor: row-resize; }
.cli-terminal-view__divider:hover,
.cli-terminal-view__divider:active { background: #7a4ab0; }
/* 命中区比视觉线宽，细线不等于细手感——4px 的视觉线配 4px 命中区在触屏上基本点不中。 */
.cli-terminal-view__divider.row::before {
  content: ''; position: absolute; top: 0; bottom: 0; left: -4px; right: -4px;
}
.cli-terminal-view__divider.column::before {
  content: ''; position: absolute; left: 0; right: 0; top: -4px; bottom: -4px;
}

.cli-terminal-view__split-pending {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: hsl(var(--muted-foreground));
  font-size: 0.8rem;
}

/* 上下分屏时，下格的状态行正好落在视口垂直中线——右缘中间固定着「工作台」抽屉把手
   （ResourceDrawer .rd-handle：fixed、right:0、垂直居中、26px 宽、z-index 290），会压住这一行最右端
   的按钮（实测压住 × 的右下角，点上去落到把手上）。只给这一行右侧让出把手的宽度；其它状态行都在
   顶部，碰不到把手。 */
.cli-terminal-view__split.column :deep(.is-second-pane .surface-status-row) {
  padding-right: 28px;
}

/* 当前格的常驻标记：状态行底边换成强调色（多一道 1px 内阴影 = 2px 粗），不改任何尺寸。 */
.cli-terminal-view__split :deep(.is-focused-pane .surface-status-row) {
  border-bottom-color: #9b6bd6;
  box-shadow: inset 0 -1px 0 #9b6bd6;
}

/* 切换焦点那一下：整格描一圈强调色 + 正中一个大写方位词，约 0.9s 淡出后由 animationend 移除。
   纯 DOM 浮层、pointer-events:none —— 不挡点击、不碰 xterm 字节流（回看/F5 回放里不会有它）。
   z-index 低于输入条悬浮球(50)、高于终端内容。 */
.pane-focus-flash {
  position: absolute;
  inset: 0;
  z-index: 30;
  display: flex;
  align-items: center;
  justify-content: center;
  pointer-events: none;
  border: 2px solid #9b6bd6;
  background: rgba(155, 107, 214, 0.10);
  animation: pane-focus-flash 0.9s ease-out forwards;
}
.pane-focus-flash__word {
  padding: 6px 22px;
  border-radius: 10px;
  background: rgba(20, 16, 30, 0.72);
  color: #eadcff;
  font: 700 2.6rem/1.1 ui-sans-serif, system-ui, sans-serif;
  letter-spacing: 0.12em;
  text-shadow: 0 2px 12px rgba(155, 107, 214, 0.6);
}
@keyframes pane-focus-flash {
  0% { opacity: 0; }
  12% { opacity: 1; }
  60% { opacity: 1; }
  100% { opacity: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .pane-focus-flash { animation-duration: 0.6s; }
}
</style>
