import { describe, it, expect } from 'bun:test'
import {
  agentAcceptsTyping,
  agentInputFacts,
  classifyKey,
  createPromptTracker,
  decideIntercept,
  type AgentInputFacts,
  type InterceptFacts,
  type KeyFacts,
} from '../composeIntercept'

const key = (k: string, extra: Partial<KeyFacts> = {}): KeyFacts => ({
  key: k, ctrlKey: false, metaKey: false, altKey: false, ...extra,
})

const idleClaude: AgentInputFacts = { tool: 'claude', status: 'idle', waitReason: '' }

/** 一切条件都满足、会拦的基线；每个用例只改一个字段，证明"这一条就足以放行"。 */
const base: InterceptFacts = {
  enabled: true,
  surfaceActive: true,
  desktop: true,
  composerOpen: false,
  connected: true,
  agent: idleClaude,
  altScreen: false,
  copyMode: false,
  searchOpen: false,
  overlayOpen: false,
  scrolledBack: false,
  promptEmpty: true,
}
const text = (k: string) => ({ kind: 'key' as const, key: key(k) })

describe('agentAcceptsTyping — REQ-TI-01/02：agent 此刻能不能收字', () => {
  for (const [tool, status, waitReason] of [
    ['claude', 'idle', ''],
    ['codex', 'running', ''],
    ['gemini', 'idle', 'prompt'],
    ['opencode', 'running', ''],
    ['claude', 'waiting', 'prompt'],
  ] as const) {
    it(`${tool} / ${status} / ${waitReason || '-'} → 能`, () => {
      expect(agentAcceptsTyping({ tool, status, waitReason })).toBe(true)
    })
  }

  for (const [name, a] of [
    ['权限确认', { tool: 'claude', status: 'waiting', waitReason: 'permission' }],
    ['选项菜单', { tool: 'claude', status: 'waiting', waitReason: 'question' }],
    ['waiting 但没说原因', { tool: 'claude', status: 'waiting', waitReason: '' }],
    ['running 配着残留的 permission（矛盾帧，宁可当菜单）', { tool: 'claude', status: 'running', waitReason: 'permission' }],
    ['idle 配着 question', { tool: 'claude', status: 'idle', waitReason: 'question' }],
    ['进程已退出', { tool: 'claude', status: 'done', waitReason: '' }],
    ['没有 agent', { tool: '', status: 'none', waitReason: '' }],
    ['状态未知（还没收到推送）', { tool: 'claude', status: '', waitReason: '' }],
  ] as const) {
    it(`${name} → 不能`, () => {
      expect(agentAcceptsTyping(a as AgentInputFacts)).toBe(false)
    })
  }

  it('没有 agent 事实 → 不能', () => {
    expect(agentAcceptsTyping(null)).toBe(false)
  })
})

describe('agentInputFacts — 会话级推送 + tmux 当前 pane', () => {
  const session = { tool: 'claude', status: 'idle', waitReason: 'prompt' } as const

  it('非 tmux：直接用会话级推送', () => {
    expect(agentInputFacts({ session, tmuxAttached: false, pane: null })).toEqual({
      tool: 'claude', status: 'idle', waitReason: 'prompt',
    })
  })

  it('非 tmux 且没有推送 → null', () => {
    expect(agentInputFacts({ session: null, tmuxAttached: false, pane: null })).toBeNull()
  })

  it('tmux：tool/status 取光标所在 pane 的，不取会话级 roll-up', () => {
    const f = agentInputFacts({
      session,
      tmuxAttached: true,
      pane: { agentTool: 'codex', agentStatus: 'running' },
    })
    expect(f).toEqual({ tool: 'codex', status: 'running', waitReason: '' }) // claude 的 waitReason 不借给 codex
  })

  it('tmux：会话级推送说的是同一个 tool 时才借它的 waitReason（pane 本身不带）', () => {
    const f = agentInputFacts({
      session: { tool: 'claude', status: 'waiting', waitReason: 'permission' },
      tmuxAttached: true,
      pane: { agentTool: 'claude', agentStatus: 'waiting' },
    })
    expect(f?.waitReason).toBe('permission')
  })

  it('tmux：光标所在的是 shell pane（旁边分屏有 agent）→ null，字照旧直达', () => {
    expect(agentInputFacts({ session, tmuxAttached: true, pane: { agentTool: '' } })).toBeNull()
    expect(agentInputFacts({ session, tmuxAttached: true, pane: {} })).toBeNull()
  })

  it('tmux：pane 还不知道 → null（保守放行）', () => {
    expect(agentInputFacts({ session, tmuxAttached: true, pane: null })).toBeNull()
  })
})

