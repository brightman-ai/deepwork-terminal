/**
 * latencyProbe — 体验延迟：从按键/发送，到它的回显**画到屏幕上**（REQ-TS-17）。
 *
 * ## 它修的是什么
 *
 * 右上角的延迟只是心跳往返：WS 来回一趟。Human 报"发送要 4–6s，右上角却只显示几十到 100ms"——
 * 心跳走的是服务端一条独立通道，不经过 PTY、不经过浏览器解析和绘制，所以它在终端卡住的时候照样
 * 很漂亮。这里量的是使用者真正等的那段，并拆成三段让人知道卡在哪：
 *
 *     发送 ──网络+程序──▶ 回显帧到达 ──浏览器解析──▶ write 回调 ──绘制──▶ 下一帧(xterm 已画完)
 *
 * ## 配对规则（刻意简单，局限写在下面）
 *
 *  · 同一时刻只有**一个**待回显的探针 = 最早一次尚未被回应的发送。之后又发的字节并入它——这样
 *    粘贴的几十个分片、滚轮循环、快速连敲都只算"一次等待"，不会灌满样本窗口。
 *  · 发送之后到达的**第一个输出帧**就是它的回显。恢复帧（replay_reset 之后那一帧）不是回显，
 *    会作废待回显的探针（回显若有，已被并进恢复的屏幕里）。
 *  · 只有"预期有回显"的输入才开探针（expectsEcho）：方向键、鼠标上报、焦点事件、终端的自动应答
 *    （CPR/DA/OSC 颜色）这些往往没有可见变化，会把一个本来正常的终端量成"卡 10 秒"。
 *  · 样本在发送→画出的整段时间里页面必须一直可见（见 renderSampleGate 的同类理由）：后台页的
 *    rAF 被挂起，量出来的是"你走开了多久"，不是延迟。这类样本丢弃、不截断。
 *
 * ## 已知局限（不是实现缺陷，是这个度量的边界）
 *
 *  · 输出本来就在持续流动时（agent 的转圈动画），发送后的第一帧可能是无关的动画帧，会**低估**。
 *  · "第一帧"量的是终端**开始**响应，不是响应**结束**：一个先回显光标、几秒后才重画整屏的 TUI，
 *    这里只看得到前者。
 *  · 超过 LATENCY_MAX_WAIT_MS 仍无输出的等待无法区分"卡死"与"这个键本来就没回显"，不记样本。
 */

export const LATENCY_WINDOW = 20
export const LATENCY_MAX_WAIT_MS = 15_000
/** 样本的保鲜期：停手超过这么久，指示回到纯网络往返，不再拿旧样本说话。 */
export const LATENCY_FRESH_MS = 120_000
/** p95 超过它就是"端到端明显慢"，指示变色。 */
export const LATENCY_SLOW_P95_MS = 1_000
export const LATENCY_REPORT_EVERY_MS = 60_000
/** 刚变慢时可以提前上报，但两次上报至少隔这么久。 */
export const LATENCY_REPORT_MIN_GAP_MS = 10_000

/**
 * 这次输入预期会让屏幕有可见回应吗？去掉转义序列（CSI/SS3/OSC/DCS…）后，还剩下可打印字符、
 * 回车或退格才算。
 */
export function expectsEcho(data: Uint8Array): boolean {
  const n = data.length
  let i = 0
  while (i < n) {
    const b = data[i]
    if (b === 0x1b) {
      const next = data[i + 1]
      if (next === 0x5b) {
        // CSI：ESC [ 参数… 终结字节(0x40–0x7e)。粘贴括号 ESC[200~ 也在此列，正文在它后面。
        let j = i + 2
        while (j < n && !(data[j] >= 0x40 && data[j] <= 0x7e)) j++
        i = j + 1
      } else if (next === 0x4f) {
        i += 3 // SS3：ESC O x
      } else if (next === 0x5d || next === 0x50 || next === 0x58 || next === 0x5e || next === 0x5f) {
        // OSC / DCS / SOS / PM / APC：到 BEL 或 ST(ESC \) 为止
        let j = i + 2
        while (j < n && data[j] !== 0x07 && !(data[j] === 0x1b && data[j + 1] === 0x5c)) j++
        i = data[j] === 0x07 ? j + 1 : j + 2
      } else {
        i += 2 // ESC x（Alt+键、单独的 Esc）
      }
      continue
    }
    if (b === 0x0d || b === 0x7f || b >= 0x20) return true
    i++
  }
  return false
}

