/**
 * PgUp / PgDn 该发给谁 —— **一条规则，两个入口**。
 *
 * ── 这个文件为什么存在 ──────────────────────────────────────────────────────────────────────────
 * 翻页键有两条路进来：
 *   · 屏幕上的按钮（工具条 / 底栏）→ `onSendKey('\x1b[5~')`
 *   · **物理键盘** → xterm 的 onData → `sendTerminalData(bytes)`
 *
 * 正确的行为只有一种，但它此前只写在**前一条**路上：普通缓冲区 + 没有 tmux 时，把翻页转成滚
 * xterm 自己的 scrollback；其余情况原样发给 PTY。物理键盘那条路直接发 PTY —— 而在普通缓冲区里
 * shell 根本不处理 `ESC [ 5 ~`，于是那个键**什么都不会发生**。使用者按的是键盘，所以他遇到的就
 * 是一个死键。
 *
 * 一条规则写两遍，只写对一遍，是这条 bug 的全部内容。所以判断挪到这里，两个入口都调它 —— 单测
 * 断言"两条路走同一个函数"，它们就再也分不了家。
 */

/** 物理键盘上的 PgUp / PgDn 序列。 */
export type PageDirection = 'up' | 'down'

/** 认出 `ESC [ 5 ~`（PgUp）和 `ESC [ 6 ~`（PgDn）。其它一律 null。 */
export function pageKeyOf(data: Uint8Array | string): PageDirection | null {
  const b = typeof data === 'string' ? new TextEncoder().encode(data) : data
  if (b.length !== 4) return null
  if (b[0] !== 0x1b || b[1] !== 0x5b || b[3] !== 0x7e) return null
  if (b[2] === 0x35) return 'up'
  if (b[2] === 0x36) return 'down'
  return null
}

/** 判断需要知道的两件事：这块屏幕现在是不是备用缓冲区，以及这个标签有没有 attach 到 tmux。 */
export interface PageKeyContext {
  /** 备用缓冲区（全屏 TUI：claude-code / less / vim）。 */
  altScreen: boolean
  /** 这个标签的 shell 已经 attach 进 tmux。 */
  tmuxAttached: boolean
}

/**
 * 这个翻页键归谁。
 *
 *   · `'pty'`   —— 原样发给 PTY。备用缓冲区里 `less`/`vim`/claude 自己会翻页；tmux 里它归 tmux。
 *   · `'local'` —— 滚 xterm 自己的 scrollback。这是唯一一种"PTY 那边没有接收者"的情形，
 *                  也正是物理键盘此前变成死键的那一种。
 */
export function pageKeyTarget(ctx: PageKeyContext): 'pty' | 'local' {
  if (ctx.altScreen) return 'pty'
  if (ctx.tmuxAttached) return 'pty'
  return 'local'
}
