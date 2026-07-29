/**
 * useAgentSignals — the client half of the EXPLICIT "I need you" signal path.
 *
 * Every other attention signal in this app is INFERRED (transcripts, screen scrapes, tmux pane
 * diffs). This one is not: the program inside the PTY emitted a BEL or an OSC 9 / 777 / 99 desktop
 * notification, which is the one moment it states its own intent — and an OSC notification carries
 * the agent's OWN WORDS ("需要你的授权", "任务已完成"). Dropping those words on the floor would make
 * this path indistinguishable from the screen-scraping guesses it exists to outrank, so this module
 * keeps them and hands them to the surfaces that can show them.
 *
 * Wire shape (server: session_signal.go / AgentSignalPayload):
 *   { type: "agent_signal", payload: { signals: [ { sessionId, kind, title?, body?, at, seq } ] } }
 *
 * Two properties of that frame define everything below:
 *   • It is the COMPLETE current set, never a delta — `{"signals":[]}` is the explicit "nothing is
 *     pending anymore". So each frame REPLACES the state; there is no client-side "read"/"dismissed"
 *     ledger here. Disappearance is the server's call, and a second opinion on the client would be a
 *     way for the two to disagree.
 *   • It is pushed on whichever session's WebSocket is open (a bell can ring in a background session
 *     that has none of its own), which is why this is a module-level singleton, exactly like
 *     useSessionsOverview: the frame arrives inside the ACTIVE terminal surface while its consumers
 *     (overview cards, tab strip) live in the portal, which is not an ancestor of that surface.
 *
 * Deliberately NOT here: any escalation of status. An explicit signal is amber "needs you" and stops
 * there — see the severity note on `AgentSignal.kind`.
 */
import { ref } from 'vue'

/** One session's currently-unanswered explicit signal (wire type: terminal.AgentSignalEntry). */
export interface AgentSignal {
  sessionId: string
  /**
   * "bell" = a raw BEL, "notify" = an OSC desktop notification.
   *
   * NEITHER may raise a session to the red, blocked `waiting` status: a bell means "come back" and
   * cannot distinguish "approve this" from "I'm done", and guessing wrong in the alarming direction
   * is worse than not guessing. The backend already lands both on AwaitingUser (amber, dismissable)
   * for this reason; nothing in the client may re-derive them into something louder.
   */
  kind: 'bell' | 'notify'
  /** OSC notifications only ('' for a bell) — usually the emitting app ("Claude Code", "Codex"). */
  title: string
  /** OSC notifications only ('' for a bell) — the message itself, the part worth quoting. */
  body: string
  /** RFC3339 (ms) arrival time. */
  at: string
  /**
   * Per-session monotonic counter. Two identical bells differ ONLY in this, so it is what a
   * consumer that re-prompts (a toast, a sound) must compare — the words alone would look unchanged.
   */
  seq: number
}

/**
 * Cap on the text we keep from a signal. An OSC body is attacker-adjacent free text (any program in
 * the PTY can emit one) and lands in tooltips and single-line card rows; a runaway body would blow
 * out the layout it lands in. 160 chars is far past any real "your turn" message.
 */
const SIGNAL_TEXT_MAX = 160

/** The complete current set, keyed by session id. Replaced wholesale by every frame. */
const signals = ref<Record<string, AgentSignal>>({})

/** Collapse newlines/runs of whitespace and cap — a card row and a tooltip are both single-line. */
function normalizeText(v: unknown): string {
  if (typeof v !== 'string') return ''
  const flat = v.replace(/\s+/g, ' ').trim()
  return flat.length > SIGNAL_TEXT_MAX ? flat.slice(0, SIGNAL_TEXT_MAX - 1) + '…' : flat
}

/**
 * Apply a pushed `{ type: "agent_signal" }` payload.
 *
 * A malformed payload is IGNORED rather than treated as an empty set: "we couldn't read this frame"
 * is not the same statement as "the server says nothing is pending", and silently clearing a live
 * needs-you on a parse hiccup is the more expensive mistake.
 */
export function applyAgentSignalFrame(payload: unknown): void {
  const list = (payload as { signals?: unknown } | null)?.signals
  if (!Array.isArray(list)) return
  const next: Record<string, AgentSignal> = {}
  for (const raw of list) {
    const e = raw as Partial<AgentSignal> | null
    const id = typeof e?.sessionId === 'string' ? e.sessionId : ''
    if (!id) continue
    next[id] = {
      sessionId: id,
      kind: e?.kind === 'notify' ? 'notify' : 'bell',
      title: normalizeText(e?.title),
      body: normalizeText(e?.body),
      at: typeof e?.at === 'string' ? e.at : '',
      seq: typeof e?.seq === 'number' ? e.seq : 0,
    }
  }
  signals.value = next
}

export function useAgentSignals() {
  /** This session's pending signal, or undefined. Reads the ref, so it tracks in computeds. */
  function signalFor(sessionId: string | undefined): AgentSignal | undefined {
    return sessionId ? signals.value[sessionId] : undefined
  }
  return { signals, signalFor }
}
