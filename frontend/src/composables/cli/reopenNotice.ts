/**
 * reopenNotice — 「这个终端是自动重开出来的」这件事的投递箱，按 **session id** 存放。
 *
 * 为什么需要一个单独的模块：这件事的两端天生不在同一棵组件树上。
 *   • 知道「刚替你重开了一个」的是标签状态层（useCliState 的 reconcile → reopenDetached）；
 *   • 能把那行字写进屏幕、也最先知道「用户在这个终端里动手了」的是终端连接层
 *     （useWebSocketClient：它拿着这条 session 的输出回调和输入通道）。
 * 两端唯一的公共身份就是 session id，所以真相按 session id 存在这里，两端各取所需。这与
 * sessions_overview 帧交给模块级单例（useSessionsOverview）是同一个做法、同一个理由。
 *
 * 三条不变量：
 *   ① 那行字**只写一次**。写完就从待写队列里消失，重连、切标签都不会再冒出来一遍——
 *      它是一次告知，不是终端状态。
 *   ② 那行字**只走输出方向**。它由客户端合成后交给「收到输出」的那个回调（和 WS 收到 PTY
 *      字节走的是同一条路），**绝不进 sendBinary**：送进 PTY 就成了替用户敲了一行东西。
 *   ③ 用户在这个终端里第一次输入 → 标记消失。他既然已经动手了就说明看见了，标记再留着只是噪音。
 *      终端里那行字仍然留在滚动缓冲区里——那是历史，不该被抹掉。
 *
 * 刻意不持久化：重开是一次性事件，写进 storage 就会出现「上次记着重开过，这次其实是用户自己新建的」
 * 这类陈旧真相（tabLiveness.ts 里同一条理由）。
 */
import { reactive } from 'vue'

/** sessionId → 那行说明。在场 = 这个终端是自动重开的，且用户还没在里面动过手（标签上挂「已重开」）。 */
const notices = reactive<Record<string, string>>({})

/** 还没写进 xterm 的那些。与 notices 分开，是因为「写没写过」和「标记还挂不挂着」是两件事：
 *  字写完了标记还得留着（直到用户动手），而标记撤了以后也绝不该把字再写一遍。 */
const unwritten = new Set<string>()

/** 自动重开接上新 shell 时投递。同一条 session 重复投递没有意义，后来的覆盖前面的。 */
export function postReopenNotice(sessionId: string, line: string): void {
  if (!sessionId || !line) return
  notices[sessionId] = line
  unwritten.add(sessionId)
}

/** 终端连接层取走「还没写进屏幕」的那行字（一次性，见不变量 ①）。没有就返回 null。 */
export function takePendingReopenNotice(sessionId: string): string | null {
  if (!sessionId || !unwritten.has(sessionId)) return null
  unwritten.delete(sessionId)
  return notices[sessionId] ?? null
}

/** 标签栏读的：这条 session 现在还该不该挂「已重开」小标（顺带给出提示文案）。 */
export function reopenNoticeOf(sessionId: string | undefined): string | undefined {
  return sessionId ? notices[sessionId] : undefined
}

/** 用户在这个终端里输入了任何东西 → 撤掉标记（见不变量 ③）。每次按键都会调到，故先做廉价判断。 */
export function noteUserInput(sessionId: string): void {
  if (!sessionId || !(sessionId in notices)) return
  delete notices[sessionId]
  unwritten.delete(sessionId)
}

/** 标签被关掉 / session 作废时清理，别把条目留成永久泄漏。 */
export function forgetReopenNotice(sessionId: string | undefined): void {
  if (!sessionId) return
  delete notices[sessionId]
  unwritten.delete(sessionId)
}
