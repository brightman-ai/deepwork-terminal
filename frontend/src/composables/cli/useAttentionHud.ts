/**
 * useAttentionHud — the attention HUD's LOGIC layer (trigger / merge / the four anti-spam gates).
 *
 * The pane bar's status dot is a PASSIVE signal: it only works once you are already looking at the
 * pane bar. This composable supplies the ACTIVE half — when a window transitions into a state that
 * needs you while your eyes are on some other window, it raises exactly one card, once, and then
 * shuts up. Rendering is entirely the caller's business (see AttentionCard); nothing here touches
 * the DOM, so every rule below is a unit test rather than a screenshot.
 *
 * What this file deliberately does NOT own:
 *  - EDGE DETECTION. Shared with `useForegroundAgentNotify` via `statusEdgeDetector` — one
 *    definition of priming/edge/pruning, two granularities. No third copy.
 *  - THE READ MODEL. "Seen" is `useAgentOverview`'s localStorage layer keyed on the backend's
 *    reload-proof `awaitingSince`; this file writes to it through the injected `markSeen` and
 *    reuses the very same `awaitingSince` as its alert identity. It invents no second notion of
 *    "already handled".
 *  - STATUS DERIVATION. `effectiveStatus` is injected from the ONE `useAgentOverview` instance the
 *    pane bar and overview grid already share, so the HUD can never disagree with the dots.
 *
 * The mute ledger below is the one piece of state that is genuinely the HUD's own, and it is not a
 * competing read model: `markSeen` answers "has the user seen this completion?" (durable, drives
 * the dots), the mute ledger answers "has the user waved this ALERT away?" (session-local, drives
 * only whether a card may re-raise). They are distinct questions and a raw `waiting` window proves
 * it — writing seen does not clear a red dot, so without the ledger a dismissed waiting card would
 * pop straight back. Both are keyed on the same `awaitingSince`, so they cannot drift apart.
 */
import { computed, getCurrentInstance, onUnmounted, ref, watch, type Ref } from 'vue'
import type { TmuxWindowState } from '@terminal/types/terminal'
import {
  URGENCY_ORDER,
  isDatedSince,
  windowAwaitingSince,
  windowKey,
  windowTool,
  type EffectiveStatus,
} from '@terminal/composables/cli/useAgentOverview'
import {
  createStatusEdgeDetector,
  type StatusEdge,
} from '@terminal/composables/cli/statusEdgeDetector'

// ─── vocabulary ──────────────────────────────────────────────────────────────────────

/** The only two states worth interrupting for: "it needs your input" and "it finished, unread".
 *  `running` never interrupts (it wants nothing from you) and neither does `idle`. */
export type AttentionStatus = Extract<EffectiveStatus, 'waiting' | 'done-unseen'>

/** Re-exported, not redefined: `URGENCY_ORDER` lives in `useAgentOverview` beside STATUS_COLOR and
 *  is the array the overview's `groups` computed literally iterates (AOV-4), so a future reorder
 *  moves the grid's top row and this card's "jump to the worst one" together — there is no second
 *  copy that could disagree. Re-exported here only so HUD callers need one import, never as a
 *  parallel definition. */
export { URGENCY_ORDER }

export function urgencyRank(status: EffectiveStatus): number {
  const i = URGENCY_ORDER.indexOf(status)
  return i < 0 ? URGENCY_ORDER.length : i
}

export function isAttentionStatus(status: EffectiveStatus): status is AttentionStatus {
  return status === 'waiting' || status === 'done-unseen'
}

/** One window's claim on your attention. `key` is the seen-layer's stable window id (`@N`) and
 *  `ackKey` is its `awaitingSince` — the two identities the rest of the app already uses. */
export interface AttentionCandidate {
  key: string
  /** Window index — the badge number, identical to `.ao-idx` / `.tpb-idx` (AOV-13). */
  index: number
  name: string
  tool: string
  status: AttentionStatus
  /** `awaitingSince`; `''` when the backend could not date the wait (see `ackKey` handling). */
  ackKey: string
}

