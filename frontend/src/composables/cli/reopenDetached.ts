/**
 * reopenDetached — 本机标签背后的进程已经结束时，**直接重开一个 shell 并 cd 回它原来的目录**。
 *
 * 为什么不是弹一张卡片等用户点「在此目录新建终端」：那个问题的答案永远相同。一个答案永远相同的
 * 问题不该做成对话框——该做的是「做了，并让你看得见」。所以这里自动做掉，同时强制留下痕迹。
 *
 * 三条硬约束，缺一条这个模块就退化成它要取代的那个谎（服务重启后偷偷 POST /sessions 顶一个空
 * PTY 上去，屏幕上和「恢复成功」一模一样）：
 *
 *   ① **cd 回原来的 cwd**，不是主目录。用户的上下文就是那个目录；把他丢回 ~ 等于换了个地方。
 *   ② **绝不执行任何命令**。只开 shell。上一个进程在跑什么我们无从得知，替他重跑是灾难；
 *      即使知道也不许——那是用户的决定（见「no-blackbox execution」）。
 *   ③ **必须留痕**。留痕是它和被删掉的静默重建唯一的区别。所以 adopt() 的签名把 notice 和
 *      sessionId 绑在同一次调用里：想接上新 shell 就必须同时交出那行说明，省不掉。
 *
 * 痕迹由**客户端写进 xterm**（照抄 CliTerminalSurface 里 `xtermRef.write('\r\n[进程已退出]\r\n')`
 * 的先例），不送进 PTY：送进 PTY 就成了「替用户敲了一行东西」，既污染 shell 历史，也违反约束 ②。
 */
import type { TabLiveness } from './tabLiveness'

/** 自动重开只看这几个字段；调用方的标签类型比这胖，胖的部分与这件事无关。 */
export interface ReopenCandidate {
  id: string
  /** 标签名。新 shell 沿用它，所以位置、编号、快捷键都不动。 */
  name: string
  /** 这个标签原来的工作目录。空 = 只知道是主目录。 */
  cwd?: string
  /** 有值 = 这个标签的进程长在别的机器上。远程一律不自动重开，见 shouldAutoReopen。 */
  remotePeerId?: string
  /** 对账结论（见 tabLiveness.ts）。 */
  liveness: TabLiveness
}

/**
 * 自动重开要用到的全部外部能力。只有两个口子，且第二个口子强制带痕迹。
 */
export interface ReopenPorts {
  /** 在 tab.cwd 下开一个新 shell，返回新的 session id；`null` = 没开成。 */
  createSession(tab: ReopenCandidate): Promise<string | null>
  /**
   * 把新 shell 接到这个标签上，**并同时交付那行痕迹**。
   *
   * 两件事写在一次调用里是刻意的：分成 bind() + writeNotice() 两步，任何一次重构都可能只留下
   * 前者——那正好就是被删掉的静默重建。签名不允许。
   */
  adopt(tabId: string, sessionId: string, notice: string): void
}

/**
 * 客户端写进终端的那一行。措辞要能同时回答用户此刻的三个问题：
 * 之前那个东西哪去了 / 我现在看到的是什么 / 我在哪个目录。
 */
export function reopenNoticeLine(cwd?: string): string {
  const dir = cwd && cwd !== '~' ? cwd : ''
  const where = dir ? `已切回 ${dir}` : '已回到主目录 ~'
  return `\r\n[上一个进程已随服务重启结束，这是一个新的 shell（${where}）]\r\n`
}

/**
 * 该不该替这个标签自动重开。
 *
 *   • 只对**确认结束**（detached）的标签动手。unreachable 是「没问到」，不是「死了」——那台机器
 *     上的 agent 可能正跑着，此刻新建一个顶上去就是反方向的同一个谎。
 *   • 只对**本机**标签动手。远程标签的进程长在别人机器上：我们既不知道那边现在是什么状态，
 *     也不该未经允许就在别人机器上开进程。远程仍然走 DetachedTerminalCard，把选择交还用户。
 */
export function shouldAutoReopen(tab: ReopenCandidate): boolean {
  return tab.liveness === 'detached' && !tab.remotePeerId
}

/**
 * 对一批标签执行自动重开。开不成的**保持 detached**——不重试、不假装成功，让
 * DetachedTerminalCard 如实说话（"没能完成"比"看起来好了"强）。
 */
export async function reopenDetachedTabs(
  tabs: readonly ReopenCandidate[],
  ports: ReopenPorts,
): Promise<void> {
  for (const tab of tabs) {
    if (!shouldAutoReopen(tab)) continue
    const sessionId = await ports.createSession(tab)
    if (!sessionId) continue
    ports.adopt(tab.id, sessionId, reopenNoticeLine(tab.cwd))
  }
}
