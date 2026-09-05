/**
 * useTabShortcuts — single-key-direct (NOT tmux prefix+key) shortcut dispatch for the CLI tab
 * strip, shared between standalone (deepwork-terminal) and pro (deepwork-pro via @terminal).
 *
 * ── Why every binding matches on `code`, never `key` ──────────────────────────────────────────
 * The first cut compared `e.key`, and the whole keymap silently did nothing on macOS: Option is a
 * COMPOSE modifier there, so Option+1 delivers `key:"¡"`, Option+N `"˜"`, Option+W `"∑"`,
 * Option+R `"®"` — none of which match "1"/"n"/"w"/"r". Non-Latin layouts break it the same way.
 * `code` is physical-key identity ("Digit1", "KeyN") and is immune to both.
 *
 * This also invalidated an earlier "verified in a browser" claim: CDP/synthetic events carry
 * `key:"1"` + `altKey:true`, so automation passes while a real keyboard fails. Synthetic events
 * can verify the DISPATCH LOGIC below; only a human on a real keyboard can verify the binding.
 *
 * Registers a CAPTURE-phase document keydown listener, gated by `adapter.isActive()` — that is
 * what lets pro scope it to "only while the CLI portal route is active", so it wins over pro's own
 * global Alt+1/2/3 binding (WindowDockOverlay, bubble phase) instead of racing it.
 */
import { computed, onBeforeUnmount, onMounted, ref, type ComputedRef } from 'vue'
import { computeVisibleTabOrder } from './useVisibleTabOrder'
import {
  useShortcutsConfig,
  bindingFor,
  bindingLabel,
  LEADER_CODES,
  type ShortcutsConfig,
  type ShortcutAction,
  type LeaderAction,
} from './useShortcutsConfig'

export interface TabShortcutsAdapter {
  /** Visible tab order, already flattened by the caller (groups respected on the standalone side). */
  orderedTabIds: () => string[]
  activeTabId: () => string | undefined
  /** Only dispatch while true — e.g. this CLI surface is the one currently mounted/focused. */
  isActive: () => boolean
  onSelect: (id: string) => void
  onNew: () => void
  onClose: (id: string) => void
  /**
   * leader 这条路此刻是否属于我们。
   *
   * 唯一的用途是**让位**：当前标签的 shell 一旦 attach 进 tmux，那个前缀（默认 Ctrl+B，正是 tmux
   * 自己的默认前缀）整个归 tmux —— 我们不注册、不 preventDefault，字节原样进 PTY。判据是
   * `attached` 而不是"有没有分屏"：分屏与否会来回变，快捷键的归属跟着变是最难学的那种不稳定；
   * 而且单 pane 时 tmux 的 prefix+c / prefix+d / prefix+数字 恰恰是最常用的一批。
   *
   * 注意让位的**只有 leader**。Alt 系一个都不让 —— tmux 用的是 C-b 前缀，跟 Alt 不冲突，让了是白让，
   * 而且那时 Alt+数字 正是你在那个标签里切 dw 标签的唯一手段。
   *
   * ⚠ **不提供 = leader 整个不启用**（而不是"默认归我们"）。
   * 这个默认方向是想清楚的：这个 composable 被两个壳共用（deepwork-terminal 与 deepwork-pro），
   * 而只有壳自己知道它的标签里有没有 tmux。若默认「归我们」，一个还没接这个回调的壳会在**每一个**
   * 标签上抢走 Ctrl+B —— 包括 attach 着 tmux 的那些，而那正是 tmux 自己的前缀。
   * 「功能暂时没有」是看得见的缺失，「悄悄抢走 tmux 用户的前缀」是看不见的破坏。选前者。
   */
  leaderEnabled?: () => boolean
  /** leader 专属动作 —— 它们本来就没有单修饰键绑定可给。不给 = 那个键不响应，也不吞。 */
  onOverview?: () => void
  onRename?: (id: string) => void
  /** 进入只读回看（复制模式）。不提供 = leader + `[` 原样放行给 shell。 */
  onCopyMode?: () => void
}

interface ParsedBinding {
  alt: boolean
  ctrl: boolean
  shift: boolean
  meta: boolean
  /** KeyboardEvent.code, e.g. "KeyN" / "ArrowUp". '' for a modifier-only binding (the digit family). */
  code: string
}

