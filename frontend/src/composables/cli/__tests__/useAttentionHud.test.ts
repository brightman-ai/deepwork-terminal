import { describe, it, expect, beforeEach } from 'bun:test'
import { ref, nextTick } from 'vue'
import {
  useAttentionHud,
  createAttentionGate,
  mergeCard,
  mergedHeadline,
  refreshCard,
  sortByUrgency,
  urgencyRank,
  isAttentionStatus,
  ATTENTION_DEFAULTS,
  URGENCY_ORDER,
  type AttentionCandidate,
  type AttentionHud,
} from '@terminal/composables/cli/useAttentionHud'
import { createStatusEdgeDetector } from '@terminal/composables/cli/statusEdgeDetector'
import {
  useAgentOverview,
  windowKey,
  type EffectiveStatus,
} from '@terminal/composables/cli/useAgentOverview'
import type { TmuxWindowState } from '@terminal/types/terminal'

// useAgentOverview's seen layer touches localStorage at composable-construction time; the HUD
// never does, but the import graph is shared, so keep bun's env honest with a tiny stub.
const _store: Record<string, string> = {}
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  value: {
    getItem: (k: string) => (k in _store ? _store[k] : null),
    setItem: (k: string, v: string) => {
      _store[k] = v
    },
    removeItem: (k: string) => {
      delete _store[k]
    },
    clear: () => {
      for (const k of Object.keys(_store)) delete _store[k]
    },
    key: () => null,
    length: 0,
  } as Storage,
})

const T1 = '2026-07-19T10:00:00.000Z'
const T2 = '2026-07-19T10:05:00.000Z' // a LATER completion — a new turn

// ─── fixtures ────────────────────────────────────────────────────────────────────────

type WinOpts = {
  status?: 'waiting' | 'running' | 'idle'
  active?: boolean
  awaiting?: boolean
  since?: string
  tool?: string
  /** Extra panes, to prove a multi-pane window still contributes ONE card item. */
  panes?: number
}
function win(index: number, opts: WinOpts = {}): TmuxWindowState {
  const { status = 'idle', active = false, awaiting = false, tool = 'claude', panes = 1 } = opts
  // Mirror the backend exactly (tmux_state.go:450-456): an explicit block ALWAYS counts as
  // needs-you and carries the turn's transcript timestamp, so one turn keeps one `awaitingSince`
  // across waiting → done-unseen. Getting this wrong here would fake-pass gate 1.
  const needsYou = awaiting || status === 'waiting'
  const since = opts.since !== undefined ? opts.since : needsYou ? T1 : undefined
  return {
    index,
    name: `w${index}`,
    windowId: `@${index}`,
    active,
    panes: Array.from({ length: panes }, (_, i) => ({
      index: i,
      active: i === 0,
      agentTool: tool as TmuxWindowState['panes'][number]['agentTool'],
      agentStatus: status as TmuxWindowState['panes'][number]['agentStatus'],
      awaitingUser: needsYou,
      awaitingSince: since,
    })),
  }
}

function cand(index: number, status: 'waiting' | 'done-unseen', ackKey = T1): AttentionCandidate {
  return { key: `@${index}`, index, name: `w${index}`, tool: 'claude', status, ackKey }
}

/** A HUD wired to an explicit clock and an explicit status map — no timers, no DOM. */
function harness(opts: { autoDismissMs?: number; cooldownMs?: number; hidden?: () => boolean } = {}) {
  const windows = ref<TmuxWindowState[]>([])
  const overviewOpen = ref(false)
  const statusOverride = new Map<string, EffectiveStatus>()
  const seenWrites: string[] = []
  let clock = 1_000_000

  const hud: AttentionHud = useAttentionHud({
    windows,
    overviewOpen,
    effectiveStatus: (w) => statusOverride.get(windowKey(w)) ?? 'idle',
    markSeen: (w) => {
      seenWrites.push(windowKey(w))
    },
    now: () => clock,
    hidden: opts.hidden ?? (() => false),
    cooldownMs: opts.cooldownMs ?? ATTENTION_DEFAULTS.cooldownMs,
    autoDismissMs: opts.autoDismissMs ?? 0, // 0 = no self-collapse timer unless a test wants one
  })

  /** Push one tmux frame: windows + their effective statuses, then let the watcher run. */
  async function frame(wins: TmuxWindowState[], statuses: Record<string, EffectiveStatus>): Promise<void> {
    statusOverride.clear()
    for (const [k, v] of Object.entries(statuses)) statusOverride.set(k, v)
    windows.value = wins
    await nextTick()
  }

  return {
    hud,
    frame,
    seenWrites,
    overviewOpen,
    advance: (ms: number) => {
      clock += ms
    },
  }
}

