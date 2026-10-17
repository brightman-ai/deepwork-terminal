/**
 * mainThreadMetrics — when the user types, is the page's main thread free to take the key?
 *
 * ── Why this exists ──────────────────────────────────────────────────────────────────────────
 * On 2026-10-18 the IME's Shift toggle fired twice on Edge/macOS, on this page only. The cause was
 * not any handler of ours: keys were reaching the page in bursts (six letters inside 12ms) because
 * the main thread was being held — and a blank page that blocks for 0.7s a second reproduces the
 * double toggle on its own. The evidence for that had to be reconstructed from keystroke timestamps
 * in a diagnostic session. This makes the same question a standing measurement:
 *
 *   inputDelay   — Event Timing: processingStart − startTime for keydown/keyup, i.e. how long a key
 *                  waited for the main thread. Only events slower than 16ms are reported by the
 *                  browser (its minimum threshold), so `keys` (from performance.eventCounts) is the
 *                  denominator and `slowKeys` counts the ones a person would feel.
 *   longFrames   — Long Animation Frames: frames over SLOW_FRAME_MS, the worst one's duration and
 *                  blocking time, and WHICH scripts it ran — the attribution that answers "what is
 *                  holding the thread" without asking anyone to open DevTools.
 *
 * Page-level and started once, because the main thread is shared by every terminal on the page.
 * Silent when nothing happened, and absent on browsers without the APIs (the report is about the
 * Chromium input path that misbehaved; Safari is not degraded by its absence).
 */
import { reportServerEvent } from '@terminal/composables/cli/useCliInputDiagnostics'

const REPORT_INTERVAL_MS = 15_000
/** A key that waited this long was felt — the same "feels instantaneous" line as SLOW_FRAME_MS. */
export const SLOW_INPUT_MS = 100
/** Long animation frames above this are counted; the API itself reports from 50ms. */
export const SLOW_FRAME_MS = 100
const MAX_SAMPLES = 400

export interface InputDelaySample { name: string; delay: number }
export interface FrameScript { invoker: string; fn: string; src: string; ms: number; layoutMs: number }
export interface LongFrameSample { duration: number; blocking: number; scripts: FrameScript[] }

export interface MainThreadSummary {
  keys: number
  slowKeys: number
  inputDelayP95: number
  inputDelayMax: number
  longFrames: number
  longFrameMax: number
  blockingTotal: number
  /** The worst frame's top scripts by time — where the thread went. */
  worst: FrameScript[]
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0
  return sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))]
}

const round = (n: number): number => Math.round(n * 10) / 10

/** Pure: one window's samples → the report, or null when there is nothing worth saying. */
export function summarizeMainThread(
  keys: number,
  inputs: InputDelaySample[],
  frames: LongFrameSample[],
): MainThreadSummary | null {
  const delays = inputs.map((s) => s.delay).sort((a, b) => a - b)
  const slow = frames.filter((f) => f.duration >= SLOW_FRAME_MS)
  if (keys === 0 && delays.length === 0 && slow.length === 0) return null
  const worstFrame = frames.reduce<LongFrameSample | null>((w, f) => (!w || f.duration > w.duration ? f : w), null)
  return {
    keys,
    slowKeys: delays.filter((d) => d >= SLOW_INPUT_MS).length,
    inputDelayP95: round(percentile(delays, 0.95)),
    inputDelayMax: round(delays[delays.length - 1] ?? 0),
    longFrames: slow.length,
    longFrameMax: round(worstFrame?.duration ?? 0),
    blockingTotal: round(frames.reduce((n, f) => n + f.blocking, 0)),
    worst: (worstFrame?.scripts ?? []).slice().sort((a, b) => b.ms - a.ms).slice(0, 3),
  }
}

/** Script source as a short, comparable label: the file, not the origin or a cache-busting query. */
export function shortSource(url: string): string {
  if (!url) return ''
  const path = url.split(/[?#]/)[0]
  return path.slice(path.lastIndexOf('/') + 1)
}

interface ScriptTimingLike {
  invoker?: string
  sourceFunctionName?: string
  sourceURL?: string
  sourceCharPosition?: number
  duration: number
  forcedStyleAndLayoutDuration?: number
}

let started = false

export function startMainThreadMetrics(): void {
  if (started || typeof window === 'undefined' || typeof PerformanceObserver === 'undefined') return
  const supported = PerformanceObserver.supportedEntryTypes ?? []
  if (!supported.includes('event') && !supported.includes('long-animation-frame')) return
  started = true

  let inputs: InputDelaySample[] = []
  let frames: LongFrameSample[] = []
  const counts = (performance as Performance & { eventCounts?: Map<string, number> }).eventCounts
  const keyCount = (): number => (counts?.get('keydown') ?? 0) + (counts?.get('keyup') ?? 0)
  let keysAtWindowStart = keyCount()

  try {
    if (supported.includes('event')) {
      new PerformanceObserver((list) => {
        for (const e of list.getEntries() as PerformanceEventTiming[]) {
          if (e.name !== 'keydown' && e.name !== 'keyup') continue
          if (inputs.length < MAX_SAMPLES) inputs.push({ name: e.name, delay: e.processingStart - e.startTime })
        }
      }).observe({ type: 'event', durationThreshold: 16 } as PerformanceObserverInit)
    }
    if (supported.includes('long-animation-frame')) {
      new PerformanceObserver((list) => {
        for (const e of list.getEntries() as Array<PerformanceEntry & { blockingDuration?: number; scripts?: ScriptTimingLike[] }>) {
          if (frames.length >= MAX_SAMPLES) break
          frames.push({
            duration: e.duration,
            blocking: e.blockingDuration ?? 0,
            scripts: (e.scripts ?? []).map((s) => ({
              invoker: s.invoker ?? '',
              fn: s.sourceFunctionName ?? '',
              src: `${shortSource(s.sourceURL ?? '')}${s.sourceCharPosition && s.sourceCharPosition > 0 ? `:${s.sourceCharPosition}` : ''}`,
              ms: round(s.duration),
              layoutMs: round(s.forcedStyleAndLayoutDuration ?? 0),
            })),
          })
        }
      }).observe({ type: 'long-animation-frame' } as PerformanceObserverInit)
    }
  } catch {
    // Observability must never break the page.
    return
  }

  setInterval(() => {
    const now = keyCount()
    const summary = summarizeMainThread(Math.max(0, now - keysAtWindowStart), inputs, frames)
    keysAtWindowStart = now
    inputs = []
    frames = []
    if (summary) reportServerEvent('cli.main.thread', { ...summary })
  }, REPORT_INTERVAL_MS)
}
