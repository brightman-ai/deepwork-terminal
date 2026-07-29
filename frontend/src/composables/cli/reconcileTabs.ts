/**
 * reconcileTabs — 重载/重启后，把「持久化的标签」和「服务端真实存在的 session」对上账。
 *
 * 它只回答一个问题：每个标签背后那个进程，现在是**活着 / 已经结束 / 问不到**（见 tabLiveness.ts）。
 * 它不新建、不删除、不重命名任何东西。
 *
 * 这一点是用**端口形状**保证的，不是靠注释保证的：ReconcilePorts 里根本没有 create/remove 这类
 * 能力，所以「孤儿标签就偷偷 POST /sessions 开一个新的空 PTY 顶上去」这种事在这里写不出来。
 * 那正是被这次改动删掉的行为——屏幕上它和「恢复成功」完全一样，用户以为 agent 还在，其实早没了。
 *
 * 与之配套的诚实姿态：
 *   • 服务端/peer 没答（listXxx 返回 null）→ 一律 unreachable，且**保留绑定**。请求失败不是死亡
 *     证明；把它当死亡去解绑，下次连接恢复时就再也接不回那个还活着的 PTY 了。
 *   • 确认没这个 session（返回了集合但不含它）→ detached，并解绑。此时那个 sessionId 已经是一条
 *     指向不存在之物的假线索，留着只会让后续的 DELETE/WS 打到空处。
 */
import { tabLivenessFrom, type TabLiveness } from './tabLiveness'

/** 对账只需要这三个字段；调用方的标签类型比这胖，但胖的部分与对账无关。 */
export interface ReconcilableTab {
  id: string
  sessionId?: string
  /** 有值 = 这个标签的 session 长在某个远程 peer 上，不在本机 session 列表里。 */
  remotePeerId?: string
}

/**
 * 对账要用到的全部外部能力。刻意只有「读」和「改存活态/解绑」——没有任何能凭空造出终端的口子。
 * 实现方负责鉴权/超时/前缀等细节；这里只关心「拿到集合还是拿不到」。
 */
export interface ReconcilePorts {
  /** 本机当前存在的 session id 集合；`null` = 没问到（网络/鉴权/超时），不是「一个都没有」。 */
  listLocalSessions(): Promise<ReadonlySet<string> | null>
  /** 某个 peer 上当前存在的 session id 集合；`null` = 那台机器问不到。 */
  listPeerSessions(peerId: string): Promise<ReadonlySet<string> | null>
  /** 记录判定结果（运行时态，调用方自己决定怎么渲染）。 */
  setLiveness(tabId: string, liveness: TabLiveness): void
  /** 把标签上那条已确认失效的 sessionId 清掉。只在 detached 时调用。 */
  unbindSession(tabId: string): void
}

/** 判定 + 落账：detached 才解绑（见文件头注释）。 */
function apply(tab: ReconcilableTab, live: ReadonlySet<string> | null, ports: ReconcilePorts): void {
  const liveness = tabLivenessFrom(tab.sessionId, live)
  ports.setLiveness(tab.id, liveness)
  if (liveness === 'detached' && tab.sessionId) ports.unbindSession(tab.id)
}

/**
 * 对完所有标签的账。
 *
 * 本机清单只取一次；远程按 peer 分组，每个 peer 也只问一次（mesh 里 peer 可能离线，一个离线的
 * peer 不该拖累别的标签，超时由 ports 实现负责）。
 */
export async function reconcileTabs(tabs: ReconcilableTab[], ports: ReconcilePorts): Promise<void> {
  if (tabs.length === 0) return

  const localTabs = tabs.filter((t) => !t.remotePeerId)
  const remoteTabs = tabs.filter((t) => !!t.remotePeerId)

  if (localTabs.length) {
    const live = await ports.listLocalSessions()
    for (const tab of localTabs) apply(tab, live, ports)
  }

  const byPeer = new Map<string, ReconcilableTab[]>()
  for (const tab of remoteTabs) {
    const peerId = tab.remotePeerId as string
    const arr = byPeer.get(peerId) ?? []
    arr.push(tab)
    byPeer.set(peerId, arr)
  }
  for (const [peerId, peerTabs] of byPeer) {
    const live = await ports.listPeerSessions(peerId)
    for (const tab of peerTabs) apply(tab, live, ports)
  }
}
