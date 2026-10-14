/**
 * outputFrames — 服务端来的一帧，怎么走完「写给终端 → 解析 → 画出」，并把这两个时刻报给
 * 恢复遮罩（REQ-TS-16）和延迟探针（REQ-TS-17）。
 *
 * 这里是两个纯状态机（restoreGate / latencyProbe）与真实 xterm 之间唯一的接缝，抽成函数是为了能用
 * 假的 write / 假的帧调度器把"帧到达 → 写入回调 → 下一帧"这条时序整个跑一遍并断言每个时刻
 * 遮罩与样本的状态——那正是这两条需求的验收口径。
 */

import { afterNextPaint, type FrameScheduler } from './framePaint'
import { RESTORE_PAINT_FALLBACK_MS, prefixBytes, type RestoreGate } from './restoreGate'
import type { LatencyTracker } from './latencyProbe'

export interface OutputFrameDeps {
  gate: RestoreGate
  latency: LatencyTracker
  /** 写给终端；`onParsed` 在 xterm 解析完这次数据时调用（不是画出）。 */
  write(data: Uint8Array, onParsed?: () => void): void
  sched: FrameScheduler
  now(): number
  visibilityEpoch(): number
  hidden(): boolean
  /** 被延后的 replay_reset 要写的清屏序列。 */
  resetBytes: Uint8Array
  /** 一个延迟样本落地（画出）后调用，用来刷新显示。 */
  onLatencySample?(): void
  /** 遥测：每个二进制帧到达时调用。 */
  onFrame?(bytes: Uint8Array): void
}

/** 恢复帧解析完 → 下一帧（xterm 已画完）→ 才还清遮罩的账。 */
export function settleRestore(deps: OutputFrameDeps, epoch: number): void {
  afterNextPaint(deps.sched, () => { deps.gate.restorePainted(epoch) }, RESTORE_PAINT_FALLBACK_MS)
}

/**
 * 一个二进制帧：replay_reset 之后的那一帧是恢复（与延后的清屏合成一次 write），其余是实时输出。
 */
export function handleOutputFrame(deps: OutputFrameDeps, bytes: Uint8Array): void {
  const verdict = deps.gate.binary()
  const token = deps.latency.noteOutput(deps.now(), verdict.restore ? 'restore' : 'live')
  deps.onFrame?.(bytes)
  deps.write(verdict.prefixReset ? prefixBytes(deps.resetBytes, bytes) : bytes, () => {
    if (token !== null) deps.latency.noteParsed(token, deps.now())
    if (!verdict.restore && token === null) return
    afterNextPaint(deps.sched, () => {
      if (verdict.restore) deps.gate.restorePainted(verdict.epoch)
      if (token !== null) {
        // 可信与否看的是可见性，不是 rAF 和定时器谁先到：主线程长时间卡住之后两者都已过期，常常是
        // 定时器先跑——那是一次真实的慢绘制，正是这个数字要显示的东西，不是丢样本的理由。
        deps.latency.notePainted(token, deps.now(), deps.visibilityEpoch(), deps.hidden())
        deps.onLatencySample?.()
      }
    }, RESTORE_PAINT_FALLBACK_MS)
  })
}

/**
 * 一个控制帧。`replay_reset` 开启一次新的恢复（清屏延后到恢复帧）；"replay_reset 之后没等到恢复帧、
 * 却先来了收尾/终止帧"时需要现在就把延后的清屏补写下去；其余控制帧与恢复无关。调用方随后照常处理
 * 该帧本身。
 */
export function handleControlFrame(deps: OutputFrameDeps, type: string): void {
  if (type === 'replay_reset') {
    deps.gate.replayReset()
    return
  }
  const v = deps.gate.control(type)
  if (!v.flushReset) return
  deps.write(deps.resetBytes, v.awaitPaint ? () => settleRestore(deps, v.epoch) : undefined)
}
