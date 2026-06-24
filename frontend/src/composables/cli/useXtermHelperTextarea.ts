import { reportCliInputDiagnostic, summarizeText } from '@terminal/composables/cli/useCliInputDiagnostics'

export interface TextareaValueLike {
  value: string
}

export function clearXtermHelperTextareaValue(
  textarea: TextareaValueLike | null | undefined,
  reason: string,
): boolean {
  if (!textarea?.value) return false
  const before = textarea.value
  textarea.value = ''
  reportCliInputDiagnostic('xterm-helper-textarea.clear', {
    reason,
    before: summarizeText(before),
  })
  return true
}
