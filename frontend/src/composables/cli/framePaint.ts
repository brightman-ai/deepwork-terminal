/**
 * framePaint — 「这次写入的结果已经画到屏幕上了」的时刻。
 *
 * xterm 的 write 回调只说明**解析**完了；画出来由 rAF 驱动的渲染另走一步。恢复遮罩和延迟探针
 * 都需要后一个时刻（REQ-TS-16 / REQ-TS-17），所以统一从这里取，而不是各写一份"回调里再套个 rAF"。
 *
 * 为什么 rAF 排在 xterm 的渲染之后：xterm 在解析过程中（回调之前）就已经为这次变化排了自己的 rAF，
 * 我们在回调里才排；同一帧里 rAF 回调按排队顺序执行，所以我们的回调一定在 xterm 画完之后跑。
 *
 * 为什么要有兜底定时器：页面在后台、或内嵌 webview 里 rAF 会被挂起/节流，等不到的话遮罩会一直盖着。
 * 兜底触发时回调收到 `viaFallback=true`——调用方自己决定这个时刻还值不值得信（延迟探针会丢掉该样本，
 * 遮罩则直接放行）。
 */

export interface FrameScheduler {
  raf(cb: () => void): number
  cancelRaf(id: number): void
  setTimeout(cb: () => void, ms: number): unknown
  clearTimeout(id: unknown): void
}

export const browserFrameScheduler: FrameScheduler = {
  raf: (cb) => requestAnimationFrame(() => cb()),
  cancelRaf: (id) => cancelAnimationFrame(id),
  setTimeout: (cb, ms) => setTimeout(cb, ms),
  clearTimeout: (id) => clearTimeout(id as ReturnType<typeof setTimeout>),
}

/**
 * 在下一帧（xterm 已经画完）之后调用 cb；rAF 在 fallbackMs 内没来则由定时器兜底。cb 恰好调用一次。
 * 返回取消函数。
 */
export function afterNextPaint(
  sched: FrameScheduler,
  cb: (viaFallback: boolean) => void,
  fallbackMs: number,
): () => void {
  let done = false
  let rafId = 0
  let timerId: unknown
  const finish = (viaFallback: boolean) => {
    if (done) return
    done = true
    sched.cancelRaf(rafId)
    sched.clearTimeout(timerId)
    cb(viaFallback)
  }
  rafId = sched.raf(() => finish(false))
  timerId = sched.setTimeout(() => finish(true), fallbackMs)
  return () => {
    if (done) return
    done = true
    sched.cancelRaf(rafId)
    sched.clearTimeout(timerId)
  }
}
