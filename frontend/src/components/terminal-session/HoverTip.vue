<script setup lang="ts">
/**
 * HoverTip —— 状态行上那些小按钮的即时文字提示。
 *
 * 为什么不用原生 title：要停一秒多才出、样式不可控、和自定义提示叠两层；而这一行恰恰是
 * "只有图标、看不懂就得靠提示"的地方（2026-10-13 Human 实测：一排图标只有搜索看得懂）。
 *
 * 为什么不直接用 @ce/components/ui/tooltip：它的配色走 bg-primary（在深色终端上是一块亮白），
 * 动画依赖 tailwindcss-animate —— 这个组件同时嵌在 standalone 和 pro 两个壳里，样式只能靠自己，
 * 不能赌宿主的 Tailwind 配置一致（standalone↔pro drift 的老坑）。所以直接用 reka-ui 原语 +
 * 自带的非 scoped 样式（内容 Portal 到 body，scoped 样式够不着它）。
 *
 * 需要祖先里有一个 reka-ui TooltipProvider —— 由 CliTerminalSurface 提供，好处是同一行里从一个
 * 按钮滑到下一个时提示直接切换、不再等一次延迟。触屏上 reka-ui 本来就不开 hover 提示。
 */
import { TooltipContent, TooltipPortal, TooltipRoot, TooltipTrigger } from 'reka-ui'

withDefaults(defineProps<{
  /** 第一行：这个按钮是什么。 */
  label: string
  /** 第二行（可选）：按下会发生什么 / 快捷键 / 代价。 */
  hint?: string
  /** 再往下的若干行（可选）：逐项读数，如延迟的分段。每项一行，样式同 hint。 */
  details?: string[]
  side?: 'top' | 'bottom' | 'left' | 'right'
}>(), { hint: '', details: () => [], side: 'bottom' })
</script>

<template>
  <!-- disable-hoverable-content：提示是纯文字、不可交互（内容层 pointer-events:none）。reka-ui 默认的
       "可悬停内容"模式会在指针离开按钮、朝提示移动时进入一段过渡态，而指针永远进不了一个
       pointer-events:none 的提示，于是过渡态收不了尾 → 这个按钮之后再悬停就不出提示了
       （2026-10-13 fixture 实测：输入条悬浮球第一次出、第二次起再不出）。 -->
  <TooltipRoot :disable-hoverable-content="true">
    <TooltipTrigger as-child>
      <slot />
    </TooltipTrigger>
    <TooltipPortal>
      <TooltipContent
        class="dw-hover-tip"
        :side="side"
        :side-offset="6"
        :collision-padding="8"
        data-testid="hover-tip"
      >
        <span class="dw-hover-tip__label">{{ label }}</span>
        <span v-if="hint" class="dw-hover-tip__hint">{{ hint }}</span>
        <span v-for="(line, i) in details" :key="i" class="dw-hover-tip__hint" data-testid="hover-tip-detail">{{ line }}</span>
      </TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>

<style>
.dw-hover-tip {
  z-index: 9999;
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-width: 280px;
  padding: 5px 9px;
  border: 1px solid #3a3a48;
  border-radius: 6px;
  background: #1d1c26;
  color: #e8e8f2;
  font-size: 12px;
  line-height: 1.4;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.45);
  pointer-events: none;
  user-select: none;
}
.dw-hover-tip__label { font-weight: 600; }
.dw-hover-tip__hint { color: #a2a2b6; font-size: 11px; }
</style>
