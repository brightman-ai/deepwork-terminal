import { describe, it, expect, beforeEach, afterAll } from 'bun:test'
import { ref, nextTick } from 'vue'
import {
  useAgentOverview,
  windowRawStatus,
  windowCwd,
  windowTool,
  windowAgentSignals,
  windowAwaitingSince,
  overviewColumns,
  STATUS_COLOR,
  STATUS_MOTION,
} from '@terminal/composables/cli/useAgentOverview'
import type { TmuxWindowState } from '@terminal/types/terminal'

// The seen-layer persists to localStorage. bun's test env doesn't reliably expose Web Storage,
// so provide a minimal in-memory stub. It survives across useAgentOverview() instances (that's
// how we simulate an F5 reload — a fresh composable re-hydrating from the same storage) and is
// cleared between tests for isolation.
const _store: Record<string, string> = {}
const storageStub = {
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
} as Storage
// Snapshot whatever this process had before we stub localStorage here, so we can put
// things back exactly as we found them once this file's tests are done. Without this,
// `Object.defineProperty` (unlike a plain assignment) defaults to `writable: false`, so
// any later test file in the same bun process that does `globalThis.localStorage = ...`
// (a plain assignment) throws "Attempted to assign to readonly property" — this file's
// stub otherwise leaks across test files.
const originalLocalStorage = (globalThis as any).localStorage

Object.defineProperty(globalThis, 'localStorage', { configurable: true, writable: true, value: storageStub })

afterAll(() => {
  // `delete` (not reassignment) is required to drop the non-writable property
  // descriptor installed above before restoring the prior value.
  delete (globalThis as any).localStorage
  if (originalLocalStorage !== undefined) (globalThis as any).localStorage = originalLocalStorage
})

beforeEach(() => localStorage.clear())

const T1 = '2026-07-09T10:00:00.000Z'
const T2 = '2026-07-09T10:05:00.000Z' // a LATER completion (a new turn)
const TZERO = '0001-01-01T00:00:00Z'  // undated: Go time.Time zero (omitempty doesn't apply)

type WinOpts = {
  status?: 'waiting' | 'running' | 'idle'
  cwd?: string
  tool?: string
  active?: boolean
  windowId?: string
  awaiting?: boolean // backend "needs-you": finished a turn, not yet responded to
  since?: string     // AwaitingSince (transcript completion time); defaults to T1 when awaiting
}
function win(index: number, opts: WinOpts = {}): TmuxWindowState {
  const { status = 'idle', cwd = '', tool = '', active = false, windowId = `@${index}`, awaiting = false } = opts
  const since = opts.since !== undefined ? opts.since : awaiting ? T1 : undefined
  return {
    index,
    name: `w${index}`,
    windowId,
    active,
    panes: [
      {
        index: 0,
        active: true,
        cwd,
        agentTool: (tool || undefined) as never,
        agentStatus: (status === 'idle' ? undefined : status) as never,
        awaitingUser: awaiting,
        awaitingSince: since,
      } as never,
    ],
  }
}

describe('windowRawStatus / cwd / tool / awaitingSince', () => {
  it('waiting > running > idle, and reads active-pane cwd/tool', () => {
    expect(windowRawStatus(win(1, { status: 'waiting' }))).toBe('waiting')
    expect(windowRawStatus(win(1, { status: 'running' }))).toBe('running')
    expect(windowRawStatus(win(1, { status: 'idle' }))).toBe('idle')
    expect(windowCwd(win(1, { cwd: '/tmp/x' }))).toBe('/tmp/x')
    expect(windowTool(win(1, { tool: 'claude' }))).toBe('claude')
  })
  it('windowAwaitingSince returns the dated completion, or "" when not awaiting / undated', () => {
    expect(windowAwaitingSince(win(1, { awaiting: true, since: T1 }))).toBe(T1)
    expect(windowAwaitingSince(win(1, { status: 'idle' }))).toBe('') // not awaiting
    expect(windowAwaitingSince(win(1, { awaiting: true, since: TZERO }))).toBe('') // undated
  })
  it('attributes a split window to its active runtime and explains every pane signal', () => {
    const w = win(5, { tool: 'claude', status: 'waiting' })
    w.panes[0].active = false
    w.panes.push({
      index: 1,
      active: true,
      agentTool: 'codex',
      agentStatus: 'running',
    } as never)
    expect(windowTool(w)).toBe('codex')
    expect(windowAgentSignals(w)).toEqual(['Claude 等待输入', 'Codex 运行中'])
  })
})

