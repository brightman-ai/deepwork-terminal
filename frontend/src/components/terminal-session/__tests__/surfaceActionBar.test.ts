import { describe, it, expect } from 'bun:test'
import {
  INTERRUPT_SEQUENCE,
  SIGINT_SEQUENCE,
  SURFACE_ACTION_ORDER,
  buildSurfaceActions,
  shortCwd,
  surfaceSlot,
  visibleSurfaceActions,
  type SurfaceActionDeps,
  type SurfaceActionState,
} from '../surfaceActionBar'

/** 一个什么都记下来、什么都不做的 deps。 */
function spyDeps() {
  const calls: string[] = []
  const sent: string[] = []
  const deps: SurfaceActionDeps = {
    openSearch: () => { calls.push('openSearch') },
    scrollToBottom: () => { calls.push('scrollToBottom') },
    copySelection: () => { calls.push('copySelection') },
    sendKey: (s) => { calls.push('sendKey'); sent.push(s) },
  }
  return { deps, calls, sent }
}

const REST: SurfaceActionState = {
  atBottom: true,
  newLinesBelow: 0,
  hasSelection: false,
  agentRunning: false,
}

function ids(state: Partial<SurfaceActionState>): string[] {
  return visibleSurfaceActions({ ...REST, ...state }, spyDeps().deps).map((a) => a.id)
}

describe('动作表 — 谁在场', () => {
  it('静息态只有「搜索」：其余三个都是有条件的', () => {
    expect(ids({})).toEqual(['search'])
  })

  it('离底才出现「回到最新」，贴底就消失', () => {
    expect(ids({ atBottom: false, newLinesBelow: 12 })).toEqual(['search', 'to-bottom'])
    expect(ids({ atBottom: true, newLinesBelow: 0 })).toEqual(['search'])
  })

  it('有选区才出现「复制选中」——"复制什么"永远有答案', () => {
    expect(ids({ hasSelection: true })).toEqual(['search', 'copy'])
    expect(ids({ hasSelection: false })).toEqual(['search'])
  })

  it('检测到 agent 且在跑，才出现「中断」（裸 shell 不配单击中断）', () => {
    expect(ids({ agentRunning: true })).toEqual(['search', 'interrupt'])
    expect(ids({ agentRunning: false })).toEqual(['search'])
  })

  it('顺序固定：条件动作只在自己的位置上出现，从不重排', () => {
    expect(ids({ atBottom: false, hasSelection: true, agentRunning: true }))
      .toEqual(['search', 'to-bottom', 'copy', 'interrupt'])
    // 中间那个缺席时，后面的不会往前挪——位置由 SURFACE_ACTION_ORDER 定死。
    expect(ids({ atBottom: false, hasSelection: false, agentRunning: true }))
      .toEqual(['search', 'to-bottom', 'interrupt'])
    expect(buildSurfaceActions(REST, spyDeps().deps).map((a) => a.id)).toEqual([...SURFACE_ACTION_ORDER])
  })
})

describe('动作表 — 按下去发生什么', () => {
  it('「中断」发的是 Esc，绝不是 Ctrl+C', () => {
    const { deps, sent } = spyDeps()
    const interrupt = buildSurfaceActions({ ...REST, agentRunning: true }, deps).find((a) => a.id === 'interrupt')!
    interrupt.run()
    expect(sent).toEqual([INTERRUPT_SEQUENCE])
    expect(sent[0]).toBe('\x1b')
    // Ctrl+C 一次误点就是杀进程、丢上下文；Esc 只停当前生成，会话保住。
    expect(sent[0]).not.toBe(SIGINT_SEQUENCE)
  })

  it('其余三个各自打到自己的那个口子上', () => {
    const { deps, calls } = spyDeps()
    const all = buildSurfaceActions({ ...REST, atBottom: false, hasSelection: true }, deps)
    all.find((a) => a.id === 'search')!.run()
    all.find((a) => a.id === 'to-bottom')!.run()
    all.find((a) => a.id === 'copy')!.run()
    expect(calls).toEqual(['openSearch', 'scrollToBottom', 'copySelection'])
  })

  it('「回到最新」带着"我错过了多少"，并在三位数时收成 99+', () => {
    const at = (n: number) => buildSurfaceActions({ ...REST, atBottom: false, newLinesBelow: n }, spyDeps().deps)
      .find((a) => a.id === 'to-bottom')!
    expect(at(7).badge).toBe('7')
    expect(at(7).hint).toBe('7 行新输出')
    expect(at(1200).badge).toBe('99+')
    expect(at(0).badge).toBe('') // 数不出来就不显示数字，而不是显示一个 0
  })

  it('搜索的快捷键文案来自调用方（= 用户自己的配置），不在这里写死', () => {
    const search = buildSurfaceActions(REST, spyDeps().deps, 'Ctrl + Shift + F').find((a) => a.id === 'search')!
    expect(search.hint).toBe('Ctrl + Shift + F')
  })
})

describe('中区内容槽 — 逐级回落，永不空着', () => {
  const CWD = '/home/u/code/stwork/deepwork-terminal'

  it('第一档：agent 那一句压过一切', () => {
    expect(surfaceSlot({ agent: 'Codex 说：“任务已完成”', title: 'npm run build', cwd: CWD }))
      .toEqual({ text: 'Codex 说：“任务已完成”', tier: 'agent' })
  })

  it('第二档：没有 agent 的话，就说正在跑的命令（终端标题）', () => {
    expect(surfaceSlot({ agent: '', title: 'npm run build', cwd: CWD }))
      .toEqual({ text: 'npm run build', tier: 'title' })
  })

  it('第三档：都没有就回答"我在哪"——cwd 压到最后两段', () => {
    expect(surfaceSlot({ agent: '', title: '', cwd: CWD }))
      .toEqual({ text: '…/stwork/deepwork-terminal', tier: 'cwd' })
  })

  it('shell 把路径写进标题时不算"正在跑的命令"（否则同一句话在同一行出现两次）', () => {
    expect(surfaceSlot({ agent: '', title: CWD, cwd: CWD }).tier).toBe('cwd')
    expect(surfaceSlot({ agent: '', title: '…/stwork/deepwork-terminal', cwd: CWD }).tier).toBe('cwd')
  })

  it('三档全空才闭嘴（整格不渲染，不留占位字样）', () => {
    expect(surfaceSlot({ agent: '', title: '', cwd: '' })).toEqual({ text: '', tier: 'none' })
    expect(surfaceSlot({ agent: '   ', title: ' ', cwd: '  ' }).tier).toBe('none')
  })

  it('cwd 砍头不砍尾：有信息量的永远是尾巴', () => {
    expect(shortCwd('/home/u/code/stwork/deepwork-terminal')).toBe('…/stwork/deepwork-terminal')
    expect(shortCwd('/home/u')).toBe('/home/u')   // 本来就短，不加省略号
    expect(shortCwd('/')).toBe('/')
    expect(shortCwd('')).toBe('')
  })
})
