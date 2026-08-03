/**
 * useAgentOverview — the Agent Overview's derived state.
 *
 * The single place the seen-state STATE MACHINE lives; the pane bar renders a compact roll-up of
 * it and the overview grid the full view. Built from the pushed tmux_state (per-window
 * panes/agentStatus/tail) plus a small client-local "seen" layer.
 *
 * Status model (the herdr-inspired `seen` dimension):
 *   waiting      — a pane needs your input (highest signal)          → red,  top
 *   running      — an agent is actively working                      → green
 *   done-unseen  — went idle AFTER you last looked (finished, unread)→ teal
 *   idle         — idle and you've seen it since (or never active)   → grey
 *
 * "Seen" = you were looking at that window's real terminal: while the overview is CLOSED, the
 * tmux-active window is continuously marked viewed; closing the overview re-marks it too.
 *
 * Identity: seen-state is keyed on the tmux STABLE window id (`@N`), never the reusable window
 * index — so a closed window's index being reused can't make a fresh window inherit "done-unseen".
 * State for vanished windows is pruned each push (no leak, and a reused id starts clean).
 */
import { computed, ref, watch, type Ref } from 'vue'
import type { TmuxWindowState } from '@terminal/types/terminal'
import type { TabNotLive } from './tabLiveness'
// 「这个名字是用户起的，还是自动生成的占位」以及「终端N」这套措辞的 SSOT（标签栏也读它）。
import { displayTabName, isDefaultTabName } from './useTabDisplayName'

export type EffectiveStatus = 'waiting' | 'running' | 'done-unseen' | 'idle'

/** The ONE status→color mapping for every surface that renders an EffectiveStatus dot
 *  (pane bar / status sheet / overview grid). `idle` is deliberately absent — every
 *  consumer renders no dot at all for idle, so there is no color to define for it.
 *  Presentation metadata lives beside the value type it describes (DDD: the enum and
 *  its canonical rendering are one concept, not one-per-consumer). */
export const STATUS_COLOR: Record<Exclude<EffectiveStatus, 'idle'>, string> = {
  waiting: '#ff5252', // red — needs your input
  running: '#3fb950', // green — an agent is actively working
  'done-unseen': '#e3b341', // amber — finished, unread (distinct from running-green / waiting-red)
}

/** One status dot's pulse. The peak is ALWAYS opacity 1 (a dot never gets brighter than its
 *  STATUS_COLOR), so a pulse is fully described by how long one dim→bright→dim cycle takes, the
 *  curve it takes, and how far down it dims. Amplitude is therefore `1 - minOpacity` — the term
 *  the vocabulary below reasons in. */
export interface DotPulse {
  /** One full dim→bright→dim cycle (CSS `animation-duration`). */
  duration: string
  /** CSS `animation-timing-function`. */
  easing: string
  /** Opacity at the trough. `1` would be a no-op — express "static" as `null`, not as `1`. */
  minOpacity: number
}

/** Shared `@keyframes` token. Not load-bearing for the browser (each scoped style block still
 *  declares its own copy — Vue scopes keyframe names per-SFC); it exists so `rg -n
 *  'status-dot-pulse'` finds this export plus exactly its consumers, and a future ad-hoc 2nd
 *  motion vocabulary stands out immediately. Deliberately ONE keyframes for all statuses: it
 *  reads `--dot-min-opacity`, which each status rule sets from its own STATUS_MOTION entry, so
 *  two different rhythms come out of one curve definition rather than two rival ones. */
export const DOT_PULSE_KEYFRAMES = 'status-dot-pulse'

