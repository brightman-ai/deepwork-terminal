import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'
import { dwQuickActions } from '../dwQuickBar'

/**
 * 这条 bar 的价值全在**它不放什么**上 —— 同一屏上已经有入口的东西一个都不重印。所以这里钉住的
 * 主要是「谁不在场」：一旦有人手滑把 cp / 查找 / PgUp 抄回来，测试就红。
 */
describe('dwQuickActions', () => {
  it('机器装了 tmux → 尾部留一个 attach 逃生口', () => {
    const ids = dwQuickActions({ tmuxInstalled: true }).map(a => a.id)
    expect(ids).toEqual(['half-up', 'half-down', 'attach'])
  })

  // 没装 tmux 还给一个 attach 按钮，就是给一个必然 command not found 的按钮。
  it('机器没装 tmux → 没有 attach', () => {
    const ids = dwQuickActions({ tmuxInstalled: false }).map(a => a.id)
    expect(ids).toEqual(['half-up', 'half-down'])
  })

  // 位置固定、条件动作只在自己的位置上出现或消失：否则 attach 状态一变，手指要重新找一遍按钮。
  it('顺序固定 —— attach 的有无不挪动前面两个的位置', () => {
    const withTmux = dwQuickActions({ tmuxInstalled: true }).map(a => a.id)
    const without = dwQuickActions({ tmuxInstalled: false }).map(a => a.id)
    expect(withTmux.slice(0, without.length)).toEqual(without)
  })

  it('不重印主 Toolbar 已有的键，也不重造已有入口的动作', () => {
    const ids = dwQuickActions({ tmuxInstalled: true }).map(a => a.id) as string[]
    // Toolbar.vue 已有：PgU / PgD / ^C / ↑ / ↓ / Enter / Spc / ⌫
    // 触摸球（常驻）已是选区入口；surfaceActionBar 的 search 已是常驻查找入口。
    for (const forbidden of ['pgup', 'pgdn', 'ctrl-c', 'enter', 'space', 'backspace', 'copy-mode', 'find']) {
      expect(ids).not.toContain(forbidden)
    }
  })

  it('每个动作都自带无障碍名与微标题，模板不再拼一次文案', () => {
    for (const a of dwQuickActions({ tmuxInstalled: true })) {
      expect(a.label.length).toBeGreaterThan(0)
      expect(a.title.length).toBeGreaterThan(0)
    }
  })

  // 半屏滚动是这条 bar 上最常按的东西 —— kind 决定它拿到更宽的点按区（和 tmux 条同一待遇）。
  it('半屏滚动归 scroll 类，attach 归 attach 类', () => {
    const byId = new Map(dwQuickActions({ tmuxInstalled: true }).map(a => [a.id, a.kind]))
    expect(byId.get('half-up')).toBe('scroll')
    expect(byId.get('half-down')).toBe('scroll')
    expect(byId.get('attach')).toBe('attach')
  })
})

/**
 * 「这一行是动作条，不是导航条」的机器门。
 *
 * 上面那组测的是**动作表**，管不到模板里塞了什么结构 —— 而编号列恰恰是结构。它 2026-08-11
 * 退场的理由（切终端已有胶囊 → 总览浮层这一个入口，不该为低频导航长期吃掉手机上最输不起
 * 那一行的一大半）不会随时间失效，所以这里对着源文件钉死，靠 grep 级别的事实，不靠人记得。
 * 真要改回去，先改这条测试 —— 那时它是一个决定，不是一次手滑。
 */
describe('DwQuickBar 模板契约', () => {
  const SRC = readFileSync(new URL('../DwQuickBar.vue', import.meta.url), 'utf8')

  it('底栏不再有标签编号列 / 新建 +（切终端走胶囊 → 总览浮层，新建走顶栏 +）', () => {
    for (const gone of ['dqb-tab', 'dqb-add', 'dqb-idx', 'dqb-dot', 'dw-tab-add', 'select-tab', 'new-tab']) {
      expect(SRC).not.toContain(gone)
    }
  })

  it('总览胶囊与状态卷起还在 —— 编号列走后它们是仅剩的入口和仅剩的一眼', () => {
    for (const kept of ['dw-overview-toggle', 'dw-rollup', 'toggle-overview']) {
      expect(SRC).toContain(kept)
    }
  })

  it('腾出来的宽度真的发给了动作区（flex 铺满，不是留白）', () => {
    expect(SRC).toContain('.dqb-actions')
    expect(SRC).toMatch(/\.dqb-actions\s*\{[^}]*flex:\s*1/)
    expect(SRC).toMatch(/\.dqb-btn\s*\{[^}]*flex:\s*1/)
  })
})
