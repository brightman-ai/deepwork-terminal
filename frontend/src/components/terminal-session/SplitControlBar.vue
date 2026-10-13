<template>
  <!-- 不再是独立的一行：2026-10-13 Human 实测"两行占屏幕"，现在作为一组内联按钮放进每格自己的
       状态行（CliTerminalSurface 的 #status-extra 插槽），一个像素的纵向空间都不多占。
       判定（每个按钮此刻是什么、做什么）全在 splitControl.ts，这里只渲染。 -->
  <div class="split-ctl" data-testid="split-control-bar">
    <template v-if="showSplitToggles">
      <HoverTip v-for="b in buttons" :key="b.orientation" :label="b.label" :hint="b.hint">
        <button
          type="button"
          class="split-ctl__btn"
          :class="{ 'split-ctl__btn--active': b.active }"
          :aria-label="b.label"
          :aria-pressed="b.active"
          :data-action="b.action"
          :data-testid="`split-ctl-${b.orientation}`"
          @click="onOrientation(b)"
        >
          <!-- 高亮态 = "切换焦点"：图标画出两格、点亮当前格 —— 按下去亮的那半换到另一边，意符即作用。 -->
          <svg
            v-if="b.action === 'switch-pane'"
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
            :data-current="b.current"
          >
            <rect x="3" y="3" width="18" height="18" rx="2" />
            <path :d="b.orientation === 'row' ? 'M12 3v18' : 'M3 12h18'" />
            <rect v-bind="litHalf(b)" rx="0.5" fill="currentColor" stroke="none" />
          </svg>
          <component :is="b.orientation === 'row' ? Columns2 : Rows2" v-else :size="15" aria-hidden="true" />
        </button>
      </HoverTip>
    </template>

    <!-- 放大只在已分屏时有意义：单格本来就是满屏。每格只管放大/还原它自己。 -->
    <HoverTip
      v-if="split && pane"
      :label="zoomedHere ? '还原分屏' : '放大这一格'"
      :hint="zoomedHere ? '两格重新一起显示' : '另一格先藏起来，仍在后台运行'"
    >
      <button
        type="button"
        class="split-ctl__btn"
        :class="{ 'split-ctl__btn--active': zoomedHere }"
        :aria-label="zoomedHere ? '还原分屏' : '放大这一格'"
        :aria-pressed="zoomedHere"
        :data-testid="`split-ctl-zoom-${pane}`"
        @click="emit('toggle-zoom', pane)"
      >
        <component :is="zoomedHere ? Minimize2 : Maximize2" :size="14" aria-hidden="true" />
      </button>
    </HoverTip>

    <!-- 关闭一格：唯一会丢东西的分屏操作，所以单独一个画着 × 的按钮，且要点两次——第一下变成
         红色「确认关闭」，3 秒内再点才真关，过时自动复原。只在第二格上（关闭第一格要把第二格
         扶正成 tab 本身，目前不提供）。 -->
    <HoverTip v-if="split && pane === 'second'" :label="closeTip.label" :hint="closeTip.hint">
      <button
        type="button"
        class="split-ctl__btn split-ctl__btn--close"
        :class="{ 'is-armed': closeArmed }"
        :aria-label="closeTip.label"
        data-testid="split-ctl-close"
        @click="onClose"
      >
        <span v-if="closeArmed" class="split-ctl__confirm">确认关闭</span>
        <X v-else :size="15" aria-hidden="true" />
      </button>
    </HoverTip>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Columns2, Maximize2, Minimize2, Rows2, X } from 'lucide-vue-next'
import HoverTip from '@terminal/components/terminal-session/HoverTip.vue'
import {
  CLOSE_CONFIRM_MS,
  closePaneTip,
  splitButtonModel,
  type PaneId,
  type SplitButtonModel,
  type SplitOrientation,
} from '@terminal/components/terminal-session/splitControl'
import type { TabSplit } from '@terminal/types/workbench'

const props = withDefaults(defineProps<{
  /** undefined = 未分屏；两个分屏按钮仍渲染（2026-10-12 拍定：未分屏时也需要这两个按钮，
   *  位置固定，高亮代替隐藏/出现）。 */
  split?: TabSplit
  /** 这组按钮挂在哪一格的状态行上；undefined = 未分屏（没有"放大"可言）。 */
  pane?: PaneId
  /** 分屏方向按钮只在一格上出现（主格；主格被放大藏起时由副格接手），两格各放一套就是重复。 */
  showSplitToggles?: boolean
  /** 最后获得焦点的那格——决定"切换焦点"往哪边切、图标点亮哪半。 */
  focusedPane?: PaneId
}>(), { showSplitToggles: true, focusedPane: 'first' })