describe('needs-you state (backend awaitingUser + reload-proof seen)', () => {
  it('finished (idle + awaitingUser) → done-unseen; dismiss → idle', async () => {
    const windows = ref([win(1, { status: 'running' })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('running')

    windows.value = [win(1, { status: 'idle', awaiting: true })] // finished, not yet responded
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    ov.dismiss(windows.value[0]) // explicit "handled"
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')
  })

  it('switching to a finished window clears it via the active flag (ctrl+b N works, not just a tap)', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, active: false })])
    const ov = useAgentOverview(windows, ref(false)) // overview closed → active window = what you see
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    windows.value = [win(1, { status: 'idle', awaiting: true, active: true })] // switched to it
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')
  })

  it('while the overview is OPEN, the active window is NOT auto-seen (you are looking at the grid)', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, active: true })])
    const ov = useAgentOverview(windows, ref(true)) // overview open
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')
  })

  it('a fresh idle window (never ran, no awaitingUser) is idle, not done-unseen', async () => {
    const windows = ref([win(2, { status: 'idle', windowId: '@9' })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')
  })

  // ── the actual bug this feature fixes ───────────────────────────────────────────
  it('SEEN survives F5: dismiss, then a fresh composable re-hydrated from storage stays idle', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, since: T1 })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    ov.dismiss(windows.value[0])
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')

    // F5: brand-new composable + windows, SAME localStorage, SAME completion (T1) still pushed.
    const windows2 = ref([win(1, { status: 'idle', awaiting: true, since: T1 })])
    const ov2 = useAgentOverview(windows2, ref(true))
    await nextTick()
    expect(ov2.effectiveStatus(windows2.value[0])).toBe('idle') // ← was 'done-unseen' before the fix
  })

  it('a NEW completion re-shows the dot even after dismiss — no need to witness the running transition', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, since: T1 })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    ov.dismiss(windows.value[0])
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')

    // Pane completes ANOTHER turn → newer AwaitingSince. No running frame in between (F5-style gap).
    windows.value = [win(1, { status: 'idle', awaiting: true, since: T2 })]
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    // And it persists across F5 too: new completion re-shows on a fresh composable.
    const ov2 = useAgentOverview(ref([win(1, { status: 'idle', awaiting: true, since: T2 })]), ref(true))
    await nextTick()
    expect(ov2.effectiveStatus(win(1, { status: 'idle', awaiting: true, since: T2 }))).toBe('done-unseen')
  })

  it('an UNDATED wait (zero timestamp, e.g. PTY-only permission prompt) is never dismissable — stays shown', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, since: TZERO })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    ov.dismiss(windows.value[0]) // no dated key to remember → no-op
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    // F5 → still shown (a high-signal wait must not be silently muted).
    const ov2 = useAgentOverview(ref([win(1, { status: 'idle', awaiting: true, since: TZERO })]), ref(true))
    await nextTick()
    expect(ov2.effectiveStatus(win(1, { status: 'idle', awaiting: true, since: TZERO }))).toBe('done-unseen')
  })

  it('seen-state is pruned for vanished windows (reused id starts clean)', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, since: T1 })])
    const ov = useAgentOverview(windows, ref(false))
    await nextTick()
    windows.value = [win(1, { status: 'idle', awaiting: true, active: true, since: T1 })] // seen
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')

    windows.value = [] // window closed
    await nextTick()
    // A brand-new window reusing id @1 with the same completion time must NOT inherit "seen".
    windows.value = [win(1, { status: 'idle', awaiting: true, since: T1 })]
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')
  })
})

describe('overviewColumns (PC 活跃大卡每行 ≤3, 4→2×2 田字格)', () => {
  it('n≤3 → n 列；4 → 2 列(田字格)；更多 → 3 列', () => {
    expect(overviewColumns(0)).toBe(1)
    expect(overviewColumns(1)).toBe(1)
    expect(overviewColumns(2)).toBe(2)
    expect(overviewColumns(3)).toBe(3)
    expect(overviewColumns(4)).toBe(2)
    expect(overviewColumns(5)).toBe(3)
    expect(overviewColumns(6)).toBe(3)
    expect(overviewColumns(9)).toBe(3)
  })
})

