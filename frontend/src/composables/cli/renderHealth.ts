/**
 * renderHealth — 「这个页面此刻是怎么把字画到屏幕上的」。
 *
 * ## 为什么它该被用户看见
 *
 * 渲染器的选择满足「关于」面板的三条收录标准：
 *
 *   ① **界面上看不见**：WebGL 和 DOM 渲染出来的终端长得一模一样。
 *   ② **它改变你对所见一切的解读**：同一句"怎么有点卡"，在 WebGL 下和在 DOM 下是两个完全不同的
 *      问题。XtermTerminal 里那段注释说得很直白——不知道渲染器，任何延迟抱怨都只能靠猜。
 *   ③ **一旦劣化，没有别的东西会告诉你**：拿不到 WebGL2 时它**静默**退回 DOM（这是对的：渲染慢
 *      一点不该让人丢掉整个终端）；GPU 复位 / 驱动更新 / 睡眠唤醒 / bfcache 恢复导致上下文丢失时，
 *      它 dispose 掉 addon，**这个页面剩下的生命周期都回不去**。两种情况用户都毫不知情，而后者
 *      恰恰**一次刷新就能修**。
 *
 * 在此之前这些事实只进服务端日志。日志能回答"你那边到底怎么回事"，但要有人去 grep；把它放进
 * 面板，用户自己就能回答。
 *
 * ## 为什么是页面级单例，而不是每个终端一份
 *
 * 渲染器不是终端的属性，是**这个页面 + 这块 GPU** 的属性：同一页里所有终端要么都拿得到 WebGL2，
 * 要么都拿不到。所以这里是模块级单例，最后挂载的终端写入即可。
 *
 * 唯一的例外是**上下文丢失**：它发生在单个 addon 实例上，但它的成因（GPU 复位、驱动更新）是整机
 * 级的，而且**修法是刷新整个页面**。所以 `contextLost` 一旦置位就**粘住**，直到页面重载——
 * 报告"这个页面已经降级过"比报告"当前这个终端还好着"更有用，也更诚实。
 *
 * ## 零新增采集
 *
 * 三个写入点都挂在**已经在跑**的上报路径上（cli.renderer.active / context_lost / render.metrics）。
 * 这个模块不测量任何东西，只是把已经算出来的事实留一份在客户端。
 */
import { ref, type Ref } from 'vue'
import { SLOW_FRAME_MS, type RenderMetricsSummary } from '@terminal/composables/cli/terminalRenderMetrics'

export type RendererKind = 'webgl' | 'dom' | 'unknown'

/** 当前渲染器。'unknown' = 还没有终端挂载过，此时**什么都不该说**。 */
const renderer = ref<RendererKind>('unknown')
/** 退回 DOM 的原因（构造/激活抛出的那条消息）。仅在被拒时有值。 */
const declineReason = ref('')
/** 本页面是否发生过 WebGL 上下文丢失。**粘性**：置位后不再清除，直到页面重载。 */
const contextLost = ref(false)
/** 最近一次渲染指标窗口（每 ~15s 一次，由 terminalRenderMetrics 产出）。 */
const metrics = ref<RenderMetricsSummary | null>(null)

export function noteRenderer(kind: 'webgl' | 'dom', reason?: string): void {
  renderer.value = kind
  declineReason.value = reason ?? ''
}

export function noteContextLost(): void {
  contextLost.value = true
  // 渲染器本身也确实退回了 DOM —— 说实话，不要留着一个"webgl"的旧值。
  renderer.value = 'dom'
}

export function noteRenderMetrics(summary: RenderMetricsSummary): void {
  metrics.value = summary
}

/** 测试用：重置页面级状态（生产代码没有清除的语义——刷新页面才是）。 */
export function resetRenderHealthForTest(): void {
  renderer.value = 'unknown'
  declineReason.value = ''
  contextLost.value = false
  metrics.value = null
}

export function useRenderHealth(): {
  renderer: Ref<RendererKind>
  declineReason: Ref<string>
  contextLost: Ref<boolean>
  metrics: Ref<RenderMetricsSummary | null>
} {
  return { renderer, declineReason, contextLost, metrics }
}

