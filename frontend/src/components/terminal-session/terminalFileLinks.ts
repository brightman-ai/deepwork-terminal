import { isSupportedPreviewName } from './previewFormats'
import { hasPathElision, normalizeTerminalPath, resolveDocumentPath } from './terminalPathResolver'

export interface TerminalFileLink {
  path: string
  start: number
  end: number
  /** Visible text width when the activated link is a synthetic gutter link. */
  displayLength?: number
  /** Optional 1-based source line/range from a location suffix or file-tool annotation. */
  lineStart?: number
  lineEnd?: number
  /** Explicit Markdown target; an enclosing document edit supplies its base. */
  documentRelative?: true
  /** A clipped edit has no document base; locate the unchanged suffix, never guess cwd. */
  searchOnly?: true
}

export interface TerminalCellLike {
  chars: string
  width: number
}

export interface WrappedTerminalRow {
  text: string
  /** Continues the previous row: xterm soft wrap or a verified edit gutter. */
  isWrapped: boolean
  /** CLI layout excluded from parsing; raw text/cell positions stay intact. */
  contentStart?: number
  contentEnd?: number
}

export interface WrappedTerminalFileLink extends TerminalFileLink {
  /** Identity within the logical line, retained across physical fragments. */
  referenceStart: number
  /** Physical row inside the supplied wrapped group. */
  row: number
  /** Raw JS-text offset on that physical row. */
  rawStart: number
  /** Raw JS-text length on that physical row. */
  rawLength: number
}

export interface TerminalBufferLike {
  length: number
  getLine(index: number): {
    isWrapped: boolean
    length: number
    getCell(index: number): { getChars(): string; getWidth(): number } | undefined
  } | undefined
}

/** Read only the logical line around the hit. Empty padding cells (including the spare cell
 * before a wide glyph wraps) are layout; explicit space cells are filename/text content.
 * Never scan the whole scrollback or join independent hard-newline records. */
function softWrappedTerminalRows(buffer: TerminalBufferLike, current: number): { rows: WrappedTerminalRow[]; currentRow: number } {
  const maxRows = 64
  let first = current
  while (first > 0 && current - first + 1 < maxRows && buffer.getLine(first)?.isWrapped) first--
  let last = current
  while (last + 1 < buffer.length && last - first + 1 < maxRows && buffer.getLine(last + 1)?.isWrapped) last++
  // A clipped logical line has unknown boundaries. Do not reinterpret its tail
  // as a new basename/absolute path after scrollback eviction or the work cap.
  if (buffer.getLine(first)?.isWrapped || buffer.getLine(last + 1)?.isWrapped) return { rows: [], currentRow: 0 }
  const rows: WrappedTerminalRow[] = []
  for (let row = first; row <= last; row++) {
    const line = buffer.getLine(row)
    if (!line) break
    let end = line.length
    while (end > 0 && !line.getCell(end - 1)?.getChars()) end--
    let text = ''
    for (let col = 0; col < end; col++) {
      const cell = line.getCell(col)
      text += cell?.getChars() || (cell?.getWidth() ? ' ' : '')
    }
    rows.push({ text, isWrapped: line.isWrapped })
  }
  return { rows, currentRow: current - first }
}

function logicalRowText(row: WrappedTerminalRow): string {
  return row.text.slice(row.contentStart ?? 0, row.contentEnd)
}

function logicalRecordText(rows: readonly WrappedTerminalRow[]): string {
  return rows.map(logicalRowText).join('')
}

/** A Write result owns its final path field, including literal spaces. Its
 * continuation gutter is the tree prefix, not the assistant bullet width. */