export interface LatencySample {
  /** 画出的时刻（performance.now）。 */
  at: number
  total: number
  /** 发送 → 回显帧到达：网络 + 程序（+ 服务端）。 */
  net: number
  /** 回显帧到达 → xterm 解析完。 */
  parse: number
  /** 解析完 → 下一帧（画出）。 */
  paint: number
}

export interface LatencySegments { total: number; net: number; parse: number; paint: number }

export interface LatencySummary {
  n: number
  /**
   * p50 样本的分段。取**总时长居中的那一条真实样本**，而不是三段各自取中位数：后者加起来不等于
   * 总数，悬停里一眼读不通。
   */
  p50: LatencySegments
  p95Total: number
  maxTotal: number
  slow: boolean
}

export interface LatencyReport extends LatencySummary {
  /** 自上次上报以来新增的样本数。 */
  newSamples: number
  /** 因后台/不可见而丢弃的样本数（累计）。 */
  discarded: number
  /** 超过 LATENCY_MAX_WAIT_MS 仍无输出的发送数（累计）。 */
  unanswered: number
}

function percentile(sortedAsc: number[], p: number): number {
  const rank = Math.ceil(p * sortedAsc.length)
  return sortedAsc[Math.min(sortedAsc.length - 1, Math.max(0, rank - 1))]
}

interface Pending { sentAt: number; epoch: number }
interface InFlight { sentAt: number; epoch: number; arrivedAt: number; parsedAt: number | null }

export function createLatencyTracker() {
  let pending: Pending | null = null
  const inflight = new Map<number, InFlight>()
  let nextToken = 1
  let samples: LatencySample[] = []
  let newSamples = 0
  let discarded = 0
  let unanswered = 0
  let lastReportAt: number | null = null
  let lastReportedSlow = false

  function fresh(now: number): LatencySample[] {
    return samples.filter(s => now - s.at <= LATENCY_FRESH_MS)
  }

  function summarize(list: LatencySample[]): LatencySummary | null {
    if (list.length === 0) return null
    const byTotal = [...list].sort((a, b) => a.total - b.total)
    const totals = byTotal.map(s => s.total)
    const median = byTotal[Math.floor((byTotal.length - 1) / 2)]
    const p95Total = percentile(totals, 0.95)
    return {
      n: list.length,
      p50: { total: median.total, net: median.net, parse: median.parse, paint: median.paint },
      p95Total,
      maxTotal: totals[totals.length - 1],
      slow: p95Total > LATENCY_SLOW_P95_MS,
    }
  }

  return {
    /** 一次预期有回显的发送。`epoch` 是发送时的可见性纪元。 */
    noteSend(now: number, epoch: number): void {
      if (pending && now - pending.sentAt > LATENCY_MAX_WAIT_MS) {
        unanswered++
        pending = null
      }
      if (pending) return // 并入最早那次等待
      pending = { sentAt: now, epoch }
    },

    /**
     * 一个输出帧到达。返回要在解析/画出时回报的令牌；这一帧不是任何探针的回显则返回 null。
     * `kind='restore'`：恢复帧，作废待回显的探针。
     */
    noteOutput(now: number, kind: 'live' | 'restore'): number | null {
      if (!pending) return null
      if (kind === 'restore') {
        pending = null
        return null
      }
      const p = pending
      pending = null
      if (now - p.sentAt > LATENCY_MAX_WAIT_MS) {
        unanswered++
        return null
      }
      const token = nextToken++
      inflight.set(token, { sentAt: p.sentAt, epoch: p.epoch, arrivedAt: now, parsedAt: null })
      if (inflight.size > 8) inflight.delete(inflight.keys().next().value as number)
      return token
    },

    noteParsed(token: number, now: number): void {
      const f = inflight.get(token)
      if (f) f.parsedAt = now
    },

    /** 画出了。`epoch/hidden` 是此刻的可见性纪元与页面是否隐藏；与发送时不一致 = 样本不可信。 */
    notePainted(token: number, now: number, epoch: number, hidden: boolean): void {
      const f = inflight.get(token)
      inflight.delete(token)
      if (!f || f.parsedAt === null) return
      if (hidden || epoch !== f.epoch) {
        discarded++
        return
      }
      samples.push({
        at: now,
        total: now - f.sentAt,
        net: f.arrivedAt - f.sentAt,
        parse: f.parsedAt - f.arrivedAt,
        paint: now - f.parsedAt,
      })
      if (samples.length > LATENCY_WINDOW) samples = samples.slice(-LATENCY_WINDOW)
      newSamples++
    },

    /** 连接断了/重连了：旧连接的样本说明不了新连接。 */
    reset(): void {
      pending = null
      inflight.clear()
      samples = []
      newSamples = 0
    },

    summary(now: number): LatencySummary | null {
      return summarize(fresh(now))
    },

    /**
     * 周期性上报（诊断）：有新样本，且距上次上报满一个周期；或刚变慢且距上次满最小间隔。
     * 返回 null = 此刻不该报。第一次调用只定锚点。
     */
    takeReport(now: number): LatencyReport | null {
      if (lastReportAt === null) lastReportAt = now
      if (newSamples === 0) return null
      const s = summarize(samples)
      if (!s) return null
      const gap = now - lastReportAt
      const due = gap >= LATENCY_REPORT_EVERY_MS
      const earlySlow = s.slow && !lastReportedSlow && gap >= LATENCY_REPORT_MIN_GAP_MS
      if (!due && !earlySlow) return null
      const report: LatencyReport = { ...s, newSamples, discarded, unanswered }
      lastReportAt = now
      lastReportedSlow = s.slow
      newSamples = 0
      return report
    },
  }
}

