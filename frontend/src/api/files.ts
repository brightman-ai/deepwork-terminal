/**
 * Files API client — SESSION-SCOPED browsing of an agent's working tree (CHG-016).
 *
 * Unlike /uploads (cross-session, opaque-id), these endpoints are anchored to ONE
 * session's cwd (server resolves session→cwd; the client only ever sends a relative
 * path, never an absolute one — traversal is rejected server-side). They power the
 * drawer's 文件 panel: 最近文件 (transcript tool_use signal) + 目录树 (single level).
 *
 *   GET /files/recent?session=<id>          → { items: RecentFileItem[] }
 *   GET /files/tree?session=<id>&path=<rel> → { cwd, rel, entries: TreeEntry[] }
 *   GET /files/raw?session=<id>&path=<rel>  → text bytes | {binary} | {tooLarge}
 *
 * Auth: same authed cliFetch (X-CLI-Auth) the rest of the terminal API uses.
 */
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

/** One entry of GET /files/recent — a file an agent recently wrote/edited. */
export interface RecentFileItem {
  /** Absolute on-disk path (the @-referenceable path for inject). */
  path: string
  name: string
  /** Parent directory (absolute or display form, as the server emits). */
  dir: string
  /** Originating agent tool: 'claude' | 'codex' | '' (defensive — may be empty). */
  tool: string
  tsMs: number
  size: number
  exists: boolean
}

/** One child of GET /files/tree (single directory level under the session cwd). */
export interface TreeEntry {
  name: string
  isDir: boolean
  size: number
  mtimeMs: number
}

export interface TreeResponse {
  cwd: string
  /** Normalized cwd-relative path of the listed directory ('.' = root). */
  rel: string
  entries: TreeEntry[]
}

/**
 * One hit of GET /files/search — a TreeEntry plus `rel`, the path RELATIVE to the
 * search root (forward slashes). Files and directories both appear; `isDir` tells
 * the UI whether to preview (file) or navigate (dir).
 */
export interface SearchEntry {
  name: string
  /** Path relative to the search cwd, forward slashes (e.g. 'src/deep/widget.go'). */
  rel: string
  isDir: boolean
  size: number
  mtimeMs: number
}

/**
 * Result of GET /files/raw. The server returns one of three body shapes; we
 * normalize them into a tagged union so the caller renders the right affordance:
 *   - { kind:'text', text }      — previewable text bytes
 *   - { kind:'binary', size }    — binary file: offer download, no inline preview
 *   - { kind:'tooLarge', size }  — >1MiB: offer download, no inline preview
 *   - { kind:'error' }           — fetch failed / not found / forbidden
 */
export type RawResult =
  | { kind: 'text'; text: string }
  | { kind: 'binary'; size: number }
  | { kind: 'tooLarge'; size: number }
  | { kind: 'error' }

// withScope builds the session + (optional) live-cwd query prefix. `cwd` is the active
// tmux pane's working directory; supplying it makes the server anchor to that pane so the
// panel follows pane/window switches instead of the session's creation cwd.
function withScope(path: string, sessionId: string, cwd?: string): string {
  const sep = path.includes('?') ? '&' : '?'
  let q = `${path}${sep}session=${encodeURIComponent(sessionId)}`
  if (cwd) q += `&cwd=${encodeURIComponent(cwd)}`
  return q
}

/** GET /files/recent — files agents recently wrote/edited, newest first (≤30). */
export async function filesRecent(sessionId: string, cwd?: string): Promise<RecentFileItem[]> {
  if (!sessionId) return []
  const { cliFetch } = useCliAuth()
  try {
    const resp = await cliFetch(cliApi(withScope('/files/recent', sessionId, cwd)))
    if (!resp.ok) return []
    const data = (await resp.json()) as { items?: RecentFileItem[] }
    return data.items ?? []
  } catch {
    return []
  }
}

/** GET /files/tree — one directory level under the session cwd (dirs first). */
export async function filesTree(sessionId: string, relPath: string, cwd?: string): Promise<TreeResponse | null> {
  if (!sessionId) return null
  const { cliFetch } = useCliAuth()
  try {
    let path = withScope('/files/tree', sessionId, cwd)
    if (relPath) path += `&path=${encodeURIComponent(relPath)}`
    const resp = await cliFetch(cliApi(path))
    if (!resp.ok) return null
    return (await resp.json()) as TreeResponse
  } catch {
    return null
  }
}

/**
 * GET /files/search — recursively find files/dirs under cwd whose NAME contains q
 * (case-insensitive), VS-Code quick-open style. Returns [] on an empty query or any
 * error so the caller can render an empty list without special-casing.
 */
export async function filesSearch(sessionId: string, cwd: string | undefined, q: string): Promise<SearchEntry[]> {
  if (!sessionId || !q.trim()) return []
  const { cliFetch } = useCliAuth()
  try {
    let path = withScope('/files/search', sessionId, cwd)
    path += `&q=${encodeURIComponent(q)}`
    const resp = await cliFetch(cliApi(path))
    if (!resp.ok) return []
    const data = (await resp.json()) as { entries?: SearchEntry[] }
    return data.entries ?? []
  } catch {
    return []
  }
}

/**
 * GET /files/raw — fetch a file for inline preview. Distinguishes text vs
 * binary/tooLarge by inspecting the response Content-Type: the server streams
 * text/* with the file's content-type, but returns application/json for the
 * {binary}/{tooLarge} metadata sentinels.
 */
export async function filesRaw(sessionId: string, relPath: string, cwd?: string): Promise<RawResult> {
  if (!sessionId) return { kind: 'error' }
  const { cliFetch } = useCliAuth()
  try {
    let path = withScope('/files/raw', sessionId, cwd)
    if (relPath) path += `&path=${encodeURIComponent(relPath)}`
    const resp = await cliFetch(cliApi(path))
    if (!resp.ok) return { kind: 'error' }
    const ct = resp.headers.get('Content-Type') || ''
    if (ct.includes('application/json')) {
      const meta = (await resp.json()) as { binary?: boolean; tooLarge?: boolean; size?: number }
      if (meta.tooLarge) return { kind: 'tooLarge', size: meta.size ?? 0 }
      if (meta.binary) return { kind: 'binary', size: meta.size ?? 0 }
      // A JSON-typed text file (e.g. a real *.json) is still text we want to show.
      return { kind: 'text', text: JSON.stringify(meta, null, 2) }
    }
    return { kind: 'text', text: await resp.text() }
  } catch {
    return { kind: 'error' }
  }
}
