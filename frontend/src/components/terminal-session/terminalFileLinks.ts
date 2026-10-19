import { isSupportedPreviewName } from './previewFormats'
import { hasPathElision, normalizeTerminalPath, resolveDocumentPath } from './terminalPathResolver'

export interface TerminalFileLink {
  path: string
  start: number
  end: number
  /** Visible text width when the activated link is a synthetic gutter link. */
  displayLength?: number
  /** Optional 1-based source line (and inclusive end line) from a `file:line` suffix. */
  lineStart?: number
  lineEnd?: number
  /** Explicit Markdown target; an enclosing document edit supplies its base. */
  documentRelative?: true
}

export interface TerminalCellLike {
  chars: string
  width: number
}

export interface WrappedTerminalRow {
  text: string
  /** xterm BufferLine.isWrapped: this physical row continues the previous row. */
  isWrapped: boolean
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
export function wrappedTerminalRows(buffer: TerminalBufferLike, current: number): { rows: WrappedTerminalRow[]; currentRow: number } {
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
const FILE_TOKEN_RE = /(?:\\[ \t()]|[^\s<>"'`|])+/gu
const LOCATION_SUFFIX_RE = /^(?::(\d+)(?:(:|-)(\d+))?|#L(\d+)(?:-L(\d+))?)(?![\p{L}\p{N}_])/u
const EDIT_STATS_RE = /\(\s*\+\d+(?:\s*[-−]\s*\d+)?\s*\)/
const EDIT_HEADER_RE = /(?:^|[•●]\s*)(?:Added|Edited|Updated|Update|Wrote|Write)\s+/i
const DIFF_LINE_NUMBER_RE = /^\s*(\d+)(?=\s+(?:[-+−|│·─—]))/
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
  if (!EDIT_HEADER_RE.test(line) || !EDIT_STATS_RE.test(line)) return undefined
  return findTerminalFileLinks(line).find((link) => !link.lineStart)?.path
}

export function diffLineNumber(line: string): number | undefined {
  const match = DIFF_LINE_NUMBER_RE.exec(line)
  if (!match) return undefined
  const value = Number(match[1])
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
    if (diffLineNumber(prior) === undefined && !/^\s*[⋮…]\s*$/.test(prior)) break
  }
  return undefined
}

/** Edit headers and preceding source rows may themselves soft-wrap. Traverse
 * logical records with the same mapping used by ordinary file references. */
export function editedDiffLineLinkFromBuffer(buffer: TerminalBufferLike, current: number): TerminalFileLink | undefined {
  const group = wrappedTerminalRows(buffer, current)
  if (!group.rows.length || group.currentRow !== 0 || diffLineNumber(group.rows[0].text) === undefined) return undefined
  const priorLines: string[] = []
  let row = current - 1
  for (let count = 0; row >= 0 && count < 24; count++) {
    const prior = wrappedTerminalRows(buffer, row)
    if (!prior.rows.length) break
    const text = prior.rows.map((part) => part.text).join('')
    priorLines.push(text)
    if (editedFileHeader(text) || diffLineNumber(text) === undefined) break
    row -= prior.currentRow + 1
  }
  return editedDiffLineLink(group.rows[0].text, priorLines)
}

function isFileReference(path: string): boolean {
  // These are expressions / foreign addresses, not local filesystem targets.
  if (!path || /[*?${}<>\r\n]/.test(path) || /^[A-Za-z][A-Za-z\d+.-]*:\/\//.test(path) || /^[A-Za-z]:[\\/]/.test(path)) return false
  if (/\\(?![ ()])/.test(path) || /:/.test(path)) return false
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

/** Extract whole references. A wrapper owns its contents even when unsupported,
 * so an unknown target can never expose a misleading clickable tail. */
export function findTerminalFileLinks(line: string): TerminalFileLink[] {
  const links: TerminalFileLink[] = []
  const owned: Array<{ start: number; end: number }> = []
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
    if (!isFileReference(path)) return
    const location = parseFileLocationSuffix(suffix)
    const suffixMatch = LOCATION_SUFFIX_RE.exec(suffix)
    const displayLength = rawPath.length + (suffixStart >= 0 && location.lineStart && suffixMatch ? suffixMatch[0].length : 0)
    links.push({ path, start, end: start + rawPath.length - 1,
      ...(displayLength !== path.length ? { displayLength } : {}), ...location,
      ...(markdown && !path.startsWith('/') && !path.startsWith('~/') ? { documentRelative: true as const } : {}) })
  }
  // Raw Markdown often appears in command output; labels are not target paths.
  // Excluding another '[' avoids quadratic retries on unmatched bracket output.
  for (const match of line.matchAll(/!?\[[^\[\]\r\n]*\]\(([^()\r\n]*(?:\([^()\r\n]*\)[^()\r\n]*)*)\)/gu)) {
    const start = match.index ?? 0
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
    const value = match[1]
    const offset = start + match[0].indexOf('(') + 1
    if (/^(['"`]).*\1$/u.test(value)) add(value.slice(1, -1), offset + 1, '', false, value[0] === '"')
    else add(value, offset)
  }
  // Explicitly delimited paths preserve spaces. Parentheses are an existing CLI
  // convention; internal parentheses without spaces are handled as whole tokens.
  for (const match of line.matchAll(/(['"`])((?:\\[^\r\n]|(?!\1)[^\\\r\n])*)\1|\(([^()\r\n]*\s[^()\r\n]*)\)/gu)) {
    const start = match.index ?? 0
    if (owned.some((range) => start >= range.start && start < range.end)) continue
    owned.push({ start, end: start + match[0].length })
    const value = match[2] ?? match[3]
    if (!value || /^\s|\s$/.test(value)) continue
    add(value, start + 1, line.slice(start + match[0].length), false, match[1] === '"')
  }
  FILE_TOKEN_RE.lastIndex = 0
  for (const match of line.matchAll(FILE_TOKEN_RE)) {
    let start = match.index ?? 0
    if (owned.some((range) => start < range.end && start + match[0].length > range.start)) continue
    let raw = match[0]
    raw = raw.replace(/[;,!。，；]+$/u, '')
    const wrappers: Record<string, string> = { '(': ')', '[': ']', '{': '}', '（': '）', '【': '】' }
    while (wrappers[raw[0]] && raw.endsWith(wrappers[raw[0]])) {
      raw = raw.slice(1, -1)
      start++
    }
    // A sentence's final period is outside the filename, not an extension.
    if (raw.endsWith('.') && !raw.endsWith('...')) raw = raw.slice(0, -1)
    add(raw, start, '', true)
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
    for (let col = 0; col < text.length; col++) {
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
  // A wrapped body row still belongs to the same numbered source record. Reuse
  // the contiguous edit-block association used by the gutter, never a nearby
  // unrelated file mention. The printed selection spans remain unchanged.
  const numbered = diffLineNumber(group.rows[0]?.text ?? '') !== undefined
  const source = numbered ? editedDiffLineLinkFromBuffer(buffer, row - group.currentRow) : undefined
  if (numbered) links = links.flatMap((link) => {
    if (!link.documentRelative) return [link]
    if (!source || hasPathElision(source.path) || !/\.(?:md|markdown|mdx)$/i.test(source.path)) return []
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
