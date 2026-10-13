import { describe, it, expect } from 'bun:test'
import {
  INTERRUPT_SEQUENCE,
  SIGINT_SEQUENCE,
  SURFACE_ACTION_ORDER,
  buildSurfaceActions,
  shortCwd,
  surfaceActionTip,
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
    pageUp: () => { calls.push('pageUp') },
    pageDown: () => { calls.push('pageDown') },
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

// search/page-up/page-down/delete 常驻——笔记本缺键补充，不挂判定，任何状态下都在场。
const ALWAYS = ['search', 'page-up', 'page-down', 'delete']

describe('动作表 — 谁在场', () => {
  it('静息态四个常驻项都在，其余三个都是有条件的', () => {
    expect(ids({})).toEqual(ALWAYS)
  })

  it('离底才出现「回到最新」，贴底就消失', () => {
    expect(ids({ atBottom: false, newLinesBelow: 12 })).toEqual([...ALWAYS, 'to-bottom'])
    expect(ids({ atBottom: true, newLinesBelow: 0 })).toEqual(ALWAYS)
  })

  it('有选区才出现「复制选中」——"复制什么"永远有答案', () => {
    expect(ids({ hasSelection: true })).toEqual([...ALWAYS, 'copy'])
    expect(ids({ hasSelection: false })).toEqual(ALWAYS)
  })

  it('检测到 agent 且在跑，才出现「中断」（裸 shell 不配单击中断）', () => {
    expect(ids({ agentRunning: true })).toEqual([...ALWAYS, 'interrupt'])
    expect(ids({ agentRunning: false })).toEqual(ALWAYS)
  })

  it('顺序固定：条件动作只在自己的位置上出现，从不重排', () => {
    expect(ids({ atBottom: false, hasSelection: true, agentRunning: true }))
      .toEqual([...ALWAYS, 'to-bottom', 'copy', 'interrupt'])
    // 中间那个缺席时，后面的不会往前挪——位置由 SURFACE_ACTION_ORDER 定死。
    expect(ids({ atBottom: false, hasSelection: false, agentRunning: true }))
      .toEqual([...ALWAYS, 'to-bottom', 'interrupt'])
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

  it('PgUp/PgDn 走各自的槽位，不借道 sendKey——normal buffer 下才会滚 scrollback 而非裸发给 PTY', () => {
    const { deps, calls, sent } = spyDeps()
    const all = buildSurfaceActions(REST, deps)
    all.find((a) => a.id === 'page-up')!.run()
    all.find((a) => a.id === 'page-down')!.run()
    expect(calls).toEqual(['pageUp', 'pageDown'])
    expect(sent).toEqual([]) // 没有一个字节走 sendKey——分流判断留给调用方的 pageUp/pageDown 实现
  })

  it('「向前删除」发的是 \\x1b[3~（forward-delete），不是 \\x7f（backspace）', () => {
    const { deps, sent } = spyDeps()
    buildSurfaceActions(REST, deps).find((a) => a.id === 'delete')!.run()
    expect(sent).toEqual(['\x1b[3~'])
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

describe('动作表 — 看得懂（2026-10-13：一排图标只有搜索看得懂）', () => {
  const all = () => buildSurfaceActions(REST, spyDeps().deps, 'Ctrl + F')

  it('补物理键的三个动作渲染成键帽文字，其余用图标', () => {
    const keycaps = Object.fromEntries(all().map((a) => [a.id, a.keycap]))
    expect(keycaps).toEqual({
      search: '',
      'page-up': 'PgUp',
      'page-down': 'PgDn',
      delete: 'Del',
      'to-bottom': '',
      copy: '',
      interrupt: '',
    })
  })

  it('每个动作都有提示主文；「向前删除」的提示讲清它不是退格', () => {
    for (const a of all()) expect(a.label.length).toBeGreaterThan(0)
    const del = all().find((a) => a.id === 'delete')!
    expect(surfaceActionTip(del)).toContain('不是退格')
  })

  it('提示全文 = 主文 · 第二段；没有第二段就只有主文', () => {
    expect(surfaceActionTip({ label: '在终端里搜索', hint: 'Ctrl + F' })).toBe('在终端里搜索 · Ctrl + F')
    expect(surfaceActionTip({ label: '复制选中的文字', hint: '' })).toBe('复制选中的文字')
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