/** The ONE status→motion mapping, the exact peer of STATUS_COLOR above: color and motion are the
 *  two halves of what a dot SAYS, so they live together and every dot-rendering consumer (pane
 *  bar / status sheet / overview grid) binds both via `v-bind` CSS custom properties. Same
 *  anti-drift mechanism, no second one invented.
 *
 *  Keyed over every non-idle status and TOTAL over that set (a test pins the key list): `null`
 *  means "decided to be static", never "nobody got around to it". `idle` renders no dot at all,
 *  so it has neither a color nor a motion — same omission as STATUS_COLOR, same reason.
 *
 *  ── The three-state motion contract (Human, 2026-07-20; supersedes the original R3 rule that
 *  only `running` moved) ────────────────────────────────────────────────────────────────────
 *  | waiting     | 2.6s, 0.35↔1 | slow, big  | "I'm here waiting for you" — insistent, unhurried |
 *  | running     | 2.0s, 0.75↔1 | quick, small| "I'm still alive" — faint but continuous         |
 *  | done-unseen | static       | —          | a finished state expresses no liveness, and does
 *                                              not nag you either                                |
 *
 *  Why this replaced "only running breathes": that rule assumed the attention HUD carried the
 *  "who needs you" signal, so the red dot didn't have to. But the HUD is EVENT-scoped (8s, then
 *  it collapses) — for most of the time on screen, "who is waiting for you" is carried by the red
 *  dot alone, next to a green dot that was the only thing moving. An independent Witness read the
 *  result as backwards ("the ones waiting for me are still, the one I don't need to touch is
 *  blinking"), which is exactly what the geometry predicts.
 *
 *  Why the two rhythms don't fight — reason in SALIENCE, not in period. A slow opacity pulse's
 *  pull on the eye tracks its rate of luminance change, ≈ 2·amplitude/period:
 *    waiting  2·0.65/2.6 = 0.50 /s      running  2·0.25/2.0 = 0.25 /s
 *  Red changes twice as fast as green and swings 2.6× as far, on top of being the more salient
 *  hue — so the hierarchy is preserved with margin, in the same direction as before but no longer
 *  by making red mute. Keep that 2:1 salience ratio if you ever retune: raising `running`'s
 *  amplitude or dropping `waiting`'s is the exact regression this table exists to prevent.
 *
 *  Opacity-only by design: no box-shadow / background-color / width. Those force a repaint;
 *  opacity (and transform) compose on the GPU, so many dots pulsing at once costs no frames on
 *  mobile. Every consumer must also degrade all of this to static under
 *  `prefers-reduced-motion: reduce`. Do not vary a period with agent activity (no such signal
 *  exists), do not enlarge a dot to "add" motion, and do not introduce a third rhythm — a
 *  waiting-ish thing that pulses on some other schedule is what "two things randomly blinking"
 *  actually looks like. */
export const STATUS_MOTION = {
  waiting: { duration: '2.6s', easing: 'ease-in-out', minOpacity: 0.35 },
  running: { duration: '2s', easing: 'ease-in-out', minOpacity: 0.75 },
  'done-unseen': null,
} as const satisfies Record<Exclude<EffectiveStatus, 'idle'>, DotPulse | null>

/** The ONE urgency ordering, most-urgent-first — co-located with STATUS_COLOR/STATUS_MOTION for
 *  the same reason they are (an EffectiveStatus and its canonical ranking are one concept).
 *
 *  This is the array `groups` below literally iterates, so the overview grid's top-to-bottom order
 *  IS this constant — and `useAttentionHud`'s `urgencyRank` imports it rather than restating it, so
 *  the card's "jump to the worst one" cannot drift from the grid's top row. Reordering here
 *  reorders both, by construction; there is no second copy to forget.
 *  (Bound by `useAttentionHud.test.ts`'s "URGENCY_ORDER drives the overview groups" test — that
 *  test is what makes this comment a fact rather than a hope.) */
export const URGENCY_ORDER: readonly EffectiveStatus[] = ['waiting', 'running', 'done-unseen', 'idle']

/** Raw per-window status from its panes: any waiting → waiting; any running → running; else idle. */
export function windowRawStatus(w: TmuxWindowState): 'waiting' | 'running' | 'idle' {
  const panes = w.panes ?? []
  if (panes.some((p) => p.agentStatus === 'waiting')) return 'waiting'
  if (panes.some((p) => p.agentStatus === 'running')) return 'running'
  return 'idle'
}

