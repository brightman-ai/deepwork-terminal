<template>
  <!-- Agent 概览的卡片渲染层（纯展示）。状态派生全在 useAgentOverview；本组件只吃
       已分组/已排序的 groups + 全局 rollup 计数，把窗口画成可点的卡片。点卡 → emit
       select(w.index)，父组件负责切窗口。
       PC = 注意力加权（方向:混合·status 主）：活跃(等你/运行/完成待看)= 大卡，按数量
       ≤3/行（4→2×2 田字格），status 定视觉权重、边框高亮；空闲 = 收成一条 chip 条，最不
       抢眼、可折叠。移动 = 单列 + 每组 sticky 分组头（不变）。配色对齐 TmuxPaneBar。 -->
  <div class="agent-overview" :class="{ 'is-mobile': isMobile, 'is-pc': !isMobile, 'is-fill': !isMobile && hasActive }" data-testid="agent-overview">
    <!-- roll-up 摘要行：单行「◉N 等你 · ●N 运行 · ✓N 完成」。只显计数>0 的段；
         只给最紧急的非零段着色，其余保持 dim；全为 0 时整行不渲染。 -->
    <div v-if="rollupSegments.length" class="ao-rollup" data-testid="overview-rollup">
      <template v-for="(seg, i) in rollupSegments" :key="seg.status">
        <span v-if="i > 0" class="ao-rollup-sep">·</span>
        <span class="ao-rollup-seg" :class="[`s-${seg.status}`, { 'is-hot': seg.colored }]">
          <span class="ao-rollup-icon">{{ seg.icon }}</span>{{ seg.count }} {{ seg.label }}
        </span>
      </template>
    </div>

    <!-- ── 移动：单列分组列表（每组 sticky 头） ─────────────────────── -->
    <div v-if="isMobile" class="ao-cards">
      <div v-for="g in groups" :key="g.status" class="ao-group">
        <div class="ao-group-head" :class="`s-${g.status}`">
          <span class="ao-group-dot" />{{ statusLabel(g.status) }}
          <span class="ao-group-count">{{ g.windows.length }}</span>
        </div>
        <button
          v-for="w in g.windows"
          :key="w.index"
          class="ao-card"
          :class="`s-${g.status}`"
          type="button"
          :data-testid="`overview-card-${w.index}`"
          @click="emit('select', w.index)"
        >
          <div class="ao-card-head">
            <span class="ao-card-name">{{ w.name || '#' + w.index }}</span>
            <span class="ao-card-badge" :class="`s-${g.status}`">{{ statusLabel(g.status) }}</span>
            <span v-if="windowTool(w)" class="ao-card-tool">{{ windowTool(w) }}</span>
          </div>
          <div v-if="tailLines(w, 2).length" class="ao-card-tail">
            <div v-for="(line, li) in tailLines(w, 2)" :key="li" class="ao-card-tail-line">{{ line || ' ' }}</div>
          </div>
          <div v-if="windowCwd(w)" class="ao-card-cwd">{{ windowCwd(w) }}</div>
        </button>
      </div>
    </div>

    <!-- ── PC ───────────────────────────────────────────────────────
         有活跃 agent：活跃大卡网格(≤3/行,4→田字格) + 空闲收成 chip 条(相对去突出)。
         全 idle（无活跃）：idle 是唯一内容 → 均质中卡网格填满空间，不压成薄条。 -->
    <template v-else>
      <template v-if="hasActive">
        <div class="ao-active" :style="{ '--cols': activeCols }" data-testid="overview-active">
          <button
            v-for="a in activeCards"
            :key="a.w.index"
            class="ao-card ao-card--big"
            :class="`s-${a.status}`"
            type="button"
            :data-testid="`overview-card-${a.w.index}`"
            @click="emit('select', a.w.index)"
          >
            <div class="ao-card-head">
              <span class="ao-card-name">{{ a.w.name || '#' + a.w.index }}</span>
              <span class="ao-card-badge" :class="`s-${a.status}`">{{ statusLabel(a.status) }}</span>
              <span v-if="windowTool(a.w)" class="ao-card-tool">{{ windowTool(a.w) }}</span>
            </div>
            <div v-if="tailLines(a.w).length" class="ao-card-tail">
              <div v-for="(line, li) in tailLines(a.w)" :key="li" class="ao-card-tail-line">{{ line || ' ' }}</div>
            </div>
            <div v-else class="ao-card-tail ao-card-tail--empty">（无最近输出）</div>
            <div v-if="windowCwd(a.w)" class="ao-card-cwd">{{ windowCwd(a.w) }}</div>
          </button>
        </div>

        <!-- 空闲：一条 chip 条，相对活跃去突出、可折叠 -->
        <div v-if="idleWindows.length" class="ao-idle" data-testid="overview-idle">
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
              <span class="ao-idle-name">{{ w.name || '#' + w.index }}</span>
              <span v-if="windowCwd(w)" class="ao-idle-cwd">{{ windowCwd(w) }}</span>
            </button>
          </template>
        </div>
      </template>

      <!-- 全 idle：均质中卡网格（idle 是唯一内容，正常呈现、用满空间） -->
      <div v-else-if="idleWindows.length" class="ao-allidle" data-testid="overview-allidle">
        <button
          v-for="w in idleWindows"
          :key="w.index"
          class="ao-card ao-card--grid s-idle"
          type="button"
          :data-testid="`overview-card-${w.index}`"
          @click="emit('select', w.index)"
        >
          <div class="ao-card-head">
            <span class="ao-card-name">{{ w.name || '#' + w.index }}</span>
            <span class="ao-card-badge s-idle">空闲</span>
            <span v-if="windowTool(w)" class="ao-card-tool">{{ windowTool(w) }}</span>
          </div>
          <div v-if="tailLines(w, 8).length" class="ao-card-tail">
            <div v-for="(line, li) in tailLines(w, 8)" :key="li" class="ao-card-tail-line">{{ line || ' ' }}</div>
          </div>
          <div v-if="windowCwd(w)" class="ao-card-cwd">{{ windowCwd(w) }}</div>
        </button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { TmuxWindowState } from '@terminal/types/terminal'
