import { describe, it, expect } from 'bun:test'
import { composerKeyAction, passthroughBytes, type ComposerKeyFacts } from '../composeKeys'

const k = (key: string, extra: Partial<ComposerKeyFacts> = {}): ComposerKeyFacts => ({
  key, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...extra,
})
const ctx = (text: string, extra: { composing?: boolean; applicationCursorKeys?: boolean } = {}) => ({
  text, composing: false, applicationCursorKeys: false, ...extra,
})

describe('REQ-TI-04 回车 = 发送并提交', () => {
  it('Enter：发送', () => {
    expect(composerKeyAction(k('Enter'), ctx('hello'))).toEqual({ kind: 'send' })
  })

  it('多行文字里的 Enter 也是发送', () => {
    expect(composerKeyAction(k('Enter'), ctx('line1\nline2'))).toEqual({ kind: 'send' })
  })

  it('Shift+Enter：换行（交给 textarea 默认行为，我们不拦）', () => {
    expect(composerKeyAction(k('Enter', { shiftKey: true }), ctx('hello'))).toEqual({ kind: 'ignore' })
  })

  it('Ctrl/⌘+Enter 保留旧肌肉记忆：同样发送', () => {
    expect(composerKeyAction(k('Enter', { ctrlKey: true }), ctx('hello'))).toEqual({ kind: 'send' })
    expect(composerKeyAction(k('Enter', { metaKey: true }), ctx('hello'))).toEqual({ kind: 'send' })
  })

  it('输入法拼写中的 Enter 只确认候选，不发送（composition 事件说在拼）', () => {
    expect(composerKeyAction(k('Enter'), ctx('ni', { composing: true }))).toEqual({ kind: 'ignore' })
  })

  it('输入法拼写中的 Enter（keydown 自带 isComposing）', () => {
    expect(composerKeyAction(k('Enter', { isComposing: true }), ctx('ni'))).toEqual({ kind: 'ignore' })
  })

  it('Safari：确认候选的 Enter 到达时 isComposing 已是 false，但 keyCode 仍是 229 —— 同样不发送', () => {
    expect(composerKeyAction(k('Enter', { isComposing: false, keyCode: 229 }), ctx('你好'))).toEqual({ kind: 'ignore' })
  })

  it('拼写中的 Esc 只取消拼写，不关框', () => {
    expect(composerKeyAction(k('Escape', { isComposing: true }), ctx('ni'))).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('Escape'), ctx('', { composing: true }))).toEqual({ kind: 'ignore' })
  })
})

describe('REQ-TI-05 空框直通', () => {
  const empty = ctx('')

  it('Enter → \\r', () => {
    expect(composerKeyAction(k('Enter'), empty)).toEqual({ kind: 'passthrough', data: '\r' })
  })

  it('Esc → ESC（打断 agent）', () => {
    expect(composerKeyAction(k('Escape'), empty)).toEqual({ kind: 'passthrough', data: '\x1b' })
  })

  it('Ctrl-C → ETX', () => {
    expect(composerKeyAction(k('c', { ctrlKey: true }), empty)).toEqual({ kind: 'passthrough', data: '\x03' })
    expect(composerKeyAction(k('C', { ctrlKey: true }), empty)).toEqual({ kind: 'passthrough', data: '\x03' })
  })

  it('Tab → \\t，Shift+Tab → CSI Z', () => {
    expect(composerKeyAction(k('Tab'), empty)).toEqual({ kind: 'passthrough', data: '\t' })
    expect(composerKeyAction(k('Tab', { shiftKey: true }), empty)).toEqual({ kind: 'passthrough', data: '\x1b[Z' })
  })

  for (const [name, f] of [['ArrowUp', 'A'], ['ArrowDown', 'B'], ['ArrowRight', 'C'], ['ArrowLeft', 'D']]) {
    it(`${name} → CSI ${f}`, () => {
      expect(composerKeyAction(k(name), empty)).toEqual({ kind: 'passthrough', data: `\x1b[${f}` })
    })
  }

  it('终端在「应用光标键」模式时方向键发 SS3，不发 CSI（否则 vim 式程序会把方向键当成 Esc + 字符）', () => {
    expect(composerKeyAction(k('ArrowUp'), ctx('', { applicationCursorKeys: true }))).toEqual({ kind: 'passthrough', data: '\x1bOA' })
  })

  it('空框里的普通字符、粘贴快捷键、带修饰的方向键：不直通（交给框自己）', () => {
    expect(composerKeyAction(k('a'), empty)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('v', { ctrlKey: true }), empty)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('v', { metaKey: true }), empty)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('ArrowLeft', { altKey: true }), empty)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('ArrowUp', { shiftKey: true }), empty)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('Backspace'), empty)).toEqual({ kind: 'ignore' })
  })

  it('空框 + 拼写中：一切归输入法，不直通', () => {
    expect(composerKeyAction(k('Escape'), ctx('', { composing: true }))).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('ArrowDown', { isComposing: true }), empty)).toEqual({ kind: 'ignore' })
  })

  it('"空"就是长度为 0：一个空格也算有内容，不直通', () => {
    expect(composerKeyAction(k('Escape'), ctx(' '))).toEqual({ kind: 'close-keep' })
    expect(composerKeyAction(k('Enter'), ctx(' '))).toEqual({ kind: 'send' })
  })
})

describe('REQ-TI-05 非空时这些键编辑输入框', () => {
  const full = ctx('draft')

  it('Esc：收起并保留草稿（调用方再看是不是只有触发输入而决定撤回补发）', () => {
    expect(composerKeyAction(k('Escape'), full)).toEqual({ kind: 'close-keep' })
  })

  it('Ctrl-C、方向键：不直通（复制 / 移动光标是框自己的事）', () => {
    expect(composerKeyAction(k('c', { ctrlKey: true }), full)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('ArrowUp'), full)).toEqual({ kind: 'ignore' })
    expect(composerKeyAction(k('ArrowLeft'), full)).toEqual({ kind: 'ignore' })
  })

  it('Tab 在框里插入制表符；Shift+Tab 只吃掉，焦点都不能跑出输入框', () => {
    expect(composerKeyAction(k('Tab'), full)).toEqual({ kind: 'insert-tab' })
    expect(composerKeyAction(k('Tab', { shiftKey: true }), full)).toEqual({ kind: 'swallow' })
  })

  it('Ctrl+Tab 是浏览器的切标签：不碰', () => {
    expect(composerKeyAction(k('Tab', { ctrlKey: true }), full)).toEqual({ kind: 'ignore' })
  })

  it('普通字符：不碰', () => {
    expect(composerKeyAction(k('a'), full)).toEqual({ kind: 'ignore' })
  })
})

describe('passthroughBytes', () => {
  it('Shift+Enter / Alt+Enter 不是回车直通', () => {
    expect(passthroughBytes(k('Enter', { shiftKey: true }), false)).toBeNull()
    expect(passthroughBytes(k('Enter', { altKey: true }), false)).toBeNull()
  })
  it('Ctrl+Shift+C 不是 Ctrl-C', () => {
    expect(passthroughBytes(k('C', { ctrlKey: true, shiftKey: true }), false)).toBeNull()
  })
})
