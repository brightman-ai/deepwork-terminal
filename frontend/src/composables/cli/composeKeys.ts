/**
 * composeKeys — 桌面输入框里每个按键归谁（terminal-input REQ-TI-04 / REQ-TI-05）。
 *
 * 纯函数，没有 DOM：输入框的键位是这个功能最容易悄悄变味的地方（回车被吞成换行、拼音确认候选的
 * 回车被当成发送、空框里的 Esc 没打断 agent），每一条都值得单独钉住。
 *
 * 一个原则贯穿全表：**框空着的时候它不是输入框，是终端的延伸**。Esc / Ctrl-C / 方向键 / Tab / 回车
 * 照旧发给终端（打断 agent、翻历史、确认），框随之收起；框里有字时这些键才属于框。
 */

export interface ComposerKeyFacts {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
  shiftKey: boolean
  isComposing?: boolean
  keyCode?: number
}

export interface ComposerKeyContext {
  /** 输入框当前全文。 */
  text: string
  /** 输入法正在拼写（来自 composition 事件，比 keydown 上的 isComposing 更可靠）。 */
  composing: boolean
  /** 终端是否处于「应用光标键」模式（DECCKM）：方向键要发 SS3 而不是 CSI。 */
  applicationCursorKeys: boolean
}

export type ComposerKeyAction =
  /** 不管：交给 textarea 自己（输入、移动光标、复制粘贴、输入法）。 */
  | { kind: 'ignore' }
  /** Enter：全文作为一次粘贴送入终端，随后一次回车提交。 */
  | { kind: 'send' }
  /** Esc 且框里有字：收起并保留草稿（若框里只有触发它的那次输入且没动过，由调用方撤回补发）。 */
  | { kind: 'close-keep' }
  /** 框里有字时的 Tab：在框里插入制表符（不能让焦点跑出输入框）。 */
  | { kind: 'insert-tab' }
  /** 框里有字时的 Shift+Tab：吃掉，只为了不让焦点跑出输入框。 */
  | { kind: 'swallow' }
  /** 框是空的：这个键原样发给终端，框随之收起。 */
  | { kind: 'passthrough'; data: string }

const ARROW_FINAL: Record<string, string> = { ArrowUp: 'A', ArrowDown: 'B', ArrowRight: 'C', ArrowLeft: 'D' }

/** 空框直通时，一个键该发给终端的字节；不在直通范围内返回 null。 */
export function passthroughBytes(k: ComposerKeyFacts, applicationCursorKeys: boolean): string | null {
  const bare = !k.ctrlKey && !k.metaKey && !k.altKey && !k.shiftKey
  const final = ARROW_FINAL[k.key]
  if (final) return bare ? (applicationCursorKeys ? '\x1bO' : '\x1b[') + final : null
  switch (k.key) {
    case 'Escape': return bare ? '\x1b' : null
    case 'Enter': return !k.shiftKey && !k.altKey ? '\r' : null
    case 'Tab': return !k.ctrlKey && !k.metaKey && !k.altKey ? (k.shiftKey ? '\x1b[Z' : '\t') : null
    case 'c':
    case 'C':
      return k.ctrlKey && !k.metaKey && !k.altKey && !k.shiftKey ? '\x03' : null
    default: return null
  }
}

export function composerKeyAction(k: ComposerKeyFacts, ctx: ComposerKeyContext): ComposerKeyAction {
  // 拼写中的一切按键都归输入法：Enter 只确认候选、Esc 只取消拼写、方向键翻候选。
  // Safari 里确认候选的那次 Enter 到达时 isComposing 已经是 false、keyCode 却仍是 229，所以两个都看。
  if (ctx.composing || k.isComposing || k.keyCode === 229) return { kind: 'ignore' }

  const empty = ctx.text.length === 0
  if (empty) {
    const data = passthroughBytes(k, ctx.applicationCursorKeys)
    return data === null ? { kind: 'ignore' } : { kind: 'passthrough', data }
  }

  switch (k.key) {
    case 'Enter':
      // Shift+Enter 换行（textarea 的默认行为）；其余 Enter 一律发送并提交。
      return k.shiftKey ? { kind: 'ignore' } : { kind: 'send' }
    case 'Escape':
      return { kind: 'close-keep' }
    case 'Tab':
      if (k.ctrlKey || k.metaKey || k.altKey) return { kind: 'ignore' }
      return k.shiftKey ? { kind: 'swallow' } : { kind: 'insert-tab' }
    default:
      return { kind: 'ignore' }
  }
}
