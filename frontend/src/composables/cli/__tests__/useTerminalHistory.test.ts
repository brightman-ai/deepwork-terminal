import { describe, it, expect, afterEach } from 'bun:test'
import {
  useTerminalHistory,
  styleToCss,
  lineText,
  HISTORY_PAGE,
  ATTR_BOLD,
  ATTR_INVERSE,
  ATTR_UNDERLINE,
  mergeMatches,
  type HistoryLine,
} from '../useTerminalHistory'

/**
 * 回看历史的数据层。三件事值得锁死，因为错了都不会报错、只会安静地给出错的画面：
 *
 *   ① 分页**合并**（不是替换）且按行号有序无洞 —— 否则往上翻会把你正在看的内容顶掉；
 *   ② 样式表**增量追加**（只取自己没有的那一段）—— 它依赖"id 只追加不复用"这条不变量，
 *      一旦改成整表替换，历史里旧行的 id 就会指向别的颜色；
 *   ③ 请求失败时**不清空已有历史** —— 一次网络抖动把翻了半天的历史清掉，比少一页糟得多。
 */

// 注入式取数：组合式不自己拼 fetch，所以测试里直接给它一个假的——同时也就没有 DOM 依赖。
// （真实调用方注入的是 useCliAuth().cliFetch，认证的唯一出口。）
let currentFetch: (path: string) => Promise<Response> = async () => {
  throw new Error('stubFetch not installed')
}
const doFetch = (path: string) => currentFetch(path)
const realFetch = globalThis.fetch
afterEach(() => { globalThis.fetch = realFetch })

interface StubPage {
  lines?: Array<{ n: number; seg: Array<{ t: string; s: number }> }>
  base?: number
  total?: number
  styles?: Array<{ fg?: string; bg?: string; a?: number }>
  stylesTotal?: number
  enabled?: boolean
  broken?: boolean
  reason?: string
}

/** 按 URL 依次返回预设响应，并记录每次请求的查询参数。 */
function stubFetch(pages: StubPage[] | ((url: URL) => StubPage | null)) {
  const calls: URL[] = []
  let i = 0
  currentFetch = (async (input: string) => {
    const url = new URL(String(input), 'http://x')
    calls.push(url)
    const page = typeof pages === 'function' ? pages(url) : pages[Math.min(i++, pages.length - 1)]
    if (page === null) return { ok: false, status: 500, json: async () => ({}) } as Response
    return {
      ok: true,
      status: 200,
      json: async () => ({
        enabled: page.enabled ?? true,
        broken: page.broken ?? false,
        lines: page.lines ?? [],
        base: page.base ?? 0,
        total: page.total ?? 0,
        styles: page.styles ?? [],
        stylesTotal: page.stylesTotal ?? 0,
        reason: page.reason,
      }),
    } as unknown as Response
  }) as unknown as (path: string) => Promise<Response>
  return calls
}

function line(n: number, text = `line ${n}`): { n: number; seg: Array<{ t: string; s: number }> } {
  return { n, seg: [{ t: text, s: 0 }] }
}

