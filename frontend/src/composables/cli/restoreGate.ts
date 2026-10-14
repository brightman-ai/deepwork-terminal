/**
 * restoreGate — 「正在恢复终端内容」遮罩：什么时候欠着、什么时候撤、什么时候才让使用者看见（REQ-TS-16）。
 *
 * ## 它修的是什么
 *
 * 旧逻辑把"收到任意一帧"当成"已恢复"。而服务端的恢复序列是
 *
 *     resized(文本) → replay_reset(文本) → 恢复字节(一个二进制帧，可为空) → session_meta(文本)
 *
 * 第一帧 `resized` 一到遮罩就撤了，随后 replay_reset 清屏、恢复字节再慢慢解析——使用者看到的正是
 * "空白 → 刷屏 → 才出内容"。另一头，遮罩的 150ms 防抖从组件挂载就开始计时，一个从未激活过的 tab
 * 第一次被切到时，遮罩已经"到期"，连接还没开始就亮着。
 *
 * ## 现在的判据
 *
 * 1. **欠着(owed)**：连接/重连开始、或收到 replay_reset 起，直到**恢复字节被终端解析完并画出**才还清。
 *    不是"收到第一帧"。
 * 2. **可见**：欠着 ∧ 这个面板正被看着(active) ∧ 连接态在连/已连——并且这个条件**连续成立满 150ms**
 *    才亮（防抖从"同时成立"那一刻起算，所以激活后 150ms 内完成的恢复不出现遮罩）。
 * 3. **清屏延后**：replay_reset 不立刻清屏，而是并进恢复字节的同一次写入（清屏 + 恢复 = 一次 write）。
 *    否则 replay_reset 到恢复帧到达之间，使用者看到的是一块被清空的屏。
 *
 * 纯状态机：不碰 DOM、不碰计时器（防抖用注入的计时器），测试直接驱动事件。
 */

import type { WSConnectionStatus } from '@terminal/types/terminal'

/** 遮罩的防抖：比这更短的恢复，闪一下标签读起来是故障而不是状态。 */
export const RESYNC_OVERLAY_DELAY_MS = 150
/** 解析完后等 rAF 画出的兜底：后台页/内嵌 webview 的 rAF 可能被挂起，遮罩不能因此卡死。 */
export const RESTORE_PAINT_FALLBACK_MS = 200

export interface BinaryVerdict {
  /** 这一帧就是 replay_reset 之后的恢复帧。 */
  restore: boolean
  /** 写入时要在最前面拼上清屏序列（被延后的 replay_reset）。 */
  prefixReset: boolean
  epoch: number
}

export interface ControlVerdict {
  /** 现在就把清屏写下去：replay_reset 之后没等到恢复帧，却已经来了收尾帧。 */
  flushReset: boolean
  /** flushReset 为真且需要等它画出来才算还清时用（session_meta 路径）；终止类帧不需要。 */
  awaitPaint: boolean
  epoch: number
}

const NO_ACTION = (epoch: number): ControlVerdict => ({ flushReset: false, awaitPaint: false, epoch })

/** 会话终止/被接管/出错：后面紧跟一行提示，遮罩不能盖在提示上面等一个不会来的恢复。 */
const TERMINAL_FRAMES = new Set(['shell_exit', 'preempted', 'error'])

