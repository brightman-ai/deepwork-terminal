import { describe, it, expect } from 'bun:test'
import { createPromptTracker, type InterceptFacts } from '../composeIntercept'
import {
  createTypeToCompose,
  type CompositionEventLike,
  type KeyEventLike,
  type TypeToComposeDeps,
} from '../typeToCompose'

const TERMINAL = { name: 'xterm-helper-textarea' } as unknown as EventTarget
const COMPOSER = { name: 'composer-textarea' } as unknown as EventTarget
const OTHER = { name: 'rename-input' } as unknown as EventTarget

const baseFacts: Omit<InterceptFacts, 'promptEmpty'> = {
  enabled: true,
  surfaceActive: true,
  desktop: true,
  composerOpen: false,
  connected: true,
  agent: { tool: 'claude', status: 'idle', waitReason: '' },
  altScreen: false,
  copyMode: false,
  searchOpen: false,
  overlayOpen: false,
  scrolledBack: false,
}

function keyEvent(key: string, extra: Partial<KeyEventLike> = {}) {
  const calls = { preventDefault: 0, stopPropagation: 0, stopImmediatePropagation: 0 }
  const e: KeyEventLike = {
    key, ctrlKey: false, metaKey: false, altKey: false, isComposing: false, keyCode: key.length === 1 ? key.toUpperCase().charCodeAt(0) : 0,
    target: TERMINAL, defaultPrevented: false,
    preventDefault() { calls.preventDefault++ },
    stopPropagation() { calls.stopPropagation++ },
    stopImmediatePropagation() { calls.stopImmediatePropagation++ },
    ...extra,
  }
  return { e, calls }
}

function compEvent(data = '', target: EventTarget = TERMINAL) {
  const calls = { stopPropagation: 0, stopImmediatePropagation: 0 }
  const e: CompositionEventLike = {
    target, data,
    stopPropagation() { calls.stopPropagation++ },
    stopImmediatePropagation() { calls.stopImmediatePropagation++ },
  }
  return { e, calls }
}

function harness(over: Partial<Omit<InterceptFacts, 'promptEmpty'>> = {}) {
  const log = { openWithText: [] as string[], openForIme: 0, adopted: [] as string[], cleared: 0, reports: [] as Array<[string, Record<string, unknown>]> }
  const tracker = createPromptTracker()
  let facts = { ...baseFacts, ...over }
  let clock = 1000
  const deps: TypeToComposeDeps = {
    facts: () => facts,
    promptEmpty: () => tracker.isEmpty(),
    isTerminalInput: (t) => t === TERMINAL,
    openWithText: (t) => { log.openWithText.push(t); facts = { ...facts, composerOpen: true } },
    openForIme: () => { log.openForIme++; facts = { ...facts, composerOpen: true } },
    adoptCommittedText: (t) => { log.adopted.push(t) },
    clearTerminalInput: () => { log.cleared++ },
    report: (ev, d) => { log.reports.push([ev, d]) },
    now: () => clock,
  }
  return {
    ctl: createTypeToCompose(deps), log, tracker,
    set: (p: Partial<Omit<InterceptFacts, 'promptEmpty'>>) => { facts = { ...facts, ...p } },
    tick: (ms: number) => { clock += ms },
  }
}