beforeEach(() => localStorage.clear())

// ─── shared edge detector (the "no third copy" invariant) ────────────────────────────

describe('statusEdgeDetector — the one edge definition', () => {
  it('primes on the first frame so already-firing keys are not announced', () => {
    const d = createStatusEdgeDetector<string>()
    expect(d.primed).toBe(false)
    expect(d.diff(new Map([['a', 'waiting']]))).toEqual([])
    expect(d.primed).toBe(true)
    expect(d.diff(new Map([['a', 'waiting']]))).toEqual([])
  })

  it('emits on change, treats a post-priming key as an edge, and prunes vanished keys', () => {
    const d = createStatusEdgeDetector<string>()
    d.diff(new Map([['a', 'running']]))
    expect(d.diff(new Map([['a', 'waiting']]))).toEqual([{ key: 'a', from: 'running', to: 'waiting' }])
    // 'b' appears after priming, already waiting → a genuine edge with no predecessor.
    expect(d.diff(new Map([['a', 'waiting'], ['b', 'waiting']]))).toEqual([
      { key: 'b', from: undefined, to: 'waiting' },
    ])
    // 'b' disappears then returns waiting → re-arms rather than inheriting its own corpse.
    d.diff(new Map([['a', 'waiting']]))
    expect(d.peek('b')).toBeUndefined()
    expect(d.diff(new Map([['a', 'waiting'], ['b', 'waiting']]))).toEqual([
      { key: 'b', from: undefined, to: 'waiting' },
    ])
  })
})

// ─── ordering / merge primitives (ATT-5) ─────────────────────────────────────────────

describe('urgency order', () => {
  it('ranks waiting → running → done-unseen → idle (same order as the overview groups)', () => {
    expect(urgencyRank('waiting')).toBeLessThan(urgencyRank('running'))
    expect(urgencyRank('running')).toBeLessThan(urgencyRank('done-unseen'))
    expect(urgencyRank('done-unseen')).toBeLessThan(urgencyRank('idle'))
  })

  // The anti-drift claim in URGENCY_ORDER's doc comment, made checkable. The three inequalities
  // above only pin the HUD's own ranking against itself — they pass just as happily if the overview
  // grid orders its groups some completely different way, which is exactly the drift the comment
  // says cannot happen. This test runs the REAL `groups` computed and asserts its output order IS
  // the constant, so reintroducing a second local `order` array in useAgentOverview (the state this
  // fix removed) fails here the moment the two disagree.
  it('URGENCY_ORDER drives the overview groups — one order, not two copies', () => {
    const windows = ref<TmuxWindowState[]>([
      win(4), // idle
      win(3, { awaiting: true }), // done-unseen (dated, unseen)
      win(2, { status: 'running' }),
      win(1, { status: 'waiting' }),
    ])
    const ov = useAgentOverview(windows, ref(false))

    // Every status is represented, so no group can be missing by accident.
    expect(ov.groups.value.map((g) => g.status)).toEqual([...URGENCY_ORDER])
    // …and the grid's top-to-bottom order agrees with the rank the card jumps by, term for term.
    const ranks = ov.groups.value.map((g) => urgencyRank(g.status))
    expect(ranks).toEqual([...ranks].sort((a, b) => a - b))
    expect(new Set(ranks).size).toBe(ranks.length) // strictly increasing, no ties
  })

  it('only waiting and done-unseen may interrupt', () => {
    expect(isAttentionStatus('waiting')).toBe(true)
    expect(isAttentionStatus('done-unseen')).toBe(true)
    expect(isAttentionStatus('running')).toBe(false)
    expect(isAttentionStatus('idle')).toBe(false)
  })

  it('sorts most urgent first, then by window number', () => {
    const sorted = sortByUrgency([cand(7, 'done-unseen'), cand(3, 'waiting'), cand(1, 'done-unseen')])
    expect(sorted.map((c) => [c.index, c.status])).toEqual([
      [3, 'waiting'],
      [1, 'done-unseen'],
      [7, 'done-unseen'],
    ])
  })

  it('merges into one card whose primary is the most urgent window', () => {
    const first = mergeCard(null, [cand(7, 'done-unseen')], 100, 8_000)!
    const merged = mergeCard(first, [cand(3, 'waiting')], 200, 8_000)!
    expect(merged.items).toHaveLength(1 + 1)
    expect(merged.primary.index).toBe(3)
    expect(merged.expiresAt).toBe(200 + 8_000) // a merged-in item is not starved by a stale expiry
  })

  it('refreshCard drops windows that stopped needing you and nulls an emptied card', () => {
    const card = mergeCard(null, [cand(3, 'waiting'), cand(7, 'done-unseen')], 0, 8_000)!
    const stillOne = refreshCard(card, new Map([['@7', cand(7, 'done-unseen')]]))!
    expect(stillOne.items.map((c) => c.index)).toEqual([7])
    expect(refreshCard(card, new Map())).toBeNull()
  })
})

