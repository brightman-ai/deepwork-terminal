/**
 * markdownEnhance.ts — post-mount async renderers for FilePreview's reader.
 *
 * renderMarkdown() (markdown.ts) emits lightweight PLACEHOLDERS for everything heavy; these
 * functions fill them in after the HTML is in the DOM, each lazy-loading its library ONCE so a
 * doc that uses no diagrams/math/code never pays for mermaid/graphviz/katex/highlight.js.
 * All are idempotent (a data-flag guards re-processing) so they're safe to call on every update.
 */
import type { LanguageFn } from 'highlight.js'

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// ── highlight.js (shared by the code-file view AND markdown fenced code) ──────────────
let hljsPromise: Promise<typeof import('highlight.js/lib/core').default> | null = null
export function loadHljs() {
  if (!hljsPromise) {
    hljsPromise = (async () => {
      const core = (await import('highlight.js/lib/core')).default
      const reg = async (id: string, imp: Promise<{ default: LanguageFn }>) =>
        core.registerLanguage(id, (await imp).default)
      await Promise.all([
        reg('go', import('highlight.js/lib/languages/go')),
        reg('typescript', import('highlight.js/lib/languages/typescript')),
        reg('javascript', import('highlight.js/lib/languages/javascript')),
        reg('python', import('highlight.js/lib/languages/python')),
        reg('rust', import('highlight.js/lib/languages/rust')),
        reg('json', import('highlight.js/lib/languages/json')),
        reg('yaml', import('highlight.js/lib/languages/yaml')),
        reg('ini', import('highlight.js/lib/languages/ini')),
        reg('bash', import('highlight.js/lib/languages/bash')),
        reg('xml', import('highlight.js/lib/languages/xml')),
        reg('css', import('highlight.js/lib/languages/css')),
        reg('sql', import('highlight.js/lib/languages/sql')),
        reg('markdown', import('highlight.js/lib/languages/markdown')),
        reg('dockerfile', import('highlight.js/lib/languages/dockerfile')),
        reg('diff', import('highlight.js/lib/languages/diff')),
        reg('ruby', import('highlight.js/lib/languages/ruby')),
        reg('java', import('highlight.js/lib/languages/java')),
        reg('c', import('highlight.js/lib/languages/c')),
        reg('cpp', import('highlight.js/lib/languages/cpp')),
      ])
      return core
    })()
  }
  return hljsPromise
}

// Highlight every markdown fenced-code block (pre.dw-pre > code[data-lang]) that isn't done yet.
export async function highlightCode(root: HTMLElement): Promise<void> {
  const blocks = Array.from(root.querySelectorAll<HTMLElement>('pre.dw-pre > code[data-lang]')).filter(
    (c) => c.dataset.hl !== '1',
  )
  if (!blocks.length) return
  const hljs = await loadHljs()
  for (const code of blocks) {
    code.dataset.hl = '1'
    const lang = code.dataset.lang || ''
    if (lang && hljs.getLanguage(lang)) {
      code.innerHTML = hljs.highlight(code.textContent || '', { language: lang, ignoreIllegals: true }).value
    }
    code.classList.add('hljs')
  }
}

// Add a "复制" button to each fenced-code block (reading agent docs = grabbing commands).
export function addCopyButtons(root: HTMLElement, onCopy: (text: string) => void): void {
  for (const pre of Array.from(root.querySelectorAll<HTMLElement>('pre.dw-pre'))) {
    if (pre.dataset.copy === '1') continue
    pre.dataset.copy = '1'
    const btn = document.createElement('button')
    btn.className = 'dw-code-copy'
    btn.type = 'button'
    btn.textContent = '复制'
    btn.addEventListener('click', (e) => {
      e.stopPropagation()
      onCopy(pre.querySelector('code')?.textContent || '')
      btn.textContent = '已复制'
      window.setTimeout(() => {
        btn.textContent = '复制'
      }, 1200)
    })
    pre.appendChild(btn)
  }
}

/** The reader's surface. A diagram brings its own colours, so it has to be told which one. */
export type DiagramTheme = 'dark' | 'light'