import {
  windowCwd,
  windowTool,
  overviewColumns,
  type EffectiveStatus,
  type OverviewGroup,
} from '@terminal/composables/cli/useAgentOverview'

const props = defineProps<{
  /** 已按状态分组 + 紧急度排序好的窗口。 */
  groups: OverviewGroup[]
  /** 全局各状态计数（供 roll-up 行）。 */
  rollup: Record<EffectiveStatus, number>
  /** 窄屏 → 单列 + sticky 分组头。 */
  isMobile: boolean
}>()

const emit = defineEmits<{ (e: 'select', index: number): void }>()

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

/** PC：活跃窗口（等你/运行/完成待看）展平成一列，保留 groups 的紧急度序，各自带 status。 */
interface ActiveCard { w: TmuxWindowState; status: EffectiveStatus }
const activeCards = computed<ActiveCard[]>(() =>
  props.groups
    .filter((g) => g.status !== 'idle')
    .flatMap((g) => g.windows.map((w) => ({ w, status: g.status }))),
)

/** PC：空闲窗口。 */
const idleWindows = computed<TmuxWindowState[]>(
  () => props.groups.find((g) => g.status === 'idle')?.windows ?? [],
)

/** 有无活跃 agent（等你/运行/完成待看）。idle 的"去突出/收 chip 条"只在**有活跃可对比**时成立：
 *  全 idle 时 idle 是唯一内容 → 改走均质卡片网格填满空间，而非薄条 + 空白。 */
const hasActive = computed(() => activeCards.value.length > 0)

/** PC 每行大卡列数（规则见 overviewColumns：≤3/行，4→2×2 田字格）。 */
const activeCols = computed(() => overviewColumns(activeCards.value.length))

/** 空闲条默认展开（chip 本就轻）；空闲多时可点折叠成一行。 */
const idleOpen = ref(true)

/** 卡片 live tail：去掉尾部空行后取末 `limit` 行；不传 limit = 全给（大卡靠 CSS 底对齐+裁剪
 *  填满卡高，行数随卡高由 CSS 决定，非这里写死）。移动卡传 2、空闲卡传 8。全空则空数组。
 *  上游行数上界由后端 overviewTailLines 决定（SSOT），这里只做展示层裁剪。 */
function tailLines(w: TmuxWindowState, limit?: number): string[] {
  const lines = (w.tail ?? []).slice()
  while (lines.length && lines[lines.length - 1].trim() === '') lines.pop()
  return limit != null ? lines.slice(-limit) : lines
}
</script>

