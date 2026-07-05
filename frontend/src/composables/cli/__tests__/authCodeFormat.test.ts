import { describe, it, expect } from 'bun:test'
import { formatAuthCode } from '../authCodeFormat'

describe('formatAuthCode — game-code style auto-format', () => {
  it('uppercases + inserts the dash after 4 chars (the XXXX-XXXX default)', () => {
    expect(formatAuthCode('e3x1m6t2')).toBe('E3X1-M6T2')
    expect(formatAuthCode('E3X1M6T2')).toBe('E3X1-M6T2')
  })
  it('is idempotent on an already-formatted code (retyping / paste)', () => {
    expect(formatAuthCode('E3X1-M6T2')).toBe('E3X1-M6T2')
  })
  it('strips stray separators/spaces the user or a paste introduces', () => {
    expect(formatAuthCode('e3x1 m6t2')).toBe('E3X1-M6T2')
    expect(formatAuthCode('e3x1--m6t2')).toBe('E3X1-M6T2')
  })
  it('no dash until there are >4 chars (mid-typing)', () => {
    expect(formatAuthCode('e3x')).toBe('E3X')
    expect(formatAuthCode('e3x1')).toBe('E3X1')
    expect(formatAuthCode('e3x1m')).toBe('E3X1-M')
  })
  it('does not truncate a longer custom code (tail kept, dash still after 4)', () => {
    expect(formatAuthCode('abcdefghij')).toBe('ABCD-EFGHIJ')
  })
  it('empty in → empty out', () => {
    expect(formatAuthCode('')).toBe('')
    expect(formatAuthCode('--')).toBe('')
  })
})
