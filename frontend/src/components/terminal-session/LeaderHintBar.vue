<template>
  <!-- leader 已按下、正在等第二段。
       一个没有任何反馈的模式，使用者按下去只会以为键坏了 —— tmux 的前缀可以不给提示，是因为它有
       一条常驻状态行可看；浏览器里这一条没有。顺带把可用的键直接印出来，于是这个功能不需要先去
       读文档。

       两个壳（deepwork-terminal / deepwork-pro）用的是**同一个组件**：这句提示、这套配色、这份
       键位说明只存在一份。此前它在 portal 和设置页各写了一份手抄的字符串，而手抄的第二份注定和
       键位表分家。
       固定定位、pointer-events:none —— 它是一句话，不是一个可点的东西，绝不能挡住下面的终端；
       等待态只有两秒，让整页跳一下不值。 -->
  <div v-if="pending" class="leader-hint" data-testid="cli-leader-hint">
    <span class="lh-key">{{ label }}</span>
    <span class="lh-keys">{{ hint }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { leaderHintText, type LeaderAction } from '@terminal/composables/cli/useShortcutsConfig'

const props = defineProps<{
  pending: boolean
  /** 印在胶囊里的键名，来自 bindingLabel（绑定措辞的 SSOT）。 */
  label: string
  /** 这个壳真的实现了的动作；不传 = 全列。没实现的不印 —— 提示里列一个按下去没反应的键更伤人。 */
  available?: ReadonlySet<LeaderAction>
}>()

const hint = computed(() => leaderHintText(props.available))
</script>

<style scoped>
.leader-hint {
  position: fixed; left: 50%; bottom: 18px; transform: translateX(-50%);
  z-index: 3000; pointer-events: none;
  display: flex; align-items: center; gap: 9px; max-width: min(92vw, 720px);
  padding: 7px 13px; border-radius: 9px;
  background: #1a1526; border: 1px solid #4a2a7a;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  font-size: 0.72rem; line-height: 1.4;
}
.lh-key {
  flex-shrink: 0; padding: 2px 8px; border-radius: 5px;
  background: #4a2a7a; color: #f0e0ff; font-weight: 600;
  font-family: ui-monospace, Menlo, monospace;
}
.lh-keys { color: #b9a8d0; font-family: ui-monospace, Menlo, monospace; }
</style>
