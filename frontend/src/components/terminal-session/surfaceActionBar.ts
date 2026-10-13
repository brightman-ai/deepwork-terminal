/**
 * surfaceActionBar —— 终端表面状态行（非 tmux 那半边）的**判定**，与 Vue 无关，因此可测。
 *
 * ── 为什么这一行值得放按钮 ────────────────────────────────────────────────────────────────────
 * 右侧的连接心跳本来就常驻，这一行的空间成本早已付出；空着并不省什么，只是浪费。而且我们是**浏览器
 * 里的终端**：没有菜单栏，Cmd+W/T/F/N 被浏览器抢走，手机上根本没有修饰键 —— 可见控件在这里的权重
 * 天然高于原生终端。所以左区放动作，中区放一句"此刻最具体的真话"。
 *
 * ── 两条硬规矩 ────────────────────────────────────────────────────────────────────────────────
 * 1. **顺序固定，永不重排**：动作按 SURFACE_ACTION_ORDER 出场，条件动作只在自己的位置上出现/消失。
 *    一个会换位置的按钮等于每次都要重新找它。
 * 2. **每个条件动作的出现条件 = 它的语义**：有选区才有"复制"，离底才有"回到最新"，agent 在跑才有
 *    "中断"。这样按钮出现本身就说明了它此刻做什么，不需要再解释一遍。
 */

export type SurfaceActionId = 'search' | 'page-up' | 'page-down' | 'delete' | 'to-bottom' | 'copy' | 'interrupt'

/** 出场顺序。位置由这里定死，与可见性无关。
 *  page-up/page-down/delete 插在 search 之后——2026-10-12 笔记本缺键补充：没有独立 PgUp/PgDn/
 *  前向 Delete 键位的笔记本（常见于紧凑键盘）没法做这几件事，键态只是"缺"不是"条件"，所以跟
 *  search 一样常驻可见，不挂判定。原有四项（to-bottom…）的相对顺序不动。 */
export const SURFACE_ACTION_ORDER: readonly SurfaceActionId[] =
  ['search', 'page-up', 'page-down', 'delete', 'to-bottom', 'copy', 'interrupt']

/**
 * 「中断」发出去的字节：**Esc**。
 *
 * 绝不是 Ctrl+C。Esc 是 Claude Code / Codex 这类 agent 的原生中断（停止本轮生成、会话保住）；
 * Ctrl+C 一次误点就是杀进程、丢上下文。一个单击就能碰到的按钮不允许有那种代价。
 */
export const INTERRUPT_SEQUENCE = '\x1b'
/** 只为在测试里点名"这个不许发"而存在。 */
export const SIGINT_SEQUENCE = '\x03'

/** 这一行需要知道的全部终端状态。全是布尔/数字 —— 判定不碰 DOM，也不碰 xterm。 */
export interface SurfaceActionState {
  /** 视口贴着缓冲区底部（= 正在看最新输出）。 */
  atBottom: boolean
  /** 离底之后又滚过去的新行数（贴底时恒为 0）。 */
  newLinesBelow: number
  /** 终端里此刻有选中的文本。 */
  hasSelection: boolean
  /** 这个会话检测到了 agent 且它正在跑。裸 shell 不配单击中断。 */
  agentRunning: boolean
}

/** 动作被按下时要做的事。由组件注入（它才知道 xterm 和 WebSocket 在哪），所以这里可以纯着测。 */
export interface SurfaceActionDeps {
  openSearch(): void
  scrollToBottom(): void
  copySelection(): void
  /** 往 PTY 里发一段原始字节——走的是无分流的裸发送，Delete 这种"任何缓冲区下都该原样转发"
   *  的键用它就够。PageUp/PageDown 不走这条：见 pageUp/pageDown。 */
  sendKey(sequence: string): void
  /** PgUp/PgDn 需要「normal buffer 下滚 xterm 自己的 scrollback、alt-screen/tmux 下转发给
   *  PTY」这层分流判断（CliTerminalSurface.vue 的 onSendKey 已经做好、已验证），所以单独两个
   *  槽位，不能跟 sendKey 共用——直接裸发 \x1b[5~/\x1b[6~ 在普通 shell 下会静默无反应
   *  （shell 根本不认这两个字节，只有 scrollback 才是这块屏幕上唯一存着历史的地方）。 */
  pageUp(): void
  pageDown(): void
}

export interface SurfaceAction {
  id: SurfaceActionId
  /** 无障碍名 + tooltip 主文。 */
  label: string
  /** tooltip 补充的第二段（快捷键、行数……）；'' = 不补。 */
  hint: string
  /** 图标角上的小数字；'' = 不显示。 */
  badge: string
  /** 非空 = 渲染成键帽文字而不是图标。只给"补一个物理键"的动作用：用户找的就是 PgUp/Del 这个键，
   *  键名本身就是最直白的意符（2026-10-13 实测：一排图标只有搜索看得懂，Delete 图标还长得像退格）。 */
  keycap: string
  visible: boolean
  run(): void
}

/** tooltip 全文：主文 + 第二段。按钮自己不再带 title —— 原生 title 要停一秒多才出、还会和这条叠两层。 */
export function surfaceActionTip(a: Pick<SurfaceAction, 'label' | 'hint'>): string {
  return a.hint ? `${a.label} · ${a.hint}` : a.label
}

/** 三位数以上不再逐个数——"99+"就够回答"下面积了很多"这个问题了。 */
function badgeFor(count: number): string {
  if (count <= 0) return ''
  return count > 99 ? '99+' : String(count)
}

