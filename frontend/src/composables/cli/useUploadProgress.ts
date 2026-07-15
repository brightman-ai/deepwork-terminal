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
import { reactive, computed, type ComputedRef } from 'vue'

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
  error?: string
  /** Re-runs this exact upload (and, on success, re-injects its path). Undefined
   * for call sites that never wired one — the float hides the button then. */
  retry?: () => void
}

export interface UploadProgressStore {
  /** Visible entries only, oldest first (batch order). */
  entries: ComputedRef<UploadEntry[]>
  register: (name: string, size: number, retry?: () => void, revealImmediately?: boolean) => string
  progress: (id: string, sent: number, total: number) => void
  complete: (id: string) => void
  /** `retryable: false` drops the entry's retry action — see fail(). */
  fail: (id: string, message: string, retryable?: boolean) => void
  remove: (id: string) => void
}

const REVEAL_DELAY_MS = 300
const DISMISS_DELAY_MS = 1500

export function createUploadProgressStore(): UploadProgressStore {
  const byId = reactive(new Map<string, UploadEntry>())

  const entries = computed<UploadEntry[]>(() =>
    Array.from(byId.values())
      .filter(e => e.revealed)
      .sort((a, b) => a.startedAt - b.startedAt),
  )

  function register(name: string, size: number, retry?: () => void, revealImmediately = false): string {
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

  function progress(id: string, sent: number, total: number): void {
    const e = byId.get(id)
    if (!e) return
    e.sent = sent
    if (total > 0) e.total = total
  }

  function complete(id: string): void {
    const e = byId.get(id)
    if (!e) return
    e.status = 'done'
    e.sent = e.total
    if (e.revealed) {
      // Brief ✓ flash, then auto-dismiss.
      setTimeout(() => byId.delete(id), DISMISS_DELAY_MS)
    } else {
      // Never shown (finished inside the grace window) — drop silently, no flicker.
      byId.delete(id)
    }
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
  }

  function remove(id: string): void {
    byId.delete(id)
  }

  return { entries, register, progress, complete, fail, remove }
}