describe('REQ-TI-01 打字即成框：keydown', () => {
  it('第一个可见字符：吃掉（preventDefault + 不再往下传），字进输入框，不发给终端', () => {
    const h = harness()
    const { e, calls } = keyEvent('h')
    expect(h.ctl.onKeydown(e)).toBe(true)
    expect(calls.preventDefault).toBe(1)
    expect(calls.stopPropagation).toBe(1)
    expect(calls.stopImmediatePropagation).toBe(1)
    expect(h.log.openWithText).toEqual(['h'])
  })

  it('输入框一开，后面的字就不再被拦（输入归框）', () => {
    const h = harness()
    h.ctl.onKeydown(keyEvent('h').e) // 打开
    const second = keyEvent('i')     // 万一焦点没换成、事件还落在终端 textarea 上
    expect(h.ctl.onKeydown(second.e)).toBe(false)
    expect(second.calls.preventDefault).toBe(0)
  })

  it('目标不是本 surface 的终端 textarea（重命名框、搜索框、输入框自己、另一格分屏）：一概不碰', () => {
    for (const target of [OTHER, COMPOSER, null]) {
      const h = harness()
      const { e, calls } = keyEvent('h', { target })
      expect(h.ctl.onKeydown(e)).toBe(false)
      expect(calls.preventDefault + calls.stopPropagation).toBe(0)
      expect(h.log.openWithText).toEqual([])
    }
  })

  it('别人已经处理过的事件（defaultPrevented）：不抢', () => {
    const h = harness()
    const { e } = keyEvent('h', { defaultPrevented: true })
    expect(h.ctl.onKeydown(e)).toBe(false)
    expect(h.log.openWithText).toEqual([])
  })

  it('REQ-TI-02：shell（没有 agent）→ 字原样直达，事件一点没动', () => {
    const h = harness({ agent: null })
    const { e, calls } = keyEvent('h')
    expect(h.ctl.onKeydown(e)).toBe(false)
    expect(calls).toEqual({ preventDefault: 0, stopPropagation: 0, stopImmediatePropagation: 0 })
  })

  it('REQ-TI-02：权限确认菜单里敲 y / 2 → 直达，一个键都不吞', () => {
    const h = harness({ agent: { tool: 'claude', status: 'waiting', waitReason: 'permission' } })
    for (const c of ['y', '2', 'n']) expect(h.ctl.onKeydown(keyEvent(c).e)).toBe(false)
    expect(h.log.openWithText).toEqual([])
  })

  it('REQ-TI-02：Ctrl+C / ⌘+V / Alt+B / 方向键 / Tab / Esc / 回车 / 退格 → 直达', () => {
    const h = harness()
    const keys: Array<[string, Partial<KeyEventLike>]> = [
      ['c', { ctrlKey: true }], ['v', { metaKey: true }], ['b', { altKey: true }],
      ['ArrowUp', {}], ['Tab', {}], ['Escape', {}], ['Enter', {}], ['Backspace', {}], ['F5', {}],
    ]
    for (const [k, extra] of keys) {
      const ev = keyEvent(k, extra)
      expect(h.ctl.onKeydown(ev.e)).toBe(false)
      expect(ev.calls.preventDefault).toBe(0)
    }
    expect(h.log.openWithText).toEqual([])
  })

  it('REQ-TI-02：提示为空时首字符是 / ! # → 直达', () => {
    for (const c of ['/', '!', '#']) {
      const h = harness()
      expect(h.ctl.onKeydown(keyEvent(c).e)).toBe(false)
      expect(h.log.openWithText).toEqual([])
    }
  })

  it('/help 整条命令不被劈开：`/` 直达并过账之后，`h e l p` 全部直达；回车提交后下一条才重新开始拦', () => {
    const h = harness()
    expect(h.ctl.onKeydown(keyEvent('/').e)).toBe(false)
    h.tracker.noteSent('/')
    for (const c of ['h', 'e', 'l', 'p']) {
      expect(h.ctl.onKeydown(keyEvent(c).e)).toBe(false)
      h.tracker.noteSent(c)
    }
    expect(h.log.openWithText).toEqual([])
    h.tracker.noteSent('\r')
    expect(h.ctl.onKeydown(keyEvent('x').e)).toBe(true)
    expect(h.log.openWithText).toEqual(['x'])
  })

  it('REQ-TI-08：总开关关了 → 什么都不拦', () => {
    const h = harness({ enabled: false })
    expect(h.ctl.onKeydown(keyEvent('h').e)).toBe(false)
    expect(h.log.openWithText).toEqual([])
  })

  it('wouldIntercept：只回答，不碰事件、不开框', () => {
    const h = harness()
    const { e, calls } = keyEvent('h')
    expect(h.ctl.wouldIntercept(e)).toBe(true)
    expect(calls.preventDefault + calls.stopPropagation + calls.stopImmediatePropagation).toBe(0)
    expect(h.log.openWithText).toEqual([])
    expect(harness({ agent: null }).ctl.wouldIntercept(keyEvent('h').e)).toBe(false)
  })

  it('诊断只记"差一点就拦了"的键，不记方向键 Tab 之类的噪音，也不带用户的字', () => {
    const h = harness({ agent: null })
    h.ctl.onKeydown(keyEvent('h').e)
    h.ctl.onKeydown(keyEvent('ArrowUp').e)
    h.ctl.onKeydown(keyEvent('c', { ctrlKey: true }).e)
    h.ctl.onKeydown(keyEvent(' ').e)
    expect(h.log.reports).toEqual([['type-to-compose.pass', { reason: 'not-agent' }]])
    const i = harness()
    i.ctl.onKeydown(keyEvent('h').e)
    expect(i.log.reports).toEqual([['type-to-compose.intercept', { mode: 'text', trigger: 'keydown' }]])
  })
})