/**
 * 这一行此刻的动作表。**总是返回全部四项**（顺序即 SURFACE_ACTION_ORDER），可见性写在 visible 上，
 * 由调用方过滤 —— 测试因此能同时断言"谁在场"和"谁按顺序排在哪"。
 *
 * `searchHint` 是搜索的快捷键文案，来自用户自己的配置（见 useShortcutsConfig.bindingLabel）。
 */
export function buildSurfaceActions(
  state: SurfaceActionState,
  deps: SurfaceActionDeps,
  searchHint = '',
): SurfaceAction[] {
  const byId: Record<SurfaceActionId, SurfaceAction> = {
    search: {
      id: 'search',
      label: '在终端里搜索',
      hint: searchHint,
      badge: '',
      keycap: '',
      // 常驻：它是这一行唯一一个"什么时候都可能想要"的动作，也是此前只有快捷键、意符完全缺失的那个。
      visible: true,
      run: deps.openSearch,
    },
    'page-up': {
      id: 'page-up',
      label: '向上翻页（PageUp）',
      hint: '看更早的输出',
      badge: '',
      keycap: 'PgUp',
      visible: true, // 笔记本缺键，不是条件动作——任何时候都可能想翻页，见上方头注
      run: deps.pageUp,
    },
    'page-down': {
      id: 'page-down',
      label: '向下翻页（PageDown）',
      hint: '往新的方向翻',
      badge: '',
      keycap: 'PgDn',
      visible: true,
      run: deps.pageDown,
    },
    delete: {
      id: 'delete',
      label: '向前删除（Delete）',
      hint: '删掉光标右边的一个字，不是退格',
      badge: '',
      keycap: 'Del',
      visible: true,
      run: () => deps.sendKey('\x1b[3~'),
    },
    'to-bottom': {
      id: 'to-bottom',
      label: '回到最新输出',
      // 数字回答的是"我错过了多少"，不是装饰：贴底时这个动作根本不存在，所以它一出现就一定有数可说。
      hint: state.newLinesBelow > 0 ? `${state.newLinesBelow} 行新输出` : '',
      badge: badgeFor(state.newLinesBelow),
      keycap: '',
      visible: !state.atBottom,
      run: deps.scrollToBottom,
    },
    copy: {
      id: 'copy',
      label: '复制选中的文字',
      hint: '',
      badge: '',
      keycap: '',
      // 有选区才出现 = 语义无歧义（"复制什么"永远有答案），顺带精确解掉浏览器/手机上最疼的那一下。
      visible: state.hasSelection,
      run: deps.copySelection,
    },
    interrupt: {
      id: 'interrupt',
      label: '中断 agent',
      hint: '发送 Esc — 停止当前生成，会话保留',
      badge: '',
      keycap: '',
      visible: state.agentRunning,
      run: () => deps.sendKey(INTERRUPT_SEQUENCE),
    },
  }
  return SURFACE_ACTION_ORDER.map((id) => byId[id])
}

/** 上面那张表里此刻真正渲染的部分。 */
export function visibleSurfaceActions(
  state: SurfaceActionState,
  deps: SurfaceActionDeps,
  searchHint = '',
): SurfaceAction[] {
  return buildSurfaceActions(state, deps, searchHint).filter((a) => a.visible)
}

// ── 中区内容槽 ────────────────────────────────────────────────────────────────────────────────

/** 这句话是从哪一档来的。渲染要用（cwd 档在窄屏第一个被砍），测试也要用。 */
export type SurfaceSlotTier = 'agent' | 'title' | 'cwd' | 'none'

export interface SurfaceSlotSources {
  /** agent 那一句：原话优先，否则我们推断的状态句（措辞全app一套，见 useSessionsOverview）。 */
  agent: string
  /** 终端标题（OSC 0/2）。多数 shell 会把正在跑的命令写进去 —— 原生终端标题栏显示的就是它。 */
  title: string
  /** 这个会话的工作目录。最后一档，也是"我在哪"这个问题的直接答案（标签名回答不了）。 */
  cwd: string
}

/**
 * 中区此刻该说的话，**永不空着**：agent 那一句 → 正在跑的命令（终端标题）→ cwd。
 *
 * 逐级回落而不是拼在一起：一行只放一句，最具体的那句。三档全空才 'none'（一个刚建好、还没 cwd
 * 的会话——那时确实无话可说，整格不渲染）。
 *
 * 标题等于 cwd 时视为没有标题：那是 shell 把路径写进了标题，不是"正在跑的命令"，
 * 留着只会让同一句话在同一行出现两次。
 */
export function surfaceSlot(src: SurfaceSlotSources): { text: string; tier: SurfaceSlotTier } {
  const agent = (src.agent || '').trim()
  if (agent) return { text: agent, tier: 'agent' }
  const cwd = (src.cwd || '').trim()
  const title = (src.title || '').trim()
  if (title && title !== cwd && title !== shortCwd(cwd)) return { text: title, tier: 'title' }
  if (cwd) return { text: shortCwd(cwd), tier: 'cwd' }
  return { text: '', tier: 'none' }
}

/**
 * 路径压到"最后两段"，前面用 … 顶掉：`/home/u/code/stwork/deepwork-terminal` → `…/stwork/deepwork-terminal`。
 *
 * 砍头不砍尾，因为有信息量的永远是尾巴（CSS 的省略号恰恰相反，它砍尾巴）。
 */
export function shortCwd(cwd: string): string {
  const raw = (cwd || '').trim()
  if (!raw) return ''
  const segs = raw.split('/').filter(Boolean)
  if (segs.length <= 2) return raw
  return `…/${segs.slice(-2).join('/')}`
}
