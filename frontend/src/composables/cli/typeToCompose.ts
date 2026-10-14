/**
 * typeToCompose — 「打字即成框」的事件侧（terminal-input REQ-TI-01）。
 *
 * 判定本身在 composeIntercept.ts（纯函数）；这里只管**事件怎么吃**：
 *   · 可见字符：keydown 阶段 preventDefault + 吃掉，字塞进输入框，一个字节都不发给终端。
 *   · 输入法：不能 preventDefault 一个 keyCode=229 的 keydown（那会掐掉输入法自己），要做的是在
 *     这次 keydown 里把焦点换到输入框——Chrome 的顺序是 keydown 之后才 compositionstart，到那时
 *     焦点已经在输入框里，拼写整个发生在输入框。Safari 的顺序相反（compositionstart 在 keydown
 *     之前），拼写已经在终端的 textarea 里开始了，这时只能把 composition 事件吃掉，等 compositionend
 *     把定稿的字交接给输入框。两条路径都存在是因为浏览器的事件顺序不一致，不是冗余。
 *
 * keydown 不在 document 捕获阶段挂，而是走 xterm 的 attachCustomKeyEventHandler（见 XtermTerminal 的
 * keyIntercept 属性）：标签快捷键、leader 键、查找键都是 document 捕获阶段的监听器，而 leader 的
 * 第二段是一个**普通字母**（Ctrl+B 然后 c）——若我们的监听器排在它前面（子组件的 onMounted 先于
 * 父级），那个 c 会被当成"第一个字符"拦进输入框。xterm 的回调在 textarea 上，天然排在所有
 * document 捕获监听器之后：到我们这里的，都是别人不要的键。composition 事件没有这个问题，仍挂 document。
 *
 * 没有 DOM 依赖：事件对象和全部副作用都经 deps 传入，所以整条状态机能在没有浏览器的测试里跑。
 */
import {
  classifyKey,
  decideIntercept,
  type InterceptFacts,
  type InterceptVerdict,
  type KeyFacts,
} from './composeIntercept'

export interface KeyEventLike extends KeyFacts {
  target: EventTarget | null
  defaultPrevented: boolean
  preventDefault(): void
  stopPropagation(): void
  stopImmediatePropagation(): void
}

export interface CompositionEventLike {
  target: EventTarget | null
  data?: string
  stopPropagation(): void
  stopImmediatePropagation(): void
}

export interface TypeToComposeDeps {
  /** 每次判定现读，不缓存：这些全是会在两次按键之间变的事实。 */
  facts(): Omit<InterceptFacts, 'promptEmpty'>
  promptEmpty(): boolean
  /** target 是不是**本 surface** 终端的 helper textarea（分屏时两格都会收到 document 上的按键）。 */
  isTerminalInput(target: EventTarget | null): boolean
  /** 打开输入框，并把这次敲的字放进去（光标在末尾，焦点在输入框）。 */
  openWithText(text: string): void
  /** 打开输入框等输入法在里面拼写：必须在当前 keydown 的同一个微任务检查点内把焦点交过去。 */
  openForIme(): void
  /** 输入法在终端 textarea 里拼完的定稿文字，交给输入框。 */
  adoptCommittedText(text: string): void
  /** 清掉终端 helper textarea 里残留的拼写内容。 */
  clearTerminalInput(): void
  /** 诊断（默认关闭，开了才记）：只带原因和模式，不带用户的字。 */
  report?(event: string, detail: Record<string, unknown>): void
  now?(): number
}

/** keydown(229) 拦下后，composition 仍落在终端 textarea 的容忍窗口。 */
const IME_ARM_MS = 600

export interface TypeToCompose {
  /** 无副作用地问一句：这次 keydown 会被拦吗？（WKWebView 上另有一条直发路径，要先让位。） */
  wouldIntercept(e: KeyEventLike): boolean
  onKeydown(e: KeyEventLike): boolean
  onCompositionStart(e: CompositionEventLike): boolean
  onCompositionUpdate(e: CompositionEventLike): boolean
  onCompositionEnd(e: CompositionEventLike): boolean
}

