import { describe, it, expect } from 'bun:test'
import { splitMidTruncate } from '../midTruncate'

/**
 * REQ-fp-midtrunc（2026-09-11）的纯规则面。这个规则坏掉的方式不报错：
 * v8/v9 全被尾部省略吃掉，两行文件名长得一模一样——用户就是在报这个。
 */

describe('splitMidTruncate（中段省略，保头保尾）', () => {
  it('版本号/扩展名永远在 tail 里（用户的原始场景）', () => {
    const name = 'S1快路径-TMG标准看板-20260806-输出字段表-v8.xlsx'
    const { head, tail } = splitMidTruncate(name, 12)
    expect(tail.endsWith('-v8.xlsx')).toBe(true)
    expect(name.startsWith(head)).toBe(true)
    expect(head + tail).toBe(name) // 切分不丢字
    // v9 同名同切法 → tail 不同 → 两行可区分
    const v9 = splitMidTruncate(name.replace('-v8', '-v9'), 12)
    expect(v9.tail).not.toBe(tail)
  })

  it('短名整体放进预算 → head 为空、无任何省略标记（A3-2：不显示全名的反面）', () => {
    const { head, tail } = splitMidTruncate('a.md', 20)
    expect(head).toBe('')
    expect(tail).toBe('a.md')
  })

  it('CJK 按宽度 2 计（预算 20 = 约 10 个汉字，不是 20 个）', () => {
    const name = '资产状态流转-v2-20260908.docx' // 6 个 CJK(12) + "-v2-20260908.docx"(17)
    const { tail } = splitMidTruncate(name, 20)
    // 预算 20：先装 ASCII 17，再装 3 个 CJK 中的 1 个半…… 恰好 17+2=19 装进 "-v2…" 前那个
    // 汉字后停止——tail 必然以 .docx 结尾且宽度 ≤ 预算
    expect(tail.endsWith('.docx')).toBe(true)
    let w = 0
    for (const ch of tail) w += ch.codePointAt(0)! > 0xff ? 2 : 1
    expect(w).toBeLessThanOrEqual(20)
  })

  it('emoji/ZWJ 序列不被劈成半个（字素切分）', () => {
    const name = '报告🇨🇳-v3.txt'
    const { tail } = splitMidTruncate(name, 8)
    expect(tail.endsWith('-v3.txt')).toBe(true)
    // 预算 6 时 tail 从 v 起（'v3.txt'）也合法——关键断言是旗语不被劈成半个：
    const tight = splitMidTruncate(name, 6)
    expect(tight.head + tight.tail).toBe(name)
    expect(tight.head).not.toContain(String.fromCodePoint(0xd83c))
    // 任何切法都不能出现孤立代理项
    const whole = splitMidTruncate(name, 20)
    expect(whole.head + whole.tail).toBe(name)
  })

  it('tail 预算再小，扩展名（含点）整体在（A3-3：退化形态保尾）', () => {
    const { tail } = splitMidTruncate('深-度-中-文-名-字-表.xlsx', 4)
    expect(tail).toBe('.xlsx') // 扩展名锚定优先于预算
  })

  it('扩展名锚定只吃最后一个点，版本 token 照常在 tail（-v8.xlsx 场景）', () => {
    const { tail } = splitMidTruncate('某项目-阶段评审材料-终稿-v8.xlsx', 10)
    expect(tail.endsWith('-v8.xlsx')).toBe(true)
  })

  it('默认预算即可用（不传参）', () => {
    const r = splitMidTruncate('whatever-long-name-v7.pdf')
    expect(r.head + r.tail).toBe('whatever-long-name-v7.pdf')
  })
})
