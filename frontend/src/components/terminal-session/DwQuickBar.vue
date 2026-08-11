<template>
  <!-- 非 tmux 底栏。挂在主 Toolbar 正上方，占的是 TmuxQuickBar 在 tmux 场景下占的那一行 ——
       两者互斥，判据是「这个 shell 在不在 tmux 里」(attached)，由宿主把关。

       ── 这一行是动作条，不是导航条 ──────────────────────────────────────────────────────────
       它一开始把标签编号列（1 2 3 4 5 +）也印在这里，理由是「顶上标签栏拇指够不着」。理由没错，
       结论错了：切终端是**低频导航**，而这是手机上最输不起的一行 —— 五个号码 + 一个加号吃掉了
       它一大半宽度，换来的是一个**早就存在**的能力的第二个入口（左端胶囊本来就弹总览浮层，
       卡片带实时输出和状态点，点一下就切过去）。

       所以编号列退场，切终端只走胶囊 → 浮层，新建退回顶栏那个 +（够不着也认，这是本轮明确的
       取舍，不是疏忽）。腾出来的宽度不加新按钮，直接给现有动作：它们 flex:1 铺满，点按区一次
       变成原来的两倍多。tmux 那一行长什么样，这一行现在就读成同一种东西。

       按钮为什么这么少，见 dwQuickBar.ts 头部：cp / 查找 / PgUp / ^C … 在这一屏上都已经有
       常驻入口了，这里只放**没有第二个入口**的东西。

       没有 v-if：编号列在时它的门是 `tabs.length || tmuxInstalled`，那个 `tabs.length` 是为
       编号列设的。编号列没了之后这条 bar 的内容（胶囊 + 半屏滚动）在任何标签数下都成立，
       再留一个门就是留一个会莫名其妙吞掉半屏滚动的条件。出不出现由宿主判（isMobile &&
       tmuxReady && !attached），这里不判第二遍。 -->
  <div class="dw-quick-bar" data-testid="dw-quick-bar" @mousedown.prevent>
    <!-- 左端锚点：总览 + 全局状态卷起。和 TmuxPaneBar 的 leading 胶囊同一个结构、同一个 SSOT，
         所以两种场景下这一格读起来是同一格。编号列退场后它是拇指够得着的**唯一**切终端入口，
         胶囊上那串 ◉N●N✓N 也因此是「哪个终端在等你」仅剩的一眼 —— 它比过去更承重，不是更闲。 -->
    <button
      class="dqb-overview"
      :class="{ on: overviewOpen }"
      type="button"
      title="总览（同屏看全部终端 · 点卡片切过去）"
      aria-label="总览"
      data-testid="dw-overview-toggle"
      @click="emit('toggle-overview')"
      @pointerup.stop
      @touchend.stop
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" />
      </svg>
      <span v-if="rollupSegs.length && !overviewOpen" class="dqb-rollup" data-testid="dw-rollup">
        <span v-for="s in rollupSegs" :key="s.k" class="dqb-seg" :class="s.cls">{{ s.icon }}{{ s.n }}</span>
      </span>
    </button>

    <!-- 动作区。铺满剩下的整行 —— 这一行的默认归属就是它。 -->
    <div class="dqb-actions">
      <button
        v-for="a in actions"
        :key="a.id"
        class="dqb-btn"
        :class="`dqb-btn--${a.kind}`"
        type="button"
        :title="a.title"
        :aria-label="a.title"
        :data-testid="`dw-action-${a.id}`"
        @click="emit('action', a.id)"
        @pointerup.stop
        @touchend.stop
      >
        <span class="dqb-cap">{{ a.label }}</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { STATUS_COLOR, type EffectiveStatus } from '@terminal/composables/cli/useAgentOverview'
import { dwQuickActions, type DwQuickAction } from './dwQuickBar'

const props = defineProps<{
  /** 全局状态卷起，来自和标签栏同一个 useAgentOverview 实例。 */
  rollup?: Record<EffectiveStatus, number>
  overviewOpen?: boolean
  /** 机器上装了 tmux —— 只决定要不要留那个 attach 逃生口。 */
  tmuxInstalled: boolean
}>()

const emit = defineEmits<{
  (e: 'toggle-overview'): void
  (e: 'action', id: DwQuickAction): void
}>()

const actions = computed(() => dwQuickActions({ tmuxInstalled: props.tmuxInstalled }))

