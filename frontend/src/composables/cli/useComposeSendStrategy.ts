/** Assembled text is one paste transaction, including short single-line text.
 * The terminal's negotiated mode decides framing; text length never decides
 * whether a paste becomes individual keystrokes. */
const BRACKETED_PASTE_START = '\x1b[200~'
const BRACKETED_PASTE_END = '\x1b[201~'
export interface ComposeSendStrategy {
  encode: (text: string, bracketedPaste?: boolean) => Uint8Array[]
}
export function useComposeSendStrategy(): ComposeSendStrategy {
  const encoder = new TextEncoder()
  return { encode(text, bracketedPaste = true) {
    if (!text) return []
    return [encoder.encode(bracketedPaste ? BRACKETED_PASTE_START + text + BRACKETED_PASTE_END : text)]
  } }
}
