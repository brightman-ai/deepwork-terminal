import type { WorkbenchTab } from '../../types/workbench'
import { URGENCY_ORDER, type EffectiveStatus } from './useAgentOverview'
import type { TabLiveness } from './tabLiveness'

export type PaneStatus = EffectiveStatus | 'disconnected' | 'unknown' | 'terminal'
export interface TabPaneStatus {
  key: 'first' | 'second'
  sessionId?: string
  label: string
  status: PaneStatus
}
export interface PaneSessionFact { status: EffectiveStatus; exited?: boolean }
export const PANE_STATUS_LABEL: Record<PaneStatus, string> = {
  waiting: '等待输入', running: '运行中', 'done-unseen': '完成未读', idle: '空闲',
  disconnected: '未连接', unknown: '状态未知', terminal: '终端',
}

/** Project each declared pane from its own session. Absence is unknown, never
 * the other pane's state. A dead primary is distinct from an unreachable peer. */
export function presentTabPanes(tab: WorkbenchTab, facts: ReadonlyMap<string, PaneSessionFact>, primaryLiveness: TabLiveness = 'live'): TabPaneStatus[] {
  const status = (id: string | undefined, first: boolean): PaneStatus => {
    if (primaryLiveness === 'unreachable' || first && primaryLiveness === 'detached') return 'disconnected'
    const fact = id ? facts.get(id) : undefined
    return fact?.exited ? 'disconnected' : fact?.status ?? 'unknown'
  }
  const labels = !tab.split ? ['', ''] : tab.split.orientation === 'row' ? ['左', '右'] : ['上', '下']
  const panes: TabPaneStatus[] = [{key: 'first', sessionId: tab.sessionId, label: labels[0]!, status: status(tab.sessionId, true)}]
  if (tab.split) panes.push({key: 'second', sessionId: tab.split.secondSessionId, label: labels[1]!, status: status(tab.split.secondSessionId, false)})
  return panes
}

/** Summaries remain useful for ranking; they no longer replace pane detail. */
export function aggregatePaneStatus(panes: readonly TabPaneStatus[]): PaneStatus {
  for (const status of URGENCY_ORDER) if (panes.some(pane => pane.status === status)) return status
  return panes.some(pane => pane.status === 'disconnected') ? 'disconnected' : 'unknown'
}

export function distinctPaneStatuses(statuses: readonly PaneStatus[]): PaneStatus[] {
  return [...new Set(statuses)].sort()
}
