/**
 * terminalRenderer — 这个页面用哪个渲染器，谁说了算。
 *
 * ── 走到今天的路 ──────────────────────────────────────────────────────────────────────────────
 * v0.8.0 把 WebGL 设成无条件默认，理由是桌面实测：同样的字节流，比 DOM 渲染器少 3.0× 布局、
 * 少 3.45× 样式重算。跟着一起上线的是一个**没有出口的默认** —— 每个终端都调
 * `enableWebglRenderer`，回到 DOM 渲染器的唯一途径是 WebGL 自己失败。
 *
 * 然后移动端坏了。iOS 实测（Human 截图，2026-08-02）：整行字被画成互不相干的 CJK 字形碎片——
 * 'e' 画成 'ョ'，'s' 画成 '，'，"ba" 挤成一个 '稲' —— 而它周围的行完好无损。出问题的行恰好都含有
 * 一个字体栈之外的字符（em dash、弯引号）：浏览器靠**字体回退**解析它，回退字形被光栅化进 WebGL
 * 的字形图集，此后这一行的拉丁字母查表全部落到错误的图集坐标，画出别人字形的碎片。这是**图集索引
 * 失效**，不是字体缺失 —— 字体缺失画的是豆腐块，不是别人的字。
 *
 * ── 2026-08 的规则（桌面部分已被下面 2026-10-18 一节改写）────────────────────────────────────
 * **DOM 是默认**，WebGL 由使用者显式选择。
 *
 * 这不是"WebGL 不好"的结论，是**默认该站在哪一边**的结论：默认要服务的是那个从没打开过设置面板的
 * 人，而这条路径上两种错法的代价完全不对称 —— DOM 慢一点，是可测量、可忍受、看得见的；WebGL 的
 * 图集串字是**画出来的字本身是错的**，而且看起来完全正常（几何精确、只有字错），使用者根本不会
 * 意识到自己在读一个被篡改的屏幕。把不对称的风险放进默认，就是把它交给最不设防的人。
 *
 * 所以取舍在**可发现性**上补回来：面板里那一行现在带一个切换按钮（renderHealth.rendererLine），
 * 想要 GPU 的人一次点击就能拿到，而且**记得住**。
 *
 * ── 2026-10-18：默认按设备分开 ─────────────────────────────────────────────────────────────────
 * 上面那笔账算的是"DOM 慢一点，可忍受"。桌面真机推翻了它：290×81 的网格上跑真实的 agent 输出，
 * DOM 单帧渲染 P95 76ms、最慢 4.6s，主线程整块被占住，按键成批到达（6 个字母 12ms 内全到）。
 * 被挡住的不只是画面：Edge/macOS 上输入法的 Shift 切中英在这时**切两次**，用一个每秒故意卡 0.7s
 * 的空白页就能复现，所以这是主线程卡顿造成的，不是哪段代码在改焦点。DOM 慢的代价落在打字上，不是
 * "看得见、可忍受"的那种慢。
 *
 * 而串字的那一边已经查清并另行处理：它出在 iOS 上，根因是隐藏画布的纹理被系统回收、上下文却还活着
 * （见 XtermTerminal 的 releaseWebglWhileHidden，隐藏即交还上下文）。所以默认现在跟着设备走：
 * **桌面 WebGL，iOS / iPadOS 与以触屏为主的设备仍是 DOM**。钉和偏好照旧压过默认。
 *
 * ── 三个输入，一个优先级 ──────────────────────────────────────────────────────────────────────
 * 「用哪个渲染器」以前只有"URL 参数 or 默认"两档，于是面板上的按钮无处安放。现在分三层，因为它们
 * 是三件不同的事，混在一个存储里必然互相吞掉：
 *
 *   1. **诊断钉**（`?renderer=dom|webgl`）—— sessionStorage，**仅本标签页**。它是用来做对照实验的：
 *      同一个终端两种渲染器各开一个标签页，直接比。所以它必须压过偏好，也必须不污染别的标签页。
 *   2. **使用者偏好**（面板按钮）—— localStorage，**跨标签页、跨刷新存活**。它是一句"我就要这个"，
 *      如果每开一个新标签页都要重说一遍，那它就不是偏好，只是一次性的挣扎。
 *   3. **默认** —— 按设备：桌面 WebGL，iOS / 触屏设备 DOM（defaultRendererFor）。
 *
 * 按钮写偏好时会**清掉本标签页的诊断钉**。否则一个几天前用过 `?renderer=webgl` 的标签页里，按钮
 * 点下去、页面刷新、渲染器纹丝不动 —— 一个看得见却无声失效的开关，比没有开关更糟。
 */

const QUERY_KEY = 'renderer'
/** 诊断钉：sessionStorage，随标签页而生灭。 */
const PIN_KEY = 'cli_renderer'
/** 使用者偏好：localStorage，跨标签页存活。 */
const PREF_KEY = 'cli_renderer_pref'

export type RendererKind = 'webgl' | 'dom'

/** 谁定的。面板据此说人话，而不是让使用者猜。 */
export type RendererSource = 'pinned' | 'chosen' | 'default'

/** 判断设备用的几个事实。单独成型，好让规则本身是纯函数、能被测试钉住。 */
export interface RendererDeviceFacts {
  userAgent: string
  maxTouchPoints: number
  /** 主指针是粗的（手指）且不能悬停 —— `(pointer: coarse) and (hover: none)`。 */
  touchFirst: boolean
}

/**
 * 没人发话时用哪个。见文件头 2026-10-18 一节：串字出在 iOS 上，DOM 的卡顿落在桌面打字上。
 *
 * iPadOS 默认以桌面 UA 访问（`Macintosh`），只能靠触点数认出来；带触屏的 Windows 笔记本主指针仍是
 * 鼠标，不是 touchFirst，按桌面算。
 */
