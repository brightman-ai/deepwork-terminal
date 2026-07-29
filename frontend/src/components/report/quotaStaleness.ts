/**
 * quotaStaleness — 一条额度读数「有多可信」，以及据此该怎么呈现。
 *
 * ## 为什么需要它
 *
 * 额度这一格里，**数字本身几乎从不出错，出错的是它对应的时刻**。来源是运行时自己写下的东西
 * （codex 的 rollout / claude 的快照），而运行时只在**真的跑了一次调用**时才写。于是：
 *
 *   · 你在**另一台机器**上用 codex → 本机的读数根本不会动
 *   · 你在**同一台机器**上跑 `codex /status` → 它只打印、不产生模型调用、**不写 token_count**
 *     → 读数照样不动（Human 实测："我打开codex进行了一次/status查询(但是没有对话),额度没更新"）
 *
 * 两种情况下那个数字都**曾经是真的**，只是老了。所以正确的处置不是藏起来，而是**让它看起来
 * 就像个旧数**，并且给出一条能立刻取到新数的路。
 *
 * ## 三条呈现规则
 *
 * 1. **过期就降权**：过期读数不该和新鲜读数长得一模一样。此前它只在角落挂了个小标签，主体仍是
 *    满格绿条 + 大号百分比 —— 视觉权重完全没有反映置信度，于是"已过期"这句话没人看见。
 * 2. **坏消息必须配动作**：「数据已过期」本身就是按钮，点它直接向账号查询（probe，与
 *    `codex /status` 同源）。只说问题不给出路，等于把诊断工作丢回给用户。
 * 3. **零信息量的行收起来**：一个既过期、值又全是"推断"（窗口早已重置、此后无任何上报）的
 *    family，绿条上那个 100% 不是事实而是缺省猜测。它不配占一整行，折叠成一句小字即可。
 * 4. **被取代的家族不该冒充当前额度**（见下 isSupersededFamily）。这是 Human 实测那句
 *    "这个额度一直不对" 的真正根因：账号已从 `codex`(5h+7d) 换成 `premium`(单个 7 天窗口)，
 *    而旧家族那条 15 小时前的读数仍以完整绿条挂在名字更"正统"的 `codex` 行上，于是人一直在
 *    读一个已经作废的数字。
 */

export interface StalePresentation {
  /** 降权渲染：数字转灰、进度条改描边而非实心。 */
  dim: boolean
  /** 折叠成一行小字（长期无数据 + 值全是推断 ⟹ 这一行没有信息）。 */
  collapse: boolean
  /** 徽标文案（空 = 不显示徽标）。 */
  badge: string
  /** 徽标 hover 文案：先说**为什么**，再说**怎么办**。 */
  hint: string
}

/** 长期无数据的门槛：超过它、且值全是推断，这一行就没有信息量了。 */
export const COLLAPSE_AFTER_SECONDS = 3 * 24 * 3600

