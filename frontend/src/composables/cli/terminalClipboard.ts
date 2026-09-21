/** OSC 52 is a write request, never permission to read the user's clipboard. */
export const MAX_TERMINAL_CLIPBOARD_BYTES = 4 * 1024 * 1024
export type TerminalClipboardRequest = { text: string } | { error: string } | null

export function decodeTerminalClipboard(payload: string): TerminalClipboardRequest {
  const separator = payload.indexOf(';')
  if (separator < 0) return null
  const target = payload.slice(0, separator)
  const encoded = payload.slice(separator + 1)
  if (encoded === '?' || !/^[cps0-7]*$/.test(target)) return null
  if (encoded.length > Math.ceil(MAX_TERMINAL_CLIPBOARD_BYTES / 3) * 4) {
    return { error: '复制内容超过 4 MiB，请分段复制' }
  }
  try {
    const binary = atob(encoded)
    if (binary.length > MAX_TERMINAL_CLIPBOARD_BYTES) return { error: '复制内容超过 4 MiB，请分段复制' }
    const bytes = Uint8Array.from(binary, c => c.charCodeAt(0))
    return { text: new TextDecoder('utf-8', { fatal: true }).decode(bytes) }
  } catch {
    return { error: '终端复制内容不是有效的 UTF-8 文本' }
  }
}

/** HTTP fallback, also allowed after a recent tmux selection gesture in Chromium.
 * A browser refusal must never be reported as a successful copy. */
export function copyTerminalTextOnClick(text: string): boolean {
  const previous = document.activeElement as HTMLElement | null
  const selection = window.getSelection()
  const ranges = selection ? Array.from({ length: selection.rangeCount }, (_, i) => selection.getRangeAt(i).cloneRange()) : []
  const input = document.createElement('textarea')
  input.value = text
  input.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;font-size:16px'
  document.body.appendChild(input)
  // The requested payload owns this copy event, even with CopyModeView mounted.
  const copy = (event: ClipboardEvent) => {
    event.clipboardData?.setData('text/plain', text)
    event.preventDefault()
    event.stopImmediatePropagation()
  }
  window.addEventListener('copy', copy, true)
  try {
    input.focus({ preventScroll: true })
    input.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    window.removeEventListener('copy', copy, true)
    input.remove()
    previous?.focus({ preventScroll: true })
    selection?.removeAllRanges()
    for (const range of ranges) selection?.addRange(range)
  }
}
