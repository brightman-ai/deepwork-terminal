import { describe, it, expect } from 'bun:test'
import {
  adoptCommitted,
  beginIme,
  beginTyped,
  consumePristine,
  noteCompositionEnd,
} from '../composeTypedTrigger'

describe('REQ-TI-03 误拦截一键撤回：框里只有触发它的那次输入且没动过', () => {
  it('Esc 撤回：返回那次输入让调用方补发给终端，框里恢复成打字之前的样子', () => {
    const { trigger, text } = beginTyped('', 'h')
    expect(text).toBe('h')
    expect(consumePristine(trigger, text)).toEqual({ typed: 'h', restoreText: '' })
  })

  it('原来有草稿：触发字追加在草稿后面；撤回时草稿原样还原，触发字不会污染草稿', () => {
    const { trigger, text } = beginTyped('之前的草稿', 'h')
    expect(text).toBe('之前的草稿h')
    expect(consumePristine(trigger, text)).toEqual({ typed: 'h', restoreText: '之前的草稿' })
  })

  it('已经编辑过（又打了字）：不是只有触发输入了 → 不撤回', () => {
    const { trigger } = beginTyped('', 'h')
    expect(consumePristine(trigger, 'hi')).toBeNull()
  })

  it('删掉了触发字：也是编辑过，不撤回（草稿保持用户改成的样子）', () => {
    const { trigger } = beginTyped('旧', 'h')
    expect(consumePristine(trigger, '旧')).toBeNull()
    expect(consumePristine(trigger, '')).toBeNull()
  })

  it('编辑过又改回来：全文和触发时一致，按 pristine 算（没有任何字会丢）', () => {
    const { trigger, text } = beginTyped('', 'h')
    expect(consumePristine(trigger, 'hi')).toBeNull()
    expect(consumePristine(trigger, text)).toEqual({ typed: 'h', restoreText: '' })
  })

  it('不是打字唤起的框（浮钮 / 快捷键）：没有可撤回的触发', () => {
    expect(consumePristine(null, 'draft')).toBeNull()
  })

  it('多码点字符（表情）原样带回', () => {
    const { trigger, text } = beginTyped('', '😀')
    expect(consumePristine(trigger, text)?.typed).toBe('😀')
  })

  it('空的触发内容不可撤回（没有什么可还给终端）', () => {
    const { trigger, text } = beginTyped('x', '')
    expect(consumePristine(trigger, text)).toBeNull()
  })
})

describe('REQ-TI-03 输入法唤起的框', () => {
  it('拼写定稿前触发内容未知：此时 Esc 没有东西可撤回', () => {
    const trigger = beginIme('')
    expect(consumePristine(trigger, '')).toBeNull()
    expect(consumePristine(trigger, 'ni')).toBeNull()
  })

  it('第一段拼写定稿（compositionend）后，触发就是那段定稿文字（草稿之后的部分）；Esc 撤回补发', () => {
    const trigger = noteCompositionEnd(beginIme('旧'), '旧你好')
    expect(trigger).toEqual({ typed: '你好', draftBefore: '旧', baseline: '旧你好' })
    expect(consumePristine(trigger, '旧你好')).toEqual({ typed: '你好', restoreText: '旧' })
  })

  it('定稿后又编辑：不撤回', () => {
    const trigger = noteCompositionEnd(beginIme(''), '你好')
    expect(consumePristine(trigger, '你好吗')).toBeNull()
  })

  it('只有第一段定稿算触发：第二段拼写是用户在编辑，不改账', () => {
    const first = noteCompositionEnd(beginIme(''), '你好')
    const second = noteCompositionEnd(first, '你好世界')
    expect(second).toEqual(first)
    expect(consumePristine(second, '你好世界')).toBeNull()
  })

  it('取消的拼写（定稿为空）：没有触发', () => {
    expect(noteCompositionEnd(beginIme('旧'), '旧')).toBeNull()
  })

  it('全文已经不以原草稿开头（用户在拼写期间改了草稿）：放弃，不猜', () => {
    expect(noteCompositionEnd(beginIme('旧'), '新你好')).toBeNull()
  })

  it('不是输入法唤起的触发：定稿不改账', () => {
    const { trigger } = beginTyped('', 'h')
    expect(noteCompositionEnd(trigger, 'h你好')).toBe(trigger)
    expect(noteCompositionEnd(null, '你好')).toBeNull()
  })
})

describe('Safari 交接：终端 textarea 里拼完的定稿文字交给框', () => {
  it('追加到全文末尾；输入法唤起后的第一段交接就是触发，可以撤回', () => {
    const adopted = adoptCommitted(beginIme('旧'), '旧', '你')
    expect(adopted.text).toBe('旧你')
    expect(consumePristine(adopted.trigger, adopted.text)).toEqual({ typed: '你', restoreText: '旧' })
  })

  it('已经有触发（可见字符唤起）时：文字追加，触发不变，于是不再 pristine', () => {
    const { trigger, text } = beginTyped('', 'h')
    const adopted = adoptCommitted(trigger, text, '你')
    expect(adopted.text).toBe('h你')
    expect(adopted.trigger).toBe(trigger)
    expect(consumePristine(adopted.trigger, adopted.text)).toBeNull()
  })

  it('交接空串：什么都不改', () => {
    const trigger = beginIme('旧')
    expect(adoptCommitted(trigger, '旧', '')).toEqual({ trigger, text: '旧' })
  })

  it('没有触发时也能交接（框已经被别的方式开着）：只追加文字', () => {
    expect(adoptCommitted(null, 'a', '你')).toEqual({ trigger: null, text: 'a你' })
  })
})
