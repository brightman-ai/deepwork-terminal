/**
 * useSessionsOverview — the NON-tmux Agent Overview feed.
 *
 * Mirrors useTmuxState: the server pushes one `sessions_overview` control frame describing EVERY
 * session (status + live tail) on the active session's existing WebSocket, on a 1s diff-suppressed
 * ticker (see sessions_overview.go). This module holds the latest frame and maps it into the same
 * OverviewUnit shape tmux windows map into, so both feed ONE state machine and ONE card grid.
 *
 * Module-level singleton on purpose: the frame arrives inside the ACTIVE terminal surface, but the
 * overview is owned by the PORTAL (which renders the strip and the popover). A singleton is the
 * cheapest correct way to get it from the one to the other without threading props through the
 * whole tree, and matches how useServerStore already shares cross-component state here.
 */
import { computed, ref } from 'vue'
import { agentSaidText, agentSignalText, type OverviewUnit } from './useAgentOverview'
import { useAgentSignals } from './useAgentSignals'

/** One session's card payload — the wire shape of terminal.SessionOverviewEntry. */
export interface SessionOverviewEntry {
  id: string
  title?: string
  cwd?: string
  engine?: string
  agentTool?: string
  agentStatus?: string
  exited?: boolean
  tail?: string[]
  /** Finished a turn / blocked and not yet responded to — drives the amber "done-unseen" dot. */
  awaitingUser?: boolean
  /** Transcript time of that completion; the reload-proof key the seen layer dismisses against. */
  awaitingSince?: string
  /** That turn ended on a question rather than a report (labels the SAME dot, never escalates it). */
  endedOnQuestion?: boolean
}

const entries = ref<SessionOverviewEntry[]>([])

/** Apply a pushed `{ type: "sessions_overview" }` WS frame payload. */
export function applySessionsOverviewFrame(payload: unknown): void {
  if (Array.isArray(payload)) {
    entries.value = payload as SessionOverviewEntry[]
  }
}

/** One session's latest pushed entry. Exported so a surface outside the card grid (the tab strip)
 *  can answer "which agent is in this session" from the SAME frame the cards read — otherwise the
 *  strip would need its own detection and the two could name one terminal two different things. */
export function sessionEntry(id: string | undefined): SessionOverviewEntry | undefined {
  return id ? entries.value.find((e) => e.id === id) : undefined
}

/**
 * 这一帧里，这个 session 的原始状态。**唯一**一条推导 —— 下面的 `units`（喂标签点和总览卡片）
 * 和任何单独的消费者都走这里，所以「同一个终端两处显示不一致」在结构上就不可能发生。
 *
 * 一个已经退出的 PTY 永远是 idle，绝不是 running：进程都没了，谈不上在干活。
 */
export function sessionRawStatus(e: SessionOverviewEntry | undefined): OverviewUnit['rawStatus'] {
  if (!e || e.exited) return 'idle'
  if (e.agentStatus === 'waiting') return 'waiting'
  if (e.agentStatus === 'running') return 'running'
  return 'idle'
}

/** 这个 session 的状态文案（"Codex 运行中"）；'' = 这个终端没有 agent，没有状态可说。
 *  措辞来自 agentSignalText（全 app 同一套），状态来自 sessionRawStatus（全 app 同一条推导）。 */
export function entrySignalText(e: SessionOverviewEntry | undefined): string {
  if (!e) return ''
  return agentSignalText(e.agentTool, sessionRawStatus(e), e.awaitingUser, e.endedOnQuestion)
}

/** 按 session id 取上面那句话，给不在卡片网格里的消费者用（终端表面那一行）。 */
export function sessionSignalText(id: string | undefined): string {
  return entrySignalText(sessionEntry(id))
}

/**
 * 终端表面状态行要说的那一句话 —— 「这个终端此刻在发生什么」。
 *
 * 优先级，从高到低：
 *   1. **agent 自己喊的原话**（agentSaidText：「Codex 说：“任务已完成”」）—— 它说的 > 我们推断的；
 *   2. 否则，**值得说的时候**说一句我们推断的状态。「值得说」= 标签点会亮的那些状态；idle 不亮点，
 *      这里也就不写字（一个闲着的终端不需要一行常驻文字告诉你它闲着）；
 *   3. 都没有 → `''`，那一行就空着，不占高度。
 *
 * 为什么这个函数在这里、而不在组件里：状态行曾经读 `useAgentIntel(sessionId)` 的另一条推送，
 * 于是同一个 session 在同一时刻，底部这行写着红色「Codex 等待输入」、顶部标签点却是绿色「运行中」
 * （Human 实测截图）。两条判定路径就是两个真相。现在这一行和标签点读同一帧、同一条推导，
 * 逐字相等由 useSessionsOverview.test.ts 钉死。
 */
