/**
 * useTerminalHistory — 一个终端的**回看历史**数据源：分页取行、缓存样式表、服务端搜索。
 *
 * ── 为什么历史不走 xterm ────────────────────────────────────────────────────────────────────────
 * xterm 只能**追加**（`write()`），没有 prepend。想让人往回滚到第 1 行，唯一的办法要么是把整段
 * 历史重新灌进去（正是「F5 不要刷 20 万行」明令禁止的事，只是换了个触发时机），要么另开一个视口。
 * 所以复制模式不是"让 xterm 多滚一点"，而是**另一个只读视口看同一份历史** —— tmux 的 copy-mode
 * 本来就是这个形状。
 *
 * 于是这里拿到的不是字节流，是**行**：`{ n, seg[] }`，`n` 是全局单调的绝对行号（淘汰之后仍然只增，
 * 所以它直接就是分页游标，不需要另造坐标系）。
 *
 * ── 样式表为什么能增量取 ────────────────────────────────────────────────────────────────────────
 * 样式 id 在一个会话的生命周期内**只追加、永不复用**。所以客户端手上只要有表的一个**前缀**，就能
 * 解析它见过的每一个 id —— 这不是"省一点带宽"的优化，而是让增量传输**安全**的那条不变量：没有
 * 版本号要对，没有失效要处理。每次请求带上 `styles=<已有条数>`，服务端只回后面新增的。
 */
import { ref, shallowRef, type Ref, type ShallowRef } from 'vue'
import { cliApi } from './useCliApiPrefix'

/**
 * 取数用的 fetch。**由调用方注入**，不在这里自己拼。
 *
 * 两个理由，都不是洁癖：
 *  ① 认证有唯一出口 `useCliAuth().cliFetch`（带 X-CLI-Auth/X-Auth-Code，并在 401/429 时唤起认证
 *     对话框）。在这里另写一份 fetch，就是又一个"看起来一样、少了一半行为"的第二实现 —— 而它的
 *     症状是复制模式永远显示"0 行历史"，页面上没有任何报错。这不是假设：初版就是这么写的，
 *     单测（stub 掉 fetch）全绿，真机一打开就是空的。
 *  ② 这个组合式因此不碰 `window`/`localStorage`，可以在没有 DOM 的 bun 测试里直接跑。
 */
export type HistoryFetch = (path: string) => Promise<Response>

/** 一段同样式的文本。`s` 是样式表下标；0 恒为默认样式。 */
export interface HistorySegment {
  t: string
  s: number
}

/** 一行历史。`seg` 为空数组 = 空行（不是"没取到"）。 */
export interface HistoryLine {
  n: number
  seg: HistorySegment[]
}

/**
 * 一条样式。颜色是**字符串三态**而不是数字：`''`=终端默认色（渲染成 inherit），`'iN'`=调色板下标
 * （查看者自己的主题说了算），`'#rrggbb'`=真彩色。服务端刻意不把调色板解析成具体颜色 —— "第 2 号
 * 绿"是哪个绿属于查看者的主题，在存储侧解析等于把今天的主题烤进比它活得久的历史里。
 */
export interface HistoryStyle {
  fg?: string
  bg?: string
  /** 属性位：与后端 Attrs 同序（bold/dim/italic/underline/blink/inverse/hidden/strike）。 */
  a?: number
}

export const ATTR_BOLD = 1 << 0
export const ATTR_DIM = 1 << 1
export const ATTR_ITALIC = 1 << 2
export const ATTR_UNDERLINE = 1 << 3
export const ATTR_BLINK = 1 << 4
export const ATTR_INVERSE = 1 << 5
export const ATTR_HIDDEN = 1 << 6
export const ATTR_STRIKE = 1 << 7

export interface HistoryPage {
  enabled: boolean
  broken: boolean
  lines: HistoryLine[]
  /** 仍然保留着的最老一行；比它更老的已被淘汰。 */
  base: number
  /** 这个会话一共滚出去过多少行。`total - base` = 现在拿得到多少。 */
  total: number
  /** 服务端说不出历史的原因（目前只有 `daemon-too-old`）。 */
  reason?: string
}

export interface HistoryMatch {
  n: number
  /** 命中在该行内的**字符**下标（不是字节 —— 换算在服务端做完了）。 */
  col: number
  text: string
}

/** 一次取多少行。约十屏：滚动时不至于每格滚轮都发一次请求，一页又只有几十 KB。 */
export const HISTORY_PAGE = 500

export interface TerminalHistory {
  /** 已取到的行，按 `n` 升序，中间无洞。 */
  lines: ShallowRef<HistoryLine[]>
  styles: ShallowRef<HistoryStyle[]>
  base: Ref<number>
  total: Ref<number>
  enabled: Ref<boolean>
  broken: Ref<boolean>
  /** 服务端给出的不可用原因；`daemon-too-old` 时 UI 要如实说，别装成"没有历史"。 */
  reason: Ref<string>
  loading: Ref<boolean>
  /** 请求失败的原文（网络/权限）。空 = 没出错。 */
  error: Ref<string>
  /** 首次进入：取最近一页 + 当前可见屏。 */
  open: () => Promise<void>
  /** 滚到顶时向更早取一页。返回这次新增了多少行（0 = 已经到头）。 */
  loadOlder: () => Promise<number>
  search: (q: string, from: number, backward: boolean) => Promise<HistoryMatch[]>
  /** 确保某一行在 `lines` 里（搜索跳转用）。 */
  ensureVisible: (n: number) => Promise<void>
  reset: () => void
}