describe('classifyKey', () => {
  for (const [k, cls] of [
    ['a', 'text'], ['Z', 'text'], ['你', 'text'], ['😀', 'text'], ['1', 'text'], ['.', 'text'],
    [' ', 'whitespace'], ['\t', 'whitespace'],
    ['Enter', 'non-text'], ['Tab', 'non-text'], ['Escape', 'non-text'], ['Backspace', 'non-text'],
    ['ArrowUp', 'non-text'], ['F5', 'non-text'], ['Dead', 'non-text'], ['Shift', 'non-text'],
  ] as const) {
    it(`${JSON.stringify(k)} → ${cls}`, () => {
      expect(classifyKey(key(k))).toBe(cls)
    })
  }

  it('Ctrl / ⌘ / Alt 无论按的是什么都是 modified', () => {
    expect(classifyKey(key('a', { ctrlKey: true }))).toBe('modified')
    expect(classifyKey(key('a', { metaKey: true }))).toBe('modified')
    expect(classifyKey(key('a', { altKey: true }))).toBe('modified')
    expect(classifyKey(key('ArrowUp', { ctrlKey: true }))).toBe('modified')
  })

  it('输入法：key=Process 或 keyCode=229 是拼写开始；isComposing 是拼写进行中', () => {
    expect(classifyKey(key('Process'))).toBe('ime-start')
    // macOS Chrome：输入法激活时 key 仍是真实字母，只有 keyCode 说了实话
    expect(classifyKey(key('n', { keyCode: 229 }))).toBe('ime-start')
    expect(classifyKey(key('n', { keyCode: 229, isComposing: false }))).toBe('ime-start')
    expect(classifyKey(key('n', { isComposing: true, keyCode: 229 }))).toBe('ime-ongoing')
  })

  it('Shift+字母仍是可见字符（大写不是组合键）', () => {
    expect(classifyKey({ ...key('A'), ctrlKey: false })).toBe('text')
  })
})

describe('decideIntercept — REQ-TI-01：满足一切条件时拦', () => {
  it('可见字符 → 拦，并带上那个字符', () => {
    expect(decideIntercept(base, text('h'))).toEqual({ intercept: true, mode: 'text', text: 'h' })
  })

  it('CJK 字符（直接键入）同样拦', () => {
    expect(decideIntercept(base, text('你'))).toEqual({ intercept: true, mode: 'text', text: '你' })
  })

  it('agent 正在运行也拦（它们运行中也接受排队输入）', () => {
    const f = { ...base, agent: { tool: 'codex', status: 'running', waitReason: '' } as const }
    expect(decideIntercept(f, text('h')).intercept).toBe(true)
  })

  for (const tool of ['claude', 'codex', 'gemini', 'opencode'] as const) {
    it(`${tool} 都算 agent`, () => {
      expect(decideIntercept({ ...base, agent: { tool, status: 'idle', waitReason: '' } }, text('h')).intercept).toBe(true)
    })
  }

  it('输入法拼写开始（keydown 229）→ 拦，模式是 ime', () => {
    expect(decideIntercept(base, { kind: 'key', key: key('n', { keyCode: 229 }) })).toEqual({ intercept: true, mode: 'ime' })
  })

  it('输入法拼写开始（compositionstart，Safari 顺序）→ 拦，模式是 ime', () => {
    expect(decideIntercept(base, { kind: 'composition' })).toEqual({ intercept: true, mode: 'ime' })
  })
})

