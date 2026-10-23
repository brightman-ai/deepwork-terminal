/** Pure path-resolution policy for terminal file links.
 *
 * A link with no directory component is a basename, so opening it directly
 * would silently anchor it to the wrong directory. Ask the files index first.
 * Relative paths with a directory are opened directly (the common fast path),
 * but retain a search query for a 404 fallback because agents often print a
 * suffix path from a different working directory.
 */

export interface TerminalPathDirectResolution {
  kind: 'direct'
  path: string
  /** Search query used only after a direct read returns 404. */
  fallbackQuery?: string
}

export interface TerminalPathSearchResolution {
  kind: 'search'
  query: string
}

export type TerminalPathResolution = TerminalPathDirectResolution | TerminalPathSearchResolution

export function normalizeTerminalPath(path: string): string {
  return path.trim().replace(/^@(?=(?:\.\/|\/))/, '')
}

const ELISION_RE = /\.{3,}|[…⋯]+/g

export function hasPathElision(path: string): boolean {
  return /\.{3,}|[…⋯]/.test(path)
}

/** A Markdown target belongs to its source document, which may itself be cwd
 * relative. Never turn that namespace into a filesystem-root absolute path. */
export function resolveDocumentPath(sourcePath: string, reference: string): string | undefined {
  if (!reference || /^[A-Za-z][A-Za-z\d+.-]*:/.test(reference)) return undefined
  if (reference.startsWith('/') || reference.startsWith('~/')) return reference
  if (!sourcePath) return undefined
  const dir = sourcePath.slice(0, sourcePath.lastIndexOf('/') + 1)
  const joined = dir + reference
  const absolute = joined.startsWith('/')
  const out: string[] = []
  for (const part of joined.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') {
      const last = out[out.length - 1]
      if (last && last !== '..' && last !== '~') out.pop()
      else if (!absolute) out.push(part)
    } else out.push(part)
  }
  const target = (absolute ? '/' : '') + out.join('/')
  // Keep known document-relative basenames on the direct-read route, rather
  // than reinterpreting them as a cwd-wide basename search.
  return absolute || target.startsWith('../') || target.startsWith('~/') ? target : './' + target
}

/** Decide whether a terminal path is complete enough for a direct read. */
export function resolveTerminalPath(path: string, searchOnly = false): TerminalPathResolution {
  const normalized = normalizeTerminalPath(path)
  if (!normalized) return { kind: 'search', query: '' }
  if (searchOnly) return { kind: 'search', query: normalized }
  // Dots can also be a real filename. Preserve the literal fast path and only
  // recover the omitted fragments after a 404; basenames still use the index.
  if (hasPathElision(normalized)) {
    return normalized.includes('/')
      ? { kind: 'direct', path: normalized, fallbackQuery: normalized }
      : { kind: 'search', query: normalized }
  }
  const absolute = normalized.startsWith('/') || /^~\//.test(normalized) || /^[A-Za-z]:[\\/]/.test(normalized)
  const explicitRelative = normalized.startsWith('./') || normalized.startsWith('../')
  if (absolute || explicitRelative) return { kind: 'direct', path: normalized }
  if (!normalized.includes('/')) return { kind: 'search', query: normalized }
  return { kind: 'direct', path: normalized, fallbackQuery: normalized }
}

export interface TerminalPathCandidate {
  name: string
  rel: string
  absPath: string
  /** False when the search returned a containing-name approximation. */
  exact: boolean
  match: 'literal' | 'suffix' | 'elided' | 'approximate'
}

