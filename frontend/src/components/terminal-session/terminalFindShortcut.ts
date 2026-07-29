/**
 * findInTerminal 快捷键的判定 —— 一条纯函数，因为这里出过的 bug 恰恰藏在**判定顺序**里。
 *
 * 原来的写法是「搜索条已经开着就早退」：
 *
 *   if (!props.active || terminalSearchOpen.value) return   // ← 早退
 *   if (!matchesBinding(...)) return
 *   e.preventDefault()
 *
 * 于是连按两次 Cmd+F，第二次压根走不到 preventDefault，按键漏给了浏览器 —— 浏览器自带的查找框
 * 和我们自己的搜索条同时出现在屏幕上（Human 实测截图）。一个被「接管」的快捷键必须**每一次**都
 * 被吃掉，否则用户学到的是「有时候会冒出两个搜索框」。
 *
 * 正确顺序：**先匹配按键，再决定开还是聚焦**。匹配上了就一律 preventDefault + stopPropagation；
 * 没开就开，已开就把焦点送回输入框并全选已有查询词（直接改词重搜，和 VS Code / 各编辑器一致）。
 */

/** 这次按键的最小契约 —— 只用到这两个方法，所以测试可以喂一个假 event。 */
export interface FindShortcutEvent {
  preventDefault(): void
  stopPropagation(): void
}

export interface FindShortcutContext {
  /** 这个 surface 是不是当前可见的那个（每个标签的 surface 都挂着，都会收到这次 keydown）。 */
  active: boolean
  /** 按键是否匹配用户配置的 findInTerminal 绑定。 */
  matches: boolean
  /** 搜索条现在开着没有。 */
  open: boolean
}

/** ignore = 不关我们的事（按键原样交给浏览器/终端）；open = 打开搜索条；refocus = 已开，送回焦点。 */
export type FindShortcutIntent = 'ignore' | 'open' | 'refocus'

export function findShortcutIntent(ctx: FindShortcutContext): FindShortcutIntent {
  if (!ctx.active || !ctx.matches) return 'ignore'
  return ctx.open ? 'refocus' : 'open'
}

/**
 * 执行上面的判定。返回实际发生的 intent，方便调用方/测试断言。
 *
 * `ignore` 是唯一一条不碰 event 的路径 —— 只有那时候按键才该继续往下走。
 */
export function handleFindShortcut(
  e: FindShortcutEvent,
  ctx: FindShortcutContext,
  actions: { open: () => void; refocus: () => void },
): FindShortcutIntent {
  const intent = findShortcutIntent(ctx)
  if (intent === 'ignore') return intent
  e.preventDefault()
  e.stopPropagation()
  if (intent === 'open') actions.open()
  else actions.refocus()
  return intent
}