/** Parses "Alt+KeyW" / "Ctrl+ArrowUp" / "Alt" into modifier flags + a physical code. */
export function parseBinding(binding: string): ParsedBinding {
  const parsed: ParsedBinding = { alt: false, ctrl: false, shift: false, meta: false, code: '' }
  for (const part of binding.split('+').map((p) => p.trim())) {
    const lower = part.toLowerCase()
    if (lower === 'alt') parsed.alt = true
    else if (lower === 'ctrl' || lower === 'control') parsed.ctrl = true
    else if (lower === 'shift') parsed.shift = true
    else if (lower === 'meta' || lower === 'cmd') parsed.meta = true
    else parsed.code = part
  }
  return parsed
}

/** Every modifier flag matches exactly (so Alt+Ctrl+W never fires a plain Alt+W binding). */
function modifiersMatch(e: KeyboardEvent, p: ParsedBinding): boolean {
  return e.altKey === p.alt && e.ctrlKey === p.ctrl && e.shiftKey === p.shift && e.metaKey === p.meta
}

/** Whether a keydown matches a full binding ("Alt+KeyW"), comparing PHYSICAL code. */
export function matchesBinding(e: KeyboardEvent, binding: string): boolean {
  const p = parseBinding(binding)
  return modifiersMatch(e, p) && !!p.code && e.code === p.code
}

/**
 * Whether a keydown is <prefix> + a digit 1-9, returning the digit.
 *
 * Reads `e.code` ("Digit1".."Digit9"), so macOS Option+1 (which prints "¡") still resolves to 1.
 * Digit0 is deliberately excluded — there is no "tab 0".
 */
export function matchesPrefixDigit(e: KeyboardEvent, prefix: string): number | undefined {
  const p = parseBinding(prefix)
  if (!modifiersMatch(e, p)) return undefined
  const m = /^Digit([1-9])$/.exec(e.code)
  return m ? Number(m[1]) : undefined
}

/** Pure dispatch: given a config + a snapshot of tab state, what action does this keydown mean? */
export function resolveShortcutAction(
  e: KeyboardEvent,
  cfg: ShortcutsConfig,
  orderedIds: string[],
  activeId: string | undefined,
): { type: 'select'; id: string } | { type: 'new' | 'close' } | null {
  const order = computeVisibleTabOrder(orderedIds)
  const b = (action: ShortcutAction): string => bindingFor(cfg, action)

  const digit = matchesPrefixDigit(e, b('switchTab'))
  if (digit !== undefined) {
    const id = order.idAtPosition(digit)
    return id ? { type: 'select', id } : null
  }
  if (matchesBinding(e, b('nextTab'))) {
    const id = activeId ? order.nextId(activeId) : orderedIds[0]
    return id ? { type: 'select', id } : null
  }
  if (matchesBinding(e, b('prevTab'))) {
    const id = activeId ? order.prevId(activeId) : orderedIds[orderedIds.length - 1]
    return id ? { type: 'select', id } : null
  }
  if (matchesBinding(e, b('newTab'))) return { type: 'new' }
  if (matchesBinding(e, b('closeTab'))) return activeId ? { type: 'close' } : null
  return null
}

/**
 * leader 按下之后，这一个键是什么意思。
 *
 * 三种结局，缺一不可：
 * · 命中 → 执行，并吞掉这个键。
 * · **明确取消**（Esc / 又按了一次 leader）→ 退出等待，什么都不发。
 * · 没命中 → 退出等待，**并且把这个键放行**。这一条是有意的：一个"进了模式就吞掉一切"的 leader，
 *   会在你误触之后静静吃掉你接下来敲的那个字符，而你完全不知道发生了什么。放行的代价只是那个字符
 *   进了终端 —— 看得见，也就改得掉。
 */
export type LeaderResolution =
  | { type: 'action'; action: LeaderAction; digit?: number }
  | { type: 'cancel' }
  | { type: 'passthrough' }

/**
 * @param available 这个宿主**真的实现了**的动作。没实现的一律 passthrough —— 绝不能「先吞键、
 *   再调用一个不存在的回调」：那样按下去既没动作、键也没进终端，是屏幕上什么都不发生的死键。
 *   宿主能力有差异（pro 的壳没有 overview/rename），差异必须在**解析阶段**就体现出来。
 */
