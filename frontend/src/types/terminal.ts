/**
 * BS-08 Terminal TypeScript types.
 * [Ref: T5-B3, BP-B3]
 */

export interface TerminalSessionInfo {
  id: string
  session_id?: string
  name: string
  title?: string
  engine?: string
  cwd?: string
  status: 'running' | 'idle' | 'exited'
  lastActive: string  // ISO 8601
  last_seen?: string
  createdAt?: string
  created_at?: string
}

/**
 * 收到 `replay_reset` 后写给终端的清屏序列：CUP(归位) + ED2(清屏) + ED3(清 scrollback)。
 *
 * **不能用 xterm 的 `reset()`** —— 它的文档原文是 "Perform a full reset (RIS, aka '\x1bc')"，
 * RIS 会把私有模式一并复位，包括鼠标跟踪 (1000/1002/1003/1006)。而 alt-screen 里的滚动**全靠**
 * 把滚轮转成 SGR 鼠标序列转发给 app —— 复位掉它，屏幕看着没问题，滚动却死了，而且看起来像终端里
 * 那个程序的 bug。这里只清内容，模式原样保留；服务端另外负责把 replay 截断丢掉的模式补回来
 * (见 Go 侧 carriedOverModes)。
 */
export const REPLAY_RESET_SEQUENCE = '\x1b[H\x1b[2J\x1b[3J'

/** 服务端宣布的网格尺寸。两个 payload 字段只有同时有效才有意义，所以一起解析、一起校验。 */
export interface ServerGrid { cols: number; rows: number }

/**
 * 从 `resized` 帧里解出网格；形状不对就返回 null。
 *
 * 两个消费者（CliTerminalSurface 与 useTerminalChannel）此前各写了一遍同样的窄化 + 校验。
 * 那不是巧合而是同一个契约被抄了两份：payload 是 `Record<string, unknown>`，所以「怎么把它
 * 读成两个正整数」这件事必须有人做，而做两遍就会漂——一边加了下界检查另一边没加，症状是网格
 * 偶尔被设成 0 然后整屏塌掉，而且只在其中一条路径上出现。
 */
export function parseServerGrid(payload: unknown): ServerGrid | null {
  const p = payload as { cols?: unknown; rows?: unknown } | null
  const cols = p?.cols
  const rows = p?.rows
  if (typeof cols !== 'number' || typeof rows !== 'number') return null
  if (!Number.isInteger(cols) || !Number.isInteger(rows)) return null
  if (cols < 1 || rows < 1) return null
  return { cols, rows }
}

export interface WSControlMessage {
  type: 'resize' | 'heartbeat' | 'heartbeat_ack' | 'auth_refresh' | 'shell_exit' | 'error' | 'preempted' | 'agent_state' | 'session_meta' | 'input' | 'tmux_nav' | 'tmux_state' | 'sessions_overview' | 'agent_signal' | 'resized' | 'replay_reset'
  payload?: Record<string, unknown>
}

// ─── tmux topology (backend agentintel.TmuxState; see WS0 contract) ──────────────
// Pushed via WS control frame { type: "tmux_state", payload: TmuxState } on ~1s diff,
// and fetched once via GET /tmux/state on mount. prefix.bytes is base64 of the tmux
// prefix control byte(s) — C-b → 0x02 ("Ag=="), C-a → 0x01 ("AQ==").
export interface TmuxPrefix { display: string; bytes: string }

/**
 * SurfaceUnit — 一个终端「单元」的 agent 事实（后端 agentintel.SurfaceUnit 的另一半）。
 *
 * 两个来源共用**同一份**声明：tmux 的一个 pane 是一个单元，一个非 tmux 的 session 也是一个单元。
 * 这里写成一个 interface 而不是在每个 payload 里各抄一遍字段，理由和服务端那侧一字不差——注释
 * 请求不了两个结构体相等，而这两份字段列表曾经真的漂了（statusRule 只有一边有过一阵子）。
 */
export interface SurfaceUnit {
  agentTool?: AgentTool
  agentStatus?: AgentStatusType
  /** Backend "needs-you": the agent finished a turn / is blocked and hasn't been responded
   *  to yet. Distinct from agentStatus==='idle' (which also covers a fresh, never-run pane).
   *  Survives reload (derived from transcript timestamps) and clears when you next respond. */
  awaitingUser?: boolean
  /** Transcript time of the completion behind `awaitingUser` (reload-proof). The Agent
   *  Overview's per-window "seen" layer dismisses against this: the same value across F5 keeps a
   *  cleared dot cleared; a new turn's newer value re-shows it. Absent = undated wait. */
  awaitingSince?: string
  /** That completed turn ended on a free-text question rather than a report. It re-LABELS the
   *  same needs-you dot ("有提问" vs "已完成") and never raises its severity: after end_turn the
   *  agent sits at an empty prompt, so nothing is blocked. Escalating this to a red `waiting` is
   *  what used to paint undismissable red dots on idle agents. */
  endedOnQuestion?: boolean
  /** 产生上面那条结论的**唯一**规则（"transcript.running" / "screen.approval" …）。诊断用，不渲染。 */
  statusRule?: string
  /** 只有「要你做点什么」的判定才带证据（匹配到的那行屏幕文字，已清洗截断）。 */
  statusEvidence?: string
  /** 这个单元的 agent 最后一次写 transcript 的时刻（ISO），不是服务端最后一次查看的时刻——
   *  后者永远是「刚刚」，读的人得不到任何信息。它是 agentStatus 这条结论的**证据年龄**：
   *  一个「运行中」配上「10 小时前」，不用看日志就知道有问题。缺省 = 没有 transcript 可问。 */
  activityAt?: string
}

