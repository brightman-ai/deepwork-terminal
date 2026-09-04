/**
 * workbenchMerge — 两台设备各改了标签列表时，怎么合并才不丢东西。
 *
 * # 为什么必须是三方合并，而不是"取服务端的"或"取本地的"
 *
 * 标签列表是**整份文档**读写的：页面加载时读一次进内存，之后任何改动都把整份 PUT 回去。两台设备
 * 同时开着，就是两份各自演化的副本。这种形状下只有三个可能的合并策略，前两个都会丢数据：
 *
 *   • 取服务端的 → 本地刚新建的标签凭空消失。
 *   • 取本地的   → 这正是那个 bug 本身：手机上一份两小时前的旧文档 PUT 回去，把 PC 期间新建的
 *                  标签连同它绑着的那个 session 一起抹掉（session 还活着，只是再也没人指着它了）。
 *   • 三方合并   → 唯一能同时保住两边的做法。
 *
 * 关键在于，"某个标签不在这份文档里"是**歧义**的：可能是这台设备把它关了，也可能是这台设备从没
 * 见过它。服务端分辨不了（它只看得见一份文档），所以服务端只负责用 `rev` 拒绝陈旧写入；能分辨的
 * 只有客户端，因为只有客户端还留着**它当初读到的那一份**（base）。有了 base，两种"不在"就分开了：
 *
 *   在 base 里、现在不在本地 → 我删的 → 保持删除（哪怕服务端还留着）
 *   不在 base 里、现在在本地 → 我加的 → 保留
 *   在 base 里、现在不在服务端 → 别人删的 → 跟着删
 *   不在 base 里、现在在服务端 → 别人加的 → 认领
 *
 * 字段级同理：这个字段我改过（≠ base）就用我的，我没动过就用服务端的（可能别人改了）。
 *
 * # 不变量
 *
 *  ① 纯函数，不碰网络、不碰 ref、不改入参。三方合并是这次修复里唯一需要密集单测的逻辑，
 *    它必须能在没有浏览器、没有服务端的情况下被逐个 case 钉住。
 *  ② 顺序以**服务端为骨架**，本地独有的标签追加在后。标签位置决定编号（终端N / Alt+N），
 *    两台设备必须收敛到同一个顺序，否则同一个编号在两边指向不同标签——那是这次事故的另一半。
 *  ③ `activeTabId` / `activeGroupId` **一律本地优先**。它们记的是"我正在看哪个"，不是共享事实；
 *    取服务端的会导致另一台设备一存盘就把你的焦点抢走。
 */
import type { WorkbenchConfig, WorkbenchGroup, WorkbenchTab } from '@terminal/types/workbench'

/** 参与字段级合并的标签字段。显式列出而不是 `Object.keys`：漏一个字段会静默丢一次用户改动，
 *  而多写一个不存在的 key 只是无害的 undefined 比较。改 WorkbenchTab 时这里要跟着改。 */
const TAB_FIELDS: readonly (keyof WorkbenchTab)[] = [
  'groupId', 'name', 'cwd', 'engine', 'sessionId', 'remotePeerId',
]

/** 参与字段级合并的分组字段（`tabs` 不在内——它由标签级合并单独重建）。 */
const GROUP_FIELDS: readonly (keyof WorkbenchGroup)[] = ['name', 'color', 'collapsed']

function tabsOf(cfg: WorkbenchConfig | null): WorkbenchTab[] {
  return cfg ? cfg.groups.flatMap(g => g.tabs) : []
}

function byId<T extends { id: string }>(items: T[]): Map<string, T> {
  return new Map(items.map(i => [i.id, i]))
}

/**
 * 逐字段三方合并一个实体。`base` 为空 = 这条是新出现的，没有"我改过什么"可言，直接用 local。
 *
 * 判据是「我改过吗」（local ≠ base），不是「谁的值更新」——后者需要时间戳，而 lastSaved 是各设备
 * 自己的墙上时钟，手机和 PC 的时钟差几分钟是常态，拿它当仲裁者会随机丢改动。
 */
function mergeFields<T extends { id: string }>(
  base: T | undefined,
  local: T,
  server: T,
  fields: readonly (keyof T)[],
): T {
  if (!base) return { ...local }
  const out: T = { ...server }
  for (const f of fields) {
    if (local[f] !== base[f]) out[f] = local[f]
  }
  return out
}