/** One card, possibly speaking for several windows (ATT-5). */
export interface AttentionCard {
  /** Most urgent first. Never empty — an emptied card becomes `null`. */
  items: AttentionCandidate[]
  /** Where a tap goes: the most urgent window. */
  primary: AttentionCandidate
  /** Epoch ms at which the card self-collapses. Collapsing is NOT reading (ATT-4). */
  expiresAt: number
}

export const ATTENTION_DEFAULTS = {
  /** Per-window silence after a card was raised for it. Backend's dual is 120s; the foreground
   *  HUD is cheaper to ignore than an OS notification, so it may speak more often. */
  cooldownMs: 60_000,
  /** How long a card stays up before collapsing itself. */
  autoDismissMs: 8_000,
} as const

// ─── pure helpers ────────────────────────────────────────────────────────────────────

/** Most urgent first, then by window number so a merged card reads left-to-right like the bar. */
export function sortByUrgency(items: readonly AttentionCandidate[]): AttentionCandidate[] {
  return [...items].sort(
    (a, b) => urgencyRank(a.status) - urgencyRank(b.status) || a.index - b.index,
  )
}

/**
 * Headline for a merged card — the honest one.
 *
 * It used to be a flat `${n} 个窗口等你` for ANY merge, on the theory that waiting and done-unseen
 * are "both just shades of needs-you". An independent Witness proved that theory wrong in the most
 * concrete way available: with 3 waiting + 1 done-unseen on screen, the card said 4, the overview
 * roll-up said `◉3`, the tmux sheet said `3 WAITING`, and the verdict was "I never worked out
 * whether it was 3 or 4 that wanted me". A merged card is the one surface that sees the whole mix,
 * so it is the one surface that must not flatten it.
 *
 * Every number this returns is therefore a number some other surface also shows: the waiting count
 * IS the roll-up's `◉N`, the done count IS its `✓N`. There is no aggregate total anywhere anymore
 * — the "4" that had no owner is gone by construction, not by wording.
 *
 * The mixed form uses a full-width comma, not the roll-up's `·`, on purpose: the card's own second
 * line already spends `·` enumerating windows (`窗口3 · 窗口5`), and reusing the same separator one
 * line above for a different job (composition, not enumeration) is what would actually make this
 * read like a log line. A comma makes it a sentence.
 */
export function mergedHeadline(items: readonly AttentionCandidate[]): string {
  const waiting = items.filter((it) => it.status === 'waiting').length
  const done = items.length - waiting
  if (!done) return `${waiting} 个窗口等你`
  if (!waiting) return `${done} 个窗口跑完了`
  return `${waiting} 个等你，${done} 个跑完了`
}

/**
 * Fold newly admitted candidates into the live card (ATT-5). One window contributes one item no
 * matter how many of its panes moved, and a later frame's status supersedes an earlier one.
 * Merging refreshes the expiry: the gates already cap traffic at one card per window per cooldown,
 * so a merged-in item cannot be starved by an about-to-expire card.
 */
export function mergeCard(
  prev: AttentionCard | null,
  incoming: readonly AttentionCandidate[],
  now: number,
  autoDismissMs: number,
): AttentionCard | null {
  if (!incoming.length) return prev
  const byKey = new Map<string, AttentionCandidate>()
  for (const c of prev?.items ?? []) byKey.set(c.key, c)
  for (const c of incoming) byKey.set(c.key, c)
  const items = sortByUrgency([...byKey.values()])
  return { items, primary: items[0], expiresAt: now + autoDismissMs }
}

/**
 * Re-seat a live card against the current frame: drop windows that stopped needing you (the user
 * replied, or the window closed) and adopt their latest status/name. A card that empties out is
 * gone — a HUD still claiming "window 3 is waiting" after window 3 went back to running is worse
 * than no HUD at all.
 */
export function refreshCard(
  prev: AttentionCard | null,
  candidates: ReadonlyMap<string, AttentionCandidate>,
): AttentionCard | null {
  if (!prev) return null
  const items = sortByUrgency(
    prev.items.map((it) => candidates.get(it.key)).filter((c): c is AttentionCandidate => !!c),
  )
  if (!items.length) return null
  return { items, primary: items[0], expiresAt: prev.expiresAt }
}

