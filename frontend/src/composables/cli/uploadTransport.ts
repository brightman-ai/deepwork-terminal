/** Keep bulk bytes out of the browser's unbounded uplink queue. A small shared
 * window across ALL upload surfaces/tabs/peers bounds queued payload, while two
 * short requests overlap the response RTT instead of leaving the uplink idle. */
export const UPLOAD_MIN_CHUNK = 8 * 1024
export const UPLOAD_MAX_CHUNK = 256 * 1024
const PROBE_BYTES = 16 * 1024
const TARGET_BODY_MS = 100

export function createUploadScheduler(concurrency = 1) {
  const lanes: Promise<unknown>[] = Array.from({ length: concurrency }, () => Promise.resolve())
  let next = 0
  return function schedule<T>(operation: () => Promise<T>): Promise<T> {
    const lane = next++ % lanes.length
    const result = lanes[lane].then(operation)
    // Both success and failure release the lane; a failed file cannot strand a batch.
    // The network callback already yields. Timers here get throttled in hidden pages.
    lanes[lane] = result.catch(() => {})
    return result
  }
}

export const scheduleUploadRequest = createUploadScheduler(2)
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
interface AttachmentResume {
  uploadId: string
  chunkSize: number
  totalChunks: number
  received?: number[]
}
// Only the same immutable Blob in the same session may reuse its failed token.
const resumable = new WeakMap<Blob, Map<string, AttachmentResume>>()


/** A bounded probe once per destination measures the actual uplink, rather than
 * guessing it from navigator.connection's download estimate. Old peers retain
 * their multipart route, serialized through the same lane. */
export async function uploadAttachment(url: string, blob: Blob, metadata: FormData,
  request: UploadRequest, onProgress: (sent: number, total: number, resumed?: number) => void): Promise<UploadResponse> {
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
  let ownsMeasurement = true
  if (!rates.has(key)) {
    let measurement = measurements.get(key)
    ownsMeasurement = !measurement
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
    try {
      const result = await measurement
      if (ownsMeasurement) probeResult = result
    }
    finally { if (measurements.get(key) === measurement) measurements.delete(key) }
  }
  if (!rates.has(key)) {
    // Capacity is shared, validation/auth failures belong to the file/session
    // that made the probe. Another file must get its own response.
    if (!ownsMeasurement) return uploadAttachment(url, blob, metadata, request, onProgress)
    if (probeResult && ![404, 405].includes(probeResult.status)) return probeResult
    const form = new FormData()
    metadata.forEach((value, name) => form.append(name, value))
    form.append('file', blob, String(metadata.get('name') || 'upload.bin'))
    return legacyUploads(() => scheduleUploadRequest(() => request(url, form, (sent, total) =>
      onProgress(total > 0 ? Math.min(blob.size, Math.round(sent / total * blob.size)) : 0, blob.size))))
  }
  const chunkSize = uploadChunkSize(rates.get(key)!)
  metadata.set('chunkSize', String(chunkSize))
  const previous = resumable.get(blob)?.get(url)
  if (previous) metadata.set('resumeId', previous.uploadId)
  else metadata.delete('resumeId')
  let init = await scheduleUploadRequest(() => request(url + '/init', metadata))
  if (previous && [404, 409].includes(init.status)) {
    resumable.get(blob)?.delete(url)
    metadata.delete('resumeId')
    init = await scheduleUploadRequest(() => request(url + '/init', metadata))
  }
  if (init.status !== 200) return init
  const info = init.json as AttachmentResume
  if (!info?.uploadId || !Number.isSafeInteger(info.chunkSize) || info.chunkSize < UPLOAD_MIN_CHUNK || info.chunkSize > UPLOAD_MAX_CHUNK
    || info.totalChunks !== Math.max(1, Math.ceil(blob.size / info.chunkSize))) {
    throw new Error('invalid upload initialization')
  }
  const base = url.replace(/\/sessions\/[^/]+\/paste-upload$/, '/files/upload')
  let complete = false
  try {
    let nextIndex = 0
    const received = new Set((info.received || []).filter(i => Number.isInteger(i) && i >= 0 && i < info.totalChunks))
    const resumed = [...received].reduce((n, i) => n + Math.min(info.chunkSize, blob.size - i * info.chunkSize), 0)
    let sent = resumed
    if (resumed) onProgress(sent, blob.size, resumed)
    let failure: UploadResponse | undefined
    let exception: unknown
    let stopped = false
    async function worker(): Promise<void> {
      try {
        while (!stopped && nextIndex < info.totalChunks) {
          const index = nextIndex++
          if (received.has(index)) continue
          const part = blob.slice(index * info.chunkSize, Math.min(blob.size, (index + 1) * info.chunkSize))
          let result: UploadResponse | undefined
          for (let attempt = 0; attempt < 3; attempt++) {
            try {
              result = await scheduleUploadRequest(() => request(
                base + '/chunk?uploadId=' + encodeURIComponent(info.uploadId) + '&index=' + index + '&ack=index', part))
              if (![408, 429, 500, 502, 503, 504].includes(result.status) || attempt === 2) break
            } catch (error) { if (attempt === 2) throw error }
          }
          if (!result) throw new Error('upload failed')
          if (result.status !== 200) { failure = result; stopped = true; return }
          // ACKs can arrive out of order. Count payload bytes exactly once.
          sent += part.size
          onProgress(sent, blob.size, resumed)
        }
      } catch (error) { exception = error; stopped = true }
    }
    // Let every in-flight request settle before abort/finalize, including failures.
    await Promise.all([worker(), worker()])
    if (exception) throw exception
    if (failure) return failure
    const form = new FormData()
    form.append('uploadId', info.uploadId)
    const result = await scheduleUploadRequest(() => request(base + '/complete', form))
    complete = result.status === 200
    return result
  } finally {
    if (complete) resumable.get(blob)?.delete(url)
    else {
      let targets = resumable.get(blob)
      if (!targets) { targets = new Map(); resumable.set(blob, targets) }
      targets.set(url, info)
      // Preserve ACKed parts for Retry; server TTL reclaims abandoned uploads.
    }
  }
}