// ── mermaid (native JS, lazy) ─────────────────────────────────────────────────────────
let mermaidPromise: Promise<typeof import('mermaid').default> | null = null
let mermaidTheme: DiagramTheme | null = null
async function loadMermaid(theme: DiagramTheme) {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid').then((m) => m.default)
  }
  const mermaid = await mermaidPromise
  // initialize is idempotent and re-callable, so the theme follows the reader instead of being
  // frozen at first import — a dark diagram dropped onto a light page is exactly the jarring
  // thing the theming exists to avoid.
  // securityLevel:'strict' → mermaid sanitises label HTML and never eval()s, so it renders
  // under the app's strict CSP (script-src 'self').
  if (mermaidTheme !== theme) {
    mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: theme === 'light' ? 'default' : 'dark' })
    mermaidTheme = theme
  }
  return mermaid
}

// ── graphviz / dot (WASM, lazy) ───────────────────────────────────────────────────────
let graphvizPromise: Promise<{ dot(src: string): string }> | null = null
function loadGraphviz() {
  if (!graphvizPromise) {
    graphvizPromise = (async () => {
      // @hpcc-js/wasm@2.34.5 ships a `types/graphviz.d.ts` that re-exports from the (uninstalled,
      // dev-only) `@hpcc-js/wasm-graphviz` package — an upstream packaging bug, not a runtime
      // issue: the bundled dist/graphviz.js genuinely exports `Graphviz` at runtime. Narrow the
      // import to the shape we actually use instead of losing type-checking with `any`.
      const mod = (await import('@hpcc-js/wasm/graphviz')) as unknown as {
        Graphviz: { load(): Promise<{ dot(src: string): string }> }
      }
      return mod.Graphviz.load()
    })()
  }
  return graphvizPromise
}

let diagramSeq = 0
// Render every .dw-diagram placeholder (data-engine=mermaid|graphviz) to SVG. A failed diagram
// shows the error + its source rather than a blank — the doc stays readable.
export async function renderDiagrams(root: HTMLElement, theme: DiagramTheme = 'dark'): Promise<void> {
  for (const el of Array.from(root.querySelectorAll<HTMLElement>('.dw-diagram'))) {
    // Rendering REPLACES the placeholder's children, source node included — so stash the source
    // first. Without it a theme switch has nothing left to re-render from.
    const src = (el.querySelector('.dw-diagram-src')?.textContent || el.dataset.src || '').trim()
    if (!src) continue
    if (el.dataset.done === '1' && el.dataset.theme === theme) continue // already rendered, same surface
    el.dataset.done = '1'
    el.dataset.theme = theme
    el.dataset.src = src
    const engine = el.dataset.engine
    try {
      if (engine === 'mermaid') {
        const mermaid = await loadMermaid(theme)
        const { svg } = await mermaid.render(`dw-mmd-${++diagramSeq}`, src)
        el.innerHTML = svg
      } else if (engine === 'graphviz') {
        const graphviz = await loadGraphviz()
        el.innerHTML = graphviz.dot(src)
      }
      el.classList.add('dw-diagram-done')
    } catch (e) {
      el.innerHTML =
        `<div class="dw-diagram-err">图表渲染失败：${escapeHtml(String((e as Error)?.message || e))}</div>` +
        `<pre class="dw-pre"><code>${escapeHtml(src)}</code></pre>`
    }
  }
}

// ── KaTeX math (lazy) ─────────────────────────────────────────────────────────────────
let katexPromise: Promise<typeof import('katex').default> | null = null
function loadKatex() {
  if (!katexPromise) {
    katexPromise = (async () => {
      // KaTeX needs its stylesheet to POSITION the glyphs AND to visually-hide the MathML
      // source annotation — without it the rendered formula shows doubled (visual + raw TeX).
      // Vite code-splits this lazy CSS import so it only loads with the first math doc.
      await import('katex/dist/katex.min.css')
      return (await import('katex')).default
    })()
  }
  return katexPromise
}

// Render every .dw-math placeholder from its raw TeX (textContent). data-display=1 → block.
export async function renderMath(root: HTMLElement): Promise<void> {
  const nodes = Array.from(root.querySelectorAll<HTMLElement>('.dw-math')).filter((el) => el.dataset.done !== '1')
  if (!nodes.length) return
  const katex = await loadKatex()
  for (const el of nodes) {
    el.dataset.done = '1'
    const tex = el.textContent || ''
    try {
      el.innerHTML = katex.renderToString(tex, { displayMode: el.dataset.display === '1', throwOnError: false })
    } catch {
      /* leave the raw TeX visible on a parse error */
    }
  }
}
