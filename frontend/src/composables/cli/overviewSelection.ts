/**
 * overviewSelection — 「在总览里点中一张卡片」这个动作的 SSOT（两个壳共用）。
 *
 * 点一张卡片的意图从来只有一个：去那个终端。总览是一层临时的放大镜，任务完成就该让路——
 * 让用户点完卡片还得再按一次 Esc / 点一下遮罩，是把「切过去」拆成了两步。
 *
 * 切换与关闭写在同一个函数里（而不是在两个壳各自的 selectOverviewIndex 里各写一遍），是因为
 * 这类「两处各留一份开关」正是 standalone/pro 漂移的老剧本。键盘可达性不受影响：Esc / 点遮罩
 * 仍然直接关闭，那条路和这条路调的是同一个 closeOverview。
 */

export interface OverviewSelectPorts {
  /** 切到这个终端（standalone = 切标签；pro = 路由过去）。 */
  switchTo(id: string): void
  /** 关掉总览。与 Esc / 点遮罩共用同一个动作。 */
  closeOverview(): void
}

/**
 * 点中第 index 张卡片（1-based，编号就是可见标签的位置）。
 *
 * 越界（卡片对不上任何标签）时**什么都不做**：既不切也不关——用户什么也没选中，凭空关掉总览
 * 只会让人以为自己点漏了。
 */
export function selectOverviewCard(
  index: number,
  orderedIds: readonly string[],
  ports: OverviewSelectPorts,
): void {
  const id = orderedIds[index - 1]
  if (!id) return
  ports.switchTo(id)
  ports.closeOverview()
}
