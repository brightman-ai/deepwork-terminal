import { isSupportedPreviewName } from './previewFormats'

export interface TerminalFileLink {
  path: string
  start: number
  end: number
}

// Keep this deliberately narrower than a shell lexer. It recognizes the paths
// agents print, while leaving punctuation and prose outside the link.
// Dots are valid inside directory names (`.tg`, `.config`, `v1.0`). Omitting
// them makes the regex restart after the dot and silently drops that character
// from the activated path.
const FILE_PATH_RE = /(?:~\/|\/|\.\.?\/)?(?:[\p{L}\p{N}._@%+~,=-]+\/)*[\p{L}\p{N}._@%+~,=-]+\.[\p{L}\p{N}]{1,16}/gu
const URL_RE = /\bhttps?:\/\/[^\s<>'"`]+/gi

/** Extract supported file paths from one rendered terminal line. */
export function findTerminalFileLinks(line: string): TerminalFileLink[] {
  const links: TerminalFileLink[] = []
  const urlRanges = [...line.matchAll(URL_RE)].map((match) => ({
    start: match.index ?? 0,
    end: (match.index ?? 0) + match[0].length - 1,
  }))
  FILE_PATH_RE.lastIndex = 0
  for (let match = FILE_PATH_RE.exec(line); match; match = FILE_PATH_RE.exec(line)) {
    const path = match[0]
    const start = match.index
    // A URL provider already owns http(s) links. Do not turn the path portion
    // of `https://host/report.md` into a second, conflicting link.
    const end = start + path.length - 1
    if (urlRanges.some((url) => start >= url.start && end <= url.end)) continue
    if (!isSupportedPreviewName(path)) continue
    links.push({ path, start, end })
  }
  return links
}