<style scoped>
.agent-overview {
  padding: 12px;
  color: #d8c8ee;
  background: #16121f;
  box-sizing: border-box;
}
.is-pc.agent-overview { padding: 16px; }

/* 有活跃 agent 时（PC）撑满 overlay（.terminal-overview-overlay inset:0 全高）：
   rollup 顶 + 活跃区 flex 吃满剩余 + 空闲条钉底。活跃卡的高度由 .ao-active 的
   grid-auto-rows: minmax(208px,1fr) 决定 —— 空间足则 1fr 铺满、卡多到 1fr<保底则取
   208px 保可读、总高超一屏由 overlay 现成的 overflow-y:auto 滚动（Q1 长高优先·溢出滚）。 */
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

/* ── roll-up 摘要行 ───────────────────────────────────────────── */
.ao-rollup {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  margin-bottom: 12px;
  padding: 4px 2px;
  font-size: 0.74rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.is-pc .ao-rollup { font-size: 0.82rem; margin-bottom: 14px; }
.ao-rollup-seg {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: #6f5a90; /* 默认 dim；只有 .is-hot 段取状态色 */
}
.ao-rollup-icon { font-size: 0.72em; line-height: 1; }
.ao-rollup-sep { color: #3a2860; }
.ao-rollup-seg.is-hot.s-waiting { color: #ff5252; }
.ao-rollup-seg.is-hot.s-running { color: #3fb950; }
.ao-rollup-seg.is-hot.s-done-unseen { color: #2dd4bf; }

/* ── PC：活跃大卡网格（每行 ≤3，宽度铺满） ───────────────────── */
.ao-active {
  display: grid;
  grid-template-columns: repeat(var(--cols, 3), minmax(0, 1fr));
  grid-auto-rows: minmax(208px, 1fr); /* 卡高 SSOT：铺满 vs 保底可读，见 .is-fill 注释 */
  gap: 14px;
  align-items: stretch;
  margin-bottom: 14px;
}

/* 全 idle：均质自动填充中卡网格（填满 PC 空间，比薄 chip 条体验好） */
.ao-allidle {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 12px;
  align-items: start;
}
.ao-card--grid { gap: 8px; padding: 12px 14px; min-height: 116px; border-radius: 10px; }
.ao-card--grid .ao-card-name { font-size: 0.9rem; }
.ao-card--grid .ao-card-tail { max-height: 4.6em; } /* 空闲卡 tail 封顶，卡不过高、网格齐整 */

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
.ao-group-head.s-waiting .ao-group-dot { background: #ff5252; }
.ao-group-head.s-running .ao-group-dot { background: #3fb950; }
.ao-group-head.s-done-unseen .ao-group-dot { background: #2dd4bf; }
.ao-group-head.s-idle .ao-group-dot { background: #7a6a9a; }
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
.ao-card.s-waiting { border-left-color: #ff5252; }
.ao-card.s-running { border-left-color: #3fb950; }
.ao-card.s-done-unseen { border-left-color: #2dd4bf; }
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
  border-color: #ff5252;
  box-shadow: 0 0 0 1px #ff5252, 0 8px 30px rgba(255, 82, 82, 0.16);
}
.ao-card--big.s-running {
  border-color: rgba(63, 185, 80, 0.5);
  box-shadow: 0 6px 22px rgba(63, 185, 80, 0.13);
}
.ao-card--big.s-done-unseen {
  border-color: rgba(45, 212, 191, 0.45);
  box-shadow: 0 6px 20px rgba(45, 212, 191, 0.1);
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
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 0.6rem;
  font-weight: 700;
  letter-spacing: 0.2px;
  line-height: 1.5;
}
.ao-card--big .ao-card-badge { font-size: 0.66rem; padding: 2px 9px; }
.ao-card-badge.s-waiting { color: #ff5252; background: rgba(255, 82, 82, 0.14); }
.ao-card-badge.s-running { color: #3fb950; background: rgba(63, 185, 80, 0.14); }
.ao-card-badge.s-done-unseen { color: #2dd4bf; background: rgba(45, 212, 191, 0.14); }
.ao-card-badge.s-idle { color: #9a8ab8; background: rgba(122, 106, 154, 0.16); }
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
