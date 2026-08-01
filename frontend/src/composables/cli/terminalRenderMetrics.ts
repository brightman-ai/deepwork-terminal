/**
 * terminalRenderMetrics — what does the BROWSER do with the bytes, and how long does it take?
 *
 * ── Why this exists ──────────────────────────────────────────────────────────────────────────
 * The server side of a tmux window switch was measured end to end (scripts/diag/switchlat): 3ms to
 * the WebSocket, indistinguishable from a native `tmux attach`, with fewer bytes. Everything a user
 * experiences as lag therefore happens after the frame arrives — parse, render, paint — and none of
 * that was observable. Two rounds of server-side optimisation were real improvements aimed at the
 * wrong half of the pipeline, because the wrong half was the only half with instrumentation.
 *
 * So: a per-terminal summary of the client-side cost, sampled from the paths that already run.
 *
 *   frames / bytes        — what arrived.
 *   parse                 — xterm.write() → its own callback: the parser's cost for that frame.
 *   render                — write() → the next onRender: parse plus the actual repaint.
 *   forcedRepaints        — how often a full-grid refresh() was forced on top of xterm's own
 *                           damage tracking (see XtermTerminal's render-sync). This is the number
 *                           worth watching: it costs a whole-screen repaint and its trigger fires
 *                           on almost any output, including a bare erase-line.
 *
 * Percentiles, not averages: a terminal that is fine 90% of the time and stalls 200ms on every
 * repaint reads as "fine" on an average and as broken to the person using it.
 *
 * And percentiles are not enough on their own. P95 describes the BULK; stutter lives in the tail.
 * Over a 15s window of 200 frames, one 800ms freeze sits around P99.5 and is invisible at P95 —
 * yet it is the only frame the user actually felt. So the summary also carries the worst frame and
 * HOW MANY crossed the "you would feel that" line: magnitude alone can't tell a one-off GC pause
 * from real stutter, and a count alone can't tell 110ms from 900ms. Two numbers, one question.
 *
 * Cost of measuring: two timestamps and an array push per frame, and one report per interval —
 * deliberately cheaper than the thing it measures, and silent when the terminal is idle.
 */

/** How often a summary is emitted. Long enough that the report is never itself the load. */
const REPORT_INTERVAL_MS = 15_000

/**
 * A repaint past this is a hitch you can feel, not a slow frame you can't.
 *
 * 16.7ms is one frame at 60Hz — too tight to be interesting here, since a terminal paints on byte
 * arrival, not on a display clock. 100ms is the long-standing "feels instantaneous" boundary, and
 * it sits comfortably above what this app measures in normal operation (P95 was 6ms on an idle
 * workbench and 59ms on a busy one), so crossing it means something actually happened.
 */
export const SLOW_FRAME_MS = 100

/** Samples kept per window. Beyond this the oldest are dropped — percentiles stay representative
 *  without the array growing without bound during a flood. */
const MAX_SAMPLES = 400

export interface RenderMetricsSummary {
  frames: number
  bytes: number
  forcedRepaints: number
  parseP50: number
  parseP95: number
  parseMax: number
  renderP50: number
  renderP95: number
  renderMax: number
  /** Frames whose repaint crossed SLOW_FRAME_MS — the frequency half of "is it stuttering". */
  renderSlow: number
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0
  const idx = Math.min(sorted.length - 1, Math.floor(sorted.length * p))
  return Math.round(sorted[idx] * 10) / 10
}

export function summarize(
  frames: number,
  bytes: number,
  forcedRepaints: number,
  parse: number[],
  render: number[],
): RenderMetricsSummary {
  const p = [...parse].sort((a, b) => a - b)
  const r = [...render].sort((a, b) => a - b)
  return {
    frames,
    bytes,
    forcedRepaints,
    parseP50: percentile(p, 0.5),
    parseP95: percentile(p, 0.95),
    parseMax: p.length ? Math.round(p[p.length - 1] * 10) / 10 : 0,
    renderP50: percentile(r, 0.5),
    renderP95: percentile(r, 0.95),
    renderMax: r.length ? Math.round(r[r.length - 1] * 10) / 10 : 0,
    renderSlow: r.reduce((n, ms) => (ms > SLOW_FRAME_MS ? n + 1 : n), 0),
  }
}

export interface RenderMetrics {
  /** One arriving frame: its size, and how long the parser took.
   *  parseMs 省略 = 这一帧的耗时样本不可信（页面在后台 / 终端没有布局盒子，见 renderSampleGate）。
   *  **帧数与字节数照记** —— 那是真实流量，与可见性无关；丢掉它们会让流量统计凭空缺一块。 */
  noteFrame(bytes: number, parseMs?: number): void
  /** write() → repaint complete, for the frame that triggered it. */
  noteRender(renderMs: number): void
  /** A full-grid refresh() was forced (not xterm's own damage-driven repaint). */
  noteForcedRepaint(): void
  /** Stop reporting; call on teardown. */
  dispose(): void
}

/**
 * Starts a reporting loop. `report` is called at most once per interval and ONLY when frames
 * actually arrived — an idle terminal stays silent rather than emitting rows of zeroes.
 */
export function createRenderMetrics(report: (summary: RenderMetricsSummary) => void): RenderMetrics {
  let frames = 0
  let bytes = 0
  let forcedRepaints = 0
  let parse: number[] = []
  let render: number[] = []

  const push = (arr: number[], v: number) => {
    arr.push(v)
    if (arr.length > MAX_SAMPLES) arr.splice(0, arr.length - MAX_SAMPLES)
  }

  const timer = setInterval(() => {
    if (frames === 0) return
    report(summarize(frames, bytes, forcedRepaints, parse, render))
    frames = 0
    bytes = 0
    forcedRepaints = 0
    parse = []
    render = []
  }, REPORT_INTERVAL_MS)

  return {
    noteFrame(b, parseMs) {
      frames++
      bytes += b
      if (parseMs !== undefined) push(parse, parseMs)
    },
    noteRender(renderMs) {
      push(render, renderMs)
    },
    noteForcedRepaint() {
      forcedRepaints++
    },
    dispose() {
      clearInterval(timer)
    },
  }
}
