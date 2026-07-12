/**
 * markdown.ts — the read-only markdown rendering pipeline behind FilePreview's reader.
 *
 * `renderMarkdown(src)` turns markdown into { html, toc }. Everything EXPENSIVE (mermaid /
 * graphviz diagrams, KaTeX math, highlight.js syntax colouring) is emitted as a lightweight
 * PLACEHOLDER and rendered post-mount by the component — three payoffs:
 *   · the heavy libs stay lazy-loaded (only fetched when a doc actually uses them),
 *   · DOMPurify only ever sanitises plain marked output (no KaTeX tag/style soup to whitelist,
 *     no library-generated SVG to scrub), and
 *   · a parse stays synchronous + cheap even for a long doc.
 *
 * The pipeline also collects a TOC (heading id + text + depth) in the same pass so the reader's
 * outline + scroll-spy anchor to the exact ids rendered into the HTML.
 */
import { Marked, type Tokens } from 'marked'
import DOMPurify from 'dompurify'

export interface TocItem {
  id: string
  text: string
  depth: number
}
export interface RenderedMarkdown {
  html: string
  toc: TocItem[]
}

// Fenced-code langs that are DIAGRAMS, not code — routed to a post-mount renderer.
const DIAGRAM_LANGS: Record<string, 'mermaid' | 'graphviz'> = {
  mermaid: 'mermaid',
  dot: 'graphviz',
  graphviz: 'graphviz',
  gv: 'graphviz',
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

// slugify a heading into an id-safe anchor. Keeps CJK (these docs are Chinese) — only
// punctuation is dropped. `used` de-dupes repeated headings (…-1, …-2) so every anchor is unique.
function slugify(text: string, used: Map<string, number>): string {
  const base =
    text
      .trim()
      .toLowerCase()
      .replace(/[^\p{L}\p{N}\s-]/gu, '')
      .replace(/\s+/g, '-')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '') || 'section'
  const n = used.get(base) ?? 0
  used.set(base, n + 1)
  return n === 0 ? base : `${base}-${n}`
}

/**
 * renderMarkdown parses `src` to sanitised HTML + a flat TOC. A FRESH Marked instance (and
 * fresh toc/slug state) per call keeps concurrent/repeated renders isolated — no shared mutable
 * renderer state to race.
 */
export function renderMarkdown(src: string): RenderedMarkdown {
  const toc: TocItem[] = []
  const usedSlugs = new Map<string, number>()
  const marked = new Marked({ gfm: true, breaks: false })

  marked.use({
    extensions: [
      // Block math: $$ … $$ → placeholder; KaTeX renders it post-mount from textContent.
      {
        name: 'mathBlock',
        level: 'block',
        start(src: string) {
          return src.indexOf('$$')
        },
        tokenizer(src: string) {
          const m = /^\$\$([\s\S]+?)\$\$/.exec(src)
          if (m) return { type: 'mathBlock', raw: m[0], text: m[1].trim() }
          return undefined
        },
        renderer(token) {
          return `<div class="dw-math" data-display="1">${escapeHtml((token as unknown as { text: string }).text)}</div>`
        },
      },
      // Inline math: $ … $ (not $$, no newline). The \\\$ allowance lets an escaped dollar sit
      // inside a formula without ending it.
      {
        name: 'mathInline',
        level: 'inline',
        start(src: string) {
          return src.indexOf('$')
        },
        tokenizer(src: string) {
          const m = /^\$(?!\$)((?:\\\$|[^$\n])+?)\$/.exec(src)
          if (m) return { type: 'mathInline', raw: m[0], text: m[1].trim() }
          return undefined
        },
        renderer(token) {
          return `<span class="dw-math" data-display="0">${escapeHtml((token as unknown as { text: string }).text)}</span>`
        },
      },
      // Obsidian-style wiki links: [[target]] or [[target|label]] → an internal link the reader
      // resolves against the doc's dir for in-app doc-to-doc navigation.
      {
        name: 'wikilink',
        level: 'inline',
        start(src: string) {
          return src.indexOf('[[')
        },
        tokenizer(src: string) {
          const m = /^\[\[([^\]|]+?)(?:\|([^\]]+?))?\]\]/.exec(src)
          if (m) return { type: 'wikilink', raw: m[0], target: m[1].trim(), label: (m[2] || m[1]).trim() }
          return undefined
        },
        renderer(token) {
          const t = token as unknown as { target: string; label: string }
          return `<a class="dw-link" data-internal="1" data-wikilink="${escapeHtml(t.target)}">${escapeHtml(t.label)}</a>`
        },
      },
    ],
    renderer: {
      heading(token: Tokens.Heading) {
        const html = this.parser.parseInline(token.tokens)
        const plain = html.replace(/<[^>]+>/g, '').trim()
        const id = slugify(plain, usedSlugs)
        toc.push({ id, text: plain, depth: token.depth })
        return `<h${token.depth} id="${id}" class="dw-h">${html}</h${token.depth}>`
      },
      code(token: Tokens.Code) {
        const lang = (token.lang || '').trim().split(/\s+/)[0].toLowerCase()
        const engine = DIAGRAM_LANGS[lang]
        if (engine) {
          // Diagram: hide the source, render the SVG post-mount from textContent.
          return `<div class="dw-diagram" data-engine="${engine}"><pre class="dw-diagram-src">${escapeHtml(token.text)}</pre></div>`
        }
        // Code: escaped now (safe, instant); highlight.js colours it + a copy button is added
        // post-mount. data-lang drives the highlighter's language pick.
        return `<pre class="dw-pre"><code class="language-${escapeHtml(lang)}" data-lang="${escapeHtml(lang)}">${escapeHtml(token.text)}</code></pre>`
      },
      link(token: Tokens.Link) {
        const href = token.href || ''
        const inner = this.parser.parseInline(token.tokens)
        if (href.startsWith('#')) {
          // In-page anchor → smooth-scroll to the heading id (handled by the reader).
          return `<a class="dw-link" data-anchor="${escapeHtml(href.slice(1))}">${inner}</a>`
        }
        const external = /^[a-z][a-z0-9+.-]*:/i.test(href) && !href.startsWith('file:')
        if (external) {
          return `<a href="${escapeHtml(href)}" target="_blank" rel="noopener noreferrer">${inner}</a>`
        }
        // Relative path / bare .md → internal doc link; the reader resolves + opens it in-app.
        return `<a class="dw-link" data-internal="1" data-href="${escapeHtml(href)}">${inner}</a>`
      },
      blockquote(token: Tokens.Blockquote) {
        const body = this.parser.parse(token.tokens)
        // GitHub/Obsidian callout: a blockquote whose first line is [!NOTE]/[!WARNING]/…
        const m = /^\s*<p>\s*\[!([a-zA-Z]+)\]\s*/.exec(body)
        if (m) {
          const type = m[1].toLowerCase()
          const cleaned = body.replace(/^\s*<p>\s*\[![a-zA-Z]+\]\s*/, '<p>')
          return `<div class="dw-callout dw-callout-${escapeHtml(type)}"><div class="dw-callout-title">${escapeHtml(type)}</div>${cleaned}</div>`
        }
        return `<blockquote>${body}</blockquote>`
      },
    },
  })

  const rawHtml = marked.parse(src, { async: false }) as string
  // Sanitise the marked output. Diagrams/math/highlight are placeholders here (their real,
  // library-generated content is injected post-mount, after sanitisation), so this only ever
  // scrubs plain markdown HTML. data-* + class are kept by default; task-list <input> is allowed.
  const html = DOMPurify.sanitize(rawHtml, { ADD_ATTR: ['target'] })
  return { html, toc }
}
