<template>
  <!-- Agent 概览的卡片渲染层（纯展示）。状态派生全在 useAgentOverview；本组件只吃
       已分组/已排序的 groups + 全局 rollup 计数，把窗口画成可点的卡片。点卡 → emit
       select(w.index)，父组件负责切窗口。
       PC = 注意力加权（方向:混合·status 主）：活跃(等你/运行/完成待看)= 大卡，一行 ≤3 张
       （列数见 overviewColumns）、卡片定高、纵向滚动，status 定视觉权重、边框高亮；空闲收成
       一条 chip 条（相对去突出），除非它们是唯一内容——那时空闲就是全部，平等进同一张网格。
       窗口拖窄 → 同一张网格自己少排一列（≤1000px 起按卡宽下限掉列，最窄单列），不是另一档布局。
       标题一律走 overviewCardTitle（自定义名 → cwd basename → 终端N），卡片/chip/编号点同名。
       tmux 与非 tmux 是同一份数据形状(OverviewUnit)，因此走的是**同一套几何**，不再分档。
       移动 = 单列 + 每组 sticky 分组头（不变）。配色对齐 TmuxPaneBar。 -->
  <div class="agent-overview" :class="{ 'is-mobile': isMobile, 'is-pc': !isMobile, 'is-fill': !isMobile && showBigGrid }" data-testid="agent-overview">
    <!-- 顶部状态条：左＝按显示位置排的编号点（每个终端一颗，颜色=状态，当前的描边），
         右＝roll-up 计数。编号条是 tmux pane bar 在非 tmux 侧的对等物：不读卡片就能看出
         「几号在等你、几号在跑」，且编号与标签栏的「终端N」和 前缀+N 快捷键是同一套。
         点编号 = 直接切过去（和点卡片同一动作，只是更快）。 -->
    <div v-if="pills.length > 1 || rollupSegments.length" class="ao-topbar">
      <div v-if="pills.length > 1" class="ao-pills" data-testid="overview-pills">
        <button
          v-for="p in pills"
          :key="p.index"
          type="button"
          class="ao-pill"
          :class="[`s-${p.status}`, { 'is-here': p.active }]"
          :title="`${p.index} ${p.title} · ${p.label}`"
          :data-testid="`overview-pill-${p.index}`"
          @click="emit('select', p.index)"
        >
          <span class="ao-pill-dot" />{{ p.index }}
        </button>
      </div>

      <!-- roll-up 摘要：单行「◉N 等你 · ●N 运行 · ✓N 完成」。只显计数>0 的段；
           只给最紧急的非零段着色，其余保持 dim；全为 0 时整段不渲染。 -->
      <div v-if="rollupSegments.length" class="ao-rollup" data-testid="overview-rollup">
        <template v-for="(seg, i) in rollupSegments" :key="seg.status">
          <span v-if="i > 0" class="ao-rollup-sep">·</span>
          <span class="ao-rollup-seg" :class="[`s-${seg.status}`, { 'is-hot': seg.colored }]">
            <span class="ao-rollup-icon">{{ seg.icon }}</span>{{ seg.count }} {{ seg.label }}
          </span>
        </template>
      </div>
    </div>

    <!-- ── 移动：单列分组列表（每组 sticky 头） ─────────────────────── -->
    <div v-if="isMobile" class="ao-cards">
      <div v-for="g in groups" :key="g.status" class="ao-group">
        <div class="ao-group-head" :class="`s-${g.status}`">
          <span class="ao-group-dot" />{{ statusLabel(g.status) }}
          <span class="ao-group-count">{{ g.units.length }}</span>
        </div>
        <button
          v-for="w in g.units"
          :key="w.index"
          class="ao-card"
          :class="`s-${g.status}`"
          type="button"
          :data-testid="`overview-card-${w.index}`"
          @click="emit('select', w.index)"
        >
          <div class="ao-card-head">
            <span class="ao-idx">{{ w.index }}</span>
            <span class="ao-card-name">{{ overviewCardTitle(w) }}</span>
            <span class="ao-card-badge" :class="unitBadge(w, g.status).cls">{{ unitBadge(w, g.status).text }}</span>
            <span v-if="w.tool" class="ao-card-tool">{{ w.tool }}</span>
          </div>
          <!-- agent 的原话（显式 BEL/OSC 信号才有）。排在徽章之下、预览之上：它是这张卡上唯一
               不是我们推断的一行，值得先看到。没有信号或裸铃时整行不渲染（见 agentSaidText）。 -->
          <div v-if="w.agentSaid" class="ao-card-said">{{ w.agentSaid }}</div>
          <div v-if="agentSummary(w)" class="ao-card-agents">{{ agentSummary(w) }}</div>
          <!-- 手机上 2 行几乎读不出「它在干嘛」（常常刚好是空行 + 半句话）。5 行是一屏里
               还能同时看到 2-3 张卡、又足够认出内容的折中。 -->
          <div v-if="tailLines(w, 5).length" class="ao-card-tail">
            <div v-for="(line, li) in tailLines(w, 5)" :key="li" class="ao-card-tail-line">{{ line || ' ' }}</div>
          </div>
          <div v-if="w.cwd" class="ao-card-cwd">{{ w.cwd }}</div>
        </button>
      </div>
    </div>

    <!-- ── PC ───────────────────────────────────────────────────────
         唯一的大卡网格（一行 ≤3 张、卡片定高、超出纵向滚动）。上卡的是活跃单元；空闲收成下面
         那条 chip 条，只有当**没有任何活跃单元**时空闲才平等进这张网格（它们是唯一内容，压成
         薄薄一条 chip 就等于整页什么也没有）。全程一套网格规则，tmux 与非 tmux 不分叉。 -->
    <template v-else>
      <template v-if="showBigGrid">
        <div
          class="ao-active"
          :style="gridStyle"
          data-testid="overview-active"
        >
          <button
            v-for="a in bigCards"
            :key="a.w.index"
            class="ao-card ao-card--big"
            :class="`s-${a.status}`"
            type="button"
            :data-testid="`overview-card-${a.w.index}`"
            @click="emit('select', a.w.index)"
          >
            <div class="ao-card-head">
              <span class="ao-idx">{{ a.w.index }}</span>
              <span class="ao-card-name">{{ overviewCardTitle(a.w) }}</span>
              <span class="ao-card-badge" :class="unitBadge(a.w, a.status).cls">{{ unitBadge(a.w, a.status).text }}</span>
              <span v-if="a.w.tool" class="ao-card-tool">{{ a.w.tool }}</span>
            </div>
            <div v-if="a.w.agentSaid" class="ao-card-said">{{ a.w.agentSaid }}</div>
            <div v-if="agentSummary(a.w)" class="ao-card-agents">{{ agentSummary(a.w) }}</div>
            <div v-if="tailLines(a.w).length" class="ao-card-tail">
              <div v-for="(line, li) in tailLines(a.w)" :key="li" class="ao-card-tail-line">{{ line || ' ' }}</div>
            </div>
            <div v-else class="ao-card-tail ao-card-tail--empty">（无最近输出）</div>
            <div v-if="a.w.cwd" class="ao-card-cwd">{{ a.w.cwd }}</div>
          </button>
        </div>

        <!-- 空闲：有活跃单元时才收成一条 chip 条（相对去突出、可折叠）；否则它们已在上面的网格里 -->
        <div v-if="!idleAsCards && idleWindows.length" class="ao-idle" data-testid="overview-idle">
          <button
            class="ao-idle-toggle"
            type="button"
            :aria-expanded="idleOpen"
            data-testid="overview-idle-toggle"
            @click="idleOpen = !idleOpen"
          >
            <span class="ao-idle-dot" />{{ idleWindows.length }} 空闲
            <span class="ao-idle-chevron" :class="{ open: idleOpen }">▸</span>
          </button>
          <template v-if="idleOpen">
            <button
              v-for="w in idleWindows"
              :key="w.index"
              class="ao-idle-chip"
              type="button"
              :data-testid="`overview-card-${w.index}`"
              @click="emit('select', w.index)"
            >
              <span class="ao-idx">{{ w.index }}</span>
              <span class="ao-idle-name">{{ overviewCardTitle(w) }}</span>
              <!-- 已经没有进程的终端不能只以一枚普通 chip 混在「空闲」里：空闲=活着但没在忙。 -->
              <span v-if="w.liveness" class="ao-card-badge s-notlive">{{ unitBadge(w, 'idle').text }}</span>
              <span v-if="w.cwd" class="ao-idle-cwd">{{ w.cwd }}</span>
            </button>
          </template>
        </div>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  overviewCardTitle,
  overviewColumns,
  STATUS_COLOR,
  STATUS_MOTION,
  type EffectiveStatus,
  type OverviewGroup,
  type OverviewUnit,
} from '@terminal/composables/cli/useAgentOverview'
// 第二个轴：这张卡背后还有没有活着的进程。词与色都取自 SSOT，不在本文件另造。
import { LIVENESS_COLOR, LIVENESS_LABEL } from '@terminal/composables/cli/tabLiveness'

