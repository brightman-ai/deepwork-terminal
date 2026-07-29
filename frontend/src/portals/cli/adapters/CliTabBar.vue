<template>
  <div class="cli-tab-bar dw-titlebar-blend" data-testid="cli-portal-tab-bar">
    <!-- D7 overview — LEADS the strip. It is the "zoom out to all terminals" control, so it
         belongs before the tabs it summarizes (the same reading order as a breadcrumb's root).
         It was in the far-right chrome cluster, where it read as one more utility icon next to
         refresh/version and was routinely missed. -->
    <button
      class="cli-tab-bar__overview"
      type="button"
      data-testid="cli-portal-overview-trigger"
      title="标签总览（所有终端的状态与实时预览）"
      @click="emit('toggle-overview')"
    >
      <LayoutGrid :size="14" />
      <!-- Global roll-up, merged INTO the toggle capsule — the same shape TmuxPaneBar's
           .tpb-caps-rollup has, for the same reason: the glance count belongs next to the control
           that opens the full view, pinned at the strip's left edge so it survives horizontal
           scrolling of a long tab list. This is what non-tmux was missing versus tmux. -->
      <span v-if="rollupSegs.length" class="cli-tab-bar__rollup" data-testid="cli-portal-rollup">
        <span v-for="s in rollupSegs" :key="s.status" class="ctb-seg" :class="`s-${s.status}`">{{ s.icon }}{{ s.count }}</span>
      </span>
    </button>

    <!-- Groups + tabs -->
    <template v-for="group in groups" :key="group.id">
      <!-- Group header (only when multiple groups) -->
      <div
        v-if="showGroupHeaders"
        class="cli-tab-bar__group-header"
        :style="group.color ? { '--group-color': group.color } : {}"
        :data-testid="`cli-portal-group-header-${group.id}`"
        @click="emit('toggle-group', group.id)"
      >
        <span class="group-name">{{ group.name }}</span>
        <span class="group-chevron">{{ group.collapsed ? '▸' : '▾' }}</span>
      </div>

      <template v-if="!group.collapsed">
        <button
          v-for="tab in group.tabs"
          :key="tab.id"
          class="cli-tab-bar__tab"
          :class="{
            'is-active': tab.id === activeTabId,
            'needs-input': tabStatus(tab.id) === 'waiting',
            'not-live': !!notLive(tab.id),
          }"
          :title="tabTitle(tab)"
          :data-testid="`cli-portal-tab-${tab.id}`"
          @click="emit('switch', tab.id)"
          @dblclick.stop="emit('rename-start', tab.id)"
          @contextmenu="emit('context-menu', $event, tab.id)"
        >
          <!-- 前导点有两个轴，存活态优先：进程都不在了，"agent 在干嘛"就无从谈起。
               ① 没活着 → 中性灰、静止（LIVENESS_COLOR / LIVENESS_MOTION）
               ② 活着   → agent 状态点，SAME status vocabulary as the overview cards and the tmux
                  pane bar (STATUS_COLOR + STATUS_MOTION, seen-aware effectiveStatus). It used to
                  carry a private palette and no done-unseen tier, so a tab and its own overview
                  card could show different colours for the same terminal. -->
          <span
            v-if="notLive(tab.id)"
            class="tab-agent-dot"
            :class="`l-${notLive(tab.id)}`"
            :data-testid="`cli-portal-tab-liveness-${tab.id}`"
          />
          <span v-else-if="tabStatus(tab.id)" class="tab-agent-dot" :class="`s-${tabStatus(tab.id)}`" />

          <!-- Rename input -->
          <input
            v-if="renamingTabId === tab.id"
            :value="renameValue"
            class="tab-rename-input"
            :data-testid="`cli-portal-tab-rename-${tab.id}`"
            @input="emit('rename-input', ($event.target as HTMLInputElement).value)"
            @blur="emit('rename-commit')"
            @keyup.enter="emit('rename-commit')"
            @keyup.escape="emit('rename-cancel')"
            @keydown.stop
            @keypress.stop
            @click.stop
            @dblclick.stop
            @mousedown.stop
          />
          <span v-else class="tab-name">{{ displayTabName(tab.name, tabPositions.get(tab.id)) }}</span>

          <!-- 「已重开」——服务重启后这个标签背后的进程没了，我们替用户开了一个新 shell 并 cd 回
               原目录。终端里那行说明是主要的告知，这枚小标只是让人在标签栏一眼看出「是哪几个」。
               它在用户于该终端首次输入后消失（他动手了就说明看见了），所以刻意做得克制：中性色、
               无动效、不抢 needs-input 的注意力。 -->
          <span
            v-if="reopened(tab.id)"
            class="tab-reopened"
            :data-testid="`cli-portal-tab-reopened-${tab.id}`"
          >已重开</span>

          <!-- Close -->
          <span
            class="tab-close"
            :data-testid="`cli-portal-tab-close-${tab.id}`"
            @click.stop="emit('close', tab.id)"
          >&times;</span>
        </button>
      </template>
    </template>

    <!-- D5: "+" creates a LOCAL terminal directly — no branch menu. Remote terminal is now its
         own small icon button, out in the top-right chrome cluster (远程终端 is the minority path
         and shouldn't intercept every tap on the primary "new terminal" action). -->
    <button
      class="cli-tab-bar__tab cli-tab-bar__tab--add"
      data-testid="cli-portal-add-tab"
      title="新建本机终端"
      @click="emit('add')"
    >+</button>

    <!-- Usage chip TRAILS the tabs (it is contextual to the terminal session, so it belongs with
         them — the same slot pro's CliV2 fills right after its TopTabBar). Keeping it here (not in
         the far-right chrome cluster) makes the chip's placement identical in both shells. -->
    <div v-if="$slots['tab-trailing']" class="cli-tab-bar__tab-trailing">
      <slot name="tab-trailing" />
    </div>

    <!-- Auto-update pill — appears ONLY when a newer build is live (zero footprint otherwise).
         Trails the usage chip; one tap clears client caches + hard-reloads onto the new build.
         This is the fix for "the tab keeps showing an old build after a redeploy". -->
    <button
      v-if="updateAvailable"
      class="cli-tab-bar__update"
      type="button"
      data-testid="cli-portal-update"
      title="有新版本 — 点此清缓存并重新加载"
      @click="applyUpdate"
    ><RefreshCw :size="13" /><span>有更新</span></button>

    <!-- Spacer pushes the top-right chrome cluster hard against the right edge. -->
    <div class="cli-tab-bar__spacer" />
    <!-- SSOT top-right chrome cluster. ONE flex row owns this corner so nothing here can overlap
         another (the old viewport-fixed help fab used to float over — and swallow the taps of — the
         usage chip). Order: version · #dw-topbar-right outlet (? ⌨) · 刷新 · 远端 —— 与 pro 顶栏
         逐枚同序（Human 2026-07-29 定：两壳顶右簇必须长一样）。
         #dw-topbar-right mirrors pro's MainLayout outlet: ANY top-right widget (help, future chrome)
         teleports into it and lands in-row, gapped — never a new fixed corner. -->
    <div class="cli-tab-bar__chrome">
      <!-- Build version — 连按钮带「关于」面板整个封装在共享组件里（pro 顶栏用的是同一枚），
           所以这里没有任何版本相关的判定或文案。 -->
      <VersionBadge app-name="deepwork-terminal" data-testid="cli-portal-version" />
      <!-- Teleport outlet for viewport-agnostic top-right chrome (HelpCenter 的 "?"、快捷键指引 ⌨).
           位置在刷新/远端两枚之前 —— 与 pro 顶栏同序（? ⌨ ⟳ ▤）。这两组的性质不同：teleport 进来的
           是"帮助/指引"类入口，后面两枚是"动作"类按钮，指引在前、动作在后，两壳一致。 -->
      <span :id="TOPBAR_RIGHT_OUTLET_ID" class="cli-tab-bar__outlet" data-testid="topbar-outlet-right" />
      <!-- Manual force-refresh: always available (not just PWA — a plain F5/reload can still
           leave a long-lived tab on a stale build behind a cache/tunnel/proxy, and the
           auto-update pill only appears once its poll notices; this gives an explicit escape
           hatch the user can tap right away). Force-fresh via /fresh (bypasses any stale
           cached index.html). -->
      <button
        class="cli-tab-bar__refresh"
        type="button"
        data-testid="cli-portal-refresh"
        title="强制刷新到最新版本"
        @click="applyUpdate"
      ><RefreshCw :size="14" /></button>
      <!-- D5: 远程终端 — split out of the "+" menu into its own small icon (minority path). -->
      <button
        class="cli-tab-bar__refresh"
        type="button"
        data-testid="cli-portal-add-remote"
        title="新建远程终端"
        @click="emit('add-remote')"
      ><Server :size="14" /></button>
      <!-- Host-provided status widget (standalone mounts the UsageChip here via CliPortal). -->
      <slot name="status" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RefreshCw, Server, LayoutGrid } from 'lucide-vue-next'
