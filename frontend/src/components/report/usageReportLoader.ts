import { ref } from 'vue'
import type { UsageReportData } from './useUsageReport'

export type UsageWindow = '24h' | '7d' | '14d' | '30d'
type FetchReport = (window: UsageWindow, signal: AbortSignal) => Promise<UsageReportData>

/** One window's last-good snapshot and one cancellable request owner. Cold
 * background calculations are followed to completion, with a bounded backoff. */
export function createUsageReportLoader(fetchReport: FetchReport, options: {
  retryDelays?: readonly number[]
  requestTimeoutMs?: number
  maxCalculationMs?: number
} = {}) {
  const report = ref<UsageReportData | null>(null)
  const loading = ref(false)
  const calculating = ref(false)
  const error = ref('')
  const retryDelays = options.retryDelays ?? [750, 1500, 3000, 5000, 5000, 5000, 5000, 5000]
  let generation = 0
  let currentWindow: UsageWindow | undefined
  let pending: Promise<void> | undefined
  let controller: AbortController | undefined
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let finishWait: ((keepGoing: boolean) => void) | undefined

  function cancel(): void {
    generation++
    controller?.abort()
    controller = undefined
    if (retryTimer !== undefined) clearTimeout(retryTimer)
    retryTimer = undefined
    finishWait?.(false)
    finishWait = undefined
    pending = undefined
    loading.value = false
    calculating.value = false
  }

  function wait(delay: number): Promise<boolean> {
    return new Promise(resolve => {
      finishWait = resolve
      retryTimer = setTimeout(() => {
        retryTimer = undefined
        finishWait = undefined
        resolve(true)
      }, delay)
    })
  }

  async function run(id: number, window: UsageWindow): Promise<void> {
    loading.value = true
    calculating.value = false
    error.value = ''
    if (report.value?.window !== window) report.value = null
    const deadline = Date.now() + (options.maxCalculationMs ?? 45000)
    for (let attempt = 0; ; attempt++) {
      if (attempt > 0 && Date.now() >= deadline) return
      const request = new AbortController()
      controller = request
      let timedOut = false
      const timeout = setTimeout(() => { timedOut = true; request.abort() }, Math.max(1, Math.min(options.requestTimeoutMs ?? 12000, deadline - Date.now())))
      let next: UsageReportData
      try {
        next = await fetchReport(window, request.signal)
        if (id !== generation) return
        if (!next || next.window !== window || typeof next.available !== 'boolean') throw new Error('返回的用量时间范围或数据不完整，请重试')
      } catch (cause) {
        if (id !== generation) return
        error.value = timedOut ? '用量加载超时，请重试' : cause instanceof TypeError ? '网络暂时不可用，请重试' : cause instanceof Error ? cause.message : '加载用量失败，请重试'
        calculating.value = false
        return
      } finally {
        clearTimeout(timeout)
        if (controller === request) controller = undefined
      }
      const refreshing = next.calculation_state === 'calculating' || next.calculation_state === 'stale' || next.reason === 'calculating'
      if (next.available || !report.value?.available || !refreshing) report.value = next
      calculating.value = refreshing
      if (!calculating.value || attempt >= retryDelays.length) return
      if (!await wait(Math.max(0, Math.min(retryDelays[attempt]!, deadline - Date.now()))) || id !== generation) return
    }
  }

  function load(window: UsageWindow = '7d'): Promise<void> {
    if (pending && currentWindow === window) return pending
    cancel()
    currentWindow = window
    const id = generation
    pending = run(id, window).finally(() => {
      if (id !== generation) return
      pending = undefined
      loading.value = false
    })
    return pending
  }

  return { report, loading, calculating, error, load, cancel }
}
