import { describe, expect, it } from 'bun:test'
import { searchPollDelay } from '../searchPollDelay'

describe('file search refresh polling', () => {
  it('checks quickly while an index is building', () => {
    expect(searchPollDelay('building', false)).toBe(500)
  })

  it('backs off when the index scan failed but leaves manual retry available', () => {
    expect(searchPollDelay('error', true)).toBe(5 * 60 * 1000)
    expect(searchPollDelay('ready', true)).toBe(5 * 60 * 1000)
  })

  it('keeps the ordinary freshness poll for a usable index', () => {
    expect(searchPollDelay('ready', false)).toBe(30 * 1000)
    expect(searchPollDelay('', false)).toBe(30 * 1000)
  })
})