export type LatencyTracker = ReturnType<typeof createLatencyTracker>

export function formatMs(ms: number): string {
  if (ms >= 10_000) return `${Math.round(ms / 1000)}s`
  if (ms >= 1_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.round(ms)}ms`
}

export function sameSummary(a: LatencySummary | null, b: LatencySummary | null): boolean {
  if (a === b) return true
  if (!a || !b) return false
  return a.n === b.n && a.slow === b.slow && a.p95Total === b.p95Total && a.maxTotal === b.maxTotal
    && a.p50.total === b.p50.total && a.p50.net === b.p50.net
    && a.p50.parse === b.p50.parse && a.p50.paint === b.p50.paint
}

export interface LatencyView {
  /** 右上角指示的数字（ms，整数；0 = 不显示）。有体验样本用 p50 总时长，否则退回纯网络往返。 */
  chipRtt: number
  slow: boolean
  /** 悬停第一行。 */
  label: string
  /** 悬停其余各行。 */
  lines: string[]
}

/** 把摘要 + 心跳 RTT 说成人话（悬停内容）。纯函数，便于测试"读得懂"。 */
export function latencyView(summary: LatencySummary | null, networkRtt: number): LatencyView {
  const net = networkRtt > 0 ? formatMs(networkRtt) : '—'
  if (!summary) {
    return {
      chipRtt: Math.max(0, Math.round(networkRtt)),
      slow: false,
      label: networkRtt > 0 ? `网络往返 ${net}` : '延迟',
      lines: ['还没有输入样本；打字或发送后，这里改为显示：从按键到回显画到屏幕上的时间'],
    }
  }
  const { p50 } = summary
  return {
    chipRtt: Math.max(1, Math.round(p50.total)),
    slow: summary.slow,
    label: `体验延迟 ${formatMs(p50.total)}（近 ${summary.n} 次输入的中位）`,
    lines: [
      `网络+程序 ${formatMs(p50.net)}`,
      `浏览器解析 ${formatMs(p50.parse)}`,
      `绘制 ${formatMs(p50.paint)}`,
      `纯网络往返 ${net}（心跳，不含程序处理）`,
      summary.slow
        ? `p95 ${formatMs(summary.p95Total)}：端到端明显偏慢`
        : `p95 ${formatMs(summary.p95Total)}`,
    ],
  }
}
