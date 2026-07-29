/**
 * tabLiveness — 「这个标签背后还有没有一个活着的进程」的 SSOT。
 *
 * 为什么要有这一层：标签栏原先只有一个轴——agent 在干嘛（EffectiveStatus: waiting/running/
 * done-unseen/idle）。那个轴**默认前提是进程还活着**。服务重启后进程全没了，reconcile 却悄悄
 * POST /sessions 开一个全新的空 PTY 顶上去，屏幕上和「恢复成功」一模一样：用户以为 agent 还在，
 * 其实早没了。这里补的是缺的那个轴，而不是给旧轴加一档。
 *
 * 两个轴的关系（重要，别混）：
 *   liveness = live       → 有活着的 PTY，EffectiveStatus 才有意义，照常画彩色状态点
 *   liveness ≠ live       → 没有活着的 PTY，EffectiveStatus 无从谈起，画的是本文件的中性灰点
 * 所以这不是「第四套状态词汇」，是第一套**存活**词汇；agent 状态那套（STATUS_COLOR /
 * STATUS_MOTION / URGENCY_ORDER，见 useAgentOverview.ts）一个字都没动，本文件刻意长成它的形状
 * （同样的 Record<非默认态, …> 结构、同样的 DotPulse 类型、同样的 null = 明确决定静止），
 * 这样两套东西并排放着一眼能看出是一家人，也一眼能看出谁管哪个轴。
 *
 * 运行时状态，**不持久化**：写进 workbench 配置就会出现「上次记着 detached，这次其实活着」的
 * 陈旧真相。重载时 reconcile 重算才是 SSOT（见 reconcileTabs.ts）。
 *
 * 行业共识是「绝不假装」：Zellij 对断开的会话挂 "Press ENTER to run…" 而不是自己重跑；iTerm2 恢复
 * 的是**内容**并明写 "Session Restored" 横幅；VS Code 严格区分 reconnect（真接回原进程）与 revive
 * （只还原了外壳）。这里照办。
 */
import type { DotPulse } from './useAgentOverview'

/**
 * 一个标签背后进程的存活态。
 *   live        — 有一个活着的 PTY 绑在这个标签上
 *   detached    — 进程已经结束了（服务重启 / PTY 被杀），确认没了，不会自己回来
 *   unreachable — 问不到那台机器（本机 API 没答 / 远程 peer 离线），所以**不知道**它还在不在
 *
 * detached 与 unreachable 的区别是认识论上的，不是程度上的：前者是「知道它死了」，后者是「不知道」。
 * 把不知道当成死了，就会劝用户新建终端，而对面那个 agent 可能正跑得好好的；把不知道当成活着，
 * 就退回了本文件要消灭的那个谎。所以必须是两个态。
 */
export type TabLiveness = 'live' | 'detached' | 'unreachable'

/** 除 live 外的态。live 不进任何表：它没有自己的颜色/文案，它就是「照常显示 agent 状态」。 */
export type TabNotLive = Exclude<TabLiveness, 'live'>

/**
 * 存活态 → 颜色。两个都是中性灰，且刻意不用任何一个 STATUS_COLOR 的色相。
 *
 * 为什么是灰而不是红：红在这套配色里已经被 waiting（等你输入）占了，含义是「你现在得做点什么」。
 * 一个进程已经结束的标签**不是故障**，也没在催你——它只是不活着了。用红会把它推到注意力金字塔尖，
 * 盖住真正等你的那个终端；用绿/琥珀则等于说它还在动。中性灰是唯一诚实的表达：在场，但不活着。
 *
 * 两档灰有别：detached 稍亮（确定的事实，值得看见），unreachable 更暗（连事实都还没拿到，
 * 更不该抢注意力）。
 */
export const LIVENESS_COLOR: Record<TabNotLive, string> = {
  detached: '#8b949e',
  unreachable: '#6e7681',
}

