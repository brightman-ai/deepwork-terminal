/**
 * dwQuickBar — 非 tmux 底栏的按钮表。
 *
 * 和 `surfaceActionBar.ts` 是同一类文件，理由也同一个：**哪些按钮存在、每个在什么条件下出现**
 * 是决策，所以它住在一个有测试的纯函数里，而不是散在模板的 v-if 里。
 *
 * ── 这条 bar 为什么存在 ────────────────────────────────────────────────────────────────────
 * tmux 快捷条过去的出现条件是「这台机器**装了** tmux」，而不是「这个 shell 正在 tmux 里」。
 * 于是一个从不敲 `tmux` 的人，屏幕最下面那一行常驻着 vspl / hspl / zoom / sess / detach ——
 * 对他每一个都是死键，占掉手机最输不起的那一行。这条 bar 就是「shell 不在 tmux 里」时那一行
 * 该说的话。
 *
 * ── 它刻意不做的事（每一条都是查实了已有入口才砍的）────────────────────────────────────────
 * · 不重印主 Toolbar 已经有的键（PgU/PgD/^C/↑/↓/Enter/Spc/⌫ 见 Toolbar.vue）。tmux 那条重印了，
 *   结果是同一个键在一屏上出现两次；照抄它就是把这个毛病抄第二遍。
 * · **没有 cp / 选择按钮**：手机上发起选区的入口是常驻触摸球（VirtualTouchball «ALWAYS visible
 *   on mobile»，tap / 双击 / 三击 / 长按 / 拖都走它），而且它能精确定位、比一个按钮更好用。
 *   「复制选中」也已经在状态行按语义出现（surfaceActionBar 的 copy，有选区才在）。
 * · **没有查找按钮**：surfaceActionBar 的 search 是常驻动作，`.is-mobile .ssr-action` 有专门
 *   尺寸 —— 那一行在手机上就是显示的。再放一个就是同一件事的第二个入口。
 * · **没有标签编号列**（曾经有过，2026-08-11 撤掉）：切终端已经有入口 —— 左端那个胶囊弹的总览
 *   浮层，卡片带实时输出和状态点，点一下就切过去。加编号列时的理由是「顶上标签栏拇指够不着」，
 *   理由成立但结论不成立：胶囊同样够得着，而编号列要为一个**低频导航**长期吃掉手机上最输不起
 *   那一行的一大半宽度。这条规则就是上面两条的第三次应用，不是例外。
 *   新建终端同理退回顶栏那个 +（CliTabBar）：够不着是真的，但一个入口够不着好过一整行被占。
 * tmux 那条 bar 之所以有 cp，是因为 tmux 的 copy-mode 是**进模式**，和这里的「已经能选」不是
 * 一回事；等价映射不是照着按钮名抄，是照着「这件事有没有入口」抄。
 *
 * ── 那个 attach 按钮为什么必须在 ───────────────────────────────────────────────────────────
 * 门从 installed 改成 attached 时，`attach` 恰好是**只在 !attached 时才显示**的那一个
 * (TmuxQuickBar.vue 的 attach 分支)。门一改它就跟着整条 bar 一起消失了 —— 装了 tmux 但此刻没
 * attach 的人，从此再也点不到「进 tmux」，只能自己手敲 `tmux attach`。所以它在这里补回来：
 * 位置放在尾部、不加强调色 —— 对这条 bar 的受众（非 tmux 用户）它是逃生口，不是主路。
 */

/** 这条 bar 上的动作。overview 胶囊不在此表：它是结构（入口），不是动作。 */
export type DwQuickAction = 'half-up' | 'half-down' | 'attach'

export interface DwQuickActionSpec {
  id: DwQuickAction
  /** 按钮上印的微标题（原样显示，别在模板里再拼一次）。 */
  label: string
  /** tooltip 与 aria-label 共用一句，两处不各写一份。 */
  title: string
  /** 视觉分组：scroll 类给更大的点按区（单手最常用），attach 是低强调的逃生口。 */
  kind: 'scroll' | 'attach'
}

export interface DwQuickBarConditions {
  /**
   * 这台机器装了 tmux。
   *
   * 注意它**不是**这条 bar 的出现条件 —— 出现条件是 `!attached`（这个 shell 不在 tmux 里），
   * 由调用方把关。这里只用它决定要不要补那个 attach 逃生口：机器上根本没有 tmux 的人，给他一个
   * attach 按钮等于给他一个必然报 command not found 的按钮。
   */
  tmuxInstalled: boolean
}

/**
 * 这条 bar 上的动作，按出场顺序。
 *
 * 顺序是固定的、条件动作只在自己的位置上出现或消失 —— 和 `surfaceActionBar` 同一条纪律：
 * 位置从不因内容而移动，否则每次 attach 状态一变，手指要重新找一遍所有按钮。
 */
export function dwQuickActions(c: DwQuickBarConditions): DwQuickActionSpec[] {
  const out: DwQuickActionSpec[] = [
    { id: 'half-up', label: '½↑', title: '向上半屏', kind: 'scroll' },
    { id: 'half-down', label: '½↓', title: '向下半屏', kind: 'scroll' },
  ]
  if (c.tmuxInstalled) {
    out.push({ id: 'attach', label: 'attach', title: '进入 tmux (tmux attach)', kind: 'attach' })
  }
  return out
}