// A merged card is the only surface that sees the whole waiting/done-unseen mix, and it used to
// flatten it to a single "N 个窗口等你". A Witness hit the consequence head-on: card said 4, the
// overview roll-up said ◉3, the tmux sheet said 3 WAITING — "I never worked out whether it was 3
// or 4 that wanted me". These tests pin the fix as a property (every number has an owner on
// another surface), not just as three literal strings.
describe('mergedHeadline — a merged card must not lie about its mix', () => {
  it('all-waiting reads as the plain wait count', () => {
    expect(mergedHeadline([cand(3, 'waiting'), cand(5, 'waiting')])).toBe('2 个窗口等你')
  })

  // The case the old code got flatly wrong: it announced "等你" for windows that were done.
  it('all-done reads as finished, never as "waiting for you"', () => {
    const headline = mergedHeadline([cand(3, 'done-unseen'), cand(5, 'done-unseen')])
    expect(headline).toBe('2 个窗口跑完了')
    expect(headline).not.toContain('等你')
  })

  it('mixed reports both counts and never the flat total', () => {
    const items = [cand(3, 'waiting'), cand(5, 'waiting'), cand(7, 'waiting'), cand(4, 'done-unseen')]
    expect(mergedHeadline(items)).toBe('3 个等你，1 个跑完了')
    // The "4" with no owner — the exact number that confused the Witness — must not appear.
    expect(mergedHeadline(items)).not.toContain('4 个')
  })

  // The point of the whole fix: whatever the mix, the counts on the card are the counts the
  // overview roll-up (◉N / ✓N) and the tmux sheet show. Assert the invariant, not one example.
  it('its counts always reconcile with the per-status roll-up counts', () => {
    for (const items of [
      [cand(1, 'waiting')],
      [cand(1, 'waiting'), cand(2, 'done-unseen')],
      [cand(1, 'done-unseen'), cand(2, 'done-unseen'), cand(3, 'waiting')],
      [cand(1, 'done-unseen'), cand(2, 'done-unseen')],
    ]) {
      const waiting = items.filter((i) => i.status === 'waiting').length
      const done = items.length - waiting
      const headline = mergedHeadline(items)
      const numbers = (headline.match(/\d+/g) ?? []).map(Number)
      expect(numbers).toEqual([waiting, done].filter((n) => n > 0))
    }
  })
})

// ─── the four gates, at the gate level (ATT-6) ───────────────────────────────────────

