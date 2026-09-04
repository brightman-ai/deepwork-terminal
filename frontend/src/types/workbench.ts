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
  sessionId?: string  // TerminalHost session ID (运行时绑定, 未连接时为空).
                      // For a REMOTE tab this holds the session id ON THE PEER instance —
                      // all bindSession / tabsWithSession machinery is reused unchanged;
                      // remotePeerId is what marks the tab remote + routes its base.
  remotePeerId?: string // when set, this tab connects to a registered remote peer (mesh
                        // direct-connect) instead of the same-origin host. Optional →
                        // backward compatible (a tab without it is local). See useRemotePeers.
}

export interface WorkbenchConfig {
  groups: WorkbenchGroup[]
  activeGroupId: string
  activeTabId: string
  lastSaved: string   // ISO 8601
  /** 服务端所有的乐观并发版本号。客户端**只负责原样回传**它 load 到的那个值，从不自己加一
   *  —— 加一是服务端接受一次写入时做的事（见 settings.go withWorkbenchRev）。
   *
   *  它存在的理由：这份文档是整份读写的，两台设备各拿一份副本各自演化；没有它，后写的那一方
   *  会静默抹掉前一方的标签（连同还活着的 session 的绑定）。PUT 带着它 → 服务端发现基准已经
   *  移动就回 409 + 当前文档 → 客户端三方合并后重试（见 workbenchMerge.ts）。
   *
   *  可选：首次使用（服务端 404）与旧版本存下来的文档都没有这个字段。 */
  rev?: number
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
  remotePeerId?: string
}): WorkbenchTab {
  const tab: WorkbenchTab = {
    id: genId(),
    groupId: opts.groupId,
    name: opts.name || '终端',  // caller should provide sequenced name like "终端 1"
    cwd: opts.cwd || '~',
    engine: opts.engine || 'shell',
  }
  // Set at creation (not post-assigned) so the tab's FIRST persist already carries it — a remote
  // tab must never round-trip through storage as a local one (it would get a local session).
  if (opts.remotePeerId) tab.remotePeerId = opts.remotePeerId
  return tab
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