// ─── the four gates ──────────────────────────────────────────────────────────────────

export interface AttentionAdmitInput {
  edges: readonly StatusEdge<EffectiveStatus>[]
  /** Every window currently in an attention status, keyed by window key. */
  candidates: ReadonlyMap<string, AttentionCandidate>
  /** Window keys the user can see right now — gate 3. */
  visible: ReadonlySet<string>
  now: number
  /** False = the HUD is not the right channel this instant (page hidden → the Web Notification
   *  path owns it, ATT-8). Bookkeeping that must not depend on who is watching still runs. */
  deliver: boolean
}

export interface AttentionGate {
  /** Feed one frame's edges; get back what is allowed to interrupt the user. */
  admit(input: AttentionAdmitInput): AttentionCandidate[]
  /** Gate 4 — the user waved this exact alert away; it never raises again. */
  mute(key: string, ackKey: string): void
  /** Forget vanished windows so a re-created one starts clean (no ledger leak). */
  prune(liveKeys: ReadonlySet<string>): void
}

export interface AttentionGateConfig {
  cooldownMs?: number
}

/**
 * The four anti-spam gates (ATT-6), as one ledger with an injected clock:
 *   1. one card per `awaitingSince`   — a turn interrupts you once, not once per frame
 *   2. per-window cooldown            — a chatty window cannot monopolise the HUD
 *   3. never for a window you can see  — you are already looking at it
 *   4. dismissed alerts stay dismissed — manual dismissal is permanent for that id
 *
 * Plus the backend's parity rule (`push_notifier.go:180-182`): a window going into `running` means
 * you just replied, which retires its cooldown — otherwise a fast agent's next completion lands
 * inside the cooldown and is silently swallowed.
 *
 * UNDATED WAITS (`ackKey === ''`) — the one case where gates 1 and 4 stand down.
 * The backend cannot always date a wait: a PTY-derived wait (a permission prompt on screen while
 * the driver still reads `running`) carries the zero time `0001-01-01T00:00:00Z`, and since
 * `CodexDriver` never emits a driver-side waiting at all, EVERY Codex wait is undated. This is
 * contract-locked backend behavior, not a glitch.
 *
 * Gates 1 and 4 compare alert IDENTITIES. Left naive, `'' === ''` compares true, so they would
 * read every future undated wait on a window as "the same alert I already showed" — meaning one
 * Codex window gets exactly ONE card per session, and a single ✕ silences it for good. That is
 * fail-CLOSED, and it inverts the policy `isDatedSince` sets everywhere else (undated ⇒ never
 * mutable ⇒ keeps showing). So both gates abstain when the id is empty, and the remaining gates
 * carry the load:
 *   - gate 2 (per-window cooldown) is TIME-based, needs no identity, and caps an undated window at
 *     one card per cooldown. It is precisely the instrument for "cannot tell these apart".
 *   - the EDGE requirement upstream does the rest: a still-pending prompt produces no new edge, so
 *     a re-raise can only follow a real waiting → running → waiting round trip. That round trip
 *     means you answered the last prompt and the agent hit a NEW one — which is exactly when the
 *     HUD should speak. So standing down cannot spam a dismissed-but-unchanged prompt.
 * The cost is honest and bounded: for undated waits the HUD is rate-limited rather than
 * identity-deduped, and ✕ mutes for one cooldown instead of forever. The alternative was losing
 * the highest-signal event in the app — an agent blocked on a prompt — in silence.
 */
