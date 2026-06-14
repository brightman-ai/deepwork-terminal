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

export interface WSControlMessage {
  type: 'resize' | 'heartbeat' | 'heartbeat_ack' | 'auth_refresh' | 'shell_exit' | 'error' | 'preempted' | 'agent_state' | 'session_meta' | 'input' | 'tmux_nav' | 'tmux_state'
  payload?: Record<string, unknown>
}

// ─── tmux topology (backend agentintel.TmuxState; see WS0 contract) ──────────────
// Pushed via WS control frame { type: "tmux_state", payload: TmuxState } on ~1s diff,
// and fetched once via GET /tmux/state on mount. prefix.bytes is base64 of the tmux
// prefix control byte(s) — C-b → 0x02 ("Ag=="), C-a → 0x01 ("AQ==").
export interface TmuxPrefix { display: string; bytes: string }
export interface TmuxPaneState {
  index: number
  active: boolean
  title?: string
  agentTool?: AgentTool
  agentStatus?: AgentStatusType
}
export interface TmuxWindowState {
  index: number
  name: string
  active: boolean
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
  attached: boolean
  prefix: TmuxPrefix
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
  startedAt?: string
  updatedAt: string
}

export interface AgentIntelResponse {
  current: AgentState | null
  notifications: AgentState[]
}