const props = defineProps<{
  /** 已按状态分组 + 紧急度排序好的单元（tmux window 或非 tmux session，同一形状）。 */
  groups: OverviewGroup[]
  /** 全局各状态计数（供 roll-up 行）。 */
  rollup: Record<EffectiveStatus, number>
  /** 窄屏 → 单列 + sticky 分组头。 */
  isMobile: boolean
}>()

const emit = defineEmits<{
  (e: 'select', index: number): void
  /** Escape pressed. Both shells render this component under `v-if="overviewOpen"`, so the
   *  listener's lifetime is exactly the overlay's — no watch, no leak, and the two hosts get the
   *  same exit affordance instead of each remembering to wire one (only the scrim click existed,
   *  which is undiscoverable and impossible on a keyboard). */
  (e: 'close'): void
}>()

function onKeyDown(e: KeyboardEvent): void {
  if (e.key === 'Escape') {
    e.stopPropagation()
    emit('close')
  }
}
onMounted(() => { document.addEventListener('keydown', onKeyDown, true) })
onBeforeUnmount(() => { document.removeEventListener('keydown', onKeyDown, true) })

/** 状态徽章/分组头文案。 */
const STATUS_LABEL: Record<EffectiveStatus, string> = {
  waiting: '等你输入',
  running: '运行中',
  'done-unseen': '完成待查看',
  idle: '空闲',
}
function statusLabel(s: EffectiveStatus): string {
  return STATUS_LABEL[s]
}

