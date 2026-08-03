import { describe, it, expect, beforeEach, afterAll } from 'bun:test'
import { ref, nextTick } from 'vue'
import {
  useAgentOverview,
  windowRawStatus,
  windowCwd,
  windowTool,
  windowAgentSignals,
  windowAwaitingSince,
  windowActivityAt,
  overviewCardTitle,
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
  // 证据的年龄。加它是因为一个 pane 曾经"运行中"了十个小时——状态本身没有年龄时，真的在跑
  // 和十小时前那条没人再写的 transcript，在屏幕上长得一模一样。
  it('windowActivityAt takes the newest agent pane, and is 0 when nothing can be dated', () => {
    const older = '2026-08-03T01:00:00.000Z'
    const newer = '2026-08-03T02:00:00.000Z'
    const w = win(9, { tool: 'claude', status: 'running' })
    w.panes[0].activityAt = older
    w.panes.push({ index: 1, active: false, agentTool: 'codex', agentStatus: 'running', activityAt: newer } as never)
    expect(windowActivityAt(w)).toBe(Date.parse(newer))

    // 没有 agent 的 pane 不算数：它的 transcript 时间根本不存在，别拿它冒充活跃度。
    const bare = win(10, {})
    bare.panes[0].activityAt = newer
    expect(windowActivityAt(bare)).toBe(0)

    expect(windowActivityAt(win(11, { tool: 'claude' }))).toBe(0) // 有 agent 但没时间戳

    // Go 的零时间不会被 omitempty 吃掉，会原样序列化成 0001-01-01 —— 定位不到 transcript 的
    // pane 就是这样过来的。不挡住，提示框里会出现"17755921 小时前"这种东西。
    const undated = win(12, { tool: 'codex', status: 'running' })
    undated.panes[0].activityAt = '0001-01-01T00:00:00Z'
    expect(windowActivityAt(undated)).toBe(0)
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

  it('an UNDATED wait is dismissable for THIS session only — cleared now, re-asked after F5', async () => {
    // Two requirements pull against each other here. An undated wait carries no identity, so a
    // PERSISTED dismissal could never be invalidated by a later turn and would mute the window
    // forever — hence the original fail-open rule. But refusing the dismissal outright is what the
    // user experiences as "这个红点竟然去除不掉". Session-scoped satisfies both: the explicit tap
    // works, and a reload honestly re-asks rather than silently swallowing a high-signal wait.
    const windows = ref([win(1, { status: 'idle', awaiting: true, since: TZERO })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    ov.dismiss(windows.value[0])
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')

    // F5 → shown again: nothing was written to storage.
    const ov2 = useAgentOverview(ref([win(1, { status: 'idle', awaiting: true, since: TZERO })]), ref(true))
    await nextTick()
    expect(ov2.effectiveStatus(win(1, { status: 'idle', awaiting: true, since: TZERO }))).toBe('done-unseen')
  })

  it('a dismissed UNDATED wait re-arms once the window stops awaiting', async () => {
    // Without this the session-scoped dismissal would swallow the window's NEXT prompt too, which
    // is the failure mode the fail-open rule was protecting against in the first place.
    const windows = ref([win(1, { status: 'idle', awaiting: true, since: TZERO })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    ov.dismiss(windows.value[0])
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')

    windows.value = [win(1, { status: 'running' })] // you replied; it went back to work
    await nextTick()
    windows.value = [win(1, { status: 'idle', awaiting: true, since: TZERO })] // new prompt
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')
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

// 布局几何是**一套**，两条数据源（tmux window / 非 tmux session）共用。曾经有过一个「少卡档」
// （≤4 个单元时空闲也当大卡、单卡宽度封顶 520px、恰好 4 张排成 2×2），Human 实测判死：4 个会话
// 被排成 2×2、每张卡大到占满整屏、几乎不滚动，一屏扫到的会话比 tmux 的一行 3 张还少。下面这组
// 用例把"删干净了"钉死：只剩一个只看卡片数的函数，且卡片数只决定列数、不决定卡片大小。
describe('overviewColumns (唯一的布局几何：宽屏一行 ≤3 张)', () => {
  it('不足 3 张按张数给列，3 张及以上恒 3 列', () => {
    expect(overviewColumns(0)).toBe(1) // 0 列的网格没有意义，下限 1
    expect(overviewColumns(1)).toBe(1)
    expect(overviewColumns(2)).toBe(2)
    expect(overviewColumns(3)).toBe(3)
    expect(overviewColumns(5)).toBe(3)
    expect(overviewColumns(6)).toBe(3)
    expect(overviewColumns(9)).toBe(3)
  })

  // 用户实测的那一屏：4 个会话。旧规则给 2 列（2×2 巨卡），归一后必须是 3 列 + 第 4 张换行，
  // 也就是"多出来的排到下一行、继续纵向滚"，而不是把卡片撑大去填满一屏。
  it('4 张不再是 2×2 田字格，而是和 tmux 一样的一行 3 张', () => {
    expect(overviewColumns(4)).toBe(3)
  })

  // 稀疏档留下的最后一点痕迹也不许有：任何张数下，列数都只是"张数封顶 3"这一条规则的结果。
  // 一旦有人再按张数塞进第二档（某个区间返回别的列数），这里立刻炸。
  it('列数是卡片数的单调不减函数，且恒等于 min(max(n,1), 3)', () => {
    let prev = 0
    for (let n = 0; n <= 24; n++) {
      const cols = overviewColumns(n)
      expect(cols).toBe(Math.min(Math.max(n, 1), 3))
      expect(cols).toBeGreaterThanOrEqual(prev)
      prev = cols
    }
  })
})

// 非 tmux 那条路径上，四张卡的标题全是一个字不差的「终端」（服务端 session 名的默认值），编号
// 又已经在左边的徽标里 —— 标题那一行等于没有信息。tmux 侧之所以可扫读，是因为窗口名就是项目名；
// 这组用例把"两条路径同一套命名规则"钉死：自定义名 → cwd basename → 终端N。
describe('overviewCardTitle (卡片标题：一眼分得出是哪个终端)', () => {
  const unit = (title: string, cwd: string, index = 1) => ({ title, cwd, index })

  it('用户自己起的名永远赢（tmux 窗口名就是这一档，行为一个字不变）', () => {
    expect(overviewCardTitle(unit('ws portal 2', '/home/me/deepwork-pro'))).toBe('ws portal 2')
    expect(overviewCardTitle(unit('voice-code', ''))).toBe('voice-code')
    // 名字比 basename 短/怪也听用户的 —— 改过名就是表过态。
    expect(overviewCardTitle(unit('部署', '/home/me/deepwork-terminal'))).toBe('部署')
  })

  it('自动生成的占位名（「终端」/「终端3」/空）退回 cwd 的 basename', () => {
    expect(overviewCardTitle(unit('终端', '/home/me/code/deepwork-terminal'))).toBe('deepwork-terminal')
    expect(overviewCardTitle(unit('终端 3', '/home/me/code/teamworkbench'))).toBe('teamworkbench')
    expect(overviewCardTitle(unit('终端3', '/home/me/code/voice-code'))).toBe('voice-code')
    expect(overviewCardTitle(unit('', '/srv/app'))).toBe('app')
    expect(overviewCardTitle(unit('   ', '/srv/app'))).toBe('app')
    // 尾部斜杠不能把 basename 吃成空串。
    expect(overviewCardTitle(unit('终端', '/home/me/code/deepwork-terminal/'))).toBe('deepwork-terminal')
    // 相对路径/单段路径同样取最后一段。
    expect(overviewCardTitle(unit('终端', 'deepwork-terminal'))).toBe('deepwork-terminal')
  })

  it('连 cwd 都没有才回落「终端N」，编号与标签栏/前缀+N 对得上', () => {
    expect(overviewCardTitle(unit('终端', '', 4))).toBe('终端4')
    expect(overviewCardTitle(unit('', '', 2))).toBe('终端2')
    // 根目录没有可读的最后一段，也走编号回落，而不是显示一个孤零零的「/」。
    expect(overviewCardTitle(unit('终端', '/', 7))).toBe('终端7')
  })

  it('同 cwd 的多个终端不会互相冒充：编号仍在卡片左侧徽标里（标题相同是允许的）', () => {
    // 这里显式记录取舍：basename 可能重名，但"看得出是哪个项目"比"绝对唯一"更有用，
    // 唯一性由卡片上恒显的 w.index 承担 —— 它同时是 终端N 和 前缀+N 的目标。
    expect(overviewCardTitle(unit('终端', '/home/me/code/dw', 1))).toBe('dw')
    expect(overviewCardTitle(unit('终端', '/home/me/code/dw', 2))).toBe('dw')
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
