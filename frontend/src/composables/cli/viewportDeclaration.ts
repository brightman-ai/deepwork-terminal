/**
 * viewportDeclaration — 什么时候可以告诉服务端「我的视口有多大」。
 *
 * ## 为什么这件事需要一条规则
 *
 * 一个终端的尺寸是**共享状态**，不是这个页面的私有属性。同一个 PTY 后面可能站着 PC 和手机两个
 * 浏览器页面（同一个 session 轮流接管），也可能是两个 PTY 各自 attach 到同一组 tmux pane——
 * 后者由 tmux 的 `window-size latest` 裁决。两种拓扑下结论一样：**尺寸归"正在被使用的那个客户端"**。
 * 这是 Human 当面拍的板："用户同一时间只会操作一个 client，以最后操作为主。"
 *
 * 于是"声明尺寸"就不是一句陈述，而是一次**抢占**：说出口就等于"我是最新的那个"。谁在什么时候
 * 有资格说，必须是一条规则，不能散落在四个 watch 里各写各的。
 *
 * ## 两个真实缺陷（都由这条规则修掉）
 *
 * · **看回来的人拿不到自己的排版。** 页面从后台切回前台，只做了一次整屏重绘（`term.refresh`），
 *   **没有声明尺寸**。理由看起来很充分：我的网格又没变，服务端知道我多大。这句话是错的——服务端
 *   那份尺寸是共享的，**在我不看的这段时间里可能已经被别人改掉了**。"我的网格没变"完全推不出
 *   "服务端记的还是我的网格"。结果：手机切回前台，看到的是为 PC 240 列排的版。
 *
 * · **没人在看的页面把正在用的人挤扁。** 尺寸声明挂在**连接生命周期**上：WS 一连上就阶梯式
 *   fit+声明三次（100/500/1500ms）。一个躺在后台的手机页面，网络一抖、WS 重连，就连喊三声
 *   "我是最新的"，正在 PC 上打字的人眼睁睁看着窗口被压成 80 列。声明本该挂在"我是不是那个正在
 *   被看的人"上，而不是挂在"我的 socket 刚好重连了"上。
 *
 * ## 规则
 *
 * 见 shouldDeclareViewport。三句话，其中最反直觉的是第二句：**刚成为观看者时必须声明，哪怕本地
 * 网格一个像素都没变**——因为要纠正的从来不是我的网格，是服务端那份可能已经被别人写过的共享尺寸。
 *
 * ## 为什么它对 tmux 和非 tmux 是同一条
 *
 * 两种模式在这一层是同一件事：浏览器视口 → xterm 网格 → WS resize → `SetPTYSize` → ioctl →
 * SIGWINCH。tmux 标签的接收方恰好是个 tmux 客户端（于是 tmux 再按 `window-size` 裁决一次），
 * 非 tmux 标签的接收方是应用自己。**同一条通路，同一条纪律**，所以这条规则写在两者共用的 surface
 * 上，而不是写进任何一侧的分支里——不是"顺便也覆盖了"，是它本来就只该有一份。
 */

/** 判断所需的全部输入。刻意只有两个：多一个都会让"我到底算不算在被看"变成一件要猜的事。 */
export interface ViewerState {
  /** 这个浏览器页面在前台（document.visibilityState === 'visible'）。 */
  pageVisible: boolean
  /** 这个终端是当前选中的那个标签（每个标签的 surface 都常驻挂载，所以必须显式问）。 */
  surfaceActive: boolean
}

/** 是不是"正在被看的那个"。两个条件缺一不可：页面在前台，且这个标签是选中的那个。 */
export function isViewer(s: ViewerState): boolean {
  return s.pageVisible && s.surfaceActive
}

/**
 * 该不该声明尺寸。
 *
 * @param was             上一次评估时是不是观看者
 * @param now             现在是不是
 * @param geometryChanged 本地网格是否真的变了（RO/fit 的结果）
 *
 * 三句话：
 *   1. 不是我在看 → 一个字都不许说。说了就是抢占正在用的人。
 *   2. 刚成为观看者 → 必须说，**哪怕网格没变**。服务端那份尺寸是共享的，可能已被别人改写。
 *   3. 一直在看 → 只有真的变了才说。没变还说，等于每次重连都对着别人喊"我是最新的"。
 */
export function shouldDeclareViewport(was: boolean, now: boolean, geometryChanged: boolean): boolean {
  if (!now) return false
  if (!was) return true
  return geometryChanged
}