import {
  agentSaidText,
  STATUS_COLOR,
  STATUS_MOTION,
  URGENCY_ORDER,
  type EffectiveStatus,
} from '@terminal/composables/cli/useAgentOverview'
import { useAgentSignals } from '@terminal/composables/cli/useAgentSignals'
import { sessionEntry } from '@terminal/composables/cli/useSessionsOverview'
import {
  LIVENESS_COLOR,
  LIVENESS_LABEL,
  type TabLiveness,
  type TabNotLive,
} from '@terminal/composables/cli/tabLiveness'
import type { WorkbenchGroup, WorkbenchTab } from '@terminal/types/workbench'
import { useAppUpdate } from '@terminal/composables/cli/useAppUpdate'
import { TAB_CLOSE_OPACITY } from '@terminal/composables/cli/tabChrome'
import { displayTabName } from '@terminal/composables/cli/useTabDisplayName'
import VersionBadge from '@terminal/components/chrome/VersionBadge.vue'
import { TOPBAR_RIGHT_OUTLET_ID } from '@terminal/composables/cli/useTopbarOutlet'

// Auto-update detection: surfaces the "有更新" pill when a newer build is deployed, and
// owns the shared clear-and-reload used by the pill + PWA refresh button + HelpCenter.
const { updateAvailable, applyUpdate } = useAppUpdate()

