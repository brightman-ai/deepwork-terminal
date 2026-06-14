/**
 * useClipboardText — Single source of truth for reading OS-clipboard *text* and
 * injecting it into the PTY. Used by both the on-screen "Paste" button and the
 * sticky-Ctrl + 'v'/'V' muscle-memory shortcut.
 *
 * Why this exists: the legacy paste path called `navigator.clipboard.readText()`
 * with an empty `.catch(() => {})`. On insecure HTTP (trycloudflare) or iOS Safari,
 * `navigator.clipboard` is undefined or `readText()` rejects (needs secure context +
 * permission + user gesture). The swallowed error meant the user saw *nothing*.
 *
 * Fallback chain (no silent failures):
 *   1. navigator.clipboard.readText()  — secure context, fast path.
 *   2. Hidden contentEditable + native `paste` event capture via
 *      document.execCommand('paste') — works on insecure HTTP / WebKit where the
 *      async Clipboard API is blocked but the legacy execCommand paste is allowed
 *      *inside a user gesture*. We read clipboardData.getData('text') from the
 *      genuine paste event (execCommand's return value is unreliable, the event is not).
 *   3. Total failure → surface a visible HUD error. Never swallow.
 *
 * Transport: text is sent raw via the injected sendBinary. We do NOT wrap with
 * bracketed-paste markers (\x1b[200~ … \x1b[201~) — consistent with every other
 * paste path in this app (resolver injectPaths, onClipboard text). The PTY/tmux
 * applies bracketed paste itself; double-wrapping would corrupt input.
 */
import { createLogger } from '@ce/utils/obs'

type HudKind = 'state' | 'error'

export interface ClipboardTextOptions {
  surface: string
  sendBinary: (data: Uint8Array) => void
  hudRecord?: (kind: HudKind, message: string) => void
}

const log = createLogger('cli-clipboard-text')
const encoder = new TextEncoder()

export function useClipboardText(options: ClipboardTextOptions) {
  /**
   * Read OS clipboard text and inject into the PTY. MUST be called from within a
   * user-gesture handler (button tap / key event) so the fallback execCommand path
   * is permitted by the browser.
   *
   * @returns true if text was injected, false otherwise (error already surfaced).
   */
  async function pasteFromClipboard(source: string): Promise<boolean> {
    const text = await readClipboardText(source)
    if (text == null) return false // error already surfaced by reader
    if (text === '') {
      options.hudRecord?.('state', 'clipboard is empty')
      return false
    }
    options.sendBinary(encoder.encode(text))
    options.hudRecord?.('state', `clipboard paste: ${text.length} chars`)
    log.info('cli.clipboard.text_injected', { surface: options.surface, source, chars: text.length })
    return true
  }

  /** @returns text on success, '' for empty clipboard, null on hard failure (HUD already updated). */
  async function readClipboardText(source: string): Promise<string | null> {
    // Tier 1: async Clipboard API (secure context only).
    if (typeof navigator.clipboard?.readText === 'function') {
      try {
        const text = await navigator.clipboard.readText()
        log.info('cli.clipboard.read_async_ok', { surface: options.surface, source, chars: text.length })
        return text
      } catch (err) {
        log.info('cli.clipboard.read_async_rejected', {
          surface: options.surface,
          source,
          error: err instanceof Error ? err.name || err.message : String(err),
        })
        // fall through to legacy path (covers iOS permission denial)
      }
    }

    // Tier 2: hidden contentEditable + legacy execCommand('paste').
    try {
      const text = await readViaExecCommand()
      if (text != null) {
        log.info('cli.clipboard.read_exec_ok', { surface: options.surface, source, chars: text.length })
        return text
      }
    } catch (err) {
      log.warn('cli.clipboard.read_exec_failed', {
        surface: options.surface,
        source,
        error: err instanceof Error ? err.message : String(err),
      })
    }

    // Tier 3: hard failure — surface, never swallow.
    options.hudRecord?.('error', 'clipboard read blocked — use the system "Paste" menu over the terminal')
    log.warn('cli.clipboard.read_unavailable', { surface: options.surface, source })
    return null
  }

  return { pasteFromClipboard }
}

/**
 * Read clipboard text via a hidden editable element. We capture the *native* paste
 * event's clipboardData (reliable) rather than execCommand's return value (unreliable).
 * execCommand('paste') is the trigger that the browser permits inside a user gesture
 * on insecure origins where the async Clipboard API is unavailable.
 */
function readViaExecCommand(): Promise<string | null> {
  return new Promise<string | null>(resolve => {
    const el = document.createElement('textarea')
    // Off-screen but focusable; must not steal viewport scroll on mobile.
    el.setAttribute('aria-hidden', 'true')
    el.style.position = 'fixed'
    el.style.top = '0'
    el.style.left = '0'
    el.style.width = '1px'
    el.style.height = '1px'
    el.style.padding = '0'
    el.style.border = '0'
    el.style.outline = 'none'
    el.style.opacity = '0'
    el.style.pointerEvents = 'none'
    el.contentEditable = 'true'
    document.body.appendChild(el)

    let settled = false
    const finish = (value: string | null) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      el.removeEventListener('paste', onPaste)
      el.blur()
      el.remove()
      // Restore focus to the previously active element so the terminal keeps input.
      if (prevActive && typeof prevActive.focus === 'function') prevActive.focus()
      resolve(value)
    }

    const onPaste = (e: ClipboardEvent) => {
      const text = e.clipboardData?.getData('text/plain') ?? ''
      e.preventDefault()
      finish(text)
    }

    const prevActive = document.activeElement as HTMLElement | null
    el.addEventListener('paste', onPaste)
    el.focus()

    // If execCommand triggers a synchronous paste event, finish() already ran.
    let pasteTriggered = false
    try {
      pasteTriggered = document.execCommand('paste')
    } catch {
      pasteTriggered = false
    }
    // If the browser rejected the legacy paste outright (no event will fire), bail now.
    // Otherwise give the async paste event a brief window before declaring failure.
    const timer = setTimeout(() => finish(pasteTriggered ? '' : null), 120)
  })
}