/** 把秒数说成人话。刻意粗粒度——这里要回答的是"多旧"，不是"精确多旧"。 */
export function humanAge(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return ''
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))} 分钟`
  if (seconds < 24 * 3600) return `${Math.round(seconds / 3600)} 小时`
  return `${Math.round(seconds / (24 * 3600))} 天`
}

export function stalePresentation(opts: {
  stale: boolean
  ageSeconds: number
  /** 该 family 的所有窗口是否都是推断值（无实测上报）。 */
  allInferred: boolean
  /** 运行时名，进文案（"未收到 codex 的用量上报"）。 */
  runtime: string
  /** 能不能就地查（有 probe 通道才给动作；没有就只降权不给假按钮）。 */
  canProbe: boolean
}): StalePresentation {
  if (!opts.stale) {
    return { dim: false, collapse: false, badge: '', hint: '' }
  }
  const age = humanAge(opts.ageSeconds)
  const why = age
    ? `本机已 ${age}未收到 ${opts.runtime} 的用量上报`
    : `本机未收到 ${opts.runtime} 的用量上报`
  // 点不了就别写"点击"——一个点了没反应的提示比没有提示更糟。
  const how = opts.canProbe ? '，点击直接向账号查询' : '（在本机运行一次该工具即可刷新）'
  return {
    dim: true,
    collapse: opts.allInferred && opts.ageSeconds >= COLLAPSE_AFTER_SECONDS,
    badge: '数据已过期',
    hint: why + how,
  }
}

/**
 * 这个 family 是否**已被取代**（账号现在跑在别的限额家族上）。
 *
 * ## 判据从哪来
 *
 * `QuotaInfo.family`（响应顶层那个）是**最新一条账号读数的 family**——kit 侧 `applyReadings`
 * 把 readings 按新旧排序后取 `groups[0].Family`。而两个来源（probe 读 `x-codex-active-limit`
 * 响应头、rollout 读 `limit_id` 且已滤掉 per-model 子限额）**都是对"账号当时在哪个家族"的陈述**，
 * 所以"最新的那条陈述"就是当前家族。
 *
 * ## 为什么不给 kit 加一个显式的 active_family 字段
 *
 * 那要三仓联动发版（kit 打版 → 公开仓 go.mod 升版 → 私有仓 go.mod 升版），而这个信息**已经在
 * 响应里且已经正确**。为一个已存在的事实做一轮跨仓发布，不划算。
 *
 * 代价是我们依赖了一个**语义上没被契约写死**的字段（它文档上写的是"兼容投影的 family"，
 * 只是恰好等于最新家族）。所以这里把这份依赖**显式写出来并用测试钉住**；哪天 kit 真要改
 * `QuotaInfo.family` 的含义，改的人至少能从这段注释看到下游有人在这么用。
 *
 * ## 不确定时不下结论
 *
 * 顶层 family 为空（老快照、或该运行时压根没有家族概念）⟹ 一律返回 false。**不知道 ≠ 已作废**，
 * 与"查不到 ≠ 已是最新"是同一条纪律。
 */
export function isSupersededFamily(groupFamily: string, activeFamily: string): boolean {
  if (!groupFamily || !activeFamily) return false
  return groupFamily !== activeFamily
}

/** 被取代那一行的说明：说清**它是什么**，以及**该看哪一行**。 */
export function supersededNote(groupFamily: string, activeFamily: string): string {
  return `${groupFamily}：账号已切换到 ${activeFamily} 家族，此为历史读数`
}

/**
 * 一个 family 分组的**完整呈现决策**（组件只渲染，不判定）。
 *
 * 把"过期"与"已被取代"合成一处，是因为它们会撞在一起：一个已被取代的家族**通常也是过期的**，
 * 而这时该说的话是"这是旧家族的历史读数"（告诉你该看哪一行），不是"数据已过期，点击刷新"
 * （刷新它毫无意义——那个家族已经不再计费了）。**先判取代，再判过期。**
 */
export interface GroupPresentation extends StalePresentation {
  /** 折叠时显示的那一行小字。 */
  note: string
}

export function groupPresentation(opts: {
  stale: boolean
  ageSeconds: number
  allInferred: boolean
  runtime: string
  canProbe: boolean
  groupFamily: string
  activeFamily: string
}): GroupPresentation {
  if (isSupersededFamily(opts.groupFamily, opts.activeFamily)) {
    // 已被取代：不给"点击刷新"，给"该看哪一行"。
    return {
      dim: true,
      collapse: true,
      badge: '',
      hint: '',
      note: supersededNote(opts.groupFamily, opts.activeFamily),
    }
  }
  const base = stalePresentation(opts)
  const age = humanAge(opts.ageSeconds)
  return {
    ...base,
    note: `${opts.groupFamily || opts.runtime}：${age ? age + '无数据' : '无数据'}`,
  }
}
