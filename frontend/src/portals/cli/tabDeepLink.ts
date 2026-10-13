// ── tab 深链（2026-10-07）：把「当前 tab」编码进 /portal/cli?t=<tabId> ──────────────────
//
// 选中态的真相一直在服务端共享文档（workbench.activeTabId，跨设备同步）里；URL 此前从不参与，
// 于是刷新/分享只会落到"全局最后活跃 tab"。这里只做两件薄薄的事：从 URL 读一次（命中则切，
// 未命中静默落到服务端兜底——无效 id 与已关 tab 是同一个动作，不是错误），以及 activeTab 变化时
// 把 id 写回 URL。写用 replace 而非 push：切换 tab 不是导航（见 terminalRenderer 的 ?renderer= 先例），
// 多一条历史记录只会让后退键变得莫名其妙。
//
// 刻意用 workbench 的 tabId 而非 sessionId：tabId 持久在共享文档里，进程退出后重开仍是同一个 id；
// sessionId 会随进程腐烂。
//
// ── pane（tmux window）深链（2026-10-11，推翻上面 v1 的"不做"决定）──────────────────────────
// v1 的理由是"多个 viewer 同时看同一 session 时 URL 会漂移"。查码证实这个前提在最常见的场景下不
// 成立：`session_manager.go SetActiveConn` 把同一 server 上的浏览器连接做成**互斥接管**（第二个连上
// 来会把第一个踢掉，不是并发共视），所以两个浏览器从不会真的同时盯着同一个 pane。真实残余风险只剩
// "裸 tmux attach 与浏览器共存"这个边缘用法（已知限制，见 request.md，不在此处防御）。Human 据此选择
// 了比 tab 更强的模型：**双向实时同步**（切 pane 写 URL；URL 变化——前进/后退/手改地址栏/外部链接——
// 也驱动真的切 pane），不是 tab 这种"只读一次"。详见
// `docs/.tg/work/20261011-230813-cli-tabs-term/request.md`。
export function resolveDeepLinkTabId(
  raw: unknown,
  tabs: ReadonlyArray<{ id: string }>,
): string | undefined {
  if (typeof raw !== 'string' || raw === '') return undefined
  return tabs.some((tab) => tab.id === raw) ? raw : undefined
}

/** activeTab 变化后该写进 URL 的 t 值；返回 undefined = URL 已正确，不必动。 */
export function syncTabQuery(
  activeTabId: string | undefined,
  currentQueryT: unknown,
): string | undefined {
  if (!activeTabId) return undefined
  return currentQueryT === activeTabId ? undefined : activeTabId
}

/** 把 `?p=` 的原始查询值解析成 tmux window index。没有列表可对照（见上方头注：选 window 的
 *  POST 本身是 best-effort，无效 index 服务端静默无视），只做"是不是一个非负整数"这一层校验；
 *  不是就当没有——与 tab 深链同一条"未命中静默落到现状"的容错语气。 */
export function resolveDeepLinkPaneIndex(raw: unknown): number | undefined {
  const n = typeof raw === 'string' ? Number(raw) : NaN
  return Number.isInteger(n) && n >= 0 ? n : undefined
}

/** activeWindowIndex 变化后该如何改 `?p=`：
 *  - `undefined` = URL 已经对，不动（防自激环路——自己刚写回的那次变化，下一轮算出来相等，不再触发）。
 *  - `null` = 当前 tab 没有 pane 概念（非 tmux，或 tmux 还没就绪）了，且 URL 上还留着上一个 tab 的
 *    `p`，要**清掉**——非 tmux tab 的 URL 不该出现 pane 片段。
 *  - 否则 = 要写进 URL 的字符串值。 */
export function syncPaneQuery(
  activeIndex: number | null,
  currentQueryP: unknown,
): string | null | undefined {
  if (activeIndex == null) {
    return currentQueryP === undefined ? undefined : null
  }
  const activeStr = String(activeIndex)
  return currentQueryP === activeStr ? undefined : activeStr
}
