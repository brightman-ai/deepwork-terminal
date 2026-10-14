/**
 * composeIntercept — 「打字即成框」的拦截判定（terminal-input REQ-TI-01 / REQ-TI-02）。
 *
 * 全部是纯函数加一个小状态机，没有 DOM、没有 Vue：这条规则的价值在于**每一个不拦截的理由都能被
 * 单独钉住**。拦错一次的代价是用户的字没有进到他以为的地方，所以判定必须保守——任何一条事实拿不准，
 * 就放行（字照旧直达终端，和这个功能上线前一模一样）。
 *
 * ── 为什么要 PromptTracker ───────────────────────────────────────────────────────────────
 * 规格写的是「提示为空时首字符是 `/` `!` `#` 不拦截」。可我们**没有任何信号**能告诉我们 agent 的提示
 * 现在是不是空的（没有 shell integration，agent 的屏幕不可解析）。如果只看"这是不是第一个字符"而
 * 不记账，`/` 放行之后敲的第二个字符 `h` 又会被当成"第一个字符"拦进输入框——于是 `/help` 被劈成
 * 终端里一个 `/` 加输入框里一个 `help`。
 *
 * 所以这里记一笔账：**凡是直达终端的字节都过一遍 PromptTracker**，它给出"提示大概还空着吗"。
 * 这笔账只会往保守的方向错：拿不准（历史回溯、补全、删除类操作）就算"不空"，结果最多是退回上线前
 * 的直接打字，绝不会把半截输入劈开。
 */
import type { AgentStatusType, AgentTool, WaitReasonType } from '@terminal/types/terminal'

// ─── agent 事实 ────────────────────────────────────────────────────────────────────────────

/** 判定用到的 agent 事实。`status` 为空串 = 还没收到过推送（未知，按不可输入处理）。 */
export interface AgentInputFacts {
  tool: AgentTool
  status: AgentStatusType | ''
  waitReason: WaitReasonType
}

/**
 * agent 此刻是不是停在"可以输入文字"的状态：等下一条提示（idle），或正在运行（running ——
 * 它们运行中也接受排队输入）。
 *
 * waitReason=permission/question 是权限确认 / 选项菜单：这时敲的字是给菜单的（`y`、`2`、方向键），
 * 劈进输入框就是吞键。**waitReason 先于 status 判**：后端在 running 时会把 waitReason 清掉，
 * 所以 running 配着残留的 permission 是矛盾帧，宁可当成菜单。
 */
export function agentAcceptsTyping(agent: AgentInputFacts | null): boolean {
  if (!agent || !agent.tool) return false
  if (agent.waitReason === 'permission' || agent.waitReason === 'question') return false
  switch (agent.status) {
    case 'idle':
    case 'running':
      return true
    case 'waiting':
      // waiting 的本意是"需要人处理"（权限/提问）；只有明说是在等下一条提示才算可输入。
      return agent.waitReason === 'prompt'
    default:
      // none / done（进程已退出）/ 未知。
      return false
  }
}

/** 一个 tmux pane 里和输入判定有关的那两个字段（TmuxPaneState 的子集）。 */
export interface PaneAgentFacts {
  agentTool?: AgentTool
  agentStatus?: AgentStatusType
}

/**
 * 把"会话级 agent 推送"和"tmux 当前 pane"拼成判定要的事实。
 *
 * · 非 tmux：会话就是一个单元，直接读 useAgentIntel 那份。
 * · tmux：**会话级那份是 roll-up**（一个窗口里 shell 分屏挨着 agent 分屏时，它说"有 agent"）。
 *   光标所在的那个 pane 才是字真正会进去的地方，所以 tool / status 取 pane 的；pane 没带 waitReason，
 *   只有会话级推送说的正是同一个 tool 时才借它的 waitReason。pane 未知 → null（保守放行）。
 */
export function agentInputFacts(opts: {
  session: { tool: AgentTool; status: AgentStatusType; waitReason: WaitReasonType } | null
  tmuxAttached: boolean
  pane: PaneAgentFacts | null
}): AgentInputFacts | null {
  const { session, tmuxAttached, pane } = opts
  if (!tmuxAttached) {
    return session ? { tool: session.tool, status: session.status, waitReason: session.waitReason ?? '' } : null
  }
  if (!pane || !pane.agentTool) return null
  return {
    tool: pane.agentTool,
    status: pane.agentStatus ?? '',
    waitReason: session && session.tool === pane.agentTool ? (session.waitReason ?? '') : '',
  }
}

// ─── 按键分类 ──────────────────────────────────────────────────────────────────────────────

/** 一次 keydown 里判定要用的最小字段（测试可以喂纯对象）。 */
export interface KeyFacts {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
  isComposing?: boolean
  keyCode?: number
}

/**
 * text         一个可见字符
 * whitespace   空格 / 制表等不可见的单字符键（规格写的是"第一个**可见**字符"）
 * ime-start    输入法即将接管这一键（keyCode 229 / key=Process）——马上会开始拼写
 * ime-ongoing  已经在拼写中的按键，归 composition 事件管，不归 keydown
 * modified     带 Ctrl/⌘/Alt
 * non-text     方向、Tab、Esc、回车、退格、功能键……
 */
