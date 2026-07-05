import { describe, it, expect } from 'bun:test'
import { fuzzyMatch } from '../fuzzyMatch'

/**
 * fuzzyMatch is the SSOT every drawer search box now shares via DrawerSearchBox
 * (最近文件 · 目录树[backend mirror] · 历史输入 · 审核). Pinning its contract here pins
 * S-2 (fuzzy 语义一致) for all four consumers at once — the semantics live in ONE place.
 */
describe('fuzzyMatch — shared drawer search SSOT (S-2)', () => {
  it('empty / whitespace-only query matches everything', () => {
    expect(fuzzyMatch('', 'anything')).toBe(true)
    expect(fuzzyMatch('   ', 'anything')).toBe(true)
    expect(fuzzyMatch('', '')).toBe(true)
  })

  it('is a plain case-insensitive substring per term', () => {
    expect(fuzzyMatch('Panel', 'ReviewPanel.vue')).toBe(true)
    expect(fuzzyMatch('panel', 'ReviewPanel.vue')).toBe(true)
    expect(fuzzyMatch('xyz', 'ReviewPanel.vue')).toBe(false)
  })

  it('splits on whitespace into AND-terms, order-independent (the doc example)', () => {
    // both "test" and "iso" appear → the space is the gap
    expect(fuzzyMatch('test iso', 'tmux-test-isolation')).toBe(true)
    expect(fuzzyMatch('iso test', 'tmux-test-isolation')).toBe(true)
    // every term must hit; "zzz" does not
    expect(fuzzyMatch('test zzz', 'tmux-test-isolation')).toBe(false)
  })

  it('matches on path segments — the审核/文件 use path, not just basename', () => {
    expect(fuzzyMatch('components review', 'src/components/terminal-session/ReviewPanel.vue')).toBe(true)
    expect(fuzzyMatch('utils review', 'src/components/terminal-session/ReviewPanel.vue')).toBe(false)
  })

  it('collapses runs of whitespace and ignores leading/trailing spaces (drops v-model.trim)', () => {
    expect(fuzzyMatch('  test   iso  ', 'tmux-test-isolation')).toBe(true)
  })
})
