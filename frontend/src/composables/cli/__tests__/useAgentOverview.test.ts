import { describe, it, expect } from 'bun:test'
import { ref, nextTick } from 'vue'
import {
  useAgentOverview,
  windowRawStatus,
  windowCwd,
  windowTool,
  overviewColumns,
} from '@terminal/composables/cli/useAgentOverview'
import type { TmuxWindowState } from '@terminal/types/terminal'

type WinOpts = {
  status?: 'waiting' | 'running' | 'idle'
  cwd?: string
  tool?: string
  active?: boolean
  windowId?: string
  awaiting?: boolean // backend "needs-you": finished a turn, not yet responded to
}
function win(index: number, opts: WinOpts = {}): TmuxWindowState {
  const { status = 'idle', cwd = '', tool = '', active = false, windowId = `@${index}`, awaiting = false } = opts
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
      } as never,
    ],
  }
}

describe('windowRawStatus / cwd / tool', () => {
  it('waiting > running > idle, and reads active-pane cwd/tool', () => {
    expect(windowRawStatus(win(1, { status: 'waiting' }))).toBe('waiting')
    expect(windowRawStatus(win(1, { status: 'running' }))).toBe('running')
    expect(windowRawStatus(win(1, { status: 'idle' }))).toBe('idle')
    expect(windowCwd(win(1, { cwd: '/tmp/x' }))).toBe('/tmp/x')
    expect(windowTool(win(1, { tool: 'claude' }))).toBe('claude')
  })
})

describe('needs-you state (backend awaitingUser + client dismiss)', () => {
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

  it('being the ACTIVE window does NOT clear it (glancing ≠ responding)', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true, active: true })])
    const ov = useAgentOverview(windows, ref(false))
    await nextTick()
    // Old behaviour auto-marked the active pane "seen" → idle; now only a response/dismiss clears.
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')
  })

  it('a fresh idle window (never ran, no awaitingUser) is idle, not done-unseen', async () => {
    const windows = ref([win(2, { status: 'idle', windowId: '@9' })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')
  })

  it('dismiss is re-armed on the next run → a NEW completion shows again', async () => {
    const windows = ref([win(1, { status: 'idle', awaiting: true })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    ov.dismiss(windows.value[0])
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')

    windows.value = [win(1, { status: 'running' })] // you responded → re-arms
    await nextTick()
    windows.value = [win(1, { status: 'idle', awaiting: true })] // finished again
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')
  })
})

describe('overviewColumns (PC 活跃大卡每行 ≤3, 4→2×2 田字格)', () => {
  it('n≤3 → n 列；4 → 2 列(田字格)；更多 → 3 列', () => {
    expect(overviewColumns(0)).toBe(1) // 空也别塌成 0 列
    expect(overviewColumns(1)).toBe(1)
    expect(overviewColumns(2)).toBe(2)
    expect(overviewColumns(3)).toBe(3)
    expect(overviewColumns(4)).toBe(2) // 2×2 田字格，不是 3+1
    expect(overviewColumns(5)).toBe(3)
    expect(overviewColumns(6)).toBe(3)
    expect(overviewColumns(9)).toBe(3) // 每行 ≤3 恒成立
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
