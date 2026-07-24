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
  // error carries WHY: the HTTP status + the backend's `error` string, so the preview can
  // explain the failure ("outside the workbench root" / "not found" / …) instead of a bare
  // "预览失败". status is absent only for a client-side network/exception failure.
  | { kind: 'error'; status?: number; reason?: string }

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
    if (!resp.ok) {
      // Surface the reason: the raw handler answers a failure with { "error": "<why>" }
      // (403 path not allowed / 404 not found / 400 is a directory). Carry both the status
      // and that string up so the preview can render a human explanation.
      let reason = ''
      try { reason = ((await resp.json()) as { error?: string }).error ?? '' } catch { /* non-JSON body */ }
      return { kind: 'error', status: resp.status, reason }
    }
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
 * GET /files/raw?…&render=1 — the URL that serves an .html/.htm file as REAL html, for the
 * preview's 渲染 toggle. Returns a plain URL string (not a fetch): the client hands it to an
 * `<iframe sandbox="allow-scripts">` WITHOUT allow-same-origin, so the page runs in an opaque
 * origin; the server pairs that with a per-response CSP that forbids network egress. Both
 * halves are load-bearing — see the render branch in files.go.
 *
 * An iframe cannot carry the X-CLI-Auth header, so the auth code rides the query string
 * (authWrap accepts ?auth=; under pro's WebUI middleware the session cookie authenticates
 * this same-origin subresource and the param is simply ignored).
 */
export function filesRawRenderUrl(sessionId: string, relPath: string, cwd?: string): string {
  if (!sessionId) return ''
  const { getAuthCode } = useCliAuth()
  let path = withScope('/files/raw', sessionId, cwd)
  if (relPath) path += `&path=${encodeURIComponent(relPath)}`
  path += '&render=1'
  const code = getAuthCode()
  if (code) path += `&auth=${encodeURIComponent(code)}`
  return cliApi(path)
}

// NOTE: the 目录树 upload + download now go through the CHUNKED protocol (chunkedUpload* below)
// and the direct-URL streaming download (filesDownloadUrl below) respectively. The former
// single-shot filesUploadToDir (/paste-upload with a dir field) and blob-buffering filesDownload
// were removed — one path each, no drift. Clipboard-paste (small images) keeps its own /paste-upload
// route in clipboard_paste.go; that is a separate bounded context and intentionally NOT unified.

// ── 分片可续传上传（POST /files/upload/{init,chunk,complete,abort}, GET status）─────────────
// 为什么分片：Cloudflare quick tunnel 单次请求体约 ~100MB 上限，弱网大文件一旦失败不能从 0 重来。
// 分片=每片远小于墙 + 服务端按 uploadId 落盘记账（重启也在），失败只补缺片。uploadId 由服务端从
// cwd|dir|name|size 派生（幂等）：同一文件重传自动续传，客户端连 id 都不必持久化。complete 返回与
// 单发 paste-upload 完全相同的 {path,relPath,size,filename} 形状，下游（抽屉刷新/注入）无需分叉。
export interface ChunkInitInfo {
  uploadId: string
  /** 服务端规定的分片大小（字节）——SSOT，客户端按此切片。 */
  chunkSize: number
  totalChunks: number
  /** 已落盘的分片下标（续传：跳过这些）。 */
  received: number[]
}
/** init：登记一次上传，拿到 uploadId + chunkSize + 已收分片。413=超上限（带 limitMb）。 */
export async function chunkedUploadInit(
  sessionId: string,
  file: File,
  dir: string,
  cwd?: string,
): Promise<{ ok: true; info: ChunkInitInfo } | { ok: false; status: number; limitMb?: number }> {
  if (!sessionId) return { ok: false, status: 0 }
  const { cliFetch } = useCliAuth()
  try {
    const form = new FormData()
    form.append('session', sessionId)
    if (cwd) form.append('cwd', cwd)
    form.append('dir', dir || '.')
    form.append('name', file.name)
    form.append('size', String(file.size))
    const resp = await cliFetch(cliApi('/files/upload/init'), { method: 'POST', body: form })
    if (!resp.ok) {
      let limitMb: number | undefined
      try { limitMb = ((await resp.json()) as { limit_mb?: number }).limit_mb } catch { /* non-JSON */ }
      return { ok: false, status: resp.status, limitMb }
    }
    return { ok: true, info: (await resp.json()) as ChunkInitInfo }
  } catch {
    return { ok: false, status: 0 }
  }
}
/** chunk：上传第 index 片（RAW 字节，非 multipart——每片本就小，省一层编码）。返回是否成功。 */
export async function chunkedUploadChunk(uploadId: string, index: number, blob: Blob): Promise<boolean> {
  const { cliFetch } = useCliAuth()
  try {
    const resp = await cliFetch(
      cliApi(`/files/upload/chunk?uploadId=${encodeURIComponent(uploadId)}&index=${index}`),
      { method: 'POST', body: blob },
    )
    return resp.ok
  } catch {
    return false
  }
}
export interface ChunkCompleteResult { path: string; relPath: string; size: number; filename: string }
/** complete：所有分片就绪后重组落地。缺片时服务端返 409，返回 null（调用方续传）。 */
export async function chunkedUploadComplete(sessionId: string, uploadId: string): Promise<ChunkCompleteResult | null> {
  const { cliFetch } = useCliAuth()
  try {
    const form = new FormData()
    form.append('session', sessionId)
    form.append('uploadId', uploadId)
    const resp = await cliFetch(cliApi('/files/upload/complete'), { method: 'POST', body: form })
    if (!resp.ok) return null
    return (await resp.json()) as ChunkCompleteResult
  } catch {
    return null
  }
}
/** abort：取消并清理服务端暂存分片（best-effort，失败无副作用——24h 后服务端也会自扫）。 */
export async function chunkedUploadAbort(uploadId: string): Promise<void> {
  if (!uploadId) return
  const { cliFetch } = useCliAuth()
  try {
    const form = new FormData()
    form.append('uploadId', uploadId)
    await cliFetch(cliApi('/files/upload/abort'), { method: 'POST', body: form })
  } catch { /* best-effort */ }
}