function normalizeSearchPath(value: string): string {
  return value.trim().replace(/^@/, '').replace(/\\/g, '/').replace(/(^|\/)\.\//g, '$1').replace(/\/+/g, '/')
}

/** Retrieval is deliberately broader than proof. Keep the original reference
 * separately so AND-term search cannot discard fragment order or boundaries. */
export function terminalPathSearchQuery(path: string, cwd = ''): string {
  let value = normalizeSearchPath(path)
  const base = normalizeSearchPath(cwd).replace(/\/$/, '')
  if (base && value.startsWith(base + '/')) value = value.slice(base.length + 1)
  if (!hasPathElision(value)) return value
  // An absolute reference is verified against absPath below. Its filename is a
  // scope-independent retrieval term; the index itself stays confined to cwd.
  if (value.startsWith('/') || value.startsWith('~/')) value = value.slice(value.lastIndexOf('/') + 1)
  return value.split(ELISION_RE).map((part) => part.replace(/^\/+|\/+$/g, '').trim()).filter(Boolean).join(' ')
}

function elidedPathMatches(path: string, pattern: string): boolean {
  // Dynamic programming across directory segments avoids regex backtracking.
  if (pattern.length > 2048 || path.length > 4096) return false
  const parts = pattern.split('/')
  const segments = path.split('/')
  if (parts.length > 128 || segments.length > 256) return false
  let previous = Array<boolean>(segments.length + 1).fill(!pattern.startsWith('/'))
  previous[0] = true
  for (const part of parts) {
    const next = Array<boolean>(segments.length + 1).fill(false)
    if (/^(?:\.{3,}|[…⋯]+)$/.test(part)) {
      next[0] = previous[0]
      for (let j = 1; j <= segments.length; j++) next[j] = previous[j] || next[j - 1]
    } else {
      const fragments = part.split(ELISION_RE)
      const matches = (segment: string): boolean => {
        if (fragments.length === 1) return segment === part
        if (!segment.startsWith(fragments[0])) return false
        let offset = fragments[0].length
        for (let i = 1; i < fragments.length - 1; i++) {
          const found = segment.indexOf(fragments[i], offset)
          if (found < 0) return false
          offset = found + fragments[i].length
        }
        const tail = fragments[fragments.length - 1]
        return segment.endsWith(tail) && segment.length - tail.length >= offset
      }
      for (let j = 1; j <= segments.length; j++) next[j] = previous[j - 1] && matches(segments[j - 1])
    }
    previous = next
  }
  return previous[segments.length]
}

/** Search results are files only; directories are navigation results, not previews.
 * Prefer exact suffix matches (e.g. `src/app.ts` or `app.ts`) over the server's
 * fuzzy/name containment results. If no exact suffix exists, keep approximations
 * so the caller can present an explicit chooser rather than guessing.
 */
export function filePathCandidates(
  entries: readonly { name: string; rel: string; isDir: boolean }[],
  cwd: string,
  query = '',
): TerminalPathCandidate[] {
  const base = cwd.replace(/\/+$/, '')
  const wanted = normalizeSearchPath(query)
  const files = entries
    .filter((entry) => !entry.isDir)
    .map((entry) => {
      const rel = entry.rel.replace(/\\/g, '/').replace(/^\.\//, '').replace(/^\/+/, '')
      const relNorm = normalizeSearchPath(rel)
      const absPath = base ? `${base}/${rel}` : rel
      const absolute = wanted.startsWith('/')
      const subject = absolute ? normalizeSearchPath(absPath) : relNorm
      let match: TerminalPathCandidate['match'] = 'approximate'
      if (!wanted || subject === wanted) match = 'literal'
      else if (!absolute && subject.endsWith(`/${wanted}`)) match = 'suffix'
      else if (hasPathElision(wanted) && elidedPathMatches(subject, wanted)) match = 'elided'
      return {
      name: entry.name,
      rel: entry.rel,
      absPath,
      exact: match !== 'approximate',
      match,
      }
    })
  // Literal ... files win over interpreting those characters as an omission.
  const literal = files.filter((entry) => entry.match === 'literal' || entry.match === 'suffix')
  if (literal.length) return literal
  const exact = files.filter((entry) => entry.exact)
  if (hasPathElision(wanted)) return exact
  return exact.length ? exact : files.map((entry) => ({ ...entry, exact: false }))
}