const props = defineProps<{
  groups: WorkbenchGroup[]
  activeTabId: string | undefined
  showGroupHeaders: boolean
  renamingTabId: string | null
  renameValue: string
  /** D4: 1-based visible position per tab id — drives the "终端N" live-position label. */
  tabPositions: Map<string, number>
  /** tabId → seen-aware status, from the SAME useOverviewUnits instance the overview grid reads.
   *  Absent id = no agent = no dot (an empty shell has no status worth a colour). */
  tabStatuses: Map<string, EffectiveStatus>
  /** tabId → 存活态（进程还在不在）。缺条目 = live。和 tabStatuses 是两个轴，不是两种说法：
   *  进程没了的时候 agent 状态无从谈起，所以存活态优先渲染。 */
  tabLiveness?: Map<string, TabLiveness>
  /** 刚被自动重开过、且用户还没在里面输入过的标签。是「留痕」的标签栏那一半（另一半是终端里
   *  那行说明）——没有它，自动重开就退化成它要取代的那次静默重建。 */
  reopenedTabs?: Set<string>
  /** Global status counts for the roll-up capsule (same source, same numbers as the overview). */
  rollup: Record<EffectiveStatus, number>
}>()

const emit = defineEmits<{
  (e: 'switch', tabId: string): void
  (e: 'close', tabId: string): void
  (e: 'add'): void
  (e: 'add-remote'): void
  (e: 'rename-start', tabId: string): void
  (e: 'rename-input', value: string): void
  (e: 'rename-commit'): void
  (e: 'rename-cancel'): void
  (e: 'toggle-group', groupId: string): void
  (e: 'toggle-overview'): void
  /** Right-click on a tab. The host owns the menu (it knows the live cwd + the tab set), so the
   *  bar only reports WHERE and ON WHAT — it never decides what the menu contains. */
  (e: 'context-menu', event: MouseEvent, tabId: string): void
}>()