describe('createAttentionGate — the four gates', () => {
  const edgesInto = (key: string, to: EffectiveStatus) => [{ key, from: 'running' as EffectiveStatus, to }]

  it('gate 1: the same awaitingSince raises at most one card', () => {
    const gate = createAttentionGate()
    const candidates = new Map([['@3', cand(3, 'waiting', T1)]])
    const input = { edges: edgesInto('@3', 'waiting' as EffectiveStatus), candidates, visible: new Set<string>(), now: 0, deliver: true }
    expect(gate.admit(input)).toHaveLength(1)
    // Same turn transitioning waiting → done-unseen carries the SAME awaitingSince → silent.
    expect(
      gate.admit({
        ...input,
        edges: edgesInto('@3', 'done-unseen'),
        candidates: new Map([['@3', cand(3, 'done-unseen', T1)]]),
        now: 1,
      }),
    ).toHaveLength(0)
  })

  it('gate 2: a window is silent for the cooldown, and replying (→running) retires it', () => {
    const gate = createAttentionGate({ cooldownMs: 60_000 })
    const raise = (now: number, ack: string) =>
      gate.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting', ack)]]),
        visible: new Set<string>(),
        now,
        deliver: true,
      })
    expect(raise(0, T1)).toHaveLength(1)
    expect(raise(59_999, T2)).toHaveLength(0) // new turn, but still inside the cooldown
    expect(raise(60_000, T2)).toHaveLength(1) // cooldown elapsed

    // Backend parity: you replied → running → the next completion is not swallowed.
    const gate2 = createAttentionGate({ cooldownMs: 60_000 })
    const raise2 = (now: number, ack: string) =>
      gate2.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting', ack)]]),
        visible: new Set<string>(),
        now,
        deliver: true,
      })
    expect(raise2(0, T1)).toHaveLength(1)
    gate2.admit({
      edges: [{ key: '@3', from: 'waiting', to: 'running' }],
      candidates: new Map(),
      visible: new Set<string>(),
      now: 1_000,
      deliver: true,
    })
    expect(raise2(2_000, T2)).toHaveLength(1) // well inside 60s, but the cooldown was retired
  })

  it('gate 3: a window you can already see never raises a card', () => {
    const gate = createAttentionGate()
    expect(
      gate.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting')]]),
        visible: new Set(['@3']),
        now: 0,
        deliver: true,
      }),
    ).toHaveLength(0)
  })

  it('gate 4: a dismissed id stays dismissed forever, while a NEW turn still raises', () => {
    const gate = createAttentionGate({ cooldownMs: 0 })
    gate.mute('@3', T1)
    const raise = (ack: string, now: number) =>
      gate.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting', ack)]]),
        visible: new Set<string>(),
        now,
        deliver: true,
      })
    expect(raise(T1, 0)).toHaveLength(0)
    expect(raise(T1, 10_000_000)).toHaveLength(0) // no amount of time revives a dismissed id
    expect(raise(T2, 10_000_001)).toHaveLength(1) // but the next turn is a different alert
  })

  // ── undated waits: the identity gates stand down ────────────────────────────────────
  // Real backend behavior, not a hypothetical: a PTY-derived wait (permission prompt on screen
  // while the driver still reads `running`) carries the zero time, and CodexDriver never emits a
  // driver-side waiting at all, so EVERY Codex wait is undated. Fixture proof: w6 `perm` →
  // {"agentStatus":"waiting","awaitingUser":true,"awaitingSince":"0001-01-01T00:00:00Z"}.
  // `windowAwaitingSince` maps that to '' — and '' === '' would make gates 1/4 read every future
  // undated wait as "already handled", i.e. ONE card per Codex window per session.
  const UNDATED = ''

  it('gate 1 stands down for an undated wait — a second prompt still gets a card', () => {
    const gate = createAttentionGate({ cooldownMs: 0 })
    const raise = (now: number) =>
      gate.admit({
        edges: edgesInto('@6', 'waiting'),
        candidates: new Map([['@6', cand(6, 'waiting', UNDATED)]]),
        visible: new Set<string>(),
        now,
        deliver: true,
      })
    expect(raise(0)).toHaveLength(1)
    // Second undated prompt on the same window. It is genuinely a different alert; we simply can't
    // prove it by id — so the HUD speaks rather than swallowing an agent blocked on a prompt.
    expect(raise(1)).toHaveLength(1)
  })

  it('an undated wait is rate-limited by the cooldown instead (gate 2 carries the load)', () => {
    const gate = createAttentionGate({ cooldownMs: 60_000 })
    const raise = (now: number) =>
      gate.admit({
        edges: edgesInto('@6', 'waiting'),
        candidates: new Map([['@6', cand(6, 'waiting', UNDATED)]]),
        visible: new Set<string>(),
        now,
        deliver: true,
      })
    expect(raise(0)).toHaveLength(1)
    expect(raise(59_999)).toHaveLength(0) // no identity dedup, but time-based dedup still applies
    expect(raise(60_000)).toHaveLength(1)
  })

  it('gate 4 stands down for an undated wait — ✕ mutes for a cooldown, not forever', () => {
    const gate = createAttentionGate({ cooldownMs: 0 })
    gate.mute('@6', UNDATED)
    expect(
      gate.admit({
        edges: edgesInto('@6', 'waiting'),
        candidates: new Map([['@6', cand(6, 'waiting', UNDATED)]]),
        visible: new Set<string>(),
        now: 0,
        deliver: true,
      }),
    ).toHaveLength(1)
  })

  it('an undated mute on one window never leaks onto another window', () => {
    // The ledgers are keyed BY WINDOW with the ack id as the value, so the empty id cannot collide
    // across windows. Pinned here because "all undated items share one key" is the intuitive-but-
    // wrong reading of this code, and a refactor to a single `Set<ackKey>` would introduce exactly
    // that bug — silencing every Codex window because you dismissed one.
    const gate = createAttentionGate({ cooldownMs: 0 })
    gate.mute('@6', UNDATED)
    expect(
      gate.admit({
        edges: [{ key: '@7', from: 'running' as EffectiveStatus, to: 'waiting' as EffectiveStatus }],
        candidates: new Map([['@7', cand(7, 'waiting', UNDATED)]]),
        visible: new Set<string>(),
        now: 0,
        deliver: true,
      }),
    ).toHaveLength(1)
  })

  it('dated waits keep full identity dedup — standing down is scoped to the undated case', () => {
    const gate = createAttentionGate({ cooldownMs: 0 })
    const input = {
      edges: edgesInto('@3', 'waiting' as EffectiveStatus),
      candidates: new Map([['@3', cand(3, 'waiting', T1)]]),
      visible: new Set<string>(),
      now: 0,
      deliver: true,
    }
    expect(gate.admit(input)).toHaveLength(1)
    expect(gate.admit({ ...input, now: 1 })).toHaveLength(0) // gate 1 still bites when dated
  })

  it('deliver:false stays silent yet still keeps the bookkeeping honest (ATT-8)', () => {
    const gate = createAttentionGate({ cooldownMs: 60_000 })
    // Page hidden: the Web Notification path owns this edge, the HUD says nothing…
    expect(
      gate.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting', T1)]]),
        visible: new Set<string>(),
        now: 0,
        deliver: false,
      }),
    ).toHaveLength(0)
    // …and it did not burn the cooldown either, so a LATER real edge is not swallowed.
    expect(
      gate.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting', T2)]]),
        visible: new Set<string>(),
        now: 1_000,
        deliver: true,
      }),
    ).toHaveLength(1)
  })

  it('prune forgets vanished windows so a re-created one starts clean', () => {
    const gate = createAttentionGate({ cooldownMs: 60_000 })
    const raise = (now: number) =>
      gate.admit({
        edges: edgesInto('@3', 'waiting'),
        candidates: new Map([['@3', cand(3, 'waiting', T1)]]),
        visible: new Set<string>(),
        now,
        deliver: true,
      })
    expect(raise(0)).toHaveLength(1)
    expect(raise(1_000)).toHaveLength(0) // gate 1 + gate 2
    gate.prune(new Set()) // window closed
    expect(raise(2_000)).toHaveLength(1) // a brand-new window with a recycled id is not muted
  })
})

