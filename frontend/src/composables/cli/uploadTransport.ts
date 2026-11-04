/** Keep bulk bytes out of the browser's unbounded uplink queue. A small shared
 * window across ALL upload surfaces/tabs/peers bounds queued payload, while two
 * short requests overlap the response RTT instead of leaving the uplink idle. */
export const UPLOAD_MIN_CHUNK = 8 * 1024
export const UPLOAD_MAX_CHUNK = 256 * 1024
const PROBE_BYTES = 16 * 1024
const TARGET_BODY_MS = 100

export function createUploadScheduler(pause = (ms: number) => new Promise<void>(r => setTimeout(r, ms)), concurrency = 1) {
  const lanes: Promise<unknown>[] = Array.from({ length: concurrency }, () => Promise.resolve())
  let next = 0
  return function schedule<T>(operation: () => Promise<T>): Promise<T> {
    const lane = next++ % lanes.length
    const result = lanes[lane].then(operation)
    // Both success and failure release the lane; a failed file cannot strand a batch.
    lanes[lane] = result.catch(() => {}).then(() => pause(4))
    return result
  }
}

export const scheduleUploadRequest = createUploadScheduler(undefined, 2)
const rtts = new Map<string, number>()
const rates = new Map<string, number>()
const measuredAt = new Map<string, number>()
const legacyUploads = createUploadScheduler()
const measurements = new Map<string, Promise<UploadResponse | undefined>>()
const originOf = (url: string) => new URL(url, typeof location === 'undefined' ? 'http://localhost' : location.href).host

export function noteUploadRtt(url: string, ms: number): void {
  if (ms > 0 && Number.isFinite(ms)) {
    const key = originOf(url)
    rtts.set(key, Math.min(rtts.get(key) ?? ms, ms))
  }
}

/** Request latency includes the response RTT. Remove the idle RTT before
 * estimating uplink capacity; keep a short target queue even on slow links. */
export function uploadChunkSize(bytesPerSecond: number): number {
  return Math.max(UPLOAD_MIN_CHUNK, Math.min(UPLOAD_MAX_CHUNK,
    Math.floor(bytesPerSecond * TARGET_BODY_MS / 1000 / UPLOAD_MIN_CHUNK) * UPLOAD_MIN_CHUNK))
}

export function preferredUploadChunkSize(url: string): number {
  const key = originOf(url)
  const rate = Date.now() - (measuredAt.get(key) ?? 0) < 60_000 ? rates.get(key) : undefined
  return rate ? uploadChunkSize(rate) : 16 * 1024
}

export interface UploadResponse { status: number; json: unknown }
export type UploadRequest = (url: string, body: FormData | Blob,
  onProgress?: (sent: number, total: number) => void) => Promise<UploadResponse>

/** A bounded probe once per destination measures the actual uplink, rather than
 * guessing it from navigator.connection's download estimate. Old peers retain
 * their multipart route, serialized through the same lane. */
export async function uploadAttachment(url: string, blob: Blob, metadata: FormData,
  request: UploadRequest, onProgress: (sent: number, total: number) => void): Promise<UploadResponse> {
  const key = originOf(url)
  if (Date.now() - (measuredAt.get(key) ?? 0) >= 60_000) rates.delete(key)
  if (blob.size <= UPLOAD_MIN_CHUNK) {
    const form = new FormData()
    metadata.forEach((value, name) => form.append(name, value))
    form.append('file', blob, String(metadata.get('name') || 'upload.bin'))
    return scheduleUploadRequest(() => request(url, form, (sent, total) =>
      onProgress(total > 0 ? Math.min(blob.size, Math.round(sent / total * blob.size)) : 0, blob.size)))
  }
  let probeResult: UploadResponse | undefined
  if (!rates.has(key)) {
    let measurement = measurements.get(key)
    if (!measurement) {
      measurement = scheduleUploadRequest(async () => {
        const probe = new FormData()
        metadata.forEach((value, name) => probe.append(name, value))
        probe.append('probe', new Blob([new Uint8Array(PROBE_BYTES)]), 'probe.bin')
        const start = performance.now()
        const result = await request(url + '/init?probe=1', probe)
        if (result.status === 200 && (result.json as { probe?: boolean } | null)?.probe) {
          const duration = Math.max(1, performance.now() - start - (rtts.get(key) ?? 0))
          rates.set(key, PROBE_BYTES / duration * 1000)
          measuredAt.set(key, Date.now())
        }
        return result
      })
      measurements.set(key, measurement)
    }
    try { probeResult = await measurement }
    finally { if (measurements.get(key) === measurement) measurements.delete(key) }
  }
  if (!rates.has(key)) {
    if (probeResult && ![404, 405].includes(probeResult.status)) return probeResult
    const form = new FormData()
    metadata.forEach((value, name) => form.append(name, value))
    form.append('file', blob, String(metadata.get('name') || 'upload.bin'))
    return legacyUploads(() => scheduleUploadRequest(() => request(url, form, (sent, total) =>
      onProgress(total > 0 ? Math.min(blob.size, Math.round(sent / total * blob.size)) : 0, blob.size))))
  }
  const chunkSize = uploadChunkSize(rates.get(key)!)
  metadata.set('chunkSize', String(chunkSize))
  const init = await scheduleUploadRequest(() => request(url + '/init', metadata))
  if (init.status !== 200) return init
  const info = init.json as { uploadId: string; chunkSize: number; totalChunks: number }
  if (!info?.uploadId || !Number.isSafeInteger(info.chunkSize) || info.chunkSize < UPLOAD_MIN_CHUNK || info.chunkSize > UPLOAD_MAX_CHUNK
    || info.totalChunks !== Math.max(1, Math.ceil(blob.size / info.chunkSize))) {
    throw new Error('invalid upload initialization')
  }
  const base = url.replace(/\/sessions\/[^/]+\/paste-upload$/, '/files/upload')
  try {
    let nextIndex = 0
    let sent = 0
    let failure: UploadResponse | undefined
    let exception: unknown
    let stopped = false
    async function worker(): Promise<void> {
      try {
        while (!stopped && nextIndex < info.totalChunks) {
          const index = nextIndex++
          const part = blob.slice(index * info.chunkSize, Math.min(blob.size, (index + 1) * info.chunkSize))
          const result = await scheduleUploadRequest(() => request(
            `${base}/chunk?uploadId=${encodeURIComponent(info.uploadId)}&index=${index}&ack=index`, part))
          if (result.status !== 200) { failure = result; stopped = true; return }
          // ACKs can arrive out of order. Count payload bytes exactly once.
          sent += part.size
          onProgress(sent, blob.size)
        }
      } catch (error) { exception = error; stopped = true }
    }
    // Let every in-flight request settle before abort/finalize, including failures.
    await Promise.all([worker(), worker()])
    if (exception) throw exception
    if (failure) return failure
    const complete = new FormData()
    complete.append('uploadId', info.uploadId)
    return await scheduleUploadRequest(() => request(base + '/complete', complete))
  } finally {
    // Complete removes staging on success; abort reclaims partial files on any exit.
    const abort = new FormData()
    abort.append('uploadId', info.uploadId)
    void scheduleUploadRequest(() => request(base + '/abort', abort)).catch(() => {})
  }
}
