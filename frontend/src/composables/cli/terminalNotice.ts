/**
 * terminalNotice — 「有一行话要写进某条 session 的终端」的投递箱，按 **session id** 存放。
 *
 * 为什么需要一个单独的模块：这件事的两端天生不在同一棵组件树上。
 *   • 知道「有话要说」的是上层（standalone 的标签状态层 useCliState → reopenDetached；
 *     pro 的进场落脚判定 useCliV2 → routedEntry）；
 *   • 能把那行字写进屏幕、也最先知道「用户在这个终端里动手了」的是终端连接层
 *     （useWebSocketClient：它拿着这条 session 的输出回调和输入通道）。
 * 两端唯一的公共身份就是 session id，所以真相按 session id 存在这里，两端各取所需。这与
 * sessions_overview 帧交给模块级单例（useSessionsOverview）是同一个做法、同一个理由。
 *
 * 本职就一句：**把一次性的一行话投递进某条 session 的终端**（postTerminalNotice）。
 * 「这个终端是自动重开出来的」只是它的第一个调用方 —— 那个场景除了写字还要在标签上挂一枚
 * 「已重开」小标，所以多包一层 postReopenNotice。**其余调用方只写字**（例如 pro 进场时发现你
 * 点名的终端已随重启结束、把你落到另一个终端上的那句告知）：那个终端并没有被重开，替它点亮
 * 「已重开」小标会是新的一句谎。两件事因此分成两个入口，而不是绑成一件。
 *
 * 三条不变量：
 *   ① 那行字**只写一次**。写完就从待写队列里消失，重连、切标签都不会再冒出来一遍——
 *      它是一次告知，不是终端状态。
 *   ② 那行字**只走输出方向**。它由客户端合成后交给「收到输出」的那个回调（和 WS 收到 PTY
 *      字节走的是同一条路），**绝不进 sendBinary**：送进 PTY 就成了替用户敲了一行东西。
 *   ③ 用户在这个终端里第一次输入 → 标记消失，同时放弃还没写出去的那行。他既然已经动手了就
 *      说明他正看着这块屏幕：标记再留着只是噪音，而此刻再把一行字插进他正敲的东西中间更糟。
 *      已经写出去的那行仍然留在滚动缓冲区里——那是历史，不该被抹掉。
 *
 * 投递时机（调用方必须遵守）：要赶在那条 session 的终端**就绪之前**投递。终端连接层是在
 * xterm ready 的那一刻来取信的（useWebSocketClient.onMessage），而 xterm 只在它所在的标签
 * 成为当前标签之后才初始化（XtermTerminal 的 initTerminal）。所以 standalone 先投递再
 * bindSession，pro 先投递再 router.replace 到落脚的那个终端——反过来就会投进一个再也不会有人
 * 来取的箱子。
 *
 * 刻意不持久化：这类告知都是一次性事件，写进 storage 就会出现「上次记着重开过，这次其实是用户
 * 自己新建的」这类陈旧真相（tabLiveness.ts 里同一条理由）。
 */
import { reactive } from 'vue'

/** sessionId → 还没写进那条 session 终端的那一行。取走即消费（见不变量 ①）。 */
const pending = reactive<Record<string, string>>({})

/** sessionId → 「这个终端是自动重开出来的」这枚标记（顺带存提示文案）。在场 = 标签上挂「已重开」，
 *  且用户还没在里面动过手。与 pending 分开，是因为「写没写过」和「标记还挂不挂着」是两件事：
 *  字写完了标记还得留着（直到用户动手），而标记撤了以后也绝不该把字再写一遍。 */
const reopened = reactive<Record<string, string>>({})

/**
 * 排队写一行进这条 session 的终端。同一条 session 重复投递没有意义，后来的覆盖前面的。
 *
 * 只做这一件事：**不**在标签上留任何标记。谁要标记谁自己加（见 postReopenNotice）。
 */
export function postTerminalNotice(sessionId: string, line: string): void {
  if (!sessionId || !line) return
  pending[sessionId] = line
}

/** 自动重开接上新 shell 时投递：写那行字（上面），**再**给这条 session 挂上「已重开」小标。 */
export function postReopenNotice(sessionId: string, line: string): void {
  if (!sessionId || !line) return
  postTerminalNotice(sessionId, line)
  reopened[sessionId] = line
}

/** 终端连接层取走「还没写进屏幕」的那行字（一次性，见不变量 ①）。没有就返回 null。 */
export function takePendingTerminalNotice(sessionId: string): string | null {
  if (!sessionId) return null
  const line = pending[sessionId]
  if (line === undefined) return null
  delete pending[sessionId]
  return line
}

/** 标签栏读的：这条 session 现在还该不该挂「已重开」小标（顺带给出提示文案）。 */
export function reopenNoticeOf(sessionId: string | undefined): string | undefined {
  return sessionId ? reopened[sessionId] : undefined
}

/** 用户在这个终端里输入了任何东西 → 撤标记 + 放弃待写的那行（见不变量 ③）。每次按键都会调到，
 *  故先做廉价判断。 */
export function noteUserInput(sessionId: string): void {
  if (!sessionId) return
  if (sessionId in reopened) delete reopened[sessionId]
  if (sessionId in pending) delete pending[sessionId]
}

/** 标签被关掉 / session 作废时清理，别把条目留成永久泄漏。 */
export function forgetTerminalNotice(sessionId: string | undefined): void {
  if (!sessionId) return
  delete reopened[sessionId]
  delete pending[sessionId]
}