describe('grouping + rollup', () => {
  it('groups are urgency-ordered (waiting first) and rollup counts match', async () => {
    const windows = ref([
      win(1, { status: 'idle' }),
      win(2, { status: 'waiting' }),
      win(3, { status: 'running' }),
    ])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    expect(ov.groups.value.map((g) => g.status)).toEqual(['waiting', 'running', 'idle'])
    expect(ov.rollup.value.waiting).toBe(1)
    expect(ov.rollup.value.running).toBe(1)
    expect(ov.rollup.value.idle).toBe(1)
  })
})

// TmuxStatusSheet was found rendering its own hand-derived, drifted dot colors (no
// done-unseen support, running mapped to grey instead of green) instead of this SSOT —
// see the two-consumer wiring in CliTerminalSurface.vue. Pin the exact values every
// dot-rendering consumer (TmuxPaneBar, TmuxStatusSheet, AgentOverview) must agree on,
// so a future edit to only one of them fails here instead of silently drifting again.
describe('STATUS_COLOR (single source every dot-rendering surface must agree on)', () => {
  it('defines exactly the three non-idle statuses, no more, no less', () => {
    expect(Object.keys(STATUS_COLOR).sort()).toEqual(['done-unseen', 'running', 'waiting'])
  })

  it('pins the canonical hex values (TmuxPaneBar.vue / TmuxStatusSheet.vue / AgentOverview.vue mirror these)', () => {
    expect(STATUS_COLOR.waiting).toBe('#ff5252')
    expect(STATUS_COLOR.running).toBe('#3fb950')
    expect(STATUS_COLOR['done-unseen']).toBe('#e3b341')
  })
})

// STATUS_MOTION is STATUS_COLOR's peer and gets the same treatment, for the same reason: it is
// bound into three SFCs via v-bind, so a value edited in only one of them must fail here.
//
// The contract these tests pin was a deliberate reversal (Human, 2026-07-20). The original rule
// was "only running moves, waiting stays static so the HUD owns attention"; an independent Witness
// read the result as backwards ("the ones waiting for me are still, the one I don't need to touch
// is blinking") because the HUD is event-scoped and collapses after 8s, leaving a static red dot
// beside a moving green one for most of the time on screen. Hence: both move now, with opposite
// characters. The old "waiting and done-unseen stay static" assertion is void — done-unseen alone
// is the static one.
describe('STATUS_MOTION (the three-state dot motion contract)', () => {
  it('is total over the non-idle statuses — done-unseen is explicitly null, not missing', () => {
    expect(Object.keys(STATUS_MOTION).sort()).toEqual(['done-unseen', 'running', 'waiting'])
    expect(STATUS_MOTION['done-unseen']).toBeNull()
  })

  it('pins waiting as the slow, big pulse and running as the quick, small one', () => {
    expect(STATUS_MOTION.waiting).toEqual({ duration: '2.6s', easing: 'ease-in-out', minOpacity: 0.35 })
    expect(STATUS_MOTION.running).toEqual({ duration: '2s', easing: 'ease-in-out', minOpacity: 0.75 })
  })

  // The actual design invariant, stated as the thing that can regress rather than as the numbers
  // above: attention hierarchy survives only while the red dot out-shouts the green one. A slow
  // opacity pulse's pull on the eye tracks its rate of luminance change, 2*amplitude/period — so
  // that, not period alone and not amplitude alone, is what has to stay ordered. Retuning either
  // entry is fine; inverting this is the regression that put us here.
  it('keeps waiting more salient than running (2*amplitude/period, the eye`s actual metric)', () => {
    const salience = (p: { duration: string; minOpacity: number }) =>
      (2 * (1 - p.minOpacity)) / parseFloat(p.duration)
    const waiting = salience(STATUS_MOTION.waiting)
    const running = salience(STATUS_MOTION.running)
    expect(waiting).toBeGreaterThan(running)
    // ...and not just barely: a hair's-width lead would read as "two things blinking", not as a
    // hierarchy. 2:1 is the margin the current values were chosen to hold.
    expect(waiting / running).toBeGreaterThanOrEqual(2)
    // Amplitude is the other half of "strong vs faint" and must point the same way.
    expect(1 - STATUS_MOTION.waiting.minOpacity).toBeGreaterThan(1 - STATUS_MOTION.running.minOpacity)
  })

  // A pulse that never dims is a lie dressed as an animation; static must be expressed as null so
  // the three consumers can omit the CSS entirely rather than run a no-op animation forever.
  it('never expresses static as minOpacity 1', () => {
    for (const pulse of Object.values(STATUS_MOTION)) {
      if (pulse) expect(pulse.minOpacity).toBeLessThan(1)
    }
  })
})
