<script setup lang="ts">
/**
 * MidTruncatedName — 文件名的"中段省略，保头保尾"展示件（2026-09-11，REQ-fp-midtrunc）。
 *
 * 横切要求写成共用组件而不是五处各写一遍——先例：ImageZoomViewer（"写三份必漂成三种手感"）。
 * 抽屉里所有"行名"面（目录树 / 搜索结果 / 最近修改 / 上传任务 / 预览标题）都用它。
 *
 * 形态：head 与 tail 两个相邻 span，宽度自适应交给 flex——head 可收缩（溢出时 CSS 打出
 * "…"，整体读作 `S1快路径-TMG标准看板…字段表-v8.xlsx`），tail 永不收缩（版本号/扩展名恒在，
 * 这是 Human 拍定方案的立意：分清 v8/v9）。名字整体放得下时不加任何省略号。
 * 极窄到连 tail 都放不下：tail 按 20 显示单位的预算截短（splitMidTruncate），扩展名
 * （ASCII，单位小）天然总是活着。
 *
 * tooltip：默认 hover 出全名；调用方传 tooltip="" 可关（如预览标题——外层已挂完整路径的
 * :title，嵌套两层 title 内层会顶掉外层）。
 */
import { computed } from 'vue'
import { splitMidTruncate } from './midTruncate'

const props = withDefaults(defineProps<{
  name: string
  tooltip?: string
}>(), {
  tooltip: undefined,
})

const parts = computed(() => splitMidTruncate(props.name))
const tip = computed(() => (props.tooltip === undefined ? props.name : props.tooltip))
</script>

<template>
  <span class="mt-name" :title="tip || undefined">
    <span class="mt-head">{{ parts.head }}</span><span class="mt-tail">{{ parts.tail }}</span>
  </span>
</template>

<style scoped>
/* 相邻不换行：head/tail 之间不能有空白（文件名是连续的），两个 span 必须紧贴。 */
.mt-name {
  display: inline-flex;
  min-width: 0;
  max-width: 100%;
  vertical-align: bottom;
}
/* head 收缩位：溢出时 ellipsis 由它打 —— "head…" + "tail" 即中段省略。min-width:0 允许
   收到 0（此时形态退化为 "…tail"——尾部完整优先于头部，与验收 A3-3 一致）。 */
.mt-head {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mt-tail {
  flex-shrink: 0;
  white-space: nowrap;
}
</style>
