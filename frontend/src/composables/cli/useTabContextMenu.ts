/**
 * useTabContextMenu — the tab context menu's MODEL (what the menu contains and when each entry is
 * available), shared by both shells.
 *
 * Presentation stays per-shell (CliTabContextMenu.vue renders it), but the item list, their
 * ordering, their wording and their enablement rules live here. That split is the same one
 * useTabShortcuts made: a menu whose entries drift between the two tab bars is the drift bug this
 * codebase keeps re-paying for, and an entry that is present-but-broken in one shell is worse than
 * one that is honestly absent.
 *
 * The menu is deliberately the SAME action vocabulary as the keyboard shortcuts (新建/关闭/重命名),
 * plus the two things a keyboard can't express well (关闭其他, 复制目录). Right-click and Alt+key
 * are two doors into one action table, not two feature sets.
 */
import { computed, ref } from 'vue'

/** The tab a menu was opened on. `cwd` is optional: a tab that has never connected has no live
 *  working directory, and the copy entry disables itself rather than copying a stale guess.
 *  `isTmux` decides whether 结束卡死进程 (force-kill the foreground process) applies — a tmux
 *  window's PTY foreground pgid belongs to tmux's own plumbing, not a fact this menu can act on
 *  (tmux has its own recovery tools). Defaults to non-tmux when the caller doesn't know yet. */
export interface TabMenuTarget {
  id: string
  name: string
  cwd?: string
  isTmux?: boolean
}

/** What the host shell can actually do. `closeOthers` is optional — a shell that cannot express it
 *  omits the callback and the entry disappears, instead of rendering a dead control. */
export interface TabMenuActions {
  rename(id: string): void
  close(id: string): void
  closeOthers?(id: string): void
  create(): void
  /** Copy arbitrary text; the host owns the clipboard implementation (and its fallbacks). */
  copy(text: string): void | Promise<unknown>
  /** How many tabs exist right now — decides whether 关闭其他 is meaningful. */
  tabCount(): number
  /** SIGKILL the tab's current PTY foreground process group — the recovery path for when Ctrl+C
   *  (SIGINT) doesn't reach a hung program. Required like rename/close, not optional like
   *  closeOthers: an item present in one shell's menu and silently missing in the other is the
   *  drift bug this file exists to prevent (see file header), and this command's whole point is
   *  being there exactly when the user is stuck and reaching for it. */
  forceKillForeground(id: string): void
  /** 把「新终端的环境变量」配置（设置页里那份 overlay）**敲进这个已经在跑的 shell**。
   *
   *  为什么需要这一项：那份配置是在 spawn 时套用的，所以它只影响新建的终端 —— 和 tmux 的
   *  `set-environment` 一样（实测过：改完之后同一个 pane 读到的还是旧值）。根因是操作系统层面
   *  就没法从外部改一个已在运行的进程的环境。
   *
   *  唯一诚实的办法就是"从里面改"：往 PTY 里写 `export` / `unset` 命令行，让 shell 自己执行。
   *  它**必须是可见的**（用户在终端里看得见那几行命令被敲进去、看得见结果），这是本仓库既定的
   *  "注入可见 PTY，绝不黑盒"范式；偷偷执行等于替用户在他自己的 shell 里跑了他没看见的命令。
   *
   *  和「新建终端」是两条路而不是一条：这条不杀任何进程（你正在跑的东西继续跑），代价是它只对
   *  这个 shell 之后启动的子进程生效 —— 已经在跑的那个 `claude` 不会因此换供应商。 */
  applyEnvHere(id: string): void
}

export interface TabMenuItem {
  key: string
  label: string
  /** Right-aligned hint, e.g. the equivalent keyboard shortcut. Empty = no hint. */
  hint?: string
  disabled?: boolean
  /** Renders in the destructive colour and sits below a separator. */
  danger?: boolean
  run(): void
}

/** Where the menu should be anchored, in viewport coordinates. */
export interface TabMenuPosition {
  x: number
  y: number
}

export function useTabContextMenu(actions: TabMenuActions) {
  const open = ref(false)
  const target = ref<TabMenuTarget | null>(null)
  const position = ref<TabMenuPosition>({ x: 0, y: 0 })

  /** Open at a pointer event's location. Suppresses the browser menu — a tab strip's own menu is
   *  strictly more useful there than "返回/重新加载". */
  function openAt(e: MouseEvent, t: TabMenuTarget): void {
    e.preventDefault()
    e.stopPropagation()
    target.value = t
    position.value = { x: e.clientX, y: e.clientY }
    open.value = true
  }

  function close(): void {
    open.value = false
    target.value = null
  }

  /** Runs an action and closes. Every entry goes through this, so "the menu closes after you pick
   *  something" is a property of the model rather than something each entry remembers to do. */
  function run(fn: () => void): void {
    const t = target.value
    if (!t) return
    close()
    fn()
  }

  const items = computed<TabMenuItem[]>(() => {
    const t = target.value
    if (!t) return []
    const list: TabMenuItem[] = [
      { key: 'rename', label: '重命名', hint: '双击标签', run: () => run(() => actions.rename(t.id)) },
      { key: 'copy-cwd', label: '复制目录路径', disabled: !t.cwd, run: () => run(() => { void actions.copy(t.cwd || '') }) },
      { key: 'new', label: '新建终端', run: () => run(() => actions.create()) },
      // 紧跟「新建终端」：两者是同一个问题的两个答案（"要新环境"→ 新建一个；"这个终端也要"→
      // 就地注入）。放在 danger 分组之前，因为它什么都不结束。
      {
        key: 'apply-env',
        label: '把环境变量应用到此终端',
        run: () => run(() => actions.applyEnvHere(t.id)),
      },
    ]
    // Not offered for tmux tabs: see TabMenuTarget.isTmux. Absent rather than disabled — a
    // disabled entry still implies "this could work here", which isn't true for tmux.
    if (!t.isTmux) {
      list.push({
        key: 'force-kill-fg',
        label: '结束卡死进程',
        danger: true,
        run: () => run(() => actions.forceKillForeground(t.id)),
      })
    }
    if (actions.closeOthers) {
      list.push({
        key: 'close-others',
        label: '关闭其他标签',
        // Disabled rather than hidden: a menu whose entries move between right-clicks forces the
        // user to re-read it every time. Position stability beats a shorter list.
        disabled: actions.tabCount() <= 1,
        danger: true,
        run: () => run(() => actions.closeOthers!(t.id)),
      })
    }
    list.push({ key: 'close', label: '关闭', danger: true, run: () => run(() => actions.close(t.id)) })
    return list
  })

  return { open, target, position, items, openAt, close }
}
