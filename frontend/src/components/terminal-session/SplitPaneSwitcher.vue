<template>
  <!-- 分屏折叠成单面板时（窄屏，splitCollapse.ts）状态行里的面板切换器：写着"当前是第几个、共两个"，
       点一下换到另一格。只换本机的显示，不写同步的分屏模型。判定全在 splitCollapse.ts，这里只渲染。
       根节点带 .split-switch：CliTerminalView 的"点进这一格就把当前格让给它"据此跳过它
       （splitControl.claimsPaneFocus）。 -->
  <HoverTip :label="model.label" :hint="model.hint">
    <button
      type="button"
      class="split-switch"
      :aria-label="`${model.label}（现在是第 ${model.index} 格，共 ${model.total} 格）`"
      :data-current="visible"
      data-testid="split-pane-switcher"
      @click="emit('switch')"
    >
      <!-- 两格示意：亮的那格 = 此刻显示的；方向跟分屏方向一致。 -->
      <span class="split-switch__glyph" :class="orientation" aria-hidden="true">
        <i :class="{ 'is-on': visible === 'first' }" />
        <i :class="{ 'is-on': visible === 'second' }" />
      </span>
      <span class="split-switch__count">{{ model.index }}/{{ model.total }}</span>
    </button>
  </HoverTip>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import HoverTip from '@terminal/components/terminal-session/HoverTip.vue'
import type { PaneId, SplitOrientation } from '@terminal/components/terminal-session/splitControl'
import { paneSwitcherModel } from '@terminal/components/terminal-session/splitCollapse'

const props = defineProps<{
  orientation: SplitOrientation
  /** 此刻显示的那一格。 */
  visible: PaneId
}>()

const emit = defineEmits<{ (e: 'switch'): void }>()

const model = computed(() => paneSwitcherModel(props.orientation, props.visible))
</script>

<style scoped>
/* 与 .split-ctl__btn 同一套视觉重量；触屏上多留一点横向命中区（手指比鼠标粗），高度仍是 24px，
   绝不把状态行撑高（同 SplitControlBar 的约束）。 */
.split-switch {
  flex-shrink: 0;
  align-self: center;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 8px;
  margin: 0 2px;
  border: none;
  border-radius: 5px;
  background: rgba(122, 74, 176, 0.22);
  color: #c9a8ea;
  font-size: 0.72rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
  touch-action: manipulation;
  transition: color 0.1s, background 0.1s;
}
.split-switch:hover { color: #e0c8f8; background: rgba(122, 74, 176, 0.32); }
.split-switch:focus-visible { outline: 1px solid hsl(var(--ring, 240 5% 64%)); outline-offset: -1px; }
.split-switch:active { transform: scale(0.94); }

.split-switch__glyph {
  display: inline-flex;
  gap: 2px;
  width: 14px;
  height: 11px;
}
.split-switch__glyph.row { flex-direction: row; }
.split-switch__glyph.column { flex-direction: column; }
.split-switch__glyph i {
  flex: 1;
  border: 1px solid currentColor;
  border-radius: 1px;
  opacity: 0.55;
}
.split-switch__glyph i.is-on { background: currentColor; opacity: 1; }
.split-switch__count { line-height: 1; }
</style>