describe('REQ-TI-01 输入法：Chrome 的顺序（keydown 在 compositionstart 之前）', () => {
  const imeKey = (extra: Partial<KeyEventLike> = {}) => keyEvent('n', { keyCode: 229, ...extra })

  it('拼写第一键：不 preventDefault（那会掐掉输入法），但终端看不到这一键，焦点当场交给输入框', () => {
    const h = harness()
    const { e, calls } = imeKey()
    expect(h.ctl.onKeydown(e)).toBe(true)
    expect(calls.preventDefault).toBe(0)
    expect(calls.stopPropagation).toBe(1)
    expect(calls.stopImmediatePropagation).toBe(1)
    expect(h.log.openForIme).toBe(1)
    expect(h.log.openWithText).toEqual([])
  })

  it('随后 compositionstart 落在输入框里（不是终端 textarea）：我们不管', () => {
    const h = harness()
    h.ctl.onKeydown(imeKey().e)
    const c = compEvent('', COMPOSER)
    expect(h.ctl.onCompositionStart(c.e)).toBe(false)
    expect(c.calls.stopPropagation).toBe(0)
  })

  it('输入法没接上新焦点、composition 仍落在终端 textarea：照样吞掉，不重复开框，定稿交给输入框', () => {
    const h = harness()
    h.ctl.onKeydown(imeKey().e)
    expect(h.ctl.onCompositionStart(compEvent().e)).toBe(true)
    expect(h.log.openForIme).toBe(1) // 没有第二次
    expect(h.ctl.onCompositionUpdate(compEvent('ni').e)).toBe(true)
    const end = compEvent('你')
    expect(h.ctl.onCompositionEnd(end.e)).toBe(true)
    expect(end.calls.stopImmediatePropagation).toBe(1)
    expect(h.log.adopted).toEqual(['你'])
    expect(h.log.cleared).toBe(1)
  })

  it('shell 里开着拼音：不拦', () => {
    const h = harness({ agent: null })
    const { e, calls } = imeKey()
    expect(h.ctl.onKeydown(e)).toBe(false)
    expect(calls.stopPropagation).toBe(0)
    expect(h.log.openForIme).toBe(0)
  })

  it('输入法激活时敲 /（提示为空）：命令前缀照旧直达', () => {
    const h = harness()
    expect(h.ctl.onKeydown(keyEvent('/', { keyCode: 229 }).e)).toBe(false)
  })
})

describe('REQ-TI-02 提示里已经有字了：输入法也不拦（一条提示不能劈在两个编辑器里）', () => {
  const imeKey = () => keyEvent('n', { keyCode: 229 })
  // 这些输入已经到达了 PTY：命令前缀放行后的字符、`!` 模式、一次粘贴、一次历史回溯。
  const reached: Array<[string, string]> = [
    ['/help', '/help'],
    ['!ls', '!ls'],
    ['粘贴', '\x1b[200~pasted text\x1b[201~'],
    ['历史回溯（方向键上）', '\x1b[A'],
    ['Tab 补全', '\t'],
  ]

  for (const [name, bytes] of reached) {
    it(`${name} 之后：keydown(229) 放行，终端照常看见这一键，不开框`, () => {
      const h = harness()
      h.tracker.noteSent(bytes)
      const { e, calls } = imeKey()
      expect(h.ctl.onKeydown(e)).toBe(false)
      expect(calls).toEqual({ preventDefault: 0, stopPropagation: 0, stopImmediatePropagation: 0 })
      expect(h.log.openForIme).toBe(0)
    })

    it(`${name} 之后：compositionstart 放行（Safari 顺序），终端自己处理拼写，不交接`, () => {
      const h = harness()
      h.tracker.noteSent(bytes)
      const c = compEvent()
      expect(h.ctl.onCompositionStart(c.e)).toBe(false)
      expect(c.calls.stopPropagation + c.calls.stopImmediatePropagation).toBe(0)
      expect(h.log.openForIme).toBe(0)
      // 后续的 update / end 也原样交给 xterm
      expect(h.ctl.onCompositionUpdate(compEvent('ni').e)).toBe(false)
      const end = compEvent('你')
      expect(h.ctl.onCompositionEnd(end.e)).toBe(false)
      expect(h.log.adopted).toEqual([])
      expect(h.log.cleared).toBe(0)
    })
  }

  it('回车提交、提示重新变空之后，两条路又都拦', () => {
    const h = harness()
    h.tracker.noteSent('/help')
    expect(h.ctl.onKeydown(imeKey().e)).toBe(false)
    h.tracker.noteSent('\r')
    expect(h.ctl.onKeydown(imeKey().e)).toBe(true)
    expect(h.log.openForIme).toBe(1)

    const h2 = harness()
    h2.tracker.noteSent('!ls')
    expect(h2.ctl.onCompositionStart(compEvent().e)).toBe(false)
    h2.tracker.noteSent('\r')
    expect(h2.ctl.onCompositionStart(compEvent().e)).toBe(true)
  })

  it('输入法激活时敲 `/`（提示为空）放行之后，紧随其后的 compositionstart 也放行 —— 同一次按键前后结论一致', () => {
    const h = harness()
    expect(h.ctl.onKeydown(keyEvent('/', { keyCode: 229 }).e)).toBe(false)
    const c = compEvent()
    expect(h.ctl.onCompositionStart(c.e)).toBe(false)
    expect(c.calls.stopPropagation).toBe(0)
    expect(h.log.openForIme).toBe(0)
  })

  it('这个"沿用放行"只管紧随其后的那一次：用掉之后、或超过窗口，下一次拼写按全新判定走', () => {
    const h = harness()
    h.ctl.onKeydown(keyEvent('/', { keyCode: 229 }).e)
    expect(h.ctl.onCompositionStart(compEvent().e)).toBe(false) // 用掉
    expect(h.ctl.onCompositionStart(compEvent().e)).toBe(true)  // 提示仍空 → 这回拦

    const h2 = harness()
    h2.ctl.onKeydown(keyEvent('/', { keyCode: 229 }).e)
    h2.tick(5000)
    expect(h2.ctl.onCompositionStart(compEvent().e)).toBe(true) // 过期
  })
})