/** Backend "needs-you": any pane finished a turn / is blocked and hasn't been responded to.
 *  This is the durable, reload-proof signal (transcript-derived) that replaces the old
 *  "witness the running→idle transition" heuristic — a pane already done at page load counts. */
export function windowAwaiting(w: TmuxWindowState): boolean {
  return (w.panes ?? []).some((p) => p.awaitingUser)
}

/** The transcript time of the awaiting pane's last completion — the reload-proof key the
 *  seen-layer dismisses against. '' when no pane is awaiting or the time is undated. */
export function windowAwaitingSince(w: TmuxWindowState): string {
  const p = (w.panes ?? []).find((p) => p.awaitingUser && isDatedSince(p.awaitingSince))
  return p?.awaitingSince ?? ''
}

/** The window's active-pane cwd (what the overview card shows). */
export function windowCwd(w: TmuxWindowState): string {
  const panes = w.panes ?? []
  return (panes.find((p) => p.active) ?? panes[0])?.cwd ?? ''
}

/**
 * 这个窗口里 agent 最后一次动的时刻（epoch ms；0 = 无从得知）。
 *
 * 取所有 agent pane 里最新的一个：一个窗口里只要还有 agent 在写，这个窗口就不是"陈的"。
 * 它服务于一件事——让状态可被人**证伪**。状态本身没有年龄时，「运行中」这三个字既可能是
 * 真的在跑，也可能是十小时前某条没人再写的 transcript 留下的，而这两者在屏幕上长得一模一样。
 */
export function windowActivityAt(w: TmuxWindowState): number {
  let newest = 0
  for (const p of w.panes ?? []) {
    // isDatedSince 是本仓库对「Go 的零时间会被序列化成 0001-01-01，不会被 omitempty 吃掉」
    // 这件事的既有共识（awaitingSince 一路都这么过滤）。一个定位不到 transcript 的 pane 正是
    // 会带着零时间过来，不挡住就会在提示框里显示"17755921 小时前"。
    if (!p.agentTool || !isDatedSince(p.activityAt)) continue
    const ms = Date.parse(p.activityAt)
    if (Number.isFinite(ms) && ms > newest) newest = ms
  }
  return newest
}

/** The window's active agent tool, if any (claude/codex badge). */
export function windowTool(w: TmuxWindowState): string {
  const panes = w.panes ?? []
  const active = panes.find((p) => p.active)
  return active?.agentTool ?? panes.find((p) => p.agentTool)?.agentTool ?? ''
}

/** Display name for an agent tool. '' stays '' (no agent → no name to show). */
export function agentToolLabel(tool: string | undefined): string {
  return tool === 'codex' ? 'Codex' : tool === 'claude' ? 'Claude' : (tool ?? '')
}

/**
 * The ONE phrasing of "what is this agent doing", shared by every surface that says it in words
 * (tmux pane attribution, non-tmux session cards, the per-surface status chip).
 *
 * Needs-you splits by HOW the turn ended: a question wants an answer, a report wants a glance.
 * Same dot, same urgency — only the wording differs, so the label carries the nuance the status
 * model deliberately refuses to carry as a separate colour. Co-located with STATUS_COLOR /
 * STATUS_MOTION for the same reason they are: colour, motion and WORDS are the three things a
 * status says, and a surface that invents its own wording is as much a drift as one that invents
 * its own hex.
 */
export function agentStatusLabel(
  status: string | undefined,
  awaitingUser?: boolean,
  endedOnQuestion?: boolean,
): string {
  if (status === 'waiting') return '等待输入'
  if (status === 'running') return '运行中'
  if (awaitingUser) return endedOnQuestion ? '有提问' : '已完成'
  return '空闲'
}

/** "Codex 运行中" — tool + status in one phrase, or '' when there is no agent to describe. */
export function agentSignalText(
  tool: string | undefined,
  status: string | undefined,
  awaitingUser?: boolean,
  endedOnQuestion?: boolean,
): string {
  const name = agentToolLabel(tool)
  return name ? `${name} ${agentStatusLabel(status, awaitingUser, endedOnQuestion)}` : ''
}