export interface RenderLine {
  /** 'ok' = GPU 在用；'warn' = 已降级且可修；'muted' = 降级但修不了 / 还不知道。 */
  tone: 'ok' | 'warn' | 'muted'
  /** 主文案（渲染器名 + 状态）。 */
  text: string
  /** 补充说明：为什么会这样。空则不显示。 */
  detail: string
  /** 有动作才给 —— 只有"刷新能修"的那一档才配一个按钮。 */
  action?: { label: string; kind: 'reload' }
}

/**
 * 渲染器那一行说什么。
 *
 * 三档的分野是**用户能不能做点什么**，不是好坏：
 *   · 上下文丢失 → 刷新就能拿回 GPU ⟹ warn + 动作
 *   · 从一开始就没有 WebGL2 → 刷新也没用（是驱动/浏览器的事）⟹ muted，只解释，不给假按钮
 *   · 正常 → ok，一句话，无动作
 */
export function rendererLine(
  kind: RendererKind,
  lost: boolean,
  reason: string,
): RenderLine {
  if (lost) {
    return {
      tone: 'warn',
      text: 'GPU 渲染已停用',
      detail: '显卡上下文丢失（GPU 复位 / 驱动更新 / 睡眠唤醒），刷新可恢复',
      action: { label: '刷新页面', kind: 'reload' },
    }
  }
  if (kind === 'webgl') return { tone: 'ok', text: 'WebGL（GPU）', detail: '' }
  if (kind === 'dom') {
    return {
      tone: 'muted',
      text: 'DOM（CPU）',
      // 说清"刷新没用"，免得用户白试 —— 这是浏览器/驱动层面的事。
      detail: reason ? `此浏览器拿不到 WebGL2：${reason}` : '此浏览器拿不到 WebGL2，刷新无法改变',
    }
  }
  return { tone: 'muted', text: '尚未初始化', detail: '' }
}

/**
 * 指标那一行。
 *
 * **刻意不报"帧率"。** 终端不是游戏循环——它只在有字节到达时才画，空闲时一帧都不画。把"每秒 3 帧"
 * 摆给用户看，只会让一个完全正常的空闲终端显得像坏了。真正能回答"卡不卡"的是**单帧渲染耗时**。
 *
 * **分位数 + 最慢一帧 + 超阈帧数**，三者缺一不可，因为它们回答的是三个不同的问题：
 *
 *   · P50/P95 —— 平常什么手感。（均值不行：90% 时间正常、每次重绘卡 200ms 的终端，均值是"正常"，
 *     人眼里是坏的。）
 *   · 最慢一帧 —— **卡顿活在尾巴上**。15 秒窗口跑 200 帧，一次 800ms 僵直落在 P99.5，P95 完全
 *     看不见它，而那偏偏是用户唯一感觉到的那一帧（Human 提出的正是这一点）。
 *   · 超阈帧数 —— 幅度分不出"偶发"和"持续"。一次 400ms 可能是 GC 或窗口 resize；十次 400ms 才叫卡。
 *
 * **为什么不列 Top5**：五个数字没有频次语境，反而比"最慢 + 几次"更难判断；而且这个面板只有 268px，
 * 五个数会把这一行挤成两行还读不出结论。要逐帧取证是服务端遥测的事，不是一眼看的面板的事。
 *
 * 另外报 forcedRepaints —— terminalRenderMetrics 自己点名它是"最值得盯的数字"：它意味着整屏重绘，
 * 而触发条件宽松到几乎任何输出都可能命中。
 *
 * ⚠️ 最慢值能被显示出来，前提是采样闸门（renderSampleGate）先存在：max 是**最容易被坏样本毁掉**
 * 的统计量——闸门之前，这里会赫然写着"最慢 177556ms"。
 */
export function metricsLine(m: RenderMetricsSummary | null): string {
  if (!m || m.frames === 0) return ''
  const parts = [`单帧 ${Math.round(m.renderP50)}/${Math.round(m.renderP95)}ms (P50/P95)`]
  if (m.renderMax > 0) parts.push(`最慢 ${Math.round(m.renderMax)}ms`)
  // 只在真有超阈帧时才说 —— 没有卡顿时多一句"0 帧"是噪音。
  if (m.renderSlow > 0) parts.push(`${m.renderSlow} 帧 >${SLOW_FRAME_MS}ms`)
  if (m.forcedRepaints > 0) parts.push(`整屏重绘 ${m.forcedRepaints}`)
  return parts.join(' · ')
}
