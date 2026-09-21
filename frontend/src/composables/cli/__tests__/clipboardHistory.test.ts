import { describe, expect, it } from 'bun:test'
import {
  boundClipboardHistory,
  sameClipboardTarget,
  type ClipboardEntry,
  type ClipboardTarget,
} from '../clipboardHistory'
const now = Date.now()
const entry = (
  id: string,
  extra: Partial<ClipboardEntry> = {},
): ClipboardEntry => ({
  id,
  sequence: 1,
  sessionId: 's',
  source: 'tmux',
  direction: 'remote',
  text: '业务基线\n',
  at: new Date(now).toISOString(),
  ...extra,
})
describe('remote clipboard history', () => {
  it('deduplicates event IDs without merging separate copies of the same text', () => {
    expect(
      boundClipboardHistory([entry('a'), entry('a'), entry('b')], 7, now).map(
        (e) => e.id,
      ),
    ).toEqual(['a', 'b'])
  })
  it('expires normal records, preserves pins, and caps both count and UTF-8 bytes', () => {
    const old = new Date(now - 9 * 86400000).toISOString()
    const records = [
      entry('expired', { at: old }),
      ...Array.from({ length: 110 }, (_, i) => entry(String(i))),
      entry('pin', { at: old, pinned: true }),
    ]
    const bounded = boundClipboardHistory(records, 7, now)
    expect(bounded.length).toBe(100)
    expect(bounded.find((e) => e.id === 'expired')).toBeUndefined()
    expect(bounded.at(-1)?.id).toBe('pin')
    const large = Array.from({ length: 10 }, (_, i) =>
      entry(String(i), { text: '文'.repeat(1024 * 1024) }),
    )
    expect(boundClipboardHistory(large, 7, now).length).toBe(6)
  })
  it('rejects changed processes even if a terminal name or pane ID was reused', () => {
    const target: ClipboardTarget = {
      id: 's/%4',
      sessionId: 's',
      name: 'tab',
      paneId: '%4',
      pid: 100,
      shellPid: 99,
      active: true,
      readCommand: 'get',
    }
    expect(sameClipboardTarget(target, { ...target, name: 'renamed' })).toBe(
      true,
    )
    expect(sameClipboardTarget(target, { ...target, pid: 101 })).toBe(false)
    expect(sameClipboardTarget(target, { ...target, shellPid: 98 })).toBe(false)
  })
})
