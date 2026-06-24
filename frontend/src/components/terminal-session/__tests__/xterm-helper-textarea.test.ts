import { describe, expect, test } from 'bun:test'
import { clearXtermHelperTextareaValue } from '../../../composables/cli/useXtermHelperTextarea'

describe('clearXtermHelperTextareaValue', () => {
  test('clears stale xterm helper textarea command residue', () => {
    const textarea = { value: 'tmuxx x attach' }

    expect(clearXtermHelperTextareaValue(textarea, 'test')).toBe(true)
    expect(textarea.value).toBe('')
  })

  test('no-ops when already empty', () => {
    const textarea = { value: '' }

    expect(clearXtermHelperTextareaValue(textarea, 'test')).toBe(false)
    expect(textarea.value).toBe('')
  })
})
