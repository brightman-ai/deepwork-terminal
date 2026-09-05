import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'
import { pageKeyOf, pageKeyTarget } from '../pageKeyRouting'

/**
 * 修的是一个**死键**：非 tmux 的普通缓冲区里，物理键盘的 PgUp/PgDn 直接发给 PTY，而那边根本没有
 * 接收者（shell 不处理 `ESC [ 5 ~`）—— 按下去什么都不发生。正确行为（转成滚 xterm 自己的
 * scrollback）此前只写在**屏幕按钮**那条路上。
 *
 * 一条规则写两遍、只写对一遍。所以这里既测规则本身，也测**两条路真的走同一个函数** —— 后者是防
 * 复发的那一半，没有它，下次有人改其中一条，另一条又会悄悄落下。
 */

describe('pageKeyOf — 认出翻页键', () => {
  it('PgUp / PgDn 的完整序列', () => {
    expect(pageKeyOf(new Uint8Array([0x1b, 0x5b, 0x35, 0x7e]))).toBe('up')
    expect(pageKeyOf(new Uint8Array([0x1b, 0x5b, 0x36, 0x7e]))).toBe('down')
    expect(pageKeyOf('\x1b[5~')).toBe('up')
    expect(pageKeyOf('\x1b[6~')).toBe('down')
  })

  it('三字节前缀不算 —— 一个被拆开的序列不能当成完整键消费掉', () => {
    // PgUp 是四个字节。少一个就认，等于把 `ESC [ 5` 之后真正的终止符吞掉，把别的序列拆坏。
    expect(pageKeyOf(new Uint8Array([0x1b, 0x5b, 0x35]))).toBeNull()
  })

  it('别的序列一律放行', () => {
    expect(pageKeyOf('\x1b[A')).toBeNull()       // 上箭头
    expect(pageKeyOf('\x1b[3~')).toBeNull()      // Delete
    expect(pageKeyOf('\x1b[15~')).toBeNull()     // F5（五字节）
    expect(pageKeyOf('a')).toBeNull()
    expect(pageKeyOf(new Uint8Array())).toBeNull()
  })
})

describe('pageKeyTarget — 这个键归谁', () => {
  it('备用缓冲区 → PTY（less / vim / claude 自己会翻页）', () => {
    expect(pageKeyTarget({ altScreen: true, tmuxAttached: false })).toBe('pty')
    expect(pageKeyTarget({ altScreen: true, tmuxAttached: true })).toBe('pty')
  })

  it('attach 了 tmux → PTY（历史归 tmux，它自己有 copy-mode）', () => {
    expect(pageKeyTarget({ altScreen: false, tmuxAttached: true })).toBe('pty')
  })

  it('普通缓冲区 + 没有 tmux → 本地滚动。这是唯一 PTY 那边没有接收者的情形', () => {
    expect(pageKeyTarget({ altScreen: false, tmuxAttached: false })).toBe('local')
  })
})

/**
 * 防复发的那一半：物理键盘路径（sendTerminalData）和屏幕按钮路径（onSendKey）必须都引用这个模块。
 *
 * 这是对源码的结构断言，不是对行为的 —— 行为断言写不出来，因为那两条路都埋在一个 2500 行的 SFC
 * 里，而恰恰是"埋在两个地方"造成了这个 bug。用 grep 守住结构，是能低成本机器核查的那种防御。
 */
describe('两条输入路径共用同一条判断（结构断言）', () => {
  const src = readFileSync(
    new URL('../CliTerminalSurface.vue', import.meta.url),
    'utf8',
  )

  it('CliTerminalSurface 引用了 pageKeyRouting', () => {
    expect(src).toContain("from '@terminal/components/terminal-session/pageKeyRouting'")
  })

  it('物理键盘路径调 pageKeyOf', () => {
    // sendTerminalData 是所有输入的汇合点；翻页键必须在这里被截住，而不是流到 sendBinary。
    const body = src.slice(src.indexOf('function sendTerminalData'))
    expect(body.slice(0, 1200)).toContain('pageKeyOf(')
  })

  it('屏幕按钮路径调 pageKeyTarget，而不是自己再写一遍条件', () => {
    const body = src.slice(src.indexOf('function onSendKey'))
    expect(body.slice(0, 2000)).toContain('pageKeyTarget(')
    // 旧的内联条件不能再出现在这条路上——它就是当年那份"第二个实现"。
    expect(body.slice(0, 2000)).not.toContain("!tmuxAttached.value && term.buffer.active.type !== 'alternate'")
  })
})