describe('decideIntercept — REQ-TI-02：每一条不拦截的理由，单独成立', () => {
  const cases: Array<[string, Partial<InterceptFacts>, string]> = [
    ['总开关关了（REQ-TI-08）', { enabled: false }, 'disabled'],
    ['不是当前可见的 surface', { surfaceActive: false }, 'inactive'],
    ['移动端', { desktop: false }, 'mobile'],
    ['输入框已经开着', { composerOpen: true }, 'composer-open'],
    ['WS 断了', { connected: false }, 'disconnected'],
    ['非 agent 面板（shell）', { agent: null }, 'not-agent'],
    ['agent 事实里 tool 为空', { agent: { tool: '', status: 'none', waitReason: '' } }, 'not-agent'],
    ['权限确认', { agent: { tool: 'claude', status: 'waiting', waitReason: 'permission' } }, 'agent-blocked'],
    ['选项菜单', { agent: { tool: 'claude', status: 'waiting', waitReason: 'question' } }, 'agent-blocked'],
    ['全屏程序（备用屏）', { altScreen: true }, 'alt-screen'],
    ['回看（复制模式）', { copyMode: true }, 'copy-mode'],
    ['搜索条打开', { searchOpen: true }, 'search-open'],
    ['总览浮层', { overlayOpen: true }, 'overlay-open'],
    ['视口在往回翻', { scrolledBack: true }, 'scrolled-back'],
    ['提示里已经有字了', { promptEmpty: false }, 'prompt-not-empty'],
  ]
  for (const [name, patch, reason] of cases) {
    it(`${name} → 放行（${reason}）`, () => {
      expect(decideIntercept({ ...base, ...patch }, text('h'))).toEqual({ intercept: false, reason: reason as never })
    })
  }

  it('提示里已经有字了：输入法开始拼写（compositionstart 和 keydown 229 两条路）也放行，不能把一条提示劈在两个编辑器里', () => {
    const f = { ...base, promptEmpty: false }
    expect(decideIntercept(f, { kind: 'composition' })).toEqual({ intercept: false, reason: 'prompt-not-empty' })
    expect(decideIntercept(f, { kind: 'key', key: key('n', { keyCode: 229 }) }))
      .toEqual({ intercept: false, reason: 'prompt-not-empty' })
    expect(decideIntercept(f, { kind: 'key', key: key('Process') }))
      .toEqual({ intercept: false, reason: 'prompt-not-empty' })
  })

  it('同一条规矩跟着账走：`/`、`!`、粘贴、历史回溯到达终端之后，拼写不拦；回车提交后又拦', () => {
    for (const reached of ['/', '!', '\x1b[200~pasted text\x1b[201~', '\x1b[A']) {
      const t = createPromptTracker()
      const facts = (): InterceptFacts => ({ ...base, promptEmpty: t.isEmpty() })
      // 账是空的：两条路都拦
      expect(decideIntercept(facts(), { kind: 'composition' }).intercept).toBe(true)
      expect(decideIntercept(facts(), { kind: 'key', key: key('n', { keyCode: 229 }) }).intercept).toBe(true)
      t.noteSent(reached)
      expect(decideIntercept(facts(), { kind: 'composition' })).toEqual({ intercept: false, reason: 'prompt-not-empty' })
      expect(decideIntercept(facts(), { kind: 'key', key: key('n', { keyCode: 229 }) }))
        .toEqual({ intercept: false, reason: 'prompt-not-empty' })
      t.noteSent('\r')
      expect(decideIntercept(facts(), { kind: 'composition' }).intercept).toBe(true)
    }
  })

  it('环境类理由对输入法同样成立：shell 里开着拼音，照旧直达', () => {
    expect(decideIntercept({ ...base, agent: null }, { kind: 'composition' })).toEqual({ intercept: false, reason: 'not-agent' })
    expect(decideIntercept({ ...base, altScreen: true }, { kind: 'key', key: key('n', { keyCode: 229 }) }))
      .toEqual({ intercept: false, reason: 'alt-screen' })
  })

  it('带 Ctrl / ⌘ / Alt 的组合键：放行', () => {
    for (const mod of ['ctrlKey', 'metaKey', 'altKey'] as const) {
      expect(decideIntercept(base, { kind: 'key', key: key('v', { [mod]: true }) }))
        .toEqual({ intercept: false, reason: 'modified-key' })
    }
  })

  for (const k of ['ArrowUp', 'ArrowLeft', 'Tab', 'Escape', 'Enter', 'Backspace', 'F2', 'Home', 'PageUp']) {
    it(`非可见字符键 ${k}：放行`, () => {
      expect(decideIntercept(base, text(k))).toEqual({ intercept: false, reason: 'non-text-key' })
    })
  }

  it('空格不是"可见字符"：放行', () => {
    expect(decideIntercept(base, text(' '))).toEqual({ intercept: false, reason: 'whitespace-key' })
  })

  it('拼写进行中的按键：不归 keydown 判定', () => {
    expect(decideIntercept(base, { kind: 'key', key: key('a', { isComposing: true }) }))
      .toEqual({ intercept: false, reason: 'ime-ongoing' })
  })

  for (const c of ['/', '!', '#']) {
    it(`提示为空时首字符是 ${c}：放行（依赖 agent 自己的补全菜单）`, () => {
      expect(decideIntercept(base, text(c))).toEqual({ intercept: false, reason: 'command-prefix' })
    })
  }

  it('同样的前缀字符在输入法激活时（keyCode 229）也放行', () => {
    expect(decideIntercept(base, { kind: 'key', key: key('/', { keyCode: 229 }) }))
      .toEqual({ intercept: false, reason: 'command-prefix' })
  })

  it('这些前缀只在**首字符**才特殊：句中的 / 不是问题，因为根本到不了这里（提示已非空）', () => {
    expect(decideIntercept({ ...base, promptEmpty: false }, text('/'))).toEqual({ intercept: false, reason: 'prompt-not-empty' })
  })

  it('其它标点不是命令前缀：照拦', () => {
    for (const c of ['.', '?', '@', '"', '-', '$']) {
      expect(decideIntercept(base, text(c)).intercept).toBe(true)
    }
  })
})