export function createAttentionGate(config: AttentionGateConfig = {}): AttentionGate {
  const cooldownMs = config.cooldownMs ?? ATTENTION_DEFAULTS.cooldownMs
  const raisedAck = new Map<string, string>()
  const mutedAck = new Map<string, string>()
  const raisedAt = new Map<string, number>()

  return {
    mute(key: string, ackKey: string): void {
      mutedAck.set(key, ackKey)
    },
    prune(liveKeys: ReadonlySet<string>): void {
      for (const ledger of [raisedAck, mutedAck, raisedAt]) {
        for (const k of [...ledger.keys()]) if (!liveKeys.has(k)) ledger.delete(k)
      }
    },
    admit({ edges, candidates, visible, now, deliver }: AttentionAdmitInput): AttentionCandidate[] {
      const out: AttentionCandidate[] = []
      for (const edge of edges) {
        // You replied → this window's cooldown is spent. Runs regardless of `deliver`: it is a
        // fact about the session, not about who happens to be watching.
        if (edge.to === 'running') raisedAt.delete(edge.key)
        if (!deliver) continue
        const c = candidates.get(edge.key)
        if (!c) continue // edge into running/idle — nothing to say
        if (visible.has(edge.key)) continue // gate 3
        // Gates 1 and 4 are IDENTITY gates: they ask "is this the same alert as the one I already
        // showed / you already waved away?". An undated wait (`ackKey === ''`) has no identity, so
        // that question has no answer and both gates must abstain — see the block comment above.
        const dated = isDatedSince(c.ackKey)
        if (dated && mutedAck.get(edge.key) === c.ackKey) continue // gate 4
        if (dated && raisedAck.get(edge.key) === c.ackKey) continue // gate 1
        const last = raisedAt.get(edge.key)
        if (last !== undefined && now - last < cooldownMs) continue // gate 2
        raisedAck.set(edge.key, c.ackKey)
        raisedAt.set(edge.key, now)
        out.push(c)
      }
      return out
    },
  }
}

// ─── the composable ──────────────────────────────────────────────────────────────────

export interface AttentionHudDeps {
  /** The pushed tmux topology — the same ref `useAgentOverview` consumes. */
  windows: Ref<TmuxWindowState[]>
  /** From the ONE shared `useAgentOverview` instance, so the HUD and the dots agree by construction. */
  effectiveStatus: (w: TmuxWindowState) => EffectiveStatus
  /** `useAgentOverview.dismiss` — the ONE durable seen writer. Never re-implement it here. */
  markSeen: (w: TmuxWindowState) => void
  /** Overview open = every window is on screen, so nothing is "elsewhere" (gate 3). */
  overviewOpen: Ref<boolean>
  /** Test seams; both default to the real environment. */
  now?: () => number
  hidden?: () => boolean
  cooldownMs?: number
  autoDismissMs?: number
}

export interface AttentionHud {
  /** The card to render, or `null`. */
  card: Ref<AttentionCard | null>
  /** Tapping the card: marks the target seen, silences it, closes. Returns the window to jump to
   *  (the caller owns `selectWindow` — this layer stays free of tmux commands). */
  activate(): AttentionCandidate | null
  /** ✕ / swipe: marks the PRIMARY window seen and silences it; every other window on a merged card
   *  is only silenced (muted), never marked seen — waving an alert away is not reading N windows'
   *  output. Closes. */
  dismiss(): void
  /** Timeout: closes and nothing else. Collapsing is not reading (ATT-4) — the dots stay lit and
   *  the next turn's newer `awaitingSince` may raise a fresh card. */
  collapse(): void
}

