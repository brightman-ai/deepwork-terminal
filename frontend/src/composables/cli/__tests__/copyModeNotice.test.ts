import { describe, it, expect } from 'bun:test'
import { emptyHistoryText, altScreenNotice } from '../copyModeNotice'

describe('emptyHistoryText', () => {
  it('没有错误：说"还没有滚出去的历史"', () => {
    expect(emptyHistoryText('')).toBe('这个终端还没有滚出去的历史。')
  })

  // 回归：请求失败曾被显示成"这个终端还没有滚出去的历史"，使用者分不清是没有历史还是没取到。
  it('有错误：说出失败和原文，不能再伪装成"没有历史"', () => {
    const text = emptyHistoryText('HTTP 502')
    expect(text).toContain('读取历史失败')
    expect(text).toContain('HTTP 502')
    expect(text).not.toContain('还没有滚出去的历史')
  })
})

describe('altScreenNotice', () => {
  it('普通缓冲区：不打扰', () => {
    expect(altScreenNotice(false)).toBe('')
  })

  it('备用屏幕：说清历史里没有它的输出，并给出两条出路', () => {
    const text = altScreenNotice(true)
    expect(text).toContain('备用屏幕')
    expect(text).toContain('不会进入历史')
    // 出路一：程序自己的界面；出路二：改成 inline 后重启 —— 缺哪条使用者都只能自己猜。
    expect(text).toContain('Ctrl+T')
    expect(text).toContain('alternate_screen = "never"')
  })
})