describe('select through end after jumping to an older search result', () => {
  it('fills every intervening page and includes the current screen', async () => {
    stubFetch(url => {
      if (url.searchParams.get('screen') === '1') return { lines: [line(1200, 'screen end')], total: 1200 }
      const from = Number(url.searchParams.get('from'))
      const count = Number(url.searchParams.get('count'))
      return { lines: Array.from({ length: count }, (_, i) => line(from + i)), total: 1200 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    h.lines.value = [line(10), line(11)]
    h.total.value = 1200
    expect(await h.loadToEnd()).toBe(true)
    expect(h.lines.value).toHaveLength(1191)
    expect(h.lines.value[0].n).toBe(10)
    expect(h.lines.value[490].n).toBe(500)
    expect(lineText(h.lines.value.at(-1)!)).toBe('screen end')
  })
  it('keeps existing lines and reports failure when the middle page is unavailable', async () => {
    stubFetch(url => url.searchParams.has('screen') ? { lines: [line(900)], total: 900 } : null)
    const h = useTerminalHistory(() => 's1', doFetch)
    h.lines.value = [line(10)]
    h.total.value = 900
    expect(await h.loadToEnd()).toBe(false)
    expect(h.lines.value).toEqual([line(10)])
    expect(h.error.value).toBe('HTTP 500')
    expect(h.loading.value).toBe(false)
  })
  it('does not report success after eviction leaves a hole', async () => {
    stubFetch(url => url.searchParams.has('screen')
      ? { lines: [line(900)], total: 900 }
      : { lines: [line(850)], total: 900, base: 850 })
    const h = useTerminalHistory(() => 's1', doFetch)
    h.lines.value = [line(10)]
    h.total.value = 900
    expect(await h.loadToEnd()).toBe(false)
    expect(h.error.value).toContain('被淘汰')
    expect(h.lines.value).toEqual([line(10)])
  })
})

describe('useTerminalHistory — 分页', () => {
  it('open 取的是【最近】一页，不是从第 0 行开始', async () => {
    // 使用者是从实时终端切过来的：他要看的是刚刚滚过去的东西，不是这个会话开机时的第一行。
    const calls = stubFetch((url) => {
      if (url.searchParams.get('screen') === '1') return { lines: [line(9000)], total: 9000, base: 0 }
      if (url.searchParams.get('count') === '1') return { lines: [], total: 9000, base: 0 }
      return { lines: [line(8600), line(8601)], total: 9000, base: 0 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    const page = calls.find((u) => u.searchParams.get('count') === String(HISTORY_PAGE))
    expect(page).toBeDefined()
    expect(Number(page!.searchParams.get('from'))).toBe(9000 - HISTORY_PAGE)
  })

  it('loadOlder 把更早的一页【并】进来，按行号有序，不丢已有的', async () => {
    stubFetch((url) => {
      if (url.searchParams.get('screen') === '1') return { lines: [], total: 1000, base: 0 }
      if (url.searchParams.get('count') === '1') return { lines: [], total: 1000, base: 0 }
      const from = Number(url.searchParams.get('from'))
      return { lines: [line(from), line(from + 1)], total: 1000, base: 0 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    const before = h.lines.value.map((l) => l.n)
    const added = await h.loadOlder()
    expect(added).toBeGreaterThan(0)
    const after = h.lines.value.map((l) => l.n)
    // 已有的一条都不许少 —— 这是"替换"和"合并"的分水岭。
    for (const n of before) expect(after).toContain(n)
    expect(after).toEqual([...after].sort((a, b) => a - b))
    expect(new Set(after).size).toBe(after.length) // 无重复
  })

  it('已经到最早一行时 loadOlder 是 0，不再发请求', async () => {
    const calls = stubFetch((url) => {
      if (url.searchParams.get('count') === '1') return { lines: [], total: 100, base: 90 }
      if (url.searchParams.get('screen') === '1') return { lines: [], total: 100, base: 90 }
      return { lines: [line(90)], total: 100, base: 90 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    const n = calls.length
    expect(await h.loadOlder()).toBe(0)
    expect(calls.length).toBe(n) // 到头了就不该再问
  })
})

describe('useTerminalHistory — 样式表增量', () => {
  it('第二次请求带上【已有条数】，只收新增的，且追加而非替换', async () => {
    // 服务端那张表是固定的；每次只回客户端还没有的那一段——这正是真实行为。
    const table = [{}, { fg: 'i1' }, { fg: 'i2' }]
    const calls = stubFetch((url) => {
      const have = Number(url.searchParams.get('styles') ?? '0')
      return {
        lines: [line(1)],
        total: 10,
        styles: table.slice(have),
        stylesTotal: table.length,
      }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()

    // 收敛到整张表，且【不重复】——重复就说明"追加"被写成了"每页拖一遍整表"。
    expect(h.styles.value.length).toBe(table.length)
    // id 只追加不复用，所以旧 id 的含义永远不变——这正是增量传输安全的那条不变量。
    expect(h.styles.value[1]).toEqual({ fg: 'i1' })
    expect(h.styles.value[2]).toEqual({ fg: 'i2' })

    // 第一次之后，每次都带上"我已经有几条"。
    const asked = calls.map((u) => Number(u.searchParams.get('styles') ?? '0'))
    expect(asked[0]).toBe(0)
    expect(asked.slice(1).every((n) => n > 0)).toBe(true)
    await h.ensureVisible(999)
    expect(h.styles.value.length).toBe(table.length) // 再取几页也不会长胖
  })
})

describe('useTerminalHistory — 失败与降级', () => {
  it('请求失败时保留已有历史，只记 error', async () => {
    let fail = false
    stubFetch((url) => {
      if (fail) return null
      if (url.searchParams.get('screen') === '1') return { lines: [], total: 500, base: 0 }
      if (url.searchParams.get('count') === '1') return { lines: [], total: 500, base: 0 }
      return { lines: [line(100), line(101)], total: 500, base: 0 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    expect(h.lines.value.length).toBe(2)

    fail = true
    await h.loadOlder()
    // 一次网络抖动不能把翻了半天的历史清掉。
    expect(h.lines.value.length).toBe(2)
    expect(h.error.value).not.toBe('')
  })

  it('daemon 太旧时如实说明原因，而不是装成"没有历史"', async () => {
    stubFetch([{ enabled: false, reason: 'daemon-too-old', lines: [], total: 0, base: 0 }])
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    expect(h.enabled.value).toBe(false)
    // 两者的补救办法完全不同（一个要重启常驻进程，一个只要等），UI 必须分得清。
    expect(h.reason.value).toBe('daemon-too-old')
  })

  it('没有 sessionId 时安静地什么都不做', async () => {
    const calls = stubFetch([{}])
    const h = useTerminalHistory(() => undefined, doFetch)
    await h.open()
    expect(calls.length).toBe(0)
  })

  it('reset 把状态清干净（切标签后不能残留上一个终端的历史）', async () => {
    stubFetch((url) => (url.searchParams.get('count') === '1'
      ? { lines: [], total: 10, base: 0 }
      : { lines: [line(1)], total: 10, base: 0, styles: [{}], stylesTotal: 1 }))
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    expect(h.lines.value.length).toBeGreaterThan(0)
    h.reset()
    expect(h.lines.value).toEqual([])
    expect(h.styles.value).toEqual([])
    expect(h.total.value).toBe(0)
    expect(h.enabled.value).toBe(true)
  })
})

describe('styleToCss', () => {
  it('默认样式不产生任何 CSS（大多数行走这条路）', () => {
    expect(styleToCss(undefined)).toBe('')
    expect(styleToCss({})).toBe('')
  })

  it('调色板下标解析成 CSS 变量，不写死颜色', () => {
    // 写死颜色 = 把今天的主题烤进比它活得久的历史里，而且必然和它上面那个终端漂移。
    const css = styleToCss({ fg: 'i1' })
    expect(css).toContain('var(--dw-ansi-1')
    expect(css).not.toContain('#')
  })

  it('真彩色原样使用', () => {
    expect(styleToCss({ fg: '#0ab0ff' })).toContain('#0ab0ff')
  })

  it('属性位逐条映射', () => {
    expect(styleToCss({ a: ATTR_BOLD })).toContain('font-weight:700')
    expect(styleToCss({ a: ATTR_UNDERLINE })).toContain('underline')
  })

  it('inverse 就地对调前后景，而不是留给 filter', () => {
    // filter 会连子元素一起翻，且与背景叠加后颜色不可预测。
    const css = styleToCss({ fg: 'i1', bg: 'i4', a: ATTR_INVERSE })
    expect(css).toContain('color:var(--dw-ansi-4')
    expect(css).toContain('background:var(--dw-ansi-1')
  })
})

describe('lineText', () => {
  it('把分段拼回整行（搜索高亮/复制/测试都靠它）', () => {
    const l: HistoryLine = { n: 1, seg: [{ t: '中文', s: 2 }, { t: 'x', s: 0 }] }
    expect(lineText(l)).toBe('中文x')
  })
  it('空行是空串，不是 undefined', () => {
    expect(lineText({ n: 1, seg: [] })).toBe('')
  })
})

/**
 * 搜索必须同时覆盖【历史】和【当前可见屏】。
 *
 * 真机上第一次搜就撞到了：搜一个此刻明明就在屏幕上的词，返回"无匹配"—— 因为服务端只搜它存的那份
 * 历史，而当前屏还没滚出去、不在里面。视图显示的是两段拼起来的东西，搜索却只覆盖前一段。
 */
describe('搜索覆盖历史 + 当前可见屏', () => {
  it('屏幕上的命中也算数（服务端搜不到它）', async () => {
    stubFetch((url) => {
      if (url.pathname.includes('/search')) return { lines: [], total: 100, base: 0 }
      if (url.searchParams.get('screen') === '1') {
        return { lines: [{ n: 100, seg: [{ t: 'MARKER on the live screen', s: 0 }] }], total: 100, base: 0 }
      }
      return { lines: [line(50)], total: 100, base: 0 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    const hits = await h.search('MARKER', 100, true)
    expect(hits.length).toBe(1)
    expect(hits[0].n).toBe(100)
    expect(hits[0].col).toBe(0)
  })

  it('服务端搜挂了，屏幕上那半边照样是真答案', async () => {
    stubFetch((url) => {
      if (url.pathname.includes('/search')) return null // 500
      if (url.searchParams.get('screen') === '1') {
        return { lines: [{ n: 10, seg: [{ t: 'still findable', s: 0 }] }], total: 10, base: 0 }
      }
      return { lines: [], total: 10, base: 0 }
    })
    const h = useTerminalHistory(() => 's1', doFetch)
    await h.open()
    const hits = await h.search('findable', 10, true)
    expect(hits.length).toBe(1)
  })
})

describe('mergeMatches', () => {
  it('去重并按方向排序', () => {
    const srv = [{ n: 5, col: 0, text: 'a' }, { n: 9, col: 0, text: 'b' }]
    const scr = [{ n: 9, col: 0, text: 'b' }, { n: 12, col: 0, text: 'c' }]
    expect(mergeMatches(srv, scr, false).map((m) => m.n)).toEqual([5, 9, 12])
    // 往回找时新的在前——「下一个匹配」读起来才对。
    expect(mergeMatches(srv, scr, true).map((m) => m.n)).toEqual([12, 9, 5])
  })
})
