import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'

/**
 * 这一行为什么值得测：它一度让使用者得出了一个**恰好相反**的结论。
 *
 * 面板显示「本周期已耗 1.2k credits ／ 上一周期 134.4k」，而同一时刻仪表说额度已用 74%。
 * 使用者的读法是"我才花了 0.86%"——完全合理，因为两个数同单位、同一行、右对齐，**版式本身就在说
 * 一个比值**。而实际上：那 1.2k 是厂商**还没结算**的当天（1,153 credits / 0 turns），
 * 那 134.4k 是**按窗口长度往回数**的一段区间，在提前重置之后甚至不是上一个计费周期。
 *
 * 三件事都不该由 tooltip 兜底，所以这里用源码结构断言把它们钉住——这个项目没有组件级 DOM 测试
 * 设施，而"没有断言"和"断言写不出来"在回归发生时是同一个结果。
 */

const chip = readFileSync(new URL('../UsageChip.vue', import.meta.url), 'utf8')
const contract = readFileSync(new URL('../useUsageQuota.ts', import.meta.url), 'utf8')

describe('结算态：一天都没结清就不给数字', () => {
  it('模板在 none 时渲染「账本尚未结算」而不是一个数', () => {
    expect(chip).toContain("creditsSettlement(q.credits) === 'none'")
    expect(chip).toContain('账本尚未结算')
  })

  it('判定只看 days 与 unsettled_days，不引入时间阈值', () => {
    // 阈值是要调的，而调参的东西在别人的机器上一定是错的。这里只用厂商自己给的两个计数。
    const fn = chip.slice(chip.indexOf('function creditsSettlement'))
    const body = fn.slice(0, fn.indexOf('\n}'))
    expect(body).toContain('c.days')
    expect(body).toContain('c.unsettled_days')
    expect(body).not.toMatch(/Date\.now|setTimeout|minutes|hours/)
  })

  it('部分结清时给数字并标注还会涨', () => {
    expect(chip).toContain("creditsSettlement(q.credits) === 'partial'")
  })

  it('未结清的那一格不用数字排版（别让它读起来像个值）', () => {
    const css = chip.slice(chip.indexOf('.uchip-credits-pending'))
    const block = css.slice(0, css.indexOf('}'))
    expect(block).not.toContain('tabular-nums')
    expect(block).not.toContain('font-weight: 600')
  })
})

describe('周期归属：没观测到边界就不叫「上一周期」', () => {
  it('标签由 prior_is_cycle 决定', () => {
    const fn = chip.slice(chip.indexOf('function priorLabel'))
    const body = fn.slice(0, fn.indexOf('\n}'))
    expect(body).toContain('c.prior_is_cycle')
    expect(body).toContain('上一周期')
    expect(body).toContain('此前')
  })

  it('区间长度是两个起点相减算出来的，不是写死的 7', () => {
    // 写死「一周」会在 5h 窗口、或厂商改了周期长度时立刻说谎。
    const fn = chip.slice(chip.indexOf('function priorLabel'))
    const body = fn.slice(0, fn.indexOf('\n}'))
    expect(body).toContain('prior_window_start')
    expect(body).toContain('window_start')
    expect(body).not.toMatch(/=\s*7\b/)
  })

  it('不是完整周期时，限定语印在面板上而不是只藏在 tooltip 里', () => {
    expect(chip).toContain('未必是完整周期')
    expect(chip).toContain('!q.credits.prior_is_cycle')
  })
})

describe('版式：两个数不再同行同单位对齐', () => {
  it('上一周期自成一行、缩进，且不再用 margin-left:auto 顶到右边', () => {
    // margin-left:auto 把它推到本周期那一行的最右侧 —— 那正是「X / Y」读法的来源。
    const css = chip.slice(chip.indexOf('.uchip-credits-prior {'))
    const block = css.slice(0, css.indexOf('}'))
    expect(block).toContain('padding-left')
    expect(block).not.toContain('margin-left: auto')
  })

  it('本周期与上一周期是两个兄弟 div，不是同一行里的两个 span', () => {
    const cur = chip.indexOf('class="uchip-credits"')
    const prior = chip.indexOf('class="uchip-credits-prior"')
    expect(cur).toBeGreaterThan(-1)
    expect(prior).toBeGreaterThan(cur)
    // 中间必须有一个 </div> 把它们分开。
    expect(chip.slice(cur, prior)).toContain('</div>')
  })
})

describe('契约字段都带上了说明（下一个读代码的人不必重新推一遍）', () => {
  for (const field of ['prior_is_cycle', 'unsettled_days', 'settled_through']) {
    it(`${field} 在前端契约里有定义`, () => {
      expect(contract).toContain(field)
    })
  }
  it('契约写明了「false/0 才是警告」这件事所依据的实测', () => {
    // 这几个数字是这轮排查的全部依据，丢了它们，下一个人只会看到一个没来由的字段。
    expect(contract).toContain('1,153.45')
    expect(contract).toContain('0 turns')
  })
})