describe('PromptTracker — 「提示为空时」靠记账认出来', () => {
  const fresh = () => createPromptTracker()

  it('新建时为空', () => {
    expect(fresh().isEmpty()).toBe(true)
  })

  it('敲过字就不空；回车提交后又空', () => {
    const t = fresh()
    t.noteSent('a')
    expect(t.isEmpty()).toBe(false)
    t.noteSent('\r')
    expect(t.isEmpty()).toBe(true)
  })

  it('这就是 /help 不被劈开的原因：`/` 放行之后，提示已经不空，`h` 不会被当成首字符', () => {
    const t = fresh()
    const facts = (): InterceptFacts => ({ ...base, promptEmpty: t.isEmpty() })
    expect(decideIntercept(facts(), text('/')).intercept).toBe(false) // 首字符是命令前缀 → 放行
    t.noteSent('/')                                                    // 它直达了终端，过账
    expect(decideIntercept(facts(), text('h'))).toEqual({ intercept: false, reason: 'prompt-not-empty' })
    t.noteSent('help')
    t.noteSent('\r')                                                   // 提交
    expect(decideIntercept(facts(), text('h')).intercept).toBe(true)  // 下一条提示又从"首字符"开始
  })

  it('退格一格一格删回去，删空了又是空的（只会多算不会少算，不会提前宣布空）', () => {
    const t = fresh()
    t.noteSent('ab')
    t.noteSent('\x7f')
    expect(t.isEmpty()).toBe(false)
    t.noteSent('\x7f')
    expect(t.isEmpty()).toBe(true)
  })

  it('多删不会出负数', () => {
    const t = fresh()
    t.noteSent('\x7f\x7f\x7f')
    t.noteSent('a')
    expect(t.isEmpty()).toBe(false)
  })

  it('一个表情（两个 UTF-16 单元）只记一格', () => {
    const t = fresh()
    t.noteSent('😀')
    t.noteSent('\x7f')
    expect(t.isEmpty()).toBe(true)
  })

  it('输入法定稿的整段中文一次到达，按码点记账', () => {
    const t = fresh()
    t.noteSent('你好')
    t.noteSent('\x7f')
    expect(t.isEmpty()).toBe(false)
    t.noteSent('\x7f')
    expect(t.isEmpty()).toBe(true)
  })

  it('括号粘贴：内容算字，里面的回车是文本不是提交', () => {
    const t = fresh()
    t.noteSent('\x1b[200~line1\rline2\x1b[201~')
    expect(t.isEmpty()).toBe(false)
  })

  it('括号粘贴之后的回车才提交', () => {
    const t = fresh()
    t.noteSent('\x1b[200~hello\x1b[201~')
    t.noteSent('\r')
    expect(t.isEmpty()).toBe(true)
  })

  it('Ctrl-C 和 Ctrl-U 清空提示', () => {
    for (const k of ['\x03', '\x15']) {
      const t = fresh()
      t.noteSent('abc')
      t.noteSent(k)
      expect(t.isEmpty()).toBe(true)
    }
  })

  it('历史回溯（方向键上/下）、Tab 补全、Ctrl-R：会往提示里塞字，记成"不知道" —— 退格也救不回来', () => {
    for (const k of ['\x1b[A', '\x1b[B', '\x1bOA', '\x1bOB', '\t', '\x12', '\x19']) {
      const t = fresh()
      t.noteSent(k)
      expect(t.isEmpty()).toBe(false)
      t.noteSent('\x7f\x7f\x7f\x7f')
      expect(t.isEmpty()).toBe(false) // 不知道提示里有多少，不敢宣布空
      t.noteSent('\r')
      expect(t.isEmpty()).toBe(true)  // 提交后重新开始
    }
  })

  it('光标左右移动、Home/End、单独一个 Esc 不改变提示', () => {
    const t = fresh()
    for (const k of ['\x1b[C', '\x1b[D', '\x1b[H', '\x1b[F', '\x1bOC', '\x1bOD', '\x1b[1;5C', '\x1b', '\x01', '\x05', '\x02', '\x06', '\x0c']) {
      t.noteSent(k)
    }
    expect(t.isEmpty()).toBe(true)
  })

  it('终端自己发出的上报不是用户在打字：焦点、鼠标（SGR 和 X10）、光标位置回应、OSC 回应', () => {
    const t = fresh()
    for (const k of [
      '\x1b[I', '\x1b[O',                 // 焦点进出
      '\x1b[<0;10;5M', '\x1b[<0;10;5m',   // SGR 鼠标
      '\x1b[M !\"',                       // X10 鼠标：ESC [ M 后面还有 3 个字节，它们不是文本
      '\x1b[12;40R',                      // DSR 回应
      '\x1b[?62;4c',                      // DA 回应
      '\x1b]11;rgb:1e1e/1e1e/1e1e\x07',   // OSC 回应（BEL 结尾）
      '\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\', // OSC 回应（ST 结尾）
    ]) {
      t.noteSent(k)
    }
    expect(t.isEmpty()).toBe(true)
  })

  it('Alt-Enter（Claude Code 的换行键）往提示里加了一个换行', () => {
    const t = fresh()
    t.noteSent('\x1b\r')
    expect(t.isEmpty()).toBe(false)
  })

  it('Ctrl-J 换行同理；Alt+字符不改变提示', () => {
    const a = fresh(); a.noteSent('\n'); expect(a.isEmpty()).toBe(false)
    const b = fresh(); b.noteSent('\x1bb'); expect(b.isEmpty()).toBe(true)
  })

  it('一个 chunk 里混着多种内容也能解析：字 + 退格 + 提交 + 又一个字', () => {
    const t = fresh()
    t.noteSent('ab\x7f\x7f\rc')
    expect(t.isEmpty()).toBe(false)
    t.noteSent('\x7f')
    expect(t.isEmpty()).toBe(true)
  })

  it('reset 强制回到空（输入框提交 / 换了 agent）', () => {
    const t = fresh()
    t.noteSent('\x1b[Aabc')
    t.reset()
    expect(t.isEmpty()).toBe(true)
  })

  it('转义序列被截断在 chunk 末尾时不死循环、不抛', () => {
    const t = fresh()
    expect(() => { t.noteSent('\x1b['); t.noteSent('\x1b[1;'); t.noteSent('\x1b]123'); t.noteSent('\x1bO') }).not.toThrow()
  })
})