// The force-fresh clear-and-reload lives in useAppUpdate.applyAppUpdate() now (SSOT for the
// manual refresh button, the auto-update pill, and HelpCenter's manual entry).

/** '' when this tab has no agent — an idle shell renders NO dot, exactly like the overview grid
 *  and the pane bar (idle is deliberately absent from STATUS_COLOR for this reason). */
function tabStatus(tabId: string): EffectiveStatus | '' {
  const s = props.tabStatuses.get(tabId)
  return s && s !== 'idle' ? s : ''
}

/** null = 这个标签背后还有活着的进程（默认）。有值就一定要说出来。 */
function notLive(tabId: string): TabNotLive | null {
  const l = props.tabLiveness?.get(tabId)
  return l && l !== 'live' ? l : null
}

/** 这个标签现在挂的是自动重开出来的新 shell，且用户还没动过手。 */
function reopened(tabId: string): boolean {
  return props.reopenedTabs?.has(tabId) ?? false
}

// 显式信号（BEL / OSC 9·777·99）：唯一带着 agent 原话的那条帧。标签上只有一枚点，容不下一句话，
// 所以原话落在 hover 提示里——点告诉你"哪个终端在喊"，提示告诉你"它喊的是什么"。
const { signalFor } = useAgentSignals()

/**
 * 标签 hover 提示 = 存活态 · agent 原话。
 *
 * 两者都没有时返回 undefined（不挂空 title，浏览器就不弹提示，与改动前逐字一致）。原话的措辞取自
 * agentSaidText（和总览卡片同一个 SSOT），说话人取自 sessions_overview 帧里检测到的引擎，所以同一个
 * 终端在卡片和标签上不会被叫成两个名字；裸 BEL 没有正文，那时这里什么也不加。
 */
function tabTitle(tab: WorkbenchTab): string | undefined {
  const parts: string[] = []
  const l = notLive(tab.id)
  if (l) parts.push(LIVENESS_LABEL[l])
  // 小标只有两个字，放不下「为什么」——完整那句话在这里补上（终端里也写了同样一句）。
  if (reopened(tab.id)) parts.push('上一个进程已随服务重启结束，这是一个新的 shell')
  const said = agentSaidText(signalFor(tab.sessionId), sessionEntry(tab.sessionId)?.agentTool)
  if (said) parts.push(said)
  return parts.length ? parts.join(' · ') : undefined
}

/** Roll-up segments, most-urgent first, zero counts dropped. Ordering comes from URGENCY_ORDER
 *  (the same constant the overview groups iterate), so the capsule can't disagree with the grid. */
const ROLLUP_ICON: Record<Exclude<EffectiveStatus, 'idle'>, string> = {
  waiting: '◉', running: '●', 'done-unseen': '✓',
}
const rollupSegs = computed(() =>
  URGENCY_ORDER
    .filter((s): s is Exclude<EffectiveStatus, 'idle'> => s !== 'idle')
    .map((status) => ({ status, icon: ROLLUP_ICON[status], count: props.rollup?.[status] ?? 0 }))
    .filter((s) => s.count > 0),
)
</script>

<style scoped>
/* Overview trigger — leads the strip, separated from the first tab by a hairline so it reads as
   chrome ("all of them") rather than as a tab. flex-shrink:0 keeps it pinned while tabs scroll. */
.cli-tab-bar__overview {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0;
  min-width: 34px;
  padding: 0 9px;
  flex-shrink: 0;
  background: transparent;
  border: none;
  border-right: 1px solid hsl(var(--border));
  color: hsl(var(--muted-foreground));
  cursor: pointer;
}
.cli-tab-bar__overview:hover { color: hsl(var(--foreground)); background: hsl(var(--accent)); }
.cli-tab-bar__overview:active { transform: scale(0.92); }

/* PWA-only refresh button — sits on the right, mirrors the add-tab affordance. */
.cli-tab-bar__refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  flex-shrink: 0;
  background: transparent;
  border: none;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
}
.cli-tab-bar__refresh:hover { color: hsl(var(--foreground)); }
.cli-tab-bar__refresh:active { transform: scale(0.92); }