/**
 * SurfaceCard — 一张总览卡片说的话（后端 agentintel.SurfaceCard）。
 *
 * 卡片**自己**带这些事实：tmux 那侧是它所有 pane 的 roll-up（服务端算，agentintel.RollUp），
 * 非 tmux 那侧只有一个单元、卡片的事实就是它的事实。这正是本轮消掉的层级错配——在此之前
 * 「这张卡在说什么」由前端 windowRawStatus/windowAwaiting/windowAwaitingSince/windowActivityAt/
 * windowTool/windowCwd 六个函数替 tmux 算一遍，而另一条来源因为 N=1 长得像赋值就直接读字段，
 * 于是同一条规则有两份实现，其中一份还写在离数据最远的语言里。
 *
 * **不含身份**（key/index/title/active）：一个 tmux window 的编号是 tmux server 的事实，而一张
 * 非 tmux 卡片的编号是**标签页的位置**，只有浏览器知道。让共享类型带上一边填不出的字段不是统一，
 * 是拿编译器给一句假话背书——所以身份由 cardToUnit 的调用方显式传入，摆在接缝处。
 */
export interface SurfaceCard extends SurfaceUnit {
  /** Last few lines of REAL output (agent chrome stripped server-side). */
  tail?: string[]
}

export interface TmuxPaneState extends SurfaceUnit {
  index: number
  active: boolean
  title?: string
  /** pane_current_path — the live working directory of this pane. */
  cwd?: string
  /** tmux's stable "%N" pane id — survives index reuse/reorder (a closed pane's index gets
   *  recycled by the next split), unlike `index`. The per-pane resource-drawer state is keyed on
   *  windowId+paneId, NOT index, so a closed-then-reopened pane never inherits stale drawer state. */
  paneId?: string
}
export interface TmuxWindowState extends SurfaceCard {
  index: number
  name: string
  /** Stable tmux window id ("@N") — survives index reuse/reorder. Seen-state keys on it. */
  windowId?: string
  active: boolean
  /** 这张卡的工作目录：活动 pane 的，没有就第一个 pane 的。服务端算（以前是前端的 windowCwd）。 */
  cwd?: string
  panes: TmuxPaneState[]
}
export interface TmuxSessionState {
  name: string
  attached: boolean
  windows: TmuxWindowState[]
}
export interface TmuxState {
  installed: boolean
  serverRunning: boolean
  /** 我们**看着的**那个 tmux server 不在了（不是「你从不用 tmux」）。缺席 = 没有这回事。
   *  只有服务端知道这件事：它跨页面刷新、跨重连、跨「事发时没人在看」都成立。 */
  serverVanished?: boolean
  attached: boolean
  /** tmux session name THIS shell's client is attached to ('' when detached). */
  attachedSession?: string
  prefix: TmuxPrefix
  /** Resolved global `mode-keys` ("vi" | "emacs") — selects the active copy-mode key
   *  table so semantic copy-mode motions map to the right keystroke. Absent on older hosts. */
  modeKeys?: string
  sessions: TmuxSessionState[]
}

// AgentState (legacy simplified) — use the full AgentState below instead.
// Kept as type alias for backward compatibility with components that only need basic fields.

export type FocusState = 'IDLE' | 'TERMINAL' | 'COMPOSE'
// [Ref: T5-B4.M3, CAP-terminal-interaction S2, DDC-05]

export type AnchorState = 'IDLE' | 'NO_ANCHOR' | 'HAS_ANCHOR_1' | 'HAS_BOTH'
// [Ref: T5-B4.M4, CAP-selection-copy S2, DDC-08]

export interface CellCoord {
  col: number
  row: number
  /** Buffer-absolute row (viewportY + row). Used for scroll-aware anchor tracking. */
  bufferRow?: number
}
// [Ref: CAP-touch-mouse S3, DDC-09]

export type WSConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'reconnecting' | 'preempted'

export type AgentTool = 'claude' | 'codex' | 'gemini' | 'opencode' | ''
export type AgentStatusType = 'none' | 'running' | 'idle' | 'waiting' | 'done'
export type WaitReasonType = '' | 'prompt' | 'permission' | 'question'

/** Full agent state from backend agent_intel system. */
export interface AgentState {
  tool: AgentTool
  status: AgentStatusType
  waitReason: WaitReasonType
  model: string
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheCreateTokens: number
  totalTokens: number
  tmuxWindow?: number | null
  tmuxPane?: number | null
  /** Backend "needs-you": finished a turn / blocked, not yet responded to. Mirrors
   *  agentintel.AgentState.AwaitingUser — the same signal TmuxPaneState carries per pane. */
  awaitingUser?: boolean
  /** That turn ended on a free-text question rather than a report. Re-labels the needs-you
   *  signal ("有提问" vs "已完成"); never raises its severity. */
  endedOnQuestion?: boolean
  startedAt?: string
  updatedAt: string
}

export interface AgentIntelResponse {
  current: AgentState | null
  notifications: AgentState[]
}
