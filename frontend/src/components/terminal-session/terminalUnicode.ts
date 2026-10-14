/**
 * terminalUnicode — 终端按哪一版 Unicode 量字符宽度。
 *
 * xterm.js 默认是 Unicode 6：那一版里 emoji（🤖 💰 🧠 ✅ 😀 …）都是 1 格。可 Claude Code / Codex 这些程序
 * 排版时按"emoji 占 2 格"算（Unicode 9 起的 East Asian Width / Emoji_Presentation），于是它们在状态行里
 * 一对齐，xterm 这边每个 emoji 少算一格，整行后面的字全部错位一格。
 *
 * `@xterm/addon-unicode11` 把宽度表换成 Unicode 11 的 —— emoji 2 格，和程序的假设一致。
 *
 * **它的边界要说清楚**：Unicode 11 表只管"码点本身的宽度"。⏺ ✻ ⚠️ 这类默认文本呈现的符号（以及靠
 * U+FE0F 才变成 emoji 呈现的序列）在这张表里仍是 1 格；要让它们也变 2 格需要按字素簇算宽度
 * （`@xterm/addon-unicode-graphemes`，6.0.0 对应的稳定版尚不存在）。这里不假装解决了它。
 *
 * 必须在**第一次 write 之前**生效，且 `terminal.unicode` 属于提议 API，需要 `allowProposedApi: true`
 * （XtermTerminal 构造 Terminal 时已经开着）。拆成独立函数，是因为 XtermTerminal.vue 没法在没有 DOM 的
 * 测试里挂载，而这条配置恰恰是"悄悄丢了也没人发现"的那一类 —— 没有它终端照样能用，只是 emoji 错位。
 */
import { Unicode11Addon } from '@xterm/addon-unicode11'
import type { Terminal } from '@xterm/xterm'

export const TERMINAL_UNICODE_VERSION = '11'

export function enableUnicode11(term: Pick<Terminal, 'loadAddon' | 'unicode'>): void {
  term.loadAddon(new Unicode11Addon())
  term.unicode.activeVersion = TERMINAL_UNICODE_VERSION
}