export function sessionNoteText(id: string | undefined): string {
  const e = sessionEntry(id)
  // 显式信号（BEL / OSC）是另一条帧：它带着 agent 的原话，而不是我们的判断，所以优先。
  const said = agentSaidText(useAgentSignals().signalFor(id), e?.agentTool)
  if (said) return said
  if (!e) return ''
  const raw = sessionRawStatus(e)
  return raw !== 'idle' || e.awaitingUser ? entrySignalText(e) : ''
}

/**
 * Sessions → overview units.
 *
 * `activeId` is the tab you're currently looking at: it drives the seen layer exactly like tmux's
 * pushed `active` flag, so switching to a finished terminal clears its "done" dot the same way.
 *
 * `order` (optional) is the visible tab order. It supplies each card's number AND, when non-empty,
 * DEFINES THE SET: a session with no tab is left out.
 *
 * That exclusion is load-bearing, not tidiness. The card number is simultaneously the tab strip's
 * 终端N and the prefix+N shortcut target, so it only means anything while the two sets are the
 * same. A server session with no tab has no position, so it used to fall back to its index in the
 * server's list — which collides with real positions (observed live: pills reading "1 2 2 3 3 6"
 * for three tabs) and makes "jump to card 2" ambiguous. It is also unreachable: there is no tab to
 * switch to. Better absent than present-but-lying.
 *
 * An EMPTY order means "we don't know the tab set yet" (still loading), not "no tabs" — filtering
 * on it would blank the overview, so that case degrades to showing everything in server order.
 */
export function useSessionsOverview(activeId: () => string | undefined, order?: () => string[]) {
  // 显式信号（BEL / OSC）是另一条帧、另一条真相：上面这些字段是我们推断出来的，它是程序自己喊的。
  // 这里只把它的**原话**贴到卡片上；状态一个字不改——后端已经把它落在 awaitingUser（琥珀）上，
  // 前端再抬一次就成了"分不清却夸大"。
  const { signalFor } = useAgentSignals()
  const units = computed<OverviewUnit[]>(() => {
    const ids = order?.() ?? []
    const position = new Map(ids.map((id, i) => [id, i + 1]))
    const visible = position.size ? entries.value.filter((e) => position.has(e.id)) : entries.value
    return visible.map((e, i) => {
      // A dead PTY is 'idle', never 'running' — an exited shell isn't working on anything.
      const raw: OverviewUnit['rawStatus'] = e.exited
        ? 'idle'
        : e.agentStatus === 'waiting'
          ? 'waiting'
          : e.agentStatus === 'running'
            ? 'running'
            : 'idle'
      // Same phrasing helper the tmux side uses, so a user who has both never learns two
      // dialects for one concept. `raw` (not e.agentStatus) so an exited PTY reads as idle here
      // exactly as its dot does.
      const signal = agentSignalText(e.agentTool, raw, e.awaitingUser, e.endedOnQuestion)
      return {
        key: e.id,
        index: position.get(e.id) ?? i + 1,
        // 原样带上服务端的名字，**不在这里编回落名**：卡面标题（自定义名 → cwd basename →
        // 终端N）由 overviewCardTitle 统一决定。以前这里回落成「终端 a1b2c3」，那串 id 前缀既
        // 认不出是哪个项目，又因为不长得像自动名而躲过了后面的 basename 回落。
        title: e.title ?? '',
        active: e.id === activeId(),
        cwd: e.cwd ?? '',
        tool: e.agentTool ?? '',
        rawStatus: raw,
        // Real transcript-derived needs-you, same as a tmux pane: the session is bound to its OWN
        // transcript by the shared PaneAgentMonitor, so this is per-session truth rather than the
        // "newest file in this directory" guess that used to make two terminals mirror each other.
        awaiting: !!e.awaitingUser,
        awaitingSince: e.awaitingSince ?? '',
        signals: signal ? [signal] : [],
        // '' when this session has no pending signal, or when the signal was a bare bell — the
        // card then reads exactly as it did before, rather than gaining an empty quote.
        agentSaid: agentSaidText(signalFor(e.id), e.agentTool),
        tail: e.tail ?? [],
      }
    })
  })

  return { entries, units }
}
