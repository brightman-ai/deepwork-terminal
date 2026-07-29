import { describe, it, expect } from 'bun:test'
import { isDefaultTabName, displayTabName } from '../useTabDisplayName'

describe('isDefaultTabName', () => {
  it('matches standalone\'s "终端 N" (with space) and the target "终端N" (no space) forms', () => {
    expect(isDefaultTabName('终端 1')).toBe(true)
    expect(isDefaultTabName('终端12')).toBe(true)
  })
  it('matches pro\'s bare "终端" default (useCliV2.createSession posts name=\'终端\')', () => {
    expect(isDefaultTabName('终端')).toBe(true)
  })
  it('rejects a user-chosen custom name', () => {
    expect(isDefaultTabName('部署脚本')).toBe(false)
    expect(isDefaultTabName('终端 1 - 备份')).toBe(false)
    expect(isDefaultTabName('终端部署')).toBe(false)
  })
})

describe('displayTabName', () => {
  it('renders an untouched default-named tab as its LIVE position, not the frozen creation-time name', () => {
    // Tab was created as "终端 3" (3rd by creation order) but now sits at visible position 1
    // (earlier tabs closed) — the label must track position, matching the Alt+1 shortcut.
    expect(displayTabName('终端 3', 1)).toBe('终端1')
  })
  it('numbers pro\'s otherwise-indistinguishable bare "终端" tabs by position', () => {
    // The screenshot bug: two pro tabs both read "终端" with nothing to tell them apart.
    expect(displayTabName('终端', 1)).toBe('终端1')
    expect(displayTabName('终端', 2)).toBe('终端2')
  })
  it('leaves a user-renamed tab untouched regardless of position', () => {
    expect(displayTabName('部署脚本', 2)).toBe('部署脚本')
  })
  it('falls back to the stored name when position is unknown (tab not in the visible set)', () => {
    expect(displayTabName('终端 1', undefined)).toBe('终端 1')
  })
})