/** The three fields an explicit signal contributes to wording (see useAgentSignals.AgentSignal).
 *  Structural, not the wire type itself, so the phrasing family below stays free of that import —
 *  it is the same "words" concept as agentStatusLabel/agentSignalText, just a different source. */
export interface AgentSaidLike {
  title?: string
  body?: string
}

/**
 * The agent's OWN words, quoted — 「Codex 说：“任务已完成”」.
 *
 * Third member of the phrasing family above, and the only one that is not our judgement: the other
 * two describe what we INFERRED a session is doing, this one repeats what the program itself said
 * when it emitted an OSC notification. That difference is the entire value of the explicit-signal
 * path, so the wording marks it — quotes plus an attribution, so a glance separates "它说的" from
 * "我们推断的". Restrained on purpose: no icon, no exclamation, no severity of its own.
 *
 * Returns '' when there is nothing quotable — a bare BEL carries no text, and an empty quote
 * (「Claude 说：“”」 or a stray "undefined") says less than the existing agentSignalText wording the
 * caller falls back to. Never invent a body for a bell: we genuinely do not know what it wanted.
 *
 * `tool` is the detected engine (claude/codex) and wins over the signal's own title, which is the
 * emitting app's name and usually says the same thing twice ("Codex" / "Claude Code"). With neither,
 * the terminal itself is named as the speaker rather than dropping the attribution.
 */
export function agentSaidText(sig: AgentSaidLike | undefined, tool?: string): string {
  const body = (sig?.body ?? '').trim()
  const title = (sig?.title ?? '').trim()
  const quote = body || title
  if (!quote) return ''
  const named = agentToolLabel(tool) || (title && title !== quote ? title : '')
  const who = named || '终端'
  // 中英混排：拉丁名后补一个空格（"Codex 说"），中文名不补（"终端说"）。
  const gap = /[A-Za-z0-9)\]]$/.test(who) ? ' ' : ''
  return `${who}${gap}说：“${quote}”`
}

/** Per-pane attribution for split windows. A window-level red dot can legitimately come
 * from a background pane while its active pane is running; exposing the owner prevents the
 * signal from looking like a stale, undismissable status. */
export function windowAgentSignals(w: TmuxWindowState): string[] {
  return (w.panes ?? [])
    .filter((p) => p.agentTool)
    .map((p) => agentSignalText(p.agentTool, p.agentStatus, p.awaitingUser, p.endedOnQuestion))
}

/** Stable seen-state key: tmux window id (`@N`), falling back to index if a backend omits it.
 *  Exported because the attention HUD keys its ledgers on the SAME identity the seen layer uses —
 *  a second copy of this fallback would drift the moment either side changed. */
export function windowKey(w: TmuxWindowState): string {
  return w.windowId || `#${w.index}`
}

/**
 * OverviewUnit — the ONE thing an overview card represents, whatever produced it.
 *
 * The status model, the seen-state machine and the urgency grouping below are about "an agent
 * working somewhere you aren't looking" — a concept that has nothing to do with tmux. Binding them
 * to TmuxWindowState is what left non-tmux users with no overview at all (and then with a list
 * strictly worse than the tab strip). Both sources normalize INTO this shape:
 *   • tmux    → tmuxWindowsToUnits()   (a window, its panes collapsed to one card)
 *   • non-tmux→ sessionsToUnits()      (a PTY session, from the sessions_overview WS frame)
 * so there is exactly one state machine and one card renderer, not two of each.
 */