export type KeyClass = 'text' | 'whitespace' | 'ime-start' | 'ime-ongoing' | 'modified' | 'non-text'

export function classifyKey(k: KeyFacts): KeyClass {
  if (k.ctrlKey || k.metaKey || k.altKey) return 'modified'
  if (k.isComposing) return 'ime-ongoing'
  // keyCode 229 先于"可见字符"判：macOS Chrome 在输入法激活时，keydown.key 是真实字母（'n'）、
  // keyCode 却是 229——只看 key 会把"拼音第一键"当成直打的英文字母，字就绕过输入法直接拦下来了。
  if (k.keyCode === 229 || k.key === 'Process') return 'ime-start'
  if (Array.from(k.key).length !== 1) return 'non-text'
  return /\s/.test(k.key) ? 'whitespace' : 'text'
}

/** agent 自带补全菜单的前缀（Claude Code：命令 / bash 模式 / 记忆）。提示为空时敲它们，字得留在终端里。 */
const PROMPT_PREFIXES = new Set(['/', '!', '#'])

// ─── 判定 ──────────────────────────────────────────────────────────────────────────────────

export interface InterceptFacts {
  /** 设置里的总开关（REQ-TI-08）。 */
  enabled: boolean
  /** 这个 surface 是不是当前可见的那个（每个标签都挂着，都会收到这次按键）。 */
  surfaceActive: boolean
  /** 桌面端才有这个功能。 */
  desktop: boolean
  /** 输入框已经开着：输入归它，不存在"再拦一次"。 */
  composerOpen: boolean
  /** WS 连着。断着时进输入框的字发不出去，比"没拦"更糟。 */
  connected: boolean
  agent: AgentInputFacts | null
  /** 全屏程序（备用屏）：vim / less / 全屏 TUI，逐键直达。 */
  altScreen: boolean
  /** 回看（复制模式）打开。 */
  copyMode: boolean
  /** 终端内搜索条打开。 */
  searchOpen: boolean
  /** 总览浮层盖在终端上。 */
  overlayOpen: boolean
  /** 视口不在底部（用户正在往回翻）。 */
  scrolledBack: boolean
  /** PromptTracker 的结论：agent 的提示大概还是空的。 */
  promptEmpty: boolean
}

export type PassReason =
  | 'disabled' | 'inactive' | 'mobile' | 'composer-open' | 'disconnected'
  | 'not-agent' | 'agent-blocked'
  | 'alt-screen' | 'copy-mode' | 'search-open' | 'overlay-open' | 'scrolled-back'
  | 'modified-key' | 'non-text-key' | 'whitespace-key' | 'ime-ongoing'
  | 'command-prefix' | 'prompt-not-empty'

export type InterceptTrigger =
  | { kind: 'key'; key: KeyFacts }
  /** 输入法在终端的 helper textarea 上 compositionstart（Safari 的顺序：先 composition 后 keydown）。 */
  | { kind: 'composition' }

export type InterceptVerdict =
  | { intercept: true; mode: 'text'; text: string }
  | { intercept: true; mode: 'ime' }
  | { intercept: false; reason: PassReason }

const pass = (reason: PassReason): InterceptVerdict => ({ intercept: false, reason })

/**
 * 这次输入是拦进输入框，还是原样放行。
 *
 * 顺序有讲究：**环境类理由（开关、设备、模式、agent 状态）先于按键类理由**。这样"为什么没拦"总能
 * 归到最上游的那一条，也让每条理由在测试里只需要一个最小反例。
 */
export function decideIntercept(f: InterceptFacts, trigger: InterceptTrigger): InterceptVerdict {
  if (!f.enabled) return pass('disabled')
  if (!f.desktop) return pass('mobile')
  if (!f.surfaceActive) return pass('inactive')
  if (f.composerOpen) return pass('composer-open')
  if (!f.connected) return pass('disconnected')
  if (f.altScreen) return pass('alt-screen')
  if (f.copyMode) return pass('copy-mode')
  if (f.searchOpen) return pass('search-open')
  if (f.overlayOpen) return pass('overlay-open')
  if (f.scrolledBack) return pass('scrolled-back')
  if (!f.agent || !f.agent.tool) return pass('not-agent')
  if (!agentAcceptsTyping(f.agent)) return pass('agent-blocked')

  if (trigger.kind === 'composition') {
    // 拼写已经开始了，没有"第一个字符"可看，所以命令前缀（`/` `!` `#`）在这里无从判断 ——
    // 但"提示里已经有字了"不需要看字符：`/help` 敲到一半、粘贴过、历史回溯过之后，再开始拼写就是在
    // 同一行提示里续写，把这半截搬进输入框会把一条提示劈在两个编辑器里。和 keydown 路径同一条规矩。
    if (!f.promptEmpty) return pass('prompt-not-empty')
    return { intercept: true, mode: 'ime' }
  }

  const k = trigger.key
  switch (classifyKey(k)) {
    case 'modified': return pass('modified-key')
    case 'non-text': return pass('non-text-key')
    case 'whitespace': return pass('whitespace-key')
    case 'ime-ongoing': return pass('ime-ongoing')
    case 'ime-start':
      // 输入法激活时敲 `/`：key 仍是 '/'、keyCode 是 229。命令前缀照样得留给 agent 自己的菜单。
      if (f.promptEmpty && PROMPT_PREFIXES.has(k.key)) return pass('command-prefix')
      if (!f.promptEmpty) return pass('prompt-not-empty')
      return { intercept: true, mode: 'ime' }
    case 'text':
      if (!f.promptEmpty) return pass('prompt-not-empty')
      if (PROMPT_PREFIXES.has(k.key)) return pass('command-prefix')
      return { intercept: true, mode: 'text', text: k.key }
  }
}