export function createRestoreGate(onOwedChange: (owed: boolean) => void = () => {}) {
  let owed = true
  let epoch = 0
  let awaitingRestore = false // replay_reset 已到、恢复帧未到：清屏被延后
  let restoreInFlight = false // 恢复帧已交给终端，还没画完
  let sawReset = false // 本次连接见过 replay_reset（没见过 = 对端不走这套分帧）

  function setOwed(next: boolean): void {
    if (owed === next) return
    owed = next
    onOwedChange(next)
  }

  return {
    get owed() { return owed },
    get epoch() { return epoch },

    /** 连接/重连开始：上一次连接的一切作废，重新欠一次恢复。 */
    connecting(): void {
      epoch++
      awaitingRestore = false
      restoreInFlight = false
      sawReset = false
      setOwed(true)
    },

    /** replay_reset 到了（首次连接、或丢帧后的重同步）：从这一刻起欠一次恢复。清屏不在这里做。 */
    replayReset(): void {
      epoch++
      sawReset = true
      awaitingRestore = true
      restoreInFlight = false
      setOwed(true)
    },

    /** 一个二进制帧到了：是不是恢复帧，要不要给它拼清屏。 */
    binary(): BinaryVerdict {
      if (awaitingRestore) {
        awaitingRestore = false
        restoreInFlight = true
        return { restore: true, prefixReset: true, epoch }
      }
      return { restore: false, prefixReset: false, epoch }
    },

    /** 恢复帧（或被延后的清屏）已被解析且画出。过期纪元的回调（期间又连接/重同步了）一律忽略。 */
    restorePainted(forEpoch: number): boolean {
      if (forEpoch !== epoch || !restoreInFlight) return false
      restoreInFlight = false
      setOwed(false)
      return true
    },

    /** 一个控制帧到了。只有收尾帧/终止帧改变欠账，其余（resized、tmux_state…）与恢复无关。 */
    control(type: string): ControlVerdict {
      if (type === 'session_meta') {
        // 服务端"初始爆发结束"的信号，在恢复帧之后。恢复帧还在解析/画 → 不能撤。
        if (awaitingRestore) {
          // 有 replay_reset 却没有恢复帧（对端不发空帧）：把延后的清屏补上，画出来才算还清。
          awaitingRestore = false
          restoreInFlight = true
          return { flushReset: true, awaitPaint: true, epoch }
        }
        if (!restoreInFlight && !sawReset) setOwed(false) // 没有 replay_reset 分帧的对端：无账可还
        return NO_ACTION(epoch)
      }
      if (TERMINAL_FRAMES.has(type)) {
        const flush = awaitingRestore
        awaitingRestore = false
        restoreInFlight = false
        setOwed(false)
        return { flushReset: flush, awaitPaint: false, epoch }
      }
      return NO_ACTION(epoch)
    },
  }
}

export type RestoreGate = ReturnType<typeof createRestoreGate>

/**
 * 清屏并进恢复帧的同一次 write：xterm 对一次 write 的数据是一口气解析完、之后才画（rAF），所以
 * 「清屏 + 恢复」合成一块就不会有"只清了没画完"的那一帧；分成两次 write 则可能被画到中间。
 */
export function prefixBytes(prefix: Uint8Array, bytes: Uint8Array): Uint8Array {
  const out = new Uint8Array(prefix.length + bytes.length)
  out.set(prefix, 0)
  out.set(bytes, prefix.length)
  return out
}

/** 遮罩"到期"的条件（还没亮，亮不亮还要过防抖）。 */
export function restoreOverlayDue(o: { owed: boolean; active: boolean; status: WSConnectionStatus }): boolean {
  return o.owed
    && o.active
    && (o.status === 'connecting' || o.status === 'reconnecting' || o.status === 'connected')
}

export interface RevealTimers {
  setTimeout(cb: () => void, ms: number): unknown
  clearTimeout(id: unknown): void
}

const browserTimers: RevealTimers = {
  setTimeout: (cb, ms) => setTimeout(cb, ms),
  clearTimeout: (id) => clearTimeout(id as ReturnType<typeof setTimeout>),
}

/**
 * 防抖显示：`set(true)` 连续满 delayMs 才 onChange(true)；中途 `set(false)` 取消，从没亮过。
 * 重复的 `set(true)` 不重新计时——"连续成立"从第一次成立算起。
 */
export function createDelayedReveal(
  delayMs: number,
  onChange: (visible: boolean) => void,
  timers: RevealTimers = browserTimers,
) {
  let timer: unknown = null
  let visible = false
  function cancel(): void {
    if (timer !== null) {
      timers.clearTimeout(timer)
      timer = null
    }
  }
  return {
    get visible() { return visible },
    set(due: boolean): void {
      if (due) {
        if (timer !== null || visible) return
        timer = timers.setTimeout(() => {
          timer = null
          visible = true
          onChange(true)
        }, delayMs)
        return
      }
      cancel()
      if (visible) {
        visible = false
        onChange(false)
      }
    },
    dispose(): void {
      cancel()
      visible = false
    },
  }
}