export function resolveLeaderKey(
  e: KeyboardEvent,
  leader: string,
  available: ReadonlySet<LeaderAction>,
): LeaderResolution {
  if (e.key === 'Escape') return { type: 'cancel' }
  // 修饰键本身不算一段 —— 按住 Ctrl 准备敲第二段时，keydown 会先为 Control 触发一次。
  if (['Alt', 'Control', 'Shift', 'Meta'].includes(e.key)) return { type: 'passthrough' }
  // 又按一次 leader = 取消（和 tmux 一样，也是误触后最自然的退出方式）。
  if (matchesBinding(e, leader)) return { type: 'cancel' }

  // 第二段刻意**不带任何修饰键**：leader 的全部意义就是第二段不需要修饰键。Ctrl+B 然后 Ctrl+C
  // 应该是「取消 + 一个真正的 ^C」，不是某个隐藏绑定；Shift 同理 —— Shift+3 打出的是 `#`，
  // 把它当成「跳到第 3 个」会让人在敲字符时莫名其妙地切了标签。
  if (e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return { type: 'passthrough' }

  const digit = /^Digit([1-9])$/.exec(e.code)
  if (digit) {
    return available.has('switchTab')
      ? { type: 'action', action: 'switchTab', digit: Number(digit[1]) }
      : { type: 'passthrough' }
  }
  const mapped = LEADER_CODES[e.code]
  if (mapped && available.has(mapped)) return { type: 'action', action: mapped }
  return { type: 'passthrough' }
}

/**
 * 这个键落在一个**正在输入文字**的地方吗。
 *
 * leader 的监听挂在 document 的捕获阶段 —— 它先于任何组件自己的 `@keydown.stop` 拿到事件。
 * 于是在标签重命名框里敲 `Ctrl+B` 再敲 `x`，会被解释成「关闭当前标签」：一次输入手滑，一个
 * 会话就没了。快捷键**永远不该在文字输入途中生效**。
 *
 * xterm 的隐藏 textarea 是个例外：它不是表单输入框，而是终端本身的输入通道 —— leader 必须在
 * 它上面工作，否则这个功能在最主要的场景里就是不存在的。
 */
function isTypingTarget(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null
  if (!t || typeof t.tagName !== 'string') return false
  if (t.classList?.contains('xterm-helper-textarea')) return false
  if (t.isContentEditable) return true
  return t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT'
}

/** 误触之后不至于让 leader 一直挂着。tmux 的前缀没有超时，但它有状态行可看；这里没有。 */
export const LEADER_TIMEOUT_MS = 2000

export function useTabShortcuts(adapter: TabShortcutsAdapter): {
  leaderPending: ComputedRef<boolean>
  /** 提示条上印的键名。走 bindingLabel（绑定措辞的 SSOT），和设置页显示的是同一个结果。 */
  leaderLabel: ComputedRef<string>
} {
  const { config, loading, load } = useShortcutsConfig()

  /**
   * 这个宿主**真的实现了**哪些 leader 动作。
   *
   * select/new/close 是 adapter 的必填项，永远可用；overview/rename 是可选的（有的壳没有这两个
   * 概念）。没实现的动作必须在**解析阶段**就 passthrough —— 而不是先吞掉键、再去调一个
   * `undefined?.()`。后者按下去既没有动作、键也没进终端，是屏幕上什么都不发生的死键，
   * 而使用者只会以为键盘坏了。
   */
  const availableActions: ReadonlySet<LeaderAction> = new Set<LeaderAction>([
    'switchTab', 'nextTab', 'prevTab', 'newTab', 'closeTab',
    ...(adapter.onOverview ? (['overview'] as const) : []),
    ...(adapter.onRename ? (['rename'] as const) : []),
    ...(adapter.onCopyMode ? (['copyMode'] as const) : []),
  ])

  /**
   * leader 等待态 —— 不只是「在等」，还记着**替谁、按哪个键在等**。
   *
   * 只用一个 boolean 会漏掉两秒之间世界可能变了这件事：在非 tmux 标签按下 leader、切到一个
   * attach 了 tmux 的标签再按 `c`，或者这两秒里刚好 attach 上、或者设置页刚换了 leader ——
   * 那一下就不再属于我们，却仍会被吃掉。第二段必须重新确认自己等的还是同一件事。
   */
  const pending = ref<{ tabId: string | undefined; binding: string } | null>(null)
  const leaderPending = computed(() => pending.value !== null)
  let leaderTimer: ReturnType<typeof setTimeout> | null = null

  function clearLeader(): void {
    pending.value = null
    if (leaderTimer) { clearTimeout(leaderTimer); leaderTimer = null }
  }
  function armLeader(binding: string): void {
    pending.value = { tabId: adapter.activeTabId(), binding }
    if (leaderTimer) clearTimeout(leaderTimer)
    leaderTimer = setTimeout(clearLeader, LEADER_TIMEOUT_MS)
  }

  /** leader 此刻归不归我们：配置得先落地，宿主得说这个标签归我们，而且它得真的配了一个键。 */
  function leaderOwned(): boolean {
    // 配置还没水合完就注册，等于用**默认值**去抢键 —— 而使用者可能早就把它关掉或改掉了。
    // 首屏那几百毫秒里抢走 Ctrl+B，对一个 tmux 用户就是一次实打实的误触。
    if (loading.value) return false
    if (!config.value.leader) return false
    // 没接这个回调的壳 = 我们不知道它的标签里有没有 tmux = 不碰这个键。见 adapter 上的说明。
    if (!adapter.leaderEnabled) return false
    return adapter.leaderEnabled()
  }

  function runLeaderAction(action: LeaderAction, digit: number | undefined, orderedIds: string[]): void {
    const order = computeVisibleTabOrder(orderedIds)
    const activeId = adapter.activeTabId()
    switch (action) {
      case 'switchTab': {
        const id = digit !== undefined ? order.idAtPosition(digit) : undefined
        if (id) adapter.onSelect(id)
        break
      }
      case 'nextTab': {
        const id = activeId ? order.nextId(activeId) : orderedIds[0]
        if (id) adapter.onSelect(id)
        break
      }
      case 'prevTab': {
        const id = activeId ? order.prevId(activeId) : orderedIds[orderedIds.length - 1]
        if (id) adapter.onSelect(id)
        break
      }
      case 'newTab': adapter.onNew(); break
      case 'closeTab': if (activeId) adapter.onClose(activeId); break
      case 'overview': adapter.onOverview?.(); break
      case 'rename': if (activeId) adapter.onRename?.(activeId); break
      case 'copyMode': adapter.onCopyMode?.(); break
    }
  }

  function handleKeydown(e: KeyboardEvent): void {
    if (!adapter.isActive()) return
    const orderedIds = adapter.orderedTabIds()
    if (orderedIds.length === 0) return

    // 正在输入文字的地方（重命名框、compose…）一律不碰 —— 见 isTypingTarget。这条必须在最前面，
    // 连等待态都要一起放弃：否则「在输入框里敲的第二个键」照样会被当成命令。
    if (isTypingTarget(e)) {
      clearLeader()
      return
    }
    // 长按自动重复不该被当成一次次新的按键（按住 Ctrl+B 会反复重置等待态）。
    if (e.repeat) return

    // ── 第二段 ────────────────────────────────────────────────────────────────────────────────
    const armed = pending.value
    if (armed) {
      // 修饰键本身不算一段：按住 Ctrl 准备敲第二段时 keydown 会先为 Control 触发一次，
      // 那一下不能把等待态清掉。
      if (['Alt', 'Control', 'Shift', 'Meta'].includes(e.key)) return

      // 这两秒里世界可能变了：切了标签、刚 attach 上 tmux、设置页换了 leader。任一条不成立，
      // 这一下就不再属于我们 —— 放弃等待并**放行**，绝不吞。
      if (armed.tabId !== adapter.activeTabId() || armed.binding !== config.value.leader || !leaderOwned()) {
        clearLeader()
        return
      }

      const r = resolveLeaderKey(e, armed.binding, availableActions)
      clearLeader()
      if (r.type === 'action') {
        e.preventDefault()
        e.stopImmediatePropagation()
        runLeaderAction(r.action, r.digit, orderedIds)
      } else if (r.type === 'cancel') {
        e.preventDefault()
        e.stopImmediatePropagation()
      }
      // passthrough：不拦，那个键照常进终端。
      return
    }

    // ── 第一段 ────────────────────────────────────────────────────────────────────────────────
    // 让位在这里发生：attach 了 tmux（或配置未落地）就当作根本没有这条绑定，字节原样进 PTY。
    if (leaderOwned() && matchesBinding(e, config.value.leader)) {
      e.preventDefault()
      e.stopImmediatePropagation()
      armLeader(config.value.leader)
      return
    }

    // ── 单修饰键前缀那条路（和 leader 并行，互不影响）─────────────────────────────────────────
    const action = resolveShortcutAction(e, config.value, orderedIds, adapter.activeTabId())
    if (!action) return

    e.preventDefault()
    e.stopImmediatePropagation()

    switch (action.type) {
      case 'select': adapter.onSelect(action.id); break
      case 'new': adapter.onNew(); break
      case 'close': { const id = adapter.activeTabId(); if (id) adapter.onClose(id); break }
    }
  }

  onMounted(() => {
    void load()
    document.addEventListener('keydown', handleKeydown, { capture: true })
  })
  onBeforeUnmount(() => {
    clearLeader()
    document.removeEventListener('keydown', handleKeydown, { capture: true })
  })

  return { leaderPending, leaderLabel: computed(() => bindingLabel(config.value.leader)) }
}