/**
 * GET /files/raw?…&download=1 的**直链**（不经 fetch→blob，浏览器原生流式落盘）。用于树内“下载”/
 * 大文件：blob 方式（filesDownload）会把整份文件缓进 JS 内存，大文件/移动端会炸；直链交给浏览器边下边
 * 写磁盘，且服务端 download 分支走 http.ServeContent（带 Accept-Ranges）→ 浏览器可断点续传。
 * /files/raw 需鉴权，<a download> 带不了 header，故 auth 走 query（authWrap 接受 ?auth=；pro 下由
 * 同源会话 cookie 鉴权、该参数被忽略——与 filesRawRenderUrl 同法）。
 */
export function filesDownloadUrl(sessionId: string, relPath: string, cwd?: string): string {
  if (!sessionId) return ''
  const { getAuthCode } = useCliAuth()
  let path = withScope('/files/raw', sessionId, cwd)
  if (relPath) path += `&path=${encodeURIComponent(relPath)}`
  path += '&download=1'
  const code = getAuthCode()
  if (code) path += `&auth=${encodeURIComponent(code)}`
  return cliApi(path)
}

// ── 目录树 CRUD（POST /files/{mkdir,create,rename,delete}）───────────────────────────────
// session/cwd/操作数都走 POST FormData（后端 r.FormValue）；path 均相对 cwd，后端 safeResolve 守卫
// （../绝对/symlink 逃逸 → 403）。返回 {ok, status} 让 UI 区分 409 已存在 / 404 不存在 等给准确文案。
export interface FileOpResult {
  ok: boolean
  /** HTTP status (0 = 网络/异常). 409=已存在, 404=不存在, 403=越界, 400=非法路径. */
  status: number
}
async function filesPost(op: string, sessionId: string, cwd: string, fields: Record<string, string>): Promise<FileOpResult> {
  if (!sessionId) return { ok: false, status: 0 }
  const { cliFetch } = useCliAuth()
  try {
    const form = new FormData()
    form.append('session', sessionId)
    if (cwd) form.append('cwd', cwd)
    for (const [k, v] of Object.entries(fields)) form.append(k, v)
    const resp = await cliFetch(cliApi(`/files/${op}`), { method: 'POST', body: form })
    return { ok: resp.ok, status: resp.status }
  } catch {
    return { ok: false, status: 0 }
  }
}
/** 新建目录（path = 相对 cwd 的新目录路径）。 */
export const filesMkdir = (sessionId: string, path: string, cwd?: string): Promise<FileOpResult> =>
  filesPost('mkdir', sessionId, cwd || '', { path })
/** 新建空文件。 */
export const filesCreate = (sessionId: string, path: string, cwd?: string): Promise<FileOpResult> =>
  filesPost('create', sessionId, cwd || '', { path })
/** 改名/移动（from、to 均相对 cwd）。 */
export const filesRename = (sessionId: string, from: string, to: string, cwd?: string): Promise<FileOpResult> =>
  filesPost('rename', sessionId, cwd || '', { from, to })
/** 删除（文件或目录，目录递归；后端禁删 cwd 根）。 */
export const filesDelete = (sessionId: string, path: string, cwd?: string): Promise<FileOpResult> =>
  filesPost('delete', sessionId, cwd || '', { path })

// ── 上传限额（服务端可配，全局设置）───────────────────────────────────────────────────────
// 服务端是 SSOT：GET 读当前 + 边界，PUT 设置（后端 clamp 到 [floorMb, ceilingMb]）。前端只做入口。
export interface UploadLimitInfo {
  maxMb: number
  defaultMb: number
  ceilingMb: number
  floorMb: number
}
export async function getUploadLimit(): Promise<UploadLimitInfo | null> {
  const { cliFetch } = useCliAuth()
  try {
    const resp = await cliFetch(cliApi('/files/upload-limit'))
    if (!resp.ok) return null
    return (await resp.json()) as UploadLimitInfo
  } catch {
    return null
  }
}
/** 设置上限（MB）；返回后端 clamp 后实际生效的 MB，失败 null。 */
export async function setUploadLimit(mb: number): Promise<number | null> {
  const { cliFetch } = useCliAuth()
  try {
    const form = new FormData()
    form.append('mb', String(mb))
    const resp = await cliFetch(cliApi('/files/upload-limit'), { method: 'PUT', body: form })
    if (!resp.ok) return null
    const j = (await resp.json()) as { maxMb: number }
    return j.maxMb
  } catch {
    return null
  }
}
