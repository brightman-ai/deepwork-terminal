/**
 * tabPresentation — 标签栏"一个标签该画成什么"的纯判定逻辑 SSOT。
 *
 * standalone 的 `CliTabBar.vue` 与 pro 的 `TopTabBar.vue` 是两个 presentational 组件（刻意的：
 * 两壳顶栏架构真实不同），但状态点颜色/存活态/已重开/roll-up 这几条判定此前各自手写了一份几乎
 * 逐行相同的代码——这才是真正的重复（组件本身该分开，判定逻辑不该分开）。
 */
import { URGENCY_ORDER, type EffectiveStatus } from './useAgentOverview'
import type { TabLiveness, TabNotLive } from './tabLiveness'

/** '' = 不画点。idle 故意不在 STATUS_COLOR 里：每个消费者对 idle 都是「不画」，空 shell 不值得
 *  占一个颜色。 */
export function effectiveTabStatus(
  tabStatuses: Map<string, EffectiveStatus> | undefined,
  tabId: string,
): EffectiveStatus | '' {
  const s = tabStatuses?.get(tabId)
  return s && s !== 'idle' ? s : ''
}

/** null = 这个标签背后还有活着的进程（默认）。有值就一定要说出来，不许当成"空闲"混过去。 */
export function tabNotLive(
  tabLiveness: Map<string, TabLiveness> | undefined,
  tabId: string,
): TabNotLive | null {
  const l = tabLiveness?.get(tabId)
  return l && l !== 'live' ? l : null
}

/** 这个标签是不是刚被自动重开过、且用户还没在里面输入过。 */
export function tabReopened(reopenedTabs: Set<string> | undefined, tabId: string): boolean {
  return reopenedTabs?.has(tabId) ?? false
}

export interface RollupSegment {
  status: Exclude<EffectiveStatus, 'idle'>
  icon: string
  count: number
}

const ROLLUP_ICON: Record<Exclude<EffectiveStatus, 'idle'>, string> = {
  waiting: '◉', running: '●', 'done-unseen': '✓',
}

/** roll-up 片段：最紧迫在前、零计数不显示。顺序取 URGENCY_ORDER（总览分组迭代的同一个常量），
 *  所以胶囊不可能和网格排出不同的次序。 */
export function rollupSegments(rollup: Record<EffectiveStatus, number> | undefined): RollupSegment[] {
  return URGENCY_ORDER
    .filter((s): s is Exclude<EffectiveStatus, 'idle'> => s !== 'idle')
    .map((status) => ({ status, icon: ROLLUP_ICON[status], count: rollup?.[status] ?? 0 }))
    .filter((s) => s.count > 0)
}
