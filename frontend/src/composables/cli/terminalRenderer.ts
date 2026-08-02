/**
 * terminalRenderer — which renderer does THIS terminal get, and can the user override it?
 *
 * ── What went wrong ──────────────────────────────────────────────────────────────────────────
 * WebGL was made unconditional (v0.8.0) on a measured desktop win: 3.0× fewer layouts and 3.45×
 * fewer style recalcs than the DOM renderer for the same byte stream. What shipped with it was a
 * default with no way out — `enableWebglRenderer` was called for every terminal, and the only way
 * back to the DOM renderer was for WebGL to FAIL.
 *
 * Then mobile broke. Observed on iOS (Human's screenshot, 2026-08-02): whole lines rendered as
 * fragments of unrelated CJK glyphs — 'e' drawn as 'ョ', 's' as '，', "ba" collapsed into a single
 * wide '稲' — while the lines around them were perfect. The garbled lines were exactly the ones
 * containing a character outside the configured font stack (an em-dash, a curly apostrophe): the
 * browser resolves those by FONT FALLBACK, the fallback glyph gets rasterised into the WebGL
 * texture atlas, and from then on the Latin lookups on that line resolve to the wrong atlas
 * coordinates and paint pieces of that CJK glyph instead. It is an atlas indexing failure, not a
 * missing font — a missing font draws tofu boxes, not other people's glyphs.
 *
 * ── The rule ─────────────────────────────────────────────────────────────────────────────────
 * Mobile defaults to the DOM renderer, desktop keeps WebGL.
 *
 * Not a blanket downgrade, because the two ends of that choice are NOT symmetric:
 *   · mobile — the defect is reproduced and visible; the win is smallest (a phone-sized grid is a
 *     fraction of the cells a desktop repaints), so WebGL is buying the least and costing the most.
 *   · desktop — the win is measured, and no corruption has been reported or reproduced there.
 * Defaulting the whole app to DOM would trade a confirmed win for an unconfirmed risk.
 *
 * And whichever way the default goes, `?renderer=dom` / `?renderer=webgl` overrides it, sticky per
 * tab. That is the part whose absence turned a rendering bug into a dead end: it is how the desktop
 * half gets ANSWERED rather than argued about — open the same terminal both ways and compare.
 */

const QUERY_KEY = 'renderer'
const STORAGE_KEY = 'cli_renderer'

export type RendererKind = 'webgl' | 'dom'

/** 'webgl' / 'dom' force it · null follow the default. Sticky per tab so a reload keeps it. */
export function rendererOverride(): RendererKind | null {
  if (typeof window === 'undefined') return null
  try {
    const q = new URLSearchParams(window.location.search).get(QUERY_KEY)
    if (q === 'webgl' || q === 'dom') {
      window.sessionStorage.setItem(STORAGE_KEY, q)
      return q
    }
    const stored = window.sessionStorage.getItem(STORAGE_KEY)
    return stored === 'webgl' || stored === 'dom' ? stored : null
  } catch {
    return null
  }
}

/**
 * The rule, pure so it is testable and so the reasoning lives in one place.
 *
 * An explicit override always wins — including "give me WebGL on this phone", because the person
 * asking for that is trying to reproduce the bug, and a switch that refuses in the case you care
 * about is not a switch.
 */
export function resolveRenderer(opts: {
  override: RendererKind | null
  isMobile: boolean
}): RendererKind {
  if (opts.override) return opts.override
  return opts.isMobile ? 'dom' : 'webgl'
}

/** Why this terminal is NOT on WebGL, for the About panel. '' when it is (or when WebGL failed,
 *  which has its own reason). Phrased for a user, not a log. */
export function rendererDeclineReason(opts: {
  override: RendererKind | null
  isMobile: boolean
}): string {
  if (resolveRenderer(opts) === 'webgl') return ''
  if (opts.override === 'dom') return '按 ?renderer=dom 指定'
  return '移动端默认（WebGL 字形图集在移动浏览器上会串字）'
}
