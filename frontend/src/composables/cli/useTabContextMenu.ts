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
 *  working directory, and the copy entry disables itself rather than copying a stale guess. */
export interface TabMenuTarget {
  id: string
  name: string
  cwd?: string
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
    ]
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
