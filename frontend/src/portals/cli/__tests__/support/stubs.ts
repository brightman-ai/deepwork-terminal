/**
 * CliTerminalView 视图接线测试用的替身。
 *
 * 真的 CliTerminalSurface 拖着 xterm / WebSocket / 一整个终端栈，这里只关心宿主（CliTerminalView）
 * 对每一格**绑了什么**：active / 样式 / 插槽 / 事件监听。所以换成一个只记录这些的替身。
 */
import { defineComponent, h } from 'vue'

/** 每次 focusTerminal() 调用记一笔（值 = 该格 sessionId），验证"手机上切格子不弹软键盘"。 */
export const focusCalls: string[] = []

export const CliTerminalSurfaceStub = defineComponent({
  name: 'CliTerminalSurfaceStub',
  props: {
    sessionId: { type: String, required: true },
    active: { type: Boolean, required: true },
    pasteFallback: { type: Boolean, default: undefined },
  },
  setup(props, { slots, expose }) {
    expose({ focusTerminal: () => { focusCalls.push(props.sessionId) } })
    return () => h(
      'div',
      { class: 'stub-surface', 'data-sid': props.sessionId, 'data-active': String(props.active) },
      [
        h('div', { class: 'status-extra' }, slots['status-extra']?.()),
        h('div', { class: 'overlay' }, slots['overlay']?.()),
      ],
    )
  },
})

/** 只透传插槽：真 HoverTip 需要 reka-ui 的 TooltipProvider 祖先，这里不关心提示。 */
export const HoverTipStub = defineComponent({
  name: 'HoverTipStub',
  props: { label: String, hint: String, side: String },
  setup(_, { slots }) {
    return () => slots.default?.()
  },
})
