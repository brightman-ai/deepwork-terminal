/**
 * terminalFit — 「这个终端此刻能不能被测量」。
 *
 * ## 它修的是什么
 *
 * 标签切换用 v-show（`display: none`），因为 xterm 绑的是长命进程，不能随可见性生灭。但
 * **一个 `display: none` 的元素没有盒子，也就没有尺寸**——而 FitAddon 并不知道这件事：
 *
 * ```js
 * const r = getComputedStyle(terminal.element.parentElement)
 * const width  = Math.max(0, parseInt(r.getPropertyValue('width')))
 * ```
 *
 * CSSOM 规定：元素没有盒子时，`getComputedStyle` 返回的是**计算值**而不是使用值。我们的
 * `.xterm-container` 写的是 `width: 100%; height: 100%`，于是隐藏时这两个读数就是字符串
 * `"100%"`——而 `parseInt("100%") === 100`。**它不是 NaN**，所以 FitAddon 自己那道
 * `isNaN` 闸门一点都拦不住：100 被当成 100 像素，算出一个又窄又矮的终端。
 *
 * 实测（夹具 19097，1440×900）：
 *
 * | 标签状态 | PTY 里 `stty size` |
 * |---|---|
 * | 显示   | `52 162` |
 * | 隐藏   | `6 10`  ← ResizeObserver 在隐藏瞬间触发 fit，把 10×6 发给了服务端 |
 *
 * 对得上算术：`cols = max(2, floor((100 - 14) / 8.4)) = 10`、`rows = max(1, floor(100 / 16.8)) = 6`。
 *
 * ## 用户看到的
 *
 * 切回这个标签时，屏幕上是那个 10 列进程**最后画的那一帧**——一根窄条。客户端随即发出正确的
 * 尺寸，服务端 SIGWINCH，程序重画，才铺满宽屏。这中间隔着**一个完整的往返**：本机是几毫秒
 * （看不见），走 cloudflare tunnel 是几百毫秒（用户实测原话："切换 terminal tab 时会有一个
 * 短期的内容显示是窄条，然后才会切换为宽屏适配屏幕"）。**延迟不是病因，是显影液。**
 *
 * 而比闪一下更糟的是：后台标签的 PTY 在你不看它的整段时间里都是 10 列——跑在里面的 agent
 * 一直按 10 列输出，滚动缓冲被永久搞坏，切回来 reflow 也救不回全屏 TUI 的帧。
 *
 * ## 为什么修在这里
 *
 * 之前的处理是「切回来时重新 fit」（pro 那份 TerminalSurface 的注释原话就写着"消除 v-show
 * 隐藏时无法测量尺寸的问题"）——那是在**症状**上打补丁：承认隐藏时量不准，却仍然让它量、
 * 还把量出来的结果发给了服务端。真正的不变量只有一句：
 *
 *   **量不到尺寸的终端，就不该有尺寸主张——它上一次量准的尺寸继续有效，直到它重新可见。**
 *
 * 所以闸门放在「测量」这个动作本身上，而不是给每个调用点各打一次补丁：fit 的每一条路径
 * （ResizeObserver、变可见、连接后的阶梯 fit、抽屉挤压重排）最终都汇到同一处。
 */

/** 拿得到盒子才量得到尺寸。`display: none`（自己或任一祖先）→ offsetWidth/Height 归零。
 *
 *  用 offsetWidth/offsetHeight 而不是 `offsetParent !== null`：后者对 `position: fixed`
 *  的元素同样返回 null，会把一个完全可见的终端误判成不可测量。
 *
 *  `visibility: hidden` 有盒子、有真实尺寸 —— 判定为**可测量**，这是对的：它的几何是真的，
 *  只是没画出来。 */
export function canMeasureTerminal(el: {
  offsetWidth?: number
  offsetHeight?: number
} | null | undefined): boolean {
  if (!el) return false
  return (el.offsetWidth ?? 0) > 0 && (el.offsetHeight ?? 0) > 0
}

/**
 * ## 上面那条不变量的后半句，之前没实现
 *
 * 文件开头写着「**它上一次量准的尺寸继续有效**，直到它重新可见」。但闸门只做了前半句——
 * 跳过错误测量——**"上一次量准的尺寸"从来没被存在任何地方**。后果是一个还没可见过的标签
 * 停在 xterm 的默认 **80×24**，它的 PTY 停在 spawn 的 **220×50**（pty_manager.go），
 * 而用户屏幕上是 52×47：三个数字，没有一个对。跑在那个标签里的程序就按错的宽度排版，
 * 等你切过去才 SIGWINCH 重画——**已经写进 scrollback 的换行救不回来**。
 *
 * 修法基于一个此处成立的事实：**CLI portal 里所有终端共用同一个布局槽位**（CliTerminalView
 * 的 v-for + v-show，同一个 flex 容器）。所以"任意一个终端最近一次量准的尺寸"对其余终端不是
 * 猜测，是**同一个盒子的尺寸**。存一份、共用，比让每个终端各自守着一个假值要准得多。
 *
 * 它只在**量不到**的时候用。量得到就以真实测量为准，并顺手更新这份缓存。
 */
export interface GridSize {
  cols: number
  rows: number
}

let lastGood: GridSize | null = null

/** 一次量准的结果。只接受站得住的数字：fit 出 2×1 这种（隐藏元素的算术产物）不配当基准。 */
export function rememberGridSize(cols: number, rows: number): void {
  if (!isPlausibleGrid(cols, rows)) return
  lastGood = { cols, rows }
}

/** 最近一次量准的尺寸；从没量准过则 null（调用方此时只能沿用 xterm 默认，没有更好的选择）。 */
export function lastGoodGridSize(): GridSize | null {
  return lastGood
}

/** 仅测试用：清掉模块级缓存，免得用例之间互相污染。 */
export function resetGridSizeMemo(): void {
  lastGood = null
}

/**
 * 一个尺寸"像不像真的"。下界卡在 20×5：真实终端再窄也不会比这更小，而隐藏元素算出来的
 * 恰恰是 10×6 那一档（见文件头的实测表）——所以这道闸同时挡住了"隐藏时量出的垃圾被存成基准"
 * 这个会把 bug 永久化的场景。
 */
export function isPlausibleGrid(cols: number, rows: number): boolean {
  return Number.isFinite(cols) && Number.isFinite(rows) && cols >= 20 && rows >= 5
}
