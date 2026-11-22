/**
 * htmlPickerUtils — Pure & DOM-safe element inspection and structured annotation
 * builders for `.html` sandbox preview picking (REQ-TW-05).
 *
 * Designed against duck-typed `PickableElementLike` so unit tests run directly under
 * `bun test` without requiring a full browser/jsdom global.
 */

export interface ElementRectLike {
  width: number
  height: number
  top?: number
  left?: number
}

export interface PickableElementLike {
  tagName: string
  id?: string
  className?: string | { baseVal?: string }
  textContent?: string | null
  outerHTML?: string
  getAttribute?: (name: string) => string | null
  getBoundingClientRect?: () => ElementRectLike
  parentElement?: PickableElementLike | null
  children?: ArrayLike<PickableElementLike>
}

export interface PickedElementInfo {
  tag: string
  selector: string
  width: number
  height: number
  dimensions: string
  file: string
  line: number
  textSnippet: string
  domSnippet: string
  cssSummary: string
}

export interface HtmlPickerAnnotationInput {
  pick: PickedElementInfo
  note: string
  includeDom?: boolean
  includeShot?: boolean
}

const INTERNAL_CLASS_PREFIX = 'dw-picker-'
const INTERNAL_ATTR_RE = /\sdata-dw-picker(?:-[a-z0-9-]+)?="[^"]*"/gi
const IGNORED_TAGS = new Set(['html', 'head', 'meta', 'link', 'style', 'script', 'title', 'noscript'])

function readClassTokens(el: PickableElementLike): string[] {
  const raw =
    typeof el.className === 'string'
      ? el.className
      : typeof el.className?.baseVal === 'string'
        ? el.className.baseVal
        : el.getAttribute?.('class') || ''
  return raw
    .split(/\s+/)
    .map((t) => t.trim())
    .filter((t) => t.length > 0 && !t.startsWith(INTERNAL_CLASS_PREFIX))
}

function escapeCssIdent(raw: string): string {
  return raw.replace(/[^a-zA-Z0-9_-]/g, (ch) => `\\${ch}`)
}

export function isIgnorablePickerElement(el: PickableElementLike | null | undefined): boolean {
  if (!el || !el.tagName) return true
  const tag = el.tagName.toLowerCase()
  if (IGNORED_TAGS.has(tag)) return true
  if (el.getAttribute?.('data-dw-picker-ignore') === '1') return true
  return false
}

/**
 * Cross-realm DOM element resolver (`iframe.contentDocument` nodes belong to the
 * iframe's Window realm and NEVER satisfy parent `instanceof Element` / `HTMLElement`).
 * Resolves Element nodes (`nodeType === 1`) directly and Text/Comment nodes via `parentElement`.
 */
export function resolvePickableElement(target: unknown): PickableElementLike | null {
  if (!target || typeof target !== 'object') return null
  const node = target as {
    nodeType?: unknown
    tagName?: unknown
    parentElement?: unknown
  }
  if (node.nodeType === 1 && typeof node.tagName === 'string') {
    return target as PickableElementLike
  }
  if (node.parentElement && typeof node.parentElement === 'object') {
    const parent = node.parentElement as { nodeType?: unknown; tagName?: unknown }
    if (parent.nodeType === 1 && typeof parent.tagName === 'string') {
      return parent as PickableElementLike
    }
  }
  return null
}

/**
 * Build a concise, stable CSS selector for an inspected element:
 *  1. Explicit `data-pick-selector` override if present
 *  2. `tag#id` when element has a non-empty `id`
 *  3. `tag.cls1.cls2` for up to 2 semantic classes (excluding internal picker classes)
 *  4. `tag[data-testid="..."]` or `tag[name="..."]`
 *  5. `parent > tag:nth-of-type(k)` when disambiguating siblings
 */