function toolResultReference(text: string): { indent: number; start: number; value: string; gitQuoted: boolean } | undefined {
  const match = /^([ \u00a0]*[⎿└][ \u00a0]+)Wrote[ \u00a0]+\d+[ \u00a0]+lines?[ \u00a0]+to(?:[ \u00a0]+|(?=[/~.])|$)(.*)$/u.exec(text)
  if (!match) return undefined
  let value = match[2], start = text.length - value.length
  const quoted = /^(['"`]).*\1$/u.test(value)
  const gitQuoted = quoted && value[0] === '"'
  if (quoted) { value = value.slice(1, -1); start++ }
  return { indent: match[1].length, start, value, gitQuoted }
}

function mergeRenderedRows(groups: WrappedTerminalRow[][], indent: number, trimPadding = true): WrappedTerminalRow[] {
  return groups.flatMap((parts, group) => parts.map((part, index) => ({
    ...part,
    isWrapped: group > 0 || part.isWrapped,
    ...(group > 0 && index === 0 ? { contentStart: indent } : {}),
    ...(trimPadding && group < groups.length - 1 && index === parts.length - 1 ? { contentEnd: part.text.trimEnd().length } : {}),
  })))
}

/** Structural ownership for a prose reference. Quotes hide filename brackets;
 * escapes hide delimiters. This locates ranges only: the existing file parser
 * still decides whether their contents are a path, URL, template or expression. */
function referenceDelimiters(text: string): { open: number; closed: Array<{start: number; end: number}> } {
  const stack: Array<{start: number; close: string; quote: boolean}> = []
  const closed: Array<{start: number; end: number}> = []
  for (let i = 0; i < text.length; i++) {
    const ch = text[i], top = stack[stack.length - 1]
    if (ch === '\\') { i++; continue }
    if (top?.quote) {
      if (ch === top.close) { stack.pop(); closed.push({start: top.start, end: i + 1}) }
      continue
    }
    if (ch === "'" && /[\p{L}\p{N}]/u.test(text[i - 1] ?? '') && /[\p{L}\p{N}]/u.test(text[i + 1] ?? '')) continue
    if (/["'`]/.test(ch)) stack.push({start: i, close: ch, quote: true})
    else if (PATH_WRAPPERS[ch]) stack.push({start: i, close: PATH_WRAPPERS[ch], quote: false})
    else if (top?.close === ch) { stack.pop(); closed.push({start: top.start, end: i + 1}) }
  }
  return {open: stack.length, closed}
}

/** A bare reference may begin on a continuation of an assistant paragraph.
 * Find its bullet owner through the same hanging gutter, without borrowing
 * independent output, tool records or a neighbouring paragraph. */
function paragraphOwnsBareReference(buffer: TerminalBufferLike, first: number, head: string, indent: number): boolean {
  let text = head, total = 0, workLeft = 128_000
  while (total < 64) {
    workLeft -= text.length
    if (workLeft < 0 || !text.trim() || diffLineNumber(text) !== undefined
      || EDIT_HEADER_RE.test(text) || FILE_TOOL_HEADER_RE.test(text) || FILE_TOOL_RESULT_RE.test(text)) return false
    const bullet = /^ *[•●⏺*-] +/u.exec(text)
    if (bullet) return bullet[0].length === indent
    if (!text.startsWith(' '.repeat(indent)) || /^\s/u.test(text.slice(indent)) || first === 0) return false
    const prior = softWrappedTerminalRows(buffer, first - 1)
    if (!prior.rows.length) return false
    total += prior.rows.length
    first -= prior.rows.length
    text = logicalRecordText(prior.rows)
  }
  return false
}

/** A label or an assistant paragraph owns a bare reference even without a
 * wrapper. Recover its fixed-gutter continuations through a complete filename,
 * never through a second label/root/record. */
function renderedBareReferenceRows(buffer: TerminalBufferLike, current: number, original: {rows: WrappedTerminalRow[]; currentRow: number}): typeof original | undefined {
  const fileName = (path: string): string => path.slice(path.lastIndexOf('/') + 1)
  const completeFile = (path: string): boolean => SPECIAL_FILE_NAMES.has(fileName(path))
    || fileName(path)[0] !== '.' && /\.[\p{L}\p{N}]{1,16}$/u.test(fileName(path))
  let first = current - original.currentRow, probe = original.rows
  let searched = probe.length, workLeft = 128_000
  while (searched <= 64) {
    const head = logicalRecordText(probe)
    workLeft -= head.length
    if (workLeft < 0 || !head.trim() || diffLineNumber(head) !== undefined || EDIT_HEADER_RE.test(head) || EDIT_TOOL_HEADER_RE.test(head)) return undefined
    const token = /[^\s"'`|｜，。；！？、]+$/u.exec(head)
    const receipt = FILE_TOOL_RESULT_RE.exec(head)
    let receiptField = receipt ? toolResultReference(head) : undefined
    const start = receiptField?.start ?? (token ? bareFileReference(token[0], token.index).start : -1)
    const value = receiptField?.value ?? (start >= 0 ? head.slice(start) : '')
    const indent = receipt?.[1].length ?? (/^( *)(?:[•●⏺*-] +)?/u.exec(head)![0].length || 2)
    const labelled = /[:：]\s*$/u.test(head.slice(0, start))
    // A relative suffix containing '/' is not a new path start if the preceding
    // record is still inside an unfinished path. Keep looking for that owner.
    const prior = first > 0 ? logicalRecordText(softWrappedTerminalRows(buffer, first - 1).rows) : ''
    const priorToken = /[^\s"'`|｜，。；！？、]+$/u.exec(prior)?.[0] ?? ''
    const suffix = !head.slice(0, start).trim() && !/^(?:\/|~\/|\.{1,2}\/)/u.test(value)
      && priorToken.includes('/') && !completeFile(priorToken)
    if (receipt || value.includes('/') && !referenceDelimiters(value).open
      && (labelled || !suffix && paragraphOwnsBareReference(buffer, first, head, indent))) {
      const opaque = NON_FILE_SCHEME_RE.test(value) || /^[A-Za-z]:[\\/]/.test(value) || /[$*?{}<>]/.test(value)
      const initial = findTerminalFileLinks(head).find(link => receipt ? receiptField && link.start === receiptField.start : link.start === start)?.path
      const initialFile = initial && completeFile(initial)
      const gutter = ' '.repeat(indent)
      const groups = [probe], cuts: number[] = []
      let next = first + probe.length, total = probe.length, length = head.length
      while (next < buffer.length && total < 64) {
        const part = softWrappedTerminalRows(buffer, next)
        const tail = logicalRecordText(part.rows)
        const body = tail.slice(indent)
        const awaitingReceiptPath = receipt && !receiptField?.value.replace(/^["'`]/u, '').trim()
        if (!part.rows.length || total + part.rows.length > 64 || !tail.startsWith(gutter) || !body || !receipt && /^\s/u.test(body)
          || /^(?:[•●⏺*-] +)/u.test(body)
          || !awaitingReceiptPath && /^(?:\/|~\/|\.{1,2}\/)/u.test(body)
          || /^[\p{L}\p{N}_.()[\]（）【】《》-]+[:：]/u.test(body)
          || diffLineNumber(tail) !== undefined || EDIT_HEADER_RE.test(tail) || EDIT_TOOL_HEADER_RE.test(tail) || FILE_TOOL_RESULT_RE.test(tail)) break
        cuts.push(length); groups.push(part.rows)
        length += body.length; total += part.rows.length; next += part.rows.length
        const rows = mergeRenderedRows(groups, indent, false), text = logicalRecordText(rows)
        workLeft -= text.length
        if (workLeft < 0) return undefined
        receiptField = receipt ? toolResultReference(text) : undefined
        const link = findTerminalFileLinks(text).find(link => link.start === (receiptField?.start ?? start))
        const path = receipt ? receiptField?.value : opaque ? text.slice(start).split(/[\s（(，。；！？、]/u, 1)[0] : link?.path
        if (path && completeFile(path) && (receipt || opaque || cuts.every(cut => start < cut && cut < link!.start + (link!.displayLength ?? link!.end - link!.start + 1)))) {
          // report.h + tml is one filename; report.md + other.md is two.
          // Only an extension continuation can extend an already complete file.
          if (initialFile && !(path.startsWith(initial) && /^[\p{L}\p{N}]+$/u.test(path.slice(initial.length)) && isSupportedPreviewName(fileName(path)))) {
            if (!path.startsWith(initial) || !/^[\p{L}\p{N}]+$/u.test(path.slice(initial.length))) break
          } else {
            const following = next < buffer.length ? logicalRecordText(softWrappedTerminalRows(buffer, next).rows) : ''
            const extension = following.startsWith(gutter) ? /^([\p{L}\p{N}]+)(?=$|[\s（(])/u.exec(following.slice(indent))?.[1] : undefined
            const endsAtBoundary = receipt || (opaque ? text.slice(start) === path : link!.start + (link!.displayLength ?? link!.end - link!.start + 1) === text.length)
            if (!(endsAtBoundary && extension && isSupportedPreviewName(fileName(path + extension)))) {
              return current < next ? {rows, currentRow: current - first} : undefined
            }
          }
        }
        if (!receipt && /\s/u.test(body)) break
      }
    }
    if (first === 0 || !/^ {2}/.test(head) || /^\s*[•●⏺*-] +/u.test(head)) return undefined
    probe = softWrappedTerminalRows(buffer, first - 1).rows
    if (!probe.length) return undefined
    searched += probe.length; first -= probe.length
  }
  return undefined
}

/** Normal assistant paragraphs also hard-wrap with a blank hanging gutter.
 * Only join when ONE explicitly delimited reference spans EVERY hard boundary.
 * Blank lines, new bullets/edit records and unindented output are boundaries.
 * Preserve actual spaces: unlike diff backgrounds, prose has no known padding. */
function renderedProseReferenceRows(buffer: TerminalBufferLike, current: number, original: {rows: WrappedTerminalRow[]; currentRow: number}): typeof original | undefined {
  let first = current - original.currentRow
  let probe = original.rows
  let searched = probe.length
  let workLeft = 128_000
  while (searched <= 64) {
    const head = logicalRecordText(probe)
    workLeft -= head.length
    if (workLeft < 0) return undefined
    if (!head.trim() || diffLineNumber(head) !== undefined || EDIT_HEADER_RE.test(head) || FILE_TOOL_RESULT_RE.test(head)) return undefined
    const prefix = /^( *)(?:[•●⏺*-] +)?/u.exec(head)![0]
    const toolHeader = FILE_TOOL_HEADER_RE.exec(head)
    if (!prefix.length && !toolHeader) return undefined
    if (referenceDelimiters(head).open) {
      const groups = [probe]
      const cuts: number[] = []
      let next = first + probe.length, total = probe.length
      let logicalLength = head.length
      let indent = prefix.length || 2
      while (next < buffer.length && total < 64) {
        const part = softWrappedTerminalRows(buffer, next)
        const tail = logicalRecordText(part.rows)
        // The title's '(' column owns its aligned continuation: Write=7,
        // Update=8, Read/Edit=6 with a bullet. Preserve spaces beyond it and
        // retain the older paragraph-gutter form; soft wraps never lose spaces.
        const toolIndent = toolHeader ? toolHeader[0].length - 1 : 0
        if (groups.length === 1 && toolHeader && tail.startsWith(' '.repeat(toolIndent))) indent = toolIndent
        if (!part.rows.length || total + part.rows.length > 64 || !tail.slice(indent).trim()
          || !tail.startsWith(' '.repeat(indent)) || /^\s*[•●⏺*-] +/u.test(tail)
          || diffLineNumber(tail) !== undefined || EDIT_HEADER_RE.test(tail) || FILE_TOOL_HEADER_RE.test(tail) || FILE_TOOL_RESULT_RE.test(tail)) break
        cuts.push(logicalLength)
        groups.push(part.rows)
        logicalLength += tail.length - indent
        next += part.rows.length
        total += part.rows.length
        const rows = mergeRenderedRows(groups, indent, false)
        const text = logicalRecordText(rows)
        workLeft -= text.length
        if (workLeft < 0) return undefined
        const delimiters = referenceDelimiters(text)
        const spansCuts = (start: number, end: number): boolean => cuts.every(cut => start < cut && cut < end)
        const owners = delimiters.closed.filter(range => spansCuts(range.start + (toolHeader ? 0 : 1), range.end - (toolHeader ? 0 : 1)))
        const links = owners.length ? findTerminalFileLinks(text) : []
        const owned = owners.some(range => {
          if (links.some(link => range.start < link.start && link.end < range.end
            && (toolHeader || spansCuts(link.start, link.start + (link.displayLength ?? link.end - link.start + 1))))) return true
          // A complete foreign/opaque target must own its continuation too;
          // otherwise its tail could become a fabricated local basename.
          const value = text.slice(range.start + 1, range.end - 1)
          return NON_FILE_SCHEME_RE.test(value) || /^[A-Za-z]:/.test(value)
            || value.includes('/') && /[$*?{}<>]/.test(value)
        })
        if (owned) return current < next ? {rows, currentRow: current - first} : undefined
        if (!delimiters.open) break
      }
    }
    if (/^\s*[•●⏺*-] +/u.test(head) || FILE_TOOL_HEADER_RE.test(head) || first === 0) return undefined
    probe = softWrappedTerminalRows(buffer, first - 1).rows
    if (!probe.length) return undefined
    searched += probe.length
    first -= probe.length
  }
  return undefined
}

/** TUI file titles can be hard-wrapped too. Only a known header opener AND a
 * complete per-file stats suffix establish ownership; never extend a finished
 * header across subsequent output. Tree continuations retain its outer indent. */
function renderedEditHeaderRows(buffer: TerminalBufferLike, current: number, original: {rows: WrappedTerminalRow[]; currentRow: number}): typeof original | undefined {
  let first = current - original.currentRow
  let probe = original.rows
  let searched = probe.length
  while (searched <= 64) {
    const text = logicalRecordText(probe)
    if (editedFileHeader(text)) return undefined
    if (EDIT_HEADER_RE.test(text) || /^\s*[└├│]\s+/.test(text)) {
      // Completed aggregate headers such as "Edited 4 files" aren't a path.
      if (EDIT_STATS_RE.test(text)) return undefined
      const indent = /^[ \t]*/.exec(text)![0].length
      const groups = [probe]
      let next = first + probe.length
      let total = probe.length
      while (next < buffer.length && total < 64) {
        const part = softWrappedTerminalRows(buffer, next)
        const tail = logicalRecordText(part.rows)
        if (!part.rows.length || total + part.rows.length > 64 || !tail.trim() || diffLineNumber(tail) !== undefined
          || EDIT_HEADER_RE.test(tail) || EDIT_TOOL_HEADER_RE.test(tail) || /^\s*[•●⏺└├│⎿]/.test(tail)
          || !tail.startsWith(' '.repeat(indent))) break
        groups.push(part.rows)
        total += part.rows.length
        next += part.rows.length
        const rows = mergeRenderedRows(groups, indent)
        if (EDIT_STATS_RE.test(tail)) {
          return current < next && editedFileHeader(logicalRecordText(rows)) ? {rows, currentRow: current - first} : undefined
        }
      }
      return undefined
    }
    if (!text.trim() || diffLineNumber(text) !== undefined || /^\s*[•●⏺⎿]/.test(text) || first === 0) return undefined
    probe = softWrappedTerminalRows(buffer, first - 1).rows
    if (!probe.length) return undefined
    searched += probe.length
    first -= probe.length
  }
  return undefined
}

/** Codex/Claude diff renderers also emit hard newlines plus a blank gutter.
 * Recover only a contiguous numbered source record, capped at 64 physical rows.
 * Remove that exact gutter (not source indentation), and renderer right padding
 * only before an accepted hard continuation. Ordinary soft-wrap spaces survive. */
export function wrappedTerminalRows(buffer: TerminalBufferLike, current: number): { rows: WrappedTerminalRow[]; currentRow: number } {
  const original = softWrappedTerminalRows(buffer, current)
  if (!original.rows.length) return original
  const header = renderedEditHeaderRows(buffer, current, original)
  if (header) return header
  const prose = renderedProseReferenceRows(buffer, current, original)
  if (prose) return prose
  const bare = renderedBareReferenceRows(buffer, current, original)
  if (bare) return bare
  const groups = [original.rows]
  let first = current - original.currentRow
  let total = original.rows.length
  const gutter = (rows: WrappedTerminalRow[]): number | undefined => {
    const match = /^([ \t]*\d+ [-+−|│·─— ])/.exec(rows[0]?.text ?? '')
    return match?.[0].length
  }
  // A hit may be on any continuation. Find its owner before removing layout.
  while (gutter(groups[0]) === undefined && first > 0 && total < 64) {
    if (!/^ {2}/.test(groups[0][0].text)) return original
    const prior = softWrappedTerminalRows(buffer, first - 1)
    if (!prior.rows.length || total + prior.rows.length > 64) return original
    first -= prior.rows.length
    total += prior.rows.length
    groups.unshift(prior.rows)
  }
  const indent = gutter(groups[0])
  if (indent === undefined) return original
  const marker = groups[0][0].text[indent - 1]
  const markedGutter = marker !== ' ' ? ' '.repeat(indent - 1) + marker : undefined
  const continues = (rows: WrappedTerminalRow[]): boolean => {
    const text = rows[0]?.text ?? ''
    return diffLineNumber(text) === undefined
      && (text.startsWith(' '.repeat(indent)) || !!markedGutter && text.startsWith(markedGutter))
      && !!text.slice(indent).trim()
  }
  if (groups.slice(1).some(rows => !continues(rows))) return original
  let next = first + total
  while (next < buffer.length && total < 64) {
    const following = softWrappedTerminalRows(buffer, next)
    if (!following.rows.length || total + following.rows.length > 64 || !continues(following.rows)) break
    groups.push(following.rows)
    total += following.rows.length
    next += following.rows.length
  }
  const rows = mergeRenderedRows(groups, indent)
  return { rows, currentRow: current - first }
}

/** Convert a JS text range to xterm cell columns (1-based, inclusive end). */
export function terminalCellRange(cells: readonly TerminalCellLike[], start: number, length: number): { start: number; end: number } {
  const cellAt = (offset: number): TerminalCellLike & { x: number } => {
    let text = 0
    for (let i = 0; i < cells.length; i++) {
      const cell = cells[i]
      const consumed = cell.chars.length || (cell.width > 0 ? 1 : 0)
      if (offset < text + consumed) return { ...cell, x: i + 1 }
      text += consumed
    }
    const last = cells[cells.length - 1]
    return { ...(last || { chars: '', width: 1 }), x: cells.length + 1 }
  }
  const first = cellAt(start)
  const last = cellAt(start + Math.max(0, length - 1))
  return { start: first.x, end: last.x + Math.max(1, last.width) - 1 }
}

// Recognize whole lexemes before interpreting paths. Restarting a path regex in
// the middle of an unknown character silently changed relative paths to absolute
// paths (e.g. a/…/b.md became /b.md). Spaces require an explicit wrapper or escape.
const FILE_TOKEN_RE = /(?:\\[ \t()]|[^\s<>"'`|｜，。；！？、])+/gu
const NON_FILE_SCHEME_RE = /^(?:https?|wss?|s?ftp|ssh|git|mailto|data|javascript|tel|urn|about|blob|vscode(?:-insiders)?):/i
const INLINE_COMMAND_RE = /^(?:python(?:\d+(?:\.\d+)*)?|node|bun|npx|npm|pnpm|yarn|deno|ruby|perl|bash|sh|zsh|cat|head|tail|sed|awk|rg|grep|git|go|cargo|rustc|tsc|uv|pip\d*|pytest|chmod|cp|mv|ls|find|test|diff|wc|curl|wget|sudo)\s/
const LOCATION_SUFFIX_RE = /^(?::(\d+)(?:(:|-)(\d+))?|#L(\d+)(?:-L(\d+))?)(?![\p{L}\p{N}_])/u
const EDIT_STATS_RE = /\(\s*\+\d+(?:\s*,?\s*[-−]\s*\d+)?\s*\)/
const EDIT_HEADER_RE = /^\s*(?:[•●]\s*)?(?:Added|Edited|Updated|Update|Wrote|Write)\s+/i
const EDIT_TOOL_HEADER_RE = /^\s*(?:[⏺●•]\s*)?(?:Update|Edit|Write)\(/i
const FILE_TOOL_HEADER_RE = /^\s*(?:[⏺●•]\s*)?(?:Read|Update|Write|Edit|Edited)\(/i
const FILE_TOOL_RESULT_RE = /^([ \u00a0]*[⎿└][ \u00a0]+)Wrote(?:[ \u00a0]|$)/u
const DIFF_LINE_NUMBER_RE = /^(?:\s*(\d+)(?=\s+[-+−|│·─—])|[ \t]{2,}(\d+)(?= {2}))/
const EDIT_SUMMARY_RE = /^\s*(?:⎿\s*)?(?:Added \d+ lines?(?:, removed \d+ lines?)?|Removed \d+ lines?)\s*$/i
const PATH_WRAPPERS: Record<string, string> = { '(': ')', '[': ']', '{': '}', '（': '）', '【': '】', '《': '》' }
const PATH_CLOSERS = new Set(Object.values(PATH_WRAPPERS))
const SPECIAL_FILE_NAMES = new Set(['Dockerfile', 'Makefile', 'LICENSE', 'README', '.gitignore', '.npmrc', '.env', '.dockerignore', '.editorconfig'])

export interface FileLocation {
  lineStart?: number
  lineEnd?: number
}

/** Explicit - / #L ranges are unambiguous. A second colon number may be a
 * column OR an agent's end line: retain the certain first line, never guess a
 * highlighted range. This also preserves :20:7 instead of rejecting the line. */
export function parseFileLocationSuffix(suffix: string): FileLocation {
  const match = LOCATION_SUFFIX_RE.exec(suffix)
  if (!match) return {}
  const lineStart = Number(match[1] ?? match[4])
  const explicitEnd = match[2] === '-' ? match[3] : match[5]
  const lineEnd = explicitEnd ? Number(explicitEnd) : lineStart
  if (!Number.isSafeInteger(lineStart) || lineStart < 1 || !Number.isSafeInteger(lineEnd) || lineEnd < lineStart) return {}
  return { lineStart, lineEnd }
}

export function editedFileHeader(line: string): string | undefined {
  if ((EDIT_HEADER_RE.test(line) && EDIT_STATS_RE.test(line)) || EDIT_TOOL_HEADER_RE.test(line)) {
    return findTerminalFileLinks(line).find((link) => !link.lineStart)?.path
  }
  // Multi-file Codex edits put each file in a tree row with its own stats.
  const tree = /^\s*[└├│]\s+/.exec(line)
  const stats = EDIT_STATS_RE.exec(line)
  if (!tree || !stats || line.slice(stats.index + stats[0].length).trim()) return undefined
  const target = line.slice(tree[0].length, stats.index).trim()
  const links = findTerminalFileLinks(target)
  return links.length === 1 && links[0].start === 0 && links[0].end === target.length - 1 ? links[0].path : undefined
}

export function diffLineNumber(line: string): number | undefined {
  const match = DIFF_LINE_NUMBER_RE.exec(line)
  if (!match) return undefined
  const value = Number(match[1] ?? match[2])
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

/** Only traverse the SAME contiguous numbered edit block, never unrelated
 * output between a number and an old file header. priorLines is nearest first. */
export function editedDiffLineLink(line: string, priorLines: readonly string[]): TerminalFileLink | undefined {
  const targetLine = diffLineNumber(line)
  const numberStart = line.search(/\d+/)
  if (targetLine === undefined || numberStart < 0) return undefined
  for (const prior of priorLines) {
    const editedPath = editedFileHeader(prior)
    if (editedPath) {
      const displayLength = String(targetLine).length
      return { path: editedPath, start: numberStart, end: numberStart + displayLength - 1,
        displayLength, lineStart: targetLine, lineEnd: targetLine }
    }
    if (!isEditBlockContinuation(prior)) break
  }
  return undefined
}

function isEditBlockContinuation(line: string): boolean {
  return diffLineNumber(line) !== undefined || EDIT_SUMMARY_RE.test(line) || /^\s*[⋮…]\s*$/.test(line)
}

/** A file header opens at the first displayed change; its numbered rows retain
 * their individual destinations. Never borrow a line from a later output block. */
function editedHeaderFirstLine(buffer: TerminalBufferLike, nextRow: number): number | undefined {
  let firstContext: number | undefined
  const firstRow = nextRow
  for (let count = 0; nextRow < buffer.length && nextRow - firstRow < 512 && count < 256; count++) {
    const group = wrappedTerminalRows(buffer, nextRow)
    if (!group.rows.length || group.currentRow !== 0) break
    const text = logicalRecordText(group.rows)
    if (!isEditBlockContinuation(text)) break
    const number = diffLineNumber(text)
    if (number !== undefined) {
      firstContext ??= number
      if (/^\s*\d+\s+[-+−]/.test(text)) return number
    }
    nextRow += group.rows.length
  }
  return firstContext
}

/** Edit headers and preceding source rows may themselves soft-wrap. Traverse
 * logical records with the same mapping used by ordinary file references. */
export function editedDiffLineLinkFromBuffer(buffer: TerminalBufferLike, current: number): TerminalFileLink | undefined {
  const group = wrappedTerminalRows(buffer, current)
  if (!group.rows.length || group.currentRow !== 0 || diffLineNumber(group.rows[0].text) === undefined) return undefined
  const priorLines: string[] = []
  let row = current - 1
  for (let count = 0; row >= 0 && current - row <= 512 && count < 256; count++) {
    const prior = wrappedTerminalRows(buffer, row)
    if (!prior.rows.length) break
    const text = logicalRecordText(prior.rows)
    priorLines.push(text)
    if (editedFileHeader(text) || !isEditBlockContinuation(text)) break
    row -= prior.currentRow + 1
  }
  return editedDiffLineLink(group.rows[0].text, priorLines)
}

function isFileReference(path: string): boolean {
  // These are expressions / foreign addresses, not local filesystem targets.
  if (!path || /[*?${}<>\r\n]/.test(path) || /^[A-Za-z][A-Za-z\d+.-]*:\/\//.test(path) || /^[A-Za-z]:[\\/]/.test(path)) return false
  if (/\\(?![ ()])/.test(path) || /:/.test(path)) return false
  // A terminal path can name a directory. A trailing slash is explicit evidence;
  // without it require a filesystem anchor, rather than turning a/b or 3/5 into
  // guessed directories. The server, not the spelling, decides the actual type.
  if (path.startsWith('//')) return false
  const anchored = /^(?:\/(?!\/)|\.{1,2}\/|~\/)/.test(path)
  if ((path.endsWith('/') && (anchored || /[\p{L}_]/u.test(path)))
    || (anchored && !/[()[\]{}（）【】《》：]/u.test(path))) {
    if (path !== '/') return true
  }
  const name = path.slice(path.lastIndexOf('/') + 1)
  return SPECIAL_FILE_NAMES.has(name) || isSupportedPreviewName(name)
    || (path.includes('/') && /\.[\p{L}\p{N}]{1,16}$/u.test(name))
}

function decodeQuotedPath(raw: string): string {
  // Git core.quotePath emits UTF-8 bytes as octal escapes inside double quotes.
  // Decode only that explicit notation; never execute shell expansions.
  try {
    return raw.replace(/(?:\\[0-7]{3})+/g, (bytes) => new TextDecoder('utf-8', { fatal: true }).decode(
      Uint8Array.from(bytes.match(/\\([0-7]{3})/g)!, (part) => parseInt(part.slice(1), 8)),
    ))
  } catch { return raw }
}

/** A prose label can end in an annotation or in the tail of one after token
 * whitespace. Only colons outside balanced wrappers separate that label from
 * its target; colons inside (来源：file.md) belong to the wrapped reference. */
function unwrappedColon(value: string): number {
  const stack: string[] = []
  for (let i = 0; i < value.length; i++) {
    const ch = value[i]
    if (ch === '\\') { i++; continue }
    if ((ch === ':' || ch === '：') && !stack.length) return i
    if (PATH_WRAPPERS[ch]) stack.push(PATH_WRAPPERS[ch])
    else if (PATH_CLOSERS.has(ch) && stack.length && stack.pop() !== ch) return -1
  }
  return -1
}

/** Sentence wrappers end the reference even when prose follows without a space.
 * Balanced brackets WITHIN a filename are content: report(final).md and (draft).md.
 * Scan complete tokens, never restart at a slash after an unsupported character. */
function bareFileReference(value: string, offset: number): { raw: string; start: number } {
  let raw = value
  let start = offset
  // Labels and wrappers can alternate: 文件：（来源：docs/a.md）. Normalize
  // them together, advancing the original offset for every consumed character.
  while (raw) {
    if (/^[:：]/u.test(raw)) { raw = raw.slice(1); start++; continue }
    if (/^[A-Za-z][A-Za-z\d+.-]*:\/\//.test(raw) || NON_FILE_SCHEME_RE.test(raw) || /^[A-Za-z]:/.test(raw)) break
    const colon = unwrappedColon(raw)
    if (colon > 0) {
      const prefix = raw.slice(0, colon)
      if (isFileReference(prefix)) {
        // ASCII numbers belong to the source location. Other colon suffixes are
        // prose unless the WHOLE value is itself a valid literal filename.
        if (!(raw[colon] === ':' && /^\d/.test(raw.slice(colon + 1))) && !isFileReference(raw)) raw = prefix
        break
      }
      // Annotation punctuation is label syntax only before an outside colon.
      // Do not recover a tail from URLs, expressions, or a directory prefix.
      if (/^[\p{L}\p{N}_.()[\]（）【】《》〔〕-]+$/u.test(prefix)) {
        raw = raw.slice(colon + 1)
        start += colon + 1
        continue
      }
    }
    if (PATH_WRAPPERS[raw[0]]) {
      const stack: string[] = []
      let closing = -1
      for (let i = 0; i < raw.length; i++) {
        if (raw[i] === '\\') { i++; continue }
        if (PATH_WRAPPERS[raw[i]]) stack.push(PATH_WRAPPERS[raw[i]])
        else if (PATH_CLOSERS.has(raw[i])) {
          if (stack.pop() !== raw[i]) break
          if (!stack.length) { closing = i; break }
        }
      }
      // A following extension/directory makes this a filename. An unclosed
      // leading wrapper is prose cut by token whitespace; quote it if literal.
      if (closing >= 0 && /^(?:\/|\.[\p{L}\p{N}_.-]*[\p{L}\p{N}_-])/u.test(raw.slice(closing + 1))) break
      raw = closing < 0 ? raw.slice(1) : raw.slice(1, closing)
      start++
      continue
    }
    break
  }
  const stack: Array<{ at: number; close: string }> = []
  const topLevelOpenings: number[] = []
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] === '\\') { i++; continue }
    if (PATH_WRAPPERS[raw[i]]) {
      if (!stack.length) topLevelOpenings.push(i)
      stack.push({at: i, close: PATH_WRAPPERS[raw[i]]})
    } else if (PATH_CLOSERS.has(raw[i]) && stack.pop()?.close !== raw[i]) { raw = raw.slice(0, i); break }
  }
  raw = raw.replace(/[;,!]+$/u, '')
  if (raw.endsWith('.') && !raw.endsWith('...')) raw = raw.slice(0, -1)
  // Agents append prose notes without a separating space: file.md（v1 到 v3…）.
  // A whitespace-delimited token can stop inside that note. Prefer a complete
  // filename (including internal brackets / directory names) when one exists;
  // otherwise an opening bracket after a complete file ends the reference.
  const location = raw.search(/(?::\d|#L\d)/)
  const path = location < 0 ? raw : raw.slice(0, location)
  const unclosed = new Set(stack.map(part => part.at))
  const noteStart = topLevelOpenings.find(i => {
    if (i >= path.length || !unclosed.has(i)) return false
    const prefix = path.slice(0, i), name = prefix.slice(prefix.lastIndexOf('/') + 1)
    return isFileReference(prefix) && (isSupportedPreviewName(name) || SPECIAL_FILE_NAMES.has(name)
      || name[0] !== '.' && /\.[\p{L}\p{N}]{1,16}$/u.test(name) && !/[()[\]{},=]/.test(prefix))
  })
  // A token can end at `.docx` INSIDE a prose note cut by whitespace. Its
  // extension cannot complete the file before that unmatched opener. Explicit
  // quoted/Markdown targets do not use this bare-token heuristic.
  if (noteStart !== undefined) raw = raw.slice(0, noteStart)
  else if (!isFileReference(path)) {
    // Later top-level notes follow the complete filename. Earlier balanced
    // groups may belong to report.md(final).txt; nested note groups cannot win.
    for (const i of topLevelOpenings.reverse()) {
      if (i >= raw.length) continue
      const prefix = raw.slice(0, i)
      const name = prefix.slice(prefix.lastIndexOf('/') + 1)
      // Unknown extensions are allowed on simple paths (.out etc.), but a
      // nested code expression ending in .strftime/.size is not file evidence.
      const fileEvidence = isSupportedPreviewName(name) || SPECIAL_FILE_NAMES.has(name) || (name[0] !== '.' && !/[()[\]{},=]/.test(prefix))
      if (fileEvidence && isFileReference(prefix)) {
        raw = raw.slice(0, i)
        break
      }
    }
  }
  return { raw, start }
}

/** Locate call wrappers without consuming their arguments. Quoted / Markdown /
 * file-tool ranges are opaque, so filename parentheses cannot close a call. */
function parenthesisRanges(line: string, owned: readonly { start: number; end: number }[]): Array<{ start: number; end: number; call: boolean }> {
  const opaque = new Map(owned.map(range => [range.start, range.end]))
  const stack: Array<{ start: number; call: boolean; close: string }> = []
  const pairs: Array<{ start: number; end: number; call: boolean }> = []
  for (let i = 0; i < line.length; i++) {
    const end = opaque.get(i)
    if (end !== undefined) { i = end - 1; continue }
    if (line[i] === '\\') { i++; continue }
    if (line[i] === '(' || line[i] === '（') stack.push({start: i, close: line[i] === '(' ? ')' : '）', call: line[i] === '(' && i > 0 && /[A-Za-z\d_$]/u.test(line[i - 1])})
    else if (line[i] === ')' || line[i] === '）') {
      const opening = stack.pop()
      if (opening?.close === line[i]) pairs.push({start: opening.start, end: i + 1, call: opening.call})
    }
  }
  return pairs.sort((a, b) => a.start - b.start)
}

/** Remove paired Markdown decoration by offsets, never by replacing text and
 * losing cell coordinates. Semantic wrappers/expressions remain opaque. */
function unownedSpans(start: number, end: number, owned: readonly { start: number; end: number }[]): Array<{ start: number; end: number }> {
  const spans: Array<{ start: number; end: number }> = []
  let cursor = start
  for (const range of owned) {
    if (range.end <= cursor || range.start >= end) continue
    if (range.start > cursor) spans.push({ start: cursor, end: range.start })
    cursor = Math.max(cursor, range.end)
  }
  if (cursor < end) spans.push({ start: cursor, end })
  return spans
}

/** Interpolations / substitutions are expressions, not evaluated paths. Own
 * their complete span so quotes INSIDE `${base + "/tail.md"}` cannot invent
 * a filesystem-root reference. Unclosed expressions own the remaining line. */
function expressionRanges(line: string): Array<{ start: number; end: number }> {
  const ranges: Array<{ start: number; end: number }> = []
  for (let start = 0; start < line.length - 1; start++) {
    // A regex literal used as an expression can contain path-looking pieces:
    // /browser|tool_search/i.test(...). Own the literal, including flags, so
    // its suffix is never recovered as a local file. A filesystem slash alone
    // is insufficient evidence: require expression position AND method syntax.
    if (line[start] === '/' && (start === 0 || /[=>(\[,!:?]$/.test(line.slice(0, start).trimEnd()))) {
      let bracket = false
      for (let end = start + 1; end < line.length; end++) {
        if (line[end] === '\\') { end++; continue }
        if (line[end] === '[') bracket = true
        else if (line[end] === ']') bracket = false
        else if (line[end] === '/' && !bracket) {
          const method = /^([dgimsuvy]*)\.[\w$]+\s*\(/.exec(line.slice(end + 1))
          if (method) { ranges.push({ start, end: end + 1 + method[1].length }); start = end + method[1].length }
          break
        }
      }
      continue
    }
    if (line[start] !== '$' || !/[{(]/.test(line[start + 1])) continue
    const stack: string[] = []
    let quote = '', end = start + 1
    for (; end < line.length; end++) {
      const ch = line[end]
      if (ch === '\\') { end++; continue }
      if (quote) { if (ch === quote) quote = ''; continue }
      if (/["'`]/.test(ch)) { quote = ch; continue }
      if ('({['.includes(ch)) stack.push(ch === '(' ? ')' : ch === '{' ? '}' : ']')
      else if (ch === stack[stack.length - 1]) { stack.pop(); if (!stack.length) { end++; break } }
    }
    ranges.push({ start, end: Math.min(end, line.length) })
    start = end - 1
  }
  return ranges
}

/** Extract whole references. A wrapper owns its contents even when unsupported,
 * so an unknown target can never expose a misleading clickable tail. */
export function findTerminalFileLinks(line: string): TerminalFileLink[] {
  const links: TerminalFileLink[] = []
  const owned: Array<{ start: number; end: number }> = expressionRanges(line)
  const add = (raw: string, start: number, outsideSuffix = '', escaped = false, gitQuoted = false, markdown = false): void => {
    const suffixStart = raw.search(/(?::\d|#L\d)/)
    const rawPath = suffixStart < 0 ? raw : raw.slice(0, suffixStart)
    const suffix = suffixStart < 0 ? outsideSuffix : raw.slice(suffixStart)
    let target = gitQuoted ? decodeQuotedPath(rawPath) : rawPath
    if (escaped) target = target.replace(/\\([ ()])/g, '$1')
    if (/^file:\/\//i.test(target)) {
      try {
        const uri = new URL(target)
        if (uri.hostname && uri.hostname !== 'localhost') return
        target = decodeURIComponent(uri.pathname)
      } catch { return }
    }
    // Git's a/ and b/ are syntax only in explicit patch headers.
    if (/^(?:diff --git |--- |\+\+\+ )/.test(line) && /^[ab]\//.test(target)) target = target.slice(2)
    const path = normalizeTerminalPath(target)
    // Bare code / measurements are not filenames. Explicit quotes and
    // Markdown/file-tool targets retain literal '=' and other legal characters.
    if (escaped && (/[=×÷]/.test(path) || (!isSupportedPreviewName(path.slice(path.lastIndexOf('/') + 1)) && /^[\w.]+\(/.test(path)))) return
    if (!isFileReference(path)) return
    const location = parseFileLocationSuffix(suffix)
    const suffixMatch = LOCATION_SUFFIX_RE.exec(suffix)
    const displayLength = rawPath.length + (suffixStart >= 0 && location.lineStart && suffixMatch ? suffixMatch[0].length : 0)
    links.push({ path, start, end: start + rawPath.length - 1,
      ...(displayLength !== path.length ? { displayLength } : {}), ...location,
      ...(markdown && !path.startsWith('/') && !path.startsWith('~/') ? { documentRelative: true as const } : {}) })
  }
  const result = toolResultReference(line)
  if (result) {
    owned.push({ start: 0, end: line.length })
    add(result.value, result.start, '', false, result.gitQuoted)
  }
  // Raw Markdown often appears in command output; labels are not target paths.
  // Excluding another '[' avoids quadratic retries on unmatched bracket output.
  for (const match of line.matchAll(/!?\[[^\[\]\r\n]*\]\(([^()\r\n]*(?:\([^()\r\n]*\)[^()\r\n]*)*)\)/gu)) {
    const start = match.index ?? 0
    if (owned.some(range => start >= range.start && start < range.end)) continue
    owned.push({ start, end: start + match[0].length })
    const value = match[1]
    const offset = match[0].indexOf('](') + 2
    if (value.startsWith('<') && value.endsWith('>')) add(value.slice(1, -1), start + offset + 1, '', false, false, true)
    else add(value, start + offset, '', false, false, true)
  }
  for (const match of line.matchAll(/\b(?:Read|Update|Write|Edit|Edited)\(([^()\r\n]*(?:\([^()\r\n]*\)[^()\r\n]*)*)\)/gu)) {
    const start = match.index ?? 0
    if (owned.some((range) => start >= range.start && start < range.end)) continue
    owned.push({ start, end: start + match[0].length })
    // Tool UI appends display metadata, not a literal filename component.
    // Recognize only a trailing annotation in this owned tool argument: dots
    // and spaces inside quoted paths / Markdown destinations stay literal.
    const annotation = /[ \t]+·[ \t]+lines?[ \t]+(\d+)(?:[ \t]*[-–][ \t]*(\d+))?[ \t]*$/i.exec(match[1])
    const value = annotation ? match[1].slice(0, annotation.index) : match[1]
    const location = annotation ? `:${annotation[1]}${annotation[2] ? `-${annotation[2]}` : ''}` : ''
    const offset = start + match[0].indexOf('(') + 1
    if (/^(['"`]).*\1$/u.test(value)) add(value.slice(1, -1), offset + 1, location, false, value[0] === '"')
    else add(value, offset, location)
  }
  // Quotes have priority over generic parentheses: Bash(... "a.md" ... "b.md")
  // contains independent references, not one parenthesized filename. Explicit
  // file-tool / Markdown wrappers above still own their complete targets.
  for (const match of line.matchAll(/(['"`])((?:\\[^\r\n]|(?!\1)[^\\\r\n])*)\1/gu)) {
    const start = match.index ?? 0
    if (owned.some((range) => start >= range.start && start < range.end)) continue
    owned.push({ start, end: start + match[0].length })
    const value = match[2]
    if (!value || /^\s|\s$/.test(value)) continue
    if (match[1] === '`') {
      const reference = /^\[Ref:\s*(.+)\]$/i.exec(value)
      if (reference) {
        add(reference[1], start + 1 + value.indexOf(reference[1]))
        continue
      }
      if (value.startsWith('> ')) {
        // An inline quotation of Markdown prose is not a single filename.
        for (const link of findTerminalFileLinks(value.slice(2))) {
          links.push({ ...link, start: start + 3 + link.start, end: start + 3 + link.end })
        }
        continue
      }
      if (INLINE_COMMAND_RE.test(value)) {
        const prefix = 'Bash('
        for (const link of findTerminalFileLinks(prefix + value + ')')) {
          links.push({ ...link, start: start + 1 + link.start - prefix.length, end: start + 1 + link.end - prefix.length })
        }
        continue
      }
      if (/\s\/\s|^[\p{L}_$][\p{L}\p{N}_.$]*\s*=/u.test(value)) continue
    }
    add(value, start + 1, line.slice(start + match[0].length), false, match[1] === '"')
  }
  // Standalone parentheses remain an explicit space-containing path convention.
  // An identifier immediately before '(' makes it a call, whose arguments keep
  // their own lexical boundaries; never make "cat docs/a.md" a filename.
  const parentheses = parenthesisRanges(line, owned)
  const calls = parentheses.filter(range => range.call)
  for (const { start, end } of parentheses) {
    if (calls.some(range => start >= range.start && start < range.end)) continue
    if (owned.some(range => start < range.end && end > range.start)) continue
    if (start > 0 && !/[\s:：(\[（【\p{Script=Han}]/u.test(line[start - 1])) continue
    const value = line.slice(start + 1, end - 1)
    if (!value || /^\s|\s$/.test(value)) continue
    const count = links.length
    const reference = /\s/.test(value) ? { raw: value, start: start + 1 } : bareFileReference(value, start + 1)
    add(reference.raw, reference.start, line.slice(end))
    if (links.length > count) owned.push({start, end})
  }
  // A template is one opaque lexeme. '<name>' within a path/URL must not
  // expose a made-up clickable basename or local absolute tail after '>'.
  for (const match of line.matchAll(/<([^\s<>/\\]+)>/gu)) {
    const opening = match.index ?? 0, closing = opening + match[0].length
    let start = opening, end = closing
    while (start > 0 && !/[\s<>"'`|]/.test(line[start - 1])) start--
    while (end < line.length && !/[\s<>"'`|]/.test(line[end])) end++
    if (start === opening && end === closing || isFileReference(match[1])) continue
    owned.push({ start, end })
  }
  const syntax: Array<{ start: number; end: number }> = []
  for (const match of line.matchAll(/(\*\*|__|~~)(?=\S)([^\r\n]*?\S)\1/gu)) {
    const start = match.index ?? 0, end = start + match[0].length
    if (/[\p{L}\p{N}_]/u.test(line[start - 1] ?? '') || /^[./]/.test(line.slice(end))) continue
    syntax.push({ start, end: start + match[1].length }, { start: end - match[1].length, end })
  }
  syntax.sort((a, b) => a.start - b.start || a.end - b.end)
  FILE_TOKEN_RE.lastIndex = 0
  for (const match of line.matchAll(FILE_TOKEN_RE)) {
    const start = match.index ?? 0
    if (owned.some(range => start < range.end && start + match[0].length > range.start)) continue
    for (const span of unownedSpans(start, start + match[0].length, syntax)) {
      // Control operators delimit unquoted command operands. Outside a call
      // they remain literal filename characters; quoted content stays whole.
      const inCall = calls.some(range => span.start > range.start && span.start < range.end)
      const text = line.slice(span.start, span.end)
      const tokens = inCall ? text.matchAll(/(?:\\.|[^;&])+/gu) : [{0: text, index: 0}]
      for (const token of tokens) {
        const option = inCall ? /^--[\w-]+=/.exec(token[0])?.[0] ?? '' : ''
        const reference = bareFileReference(token[0].slice(option.length), span.start + (token.index ?? 0) + option.length)
        add(reference.raw, reference.start, '', true)
      }
    }
  }
  return links.sort((a, b) => a.start - b.start)
}

/**
 * Parse one xterm logical line even when it is visually wrapped across rows.
 * xterm exposes continuation rows through `isWrapped`; joining those rows is
 * safer than guessing from punctuation and avoids treating a wrapped path as
 * two unrelated filenames. Physical padding is removed by wrappedTerminalRows;
 * real whitespace must survive, including spaces at the wrap boundary.
 */
export function findWrappedTerminalFileLinks(
  rows: readonly WrappedTerminalRow[],
  currentRow: number,
): WrappedTerminalFileLink[] {
  if (!rows.length || currentRow < 0 || currentRow >= rows.length) return []
  let first = currentRow
  while (first > 0 && rows[first].isWrapped) first--
  let last = currentRow
  while (last + 1 < rows.length && rows[last + 1].isWrapped) last++

  const logicalChars: Array<{ row: number; col: number }> = []
  const logical: string[] = []
  for (let row = first; row <= last; row++) {
    const text = rows[row].text
    for (let col = rows[row].contentStart ?? 0; col < (rows[row].contentEnd ?? text.length); col++) {
      logical.push(text[col])
      logicalChars.push({ row, col })
    }
  }

  const links = findTerminalFileLinks(logical.join(''))
  const out: WrappedTerminalFileLink[] = []
  for (const link of links) {
    const length = link.displayLength ?? (link.end - link.start + 1)
    const byRow = new Map<number, number[]>()
    for (let i = link.start; i < link.start + length && i < logicalChars.length; i++) {
      const pos = logicalChars[i]
      const list = byRow.get(pos.row) ?? []
      list.push(pos.col)
      byRow.set(pos.row, list)
    }
    for (const [row, cols] of byRow) {
      if (row !== currentRow || cols.length === 0) continue
      const rawStart = Math.min(...cols)
      const rawEnd = Math.max(...cols)
      out.push({
        ...link,
        referenceStart: link.start,
        row,
        rawStart,
        rawLength: rawEnd - rawStart + 1,
        start: rawStart,
        end: rawEnd,
        displayLength: rawEnd - rawStart + 1,
      })
    }
  }
  return out
}

export interface TerminalCellFileLink extends WrappedTerminalFileLink {
  bufferRow: number
  cellStart: number
  cellEnd: number
}

/** Shared hit map for native xterm links AND the mobile virtual pointer. */
export function terminalFileLinksAtRow(buffer: TerminalBufferLike, row: number): TerminalCellFileLink[] {
  const physical = buffer.getLine(row)
  if (!physical) return []
  const group = wrappedTerminalRows(buffer, row)
  let links = findWrappedTerminalFileLinks(group.rows, group.currentRow)
  const headerPath = editedFileHeader(logicalRecordText(group.rows))
  if (headerPath) {
    const line = editedHeaderFirstLine(buffer, row - group.currentRow + group.rows.length)
    if (line) links = links.map(link => link.path === headerPath && !link.lineStart ? { ...link, lineStart: line, lineEnd: line } : link)
  }
  // A wrapped body row still belongs to the same numbered source record. Reuse
  // the contiguous edit-block association used by the gutter, never a nearby
  // unrelated file mention. The printed selection spans remain unchanged.
  const numbered = diffLineNumber(group.rows[0]?.text ?? '') !== undefined
  const source = numbered ? editedDiffLineLinkFromBuffer(buffer, row - group.currentRow) : undefined
  if (numbered) links = links.flatMap((link) => {
    if (!link.documentRelative) return [link]
    if (!source || hasPathElision(source.path) || !/\.(?:md|markdown|mdx)$/i.test(source.path)) {
      // Preserve complete file suffixes as well as basenames when the source
      // header was clipped/unavailable. Search can retain directory constraints
      // and present ambiguity; a cwd-first read could open a different file.
      // Parent traversal needs the missing document base, so do not reinterpret it.
      if (link.path.split('/').includes('..') || link.path.endsWith('/')) return []
      return [{ ...link, searchOnly: true as const }]
    }
    const path = resolveDocumentPath(source.path, link.path)
    return path ? [{ ...link, path }] : []
  })
  if (source && group.currentRow === 0) links.push({ ...source, referenceStart: -1, row: group.currentRow, rawStart: source.start, rawLength: source.displayLength! })
  const cells = Array.from({ length: physical.length }, (_, x) => {
    const cell = physical.getCell(x)
    return { chars: cell?.getChars() ?? '', width: cell?.getWidth() ?? 1 }
  })
  return links.map((link) => {
    const span = terminalCellRange(cells, link.rawStart, link.rawLength)
    return { ...link, bufferRow: row, cellStart: span.start, cellEnd: span.end }
  })
}

export function terminalFileLinkAtCell(buffer: TerminalBufferLike, row: number, col: number): TerminalCellFileLink | undefined {
  return terminalFileLinksAtRow(buffer, row).find((link) => col >= link.cellStart - 1 && col < link.cellEnd)
}

/** A double-tap selects the SAME whole reference, including its soft-wrapped
 * fragments, using cell offsets rather than JS indexes into Chinese text. */
export function terminalFileSelectionAtCell(buffer: TerminalBufferLike, row: number, col: number, cols: number): {
  path: string; startCol: number; startRow: number; endCol: number; endRow: number; length: number
} | undefined {
  const hit = terminalFileLinkAtCell(buffer, row, col)
  if (!hit) return undefined
  const group = wrappedTerminalRows(buffer, row)
  const firstRow = row - group.currentRow
  const fragments = hit.referenceStart < 0 ? [hit] : group.rows.flatMap((_, offset) =>
    terminalFileLinksAtRow(buffer, firstRow + offset).filter((link) => link.referenceStart === hit.referenceStart),
  )
  const first = fragments[0], last = fragments[fragments.length - 1]
  if (!first || !last) return undefined
  return { path: hit.path, startCol: first.cellStart - 1, startRow: first.bufferRow,
    endCol: last.cellEnd - 1, endRow: last.bufferRow,
    length: (last.bufferRow - first.bufferRow) * cols + last.cellEnd - first.cellStart + 1 }
}