export interface OverviewUnit {
  /** Stable seen-state identity. tmux: window id (`@N`); cli: session id. Never a reusable index. */
  key: string
  /** Ordering + the "jump to N" number shown on the card. */
  index: number
  /** 原始名：tmux window 名 / session 标题 / 标签名，**可能是自动生成的占位**（pro 的 session
   *  名就是一个字不差的「终端」）。卡面显示的标题一律走 overviewCardTitle，不直接渲染这个字段。 */
  title: string
  /** You are currently looking at this unit's real terminal (drives the seen layer). */
  active: boolean
  cwd: string
  /** '' | 'claude' | 'codex' — the engine badge. */
  tool: string
  /** Backend-reported liveness, before the seen layer is applied. */
  rawStatus: 'waiting' | 'running' | 'idle'
  /** Backend "needs-you": finished a turn / blocked, not yet responded to. */
  awaiting: boolean
  /** Reload-proof completion time the seen layer dismisses against ('' = undated). */
  awaitingSince: string
  /** Per-agent attribution lines ("Claude 运行中"). Split tmux windows can have several. */
  signals: string[]
  /**
   * agent 自己喊的那一句，已成句：「Codex 说：“任务已完成”」（措辞见 agentSaidText）。
   *
   * 省略/'' = 这个单元当前没有显式信号，或者信号是一记裸 BEL（没带正文）——两种情况卡片都沿用
   * 既有措辞（signals / 徽章），绝不显示空引号。它只是**多出来的原话**，不改状态、不改颜色：
   * 显式信号在后端就落在 AwaitingUser（琥珀）上，前端这里一个字都不许把它抬成红色阻塞态。
   *
   * 只有 PTY session 卡片会有值：显式信号按 session 归属（一个 tmux window 里哪个 pane 响的铃，
   * 这条路径并不知道），所以 tmux 那侧留空而不是猜一个。
   */
  agentSaid?: string
  /**
   * 这个单元的 agent 最后一次**写 transcript** 的时刻（epoch ms）。省略/0 = 无从得知。
   *
   * 它是 rawStatus 这条结论的**证据年龄**。放进 OverviewUnit 而不是只放在 tmux 那侧，是因为
   * 「一个没有年龄的状态无法被证伪」对两种来源同样成立：一条十小时没人再写的 transcript 撑出来的
   * 「运行中」，和真的在跑，在屏幕上长得一模一样。非 tmux 来源暂时给不出这个值 → 省略，卡片就不显示，
   * 而不是编一个「刚刚」。
   */
  activityAt?: number
  /** Last few lines of REAL output (agent chrome already stripped, server-side). */
  tail: string[]
  /**
   * 第二个轴：这张卡背后**还有没有活着的进程**（见 tabLiveness.ts）。
   *
   * 省略 = 有（tmux window / 服务端推送的 session 天然都活着，两个既有来源一个字都不用改）。
   * 有值只可能是 detached / unreachable，由宿主为「没有 session 的标签」补出来的卡片携带——
   * 那种卡片永远不会出现在服务端帧里，但恰恰是最该被看见的（用户以为 agent 还在跑的就是它）。
   * 它不参与状态分组（没有进程就谈不上 waiting/running，rawStatus 就是 idle），只改卡面上那枚
   * 徽章的措辞，免得一个已经死掉的终端被写成「空闲」——那正是本次要消灭的那类半真话。
   */
  liveness?: TabNotLive
}

/** tmux windows → units. Reuses the window* accessors above so tmux's semantics (active pane
 *  wins for cwd/tool, any pane waiting → waiting) stay defined in exactly one place. */
export function tmuxWindowsToUnits(windows: TmuxWindowState[]): OverviewUnit[] {
  return windows.map((w) => ({
    key: windowKey(w),
    index: w.index,
    title: w.name,
    active: !!w.active,
    cwd: windowCwd(w),
    tool: windowTool(w),
    rawStatus: windowRawStatus(w),
    awaiting: windowAwaiting(w),
    awaitingSince: windowAwaitingSince(w),
    signals: windowAgentSignals(w),
    activityAt: windowActivityAt(w),
    tail: w.tail ?? [],
  }))
}

/** 路径的最后一段（'' / '/' → ''）。尾部斜杠先剃掉，否则 `/a/b/` 会得到空串。 */
function cwdBasename(cwd: string): string {
  const trimmed = (cwd || '').replace(/\/+$/, '')
  if (!trimmed) return ''
  const i = trimmed.lastIndexOf('/')
  return i >= 0 ? trimmed.slice(i + 1) : trimmed
}