// 只显示非零、按紧急度排序（idle 省掉——扫一眼时它不是可行动信息）。与 TmuxPaneBar 同一份规则。
const rollupSegs = computed(() => {
  const r = props.rollup
  if (!r) return []
  const defs: Array<{ k: EffectiveStatus; icon: string; cls: string }> = [
    { k: 'waiting', icon: '◉', cls: 'seg-waiting' },
    { k: 'running', icon: '●', cls: 'seg-running' },
    { k: 'done-unseen', icon: '✓', cls: 'seg-done' },
  ]
  return defs.filter((d) => (r[d.k] ?? 0) > 0).map((d) => ({ ...d, n: r[d.k] }))
})
</script>

<style scoped>
.dw-quick-bar {
  /* 三个颜色在这个文件里只从这里进来 —— 绑的是 STATUS_COLOR（useAgentOverview），和
     TmuxPaneBar / TmuxStatusSheet / AgentOverview 绑的是同一个常量，所以卷起那三段不可能
     和那三处各跳各的。（STATUS_MOTION 不在这里了：会脉动的状态点是编号列上的东西，编号列
     退场时它们一起走了；卷起是静态数字。） */
  --status-waiting: v-bind('STATUS_COLOR.waiting');
  --status-running: v-bind('STATUS_COLOR.running');
  --status-done: v-bind("STATUS_COLOR['done-unseen']");
  position: relative;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 6px;
  background: #150f20;
  border-top: 1px solid #2a1f3a;
  user-select: none;
  -webkit-user-select: none;
}
/* 不再横向滚动、不再 sticky：能溢出的那一列（编号列）没了，剩下的两块本来就一屏放得下。
   留着 overflow-x:auto + 两个 sticky，等于给一个永远不滚的行留一套滚动机关。 */

/* 左端锚点：总览 + 卷起计数。宽度随卷起内容自适应，不参与铺满 —— 它是入口，不是动作。 */
.dqb-overview {
  flex: 0 0 auto;
  display: inline-flex; align-items: center; gap: 5px;
  width: auto; min-width: 34px; height: 30px; padding: 0 9px;
  background: #150f20; border: 1px solid transparent; border-radius: 6px;
  color: #6f5a90; cursor: pointer; touch-action: manipulation;
  transition: color 0.1s, background 0.1s, border-color 0.1s;
}
.dqb-overview:hover { color: #b08fd0; }
.dqb-overview:active { transform: scale(0.94); }
.dqb-overview.on { color: #f0e0ff; background: #4a2a7a; border-color: #7a4ab0; }
.dqb-rollup { display: inline-flex; align-items: center; gap: 5px; font-size: 0.62rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.dqb-seg { color: #6f5a90; }
.dqb-seg.seg-waiting { color: var(--status-waiting); }
.dqb-seg.seg-running { color: var(--status-running); }
.dqb-seg.seg-done { color: var(--status-done); }

/* 动作区吃掉剩下的整行。这就是「默认留给动作条」那句话的全部实现 —— 不是留白，是把宽度
   真的发下去。 */
.dqb-actions {
  flex: 1 1 auto; min-width: 0;
  display: flex; align-items: center; gap: 4px;
}

.dqb-btn {
  flex: 1 1 0; min-width: 56px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 30px;
  padding: 0 10px;
  background: #1c1430;
  color: #b08fd0;
  border: 1px solid #3a2860;
  border-bottom: 2px solid #1f1040;
  border-radius: 6px;
  font-size: 0.66rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
}
.dqb-btn:active { background: #3a2860; transform: translateY(1px) scale(0.96); border-bottom-width: 1px; }
.dqb-cap { font-size: 0.7rem; line-height: 1; }

/* 半屏滚动 —— 单手最常用的两个目标，更亮的静息色和更强的按下反馈（和 TmuxQuickBar 的
   .tqb-btn--scroll 同一个待遇）。宽度不再靠 min-width 争，交给 flex 分。 */
.dqb-btn--scroll {
  color: #d6b8f4;
  border-color: #4c3676;
}
.dqb-btn--scroll .dqb-cap { font-size: 0.78rem; font-weight: 600; }
.dqb-btn--scroll:active { background: #4a3284; transform: translateY(1px) scale(0.94); }

/* attach 是逃生口不是主路：不给强调色（tmux 那条 bar 在未 attach 时把它做成醒目的绿，
   那是给 tmux 用户的；这条 bar 的受众是不用 tmux 的人）。它也不该和两个主动作分同样宽 ——
   点按区变大是好事，但「一样大」会读成「一样常用」。 */
.dqb-btn--attach {
  flex: 0 1 auto; min-width: 64px;
  color: #7a6a9a;
  border-color: #2f2450;
}
.dqb-btn--attach:active { background: #2a1f45; }
</style>