export function defaultRendererFor(d: RendererDeviceFacts): RendererKind {
  const ios = /iPad|iPhone|iPod/.test(d.userAgent) || (/Macintosh/.test(d.userAgent) && d.maxTouchPoints > 1)
  return ios || d.touchFirst ? 'dom' : 'webgl'
}

function currentDeviceFacts(): RendererDeviceFacts {
  if (typeof navigator === 'undefined') return { userAgent: '', maxTouchPoints: 0, touchFirst: false }
  let touchFirst = false
  try {
    touchFirst = typeof window !== 'undefined'
      && window.matchMedia?.('(pointer: coarse) and (hover: none)').matches === true
  } catch {
    // 拿不到媒体查询就按 UA 判，不比没有更差。
  }
  return { userAgent: navigator.userAgent ?? '', maxTouchPoints: navigator.maxTouchPoints ?? 0, touchFirst }
}

/** 这台设备的默认渲染器。 */
export function defaultRenderer(): RendererKind {
  return defaultRendererFor(currentDeviceFacts())
}

export interface RendererDecision {
  renderer: RendererKind
  source: RendererSource
}

function asKind(v: string | null): RendererKind | null {
  return v === 'webgl' || v === 'dom' ? v : null
}

/**
 * 本标签页的诊断钉。`?renderer=` 出现时写入 sessionStorage，之后刷新仍然有效 —— 对照实验要能
 * 反复刷新同一个标签页，否则第一次刷新就把实验条件丢了。
 */
export function rendererPin(): RendererKind | null {
  if (typeof window === 'undefined') return null
  try {
    const q = asKind(new URLSearchParams(window.location.search).get(QUERY_KEY))
    if (q) {
      window.sessionStorage.setItem(PIN_KEY, q)
      return q
    }
    return asKind(window.sessionStorage.getItem(PIN_KEY))
  } catch {
    return null
  }
}

/** 使用者在面板里选的那个。null = 从没选过。 */
export function rendererPreference(): RendererKind | null {
  if (typeof window === 'undefined') return null
  try {
    return asKind(window.localStorage.getItem(PREF_KEY))
  } catch {
    return null
  }
}

/**
 * 面板按钮落到这里：记住选择，并**把钉从它存在的每一处**拔掉。
 *
 * 拔钉不是顺手打扫 —— 钉压过偏好，不拔就是让一个早已被忘记的 URL 参数无声否决使用者刚刚做的选择。
 * 明确的当下动作，压过含糊的过去动作。
 *
 * 而钉存在于**两处**：sessionStorage 里的那份，以及**地址栏里那个查询参数本身**。只清前者是这段
 * 代码第一版真实犯过的错（被单测当场抓住）：切换按下去 → 页面重载 → 地址栏里那个 `?renderer=dom`
 * 原封不动地又被读了一遍、又写回 sessionStorage、渲染器纹丝不动。一个看得见却无声失效的开关，比
 * 没有开关更糟。一份状态存两处，就必须两处一起收，否则"清掉了"只是一句自我安慰。
 */
export function setRendererPreference(kind: RendererKind): void {
  if (typeof window === 'undefined') return
  // 三处**各自** try/catch，不共用一个。共用时，只要第一句抛了（有些隐私模式/扩展会禁掉持久的
  // localStorage 而放行会话级的 sessionStorage——这是真实存在的组合），后面两句根本不会执行，
  // 钉原封不动地留着、重载后照旧压过偏好，于是又变回"一个看得见却无声失效的开关"——正是这个
  // 函数存在的理由，只是从存储层的另一侧绕回来。三件事互不依赖，就不该被同一个 catch 吞掉。
  try {
    window.localStorage.setItem(PREF_KEY, kind)
  } catch {
    // 记不住偏好：这次切换只能活到刷新为止。不值得把终端拦下来，更不该拖累拔钉。
  }
  try {
    window.sessionStorage.removeItem(PIN_KEY)
  } catch {
    // 钉拔不掉：回到"钉还在"的旧行为，不会更糟。
  }
  clearRendererQuery()
}

/**
 * 从地址栏里摘掉 `?renderer=`，不产生历史记录条目。
 *
 * replaceState 而不是 pushState：使用者按的是"换渲染器"，不是"去了个新页面"，多一条历史记录只会
 * 让后退键行为变得莫名其妙。摘不掉（老浏览器 / URL 解析不了）就算了 —— 那是回到"钉还在"的旧行为，
 * 不会更糟。
 */
function clearRendererQuery(): void {
  try {
    const url = new URL(window.location.href)
    if (!url.searchParams.has(QUERY_KEY)) return
    url.searchParams.delete(QUERY_KEY)
    window.history.replaceState(window.history.state, '', url.toString())
  } catch {
    // 见上：失败即回到旧行为，不值得中断切换。
  }
}

/**
 * 优先级规则本体。纯函数，所以推理只存在于一处，并且可以被测试钉住。
 *
 * 显式的意愿两个方向都压过默认 —— 包括"就在这台手机上给我 WebGL"：提这个要求的人正是在复现那个
 * bug，而一个恰恰在你在乎的场景下拒绝生效的开关，不算开关。
 */
export function resolveRenderer(opts: {
  pin: RendererKind | null
  preference: RendererKind | null
  /** 这台设备的默认。省略时现读设备（见 defaultRenderer）。 */
  fallback?: RendererKind
}): RendererDecision {
  if (opts.pin) return { renderer: opts.pin, source: 'pinned' }
  if (opts.preference) return { renderer: opts.preference, source: 'chosen' }
  return { renderer: opts.fallback ?? defaultRenderer(), source: 'default' }
}
