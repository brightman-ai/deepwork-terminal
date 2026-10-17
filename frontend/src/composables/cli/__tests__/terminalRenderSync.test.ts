import { describe, it, expect } from 'bun:test'
import { renderSyncEnabled } from '../terminalRenderSync'

/**
 * The forced full-grid repaint was a remedy for residue that turned out to live in xterm's buffer,
 * not in its renderer (see the file header). These pin the rule to that finding.
 */
describe('renderSyncEnabled', () => {
  it('is off under WebGL', () => {
    expect(renderSyncEnabled('webgl', null)).toBe(false)
  })

  // The 2026-07-24 diagnosis: term.refresh() repainted the same drifted buffer and cleared nothing.
  it('is off for the DOM renderer too, including the WebGL-unavailable fallback', () => {
    expect(renderSyncEnabled('dom', null)).toBe(false)
  })

  it('explicit override wins in both directions — the escape hatch when the rule is wrong', () => {
    expect(renderSyncEnabled('webgl', '1')).toBe(true)
    expect(renderSyncEnabled('dom', '1')).toBe(true)
    expect(renderSyncEnabled('dom', '0')).toBe(false)
  })
})
