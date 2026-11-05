/**
 * useUploadProgress — SSOT registry of in-flight paste/attach uploads for ONE
 * terminal surface (instantiated per useClipboardPaste(sessionId) call — one per
 * tab — so uploads never leak across tabs).
 *
 * Design goals (see UploadProgressFloat.vue, the only reader):
 *   - Delayed reveal: an upload only becomes visible if it is still uploading
 *     300ms after it started. A fast upload (the common case) never flashes any
 *     UI — the path landing in the PTY is feedback enough. This is why the paste
 *     path felt "silent" before: `uploading` existed but nothing consumed it, so
 *     a SLOW upload also looked silent → the user retried → 3-4 duplicate paths.
 *   - Errors ALWAYS reveal immediately, even inside the 300ms grace window — an
 *     error is exactly the case the user must not miss (that's the retry storm).
 *   - The float is a pure reader of `entries`; all timing/visibility logic lives
 *     here so the component stays dumb and there is exactly one place that can
 *     get the "should this show" rule wrong.
 */
import { reactive, computed, getCurrentScope, onScopeDispose, type ComputedRef } from 'vue'
import { createTransferMeter } from './uploadTelemetry'

export type UploadStatus = 'uploading' | 'done' | 'error'

export interface UploadEntry {
  id: string
  name: string
  size: number
  sent: number
  total: number
  status: UploadStatus
  startedAt: number
  revealed: boolean
  speedBps?: number
  etaSeconds?: number | null
  resumed?: number
  error?: string
  /** Re-runs this exact upload (and, on success, re-injects its path). Undefined
   * for call sites that never wired one — the float hides the button then. */
  retry?: () => void
}

export interface UploadProgressStore {
  /** Visible entries only, oldest first (batch order). */
  entries: ComputedRef<UploadEntry[]>
  register: (name: string, size: number, retry?: () => void, revealImmediately?: boolean) => string
  progress: (id: string, sent: number, total: number, resumed?: number) => void
  complete: (id: string) => void
  /** `retryable: false` drops the entry's retry action — see fail(). */
  fail: (id: string, message: string, retryable?: boolean) => void
  remove: (id: string) => void
}

const REVEAL_DELAY_MS = 300
const DISMISS_DELAY_MS = 1500

export function createUploadProgressStore(now = () => performance.now()): UploadProgressStore {
  const byId = reactive(new Map<string, UploadEntry>())
  const totalMeter = createTransferMeter(now)
  let timer: ReturnType<typeof setInterval> | undefined
  let settledTimer: ReturnType<typeof setTimeout> | undefined

  function sample(): void {
    const all = Array.from(byId.values())
    const speed = totalMeter.sample(all.reduce((n, e) => n + e.sent - (e.resumed || 0), 0))
    const active = all.filter(e => e.status === 'uploading')
    const remaining = active.reduce((n, e) => n + Math.max(0, e.total - e.sent), 0)
    for (const e of active) {
      e.speedBps = speed
      e.etaSeconds = speed > 0 ? remaining / speed : null
    }
    if (!active.length && timer) { clearInterval(timer); timer = undefined }
  }

  function dismissCompleted(): void {
    if (Array.from(byId.values()).some(e => e.status === 'uploading')) return
    if (settledTimer) clearTimeout(settledTimer)
    settledTimer = setTimeout(() => {
      for (const e of byId.values()) if (e.status === 'done') remove(e.id)
    }, DISMISS_DELAY_MS)
  }

  if (getCurrentScope()) onScopeDispose(() => {
    if (timer) clearInterval(timer)
    if (settledTimer) clearTimeout(settledTimer)
  })

  const entries = computed<UploadEntry[]>(() =>
    Array.from(byId.values())
      .filter(e => e.revealed)
      .sort((a, b) => a.startedAt - b.startedAt),
  )

  function register(name: string, size: number, retry?: () => void, revealImmediately = false): string {
    if (settledTimer) { clearTimeout(settledTimer); settledTimer = undefined }
    const id = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `up-${Date.now()}-${Math.random().toString(36).slice(2)}`
    const entry: UploadEntry = {
      id,
      name,
      size,
      sent: 0,
      total: size || 0,
      status: 'uploading',
      startedAt: Date.now(),
      revealed: revealImmediately,
      retry,
    }
    byId.set(id, entry)
    totalMeter.sample(Array.from(byId.values()).reduce((n, e) => n + e.sent - (e.resumed || 0), 0))
    if (!timer) timer = setInterval(sample, 250)
    if (!revealImmediately) {
      setTimeout(() => {
        const e = byId.get(id)
        // Only reveal if STILL uploading — a fast success/failure that already
        // resolved keeps its own decision (fail() already revealed; complete()
        // already deleted a never-shown entry, so `e` is gone here).
        if (e && e.status === 'uploading') e.revealed = true
      }, REVEAL_DELAY_MS)
    }
    return id
  }

  function progress(id: string, sent: number, total: number, resumed = 0): void {
    const e = byId.get(id)
    if (!e) return
    e.sent = Math.max(e.sent, Math.min(Math.max(0, sent), total > 0 ? total : e.total))
    if (total > 0) e.total = total
    e.resumed = Math.max(e.resumed || 0, Math.min(e.sent, resumed))
    sample()
  }

  function complete(id: string): void {
    const e = byId.get(id)
    if (!e) return
    e.status = 'done'
    e.sent = e.total
    if (e.revealed) {
      // Brief ✓ flash, then auto-dismiss.
      // Retain completed bytes while another file is active: aggregate percent
      // must not jump backwards when the first file finishes.
      dismissCompleted()
    } else {
      // Never shown (finished inside the grace window) — drop silently, no flicker.
      remove(id)
    }
    sample()
    dismissCompleted()
  }

  function fail(id: string, message: string, retryable = true): void {
    const e = byId.get(id)
    if (!e) return
    e.status = 'error'
    e.error = message
    // A DETERMINISTIC rejection (the server judged these exact bytes: too large, malformed)
    // loses its retry action. Offering 重试 there is a lie the user pays for — a .drawio
    // refused by the old MIME allowlist showed the button, and pressing it re-uploaded the
    // whole file just to be refused identically 7 seconds later. Only genuinely transient
    // failures (5xx, network) keep it.
    if (!retryable) e.retry = undefined
    // Errors always surface, even inside the 300ms grace window — this IS the
    // feedback that stops the user from blindly retrying into the PTY.
    e.revealed = true
    sample()
    dismissCompleted()
  }

  function remove(id: string): void {
    byId.delete(id)
    sample()
  }

  return { entries, register, progress, complete, fail, remove }
}
