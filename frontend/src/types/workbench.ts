/**
 * CLI Workbench types — Group/Tab/Config model for multi-session terminal workbench.
 * [Ref: TH-0502-w6d Round 4-5]
 */

export interface WorkbenchGroup {
  id: string
  name: string
  color?: string      // 可选颜色标签 (CSS color)
  tabs: WorkbenchTab[]
  collapsed: boolean
}

export interface WorkbenchTab {
  id: string
  groupId: string
  name: string
  cwd: string
  engine: string      // "shell" | "claude" | "codex"
  sessionId?: string  // TerminalHost session ID (运行时绑定, 未连接时为空)
}

export interface WorkbenchConfig {
  groups: WorkbenchGroup[]
  activeGroupId: string
  activeTabId: string
  lastSaved: string   // ISO 8601
}

function genId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`
}

/** 创建新 tab 的工厂函数 */
export function createTab(opts: {
  name?: string
  cwd?: string
  engine?: string
  groupId: string
}): WorkbenchTab {
  return {
    id: genId(),
    groupId: opts.groupId,
    name: opts.name || '终端',  // caller should provide sequenced name like "终端 1"
    cwd: opts.cwd || '~',
    engine: opts.engine || 'shell',
  }
}

/** 创建新 group 的工厂函数 */
export function createGroup(name: string): WorkbenchGroup {
  return {
    id: genId(),
    name,
    tabs: [],
    collapsed: false,
  }
}

/** 默认配置工厂 — 1 个空 group, 0 个 tab */
export function createDefaultWorkbenchConfig(): WorkbenchConfig {
  const defaultGroup = createGroup('默认')
  return {
    groups: [defaultGroup],
    activeGroupId: defaultGroup.id,
    activeTabId: '',
    lastSaved: new Date().toISOString(),
  }
}