/**
 * 卡片标题：**这张卡是哪个终端**，一眼可辨（布局之外的另一半 SSOT，两条数据源共用）。
 *
 * 优先级：用户自己起的名 → cwd 的 basename → 「终端N」。
 *
 * 为什么要有中间那一档：tmux 侧的窗口名（`ws portal 2` / `voice-code`）天然可扫读，而非 tmux 侧
 * 的 session 名默认就是一个字不差的「终端」——四张卡四个「终端」，编号又已经在左边的徽标里，标题
 * 那一行等于什么都没说（Human 实测）。cwd 的 basename 恰好是 tmux 用户自己敲出来的那种名字
 * （`deepwork-terminal`），所以拿它当默认名不是新发明，是把 tmux 侧行之有效的东西补给另一条路径。
 *
 * 「自动生成的占位名」的判定和「终端N」的措辞都取自 useTabDisplayName（标签栏同款），这里不另起
 * 一套词：卡片和标签必须是同一个称呼，否则用户要在两个地方认两个名字。
 */
export function overviewCardTitle(u: Pick<OverviewUnit, 'title' | 'cwd' | 'index'>): string {
  const raw = (u.title || '').trim()
  // 用户改过名就永远听用户的——哪怕它比 basename 短、比 basename 怪。
  if (raw && !isDefaultTabName(raw)) return raw
  const base = cwdBasename(u.cwd)
  if (base) return base
  // 连 cwd 都没有（例如进程已结束的标签）：退回带编号的占位，至少与标签栏、前缀+N 对得上。
  return displayTabName('终端', u.index)
}

export interface OverviewGroup {
  status: EffectiveStatus
  units: OverviewUnit[]
}

/**
 * PC 概览大卡网格的每行列数（布局几何的唯一规则，纯函数可单测）：卡片不足 3 张就按张数给列
 * （下限 1，免得 0 张时出一个 0 列的网格），3 张及以上恒 3 列、纵向滚动。
 *
 * **一套几何，两条数据源共用**：tmux 窗口和非 tmux session 都是 OverviewUnit，进的是同一张
 * 网格、同一个列数函数、同一个卡高（.ao-active 的 grid-auto-rows）。这里没有、也不许再有
 * "卡少的时候换一套排法"的分支。
 *
 * 曾经有过一个「少卡档」（≤4 个单元时：空闲也当大卡、单卡宽度封顶 520px、恰好 4 张排成 2×2
 * 田字格），初衷是"卡少时别太空旷"。Human 实测把它判死：4 个会话被排成 2×2、每张卡大到占满
 * 整屏、纵向几乎不滚动，一屏能扫到的会话反而比 tmux 的一行 3 张更少。结论写进规则：**卡片数量
 * 只决定用几列，永远不决定卡片多大**——留白比"看不到第 5 个会话"便宜得多。
 */
export function overviewColumns(cardCount: number): number {
  return Math.min(Math.max(1, cardCount), 3)
}

// ─── persisted "seen" layer (localStorage, device-local) ─────────────────────────────
// The needs-you dot's dismissal is keyed on the backend's reload-proof AwaitingSince
// timestamp, so "I've seen this completion" survives F5 — yet a NEW turn (new timestamp)
// re-shows the dot on its own (no running→re-arm heuristic needed). Module-level singleton
// + localStorage → one seen-state across every component instance AND across reloads.
// Device-local by design (needs-you = "seen on THIS device"); never touches the server store
// (avoids the cross-device merge + the server-store overwrite hazard).
const SEEN_STORAGE_KEY = 'needsYouSeen'

function loadSeen(): Record<string, string> {
  try {
    const raw = JSON.parse(localStorage.getItem(SEEN_STORAGE_KEY) || '{}')
    return raw && typeof raw === 'object' ? (raw as Record<string, string>) : {}
  } catch {
    return {}
  }
}

