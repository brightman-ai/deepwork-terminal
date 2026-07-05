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
}
function win(index: number, opts: WinOpts = {}): TmuxWindowState {
  const { status = 'idle', cwd = '', tool = '', active = false, windowId = `@${index}` } = opts
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

describe('seen state machine (AOV-5 done-unseen)', () => {
  it('running→idle while unwatched → done-unseen; markViewed → idle', async () => {
    const windows = ref([win(1, { status: 'running' })])
    const overviewOpen = ref(true) // open → the active window is NOT auto-marked seen
    const ov = useAgentOverview(windows, overviewOpen)
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('running')

    windows.value = [win(1, { status: 'idle' })] // agent finished
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('done-unseen')

    ov.markViewed(windows.value[0]) // user opened it
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')
  })

  it('the active window, while the overview is CLOSED, is continuously seen (never done-unseen)', async () => {
    const windows = ref([win(1, { status: 'running', active: true })])
    const ov = useAgentOverview(windows, ref(false))
    await nextTick()
    windows.value = [win(1, { status: 'idle', active: true })]
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle')
  })

  it('a reused window index does NOT inherit stale done-unseen (keyed on windowId)', async () => {
    const windows = ref([win(2, { status: 'running', windowId: '@5' })])
    const ov = useAgentOverview(windows, ref(true))
    await nextTick()
    // @5 closes; a brand-new window reuses index 2 but has a different id @9, and is idle.
    windows.value = [win(2, { status: 'idle', windowId: '@9' })]
    await nextTick()
    expect(ov.effectiveStatus(windows.value[0])).toBe('idle') // fresh window, not "finished"
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