/* Auto-update pill — rare, so it should catch the eye when it appears (soft slide-in). */
.cli-tab-bar__update {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  margin-left: 6px;
  padding: 3px 10px;
  border: none;
  border-radius: 999px;
  background: hsl(var(--accent));
  color: hsl(var(--foreground));
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  white-space: nowrap;
  cursor: pointer;
  animation: cli-update-in 0.24s ease;
}
.cli-tab-bar__update:hover { background: hsl(var(--accent) / 0.8); }
.cli-tab-bar__update:active { transform: scale(0.96); }
@keyframes cli-update-in {
  from { opacity: 0; transform: translateY(-3px); }
  to { opacity: 1; transform: none; }
}

.cli-tab-bar {
  display: flex;
  align-items: stretch;
  height: 36px;
  background: hsl(var(--card));
  border-bottom: 1px solid hsl(var(--border));
  overflow-x: auto;
  overflow-y: hidden;
  flex-shrink: 0;
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.cli-tab-bar::-webkit-scrollbar { display: none; }

/* Group header */
.cli-tab-bar__group-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 10px;
  font-size: 0.7rem;
  color: hsl(var(--muted-foreground));
  border-left: 3px solid var(--group-color, #4a9eff);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  user-select: none;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.cli-tab-bar__group-header:hover {
  background: hsl(var(--accent));
}
.group-chevron { font-size: 0.7rem; }

/* Tab */
.cli-tab-bar__tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 12px;
  min-width: 80px;
  max-width: 200px;
  height: 36px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: hsl(var(--muted-foreground));
  font-size: 0.8125rem;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  flex-shrink: 0;
  transition: background 0.12s, color 0.12s;
  outline: none;
}
.cli-tab-bar__tab:hover {
  background: hsl(var(--accent));
  color: hsl(var(--foreground));
}
.cli-tab-bar__tab.is-active {
  border-bottom-color: #4a9eff;
  color: hsl(var(--foreground));
  background: hsl(var(--accent));
}
/* Pulse border on agent needs-input */
.cli-tab-bar__tab.needs-input {
  border-bottom-color: #ff9800;
  animation: tab-needs-input 1.5s infinite;
}
@keyframes tab-needs-input {
  0%, 100% { border-bottom-color: #ff9800; }
  50%       { border-bottom-color: rgba(255,152,0,0.3); }
}

.tab-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tab-rename-input {
  flex: 1;
  min-width: 40px;
  max-width: 120px;
  height: 22px;
  border: 1px solid hsl(var(--border));
  border-radius: 3px;
  background: hsl(var(--background));
  color: inherit;
  font: inherit;
  padding: 0 4px;
  outline: none;
}

/* Close ✕ —— 常驻可见，不再靠 hover 揭示。取值与理由都在共享的 TAB_CLOSE_OPACITY，
   pro 的 TopTabBar 用同一份：hover 揭示会让 iOS 把切标签的第一次点击吃掉。 */
.tab-close {
  font-size: 1rem;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
  border-radius: 3px;
  opacity: v-bind('TAB_CLOSE_OPACITY.idle');
  margin-left: auto;
  color: hsl(var(--muted-foreground));
  transition: opacity 0.1s;
}
.tab-close:hover {
  opacity: v-bind('TAB_CLOSE_OPACITY.hover');
  color: #ff6b6b;
  background: rgba(255,255,255,0.12);
}

/* Agent dot — colours + rhythms bound from the SSOT constants, never typed as hex here.
   Fourth consumer of the same pair (pane bar / status sheet / overview grid are the others). */
.cli-tab-bar {
  --status-waiting: v-bind('STATUS_COLOR.waiting');
  --status-running: v-bind('STATUS_COLOR.running');
  --status-done: v-bind("STATUS_COLOR['done-unseen']");
  --dot-waiting-duration: v-bind('STATUS_MOTION.waiting.duration');
  --dot-waiting-easing: v-bind('STATUS_MOTION.waiting.easing');
  --dot-waiting-min: v-bind('STATUS_MOTION.waiting.minOpacity');
  --dot-running-duration: v-bind('STATUS_MOTION.running.duration');
  --dot-running-easing: v-bind('STATUS_MOTION.running.easing');
  --dot-running-min: v-bind('STATUS_MOTION.running.minOpacity');
  /* 第二个轴（存活）的两枚中性灰，同样从 SSOT 绑来，不在这里敲 hex。 */
  --liveness-detached: v-bind('LIVENESS_COLOR.detached');
  --liveness-unreachable: v-bind('LIVENESS_COLOR.unreachable');
}
.tab-agent-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}
.tab-agent-dot.s-waiting {
  background: var(--status-waiting);
  --dot-min-opacity: var(--dot-waiting-min);
  animation: status-dot-pulse var(--dot-waiting-duration) var(--dot-waiting-easing) infinite;
}
.tab-agent-dot.s-running {
  background: var(--status-running);
  --dot-min-opacity: var(--dot-running-min);
  animation: status-dot-pulse var(--dot-running-duration) var(--dot-running-easing) infinite;
}
.tab-agent-dot.s-done-unseen { background: var(--status-done); }  /* STATIC by contract */
/* 存活态点：中性灰、静止（LIVENESS_MOTION 两档都是 null——脉冲表达的是"我还活着"）。 */
.tab-agent-dot.l-detached { background: var(--liveness-detached); }
.tab-agent-dot.l-unreachable { background: var(--liveness-unreachable); }
/* 名字压暗：标签还在（用户的工作上下文没丢），但它不代表一个活着的终端了。 */
.cli-tab-bar__tab.not-live .tab-name { opacity: 0.62; }
/* 「已重开」小标：中性描边、无动效。它报告的是一件**已经做完**的事，不是要人处理的事，
   所以不配色、不脉冲——那些是 waiting/running 的语言。 */
