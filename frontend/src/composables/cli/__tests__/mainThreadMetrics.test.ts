import { describe, it, expect } from 'bun:test'
import { summarizeMainThread, shortSource, SLOW_INPUT_MS, SLOW_FRAME_MS } from '../mainThreadMetrics'

describe('summarizeMainThread', () => {
  it('says nothing for a window in which nothing happened', () => {
    expect(summarizeMainThread(0, [], [])).toBeNull()
  })

  // The browser only reports events slower than 16ms, so the key count has to come from elsewhere —
  // otherwise "3 slow keys" could not be told apart from "3 slow keys out of 300".
  it('reports typing even when no key was slow enough to be sampled', () => {
    const s = summarizeMainThread(40, [], [])!
    expect(s.keys).toBe(40)
    expect(s.slowKeys).toBe(0)
    expect(s.inputDelayMax).toBe(0)
  })

  it('counts the keys a person would feel, and the worst wait', () => {
    const s = summarizeMainThread(10, [
      { name: 'keydown', delay: 20 },
      { name: 'keyup', delay: SLOW_INPUT_MS + 300 },
      { name: 'keydown', delay: SLOW_INPUT_MS },
    ], [])!
    expect(s.slowKeys).toBe(2)
    expect(s.inputDelayMax).toBe(SLOW_INPUT_MS + 300)
  })

  // Attribution is the point: the worst frame's heaviest scripts, heaviest first.
  it('names the scripts of the worst frame, heaviest first, at most three', () => {
    const script = (fn: string, ms: number) => ({ invoker: 'TimerHandler:setTimeout', fn, src: 'index.js:10', ms, layoutMs: 0 })
    const s = summarizeMainThread(0, [], [
      { duration: SLOW_FRAME_MS + 20, blocking: 70, scripts: [script('a', 100)] },
      { duration: 900, blocking: 850, scripts: [script('x', 50), script('y', 600), script('z', 200), script('w', 10)] },
      { duration: 60, blocking: 10, scripts: [] },
    ])!
    expect(s.longFrames).toBe(2)          // the 60ms frame is below the line
    expect(s.longFrameMax).toBe(900)
    expect(s.blockingTotal).toBe(930)
    expect(s.worst.map((w) => w.fn)).toEqual(['y', 'z', 'x'])
  })
})

describe('shortSource', () => {
  it('keeps the file name and drops origin and query', () => {
    expect(shortSource('http://stwork:18074/assets/index-Ab12.js?v=3')).toBe('index-Ab12.js')
    expect(shortSource('')).toBe('')
  })
})
