// ── tab 深链（2026-10-07）：把「当前 tab」编码进 /portal/cli?t=<tabId> ──────────────────
//
// 选中态的真相一直在服务端共享文档（workbench.activeTabId，跨设备同步）里；URL 此前从不参与，
// 于是刷新/分享只会落到"全局最后活跃 tab"。这里只做两件薄薄的事：从 URL 读一次（命中则切，
// 未命中静默落到服务端兜底——无效 id 与已关 tab 是同一个动作，不是错误），以及 activeTab 变化时
// 把 id 写回 URL。写用 replace 而非 push：切换 tab 不是导航（见 terminalRenderer 的 ?renderer= 先例），
// 多一条历史记录只会让后退键变得莫名其妙。
//
// 刻意用 workbench 的 tabId 而非 sessionId：tabId 持久在共享文档里，进程退出后重开仍是同一个 id；
// sessionId 会随进程腐烂。tmux pane 刻意不入 URL（v1）：pane 选择是 tmux session 级共享状态，
// 多个 viewer 同时看同一个 session 时 URL 只是加载时刻的一次提示，编码它必然漂移——与今天点 pane bar
// 的行为一致，收益/复杂度比不成立。
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
