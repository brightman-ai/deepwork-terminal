/** Retrieve with the original path first; a filename recovery is evidence for a
 * chooser, never proof that a different directory is the intended document. */
import type { SearchResult } from '@terminal/api/files'
import { filePathCandidates, terminalPathSearchQuery, type TerminalPathCandidate } from './terminalPathResolver'

export function terminalPathFilename(path: string): string {
  const name = path.trim().replace(/\\/g, '/').split('/').at(-1) || ''
  return name === '.' || name === '..' ? '' : name
}

export async function searchTerminalPathCandidates(
  reference: string,
  cwd: string,
  search: (query: string) => Promise<SearchResult>,
  signal: AbortSignal,
  onFilenameFallback?: (query: string) => void,
): Promise<{ result: SearchResult; candidates: TerminalPathCandidate[]; query: string; filenameFallback: boolean }> {
  const query = terminalPathSearchQuery(reference, cwd)
  const result = await search(query)
  signal.throwIfAborted()
  const candidates = filePathCandidates(result.entries, cwd, reference)
  const filename = terminalPathFilename(reference)
  const fallback = terminalPathSearchQuery(filename)
  if (result.error || candidates.some(c => c.exact) || !fallback || fallback === query) {
    return { result, candidates, query, filenameFallback: false }
  }
  onFilenameFallback?.(fallback)
  const byName = await search(fallback)
  signal.throwIfAborted()
  return {
    // A prior partial scan cannot establish that the original path is absent.
    result: { ...byName, incomplete: !!byName.incomplete || !!result.incomplete },
    candidates: filePathCandidates(byName.entries, cwd, filename).map(c => ({ ...c, exact: false })),
    query: fallback,
    filenameFallback: true,
  }
}