export function createTypeToCompose(deps: TypeToComposeDeps): TypeToCompose {
  const now = deps.now ?? (() => Date.now())
  /** 最近一次拦下 keydown(229) 的时刻；0 = 没有。 */
  let imeArmedAt = 0
  /**
   * 最近一次因"命令前缀"放行的 keydown(229) 的时刻；0 = 没有。输入法激活时敲 `/`，keydown 里还能看到
   * 字符，放行了；随后的 compositionstart 里字符已经看不见，若重新判一次就会把这次拼写拦走 ——
   * 同一次按键前后两个判定自相矛盾。所以放行的结论要带到紧随其后的 compositionstart。
   */
  let imePassedAt = 0
  /** 正在吞终端 textarea 上的一段拼写，等 compositionend 交接。 */
  let transferring = false

  const verdictFor = (trigger: Parameters<typeof decideIntercept>[1]): InterceptVerdict =>
    decideIntercept({ ...deps.facts(), promptEmpty: deps.promptEmpty() }, trigger)

  const swallow = (e: { stopPropagation(): void; stopImmediatePropagation(): void }): void => {
    e.stopPropagation()
    e.stopImmediatePropagation()
  }

  function wouldIntercept(e: KeyEventLike): boolean {
    if (!deps.isTerminalInput(e.target)) return false
    return verdictFor({ kind: 'key', key: e }).intercept
  }

  function onKeydown(e: KeyEventLike): boolean {
    if (e.defaultPrevented) return false
    if (!deps.isTerminalInput(e.target)) return false
    if (transferring) {
      // 交接期间终端 textarea 上的拼写按键（含确认候选的 Enter）整段不许让 xterm 看见。
      if (e.isComposing || e.keyCode === 229) { swallow(e); return true }
      // 来了一个不是拼写中的按键：上一段交接如果因为没收到 compositionend 而悬着，到此为止。
      transferring = false
    }

    const verdict = verdictFor({ kind: 'key', key: e })
    if (!verdict.intercept) {
      // 只记"差一点就拦了"的键（可见字符 / 拼音第一键），不记方向键 Tab 之类每秒几十次的噪音。
      const cls = classifyKey(e)
      if (cls === 'text' || cls === 'ime-start') deps.report?.('type-to-compose.pass', { reason: verdict.reason })
      if (cls === 'ime-start' && verdict.reason === 'command-prefix') imePassedAt = now()
      return false
    }

    deps.report?.('type-to-compose.intercept', { mode: verdict.mode, trigger: 'keydown' })
    if (verdict.mode === 'text') {
      e.preventDefault()
      swallow(e)
      deps.openWithText(verdict.text)
      return true
    }
    // 输入法：不 preventDefault（那会让输入法本身失效），只让终端和其它 keydown 监听器看不到这一键，
    // 并在这个微任务检查点内把焦点交给输入框。
    swallow(e)
    imeArmedAt = now()
    deps.openForIme()
    return true
  }

  function onCompositionStart(e: CompositionEventLike): boolean {
    if (!deps.isTerminalInput(e.target)) return false
    const armed = imeArmedAt !== 0 && now() - imeArmedAt < IME_ARM_MS
    const prefixPassed = imePassedAt !== 0 && now() - imePassedAt < IME_ARM_MS
    imeArmedAt = 0
    imePassedAt = 0
    if (prefixPassed) {
      deps.report?.('type-to-compose.pass', { reason: 'command-prefix', trigger: 'compositionstart' })
      return false
    }
    if (!armed) {
      const verdict = verdictFor({ kind: 'composition' })
      if (!verdict.intercept) {
        deps.report?.('type-to-compose.pass', { reason: verdict.reason, trigger: 'compositionstart' })
        return false
      }
      deps.report?.('type-to-compose.intercept', { mode: 'ime', trigger: 'compositionstart' })
      deps.openForIme()
    } else {
      // keydown 已经把输入框开了、焦点也换了，composition 却还是落在终端 textarea 里（这个浏览器
      // 的输入法已经绑定了旧元素）：照样吞掉，等定稿交接。
      deps.report?.('type-to-compose.intercept', { mode: 'ime', trigger: 'compositionstart-after-keydown' })
    }
    swallow(e)
    transferring = true
    return true
  }

  function onCompositionUpdate(e: CompositionEventLike): boolean {
    if (!transferring || !deps.isTerminalInput(e.target)) return false
    swallow(e)
    return true
  }

  function onCompositionEnd(e: CompositionEventLike): boolean {
    if (!transferring || !deps.isTerminalInput(e.target)) return false
    swallow(e)
    transferring = false
    const committed = e.data ?? ''
    if (committed) deps.adoptCommittedText(committed)
    deps.clearTerminalInput()
    return true
  }

  return { wouldIntercept, onKeydown, onCompositionStart, onCompositionUpdate, onCompositionEnd }
}
