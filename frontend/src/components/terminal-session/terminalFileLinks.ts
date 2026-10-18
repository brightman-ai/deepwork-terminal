import { isSupportedPreviewName } from './previewFormats'

export interface TerminalFileLink {
  path: string
  start: number
  end: number
  /** Visible text width when the activated link is a synthetic gutter link. */
  displayLength?: number
  /** Optional 1-based source line (and inclusive end line) from a `file:line` suffix. */
  lineStart?: number
  lineEnd?: number
}

export interface TerminalCellLike {
  chars: string
  width: number
}

/** Convert a JS text range to xterm cell columns (1-based, inclusive end). */
export function terminalCellRange(cells: readonly TerminalCellLike[], start: number, length: number): { start: number; end: number } {
  const cellAt = (offset: number): TerminalCellLike & { x: number } => {
    let text = 0
    for (let i = 0; i < cells.length; i++) {
      const cell = cells[i]
      const consumed = cell.chars.length || (cell.width > 0 ? 1 : 0)
      if (offset < text + consumed || (consumed === 0 && offset === text)) return { ...cell, x: i + 1 }
      text += consumed
    }
    const last = cells[cells.length - 1]
    return { ...(last || { chars: '', width: 1 }), x: cells.length + 1 }
  }
  const first = cellAt(start)
  const last = cellAt(start + Math.max(0, length - 1))
  return { start: first.x, end: last.x + Math.max(1, last.width) - 1 }
}

// Keep this deliberately narrower than a shell lexer. It recognizes the paths
// agents print, while leaving punctuation and prose outside the link.
// Dots are valid inside directory names (`.tg`, `.config`, `v1.0`). Omitting
// them makes the regex restart after the dot and silently drops that character
// from the activated path.
const FILE_PATH_RE = /(?:~\/|\/|\.\.?\/)?(?:[\p{L}\p{N}._@%+~,=-]+\/)*[\p{L}\p{N}._@%+~,=-]+\.[\p{L}\p{N}]{1,16}/gu
const URL_RE = /\bhttps?:\/\/[^\s<>'"`]+/gi
// Agent output commonly appends a source location to a path: `foo.ts:42` or
// `foo.ts:42:47`. Keep the suffix outside the path itself so the files API still
// receives the real path, while retaining enough information for the preview to
// place the reader on the requested line.
const LOCATION_SUFFIX_RE = /^:(\d+)(?::(\d+))?(?![\p{L}\p{N}_])/u
const EDIT_STATS_RE = /\(\s*\+\d+(?:\s*[-−]\s*\d+)?\s*\)/
const DIFF_LINE_NUMBER_RE = /^\s*(\d+)(?=\s+(?:[-+−|│·─—]))/

export interface FileLocation {
  lineStart?: number
  lineEnd?: number
}

/** Parse a location suffix immediately following a path match. */
export function parseFileLocationSuffix(suffix: string): FileLocation {
  const match = LOCATION_SUFFIX_RE.exec(suffix)
  if (!match) return {}
  const lineStart = Number(match[1])
  const lineEnd = match[2] ? Number(match[2]) : lineStart
  if (!Number.isSafeInteger(lineStart) || lineStart < 1 || !Number.isSafeInteger(lineEnd) || lineEnd < lineStart) return {}
  return { lineStart, lineEnd }
}

/** Return the path from an agent "Edited … (+N -M)" header, if present. */
export function editedFileHeader(line: string): string | undefined {
  if (!EDIT_STATS_RE.test(line)) return undefined
  return findTerminalFileLinks(line).find((link) => !link.lineStart)?.path
}

/** Parse the old/new line number gutter rendered below an edit header. */
export function diffLineNumber(line: string): number | undefined {
  const match = DIFF_LINE_NUMBER_RE.exec(line)
  if (!match) return undefined
  const value = Number(match[1])
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

/**
 * Build the synthetic link for a numbered row in an edit preview. `priorLines`
 * is ordered from nearest preceding row to farthest; a blank row terminates the
 * block so an unrelated number is never attached to an old edit header.
 */
export function editedDiffLineLink(line: string, priorLines: readonly string[]): TerminalFileLink | undefined {
  const targetLine = diffLineNumber(line)
  const numberStart = line.search(/\d+/)
  if (targetLine === undefined || numberStart < 0) return undefined
  for (const prior of priorLines) {
    const editedPath = editedFileHeader(prior)
    if (editedPath) {
      const displayLength = String(targetLine).length
      return {
        path: editedPath,
        start: numberStart,
        end: numberStart + displayLength - 1,
        displayLength,
        lineStart: targetLine,
        lineEnd: targetLine,
      }
    }
    if (!prior.trim()) break
  }
  return undefined
}

/** Extract supported file paths from one rendered terminal line. */
export function findTerminalFileLinks(line: string): TerminalFileLink[] {
  const links: TerminalFileLink[] = []
  const urlRanges = [...line.matchAll(URL_RE)].map((match) => ({
    start: match.index ?? 0,
    end: (match.index ?? 0) + match[0].length - 1,
  }))
  FILE_PATH_RE.lastIndex = 0
  for (let match = FILE_PATH_RE.exec(line); match; match = FILE_PATH_RE.exec(line)) {
    const rawPath = match[0]
    const rawStart = match.index
    // Codex's `@./path` / `@/path` marker is part of the visible token, but
    // the @ is a composer hint rather than part of the filesystem path. Keep
    // it in the link range and strip it from the value handed to /files/raw.
    const atPrefix = rawPath.startsWith('@./') || rawPath.startsWith('@/')
    const path = atPrefix ? rawPath.slice(1) : rawPath
    const start = atPrefix ? rawStart + 1 : rawStart
    // A terminal output line may echo Codex's `@./path`/`@/path` reference. The composer owns
    // typed input separately; this provider only handles rendered output, so keep the @ prefix in
    // the hit range while sending the real path (without @) to the preview resolver.
    // A URL provider already owns http(s) links. Do not turn the path portion
    // of `https://host/report.md` into a second, conflicting link.
    const end = start + path.length - 1
    const rawEnd = rawStart + rawPath.length - 1
    if (urlRanges.some((url) => rawStart >= url.start && rawEnd <= url.end)) continue
    if (!isSupportedPreviewName(path)) continue
    const suffix = line.slice(end + 1)
    const location = parseFileLocationSuffix(suffix)
    const suffixMatch = LOCATION_SUFFIX_RE.exec(suffix)
    links.push({
      path,
      start: atPrefix ? rawStart : start,
      end,
      ...(atPrefix ? { displayLength: path.length + 1 } : {}),
      ...(location.lineStart && suffixMatch ? { displayLength: path.length + suffixMatch[0].length + (atPrefix ? 1 : 0) } : {}),
      ...location,
    })
  }
  return links
}