export function buildElementSelector(el: PickableElementLike): string {
  const explicit = el.getAttribute?.('data-pick-selector')?.trim()
  if (explicit) return explicit

  const tag = (el.tagName || 'div').toLowerCase()
  const id = (el.id || el.getAttribute?.('id') || '').trim()
  if (id) {
    return `${tag}#${escapeCssIdent(id)}`
  }

  const classes = readClassTokens(el)
  if (classes.length > 0) {
    return `${tag}.${classes.slice(0, 2).map(escapeCssIdent).join('.')}`
  }

  const testId = el.getAttribute?.('data-testid')?.trim()
  if (testId) {
    return `${tag}[data-testid="${testId.replace(/"/g, '\\"')}"]`
  }

  const nameAttr = el.getAttribute?.('name')?.trim()
  if (nameAttr) {
    return `${tag}[name="${nameAttr.replace(/"/g, '\\"')}"]`
  }

  const parent = el.parentElement
  if (parent && parent.children && parent.children.length > 1) {
    let sameTagCount = 0
    let myIndex = 0
    for (let i = 0; i < parent.children.length; i++) {
      const sibling = parent.children[i]
      if (sibling && sibling.tagName?.toLowerCase() === tag) {
        sameTagCount++
        if (sibling === el) {
          myIndex = sameTagCount
        }
      }
    }
    const parentTag = (parent.tagName || '').toLowerCase()
    const prefix = parentTag && parentTag !== 'body' && parentTag !== 'html' ? `${parentTag} > ` : ''
    if (sameTagCount > 1 && myIndex > 0) {
      return `${prefix}${tag}:nth-of-type(${myIndex})`
    }
  }

  return tag
}

export function formatElementDimensions(
  tag: string,
  rect?: { width: number; height: number } | null,
): { width: number; height: number; dimensions: string } {
  const cleanTag = (tag || 'div').toLowerCase()
  const width = Math.max(0, Math.round(rect?.width ?? 0))
  const height = Math.max(0, Math.round(rect?.height ?? 0))
  const dimensions = width > 0 || height > 0 ? `${cleanTag} ${width}×${height}` : cleanTag
  return { width, height, dimensions }
}

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Best-effort 1-based source line locator matching an inspected DOM element back
 * to its opening tag in the raw `.html` source text.
 */
export function locateElementSourceLine(htmlSource: string, el: PickableElementLike): number {
  const explicitLn = Number(
    el.getAttribute?.('data-pick-ln') || el.getAttribute?.('data-source-line') || 0,
  )
  if (Number.isSafeInteger(explicitLn) && explicitLn >= 1) {
    return explicitLn
  }

  if (!htmlSource) return 1
  const lines = htmlSource.split('\n')
  const tag = (el.tagName || 'div').toLowerCase()
  const openTagRe = new RegExp(`<${escapeRegex(tag)}\\b`, 'i')

  const id = (el.id || el.getAttribute?.('id') || '').trim()
  if (id) {
    const idRe = new RegExp(`\\bid\\s*=\\s*["']${escapeRegex(id)}["']`, 'i')
    const idx = lines.findIndex((ln) => openTagRe.test(ln) && idRe.test(ln))
    if (idx >= 0) return idx + 1
    const looseIdx = lines.findIndex((ln) => idRe.test(ln))
    if (looseIdx >= 0) return looseIdx + 1
  }

  const testId = el.getAttribute?.('data-testid')?.trim()
  if (testId) {
    const tidRe = new RegExp(`data-testid\\s*=\\s*["']${escapeRegex(testId)}["']`, 'i')
    const idx = lines.findIndex((ln) => tidRe.test(ln))
    if (idx >= 0) return idx + 1
  }

  const classes = readClassTokens(el)
  const textSnippet = (el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 28)

  if (classes.length > 0 && textSnippet) {
    const clsToken = classes[0]
    for (let i = 0; i < lines.length; i++) {
      if (openTagRe.test(lines[i]) && lines[i].includes(clsToken)) {
        const windowText = lines.slice(i, Math.min(lines.length, i + 4)).join(' ')
        if (windowText.includes(textSnippet)) return i + 1
      }
    }
  }

  if (classes.length > 0) {
    const clsToken = classes[0]
    const idx = lines.findIndex((ln) => openTagRe.test(ln) && ln.includes(clsToken))
    if (idx >= 0) return idx + 1
  }

  if (textSnippet.length >= 2) {
    for (let i = 0; i < lines.length; i++) {
      if (openTagRe.test(lines[i])) {
        const windowText = lines.slice(i, Math.min(lines.length, i + 3)).join(' ')
        if (windowText.includes(textSnippet)) return i + 1
      }
    }
  }

  const anyTagIdx = lines.findIndex((ln) => openTagRe.test(ln))
  return anyTagIdx >= 0 ? anyTagIdx + 1 : 1
}

