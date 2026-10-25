import { describe, expect, it } from 'bun:test'
import { createUsageReportLoader, type UsageWindow } from '../usageReportLoader'
import type { UsageReportData } from '../useUsageReport'

const ready = (window: UsageWindow, total = 7): UsageReportData => ({window, available: true, summary: {input_tokens: total, output_tokens: 0, cache_read_tokens: 0, total_tokens: total}, calculation_state: 'fresh'})
const cold = (window: UsageWindow): UsageReportData => ({window, available: false, reason: 'calculating', calculation_state: 'calculating'})
const tick = () => new Promise(resolve => setTimeout(resolve, 0))
const deferred = <T>() => {let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((a,b) => {resolve=a; reject=b}); return {promise, resolve, reject}}

describe('usage report request lifecycle', () => {
  it('follows a cold calculation to the requested window without requiring a refresh click', async () => {
    let calls = 0
    const model = createUsageReportLoader(async window => ++calls < 3 ? cold(window) : ready(window, 1414), {retryDelays: [0, 0]})
    const work = model.load('14d')
    expect(model.loading.value).toBe(true)
    await work
    expect(calls).toBe(3)
    expect(model.report.value).toEqual(ready('14d', 1414))
    expect(model.calculating.value).toBe(false)
    expect(model.error.value).toBe('')
    expect(model.loading.value).toBe(false)
  })
  it('deduplicates a same-window refresh while a request is already in progress', async () => {
    const answer = deferred<UsageReportData>(); let calls = 0
    const model = createUsageReportLoader(async () => {calls++; return answer.promise})
    const first = model.load('7d'), second = model.load('7d')
    expect(first).toBe(second); expect(calls).toBe(1)
    answer.resolve(ready('7d')); await first
  })
  it('keeps independent 24h/7d/14d/30d snapshots even when responses arrive out of order', async () => {
    const windows: UsageWindow[] = ['24h','7d','14d','30d']
    const answers = Object.fromEntries(windows.map(window => [window, deferred<UsageReportData>()]))
    const models = Object.fromEntries(windows.map(window => [window, createUsageReportLoader(() => answers[window]!.promise)]))
    const work = windows.map(window => models[window]!.load(window))
    for (const window of [...windows].reverse()) answers[window]!.resolve(ready(window, windows.indexOf(window) + 1))
    await Promise.all(work)
    for (const window of windows) expect(models[window]!.report.value).toEqual(ready(window, windows.indexOf(window) + 1))
  })
  it('rejects a late response from a replaced window, including fetchers that ignore abort', async () => {
    const old = deferred<UsageReportData>()
    const model = createUsageReportLoader(window => window === '7d' ? old.promise : Promise.resolve(ready(window, 14)))
    const first = model.load('7d'); await model.load('14d')
    old.resolve(ready('7d', 999)); await first
    expect(model.report.value).toEqual(ready('14d', 14)); expect(model.error.value).toBe('')
  })
  it('preserves the same window last-good result on failure and recovers on retry', async () => {
    let fail = false
    const model = createUsageReportLoader(async window => {if(fail) throw new Error('加载用量失败 (503)'); return ready(window)})
    await model.load('7d'); fail = true; await model.load('7d')
    expect(model.report.value).toEqual(ready('7d')); expect(model.error.value).toContain('503')
    fail = false; await model.load('7d'); expect(model.error.value).toBe('')
  })
  it('does not show another window last-good result after a failed selection', async () => {
    const model = createUsageReportLoader(async window => {if(window === '14d') throw new TypeError('offline'); return ready(window)})
    await model.load('7d'); await model.load('14d')
    expect(model.report.value).toBeNull(); expect(model.error.value).toContain('网络')
  })
  it('stops polling when the view cancels and allows reopening the same window', async () => {
    let calls = 0
    const model = createUsageReportLoader(async window => ++calls === 1 ? cold(window) : ready(window), {retryDelays: [10000]})
    const work = model.load('7d'); await tick(); expect(model.calculating.value).toBe(true)
    model.cancel(); await work
    expect(calls).toBe(1); expect(model.loading.value).toBe(false)
    await model.load('7d'); expect(model.report.value?.available).toBe(true)
  })
  it('bounds cold polling while leaving an explicit calculation state and retry route', async () => {
    let calls = 0
    const model = createUsageReportLoader(async window => {calls++; return cold(window)}, {retryDelays: [0]})
    await model.load('30d')
    expect(calls).toBe(2); expect(model.calculating.value).toBe(true); expect(model.loading.value).toBe(false)
  })
  it('also bounds elapsed calculation time rather than only counting attempts', async () => {
    let calls = 0
    const model = createUsageReportLoader(async window => {calls++; return cold(window)}, {retryDelays: [10000], maxCalculationMs: 5})
    await model.load('7d')
    expect(calls).toBe(1); expect(model.calculating.value).toBe(true); expect(model.loading.value).toBe(false)
  })
  it('revalidates a stale snapshot and retains data while the background calculation runs', async () => {
    let calls = 0; const update = deferred<UsageReportData>()
    const model = createUsageReportLoader(async window => ++calls === 1 ? {...ready(window, 7), calculation_state: 'stale'} : update.promise, {retryDelays: [0]})
    const work = model.load('7d'); await tick()
    expect(model.report.value?.summary?.total_tokens).toBe(7); expect(model.calculating.value).toBe(true)
    update.resolve(ready('7d', 8)); await work
    expect(model.report.value?.summary?.total_tokens).toBe(8); expect(model.calculating.value).toBe(false)
  })
  it('distinguishes no local data from failure or ongoing calculation', async () => {
    const model = createUsageReportLoader(async window => ({window, available: false, reason: 'no_data_source'}))
    await model.load('7d'); expect(model.error.value).toBe(''); expect(model.calculating.value).toBe(false)
    expect(model.report.value?.reason).toBe('no_data_source')
  })
  it('does not silently paint last-good data after a definitive no-source response', async () => {
    let calls = 0
    const model = createUsageReportLoader(async window => ++calls === 1 ? ready(window) : {window,available:false,reason:'no_data_source'})
    await model.load('7d'); await model.load('7d')
    expect(model.report.value?.available).toBe(false); expect(model.calculating.value).toBe(false)
  })
  it('times out a transport that never returns and releases the loading state', async () => {
    const model = createUsageReportLoader((_window, signal) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('aborted')))), {requestTimeoutMs: 5})
    await model.load('7d'); expect(model.error.value).toContain('超时'); expect(model.loading.value).toBe(false)
  })
  it('does not accept a response labelled with a different window', async () => {
    const model = createUsageReportLoader(async () => ready('24h'))
    await model.load('14d'); expect(model.report.value).toBeNull(); expect(model.error.value).toContain('时间范围')
  })
})
