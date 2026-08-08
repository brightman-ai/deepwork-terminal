import { describe, it, expect } from 'bun:test'
import { cardToUnit, type CardIdentity } from '@terminal/composables/cli/useAgentOverview'
import type { SurfaceCard, SurfaceUnit } from '@terminal/types/terminal'
// 唯一那份向量文件，Go 侧同一个文件（surface_rollup_test.go 读它的另一半）。
// 相对路径直指仓库根的 testdata/，**故意不复制一份到 frontend/ 下**：两份向量就是两份契约。
import vectors from '../../../../../testdata/surface_rollup_vectors.json'

/**
 * 跨语言契约的 TS 那一半。
 *
 *   Go：units + active            --RollUp-->      card
 *   这里：card + identity          --cardToUnit-->   overview unit
 *
 * 为什么是"同一份向量"而不是"同一份代码"：这道缝没有任何编译器管得着——服务端算 roll-up、前端只读
 * 卡片，这个分工正是本轮的改进，同时也是一种新的出错方式。能共享的只有例子，于是共享例子：任何
 * 一侧改了行为，另一侧的断言就红。
 */

interface Vector {
  name: string
  active: number
  /** null = 一个 RollUp 产不出的卡片（没升级的服务端发的）。Go 那侧跳过，这侧必须照样处理。 */
  units: SurfaceUnit[] | null
  card: SurfaceCard
  identity: { key: string; index: number; title: string; active: boolean; cwd?: string; exited?: boolean }
  unit: {
    rawStatus: 'waiting' | 'running' | 'idle'
    tool: string
    awaiting: boolean
    awaitingSince: string
    /** ISO 或 null；期望的 epoch ms 由这里换算，免得把一个魔数写死在契约里。 */
    activityAt: string | null
    signals: string[]
  }
}

const cases = (vectors as { cases: Vector[] }).cases

describe('cross-language surface contract (testdata/surface_rollup_vectors.json)', () => {
  it('has vectors at all — a contract that pins nothing is worse than none', () => {
    expect(cases.length).toBeGreaterThan(0)
  })

  for (const v of cases) {
    it(v.name, () => {
      const id: CardIdentity = {
        key: v.identity.key,
        index: v.identity.index,
        title: v.identity.title,
        active: v.identity.active,
        cwd: v.identity.cwd,
        exited: v.identity.exited,
        // units 省略（null）= 这张卡就是它自己那个单元，非 tmux 卡片走的正是这一支。
        units: v.units ?? undefined,
      }
      const got = cardToUnit(v.card, id)

      expect(got.key).toBe(v.identity.key)
      expect(got.index).toBe(v.identity.index)
      expect(got.title).toBe(v.identity.title)
      expect(got.active).toBe(v.identity.active)
      expect(got.cwd).toBe(v.identity.cwd ?? '')

      expect(got.rawStatus).toBe(v.unit.rawStatus)
      expect(got.tool).toBe(v.unit.tool)
      expect(got.awaiting).toBe(v.unit.awaiting)
      expect(got.awaitingSince).toBe(v.unit.awaitingSince)
      expect(got.activityAt).toBe(v.unit.activityAt ? Date.parse(v.unit.activityAt) : 0)
      expect(got.signals).toEqual(v.unit.signals)
    })
  }
})