function saveSeen(map: Record<string, string>): void {
  try {
    localStorage.setItem(SEEN_STORAGE_KEY, JSON.stringify(map))
  } catch {
    /* private-mode / quota — in-memory seen still works for this session */
  }
}

/** A dated (non-zero) AwaitingSince. A tmux "zero" time (`0001-01-01…`, from omitempty not
 *  applying to time.Time) or an absent value means the wait couldn't be dated (e.g. a PTY-only
 *  permission prompt) → treated as "unknown completion": never persist-dismissable, so such a
 *  high-signal wait keeps showing (incl. across F5) rather than being wrongly muted.
 *
 *  NOT a rare edge case, and not a bug to be fixed upstream: the backend contract-tests the zero
 *  value (`agentintel/awaiting_since_contract_test.go`), every PTY-derived wait carries it, and
 *  `CodexDriver` never emits a driver-side waiting at all — so for Codex, EVERY waiting is undated.
 *  Exported because the attention HUD must branch on the SAME predicate: one policy for "we cannot
 *  tell two of these apart", not one per consumer. The policy is fail-OPEN (keep showing / stay
 *  interruptible), because the alternative silently swallows the highest-signal state in the app. */
export function isDatedSince(since: string | undefined): since is string {
  return !!since && !since.startsWith('0001-01-01')
}

/**
 * The tmux entry point. Signature and behavior are UNCHANGED — it maps windows to units and
 * delegates to the shared core, so every tmux consumer and its tests are untouched while the
 * state machine itself becomes source-agnostic. Non-tmux callers use useOverviewUnits directly.
 */
export function useAgentOverview(windows: Ref<TmuxWindowState[]>, overviewOpen: Ref<boolean>) {
  const units = computed(() => tmuxWindowsToUnits(windows.value))
  const core = useOverviewUnits(units, overviewOpen)
  // effectiveStatus/dismiss keep taking a WINDOW so every existing tmux caller (pane bar, status
  // sheet, surface, their tests) is untouched by the generalization. The seen layer keys on
  // windowKey either way, so a window and its unit are the same identity.
  return {
    ...core,
    effectiveStatus: (w: TmuxWindowState): EffectiveStatus => core.effectiveStatus(tmuxWindowsToUnits([w])[0]),
    dismiss: (w: TmuxWindowState): void => core.dismiss(tmuxWindowsToUnits([w])[0]),
  }
}

