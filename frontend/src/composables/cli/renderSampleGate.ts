/**
 * renderSampleGate — 这一次「写入 → 重绘」的耗时，值不值得当成一个渲染耗时样本。
 *
 * ## 它修的是什么
 *
 * 渲染耗时是这样量的：
 *
 * ```
 * const started = performance.now()
 * terminal.onRender(() => noteRender(performance.now() - started))
 * terminal.write(data, …)
 * ```
 *
 * `onRender` **由 requestAnimationFrame 驱动**。而浏览器在标签页不可见时会节流、乃至完全挂起
 * rAF —— 于是那次回调可能在几分钟后才发生，中间整段闲置时间被一并算进"单帧耗时"。
 *
 * Human 实测截图：`单帧 57483/177556ms (P50/P95)`。177 秒不是渲染慢，是那个标签页在后台待了
 * 3 分钟。用户的判断"是不是放久了会有 bug"完全正确 —— **放久正是触发条件**。
 *
 * 同一个陷阱还有第二种形态：终端在**非当前标签**里（v-show → display:none）。页面可见、rAF 照跑，
 * 但一个没有布局盒子的终端不会真正重绘，样本同样失真。
 *
 * ## 为什么是闸门，不是上限
 *
 * 给数字加个上限（"超过 5 秒就当 5 秒"）只会把假数据变成好看的假数据 —— P95 依然由不可信样本决定，
 * 只是不再显眼。**要么这个样本可信、要么它根本不该存在。**
 *
 * 也**不能**反过来只信"小的值"：一次真实的 2 秒卡顿是这个指标存在的全部意义，按大小筛掉它，
 * 等于把唯一值得看的样本删掉。所以判据必须落在**成因**上（可见性 / 布局），不在数值上。
 */

/** 可见性纪元：每次 visibilitychange 自增。跨纪元的样本一律不可信。 */
let visibilityEpoch = 0

if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => { visibilityEpoch++ })
}

export function currentVisibilityEpoch(): number {
  return visibilityEpoch
}

/** 测试用：重置纪元。 */
export function resetVisibilityEpochForTest(): void {
  visibilityEpoch = 0
}

/** 测试用：模拟一次可见性切换。 */
export function bumpVisibilityEpochForTest(): void {
  visibilityEpoch++
}

/**
 * 样本可信 ⟺ 从写入到重绘的整段时间里，这个终端都**既可见、又有布局盒子**。
 *
 * 三个条件缺一不可：
 *   · `epochAtWrite === epochNow` —— 中途没有发生过可见性切换（后台→前台也算）
 *   · `!hiddenNow` —— 此刻页面是可见的（写入时就已在后台的情形）
 *   · `laidOutAtWrite` —— 写入时这个终端有盒子（不是躺在非当前标签里）
 */
export function isRenderSampleTrustworthy(opts: {
  epochAtWrite: number
  epochNow: number
  hiddenNow: boolean
  laidOutAtWrite: boolean
}): boolean {
  return opts.epochAtWrite === opts.epochNow && !opts.hiddenNow && opts.laidOutAtWrite
}
