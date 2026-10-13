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

/** 一个标准 tab 原生分屏成两个独立终端格子（scope: terminal-split，docs/product/terminal-split/）。
 *  主格继续用 `WorkbenchTab.sessionId`；这里只装副格那一半 + 布局。可选字段 → 未分屏 tab 的既有数据
 *  形状逐字节不变（`split` 整个不存在），零回归面。故意不叫 `layout`——`ResourceDrawer.vue` 已用这个
 *  词表达一个无关概念（文件抽屉贴边/悬浮，纯本机偏好，非 per-tab），避免同词异义的混淆。 */
export interface TabSplit {
  orientation: 'row' | 'column'   // row=左右并排，column=上下堆叠
  ratio: number                    // 0.1–0.9，主格占比；拖拽分隔条改它
  secondSessionId?: string         // 副格的 session；分屏瞬间尚未起好新 shell 时短暂为 undefined
  /** 某一格临时占满整个 tab（另一格不渲染但进程仍在跑，退出 zoom 原样恢复）。undefined = 都不
   *  放大（正常双格）。走跟 orientation/ratio 一样的同步路径——分屏本身选的是"跨设备同步"，
   *  zoom 作为同一份 split 状态的一部分，没有理由单独走一条不同步的本地态，省一套机制。
   *  [假设] 这条沿用了 split 本身的同步口径，未逐条问过，Human 见此节点可翻。 */
  zoomed?: 'first' | 'second'
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
  split?: TabSplit    // undefined = 未分屏（今天的全部行为，一字节不改）
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