// ─── PromptTracker ────────────────────────────────────────────────────────────────────────

export interface PromptTracker {
  /** agent 的提示大概还是空的？（只会往"不空"的方向错。） */
  isEmpty(): boolean
  /** 一段**直达终端**的字节（已解码成串）。凡是发进 PTY 的用户输入都该过这里。 */
  noteSent(data: string): void
  /** 已知提示被清空：输入框提交、agent 换了一个、会话重连后的新提示……。 */
  reset(): void
}

const PASTE_START = '\x1b[200~'
const PASTE_END = '\x1b[201~'

/**
 * 记账规则（宁可错在"不空"）：
 *
 *  加：可见字符 / 粘贴内容（按码点数）、换行（Ctrl-J、Alt-Enter）
 *  减：退格 一格（可见字符数永远 >= 真实剩余，所以只会多算）
 *  清：回车（提交）、Ctrl-C、Ctrl-U
 *  拿不准 → 标记 unknown，直到下一次"清"：Tab（补全）、方向键上/下（历史）、Ctrl-R、Ctrl-Y
 *  不动：光标移动、鼠标 / 焦点上报、终端对 DSR 等查询的回应、删词/删到行尾（只会让提示更空，
 *        而我们不敢据此宣布"空了"）
 */
export function createPromptTracker(): PromptTracker {
  let pending = 0
  let unknown = false

  function reset(): void {
    pending = 0
    unknown = false
  }

  function noteSent(data: string): void {
    let i = 0
    let inPaste = false
    while (i < data.length) {
      if (data.startsWith(PASTE_START, i)) { inPaste = true; i += PASTE_START.length; continue }
      if (data.startsWith(PASTE_END, i)) { inPaste = false; i += PASTE_END.length; continue }

      const cp = data.codePointAt(i)!
      const len = cp > 0xffff ? 2 : 1
      const ch = data.slice(i, i + len)

      if (inPaste) { pending++; i += len; continue }

      if (ch === '\x1b') { i = skipEscape(data, i); continue }
      if (ch === '\r') { reset(); i += 1; continue }
      if (ch === '\x03' || ch === '\x15') { reset(); i += 1; continue }
      if (ch === '\n') { pending++; i += 1; continue }
      if (ch === '\x7f' || ch === '\x08') { pending = Math.max(0, pending - 1); i += 1; continue }
      if (ch === '\t' || ch === '\x12' || ch === '\x19') { unknown = true; i += 1; continue }
      if (cp < 0x20) { i += 1; continue }
      pending++
      i += len
    }
  }

  /** 吃掉从 data[i]（一个 ESC）开始的一整条转义序列，返回下一个位置；副作用只有"历史回溯 → unknown"。 */
  function skipEscape(s: string, i: number): number {
    const next = s[i + 1]
    if (next === undefined) return i + 1 // 单独一个 ESC：不动

    if (next === '[') {
      // CSI：参数字节 0x30-0x3F，中间字节 0x20-0x2F，终止字节 0x40-0x7E
      let j = i + 2
      while (j < s.length && s.charCodeAt(j) >= 0x30 && s.charCodeAt(j) <= 0x3f) j++
      const paramsEnd = j
      while (j < s.length && s.charCodeAt(j) >= 0x20 && s.charCodeAt(j) <= 0x2f) j++
      if (j >= s.length) return s.length
      const final = s[j]
      if (final === 'M' && paramsEnd === i + 2 && j === paramsEnd) return Math.min(s.length, j + 4) // X10 鼠标：后面还有 3 字节
      if (final === 'A' || final === 'B') unknown = true // 方向键上/下 = 历史回溯，会往提示里塞字
      return j + 1
    }
    if (next === 'O') {
      // SS3（应用光标键模式下的方向键）
      const final = s[i + 2]
      if (final === 'A' || final === 'B') unknown = true
      return Math.min(s.length, i + 3)
    }
    if (next === ']') {
      // OSC：到 BEL 或 ST 为止
      let j = i + 2
      while (j < s.length) {
        if (s[j] === '\x07') return j + 1
        if (s[j] === '\x1b' && s[j + 1] === '\\') return j + 2
        j++
      }
      return s.length
    }
    if (next === '\r') { pending++; return i + 2 } // Alt-Enter：Claude Code 的换行键
    return i + 2 // Alt+字符：不动
  }

  return {
    isEmpty: () => pending === 0 && !unknown,
    noteSent,
    reset,
  }
}