/**
 * 存活态 → 动效。两个都是 null（明确决定静止），和 STATUS_MOTION['done-unseen'] 是同一个约定：
 * null 表示「想清楚了就是不动」，不是「还没人来加」。
 *
 * 脉冲在这套语言里表达的是**存活**（"I'm still alive" / "I'm here waiting for you"）。一个已经
 * 结束的进程去脉冲，字面上就是在撒谎；一个联系不上的机器去脉冲，是在假装我们还在跟它通着话。
 */
export const LIVENESS_MOTION = {
  detached: null,
  unreachable: null,
} as const satisfies Record<TabNotLive, DotPulse | null>

/** 标签点/tooltip 的短词（人话，不是术语；标签栏地方就这么大）。 */
export const LIVENESS_LABEL: Record<TabNotLive, string> = {
  detached: '进程已结束',
  unreachable: '联系不上',
}

/** 大卡文案。按钮文案必须说清「点下去会发生什么」，别用「恢复」这种承诺不了的词。 */
export interface LivenessCopy {
  /** 标题：一句话说清发生了什么。 */
  headline: string
  /** 正文：为什么会这样 + 现在的处境（人话）。 */
  body: string
  /** 主按钮文案。 */
  primary: string
  /** 主按钮下方的一行小字：点下去到底会发生什么。 */
  primaryHint: string
  /** 次按钮文案。 */
  secondary: string
}

/** 生成大卡文案。cwd/机器名有就写进去——「在此目录新建终端」得让人看见是哪个目录。 */
export function livenessCopy(
  liveness: TabNotLive,
  ctx: { remote?: boolean; machineLabel?: string; cwd?: string } = {},
): LivenessCopy {
  const where = ctx.remote ? (ctx.machineLabel || '远端机器') : '本机'
  if (liveness === 'unreachable') {
    return {
      headline: `暂时联系不上${ctx.remote ? ` ${where}` : '这台机器'}`,
      body: ctx.remote
        ? `没能问到 ${where} 上还有哪些终端，所以现在无法确定原来的进程还在不在。它有可能还在那边跑着——别急着新建，等这台机器回来再看一眼。`
        : '没能问到服务端还有哪些终端，所以现在无法确定原来的进程还在不在。它有可能还在跑着——等连接恢复后再看一眼。',
      primary: '重新检查一次',
      primaryHint: '重新向服务端要一次终端清单，不会新建也不会结束任何进程',
      secondary: '关闭这个标签',
    }
  }
  const dir = ctx.cwd && ctx.cwd !== '~' ? ctx.cwd : ''
  return {
    headline: '这个终端里的进程已经结束了',
    body: ctx.remote
      ? `${where} 上原来这个终端的进程已经不在了（通常是那边的服务重启过）。里面运行的程序没有在后台继续跑，之前的输出也没有留下来。`
      : '原来在这个终端里运行的程序，已经随服务重启一起结束了。它没有在后台继续跑，之前的输出也没有留下来。',
    primary: dir ? '在此目录新建终端' : '在这里新建终端',
    primaryHint: dir
      ? `会在 ${dir} 下开一个全新的终端，沿用这个标签的名字；之前的程序不会被接回来`
      : `会在${ctx.remote ? ` ${where} 上` : '主目录下'}开一个全新的终端，沿用这个标签的名字；之前的程序不会被接回来`,
    secondary: '关闭这个标签',
  }
}

/**
 * 存活态的**唯一**判定：给定一个标签绑的 session id 和服务端当前的 session 集合，它算什么。
 *
 * `liveSessions === null` 的含义是「没问到」（请求失败/超时/peer 离线），不是「一个都没有」。
 * 这个区分是承重的：把「没问到」当成空集，服务端一抖动，所有标签就会被判成 detached，那是反方向的
 * 同一个谎。
 */
export function tabLivenessFrom(
  sessionId: string | undefined,
  liveSessions: ReadonlySet<string> | null,
): TabLiveness {
  if (liveSessions === null) return 'unreachable'
  if (sessionId && liveSessions.has(sessionId)) return 'live'
  return 'detached'
}