/**
 * 卡片徽章：先看**还活着吗**，再看 agent 在干嘛。
 *
 * 一个进程已经结束的终端如果照 EffectiveStatus 写成「空闲」，说的就是半真话——空闲的前提是它还
 * 活着、只是没在忙。存活态一旦为真（只有宿主补出来的无 session 卡片才带），徽章就换成中性灰的
 * 「进程已结束 / 联系不上」，不参与紧急度、不脉冲。
 */
function unitBadge(w: OverviewUnit, status: EffectiveStatus): { text: string; cls: string } {
  if (w.liveness) return { text: LIVENESS_LABEL[w.liveness], cls: 's-notlive' }
  return { text: statusLabel(status), cls: `s-${status}` }
}

/** Only a multi-agent unit needs an attribution line; a single-agent card already has a tool
 *  badge. (tmux: a split window's panes; cli: always a single agent, so this stays empty.) */
function agentSummary(w: OverviewUnit): string {
  return w.signals.length > 1 ? w.signals.join(' · ') : ''
}

/** roll-up 段定义 —— 顺序即紧急度（idle 不进摘要行）。 */
const ROLLUP_DEFS: { status: EffectiveStatus; icon: string; label: string }[] = [
  { status: 'waiting', icon: '◉', label: '等你' },
  { status: 'running', icon: '●', label: '运行' },
  { status: 'done-unseen', icon: '✓', label: '完成' },
]

/** 过滤计数为 0 的段；剩下第一个（最紧急）着色，其余 dim。 */
const rollupSegments = computed(() =>
  ROLLUP_DEFS.map((d) => ({ ...d, count: props.rollup[d.status] ?? 0 }))
    .filter((s) => s.count > 0)
    .map((s, i) => ({ ...s, colored: i === 0 })),
)

/**
 * 顶部编号点条：**按显示位置**（w.index）排全部单元，不按紧急度分组。
 *
 * 这跟下面的卡片区刻意用两种排序，是因为它们回答两个不同问题：卡片区回答「先看谁」（紧急度
 * 优先），编号条回答「几号是什么状态」——后者只有在顺序和标签栏/快捷键一致时才成立，一旦
 * 按状态重排，编号条就变成需要逐个读的列表，失去"扫一眼"的价值。
 */
