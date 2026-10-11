/**
 * 回看视口里两句"别让人误会"的话。
 *
 * 为什么单独成一个文件：这两句话是**判断**（什么时候说什么），不是排版，放在组件里就没法在没有
 * DOM 的 bun 测试里锁住它。
 */

/**
 * 视口里一行都没有时说什么。
 *
 * 有两种完全不同的原因会走到这里 —— 终端真的还没有滚出去的行，或者**取历史的请求没成功**
 * （认证过期、网络、服务端 5xx）。以前两者显示同一句"还没有滚出去的历史"，请求失败被伪装成
 * "这个终端没有历史"，使用者没有任何办法分辨。失败要说出来，并且说出原文。
 */
export function emptyHistoryText(error: string): string {
  return error
    ? `读取历史失败（${error}）——请重试；一直如此就刷新页面。`
    : '这个终端还没有滚出去的历史。'
}

/**
 * 当前程序在备用屏幕（全屏界面：Codex 的默认模式、vim、less、claude-code…）时的提醒；否则空串。
 *
 * 为什么必须说：备用屏幕不属于滚动历史 —— 常驻进程（muxd）按设计**不记录**它画的内容，回看拿到的
 * 是程序启动**之前**停放的那块普通屏幕。不说的话，使用者看到的是一屏陈旧的 shell 提示符（或者
 * 近乎空白），以为是回看坏了；其实这个程序的输出根本不在这份历史里，查看它只能走它自己的界面。
 *
 * 调用方负责 tmux：tmux 标签的历史来自 tmux 自己的 capture-pane，不受这条限制，不该提醒。
 */
export function altScreenNotice(altScreen: boolean): string {
  if (!altScreen) return ''
  return '当前程序运行在备用屏幕（全屏界面，如 Codex 默认模式 / vim / less）：它的输出不会进入历史，'
    + '这里只有它启动之前的终端内容。要看它自己的历史，请在程序里翻'
    + '（Codex：Ctrl+T 打开 transcript，再 PgUp/PgDn）；或让它以 inline 模式运行后重启'
    + '（Codex：config.toml 里 [tui] alternate_screen = "never"），之后滚轮、PageUp、回看都能用。'
}