const emit = defineEmits<{
  /** 未分屏 → 分屏；已分屏 → 换方向。**永远不会**取消分屏（见 splitControl.ts 头注）。 */
  (e: 'split-orientation', orientation: SplitOrientation): void
  /** 切换焦点到另一格（放大态下 = 改为放大另一格）。 */
  (e: 'switch-pane'): void
  (e: 'toggle-zoom', which: PaneId): void
  /** 已确认的关闭（第二次点击）。 */
  (e: 'close-pane'): void
}>()

const buttons = computed(() =>
  (['row', 'column'] as const).map((o) => splitButtonModel(o, props.split, props.focusedPane)),
)

function onOrientation(b: SplitButtonModel): void {
  if (b.action === 'switch-pane') emit('switch-pane')
  else emit('split-orientation', b.orientation)
}

/** 切换焦点图标里点亮的那半（落在描边内侧，留 1px 缝）。 */
function litHalf(b: SplitButtonModel): Record<string, number> {
  const second = b.current === 'second'
  return b.orientation === 'row'
    ? { x: second ? 14 : 5, y: 5, width: 5, height: 14 }
    : { x: 5, y: second ? 14 : 5, width: 14, height: 5 }
}

const zoomedHere = computed(() => !!props.pane && props.split?.zoomed === props.pane)

// ── 关闭：两次点击 ────────────────────────────────────────────────────────────────────────
const closeArmed = ref(false)
let closeTimer: ReturnType<typeof setTimeout> | null = null
function disarm(): void {
  closeArmed.value = false
  if (closeTimer) { clearTimeout(closeTimer); closeTimer = null }
}
function onClose(): void {
  if (!closeArmed.value) {
    closeArmed.value = true
    closeTimer = setTimeout(disarm, CLOSE_CONFIRM_MS)
    return
  }
  disarm()
  emit('close-pane')
}
const closeTip = computed(() =>
  closePaneTip(props.split?.orientation ?? 'row', props.pane ?? 'second', closeArmed.value),
)
// 分屏状态一变（换方向/放大/被别处关掉）就复原——"上膛"只对点下去那一刻的那个画面有效。
watch(() => props.split, disarm, { deep: true })
onBeforeUnmount(disarm)
</script>

<style scoped>
/* 行尾一组：左边一道细分隔线把"版面"按钮和左侧的"终端"动作分开。
   align-self:center 而不是上下 margin —— 这组的高度就是按钮的 24px，绝不能把状态行撑高
   （加 margin 那版实测把行高从 31 撑到 37，正是 Human 要省的那点屏幕）。 */
.split-ctl {
  flex-shrink: 0;
  align-self: center;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 4px 0 5px;
  border-left: 1px solid hsl(var(--border, 240 4% 24%));
}
/* 与左侧 .ssr-action 同一套视觉重量：默认几乎透明，hover 才浮起来。 */
.split-ctl__btn {
  display: inline-grid;
  place-items: center;
  min-width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: hsl(var(--muted-foreground, 240 5% 64%));
  cursor: pointer;
  transition: color 0.1s, background 0.1s;
}
.split-ctl__btn:hover { color: hsl(var(--foreground, 0 0% 98%)); background: hsl(var(--accent, 240 4% 22%)); }
.split-ctl__btn:focus-visible { outline: 1px solid hsl(var(--ring, 240 5% 64%)); outline-offset: -1px; }
.split-ctl__btn:active { transform: scale(0.92); }
.split-ctl__btn--active { color: #c9a8ea; background: rgba(122, 74, 176, 0.22); }
.split-ctl__btn--active:hover { color: #e0c8f8; background: rgba(122, 74, 176, 0.32); }
/* × 只在 hover 时显红：静止态与其余同重量，不在没事时喊。 */
.split-ctl__btn--close:hover { color: #ff6b6b; background: rgba(255, 82, 82, 0.12); }
.split-ctl__btn--close.is-armed,
.split-ctl__btn--close.is-armed:hover {
  padding: 0 7px;
  color: #fff;
  background: #c62828;
}
.split-ctl__confirm { font-size: 0.68rem; font-weight: 600; white-space: nowrap; }
</style>