interface PillItem { index: number; status: EffectiveStatus; title: string; active: boolean; label: string }
const pills = computed<PillItem[]>(() =>
  props.groups
    .flatMap((g) => g.units.map((w) => ({
      index: w.index,
      status: g.status,
      title: overviewCardTitle(w),
      active: w.active,
      // 没活着的终端在 tooltip 里也不许被写成「空闲」——和卡片徽章同一个判断（unitBadge）。
      label: unitBadge(w, g.status).text,
    })))
    .sort((a, b) => a.index - b.index),
)

/** PC：活跃窗口（等你/运行/完成待看）展平成一列，保留 groups 的紧急度序，各自带 status。 */
interface ActiveCard { w: OverviewUnit; status: EffectiveStatus }
const activeCards = computed<ActiveCard[]>(() =>
  props.groups
    .filter((g) => g.status !== 'idle')
    .flatMap((g) => g.units.map((w) => ({ w, status: g.status }))),
)

/** PC：空闲窗口。 */
const idleWindows = computed<OverviewUnit[]>(
  () => props.groups.find((g) => g.status === 'idle')?.units ?? [],
)

/** 空闲要不要也当大卡：只有在它们是**唯一内容**时（一个活跃单元都没有）。
 *  这不是"卡少就换一套排法"的档位——它跟单元总数无关，只跟"这一页上还有没有别的东西值得
 *  相对突出"有关：有活跃卡时空闲收成 chip 条是减噪，没有活跃卡时收成 chip 条等于整页空白。 */
const idleAsCards = computed(() => activeCards.value.length === 0)

/** PC 进大卡网格的单元。直接展平 groups，所以顺序仍是紧急度序、idle 天然排最后。 */
const bigCards = computed<ActiveCard[]>(() =>
  props.groups
    .filter((g) => g.status !== 'idle' || idleAsCards.value)
    .flatMap((g) => g.units.map((w) => ({ w, status: g.status }))),
)

/** 有没有大卡要画（一个单元都没有时为 false）。 */
const showBigGrid = computed(() => bigCards.value.length > 0)

/** 网格唯一的布局输入：列数。卡宽/卡高一律由 CSS 定死，不随卡片数量变——放大卡片来填满空白
 *  正是上一版被实测判死的做法。 */
const gridStyle = computed<Record<string, string>>(() => ({
  '--cols': String(overviewColumns(bigCards.value.length)),
}))

/** 空闲条默认展开（chip 本就轻）；空闲多时可点折叠成一行。 */
const idleOpen = ref(true)

/** 卡片 live tail：去掉尾部空行后取末 `limit` 行；不传 limit = 全给（大卡靠 CSS 底对齐+裁剪
 *  填满卡高，行数随卡高由 CSS 决定，非这里写死）。移动卡传 2、空闲卡传 8。全空则空数组。
 *  上游行数上界由后端 overviewTailLines 决定（SSOT），这里只做展示层裁剪。 */
function tailLines(w: OverviewUnit, limit?: number): string[] {
  const lines = (w.tail ?? []).slice()
  while (lines.length && lines[lines.length - 1].trim() === '') lines.pop()
  return limit != null ? lines.slice(-limit) : lines
}
</script>

<style scoped>
.agent-overview {
  /* The ONE place these three colors enter CSS — bound live from STATUS_COLOR
     (useAgentOverview.ts), the same constant TmuxStatusSheet.vue binds inline and
     TmuxPaneBar.vue mirrors. Every s-waiting/s-running/s-done-unseen rule below reads
     these vars (solid or color-mix()'d for a tint) instead of a hand-typed hex/rgba —
     they cannot drift from each other or from the other two consumers again. This
     replaced a real bug: .ao-card--big.s-done-unseen's border/glow was teal
     (rgb(45,212,191)) while every other done-unseen indicator in this file was amber
     — an rgba literal that had drifted from its own siblings. */
  --status-waiting: v-bind('STATUS_COLOR.waiting');
  --status-running: v-bind('STATUS_COLOR.running');
  --status-done: v-bind("STATUS_COLOR['done-unseen']");
  /* Per-status dot motion — bound live from STATUS_MOTION (useAgentOverview.ts), the same
     constant TmuxPaneBar.vue and TmuxStatusSheet.vue bind, so no rhythm can drift between the
     three dot-rendering consumers. `done-unseen` is STATUS_MOTION's documented `null` (static)
     and therefore has no vars here — that absence IS the contract. */
  --dot-waiting-duration: v-bind('STATUS_MOTION.waiting.duration');
  --dot-waiting-easing: v-bind('STATUS_MOTION.waiting.easing');
  --dot-waiting-min: v-bind('STATUS_MOTION.waiting.minOpacity');
  --dot-running-duration: v-bind('STATUS_MOTION.running.duration');
  --dot-running-easing: v-bind('STATUS_MOTION.running.easing');
  --dot-running-min: v-bind('STATUS_MOTION.running.minOpacity');
  padding: 12px;
  color: #d8c8ee;
  background: #16121f;
  box-sizing: border-box;
}
.is-pc.agent-overview { padding: 16px; }

