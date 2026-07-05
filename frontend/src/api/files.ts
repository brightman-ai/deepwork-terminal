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
 *   GET /files/raw?session=<id>&path=<rel>  → text bytes | image bytes | {binary} | {tooLarge}
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
 * Result of GET /files/search. `truncated` is true when the walk hit a cap (too many
 * hits, or a tree larger than the server's scan budget) and stopped early — the UI
 * surfaces it so a huge cwd reads as "narrow your search", not "file doesn't exist".
 */
export interface SearchResult {
  entries: SearchEntry[]
  truncated: boolean
}

/**
 * Result of GET /files/raw. The server returns one of three body shapes; we
 * normalize them into a tagged union so the caller renders the right affordance:
 *   - { kind:'text', text }      — previewable text bytes
 *   - { kind:'image', url }      — raster image: a blob: object URL for <img> (see below)
 *   - { kind:'binary', size }    — binary file: offer download, no inline preview
 *   - { kind:'tooLarge', size }  — too large: offer download, no inline preview
 *   - { kind:'error' }           — fetch failed / not found / forbidden
 *
 * NOTE on images: /files/raw needs the X-CLI-Auth header, so a plain <img src=…> (an
 * unauthenticated GET) would 401. We instead read the body as a blob and hand back an
 * object URL. The caller MUST URL.revokeObjectURL(url) when the preview goes away.
 */
export type RawResult =
  | { kind: 'text'; text: string }
  | { kind: 'image'; url: string }
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
 * (case-insensitive), VS-Code quick-open style. Returns an empty result on an empty
 * query or any error so the caller can render an empty list without special-casing.
 */
export async function filesSearch(sessionId: string, cwd: string | undefined, q: string): Promise<SearchResult> {
  if (!sessionId || !q.trim()) return { entries: [], truncated: false }
  const { cliFetch } = useCliAuth()
  try {
    let path = withScope('/files/search', sessionId, cwd)
    path += `&q=${encodeURIComponent(q)}`
    const resp = await cliFetch(cliApi(path))
    if (!resp.ok) return { entries: [], truncated: false }
    const data = (await resp.json()) as { entries?: SearchEntry[]; truncated?: boolean }
    return { entries: data.entries ?? [], truncated: data.truncated ?? false }
  } catch {
    return { entries: [], truncated: false }
  }
}

/** One changed file in GET /git/diff (session review — read-only). */
export interface GitDiffFile {
  /** Single-letter status: M/A/D/R/? (the UI colors the row by it). */
  status: string
  /** Repo-root-relative path, forward slashes. */
  path: string
  /** Pre-rename path — set only for a rename ("old → new"). */
  orig?: string
  added: number
  deleted: number
  /** True when git couldn't render a text diff (binary change). */
  binary?: boolean
  /** Unified diff body (may be clipped — see truncated — or empty when the budget was spent). */
  diff: string
  /** True when THIS file's diff was clipped to the per-file byte cap. */
  truncated?: boolean
}

/**
 * Result of GET /git/diff. Always a valid shape so the UI renders an empty state without
 * special-casing an error status:
 *   - notGit  → cwd resolved but isn't a git work tree ("非 git 仓库")
 *   - noCwd   → no working directory could be resolved ("cwd 缺失")
 *   - truncated → the changeset hit a file-count / total-byte cap
 *   - error   → CLIENT-side only: the fetch itself failed
 */
export interface GitDiffResult {
  root: string
  files: GitDiffFile[]
  notGit: boolean
  noCwd: boolean
  truncated: boolean
  error: boolean
}

/**
 * GET /git/diff — the workbench cwd's repo changed files + per-file unified diff (whole
 * working tree: staged + unstaged + untracked). Read-only. Soft-fails to a graceful empty
 * result on any error so the caller never has to special-case a failure status.
 */
export async function gitDiff(sessionId: string, cwd?: string): Promise<GitDiffResult> {
  const empty: GitDiffResult = { root: '', files: [], notGit: false, noCwd: false, truncated: false, error: false }
  if (!sessionId) return { ...empty, noCwd: true }
  const { cliFetch } = useCliAuth()
  try {
    const resp = await cliFetch(cliApi(withScope('/git/diff', sessionId, cwd)))
    if (!resp.ok) return { ...empty, error: true }
    const data = (await resp.json()) as Partial<GitDiffResult>
    return {
      root: data.root ?? '',
      files: data.files ?? [],
      notGit: data.notGit ?? false,
      noCwd: data.noCwd ?? false,
      truncated: data.truncated ?? false,
      error: false,
    }
  } catch {
    return { ...empty, error: true }
  }
}

/**
 * GET /files/raw — fetch a file for inline preview. Distinguishes the body shapes by
 * the response Content-Type: image/* → an image (wrapped in an object URL), text/plain
 * → text, application/json → the {binary}/{tooLarge} metadata sentinels.
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
    if (ct.startsWith('image/')) {
      // Wrap the authed bytes in an object URL so <img> can render them without re-fetching.
      return { kind: 'image', url: URL.createObjectURL(await resp.blob()) }
    }
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

/**
 * GET /files/raw?…&download=1 — fetch the FULL bytes of any file (text / image / binary /
 * oversized) and save them locally. Works for formats preview can't render. /files/raw needs
 * the X-CLI-Auth header, so a bare <a download> (unauthenticated GET) would 401 — we fetch
 * authed, wrap the bytes in an object URL, and click a synthetic anchor. `name` is the suggested
 * filename. Returns true on success.
 */
export async function filesDownload(
  sessionId: string,
  relPath: string,
  name: string,
  cwd?: string,
): Promise<boolean> {
  if (!sessionId) return false
  const { cliFetch } = useCliAuth()
  try {
    let path = withScope('/files/raw', sessionId, cwd)
    if (relPath) path += `&path=${encodeURIComponent(relPath)}`
    path += '&download=1'
    const resp = await cliFetch(cliApi(path))
    if (!resp.ok) return false
    const url = URL.createObjectURL(await resp.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = name || 'download'
    document.body.appendChild(a)
    a.click()
    a.remove()
    // Revoke after the click has consumed the URL (next macrotask).
    setTimeout(() => URL.revokeObjectURL(url), 0)
    return true
  } catch {
    return false
  }
}
