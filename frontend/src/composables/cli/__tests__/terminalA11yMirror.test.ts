import { describe, it, expect } from 'bun:test'
import { a11yMirrorEnabled } from '../terminalA11yMirror'

describe('a11yMirrorEnabled', () => {
  // Off by default: nothing reads the mirror, and while output streams it keeps the browser's
  // accessibility tree churning on the thread that delivers keys (see the file header).
  it('is off when nobody asked for it', () => {
    expect(a11yMirrorEnabled(null)).toBe(false)
  })

  it('?a11y_mirror=1 turns it back on — the A/B for that reasoning', () => {
    expect(a11yMirrorEnabled('1')).toBe(true)
    expect(a11yMirrorEnabled('0')).toBe(false)
  })
})