// ─── the composable, end to end (ATT-1 / ATT-5 / ATT-6) ──────────────────────────────

describe('useAttentionHud', () => {
  it('ATT-1: raises a card when a non-visible window transitions into waiting', async () => {
    const h = harness()
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    expect(h.hud.card.value).toBeNull() // priming frame announces nothing

    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value?.items.map((c) => c.index)).toEqual([3])
    expect(h.hud.card.value?.primary.status).toBe('waiting')
    expect(h.hud.card.value?.primary.tool).toBe('claude')
  })

  it('ATT-9: no windows / no attention status / a window that vanishes → no card, no throw', async () => {
    const h = harness()
    await h.frame([], {})
    await h.frame([win(1, { active: true, status: 'running' })], { '@1': 'running' })
    expect(h.hud.card.value).toBeNull()
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value).not.toBeNull()
    // Window 3 closes while its card is up → the card cannot keep claiming it needs you.
    await h.frame([win(1, { active: true })], { '@1': 'idle' })
    expect(h.hud.card.value).toBeNull()
  })

  it('gate 3 (composable): the visible window is silent, and an open overview silences everything', async () => {
    const h = harness()
    await h.frame([win(1, { active: true, status: 'running' }), win(3, { status: 'running' })], { '@1': 'running', '@3': 'running' })
    // The window you are LOOKING at goes waiting → no interruption.
    await h.frame([win(1, { active: true, status: 'waiting' }), win(3, { status: 'running' })], { '@1': 'waiting', '@3': 'running' })
    expect(h.hud.card.value).toBeNull()

    // Overview open: every window is on screen, so nothing is "elsewhere".
    h.overviewOpen.value = true
    await h.frame([win(1, { active: true, status: 'waiting' }), win(3, { status: 'waiting' })], { '@1': 'waiting', '@3': 'waiting' })
    expect(h.hud.card.value).toBeNull()
  })

  it('ATT-5: two windows flipping in the SAME frame merge into one card, most urgent first', async () => {
    const h = harness()
    await h.frame(
      [win(1, { active: true }), win(3, { status: 'running' }), win(7, { status: 'running' })],
      { '@1': 'idle', '@3': 'running', '@7': 'running' },
    )
    // Same second, same push frame: 7 finished (unread) and 3 blocked on you.
    await h.frame(
      [win(1, { active: true }), win(3, { status: 'waiting' }), win(7, { awaiting: true })],
      { '@1': 'idle', '@3': 'waiting', '@7': 'done-unseen' },
    )
    const card = h.hud.card.value!
    expect(card.items.map((c) => c.index)).toEqual([3, 7])
    expect(card.primary.index).toBe(3) // waiting outranks done-unseen
    expect(card.primary.status).toBe('waiting')
  })

  it('ATT-5: a multi-pane window contributes exactly one item', async () => {
    const h = harness()
    await h.frame([win(1, { active: true }), win(3, { status: 'running', panes: 3 })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting', panes: 3 })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value?.items).toHaveLength(1)
  })

  it('ATT-5: a later edge merges into the live card instead of stacking a second one', async () => {
    const h = harness()
    await h.frame([win(1, { active: true }), win(3, { status: 'running' }), win(7, { status: 'running' })], { '@1': 'idle', '@3': 'running', '@7': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' }), win(7, { status: 'running' })], { '@1': 'idle', '@3': 'waiting', '@7': 'running' })
    h.advance(2_000)
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' }), win(7, { awaiting: true })], { '@1': 'idle', '@3': 'waiting', '@7': 'done-unseen' })
    expect(h.hud.card.value?.items.map((c) => c.index)).toEqual([3, 7])
  })

  it('ATT-2: tapping the card marks the target seen, silences it and closes', async () => {
    const h = harness({ cooldownMs: 0 })
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })

    const target = h.hud.activate()
    expect(target?.index).toBe(3) // the caller does selectWindow(3)
    expect(h.seenWrites).toEqual(['@3'])
    expect(h.hud.card.value).toBeNull()

    // Same alert id re-entering waiting must stay silent (gate 4).
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value).toBeNull()
  })

  it('ATT-3: ✕ writes seen for the PRIMARY only, and silences every id on the card', async () => {
    const h = harness({ cooldownMs: 0 })
    await h.frame([win(1, { active: true }), win(3, { status: 'running' }), win(7, { status: 'running' })], { '@1': 'idle', '@3': 'running', '@7': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' }), win(7, { awaiting: true })], { '@1': 'idle', '@3': 'waiting', '@7': 'done-unseen' })

    h.hud.dismiss()
    // @3 is the primary (a tap would have gone there) → seen. @7 is a merged-in window whose
    // output the user never looked at → muted, NOT seen, so its amber dot stays lit. "I don't want
    // to be interrupted" is not "I have read this"; paying all N windows' read state for one ✕ is
    // the same mistake ATT-4 exists to prevent, just settled in a lump sum.
    expect(h.seenWrites).toEqual(['@3'])
    expect(h.hud.card.value).toBeNull()

    await h.frame([win(1, { active: true }), win(3, { status: 'running' }), win(7, { status: 'running' })], { '@1': 'idle', '@3': 'running', '@7': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' }), win(7, { awaiting: true })], { '@1': 'idle', '@3': 'waiting', '@7': 'done-unseen' })
    expect(h.hud.card.value).toBeNull()

    // …but a NEW turn (newer awaitingSince) is a different alert and speaks up again.
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting', since: T2 })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value?.items.map((c) => c.index)).toEqual([3])
  })

  it('ATT-4: auto-collapse closes the card WITHOUT writing seen', async () => {
    const h = harness({ autoDismissMs: 15, cooldownMs: 0 })
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value).not.toBeNull()

    await new Promise((r) => setTimeout(r, 60))
    expect(h.hud.card.value).toBeNull()
    expect(h.seenWrites).toEqual([]) // collapsing is not reading — the pane-bar dot stays lit
  })

  it('ATT-6 gate 1 (composable): a sustained wait raises exactly one card', async () => {
    const h = harness({ cooldownMs: 0 })
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    h.hud.collapse()
    // The same turn now shows as done-unseen — same awaitingSince, so it is the same alert.
    await h.frame([win(1, { active: true }), win(3, { awaiting: true })], { '@1': 'idle', '@3': 'done-unseen' })
    expect(h.hud.card.value).toBeNull()
  })

  it('ATT-8: nothing is raised while the page is hidden, and nothing is replayed on return', async () => {
    let hidden = true
    const h = harness({ hidden: () => hidden })
    await h.frame([win(1, { active: true }), win(3, { status: 'running' })], { '@1': 'idle', '@3': 'running' })
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value).toBeNull() // the Web Notification path owned this one

    // Tab comes back. The window is STILL waiting, but that edge already happened and expired.
    hidden = false
    await h.frame([win(1, { active: true }), win(3, { status: 'waiting' })], { '@1': 'idle', '@3': 'waiting' })
    expect(h.hud.card.value).toBeNull()
  })

  // ── undated waits, end to end (Codex / PTY-derived permission prompts) ──────────────
  const ZERO_TIME = '0001-01-01T00:00:00Z' // exactly what /api/tmux/state returns for these

  it('an undated wait (Codex / PTY prompt) raises a card at all', async () => {
    const h = harness({ cooldownMs: 0 })
    await h.frame([win(1, { active: true }), win(6, { status: 'running' })], { '@1': 'idle', '@6': 'running' })
    await h.frame([win(1, { active: true }), win(6, { status: 'waiting', since: ZERO_TIME })], { '@1': 'idle', '@6': 'waiting' })
    expect(h.hud.card.value?.items.map((c) => c.index)).toEqual([6])
    expect(h.hud.card.value?.primary.ackKey).toBe('') // no identity — windowAwaitingSince rejects the zero time
  })

  it('a SECOND undated prompt on the same window still raises, after the user answered the first', async () => {
    const h = harness({ cooldownMs: 0 })
    const running = () => [win(1, { active: true }), win(6, { status: 'running' })]
    const prompt = () => [win(1, { active: true }), win(6, { status: 'waiting', since: ZERO_TIME })]

    await h.frame(running(), { '@1': 'idle', '@6': 'running' })
    await h.frame(prompt(), { '@1': 'idle', '@6': 'waiting' })
    expect(h.hud.card.value).not.toBeNull()

    // The user answers it: back to running (this also retires the cooldown, backend parity rule).
    h.hud.activate()
    await h.frame(running(), { '@1': 'idle', '@6': 'running' })

    // Codex hits a NEW permission prompt. Same zero time — indistinguishable by id from the last
    // one. Before this fix, gates 1 and 4 both matched on '' and the card never came back for the
    // rest of the session: a Codex window got exactly one alert, ever. Silence here would lose the
    // single highest-signal event this whole feature exists to surface.
    await h.frame(prompt(), { '@1': 'idle', '@6': 'waiting' })
    expect(h.hud.card.value?.items.map((c) => c.index)).toEqual([6])
  })

  it('the seen layer the HUD delegates to REFUSES an undated dismiss (one policy, not two)', () => {
    // The HUD's `markSeen` IS `useAgentOverview.dismiss`, so "undated ⇒ never persist-dismissable"
    // is not something the HUD re-decides — it inherits it. Asserted against the real composable
    // (the harness's markSeen is a spy and could not show this): tapping the card writes through,
    // and the seen layer declines the write, so an undated prompt keeps its dot instead of being
    // wrongly muted. The HUD's own gates use the SAME `isDatedSince` predicate for the same reason.
    const w = win(6, { awaiting: true, since: ZERO_TIME })
    const windows = ref<TmuxWindowState[]>([w])
    const ov = useAgentOverview(windows, ref(false))

    expect(ov.effectiveStatus(w)).toBe('done-unseen')
    ov.dismiss(w)
    expect(ov.effectiveStatus(w)).toBe('done-unseen') // still lit — a dated one would go 'idle' here

    // Control: the very same call on a DATED completion does mute it, proving the refusal above is
    // the undated branch and not a broken dismiss.
    const dated = win(7, { awaiting: true, since: T1 })
    windows.value = [w, dated]
    expect(ov.effectiveStatus(dated)).toBe('done-unseen')
    ov.dismiss(dated)
    expect(ov.effectiveStatus(dated)).toBe('idle')
  })
})
