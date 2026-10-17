/**
 * writeSlices — hand xterm big frames in pieces it can yield between.
 *
 * [Ref: guide/troubleshooting/ime-shift-double-toggle.md] — 输入法 Shift 切中英连切两次（2026-10-18，FMEA F12）
 *
 * xterm's WriteBuffer gives the parser a 12ms budget and yields to the event loop when it runs out
 * (WRITE_TIMEOUT_MS in @xterm/xterm src/common/input/WriteBuffer.ts) — but it only checks the clock
 * BETWEEN chunks. One write() is one chunk, parsed to the end in a single task. A reconnect or
 * tab-switch replay arrives as one frame of hundreds of KB (520KB measured on 2026-10-18), and that
 * frame held the main thread long enough for every key typed meanwhile to arrive in a burst — six
 * letters inside 12ms — which is what made the IME's Shift toggle fire twice on Edge/macOS.
 *
 * Splitting is safe anywhere: the escape-sequence parser is a state machine that resumes mid
 * sequence, and both of xterm's decoders carry an incomplete character across writes (Utf8ToUtf32
 * keeps up to three pending bytes, StringToUtf32 a pending high surrogate).
 */

/** Largest piece passed to one xterm write(). Well under a 12ms parse even for dense TUI output. */
export const WRITE_SLICE_BYTES = 16 * 1024

export function sliceForWrite(data: string | Uint8Array, max = WRITE_SLICE_BYTES): Array<string | Uint8Array> {
  const len = typeof data === 'string' ? data.length : data.byteLength
  if (len <= max) return [data]
  const out: Array<string | Uint8Array> = []
  for (let i = 0; i < len; i += max) {
    out.push(typeof data === 'string' ? data.slice(i, i + max) : data.subarray(i, i + max))
  }
  return out
}