/* 画大卡时（PC）撑满 overlay（.terminal-overview-overlay inset:0 全高）：
   rollup 顶 + 卡片区 flex 吃满剩余 + 空闲条钉底。卡高由 .ao-active 的
   grid-auto-rows: minmax(300px,1fr) 决定 —— 空间足则 1fr 铺满、卡多到 1fr<保底则取
   300px 保可读、总高超一屏由 overlay 现成的 overflow-y:auto 滚动（Q1 长高优先·溢出滚）。 */
.agent-overview.is-fill {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}
.agent-overview.is-fill .ao-active {
  flex: 1 1 auto;
  min-height: 0;
}
.agent-overview.is-fill .ao-idle {
  flex: 0 0 auto;
}

/* ── 顶部状态条：编号点条（左，可换行）+ roll-up 计数（右，钉住不换行） ── */
.ao-topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin-bottom: 12px;
  padding: 4px 2px;
}
.is-pc .ao-topbar { margin-bottom: 14px; }

.ao-pills { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; min-width: 0; }
.ao-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px 2px 6px;
  border-radius: 999px;
  border: 1px solid #3a2860;
  background: #1a1526;
  color: #a693c2;
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.7rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.6;
  cursor: pointer;
  transition: background 0.1s, border-color 0.1s;
}
.ao-pill:hover { background: #271a3f; border-color: #4a3570; }
.ao-pill:active { transform: translateY(1px); }
/* "你正在看的那个" — 用描边而非填色：填色会和状态色抢同一个通道，描边是正交的第二维度。 */
.ao-pill.is-here { border-color: #b08fd0; color: #f0e0ff; }

.ao-pill-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; background: #6f5a90; }
/* 同一套 STATUS_COLOR + STATUS_MOTION，和分组头点、卡片徽章点、tmux pane bar 完全一致 —
   这是第四个消费者，仍然零新颜色/零新节奏。 */
.ao-pill.s-waiting .ao-pill-dot {
  background: var(--status-waiting);
  --dot-min-opacity: var(--dot-waiting-min);
  animation: status-dot-pulse var(--dot-waiting-duration) var(--dot-waiting-easing) infinite;
}
.ao-pill.s-running .ao-pill-dot {
  background: var(--status-running);
  --dot-min-opacity: var(--dot-running-min);
  animation: status-dot-pulse var(--dot-running-duration) var(--dot-running-easing) infinite;
}
.ao-pill.s-done-unseen .ao-pill-dot { background: var(--status-done); }  /* STATIC by contract */
.ao-pill.s-waiting { border-color: color-mix(in srgb, var(--status-waiting) 45%, transparent); }
@media (prefers-reduced-motion: reduce) {
  .ao-pill.s-waiting .ao-pill-dot,
  .ao-pill.s-running .ao-pill-dot { animation: none; opacity: 1; }
}

/* ── roll-up 摘要 ─────────────────────────────────────────────── */
.ao-rollup {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  margin-left: auto;
  font-size: 0.74rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.is-pc .ao-rollup { font-size: 0.82rem; }
.ao-rollup-seg {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: #6f5a90; /* 默认 dim；只有 .is-hot 段取状态色 */
}
.ao-rollup-icon { font-size: 0.72em; line-height: 1; }
.ao-rollup-sep { color: #3a2860; }
.ao-rollup-seg.is-hot.s-waiting { color: var(--status-waiting); }
.ao-rollup-seg.is-hot.s-running { color: var(--status-running); }
.ao-rollup-seg.is-hot.s-done-unseen { color: var(--status-done); }

/* ── PC：唯一的大卡网格（每行 ≤3、卡片定高、超出纵向滚动） ──
   卡片数量只决定**用几列**（overviewColumns），永远不决定卡片多大：没有第二套"卡少时换个排法"
   的规则，tmux 与非 tmux 命中的是逐字相同的这几条声明。 */
.ao-active {
  display: grid;
  grid-template-columns: repeat(var(--cols, 3), minmax(0, 1fr));
  /* 卡高 SSOT。保底值不是拍脑袋：后端每张卡固定发 sessionTailLines=8 行
     （sessions_overview.go / tmux overviewTailLines），大卡 tail 是 0.72rem×1.55 ≈ 18px/行，
     8 行 ≈ 143px，加 tail 内边距 18 + 头部 22 + cwd 16 + 间距/卡内边距 ≈ 100 → 约 300px 才
     刚好把「发过来的东西全看见」。原来的 208px 会把一半行数裁掉——预览框显得太矮的直接原因，
     而且是纯浪费：那些行已经在 payload 里了。1fr 让空间富裕时继续长高。 */
  grid-auto-rows: minmax(300px, 1fr);
  gap: 14px;
  align-items: stretch;
  margin-bottom: 14px;
}

/* 窄窗口（把桌面浏览器拖窄——非触摸设备走的仍是这条 PC 分支）：列数改由「一张卡至少 320px
   才读得下」决定。auto-fit 会把放不下的列掉到下一行、并折叠空轨道，所以卡少时也不会留一条
   幽灵空列；窄到只容得下一张卡时自然就是单列。这不是第二档布局——卡高、间距、卡片本身的样式
   一个字都没变，只是同一张网格在放不下时少排一列。1000px 这个断点接着 3 列的下界：
   (1000 - 2×14)/3 ≈ 324px，与下面的 320px 下限连续，切换时不会跳一下。 */
@media (max-width: 1000px) {
  .ao-active { grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr)); }
}

/* ── 移动：单列分组 ───────────────────────────────────────────── */
.ao-cards { display: block; }
.is-mobile .ao-group { display: flex; flex-direction: column; gap: 8px; }
.is-mobile .ao-group + .ao-group { margin-top: 14px; }

/* 移动 sticky 分组头 */
.ao-group-head {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 2px 5px 12px;
  margin: 0 -12px;
  background: #16121f;
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.3px;
  color: #b08fd0;
}
.ao-group-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
/* The three-state motion contract (STATUS_MOTION doc comment has the full argument): waiting =
   slow/big pulse, running = quick/small one, done-unseen = static. Each rule only remaps its own
   entry onto the single --dot-min-opacity the one shared @keyframes reads. */
.ao-group-head.s-waiting .ao-group-dot {
  background: var(--status-waiting);
  --dot-min-opacity: var(--dot-waiting-min);
  animation: status-dot-pulse var(--dot-waiting-duration) var(--dot-waiting-easing) infinite;
}
.ao-group-head.s-running .ao-group-dot {
  background: var(--status-running);
  --dot-min-opacity: var(--dot-running-min);
  animation: status-dot-pulse var(--dot-running-duration) var(--dot-running-easing) infinite;
}
.ao-group-head.s-done-unseen .ao-group-dot { background: var(--status-done); }         /* STATIC by contract */
.ao-group-head.s-idle .ao-group-dot { background: #7a6a9a; }

@keyframes status-dot-pulse {
  0%, 100% { opacity: var(--dot-min-opacity); }
  50% { opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .ao-group-head.s-waiting .ao-group-dot,
  .ao-group-head.s-running .ao-group-dot { animation: none; opacity: 1; }
}
.ao-group-count { margin-left: auto; color: #6f5a90; font-variant-numeric: tabular-nums; }

/* ── 卡片（基础） ─────────────────────────────────────────────── */
.ao-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
  min-width: 0;
  padding: 9px 11px;
  text-align: left;
  background: #221636;
  border: 1px solid #3a2860;
  border-left: 3px solid #3a2860;
  border-radius: 8px;
  color: #d8c8ee;
  cursor: pointer;
  touch-action: manipulation;
  transition: background 0.1s, border-color 0.1s, transform 0.08s;
}
.ao-card:hover { background: #2a1c44; border-color: #4a3570; }
.ao-card:active { transform: translateY(1px) scale(0.99); background: #301f4e; }
/* 移动/基础：状态左侧色条 */
.ao-card.s-waiting { border-left-color: var(--status-waiting); }
.ao-card.s-running { border-left-color: var(--status-running); }
.ao-card.s-done-unseen { border-left-color: var(--status-done); }
.ao-card.s-idle { border-left-color: #4a3570; }

/* ── PC 大卡：更大气 + 整圈状态边框高亮（重点展示） ───────────── */
.ao-card--big {
  gap: 11px;
  padding: 15px 17px;
  border-left-width: 1px; /* 大卡用整圈边框，不用左色条 */
  border-radius: 11px;
  /* 卡高不在此写死：由 .ao-active grid-auto-rows 统一控（align-items:stretch 拉伸填满行）*/
}
.ao-card--big.s-waiting {
  border-color: var(--status-waiting);
  box-shadow: 0 0 0 1px var(--status-waiting), 0 8px 30px color-mix(in srgb, var(--status-waiting) 16%, transparent);
}
.ao-card--big.s-running {
  border-color: color-mix(in srgb, var(--status-running) 50%, transparent);
  box-shadow: 0 6px 22px color-mix(in srgb, var(--status-running) 13%, transparent);
}
.ao-card--big.s-done-unseen {
  border-color: color-mix(in srgb, var(--status-done) 45%, transparent);
  box-shadow: 0 6px 20px color-mix(in srgb, var(--status-done) 10%, transparent);
}
.ao-card--big:hover { background: #271a3f; }
.ao-card--big .ao-card-name { font-size: 0.98rem; }
/* 大卡 tail 填满卡片剩余高度，内容底对齐（终端惯例：最新在底），超出从顶部裁剪。
   卡越高露越多行，卡矮则只留最近几行 —— 显示行数由高度定，不写死。 */
.ao-card--big .ao-card-tail {
  font-size: 0.72rem;
  line-height: 1.55;
  flex: 1;
  min-height: 0;
  padding: 9px 11px;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
}

.ao-card-head { display: flex; align-items: center; gap: 7px; min-width: 0; }
/* tmux 窗口编号徽章：概览每项恒显 w.index，与 pane bar 编号对齐 —— 对回 tab + 区分同名窗口。 */
.ao-idx {
  flex-shrink: 0;
  min-width: 1.3em;
  padding: 0 4px;
  border-radius: 4px;
  background: #2a1f3a;
  border: 1px solid #3a2860;
  color: #b08fd0;
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.62rem;
  font-weight: 700;
  line-height: 1.55;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.ao-card--big .ao-idx { font-size: 0.68rem; }
.ao-card-name {
  font-size: 0.82rem;
  font-weight: 700;
  color: #f0e0ff;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.ao-card-badge {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 0.6rem;
  font-weight: 700;
  letter-spacing: 0.2px;
  line-height: 1.5;
}
/* The badge's own status DOT — the third consumer of the same 5px round shape the group head
   (.ao-group-dot) and the pane bar already render, so all three now match in FORM, not just in
   timing. It is also what carries the breathing (below): animating the badge element itself
   oscillated a whole colored text capsule ("运行中" pulsing at 0.55↔1), which is an order of
   magnitude more screen area than a dot and made four running cards out-shout the one static red
   waiting card — the exact hierarchy inversion R3 exists to prevent. `currentColor` keeps it on
   the STATUS_COLOR SSOT with no new color declarations. */
.ao-card-badge::before {
  content: '';
  flex-shrink: 0;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
}
.ao-card--big .ao-card-badge { font-size: 0.66rem; padding: 2px 9px; }
.ao-card-badge.s-waiting { color: var(--status-waiting); background: color-mix(in srgb, var(--status-waiting) 14%, transparent); }
.ao-card-badge.s-running { color: var(--status-running); background: color-mix(in srgb, var(--status-running) 14%, transparent); }
/* Motion (PC big cards + mobile cards share this badge class) lands on the DOT only, never the
   text: opacity-only (no box-shadow/background-color/width — see STATUS_MOTION's doc comment), and
   the label stays fully legible instead of fading in and out. The card's own border/box-shadow
   highlight likewise stays static — a whole card pulsing is an order of magnitude more screen area
   than a dot, which is the hierarchy inversion this whole contract exists to prevent. */
.ao-card-badge.s-waiting::before {
  --dot-min-opacity: var(--dot-waiting-min);
  animation: status-dot-pulse var(--dot-waiting-duration) var(--dot-waiting-easing) infinite;
}
.ao-card-badge.s-running::before {
  --dot-min-opacity: var(--dot-running-min);
  animation: status-dot-pulse var(--dot-running-duration) var(--dot-running-easing) infinite;
}
.ao-card-badge.s-done-unseen { color: var(--status-done); background: color-mix(in srgb, var(--status-done) 16%, transparent); }         /* STATIC by contract */
.ao-card-badge.s-idle { color: #9a8ab8; background: rgba(122, 106, 154, 0.16); }
/* 存活态徽章：中性灰、静止（LIVENESS_MOTION 两档都是 null）。刻意不用任何一个状态色——
   它不是故障（红是"等你输入"）、也不在动，颜色从 LIVENESS_COLOR 绑来，不在这里敲 hex。 */
.ao-card-badge.s-notlive {
  color: v-bind('LIVENESS_COLOR.detached');
  background: color-mix(in srgb, v-bind('LIVENESS_COLOR.detached') 14%, transparent);
}
@media (prefers-reduced-motion: reduce) {
  .ao-card-badge.s-waiting::before,
  .ao-card-badge.s-running::before { animation: none; opacity: 1; }
}
.ao-card-tool {
  flex-shrink: 0;
  margin-left: auto;
  padding: 1px 6px;
  border-radius: 4px;
  background: #16121f;
  border: 1px solid #3a2860;
  color: #8a7aa8;
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.6rem;
  font-weight: 600;
}

.ao-card-agents {
  overflow: hidden;
  color: #9a8ab8;
  font-size: 0.62rem;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ao-card--big .ao-card-agents { font-size: 0.68rem; }

/* agent 的原话。比推断出来的那些行亮一档（它是这张卡上唯一"它自己说的"内容），但仍然只是文字：
   不加图标、不加动效、不占状态色——严重度由徽章那枚点表达，这行只负责内容。一行放不下就省略号，
   卡片高度不因一句长通知被撑开。 */
.ao-card-said {
  overflow: hidden;
  color: #d8c8ee;
  font-size: 0.66rem;
  font-weight: 600;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ao-card--big .ao-card-said { font-size: 0.74rem; }

.ao-card-tail {
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.66rem;
  line-height: 1.45;
  color: #8a7aa8;
  background: #1a1526;
  border-radius: 5px;
  padding: 5px 7px;
  overflow: hidden;
}
.ao-card-tail-line { white-space: pre; overflow: hidden; text-overflow: ellipsis; }
.ao-card-tail--empty { color: #5a4a78; font-style: italic; }

.ao-card-cwd {
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.62rem;
  color: #6f5a90;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.ao-card--big .ao-card-cwd { font-size: 0.68rem; }

/* ── PC：空闲 chip 条（最不抢眼、可折叠） ─────────────────────── */
.ao-idle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 9px;
  padding: 9px 13px;
  background: #120e1a;
  border: 1px dashed #2a1f3a;
  border-radius: 11px;
}
.ao-idle-toggle {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  flex-shrink: 0;
  padding: 2px 4px;
  background: transparent;
  border: 0;
  color: #6f5a90;
  font-size: 0.74rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  cursor: pointer;
}
.ao-idle-dot { width: 7px; height: 7px; border-radius: 50%; background: #6f5a90; }
.ao-idle-chevron { transition: transform 0.12s; font-size: 0.7em; }
.ao-idle-chevron.open { transform: rotate(90deg); }
.ao-idle-chip {
  display: inline-flex;
  align-items: baseline;
  gap: 7px;
  min-width: 0;
  max-width: 100%;
  padding: 4px 11px;
  background: #1a1526;
  border: 1px solid #2a1f3a;
  border-radius: 8px;
  color: #a693c2;
  cursor: pointer;
  transition: background 0.1s, border-color 0.1s;
}
.ao-idle-chip:hover { background: #241a34; border-color: #3a2860; }
.ao-idle-chip:active { transform: translateY(1px); }
.ao-idle-name { font-size: 0.75rem; font-weight: 600; color: #a693c2; flex-shrink: 0; }
.ao-idle-cwd {
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.66rem;
  color: #6f5a90;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
</style>