.tab-reopened {
  flex-shrink: 0;
  padding: 0 4px;
  border: 1px solid hsl(var(--border));
  border-radius: 3px;
  font-size: 0.6rem;
  line-height: 14px;
  color: hsl(var(--muted-foreground));
  white-space: nowrap;
}
@keyframes status-dot-pulse {
  0%, 100% { opacity: var(--dot-min-opacity); }
  50% { opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .tab-agent-dot { animation: none !important; opacity: 1; }
}

/* Roll-up capsule inside the overview toggle. */
.cli-tab-bar__rollup {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: 5px;
  font-size: 0.66rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}
.ctb-seg { display: inline-flex; align-items: center; gap: 1px; }
.ctb-seg.s-waiting { color: var(--status-waiting); }
.ctb-seg.s-running { color: var(--status-running); }
.ctb-seg.s-done-unseen { color: var(--status-done); }

/* Add button */
.cli-tab-bar__tab--add {
  min-width: 36px;
  max-width: 36px;
  font-size: 1.1rem;
  color: hsl(var(--muted-foreground));
  flex-shrink: 0;
  padding: 0;
  justify-content: center;
}
.cli-tab-bar__tab--add:hover { color: rgba(255,255,255,0.8); }

/* Usage chip slot — trails the tabs (contextual), spaced from the + button. */
.cli-tab-bar__tab-trailing {
  display: inline-flex;
  align-items: center;
  margin-left: 6px;
  flex-shrink: 0;
}

/* Spacer */
.cli-tab-bar__spacer { flex: 1; min-width: 8px; }

/* SSOT top-right chrome cluster — ONE flex row, evenly gapped, so version / usage chip / the
   #dw-topbar-right outlet (help "?" etc.) sit side-by-side and can never overlap or steal each
   other's taps. Replaces the era of independently viewport-fixed corner widgets. */
.cli-tab-bar__chrome {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  padding-right: 10px;
}
/* Outlet: an inline flex box (not a fixed corner) that hosts teleported top-right chrome, so
   multiple widgets stack in-row with a shared gap instead of layering on top of each other. */
.cli-tab-bar__outlet {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}


</style>
