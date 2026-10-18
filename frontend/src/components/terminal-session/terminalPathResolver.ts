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

/** Decide whether a terminal path is complete enough for a direct read. */
export function resolveTerminalPath(path: string): TerminalPathResolution {
  const normalized = normalizeTerminalPath(path)
  if (!normalized) return { kind: 'search', query: '' }
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
}

function normalizeSearchPath(value: string): string {
  return value.trim().replace(/^@/, '').replace(/\\/g, '/').replace(/(^|\/)\.\//g, '$1').replace(/\/+/g, '/').toLowerCase()
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
      const exact = !wanted || relNorm === wanted || relNorm.endsWith(`/${wanted}`)
      return {
      name: entry.name,
      rel: entry.rel,
      absPath: base ? `${base}/${entry.rel}` : entry.rel,
      exact,
      }
    })
  const exact = files.filter((entry) => entry.exact)
  return exact.length ? exact : files.map((entry) => ({ ...entry, exact: false }))
}