export function extractCompactDomSnippet(el: PickableElementLike, maxLen = 200): string {
  const rawOuter = (el.outerHTML || '').replace(INTERNAL_ATTR_RE, '').replace(/\s+/g, ' ').trim()
  if (rawOuter) {
    return rawOuter.length > maxLen ? `${rawOuter.slice(0, maxLen - 1)}…` : rawOuter
  }
  const tag = (el.tagName || 'div').toLowerCase()
  const text = (el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 60)
  return text ? `<${tag}>${text}</${tag}>` : `<${tag} />`
}

export function summarizeComputedStyle(
  style?: Record<string, unknown> | CSSStyleDeclaration | null,
): string {
  if (!style) return ''
  const keys = ['color', 'backgroundColor', 'fontSize', 'fontWeight', 'borderRadius', 'padding']
  const parts: string[] = []
  for (const key of keys) {
    const val = (style as Record<string, unknown>)[key]
    if (typeof val === 'string' && val.trim() && val !== '0px' && val !== 'rgba(0, 0, 0, 0)') {
      const kebab = key.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`)
      parts.push(`${kebab}: ${val.trim()}`)
    }
  }
  return parts.join('; ')
}

export function normalizeDisplayFilePath(filePath: string, cwd?: string): string {
  const cleanFile = (filePath || '').trim()
  const cleanCwd = (cwd || '').trim().replace(/\/+$/, '')
  if (cleanCwd && cleanFile.startsWith(`${cleanCwd}/`)) {
    return cleanFile.slice(cleanCwd.length + 1)
  }
  return cleanFile || 'index.html'
}

export function inspectPickedElement(
  el: PickableElementLike,
  opts: {
    file: string
    cwd?: string
    htmlSource?: string
    computedStyle?: Record<string, unknown> | CSSStyleDeclaration | null
  },
): PickedElementInfo {
  const tag = (el.tagName || 'div').toLowerCase()
  const selector = buildElementSelector(el)
  const explicitDim = el.getAttribute?.('data-pick-dim')?.trim()
  const rect = el.getBoundingClientRect?.() ?? null
  const measured = formatElementDimensions(tag, rect)
  const dimensions = explicitDim || measured.dimensions
  const file = normalizeDisplayFilePath(opts.file, opts.cwd)
  const line = locateElementSourceLine(opts.htmlSource ?? '', el)
  const textSnippet = (el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 80)
  const domSnippet = extractCompactDomSnippet(el)
  const cssSummary = summarizeComputedStyle(opts.computedStyle)

  return {
    tag,
    selector,
    width: measured.width,
    height: measured.height,
    dimensions,
    file,
    line,
    textSnippet,
    domSnippet,
    cssSummary,
  }
}

/**
 * Format compact draft text + `@file:line` chip for `↳ 入框` (`dpp-to-draft`).
 */
export function formatHtmlPickerDraft(input: HtmlPickerAnnotationInput): {
  draftText: string
  refChip: string
} {
  const { pick, note, includeDom } = input
  const refChip = `${pick.file}:${pick.line}`
  const head = `[🎯 <${pick.selector}> (${pick.dimensions})]`
  const cleanNote = (note || '').trim()
  const domTail = includeDom && pick.cssSummary ? ` [CSS: ${pick.cssSummary}]` : ''
  const draftText = [head, cleanNote].filter(Boolean).join(' ') + domTail
  return {
    draftText: draftText.trim(),
    refChip,
  }
}

/**
 * Format structured direct-send prompt for `直接发送 ↵` (`dpp-send-now`).
 * Always carries `@file:line`, CSS selector, bounding dimensions, and user note.
 */
export function formatHtmlPickerPayload(input: HtmlPickerAnnotationInput): string {
  const { pick, note, includeDom = true, includeShot = false } = input
  const cleanNote = (note || '').trim() || '请优化此元素的视觉样式、间距与交互表现'
  const lines: string[] = [
    `🎯 **网页拾取批注** \`@${pick.file}:${pick.line}\` · \`<${pick.selector}> (${pick.dimensions})\``,
    cleanNote,
  ]
  if (includeDom) {
    const details: string[] = []
    if (pick.domSnippet) details.push(`DOM: \`${pick.domSnippet}\``)
    if (pick.cssSummary) details.push(`CSS: \`${pick.cssSummary}\``)
    if (details.length > 0) {
      lines.push(details.join(' · '))
    }
  }
  if (includeShot) {
    lines.push(`[视口尺寸参考: ${pick.width}×${pick.height}px]`)
  }
  return lines.join('\n')
}
