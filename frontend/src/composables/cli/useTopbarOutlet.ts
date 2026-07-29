/**
 * useTopbarOutlet — 「顶栏右侧那个共享出口现在在不在」。**一个全局事实，一个观察者，一份状态。**
 *
 * ## 它修的是什么
 *
 * `#dw-topbar-right` 是顶右 chrome 的 SSOT 出口：想待在那个角落的小部件都 teleport 进去，于是它们
 * 在**同一个 flex 行**里并排、共享间距，谁也压不着谁。CliTabBar 的注释写得很清楚：建这个出口就是
 * 为了终结"各自 position:fixed 抢同一块像素"的年代。
 *
 * 但消费者原先各自用**一次性探测**判断出口在不在，而 standalone 上这个判定必然失败：
 *
 * ```
 * App.vue 挂 <HelpCenter/>（全局，所有路由）
 *   → onMounted → nextTick 探一次 #dw-topbar-right
 *   → 可 /portal/cli 是【懒加载】路由，拥有出口的 CliTabBar 还没下载完
 *   → outletPresent = false，此后再没重探过（route.path 没变，watcher 不触发）
 * ```
 *
 * ⚠️ **诚实的因果**：这是真缺陷，但它**不是**用户看到的那个症状（"?" 悬浮球压住 ⌨）的原因。
 * 那个症状的真凶是 HelpCenter 的 Boolean prop 转型（见 HelpCenter.vue 里 `inline: undefined`
 * 的长注释）——AUTO 档根本到不了，这个 ref 是 true 是 false 都不影响结果。我一度把两者当成
 * 一回事，连着改错了两版；写在这里防止下一个人重蹈。
 *
 * ## 为什么是 MutationObserver（前两版都被实测否掉了）
 *
 * 我先试了 `router.afterEach`（"出口只在路由组件挂载时出现"），再试了"挂载时跑一轮有界重试"。
 * 两版都没能让判定变得可靠：出口的落地时刻既不在 `afterEach`、也不在 App 的 `onMounted` 之后的
 * 任何一个固定窗口里。结论不是"再猜一个更晚的时刻"，而是：**不存在能一次问准的时刻，所以别问
 * 时刻，直接观察那个元素本身。**
 *
 * 原先反对 MutationObserver 的理由是"别放到 xterm 的渲染热路径上"。那个担心在这个设计下不成立：
 *
 *   · 出口**不在**时，才用 `subtree` 观察 body 等它出现 —— 而出口不在，就意味着 CLI portal 没挂载，
 *     **xterm 根本不存在**，没有热路径可言。
 *   · 出口**在**时，立刻降级成只观察它的父节点（`childList`，不带 subtree）—— 那是标签栏的 chrome
 *     行，几乎不变；xterm 的每帧 DOM 改动离它十万八千里，一次回调都不会触发。
 *
 * 两个状态**都**避开了热路径。而且它是**因果正确**的：状态直接跟着那个元素的生灭走，不跟着任何
 * 关于"什么时候它应该出现了"的猜测走。
 *
 * 全模块单例：出口在不在是**一个**全局事实，N 个消费者共享同一个 ref 和同一个观察者，而不是各自
 * 探各自的 —— 那正是当初 HelpCenter 和 ShortcutsGuideBanner 判定分叉的根。
 */
import { onMounted, ref, type Ref } from 'vue'

/** 出口的 DOM id —— 唯一一处字面量。两壳的声明方（standalone CliTabBar / pro MainLayout）和所有
 *  消费方都从这里取，杜绝第三处手写出一个拼错的 id 然后静默失效。 */
export const TOPBAR_RIGHT_OUTLET_ID = 'dw-topbar-right'

export function topbarOutletExists(): boolean {
  return typeof document !== 'undefined'
    && !!document.getElementById(TOPBAR_RIGHT_OUTLET_ID)
}

// ── 单例状态 ────────────────────────────────────────────────────────────────────────────────
const present = ref(false)
let observer: MutationObserver | null = null
/** 当前观察的是谁：'body' = 等出口出现（subtree）；'parent' = 等出口消失（只看父节点一层）。 */
let watching: 'body' | 'parent' | null = null

function sync(): void {
  const el = typeof document === 'undefined'
    ? null
    : document.getElementById(TOPBAR_RIGHT_OUTLET_ID)
  present.value = !!el
  rearm(el)
}

function rearm(el: HTMLElement | null): void {
  if (typeof MutationObserver === 'undefined' || typeof document === 'undefined') return
  const want: 'body' | 'parent' = el ? 'parent' : 'body'
  const target = el?.parentNode ?? document.body
  if (observer && watching === want && observedTarget === target) return
  observer?.disconnect()
  observer ??= new MutationObserver(() => sync())
  observedTarget = target
  watching = want
  // 找到了 → 只盯父节点这一层（等它被移除）；没找到 → 盯 body 的整棵子树（等它出现）。
  observer.observe(target, el ? { childList: true } : { childList: true, subtree: true })
}
let observedTarget: Node | null = null

/**
 * 返回跟着出口生灭走的响应式布尔（全局共享）。
 *
 * 用法：把 Teleport 的 `v-if` 挂在它上面。为 false 时消费方退回自己的独立形态（HelpCenter 的
 * 悬浮球、ShortcutsGuideBanner 的内联小图标）——**那是降级，不是常态**。
 */
export function useTopbarOutlet(): Ref<boolean> {
  onMounted(sync)
  if (typeof document !== 'undefined') sync()
  return present
}
