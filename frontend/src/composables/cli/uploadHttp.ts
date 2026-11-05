/** Native idle deadlines release stalled lanes without a JavaScript timer. */
export const UPLOAD_IDLE_TIMEOUT_MS = 90_000
export interface XhrUploadResult { status: number; json: unknown }

/** Tree-upload fetches share the same liveness bound. Native abort deadlines
 * are preferred; the fallback is checked again when a suspended page resumes. */
export async function withUploadDeadline<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
  if (typeof AbortSignal.timeout === 'function') return operation(AbortSignal.timeout(UPLOAD_IDLE_TIMEOUT_MS))
  const controller = new AbortController()
  const expires = Date.now() + UPLOAD_IDLE_TIMEOUT_MS
  const check = () => { if (Date.now() >= expires) controller.abort() }
  const timer = setTimeout(() => controller.abort(), UPLOAD_IDLE_TIMEOUT_MS)
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', check)
  try { return await operation(controller.signal) }
  finally {
    clearTimeout(timer)
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', check)
  }
}

export function xhrUploadFormData(
  url: string, body: FormData | Blob, headers: Record<string, string>,
  onProgress?: (sent: number, total: number) => void,
  idleTimeoutMs = UPLOAD_IDLE_TIMEOUT_MS,
): Promise<XhrUploadResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    const startedAt = performance.now()
    xhr.open('POST', url, true)
    xhr.timeout = idleTimeoutMs
    for (const [key, value] of Object.entries(headers)) xhr.setRequestHeader(key, value)
    xhr.upload.onprogress = (e) => {
      // XHR timeout is relative to request start, not the last setter call.
      xhr.timeout = Math.ceil(performance.now() - startedAt) + idleTimeoutMs
      if (e.lengthComputable) onProgress?.(e.loaded, e.total)
    }
    xhr.onload = () => {
      let json: unknown = null
      try { json = xhr.responseText ? JSON.parse(xhr.responseText) : null } catch { /* non-JSON */ }
      resolve({ status: xhr.status, json })
    }
    xhr.onerror = () => reject(new Error('network error'))
    xhr.ontimeout = () => reject(new Error('upload stalled'))
    xhr.onabort = () => reject(new Error('upload aborted'))
    xhr.send(body)
  })
}