export function useAttentionHud(deps: AttentionHudDeps): AttentionHud {
  const clock = deps.now ?? (() => Date.now())
  const isHidden = deps.hidden ?? (() => typeof document !== 'undefined' && document.hidden)
  const autoDismissMs = deps.autoDismissMs ?? ATTENTION_DEFAULTS.autoDismissMs
  const detector = createStatusEdgeDetector<EffectiveStatus>()
  const gate = createAttentionGate({ cooldownMs: deps.cooldownMs })
  const card = ref<AttentionCard | null>(null)
  /** Windows by key on the current frame — `activate`/`dismiss` need the window object to hand to
   *  `markSeen`, and a card can outlive nothing (it is re-seated every frame). */
  const windowByKey = new Map<string, TmuxWindowState>()

  let timer: ReturnType<typeof setTimeout> | null = null
  function disarm(): void {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }
  function arm(): void {
    disarm()
    if (autoDismissMs <= 0 || !card.value) return
    timer = setTimeout(collapse, Math.max(0, card.value.expiresAt - clock()))
  }

  /** Gate 3's input: what the user can actually see. Overview open → everything. */
  const visibleKeys = computed<Set<string>>(() => {
    const s = new Set<string>()
    const wins = deps.windows.value ?? []
    if (deps.overviewOpen.value) {
      for (const w of wins) s.add(windowKey(w))
      return s
    }
    const active = wins.find((w) => w.active)
    if (active) s.add(windowKey(active))
    return s
  })

  function candidateOf(w: TmuxWindowState, status: EffectiveStatus, key: string): AttentionCandidate | null {
    if (!isAttentionStatus(status)) return null
    return {
      key,
      index: w.index,
      name: w.name,
      tool: windowTool(w),
      status,
      // `awaitingSince` is the alert's identity — '' when the backend could not date the wait
      // (PTY-derived prompts; ALL Codex waits). An undated wait still gets an alert: the edge is
      // proof something changed. It simply has no identity to dedupe on, so the two identity gates
      // stand down for it and the cooldown takes over — see createAttentionGate's block comment.
      ackKey: windowAwaitingSince(w),
    }
  }

  const stop = watch(
    deps.windows,
    () => {
      const wins = deps.windows.value ?? []
      const statuses = new Map<string, EffectiveStatus>()
      const candidates = new Map<string, AttentionCandidate>()
      const live = new Set<string>()
      windowByKey.clear()
      for (const w of wins) {
        const key = windowKey(w)
        live.add(key)
        windowByKey.set(key, w)
        const status = deps.effectiveStatus(w)
        statuses.set(key, status)
        const c = candidateOf(w, status, key)
        if (c) candidates.set(key, c)
      }

      // Unconditional — the memory must advance even when nothing will be delivered, or returning
      // to a visible tab would replay every edge that expired while you were away (ATT-8).
      const edges = detector.diff(statuses)
      gate.prune(live)

      const now = clock()
      const admitted = gate.admit({
        edges,
        candidates,
        visible: visibleKeys.value,
        now,
        deliver: !isHidden(),
      })

      const next = mergeCard(refreshCard(card.value, candidates), admitted, now, autoDismissMs)
      const changed = next !== card.value
      card.value = next
      if (changed) {
        if (next) arm()
        else disarm()
      }
    },
    { deep: true, immediate: true },
  )

  function close(): void {
    disarm()
    card.value = null
  }

  function collapse(): void {
    close()
  }

  /** Writes the durable seen state AND silences the alert id — see the header: the two answer
   *  different questions and a raw `waiting` window needs both. Reserved for the window the user
   *  actually went to; everything else gets `gate.mute` alone (see `dismiss`). */
  function acknowledge(c: AttentionCandidate): void {
    const w = windowByKey.get(c.key)
    if (w) deps.markSeen(w)
    gate.mute(c.key, c.ackKey)
  }

  function activate(): AttentionCandidate | null {
    const target = card.value?.primary ?? null
    if (target) acknowledge(target)
    close()
    return target
  }

  /**
   * ✕ / swipe. "I don't want to be interrupted" is NOT "I have read this" — so a merged card's ✕
   * writes seen for the PRIMARY only (the one the card is actually about, and the one a tap would
   * have taken you to) and merely MUTES the rest: their alert never re-raises this session, but
   * their amber/red dot stays lit because you have not looked at their output.
   *
   * Dismissing all N would let one ✕ silently retire windows you never saw — the same "collapsing
   * is not reading" mistake ATT-4 exists to prevent, just paid all at once instead of on a timer.
   * The mute ledger is exactly the right instrument here: session-local, drives only whether a card
   * may speak again, and deliberately does not touch the durable read model.
   */
  function dismiss(): void {
    const c = card.value
    if (c) {
      acknowledge(c.primary)
      for (const it of c.items) {
        if (it.key !== c.primary.key) gate.mute(it.key, it.ackKey)
      }
    }
    close()
  }

  // Only a component owns an unmount. Called from a test/harness there is nothing to tear down
  // for, and registering anyway would just emit a spurious Vue warning.
  if (getCurrentInstance()) {
    onUnmounted(() => {
      stop()
      disarm()
    })
  }

  return { card, activate, dismiss, collapse }
}