export function useOverviewUnits(windows: Ref<OverviewUnit[]>, overviewOpen: Ref<boolean>) {
  // Device-local "seen" layer over the backend's reload-proof "needs-you" (awaitingUser +
  // awaitingSince). A finished window keeps its dot until you've SEEN this completion — "seen" =
  // the window became ACTIVE (you switched to it), keyed on the pushed `active` flag so a native
  // ctrl+b N switch clears it exactly like a pane-bar tap. Persisted to localStorage keyed on the
  // completion's AwaitingSince, so it survives F5 yet a later turn's newer timestamp re-shows it —
  // no running-transition witness needed. Single call site → this ref is the shared seen-state.
  const seen = ref<Record<string, string>>(loadSeen())
  function persistSeen(): void {
    saveSeen(seen.value)
  }

  // Session-scoped dismissal for UNDATED waits (see isDatedSince). Those carry no timestamp, so
  // there is nothing to persist a dismissal against and nothing a later turn could invalidate —
  // storing one would mute the unit forever, which is exactly why the dated path refuses to.
  // Keeping it in memory gives the user the action they expect ("I've handled this, stop nagging")
  // while a reload honestly re-asks, and the entry is dropped the moment the unit stops awaiting
  // so the NEXT completion shows up again on its own.
  const undatedSeen = ref<Set<string>>(new Set())

  watch(
    windows,
    (wins) => {
      const live = new Set<string>()
      let changed = false
      for (const w of wins) {
        live.add(w.key)
        // Stopped awaiting → re-arm the undated dismissal, so its next completion is visible.
        if (!w.awaiting) undatedSeen.value.delete(w.key)
        // You're viewing a finished window (it's active; overview closed so the terminal is what
        // you see) → seen: remember THIS completion's timestamp. `active` comes from the topology
        // push, so a native ctrl+b switch clears it exactly like a pane-bar tap. No running→re-arm
        // needed: a new turn carries a newer AwaitingSince, so the stored one stops matching and
        // the dot returns on its own — reload-proof, because both sides are transcript-derived.
        if (w.active && !overviewOpen.value && w.awaiting) {
          const k = w.key
          const since = w.awaitingSince
          if (since && seen.value[k] !== since) {
            seen.value[k] = since
            changed = true
          }
        }
      }
      // Prune vanished windows — no leak; a reused id starts clean.
      for (const k of Object.keys(seen.value)) {
        if (!live.has(k)) {
          delete seen.value[k]
          changed = true
        }
      }
      for (const k of undatedSeen.value) {
        if (!live.has(k)) undatedSeen.value.delete(k)
      }
      if (changed) persistSeen()
    },
    { immediate: true, deep: true },
  )

  /** Explicit "handled — hide it" for a unit (e.g. tapping its overview card).
   *
   *  Dated wait → persisted against that timestamp; no re-arm needed, because its next turn's
   *  newer AwaitingSince won't match the stored one and the dot returns by itself.
   *  Undated wait → session-scoped (see undatedSeen). Before this, dismiss() simply returned for
   *  undated waits, which read to the user as a dot that "去除不掉". */
  function dismiss(w: OverviewUnit): void {
    const since = w.awaitingSince
    if (!isDatedSince(since)) {
      if (w.awaiting) undatedSeen.value = new Set(undatedSeen.value).add(w.key)
      return
    }
    if (seen.value[w.key] === since) return
    seen.value[w.key] = since
    persistSeen()
  }

  function effectiveStatus(w: OverviewUnit): EffectiveStatus {
    const raw = w.rawStatus
    if (raw !== 'idle') return raw // waiting (red) / running (green) come straight from the backend
    // Idle: "needs-you" (finished a turn, not yet responded) unless you've SEEN this exact
    // completion — stored AwaitingSince equals the current one. A later turn's newer timestamp
    // won't match → dot re-appears. An undated wait can't be matched that way, so it stays shown
    // by default (fail-open) but honors an explicit session-scoped dismissal.
    if (!w.awaiting) return 'idle'
    const since = w.awaitingSince
    if (isDatedSince(since)) return seen.value[w.key] === since ? 'idle' : 'done-unseen'
    return undatedSeen.value.has(w.key) ? 'idle' : 'done-unseen'
  }

  /** Units grouped by effective status, groups ordered by urgency, units by index within. */
  const groups = computed<OverviewGroup[]>(() => {
    const buckets = new Map<EffectiveStatus, OverviewUnit[]>()
    for (const w of windows.value) {
      const s = effectiveStatus(w)
      if (!buckets.has(s)) buckets.set(s, [])
      buckets.get(s)!.push(w)
    }
    return URGENCY_ORDER
      .filter((s) => buckets.has(s))
      .map((s) => ({
        status: s,
        units: buckets.get(s)!.slice().sort((a, b) => a.index - b.index),
      }))
  })

  /** Global counts for the roll-up summary line. */
  const rollup = computed(() => {
    const c: Record<EffectiveStatus, number> = { waiting: 0, running: 0, 'done-unseen': 0, idle: 0 }
    for (const w of windows.value) c[effectiveStatus(w)]++
    return c
  })

  /** Reactive index→effectiveStatus map so the always-on tmux bar can dot each window with the
   *  SAME seen-aware status the overview uses (incl. done-unseen) — one source, no recompute. */
  const statusByIndex = computed(() => {
    const m: Record<number, EffectiveStatus> = {}
    for (const w of windows.value) m[w.index] = effectiveStatus(w)
    return m
  })

  return { effectiveStatus, groups, rollup, statusByIndex, dismiss }
}
