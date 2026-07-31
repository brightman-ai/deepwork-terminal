import { describe, it, expect } from 'bun:test'
import { renderSyncEnabled } from '../terminalRenderSync'

/**
 * The forced full-grid repaint is a DOM-renderer remedy that used to run unconditionally, because
 * when it was written there was only one renderer. These pin the rule to its reason.
 */
describe('renderSyncEnabled', () => {
  it('is off under WebGL — the damage-tracking gap it compensates for is not there', () => {
    expect(renderSyncEnabled('webgl', null)).toBe(false)
  })

  it('stays on for the DOM renderer, including the WebGL-unavailable fallback', () => {
    expect(renderSyncEnabled('dom', null)).toBe(true)
  })

  it('an unknown renderer keeps the safe behaviour', () => {
    // A future renderer is guilty until measured: residue is worse than a repaint.
    expect(renderSyncEnabled('canvas', null)).toBe(true)
  })

  it('explicit override wins in both directions — the escape hatch when the rule is wrong', () => {
    expect(renderSyncEnabled('webgl', '1')).toBe(true)
    expect(renderSyncEnabled('dom', '0')).toBe(false)
  })
})