export function useTerminalHistory(
  sessionId: () => string | undefined,
  doFetch: HistoryFetch,
): TerminalHistory {
  const lines = shallowRef<HistoryLine[]>([])
  const styles = shallowRef<HistoryStyle[]>([])
  const base = ref(0)
  const total = ref(0)
  const enabled = ref(true)
  const broken = ref(false)
  const reason = ref('')
  const loading = ref(false)
  const error = ref('')

  function reset(): void {
    lines.value = []
    styles.value = []
    base.value = 0
    total.value = 0
    enabled.value = true
    broken.value = false
    reason.value = ''
    error.value = ''
  }

  /**
   * 取一页。`screen=true` 取的是当前可见屏（行号接着历史continue），不是滚出去的历史。
   *
   * 样式表**只请求自己还没有的部分**（见文件头）。返回 null = 这次请求失败，调用方保持原样别清空
   * —— 把已经拿到的历史因为一次网络抖动清掉，是比"少一页"糟得多的事。
   */
  async function fetchPage(params: Record<string, string>): Promise<HistoryPage | null> {
    const id = sessionId()
    if (!id) return null
    const qs = new URLSearchParams({ ...params, styles: String(styles.value.length) })
    try {
      const res = await doFetch(cliApi(`/sessions/${id}/history?${qs}`))
      if (!res.ok) {
        error.value = `HTTP ${res.status}`
        return null
      }
      const data = await res.json()
      error.value = ''
      enabled.value = data.enabled !== false
      broken.value = data.broken === true
      reason.value = typeof data.reason === 'string' ? data.reason : ''
      base.value = Number(data.base) || 0
      total.value = Number(data.total) || 0
      // 追加而不是替换：服务端只回了新增的那几条，前缀仍然有效。
      if (Array.isArray(data.styles) && data.styles.length > 0) {
        styles.value = styles.value.concat(data.styles as HistoryStyle[])
      }
      return {
        enabled: enabled.value,
        broken: broken.value,
        lines: Array.isArray(data.lines) ? (data.lines as HistoryLine[]) : [],
        base: base.value,
        total: total.value,
        reason: reason.value,
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      return null
    }
  }

  /** 把新取到的一页并进 `lines`，按 `n` 去重并保持升序。 */
  function merge(incoming: HistoryLine[]): number {
    if (incoming.length === 0) return 0
    const seen = new Set(lines.value.map((l) => l.n))
    const fresh = incoming.filter((l) => !seen.has(l.n))
    if (fresh.length === 0) return 0
    const next = lines.value.concat(fresh)
    next.sort((a, b) => a.n - b.n)
    lines.value = next
    return fresh.length
  }

  async function open(): Promise<void> {
    loading.value = true
    try {
      // 先问一次拿到 total，才知道"最近一页"从哪开始 —— 不能假设 0。
      const probe = await fetchPage({ from: '0', count: '1' })
      if (!probe) return
      lines.value = []
      const from = Math.max(probe.base, probe.total - HISTORY_PAGE)
      const page = await fetchPage({ from: String(from), count: String(HISTORY_PAGE) })
      if (page) merge(page.lines)
      // 可见屏接在历史后面。没有它，历史的末尾和终端正在显示的内容之间会空出一屏的洞，
      // 而那个洞有多大只有服务端知道。
      const screen = await fetchPage({ screen: '1' })
      if (screen) merge(screen.lines)
    } finally {
      loading.value = false
    }
  }

  async function loadOlder(): Promise<number> {
    if (loading.value) return 0
    const oldest = lines.value.length > 0 ? lines.value[0].n : total.value
    if (oldest <= base.value) return 0
    loading.value = true
    try {
      const from = Math.max(base.value, oldest - HISTORY_PAGE)
      const page = await fetchPage({ from: String(from), count: String(oldest - from) })
      return page ? merge(page.lines) : 0
    } finally {
      loading.value = false
    }
  }

  async function ensureVisible(n: number): Promise<void> {
    if (lines.value.some((l) => l.n === n)) return
    loading.value = true
    try {
      const from = Math.max(base.value, n - Math.floor(HISTORY_PAGE / 2))
      const page = await fetchPage({ from: String(from), count: String(HISTORY_PAGE) })
      if (page) {
        // 跳到一个离已有区间很远的位置时，中间会有洞。行是按 n 排序渲染的，一个洞会让滚动条
        // 说谎；干脆丢掉旧的、只留这一段，然后让向上翻页照常补。
        const contiguous = lines.value.length > 0
          && n >= lines.value[0].n - HISTORY_PAGE
          && n <= lines.value[lines.value.length - 1].n + HISTORY_PAGE
        if (!contiguous) lines.value = []
        merge(page.lines)
      }
    } finally {
      loading.value = false
    }
  }

  /**
   * 在已经载入的行里就地找一遍（当前可见屏就在这里面）。
   *
   * 为什么需要这一段：视图显示的是"历史 + 当前可见屏"，而服务端搜索**只覆盖历史** —— 当前屏还没
   * 滚出去，不在那份存储里。于是搜一个此刻明明就在屏幕上的词，会得到"无匹配"。真机上第一次搜就
   * 撞到了。
   *
   * 修在这一侧而不是 daemon 里，是因为屏幕内容本来就已经在客户端手上，而改 daemon 意味着又一次
   * `muxd --restart` —— 那要杀掉使用者全部会话，为一个客户端能自己答的问题付这个代价不合理。
   */
  function searchLoaded(q: string, from: number, backward: boolean): HistoryMatch[] {
    const needle = q.toLowerCase()
    const hits: HistoryMatch[] = []
    for (const l of lines.value) {
      // 只看服务端搜不到的那一段：历史末尾之后的（也就是当前屏）。
      if (l.n < total.value) continue
      if (backward ? l.n > from : l.n < from) continue
      const text = lineText(l)
      const col = text.toLowerCase().indexOf(needle)
      if (col >= 0) hits.push({ n: l.n, col, text })
    }
    return backward ? hits.reverse() : hits
  }

  async function search(q: string, from: number, backward: boolean): Promise<HistoryMatch[]> {
    const id = sessionId()
    if (!id || !q) return []
    const onScreen = searchLoaded(q, from, backward)
    const qs = new URLSearchParams({ q, from: String(from), limit: '100' })
    if (backward) qs.set('back', '1')
    try {
      const res = await doFetch(cliApi(`/sessions/${id}/history/search?${qs}`))
      if (!res.ok) {
        error.value = `HTTP ${res.status}`
        return onScreen // 服务端那半边不可用，屏幕上这半边照样是真答案
      }
      const data = await res.json()
      error.value = ''
      if (data.enabled === false) {
        enabled.value = false
        return onScreen
      }
      const fromServer = Array.isArray(data.matches) ? (data.matches as HistoryMatch[]) : []
      return mergeMatches(fromServer, onScreen, backward)
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      // 服务端那半边挂了，屏幕上这半边照样是真答案——比返回空好。
      return onScreen
    }
  }

  return { lines, styles, base, total, enabled, broken, reason, loading, error, open, loadOlder, search, ensureVisible, reset }
}

/**
 * 样式 → 内联 CSS。
 *
 * 调色板下标交给 xterm 的主题变量（`--dw-ansi-N`），**不在这里硬编码颜色** —— 复制模式的配色必须
 * 和它上面那个终端一致，两处各写一份就是漂移的起点。变量在 CopyModeView 里按 xterm 的主题定义一次。
 */
export function styleToCss(st: HistoryStyle | undefined): string {
  if (!st) return ''
  const out: string[] = []
  const color = (v: string | undefined): string => {
    if (!v) return ''
    if (v.startsWith('i')) return `var(--dw-ansi-${v.slice(1)}, inherit)`
    return v
  }
  const fg = color(st.fg)
  const bg = color(st.bg)
  const a = st.a ?? 0
  // inverse 在这里就地换掉前后景，而不是留给 CSS filter：filter 会连子元素一起翻，而且和背景色
  // 叠加后颜色不可预测。
  if (a & ATTR_INVERSE) {
    out.push(`color:${bg || 'var(--dw-term-bg, #1e1e1e)'}`)
    out.push(`background:${fg || 'var(--dw-term-fg, #d4d4d4)'}`)
  } else {
    if (fg) out.push(`color:${fg}`)
    if (bg) out.push(`background:${bg}`)
  }
  if (a & ATTR_BOLD) out.push('font-weight:700')
  if (a & ATTR_DIM) out.push('opacity:.65')
  if (a & ATTR_ITALIC) out.push('font-style:italic')
  if (a & ATTR_STRIKE) out.push('text-decoration:line-through')
  else if (a & ATTR_UNDERLINE) out.push('text-decoration:underline')
  if (a & ATTR_HIDDEN) out.push('visibility:hidden')
  return out.join(';')
}

/**
 * 把服务端命中（历史）和客户端命中（当前屏）合成一条按行号有序、无重复的列表。
 *
 * 方向决定顺序：往回找时新的在前，往后找时旧的在前 —— 「下一个匹配」读起来才是对的。
 */
export function mergeMatches(
  fromServer: HistoryMatch[],
  onScreen: HistoryMatch[],
  backward: boolean,
): HistoryMatch[] {
  const seen = new Set<number>()
  const all: HistoryMatch[] = []
  for (const m of fromServer.concat(onScreen)) {
    if (seen.has(m.n)) continue
    seen.add(m.n)
    all.push(m)
  }
  all.sort((a, b) => (backward ? b.n - a.n : a.n - b.n))
  return all
}

/** 一行的纯文本（搜索高亮、复制、测试都用它）。 */
export function lineText(l: HistoryLine): string {
  let s = ''
  for (const seg of l.seg) s += seg.t
  return s
}