/**
 * 合并 base / local / server 三份标签列表，返回可以直接 PUT 回去的那一份。
 *
 * `base` = 本地这份文档当初是从服务端哪一版长出来的（最后一次成功 load 或 save 的快照）。
 * 传 `null` 表示不知道（理论上不该发生——hydration gate 会拦住没成功 load 就存盘）。此时无法
 * 分辨删除，一律按**并集**处理：宁可留下一个该关的标签，也不能再丢一个还活着的 session。
 */
export function mergeWorkbench(
  base: WorkbenchConfig | null,
  local: WorkbenchConfig,
  server: WorkbenchConfig,
): WorkbenchConfig {
  const baseTabs = byId(tabsOf(base))
  const localTabs = byId(tabsOf(local))
  const serverTabs = byId(tabsOf(server))

  // ── 决定哪些标签活下来 ─────────────────────────────────────────────────────
  const survivors: WorkbenchTab[] = []
  const taken = new Set<string>()

  // 骨架：服务端的顺序（不变量②）。
  for (const st of tabsOf(server)) {
    const lt = localTabs.get(st.id)
    if (!lt) {
      // 本地没有它。base 里有过 → 是我删的，保持删除；base 里没有 → 别人新加的，认领。
      if (base && baseTabs.has(st.id)) continue
      survivors.push({ ...st })
      taken.add(st.id)
      continue
    }
    survivors.push(mergeFields(baseTabs.get(st.id), lt, st, TAB_FIELDS))
    taken.add(st.id)
  }

  // 本地独有的：base 里没有 = 我新建的，追加。base 里有而服务端没有 = 别人删的，不追加。
  for (const lt of tabsOf(local)) {
    if (taken.has(lt.id)) continue
    if (base && baseTabs.has(lt.id) && !serverTabs.has(lt.id)) continue
    survivors.push({ ...lt })
    taken.add(lt.id)
  }

  // ── 决定哪些分组活下来（同一套规则）────────────────────────────────────────
  const baseGroups = byId(base?.groups ?? [])
  const localGroups = byId(local.groups)
  const serverGroups = byId(server.groups)
  const mergedGroups: WorkbenchGroup[] = []
  const groupTaken = new Set<string>()

  for (const sg of server.groups) {
    const lg = localGroups.get(sg.id)
    if (!lg) {
      if (base && baseGroups.has(sg.id)) continue
      mergedGroups.push({ ...sg, tabs: [] })
      groupTaken.add(sg.id)
      continue
    }
    mergedGroups.push({ ...mergeFields(baseGroups.get(sg.id), lg, sg, GROUP_FIELDS), tabs: [] })
    groupTaken.add(sg.id)
  }
  for (const lg of local.groups) {
    if (groupTaken.has(lg.id)) continue
    if (base && baseGroups.has(lg.id) && !serverGroups.has(lg.id)) continue
    mergedGroups.push({ ...lg, tabs: [] })
    groupTaken.add(lg.id)
  }
  // 一个分组都不剩会让 addTab 无处可去（`groups.value[0]?.id` 取不到 → 静默不新建）。
  // 只可能发生在两边都把分组删空的退化输入上，兜一个而不是返回一份不可用的配置。
  if (mergedGroups.length === 0) {
    const fallback = local.groups[0] ?? server.groups[0]
    if (fallback) mergedGroups.push({ ...fallback, tabs: [] })
  }

  // ── 把标签归位到分组；groupId 已经不存在的挂到第一个分组，不丢 ──────────────
  const slot = new Map(mergedGroups.map(g => [g.id, g]))
  for (const t of survivors) {
    const g = slot.get(t.groupId) ?? mergedGroups[0]
    if (!g) continue
    g.tabs.push(t.groupId === g.id ? t : { ...t, groupId: g.id })
  }

  // 不变量③：焦点是本设备的私事。指向已经不存在的标签时才退回服务端的选择。
  const survivingIds = new Set(survivors.map(t => t.id))
  const activeTabId = survivingIds.has(local.activeTabId)
    ? local.activeTabId
    : (survivingIds.has(server.activeTabId) ? server.activeTabId : '')
  const activeGroupId = slot.has(local.activeGroupId)
    ? local.activeGroupId
    : (slot.has(server.activeGroupId) ? server.activeGroupId : (mergedGroups[0]?.id ?? ''))

  return {
    groups: mergedGroups,
    activeGroupId,
    activeTabId,
    // rev 是服务端所有物：合并结果要建立在**服务端当前那一版**之上，否则下一次 PUT 又是陈旧的。
    ...(typeof (server as { rev?: number }).rev === 'number'
      ? { rev: (server as { rev?: number }).rev }
      : {}),
    lastSaved: local.lastSaved,
  } as WorkbenchConfig
}
