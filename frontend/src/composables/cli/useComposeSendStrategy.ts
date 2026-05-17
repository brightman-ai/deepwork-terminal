/**
 * useComposeSendStrategy — Determines how to send text from Compose Bar to PTY.
 * Short text (<=200 chars, single line): send as individual keystrokes.
 * Long text (multi-line or >200 chars): use bracketed paste mode.
 * [Ref: CAP-terminal-interaction S4, DDC-06]
 */

const BRACKETED_PASTE_START = '\x1b[200~'
const BRACKETED_PASTE_END = '\x1b[201~'
const SHORT_TEXT_THRESHOLD = 200

export interface ComposeSendStrategy {
  /**
   * Convert text into one or more Uint8Array chunks for sending via WebSocket.
   * Short single-line text: returns individual character chunks.
   * Long or multi-line text: returns a single bracketed-paste chunk.
   */
  encode: (text: string) => Uint8Array[]
}

export function useComposeSendStrategy(): ComposeSendStrategy {
  const encoder = new TextEncoder()

  function encode(text: string): Uint8Array[] {
    if (!text) return []

    const isMultiLine = text.includes('\n')
    const isLong = text.length > SHORT_TEXT_THRESHOLD

    if (isMultiLine || isLong) {
      // Bracketed paste mode for multi-line or long text.
      const wrapped = BRACKETED_PASTE_START + text + BRACKETED_PASTE_END
      return [encoder.encode(wrapped)]
    }

    // Short single-line text: send character by character.
    return Array.from(text).map(ch => encoder.encode(ch))
  }

  return { encode }
}