describe('REQ-TI-01 输入法：Safari 的顺序（compositionstart 在 keydown 之前）', () => {
  it('拼写已经在终端 textarea 里开始：吞掉 composition，开框，等定稿交接', () => {
    const h = harness()
    const start = compEvent()
    expect(h.ctl.onCompositionStart(start.e)).toBe(true)
    expect(start.calls.stopImmediatePropagation).toBe(1)
    expect(h.log.openForIme).toBe(1)
    expect(h.ctl.onCompositionUpdate(compEvent('ni').e)).toBe(true)
    expect(h.ctl.onCompositionEnd(compEvent('你').e)).toBe(true)
    expect(h.log.adopted).toEqual(['你'])
    expect(h.log.cleared).toBe(1)
  })

  it('交接期间终端 textarea 上的 keydown(229)（含确认候选的 Enter）整段不让 xterm 看见', () => {
    const h = harness()
    h.ctl.onCompositionStart(compEvent().e)
    const enter = keyEvent('Enter', { keyCode: 229 })
    expect(h.ctl.onKeydown(enter.e)).toBe(true)
    expect(enter.calls.stopImmediatePropagation).toBe(1)
  })

  it('交接结束后 composition 事件恢复正常', () => {
    const h = harness()
    h.ctl.onCompositionStart(compEvent().e)
    h.ctl.onCompositionEnd(compEvent('你').e)
    expect(h.ctl.onCompositionUpdate(compEvent('x').e)).toBe(false)
    expect(h.ctl.onCompositionEnd(compEvent('x').e)).toBe(false)
  })

  it('compositionend 一直没来（被取消）：下一个非拼写按键给交接收尾', () => {
    const h = harness()
    h.ctl.onCompositionStart(compEvent().e)
    h.set({ composerOpen: false }) // 输入框随后被关了
    expect(h.ctl.onKeydown(keyEvent('a').e)).toBe(true) // 这一键自己照常被判定（会拦）
    expect(h.ctl.onCompositionEnd(compEvent('x').e)).toBe(false) // 交接已结束
  })

  it('取消的拼写（compositionend 没有数据）：不交接空串，但终端 textarea 要清掉', () => {
    const h = harness()
    h.ctl.onCompositionStart(compEvent().e)
    h.ctl.onCompositionEnd(compEvent('').e)
    expect(h.log.adopted).toEqual([])
    expect(h.log.cleared).toBe(1)
  })

  it('shell 里：compositionstart 不碰', () => {
    const h = harness({ agent: null })
    const c = compEvent()
    expect(h.ctl.onCompositionStart(c.e)).toBe(false)
    expect(c.calls.stopPropagation).toBe(0)
    expect(h.log.openForIme).toBe(0)
  })

  it('不是终端 textarea 的 composition（重命名框里拼写）：不碰', () => {
    const h = harness()
    expect(h.ctl.onCompositionStart(compEvent('', OTHER).e)).toBe(false)
  })

  it('keydown(229) 拦下已久（超过容忍窗口）之后才来的 compositionstart 按全新判定走', () => {
    const h = harness()
    h.ctl.onKeydown(keyEvent('n', { keyCode: 229 }).e)
    h.tick(5000)
    h.set({ composerOpen: false })
    expect(h.ctl.onCompositionStart(compEvent().e)).toBe(true)
    expect(h.log.openForIme).toBe(2) // 窗口过了，不当成同一次，重新开
  })
})
